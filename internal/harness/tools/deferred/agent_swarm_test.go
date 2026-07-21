package deferred

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	tools "go-agent-harness/internal/harness/tools"
)

// fakeSwarmRunner implements tools.SwarmRunner, capturing the request.
type fakeSwarmRunner struct {
	mu     sync.Mutex
	calls  int
	req    tools.SwarmRequest
	report tools.SwarmReport
	err    error
}

func (f *fakeSwarmRunner) RunSwarm(_ context.Context, req tools.SwarmRequest) (tools.SwarmReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.req = req
	return f.report, f.err
}

func (f *fakeSwarmRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSwarmRunner) lastReq() tools.SwarmRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.req
}

func TestAgentSwarmToolDefinition(t *testing.T) {
	t.Parallel()

	tool := AgentSwarmTool(&fakeSwarmRunner{}, "")
	def := tool.Definition
	if def.Name != "agent_swarm" {
		t.Errorf("Name = %q, want agent_swarm", def.Name)
	}
	if def.Action != tools.ActionExecute {
		t.Errorf("Action = %q, want ActionExecute (mutating policy path)", def.Action)
	}
	if !def.Mutating {
		t.Error("Mutating = false, want true (approval policy integration)")
	}
	if def.Tier != tools.TierDeferred {
		t.Errorf("Tier = %q, want TierDeferred", def.Tier)
	}
	if def.ParallelSafe {
		t.Error("ParallelSafe = true, want false")
	}
	if def.Description == "" {
		t.Error("Description is empty, want loaded description")
	}

	required, _ := def.Parameters["required"].([]string)
	seen := map[string]bool{}
	for _, name := range required {
		seen[name] = true
	}
	if !seen["prompt_template"] || !seen["items"] {
		t.Errorf("required = %v, want prompt_template and items", required)
	}
	props, _ := def.Parameters["properties"].(map[string]any)
	for _, name := range []string{"prompt_template", "items", "resume_agent_ids", "profile", "model", "max_steps", "allowed_tools"} {
		if _, ok := props[name]; !ok {
			t.Errorf("parameters missing %q", name)
		}
	}
}

func TestAgentSwarmToolHandlerValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "invalid json", raw: `{not json`, wantErr: "parse"},
		{name: "missing prompt_template", raw: `{"items":["a"]}`, wantErr: "prompt_template"},
		{name: "blank prompt_template", raw: `{"prompt_template":"  ","items":["a"]}`, wantErr: "prompt_template"},
		{name: "missing items", raw: `{"prompt_template":"do {{item}}"}`, wantErr: "items"},
		{name: "empty items", raw: `{"prompt_template":"do {{item}}","items":[]}`, wantErr: "items"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeSwarmRunner{}
			tool := AgentSwarmTool(runner, "")
			_, err := tool.Handler(context.Background(), json.RawMessage(tt.raw))
			if err == nil {
				t.Fatalf("handler error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("handler error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("invalid args reached the swarm runner %d times, want 0", runner.callCount())
			}
		})
	}
}

func TestAgentSwarmToolHandlerMapsRequest(t *testing.T) {
	t.Parallel()

	runner := &fakeSwarmRunner{report: tools.SwarmReport{Total: 2, Completed: 2}}
	tool := AgentSwarmTool(runner, "")
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "parent-run-1"})

	raw := json.RawMessage(`{
		"prompt_template": "Review {{item}} carefully",
		"items": ["a", "b"],
		"resume_agent_ids": ["subagent_1"],
		"model": "gpt-swarm",
		"max_steps": 7,
		"allowed_tools": ["read", "bash"]
	}`)
	if _, err := tool.Handler(ctx, raw); err != nil {
		t.Fatalf("handler error = %v, want nil", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("swarm runner calls = %d, want 1", runner.callCount())
	}
	req := runner.lastReq()
	if req.PromptTemplate != "Review {{item}} carefully" {
		t.Errorf("PromptTemplate = %q", req.PromptTemplate)
	}
	if len(req.Items) != 2 || req.Items[0] != "a" || req.Items[1] != "b" {
		t.Errorf("Items = %v, want [a b]", req.Items)
	}
	if len(req.ResumeAgentIDs) != 1 || req.ResumeAgentIDs[0] != "subagent_1" {
		t.Errorf("ResumeAgentIDs = %v, want [subagent_1]", req.ResumeAgentIDs)
	}
	if req.Model != "gpt-swarm" {
		t.Errorf("Model = %q, want gpt-swarm override", req.Model)
	}
	if req.MaxSteps != 7 {
		t.Errorf("MaxSteps = %d, want 7 override", req.MaxSteps)
	}
	if len(req.AllowedTools) != 2 || req.AllowedTools[0] != "read" {
		t.Errorf("AllowedTools = %v, want [read bash]", req.AllowedTools)
	}
	if req.ProfileName != "full" {
		t.Errorf("ProfileName = %q, want default profile full", req.ProfileName)
	}
	if req.ParentContextHandoff == nil || req.ParentContextHandoff.ParentRunID != "parent-run-1" {
		t.Errorf("ParentContextHandoff = %+v, want parent run id parent-run-1", req.ParentContextHandoff)
	}
}

