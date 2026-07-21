package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// runnerFakeSwarmRunner implements htools.SwarmRunner for runner-level tests.
type runnerFakeSwarmRunner struct {
	mu     sync.Mutex
	reqs   []htools.SwarmRequest
	report htools.SwarmReport
	err    error
}

func (f *runnerFakeSwarmRunner) RunSwarm(_ context.Context, req htools.SwarmRequest) (htools.SwarmReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.report, f.err
}

func (f *runnerFakeSwarmRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func swarmToolMessages(t *testing.T, r *Runner, runID string) []Message {
	t.Helper()
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		t.Fatalf("run %q not found", runID)
	}
	out := make([]Message, 0, len(state.messages))
	for _, m := range state.messages {
		if m.Role == "tool" {
			out = append(out, m)
		}
	}
	return out
}

func toolMessageForCall(t *testing.T, r *Runner, runID, callID string) string {
	t.Helper()
	for _, m := range swarmToolMessages(t, r, runID) {
		if m.ToolCallID == callID {
			return m.Content
		}
	}
	t.Fatalf("no tool message for call %q in run %q", callID, runID)
	return ""
}

func TestAgentSwarmRegisteredAsMutatingDeferredTool(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 1, Completed: 1}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})

	if !registry.IsMutating("agent_swarm") {
		t.Fatal("agent_swarm IsMutating = false, want true (approval policy integration)")
	}
	found := false
	for _, def := range registry.DeferredDefinitions() {
		if def.Name == "agent_swarm" {
			found = true
			if !def.Mutating {
				t.Fatal("agent_swarm Mutating = false, want true")
			}
		}
	}
	if !found {
		t.Fatal("agent_swarm not registered as a deferred tool")
	}
}

func TestAgentSwarmSoleCallRuleRejectsExtras(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 2, Completed: 2}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a","b"]}`},
			{ID: "call-extra", Name: "read", Arguments: `{"path":"x"}`},
		}},
		{Content: "done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test", DefaultSystemPrompt: "sys"})

	run, err := runner.StartRun(RunRequest{Prompt: "fan out and read"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", got)
	}

	// The swarm call itself executes.
	if swarmRunner.callCount() != 1 {
		t.Fatalf("swarm runner calls = %d, want 1", swarmRunner.callCount())
	}
	swarmResult := toolMessageForCall(t, runner, run.ID, "call-swarm")
	if !strings.Contains(swarmResult, "\"total\":2") {
		t.Fatalf("swarm tool result = %q, want aggregated report with total 2", swarmResult)
	}

	// The extra call is rejected with a corrective error naming the rule.
	extraResult := toolMessageForCall(t, runner, run.ID, "call-extra")
	if !strings.Contains(extraResult, "agent_swarm") || !strings.Contains(extraResult, "only tool call") {
		t.Fatalf("extra call result = %q, want corrective error naming the agent_swarm sole-call rule", extraResult)
	}
}

func TestAgentSwarmSoleCallAloneIsAllowed(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 1, Completed: 1}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a"]}`},
		}},
		{Content: "done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test", DefaultSystemPrompt: "sys"})

	run, err := runner.StartRun(RunRequest{Prompt: "fan out"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", got)
	}
	if swarmRunner.callCount() != 1 {
		t.Fatalf("swarm runner calls = %d, want 1", swarmRunner.callCount())
	}
	swarmResult := toolMessageForCall(t, runner, run.ID, "call-swarm")
	if strings.Contains(swarmResult, "only tool call") {
		t.Fatalf("sole swarm call was rejected: %q", swarmResult)
	}
}

func TestAgentSwarmTwoSwarmCallsKeepOnlyFirst(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 1, Completed: 1}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm-1", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a"]}`},
			{ID: "call-swarm-2", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["b"]}`},
		}},
		{Content: "done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test", DefaultSystemPrompt: "sys"})

	run, err := runner.StartRun(RunRequest{Prompt: "two swarms"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", got)
	}
	if swarmRunner.callCount() != 1 {
		t.Fatalf("swarm runner calls = %d, want 1 (second swarm call rejected)", swarmRunner.callCount())
	}
	second := toolMessageForCall(t, runner, run.ID, "call-swarm-2")
	if !strings.Contains(second, "only tool call") {
		t.Fatalf("second swarm call result = %q, want sole-call corrective error", second)
	}
}

