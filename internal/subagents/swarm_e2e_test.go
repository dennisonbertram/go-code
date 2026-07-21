package subagents

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// waitSwarmE2ETerminal polls until the run reaches a terminal status.
func waitSwarmE2ETerminal(t *testing.T, r *harness.Runner, runID string) harness.Run {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := r.GetRun(runID)
		if !ok {
			t.Fatalf("run %q not found", runID)
		}
		switch run.Status {
		case harness.RunStatusCompleted, harness.RunStatusFailed, harness.RunStatusCancelled:
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %q did not reach terminal state", runID)
	return harness.Run{}
}

// swarmE2EHandoff forwards subagents.RunEngine and tools.RunSteerer to the
// runner under construction, mirroring cmd/harnessd's subagentRunnerHandoff.
type swarmE2EHandoff struct {
	runner atomic.Pointer[harness.Runner]
}

func (h *swarmE2EHandoff) setRunner(r *harness.Runner) { h.runner.Store(r) }

func (h *swarmE2EHandoff) StartRun(req harness.RunRequest) (harness.Run, error) {
	return h.runner.Load().StartRun(req)
}

func (h *swarmE2EHandoff) GetRun(runID string) (harness.Run, bool) {
	return h.runner.Load().GetRun(runID)
}

func (h *swarmE2EHandoff) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	return h.runner.Load().Subscribe(runID)
}

func (h *swarmE2EHandoff) CancelRun(runID string) error {
	return h.runner.Load().CancelRun(runID)
}

func (h *swarmE2EHandoff) SteerRun(runID, message string) error {
	return h.runner.Load().SteerRun(runID, message)
}

func (h *swarmE2EHandoff) ParentRunID(string) (string, bool) { return "", false }

// TestAgentSwarmEndToEndFanOut drives the production wiring shape —
// fakeprovider → registry(agent_swarm) → runner → handoff → manager →
// InlineManager → Swarm — and asserts a 4-item swarm produces four member
// runs and exactly one aggregated tool result returned to the parent run.
func TestAgentSwarmEndToEndFanOut(t *testing.T) {
	provider := fakeprovider.New([]fakeprovider.Turn{
		{ToolCalls: []harness.ToolCall{
			{ID: "call-find", Name: "find_tool", Arguments: `{"query":"select:agent_swarm"}`},
		}},
		{ToolCalls: []harness.ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["i1","i2","i3","i4"]}`},
		}},
		{Content: "member done"},
		{Content: "member done"},
		{Content: "member done"},
		{Content: "member done"},
		{Content: "all done"},
	})

	handoff := &swarmE2EHandoff{}
	mgr, err := NewManager(Options{InlineRunner: handoff})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	swarm := NewSwarm(NewInlineManager(mgr), WithSwarmSteerer(handoff))
	registry := harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{
		AgentSwarmRunner: NewToolSwarmRunner(swarm),
	})
	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
	})
	handoff.setRunner(runner)

	run, err := runner.StartRun(harness.RunRequest{Prompt: "fan out please"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	final := waitSwarmE2ETerminal(t, runner, run.ID)
	if final.Status != harness.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed (output %q, error %q)", final.Status, final.Output, final.Error)
	}

	invocations := provider.Invocations()
	// Four member runs each consume exactly one provider turn with their
	// expanded item prompt.
	memberPrompts := map[string]bool{}
	for _, inv := range invocations {
		for _, msg := range inv.Request.Messages {
			if msg.Role == "user" && strings.HasPrefix(msg.Content, "do i") {
				memberPrompts[msg.Content] = true
			}
		}
	}
	for _, want := range []string{"do i1", "do i2", "do i3", "do i4"} {
		if !memberPrompts[want] {
			t.Fatalf("no member run saw prompt %q; saw %v", want, memberPrompts)
		}
	}

	// The parent's final provider turn must carry exactly one agent_swarm
	// tool result: the aggregated report covering all four members.
	last := invocations[len(invocations)-1]
	swarmResults := 0
	var report struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
		Members   []struct {
			ID     string `json:"id"`
			Item   string `json:"item"`
			Status string `json:"status"`
			Output string `json:"output"`
		} `json:"members"`
	}
	for _, msg := range last.Request.Messages {
		if msg.Role == "tool" && msg.Name == "agent_swarm" {
			swarmResults++
			if err := json.Unmarshal([]byte(msg.Content), &report); err != nil {
				t.Fatalf("agent_swarm tool result is not JSON: %v\n%s", err, msg.Content)
			}
		}
	}
	if swarmResults != 1 {
		t.Fatalf("agent_swarm tool results in final turn = %d, want exactly 1", swarmResults)
	}
	if report.Total != 4 || report.Completed != 4 || report.Failed != 0 {
		t.Fatalf("report counts = total:%d completed:%d failed:%d, want 4/4/0", report.Total, report.Completed, report.Failed)
	}
	if len(report.Members) != 4 {
		t.Fatalf("report members = %d, want 4", len(report.Members))
	}
	for i, want := range []string{"i1", "i2", "i3", "i4"} {
		m := report.Members[i]
		if m.Item != want {
			t.Errorf("member %d item = %q, want %q (deterministic order)", i, m.Item, want)
		}
		if m.Status != string(harness.RunStatusCompleted) {
			t.Errorf("member %d status = %q, want completed", i, m.Status)
		}
		if m.ID == "" {
			t.Errorf("member %d has no subagent id", i)
		}
	}

	// Every member is an ordinary subagent visible through the manager.
	items, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("manager List: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("manager tracks %d subagents, want 4", len(items))
	}
}
