package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// sseChanCap is the channel buffer depth for SSE messages. The bridge
// delivers decoded events with blocking sends (see send) so that a lagging
// TUI update loop applies natural backpressure to the HTTP scanner instead
// of silently dropping real events; the capacity below just gives bursty
// event types (e.g. many distinct tool.call.started events) headroom before
// that backpressure kicks in. tool.output.delta chunks for the same call_id
// are additionally coalesced in the scan loop below, which is what keeps
// very large tool outputs (e.g. `ls -laR` in a big repo) cheap to deliver.
const sseChanCap = 1024

// sseScannerInitialBufferBytes/sseScannerMaxBufferBytes size the bufio.Scanner
// used to read SSE lines. Without an explicit .Buffer() call, bufio.Scanner
// defaults to a 64KB max token size — comfortably exceeded by real tool
// output: merged stdout+stderr is capped at ~60KB
// (internal/harness/tools/head_tail_buffer.go: 30KB head/tail per stream),
// plus JSON escaping/envelope overhead, and a single tool.output.delta line
// can be as large as 1MB (internal/harness/tools/bash_manager.go
// defaultMaxStreamLineBytes). A single oversized line previously made
// bufio.Scanner return bufio.ErrTooLong and stop scanning permanently,
// killing the stream for the rest of the run. 4MB is comfortably above the
// 1MB server-side per-line cap.
const (
	sseScannerInitialBufferBytes = 64 * 1024
	sseScannerMaxBufferBytes     = 4 * 1024 * 1024
)

// maxCoalescedDeltaBytes bounds how much tool.output.delta content the
// bridge accumulates for a single call_id before flushing it as a message,
// so a very long-running streaming command still produces periodic updates
// (and bounded memory) rather than one giant message at the very end.
const maxCoalescedDeltaBytes = 32 * 1024

// SSEBridgeOptions configures a single SSE bridge connection attempt.
type SSEBridgeOptions struct {
	// LastEventID, if non-empty, is sent as the Last-Event-ID request header
	// so the server resumes the stream from that point (see
	// internal/server/http_runs.go, which trims already-delivered history
	// via harness.ParseEventID) instead of replaying everything from the
	// start.
	LastEventID string
	// APIKey, if non-empty, is sent as "Authorization: Bearer <APIKey>" so
	// the request authenticates the same way the rest of the harnesscli
	// client does (see cmd/harnesscli/auth.go's newAuthedRequest, which
	// sources this key from ~/.harness/config.json via "harnesscli auth
	// login"). When empty, no Authorization header is sent at all,
	// preserving today's unauthenticated-local behavior.
	APIKey string
}

// StartSSEBridge connects to the SSE endpoint at url and delivers decoded
// tea.Msg values on the returned channel. Call stop() to disconnect early.
// The channel is closed when the stream ends or ctx is cancelled.
//
// This is equivalent to StartSSEBridgeWithOptions with a zero-value
// SSEBridgeOptions (i.e. a fresh, unauthenticated connection with no resume
// point).
func StartSSEBridge(ctx context.Context, url string) (<-chan tea.Msg, func()) {
	return StartSSEBridgeWithOptions(ctx, url, SSEBridgeOptions{})
}

// StartSSEBridgeFrom is like StartSSEBridge but sets the Last-Event-ID
// request header to lastEventID (if non-empty) so the server resumes the
// stream from that point instead of replaying everything from the start.
//
// This is equivalent to StartSSEBridgeWithOptions with only LastEventID set.
func StartSSEBridgeFrom(ctx context.Context, url, lastEventID string) (<-chan tea.Msg, func()) {
	return StartSSEBridgeWithOptions(ctx, url, SSEBridgeOptions{LastEventID: lastEventID})
}

// StartSSEBridgeWithOptions is the fully-configurable entry point: see
// SSEBridgeOptions for what each field controls.
func StartSSEBridgeWithOptions(ctx context.Context, url string, opts SSEBridgeOptions) (<-chan tea.Msg, func()) {
	ch := make(chan tea.Msg, sseChanCap)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer cancel()
		defer close(ch)
		runBridge(ctx, url, opts, ch)
	}()

	return ch, cancel
}