func TestAgentSwarmDeniedForMemberRuns(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 1, Completed: 1}}
	activations := NewActivationTracker()
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		AgentSwarmRunner: swarmRunner,
		Activations:      activations,
	})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a"]}`},
		}},
		{Content: "done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "sys",
		Activations:         activations,
	})

	// Swarm-member runs carry DeniedTools: agent_swarm must be neither
	// offered nor callable for them.
	run, err := runner.StartRun(RunRequest{Prompt: "member", DeniedTools: []string{"agent_swarm"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", got)
	}
	if swarmRunner.callCount() != 0 {
		t.Fatalf("swarm runner calls = %d, want 0 (member runs cannot call agent_swarm)", swarmRunner.callCount())
	}
	denied := toolMessageForCall(t, runner, run.ID, "call-swarm")
	if !strings.Contains(denied, "agent_swarm") {
		t.Fatalf("denied call result = %q, want message naming agent_swarm", denied)
	}

	// Even when activated for the run, the definition stays hidden.
	activations.Activate(run.ID, "agent_swarm")
	for _, def := range runner.filteredToolsForRun(run.ID) {
		if def.Name == "agent_swarm" {
			t.Fatal("agent_swarm visible in member run definitions despite DeniedTools")
		}
	}

	// Control: an unrestricted run sees the activated definition.
	run2, err := runner.StartRun(RunRequest{Prompt: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	activations.Activate(run2.ID, "agent_swarm")
	seen := false
	for _, def := range runner.filteredToolsForRun(run2.ID) {
		if def.Name == "agent_swarm" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("agent_swarm missing from unrestricted run definitions after activation")
	}
	waitForStatus(t, runner, run2.ID, RunStatusCompleted, RunStatusFailed)
}

func TestAgentSwarmApprovalFlowSurfacesMutatingCall(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{Total: 1, Completed: 1}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a"]}`},
		}},
		{Content: "done"},
	}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "sys",
		ApprovalBroker:      broker,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:      "fan out",
		Permissions: &PermissionConfig{Sandbox: SandboxScopeUnrestricted, Approval: ApprovalPolicyDestructive},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The destructive policy must pause the mutating agent_swarm call for approval.
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, ok := broker.Pending(run.ID)
		if ok {
			if pending.Tool != "agent_swarm" {
				t.Fatalf("pending approval tool = %q, want agent_swarm", pending.Tool)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent_swarm never surfaced for approval under the destructive policy")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", got)
	}
	if swarmRunner.callCount() != 1 {
		t.Fatalf("swarm runner calls = %d, want 1 after approval", swarmRunner.callCount())
	}
}

// TestAgentSwarmEndToEndReportShape guards the JSON contract returned to the
// model for a completed swarm call.
func TestAgentSwarmEndToEndReportShape(t *testing.T) {
	t.Parallel()

	swarmRunner := &runnerFakeSwarmRunner{report: htools.SwarmReport{
		Total:     2,
		Completed: 2,
		Members: []htools.SwarmMemberReport{
			{ID: "sub-1", Item: "a", Prompt: "do a", Status: "completed", Output: "done a"},
			{ID: "sub-2", Item: "b", Prompt: "do b", Status: "completed", Output: "done b"},
		},
	}}
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{AgentSwarmRunner: swarmRunner})
	provider := &capturingProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "call-swarm", Name: "agent_swarm", Arguments: `{"prompt_template":"do {{item}}","items":["a","b"]}`},
		}},
		{Content: "done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test", DefaultSystemPrompt: "sys"})

	run, err := runner.StartRun(RunRequest{Prompt: "fan out"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	content := toolMessageForCall(t, runner, run.ID, "call-swarm")
	var report struct {
		Total   int `json:"total"`
		Members []struct {
			ID    string `json:"id"`
			Item  string `json:"item"`
			Error string `json:"error"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(content), &report); err != nil {
		t.Fatalf("swarm tool result is not JSON: %v\n%s", err, content)
	}
	if report.Total != 2 || len(report.Members) != 2 {
		t.Fatalf("report = total:%d members:%d, want 2/2", report.Total, len(report.Members))
	}
}
