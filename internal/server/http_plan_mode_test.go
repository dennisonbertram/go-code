package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

func startPlanModeHTTPRun(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"plan", "plan_mode":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("start status=%d body=%s", resp.StatusCode, b)
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.RunID
}
func waitPending(t *testing.T, b *harness.InMemoryApprovalBroker, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := b.Pending(runID); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for broker pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPPlanModeRealDispatchDeniesOutsidePlanFile(t *testing.T) {
	provider := &scriptedProvider{turns: []harness.CompletionResult{{ToolCalls: []harness.ToolCall{{ID: "write-outside", Name: "write", Arguments: `{"path":"outside.go","content":"no"}`}}}, {Content: "plan complete"}}}
	reg := harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{})
	runner := harness.NewRunner(provider, reg, harness.RunnerConfig{DefaultModel: "test", ApprovalBroker: harness.NewInMemoryApprovalBroker()})
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, AuthDisabled: true}))
	defer ts.Close()
	runID := startPlanModeHTTPRun(t, ts)
	deadline := time.Now().Add(5 * time.Second)
	for {
		msgs := runner.GetRunMessages(runID)
		for _, m := range msgs {
			if m.Role == "tool" && strings.Contains(m.Content, "plan_mode_denied") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("real HTTP run never surfaced plan_mode_denied: %#v", msgs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPPlanExitApprovalRoutesTransitionState(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		want       harness.PlanModeState
		wantRevise bool
	}{{"approve", "approve", harness.PlanModeInactive, false}, {"deny", "deny", harness.PlanModeActive, true}} {
		t.Run(tc.name, func(t *testing.T) {
			broker := harness.NewInMemoryApprovalBroker()
			provider := &scriptedProvider{turns: []harness.CompletionResult{{Content: "# proposed plan"}}}
			runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "test", ApprovalBroker: broker})
			ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, AuthDisabled: true}))
			defer ts.Close()
			runID := startPlanModeHTTPRun(t, ts)
			waitPending(t, broker, runID)
			resp, err := http.Post(ts.URL+"/v1/runs/"+runID+"/"+tc.path, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("route status=%d", resp.StatusCode)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				state, ok := runner.PlanModeState(runID)
				if ok && state == tc.want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("state=%q ok=%v want=%q", state, ok, tc.want)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if tc.wantRevise {
				deadline = time.Now().Add(5 * time.Second)
				for {
					found := false
					for _, m := range runner.GetRunMessages(runID) {
						found = found || strings.Contains(m.Content, "requested changes")
					}
					if found {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("deny did not send revise message")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		})
	}
}

// planWithApproaches is a presented plan offering two approach options per the
// "## Approaches" convention from the plan-mode guidance.
const planWithApproaches = "# Plan\n\nBuild it.\n\n## Approaches\n\n1. Incremental — migrate piece by piece\n2. Big bang — rewrite in one pass\n"

func planGrantedEvent(t *testing.T, runner *harness.Runner, runID string) harness.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		history, _, cancel, err := runner.Subscribe(runID)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		for _, ev := range history {
			if ev.Type == harness.EventPlanApprovalGranted {
				cancel()
				return ev
			}
		}
		cancel()
		if time.Now().After(deadline) {
			t.Fatal("plan.approval_granted not emitted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHTTPPlanExitApproveWithOption drives the full HTTP flow: the presented
// plan's options surface in plan.approval_required, and approving with
// {"option":"b"} echoes the selection in plan.approval_granted and relays it
// to the model.
func TestHTTPPlanExitApproveWithOption(t *testing.T) {
	broker := harness.NewInMemoryApprovalBroker()
	provider := &scriptedProvider{turns: []harness.CompletionResult{{Content: planWithApproaches}}}
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "test", ApprovalBroker: broker})
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, AuthDisabled: true}))
	defer ts.Close()
	runID := startPlanModeHTTPRun(t, ts)
	waitPending(t, broker, runID)

	// The pending approval carries the extracted options.
	pending, ok := broker.Pending(runID)
	if !ok || len(pending.Options) != 2 {
		t.Fatalf("pending options = %#v ok=%v, want 2 options", pending.Options, ok)
	}

	resp, err := http.Post(ts.URL+"/v1/runs/"+runID+"/approve", "application/json", bytes.NewBufferString(`{"option":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status=%d", resp.StatusCode)
	}

	granted := planGrantedEvent(t, runner, runID)
	if got, _ := granted.Payload["option"].(string); got != "b" {
		t.Fatalf("plan.approval_granted option = %q, want %q", got, "b")
	}
	if got, _ := granted.Payload["option_label"].(string); got != "Big bang" {
		t.Fatalf("plan.approval_granted option_label = %q, want %q", got, "Big bang")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		relayed := false
		for _, m := range runner.GetRunMessages(runID) {
			if m.Role == "user" && strings.Contains(m.Content, "Big bang") {
				relayed = true
			}
		}
		if relayed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("selected approach not relayed to the model")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHTTPPlanExitApproveWithInvalidOptionFallsBack pins the defined fallback:
// an option ID that is not among the pending options is ignored and the
// approval proceeds as a plain approve.
func TestHTTPPlanExitApproveWithInvalidOptionFallsBack(t *testing.T) {
	broker := harness.NewInMemoryApprovalBroker()
	provider := &scriptedProvider{turns: []harness.CompletionResult{{Content: planWithApproaches}}}
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "test", ApprovalBroker: broker})
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, AuthDisabled: true}))
	defer ts.Close()
	runID := startPlanModeHTTPRun(t, ts)
	waitPending(t, broker, runID)

	resp, err := http.Post(ts.URL+"/v1/runs/"+runID+"/approve", "application/json", bytes.NewBufferString(`{"option":"zzz"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status=%d", resp.StatusCode)
	}

	granted := planGrantedEvent(t, runner, runID)
	if opt, present := granted.Payload["option"]; present {
		t.Fatalf("invalid option must fall back to plain approve; granted carried option %q", opt)
	}
}