func runBridge(ctx context.Context, url string, opts SSEBridgeOptions, ch chan<- tea.Msg) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		send(ctx, ch, SSEErrorMsg{Err: err})
		send(ctx, ch, SSEDoneMsg{EventType: "bridge.closed"})
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	if opts.LastEventID != "" {
		req.Header.Set("Last-Event-ID", opts.LastEventID)
	}
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			send(ctx, ch, SSEErrorMsg{Err: err})
			send(ctx, ch, SSEDoneMsg{EventType: "bridge.closed"})
		}
		return
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if ctx.Err() != nil {
			return
		}
		if isNonRetryableSSEStatus(resp.StatusCode) {
			// A permanent rejection (bad/missing credentials, unknown run):
			// retrying would just burn the bounded reconnect budget against
			// the same outcome every time and confuse the user with 5
			// backed-off attempts before finally giving up. Surface one
			// clear, actionable error and end the run immediately instead.
			send(ctx, ch, SSEErrorMsg{Err: nonRetryableSSEError(resp.StatusCode, body)})
			send(ctx, ch, SSEDoneMsg{EventType: "bridge.fatal"})
			return
		}
		// Anything else unexpected (5xx, unusual redirects, etc.) is treated
		// as transient — recoverable via the caller's bounded reconnect,
		// same as a dropped connection.
		send(ctx, ch, SSEErrorMsg{Err: fmt.Errorf("SSE bridge: unexpected status %d from %s", resp.StatusCode, url)})
		send(ctx, ch, SSEDoneMsg{EventType: "bridge.closed"})
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, sseScannerInitialBufferBytes), sseScannerMaxBufferBytes)

	var event, id string
	var dataParts []string

	// pendingDelta buffers consecutive tool.output.delta chunks for the same
	// call_id so a burst of thousands of tiny chunks (e.g. `ls -laR` output)
	// becomes a handful of merged messages instead of one channel send per
	// line. It is flushed whenever a different event/call_id arrives, the
	// accumulated content crosses maxCoalescedDeltaBytes, or the stream ends.
	var pendingDelta map[string]any
	var pendingCallID string
	var pendingID string

	flushPending := func() {
		if pendingDelta == nil {
			return
		}
		raw, err := json.Marshal(pendingDelta)
		merged := pendingID
		pendingDelta, pendingCallID, pendingID = nil, "", ""
		if err != nil {
			send(ctx, ch, SSEErrorMsg{Err: err})
			return
		}
		send(ctx, ch, SSEEventMsg{EventType: "tool.output.delta", Raw: raw, ID: merged})
	}

	deliver := func(msg tea.Msg) {
		if evt, ok := msg.(SSEEventMsg); ok && evt.EventType == "tool.output.delta" {
			if callID, ok := toolDeltaCallID(evt.Raw); ok {
				if pendingDelta != nil && pendingCallID != callID {
					flushPending()
				}
				pendingDelta = mergeToolOutputDelta(pendingDelta, evt.Raw)
				pendingCallID = callID
				pendingID = evt.ID
				if pendingDeltaContentLen(pendingDelta) >= maxCoalescedDeltaBytes {
					flushPending()
				}
				return
			}
		}
		flushPending()
		send(ctx, ch, msg)
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			// Per SSE spec: multiple data: lines are concatenated with "\n".
			dataParts = append(dataParts, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if len(dataParts) > 0 {
				data := strings.Join(dataParts, "\n")
				msg := decodeSSE(event, data, id)
				if _, ok := msg.(SSEDoneMsg); ok {
					flushPending()
					send(ctx, ch, msg)
					return
				}
				deliver(msg)
			}
			event, id, dataParts = "", "", nil
		}
	}
	// Flush any partial event buffered before EOF / connection drop.
	// Per SSE spec the stream should end with a blank line, but servers
	// may close the connection abruptly; deliver whatever data was pending.
	if len(dataParts) > 0 && ctx.Err() == nil {
		data := strings.Join(dataParts, "\n")
		deliver(decodeSSE(event, data, id))
	}
	flushPending()
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		if errors.Is(err, bufio.ErrTooLong) {
			send(ctx, ch, SSEErrorMsg{Err: fmt.Errorf("SSE bridge: event exceeded max buffer size (%d bytes), connection will be retried: %w", sseScannerMaxBufferBytes, err)})
		} else {
			send(ctx, ch, SSEErrorMsg{Err: err})
		}
	}
	// Signal that this connection attempt ended without a run.completed/
	// run.failed terminal event (covers normal EOF and connection drops).
	// The caller (the TUI model's SSEDoneMsg handler) treats this as
	// recoverable and reconnects using Last-Event-ID rather than treating
	// the run as finished.
	send(ctx, ch, SSEDoneMsg{EventType: "bridge.closed"})
}

