package main

// askuser.go — TUI #476 non-TUI mode
// handleAskUserQuestion implements stdin/stdout interaction for the non-TUI
// streaming CLI when a run.waiting_for_user event is received.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cliHTTPClient is the shared HTTP client for ask-user calls in non-TUI mode.
// A 10-second timeout prevents hanging on slow or unreachable servers.
var cliHTTPClient = &http.Client{Timeout: 10 * time.Second}

// pendingInputResponse matches the JSON returned by GET /v1/runs/{id}/input.
type pendingInputResponse struct {
	RunID      string           `json:"run_id"`
	CallID     string           `json:"call_id"`
	Tool       string           `json:"tool"`
	Questions  []cliAskQuestion `json:"questions"`
	DeadlineAt time.Time        `json:"deadline_at"`
}

// cliAskQuestion mirrors the server-side AskUserQuestion schema.
type cliAskQuestion struct {
	Question    string         `json:"question"`
	Header      string         `json:"header"`
	Options     []cliAskOption `json:"options"`
	MultiSelect bool           `json:"multiSelect"`
}

// cliAskOption mirrors the server-side AskUserQuestionOption schema.
type cliAskOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// handleAskUserQuestion fetches the pending question for the given runID,
// prints it to out, reads one answer per question from in, and POSTs the
// answers back to the server.
//
// Intended to be called when a run.waiting_for_user SSE event is received in
// non-TUI streaming mode.
func handleAskUserQuestion(baseURL, runID string, in io.Reader, out io.Writer) error {
	// Step 1: GET /v1/runs/{id}/input
	getURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/input"
	getReq, err := newAuthedRequest(context.Background(), http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("build pending input request: %w", err)
	}
	resp, err := cliHTTPClient.Do(getReq)
	if err != nil {
		return fmt.Errorf("fetch pending input: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch pending input: HTTP %d", resp.StatusCode)
	}

	var pending pendingInputResponse
	if err := json.NewDecoder(resp.Body).Decode(&pending); err != nil {
		return fmt.Errorf("decode pending input: %w", err)
	}

	if len(pending.Questions) == 0 {
		return fmt.Errorf("no questions in pending input")
	}

	// Check deadline before prompting. If already expired, return an error
	// rather than asking for input that the server will reject anyway.
	if !pending.DeadlineAt.IsZero() && time.Now().After(pending.DeadlineAt) {
		return fmt.Errorf("question deadline has already expired (deadline: %s)", pending.DeadlineAt.Format(time.RFC3339))
	}

	// Step 2: Print each question and collect answers from stdin.
	answers := make(map[string]string, len(pending.Questions))
	reader := &lineReader{r: in}

	for _, q := range pending.Questions {
		fmt.Fprintf(out, "\n─── %s ───\n", q.Header)
		fmt.Fprintf(out, "Question: %s\n", q.Question)
		fmt.Fprintln(out, "Options:")
		validLabels := make(map[string]struct{}, len(q.Options))
		for i, opt := range q.Options {
			fmt.Fprintf(out, "  %d. %s — %s\n", i+1, opt.Label, opt.Description)
			validLabels[opt.Label] = struct{}{}
		}
		fmt.Fprint(out, "Enter your choice: ")

		answer, err := reader.readLine()
		if err != nil {
			return fmt.Errorf("read answer for %q: %w", q.Question, err)
		}
		answer = strings.TrimSpace(answer)

		// Validate the answer against the option labels.
		if _, ok := validLabels[answer]; !ok {
			return fmt.Errorf("invalid option %q for question %q (valid: %s)",
				answer, q.Question, labelList(q.Options))
		}
		answers[q.Question] = answer
	}

	// Step 3: POST /v1/runs/{id}/input with the answers.
	postURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/input"
	payload := map[string]interface{}{"answers": answers}
	body, _ := json.Marshal(payload)
	postReq, err := newAuthedRequest(context.Background(), http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build submit answers request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := cliHTTPClient.Do(postReq)
	if err != nil {
		return fmt.Errorf("submit answers: %w", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode >= 300 {
		return fmt.Errorf("submit answers: HTTP %d", postResp.StatusCode)
	}

	return nil
}

// labelList returns a comma-separated string of option labels for error messages.
func labelList(options []cliAskOption) string {
	labels := make([]string, len(options))
	for i, opt := range options {
		labels[i] = opt.Label
	}
	return strings.Join(labels, ", ")
}

// lineReader reads one line at a time from any io.Reader.
type lineReader struct {
	r    io.Reader
	rest []byte
}

func (lr *lineReader) readLine() (string, error) {
	// Drain any buffered content from a previous read.
	for {
		if i := bytes.IndexByte(lr.rest, '\n'); i >= 0 {
			line := string(lr.rest[:i])
			lr.rest = lr.rest[i+1:]
			return line, nil
		}
		tmp := make([]byte, 256)
		n, err := lr.r.Read(tmp)
		if n > 0 {
			lr.rest = append(lr.rest, tmp[:n]...)
		}
		if err == io.EOF {
			// Return whatever remains without a newline.
			line := string(lr.rest)
			lr.rest = nil
			return line, nil
		}
		if err != nil {
			return "", err
		}
	}
}