func TestAgentSwarmToolHandlerExplicitProfile(t *testing.T) {
	t.Parallel()

	runner := &fakeSwarmRunner{report: tools.SwarmReport{Total: 1, Completed: 1}}
	tool := AgentSwarmTool(runner, "")
	raw := json.RawMessage(`{"prompt_template":"do {{item}}","items":["a"],"profile":"reviewer"}`)
	if _, err := tool.Handler(context.Background(), raw); err != nil {
		t.Fatalf("handler error = %v, want nil", err)
	}
	if got := runner.lastReq().ProfileName; got != "reviewer" {
		t.Fatalf("ProfileName = %q, want reviewer", got)
	}
}

func TestAgentSwarmToolHandlerUnknownProfile(t *testing.T) {
	t.Parallel()

	runner := &fakeSwarmRunner{}
	tool := AgentSwarmTool(runner, "")
	raw := json.RawMessage(`{"prompt_template":"do {{item}}","items":["a"],"profile":"nonexistent-profile-xyz"}`)
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("handler error = nil, want unknown profile error")
	}
	if !strings.Contains(err.Error(), "nonexistent-profile-xyz") {
		t.Fatalf("handler error = %q, want profile name mentioned", err.Error())
	}
	if runner.callCount() != 0 {
		t.Fatalf("unknown profile reached the swarm runner %d times, want 0", runner.callCount())
	}
}

func TestAgentSwarmToolHandlerReturnsAggregatedReport(t *testing.T) {
	t.Parallel()

	runner := &fakeSwarmRunner{report: tools.SwarmReport{
		Total:     2,
		Completed: 1,
		Failed:    1,
		Members: []tools.SwarmMemberReport{
			{ID: "sub-1", Item: "a", Prompt: "do a", Status: "completed", Output: "done a"},
			{ID: "sub-2", Item: "b", Prompt: "do b", Status: "failed", Error: "boom"},
		},
	}}
	tool := AgentSwarmTool(runner, "")
	raw := json.RawMessage(`{"prompt_template":"do {{item}}","items":["a","b"]}`)
	out, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("handler error = %v, want nil", err)
	}

	var decoded struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
		Members   []struct {
			ID     string `json:"id"`
			Item   string `json:"item"`
			Status string `json:"status"`
			Output string `json:"output"`
			Error  string `json:"error"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("tool result is not JSON: %v\n%s", err, out)
	}
	if decoded.Total != 2 || decoded.Completed != 1 || decoded.Failed != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", decoded.Total, decoded.Completed, decoded.Failed)
	}
	if len(decoded.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(decoded.Members))
	}
	if decoded.Members[0].ID != "sub-1" || decoded.Members[0].Output != "done a" {
		t.Errorf("member 0 = %+v", decoded.Members[0])
	}
	if decoded.Members[1].Status != "failed" || decoded.Members[1].Error != "boom" {
		t.Errorf("member 1 = %+v, want failed/boom", decoded.Members[1])
	}
}

func TestAgentSwarmToolHandlerRunnerError(t *testing.T) {
	t.Parallel()

	runner := &fakeSwarmRunner{err: errors.New("invalid swarm request: prompt_template must contain the \"{{item}}\" placeholder")}
	tool := AgentSwarmTool(runner, "")
	raw := json.RawMessage(`{"prompt_template":"do {{item}}","items":["a"]}`)
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("handler error = nil, want swarm validation error")
	}
	if !strings.Contains(err.Error(), "{{item}}") {
		t.Fatalf("handler error = %q, want the swarm validation reason", err.Error())
	}
}

func TestAgentSwarmToolNilRunner(t *testing.T) {
	t.Parallel()

	tool := AgentSwarmTool(nil, "")
	raw := json.RawMessage(`{"prompt_template":"do {{item}}","items":["a"]}`)
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("handler error = nil, want not-configured error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("handler error = %q, want not configured", err.Error())
	}
}