// isNonRetryableSSEStatus reports whether an HTTP status on the events
// endpoint represents a permanent failure that a reconnect cannot fix:
// 401/403 mean the credentials are missing or rejected, and 404 means the
// run ID itself does not exist (or has expired) — none of these change on
// retry. Everything else (5xx, unexpected redirects, etc.) is treated as
// transient and keeps the caller's bounded reconnect behavior, since a
// network blip or a momentarily overloaded server genuinely can recover.
func isNonRetryableSSEStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}

// nonRetryableSSEError builds a single, actionable error message for a
// non-retryable SSE status so the user sees one clear explanation instead of
// a generic "stream error" (or, before this fix, a storm of them from
// burning the whole reconnect budget against the same permanent failure).
func nonRetryableSSEError(status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg := fmt.Sprintf("SSE bridge: authentication rejected (HTTP %d) — the harnessd API key is missing or invalid; run \"harnesscli auth login\" to set one", status)
		if detail != "" {
			msg += ": " + detail
		}
		return errors.New(msg)
	case http.StatusNotFound:
		msg := fmt.Sprintf("SSE bridge: run not found (HTTP %d) — the run ID is invalid or the run has expired", status)
		if detail != "" {
			msg += ": " + detail
		}
		return errors.New(msg)
	default:
		msg := fmt.Sprintf("SSE bridge: non-retryable status %d", status)
		if detail != "" {
			msg += ": " + detail
		}
		return errors.New(msg)
	}
}

type sseEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func decodeSSE(event, data, id string) tea.Msg {
	var env sseEnvelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return SSEErrorMsg{Err: err}
	}
	if env.Type == "run.completed" || env.Type == "run.failed" {
		// Extract error message from run.failed payload so the TUI can display it.
		var errMsg string
		if env.Type == "run.failed" {
			var p struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(env.Payload, &p); err == nil {
				errMsg = p.Error
			}
		}
		return SSEDoneMsg{EventType: env.Type, Error: errMsg}
	}
	// Unknown event types are forwarded as SSEEventMsg so that consumers
	// can inspect EventType and Raw. No silent discard.
	return SSEEventMsg{EventType: env.Type, Raw: env.Payload, ID: id}
}

// toolDeltaCallID extracts the call_id field from a tool.output.delta
// payload. It returns ok=false if the payload cannot be parsed or has no
// call_id, in which case the caller must not attempt to coalesce it.
func toolDeltaCallID(raw json.RawMessage) (callID string, ok bool) {
	var p struct {
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.CallID == "" {
		return "", false
	}
	return p.CallID, true
}

// mergeToolOutputDelta merges a newly decoded tool.output.delta payload into
// the pending accumulator for the same call_id, concatenating "content" and
// otherwise taking the latest value for every other field (e.g. tool,
// stream_index) so the merged message stays representative of the most
// recent chunk.
func mergeToolOutputDelta(pending map[string]any, raw json.RawMessage) map[string]any {
	var next map[string]any
	if err := json.Unmarshal(raw, &next); err != nil {
		return pending
	}
	if pending == nil {
		return next
	}
	merged := make(map[string]any, len(next))
	for k, v := range next {
		merged[k] = v
	}
	pc, pcOK := pending["content"].(string)
	nc, ncOK := next["content"].(string)
	switch {
	case pcOK && ncOK:
		merged["content"] = pc + nc
	case pcOK:
		merged["content"] = pc
	}
	return merged
}

// pendingDeltaContentLen returns the length of the accumulated "content"
// field on a pending coalesced delta, used to bound how large a single
// coalesced message is allowed to grow before being flushed.
func pendingDeltaContentLen(pending map[string]any) int {
	if pending == nil {
		return 0
	}
	if c, ok := pending["content"].(string); ok {
		return len(c)
	}
	return 0
}

func send(ctx context.Context, ch chan<- tea.Msg, msg tea.Msg) {
	select {
	case ch <- msg:
	case <-ctx.Done():
	}
}
