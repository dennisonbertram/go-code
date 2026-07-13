package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

// TestMain forces a fast SSE keepalive interval for the whole package so
// tests that wait on a slow provider reliably observe at least one ": ping"
// comment line, proving the live stream (not just buffered history) is
// exercised and that keepalive framing is parsed correctly by clients.
func TestMain(m *testing.M) {
	os.Setenv("HARNESS_SSE_KEEPALIVE_SECONDS", "1")
	os.Exit(m.Run())
}

// TestE2E_HappyPathRunCompletes drives a full run over real HTTP + SSE: POST
// /v1/runs, then GET /v1/runs/{id}/events as a live stream. The provider
// deliberately takes longer than the keepalive interval so the stream must
// survive at least one ": ping" comment line before the terminal event, and
// the test asserts both the terminal event sequence and the SSE keepalive
// framing are handled correctly end to end.
func TestE2E_HappyPathRunCompletes(t *testing.T) {
	t.Parallel()

	provider := &slowProvider{
		delay:  1500 * time.Millisecond,
		result: harness.CompletionResult{Content: "hello from the fake model"},
	}
	ts := newTestServer(t, provider, nil, nil)

	runID := startRun(t, ts, `{"prompt":"say hi"}`)

	reader, closeStream := openEventStream(t, ts, runID)
	defer closeStream()

	types, terminal := drainUntilTerminal(t, reader, 10*time.Second)

	if len(types) == 0 || types[0] != harness.EventRunStarted {
		t.Fatalf("expected first event to be %q, got sequence %v", harness.EventRunStarted, types)
	}
	if terminal.Type != harness.EventRunCompleted {
		t.Fatalf("expected terminal event %q, got %q (sequence %v)", harness.EventRunCompleted, terminal.Type, types)
	}
	if reader.pings < 1 {
		t.Fatalf("expected at least one SSE keepalive ping comment line while the run was in flight, saw %d", reader.pings)
	}

	// The stream must end (server closes after the terminal event) rather
	// than hang waiting for more events.
	if _, err := reader.next(); err == nil {
		t.Fatal("expected SSE stream to close after the terminal event")
	}
}

// TestE2E_CancelledRunReachesCancelledEvent drives a run whose provider
// blocks indefinitely, cancels it via POST /v1/runs/{id}/cancel exactly as
// the CLI does, and asserts the live SSE stream delivers a terminal
// run.cancelled event.
func TestE2E_CancelledRunReachesCancelledEvent(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider()
	ts := newTestServer(t, provider, nil, nil)

	runID := startRun(t, ts, `{"prompt":"do something slow"}`)

	reader, closeStream := openEventStream(t, ts, runID)
	defer closeStream()

	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started blocking")
	}

	res, err := ts.Client().Post(ts.URL+"/v1/runs/"+runID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("POST cancel: expected 200, got %d", res.StatusCode)
	}

	types, terminal := drainUntilTerminal(t, reader, 10*time.Second)

	if terminal.Type != harness.EventRunCancelled {
		t.Fatalf("expected terminal event %q, got %q (sequence %v)", harness.EventRunCancelled, terminal.Type, types)
	}
	if terminal.RunID != runID {
		t.Fatalf("terminal event run_id = %q, want %q", terminal.RunID, runID)
	}
}

// TestE2E_ToolCallApprovalRoundTrip drives a run whose fake provider requests
// a tool call under an "approval: all" permission policy, watches the live
// SSE stream for tool.call.started and tool.approval_required, approves the
// pending call via POST /v1/runs/{id}/approve exactly as a client would, and
// asserts the tool executes and the run reaches a completed terminal event
// with the post-approval assistant content.
func TestE2E_ToolCallApprovalRoundTrip(t *testing.T) {
	t.Parallel()

	broker := harness.NewInMemoryApprovalBroker()

	provider := &scriptedProvider{
		turns: []harness.CompletionResult{
			{
				ToolCalls: []harness.ToolCall{{
					ID:        "call_1",
					Name:      "echo_tool",
					Arguments: `{"value":"ping"}`,
				}},
			},
			{Content: "done after approval"},
		},
	}

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        "echo_tool",
		Description: "echoes its input",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
		},
		ParallelSafe: true,
	}, func(_ context.Context, args json.RawMessage) (string, error) {
		return string(args), nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	ts := newTestServer(t, provider, registry, broker)

	runID := startRun(t, ts, `{"prompt":"use the tool","permissions":{"sandbox":"unrestricted","approval":"all"}}`)

	reader, closeStream := openEventStream(t, ts, runID)
	defer closeStream()

	var (
		types           []harness.EventType
		sawToolStarted  bool
		sawApproval     bool
		sawToolComplete bool
		terminal        harness.Event
	)

	deadline := time.Now().Add(10 * time.Second)
	approved := false
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run to complete (sequence so far: %v)", types)
		}
		ev, err := reader.next()
		if err != nil {
			t.Fatalf("reading SSE stream: %v (sequence so far: %v)", err, types)
		}
		hev := ev.harnessEvent(t)
		types = append(types, hev.Type)

		switch hev.Type {
		case harness.EventToolCallStarted:
			sawToolStarted = true
		case harness.EventToolApprovalRequired:
			sawApproval = true
			if !approved {
				approved = true
				res, err := ts.Client().Post(ts.URL+"/v1/runs/"+runID+"/approve", "application/json", nil)
				if err != nil {
					t.Fatalf("POST approve: %v", err)
				}
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatalf("POST approve: expected 200, got %d", res.StatusCode)
				}
			}
		case harness.EventToolCallCompleted:
			sawToolComplete = true
		}

		if harness.IsTerminalEvent(hev.Type) {
			terminal = hev
			break
		}
	}

	if !sawToolStarted {
		t.Errorf("expected to observe %q in the SSE stream, sequence: %v", harness.EventToolCallStarted, types)
	}
	if !sawApproval {
		t.Errorf("expected to observe %q in the SSE stream, sequence: %v", harness.EventToolApprovalRequired, types)
	}
	if !sawToolComplete {
		t.Errorf("expected to observe %q in the SSE stream, sequence: %v", harness.EventToolCallCompleted, types)
	}
	if terminal.Type != harness.EventRunCompleted {
		t.Fatalf("expected terminal event %q, got %q (sequence %v)", harness.EventRunCompleted, terminal.Type, types)
	}
}
