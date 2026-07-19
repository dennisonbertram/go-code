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
