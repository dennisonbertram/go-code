package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Harness run lifecycle event types the ACP adapter reacts to. Terminal for a
// prompt turn: run.completed / run.failed / run.cancelled.
// run.cost_limit_reached is non-terminal — the run then completes — so it is
// tracked as a flag on the outcome instead.
const (
	eventTypeRunCompleted        = "run.completed"
	eventTypeRunFailed           = "run.failed"
	eventTypeRunCancelled        = "run.cancelled"
	eventTypeRunCostLimitReached = "run.cost_limit_reached"
)

// maxResponseBodyBytes bounds how much of any non-streaming harnessd response
// body is read into memory.
const maxResponseBodyBytes = 8 * 1024 * 1024

// maxSSELineSize bounds a single SSE line; longer lines are drained and the
// event they belong to is skipped, mirroring the harnesscli SSE client.
const maxSSELineSize = 16 * 1024 * 1024

// RunsClient is a minimal stdlib HTTP/SSE client for the harnessd runs API.
// It exists so the ACP server can map one ACP session onto one go-code run
// without pulling in the harness internals.
type RunsClient struct {
	baseURL string
	apiKey  string
	// http is for bounded request/response calls (start, cancel).
	http *http.Client
	// stream is for the SSE event subscription and must have no client-level
	// timeout: runs can legitimately stream for a long time.
	stream *http.Client
}

// NewRunsClient returns a client for the given harnessd base URL. apiKey is
// sent as a Bearer token when non-empty.
func NewRunsClient(baseURL, apiKey string) *RunsClient {
	return &RunsClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
		stream:  &http.Client{},
	}
}

// do executes req with the client's credentials and bounded client.
func (c *RunsClient) do(req *http.Request) (*http.Response, error) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.http.Do(req)
}

// StartRun POSTs /v1/runs with the given prompt and returns the new run id.
func (c *RunsClient) StartRun(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return "", fmt.Errorf("encode run request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build run request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("send run request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return "", fmt.Errorf("read run response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("start run: harnessd returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return "", fmt.Errorf("decode run response: %w", err)
	}
	if created.RunID == "" {
		return "", fmt.Errorf("decode run response: missing run_id")
	}
	return created.RunID, nil
}

// CancelRun POSTs /v1/runs/{id}/cancel. The call is idempotent server-side
// for terminal runs; unknown runs surface as an error.
func (c *RunsClient) CancelRun(ctx context.Context, runID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs/"+runID+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("build cancel request: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("send cancel request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel run %s: harnessd returned %s", runID, resp.Status)
	}
	return nil
}

// terminalOutcome describes how a run ended, from the ACP adapter's view.
type terminalOutcome struct {
	eventType string // run.completed | run.failed | run.cancelled
	costLimit bool   // run.cost_limit_reached was seen before completion
	errText   string // run.failed payload error, when present
}

// WaitTerminal subscribes to GET /v1/runs/{id}/events and blocks until a
// terminal run event arrives, the stream breaks, or ctx is cancelled.
func (c *RunsClient) WaitTerminal(ctx context.Context, runID string) (terminalOutcome, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		return terminalOutcome{}, fmt.Errorf("build events request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.stream.Do(req)
	if err != nil {
		return terminalOutcome{}, fmt.Errorf("subscribe to run events: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		return terminalOutcome{}, fmt.Errorf("subscribe to run events: harnessd returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out terminalOutcome
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var block []string
	for {
		line, err := readSSELine(reader)
		if err != nil {
			if err == io.EOF {
				return terminalOutcome{}, fmt.Errorf("event stream ended before a terminal event")
			}
			return terminalOutcome{}, fmt.Errorf("read event stream: %w", err)
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			// Blank line terminates an SSE block.
			typ, payload := parseSSEBlock(block)
			block = block[:0]
			if typ == "" {
				continue
			}
			if typ == eventTypeRunCostLimitReached {
				out.costLimit = true
				continue
			}
			if typ == eventTypeRunCompleted || typ == eventTypeRunFailed || typ == eventTypeRunCancelled {
				out.eventType = typ
				out.errText = payloadError(payload)
				return out, nil
			}
			continue // non-terminal event; slice 3 translates these into session/update
		}
		block = append(block, line)
	}
}

// readSSELine reads one '\n'-terminated line (without the delimiter),
// tolerating arbitrarily long lines by draining them.
func readSSELine(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(buf)+len(frag) <= maxSSELineSize {
			buf = append(buf, frag...)
		}
		switch {
		case err == nil:
			return string(buf[:len(buf)-1]), nil
		case err == bufio.ErrBufferFull:
			continue
		case err == io.EOF && len(buf) > 0:
			return string(buf), nil
		default:
			return "", err
		}
	}
}

// parseSSEBlock extracts the event type and data payload from one SSE block.
// Comment (":") and field lines other than event:/data: are ignored.
func parseSSEBlock(lines []string) (eventType string, data string) {
	var dataLines []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, ":"):
			// keepalive comment
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	data = strings.Join(dataLines, "")
	if eventType == "" && data != "" {
		// Fall back to the type member inside the JSON payload.
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &probe) == nil {
			eventType = probe.Type
		}
	}
	return eventType, data
}

// payloadError extracts the "error" member of an event payload, if present.
func payloadError(data string) string {
	if data == "" {
		return ""
	}
	var probe struct {
		Payload struct {
			Error string `json:"error"`
		} `json:"payload"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(data), &probe) != nil {
		return ""
	}
	if probe.Payload.Error != "" {
		return probe.Payload.Error
	}
	return probe.Error
}
