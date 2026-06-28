package server

// http_callback_sse_test.go — T8: prove that callback lifecycle events are
// observable on the HTTP SSE run stream (deliverable B), using the P2 runner
// bridge (harness.CallbackEventBridge / Runner.NewCallbackManager).
//
// What is genuinely observable, and what is NOT:
//
//   - callback.scheduled is emitted SYNCHRONOUSLY while the agent's
//     set_delayed_callback tool call executes — i.e. DURING the originating run,
//     which is still live. The bridge resolves the live run by conversation ID
//     and emits the event on that run's stream, so it is observable on
//     GET /v1/runs/{id}/events. This test asserts exactly that.
//   - callback.fired is asserted NOWHERE here: it occurs after the timer elapses
//     (minimum 5s), by which point the originating run has long since sealed its
//     event stream. There is no open SSE stream to deliver it to. Asserting it on
//     a sealed stream would be dishonest, so this test does not.
//
// Design notes:
//   - package server (in-package) so it may import internal/fakeprovider; that
//     package only imports internal/harness, so there is no import cycle with the
//     server package.
//   - The run's event history is retained and replayed when a subscriber connects
//     (see Runner.Subscribe). callback.scheduled is therefore observable on the
//     /events stream regardless of whether the HTTP subscriber connects before or
//     after the tool fires it — making the assertion deterministic rather than
//     racing the run loop. The event still travels the real live-stream handler
//     (handleRunEvents), which is the deliverable under test.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/deferred"
)

// sseCallbackEvent is a minimally-typed view of an SSE-delivered harness.Event
// frame for the callback assertions in this file.
type sseCallbackEvent struct {
	Type    string         `json:"type"`
	RunID   string         `json:"run_id"`
	Payload map[string]any `json:"payload"`
}

// newCallbackRunner builds a Runner whose registry contains the
// set_delayed_callback tool, wired to a CallbackManager whose lifecycle events
// are bridged onto the originating run's SSE stream. The CallbackManager is
// returned so the test can Shutdown it (stopping any pending timers) on cleanup.
func newCallbackRunner(t *testing.T, prov harness.Provider) (*harness.Runner, *htools.CallbackManager) {
	t.Helper()

	// The bridge must be bound to the Runner, but the Runner needs the registry
	// (which needs the manager which needs the bridge) — the same chicken-and-egg
	// the production wiring solves. Construct the bridge unbound, build the
	// manager with it as the event sink, register the tool, build the Runner,
	// then bind.
	bridge := harness.NewCallbackEventBridge()

	// noopStarter satisfies tools.RunStarter. callback.scheduled (the event under
	// test) is emitted by Set BEFORE the timer ever fires, so StartRun is never
	// reached in this test; a no-op keeps the manager honest without spawning
	// follow-up runs.
	mgr := htools.NewCallbackManager(noopRunStarter{}, htools.WithEventSink(bridge))

	registry := harness.NewRegistry()
	tool := deferred.SetDelayedCallbackTool(mgr)
	if err := registry.Register(harness.ToolDefinition{
		Name:        tool.Definition.Name,
		Description: tool.Definition.Description,
		Parameters:  tool.Definition.Parameters,
		Mutating:    tool.Definition.Mutating,
	}, harness.ToolHandler(tool.Handler)); err != nil {
		t.Fatalf("register set_delayed_callback: %v", err)
	}

	runner := harness.NewRunner(prov, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})
	bridge.BindRunner(runner)

	t.Cleanup(func() {
		mgr.Shutdown()
		runner.Shutdown(context.Background())
	})

	return runner, mgr
}

type noopRunStarter struct{}

func (noopRunStarter) StartRun(_, _, _, _ string) error { return nil }

// TestCallbackScheduledOnRunSSEStream proves callback.scheduled appears on the
// originating run's live SSE stream with the expected payload when the agent
// calls set_delayed_callback during the run.
func TestCallbackScheduledOnRunSSEStream(t *testing.T) {
	t.Parallel()

	const callbackPrompt = "check the build status"

	// Turn 1: the agent issues a set_delayed_callback tool call.
	// Turn 2: a plain content turn so the run completes cleanly.
	prov := fakeprovider.New([]fakeprovider.Turn{
		{
			ToolCalls: []harness.ToolCall{{
				ID:        "call-cb-1",
				Name:      "set_delayed_callback",
				Arguments: `{"delay":"30s","prompt":"` + callbackPrompt + `"}`,
			}},
		},
		{Content: "scheduled the callback"},
	})

	runner, _ := newCallbackRunner(t, prov)

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Start the run.
	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"please schedule a callback"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("start run: expected 202, got %d", res.StatusCode)
	}

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("start run: empty run_id")
	}

	// Open the live SSE event stream for the run and read callback.scheduled.
	got := readForCallbackScheduled(t, ts.URL, created.RunID)

	// Assert the scheduled event carries the expected payload.
	if got.RunID != created.RunID {
		t.Errorf("callback.scheduled run_id = %q, want %q", got.RunID, created.RunID)
	}
	// The auto-assigned conversation ID equals the run ID when none was supplied.
	if conv, _ := got.Payload["conversation_id"].(string); conv != created.RunID {
		t.Errorf("callback.scheduled conversation_id = %q, want %q", conv, created.RunID)
	}
	if state, _ := got.Payload["state"].(string); state != string(htools.CallbackStatePending) {
		t.Errorf("callback.scheduled state = %q, want %q", state, htools.CallbackStatePending)
	}
	if p, _ := got.Payload["prompt"].(string); p != callbackPrompt {
		t.Errorf("callback.scheduled prompt = %q, want %q", p, callbackPrompt)
	}
	if id, _ := got.Payload["callback_id"].(string); id == "" {
		t.Error("callback.scheduled payload missing non-empty callback_id")
	}
	if delay, _ := got.Payload["delay"].(string); delay != "30s" {
		t.Errorf("callback.scheduled delay = %q, want %q", delay, "30s")
	}
}

// readForCallbackScheduled opens GET /v1/runs/{id}/events and reads SSE frames
// until it sees a callback.scheduled event (returned) or a terminal run event /
// timeout (fatal). It returns the parsed callback.scheduled frame.
func readForCallbackScheduled(t *testing.T, baseURL, runID string) sseCallbackEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatalf("build events request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open events stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events stream: expected 200, got %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var sawTerminal bool
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev sseCallbackEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			// Non-event data line (e.g. keepalive comment handled above); skip.
			continue
		}
		switch ev.Type {
		case string(harness.EventCallbackScheduled):
			return ev
		case string(harness.EventRunCompleted), string(harness.EventRunFailed), string(harness.EventRunCancelled):
			// The stream closes on a terminal event. Record it; if we exit the
			// loop without having seen callback.scheduled, fail with context.
			sawTerminal = true
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("scan events stream: %v", err)
	}

	if sawTerminal {
		t.Fatal("run stream reached a terminal event without emitting callback.scheduled")
	}
	t.Fatal("timed out waiting for callback.scheduled on the run SSE stream")
	return sseCallbackEvent{} // unreachable
}
