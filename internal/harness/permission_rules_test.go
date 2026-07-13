package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func permissionRule(pattern string, effect PermissionEffect) PermissionRule {
	return PermissionRule{Pattern: pattern, Effect: effect}
}

func permissionRuleSet(rules ...PermissionRule) *PermissionRuleSet {
	return NewPermissionRuleSet(rules)
}

func evaluatePermissionRule(t *testing.T, rules []PermissionRule, tool string, args any, workspace string) PermissionEffect {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	effect, err := EvaluatePermissionRules(rules, tool, raw, workspace)
	if err != nil {
		t.Fatalf("evaluate rules: %v", err)
	}
	return effect
}

func TestPermissionRule_ParseExactAndGlob(t *testing.T) {
	t.Parallel()

	exact, err := ParsePermissionRule("bash(git status)", PermissionEffectAllow)
	if err != nil {
		t.Fatalf("parse exact rule: %v", err)
	}
	if exact.Pattern != "bash(git status)" || exact.Effect != PermissionEffectAllow {
		t.Fatalf("unexpected exact rule: %+v", exact)
	}
	glob, err := ParsePermissionRule("read(./src/**)", PermissionEffectDeny)
	if err != nil {
		t.Fatalf("parse glob rule: %v", err)
	}
	if glob.Pattern != "read(./src/**)" || glob.Effect != PermissionEffectDeny {
		t.Fatalf("unexpected glob rule: %+v", glob)
	}
	rules := []PermissionRule{permissionRule("bash(git diff:*)", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": "git diff --stat"}, t.TempDir()); got != PermissionEffectDeny {
		t.Fatalf("got %q, want deny for bash glob", got)
	}
}

func TestPermissionConfigRulesEncodeAsArray(t *testing.T) {
	t.Parallel()

	original := PermissionConfig{
		Sandbox:  SandboxScopeWorkspace,
		Approval: ApprovalPolicyNone,
		Rules:    permissionRuleSet(permissionRule("bash", PermissionEffectAllow)),
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PermissionConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(permissionRulesFromSet(decoded.Rules)) != 1 || permissionRulesFromSet(decoded.Rules)[0] != permissionRulesFromSet(original.Rules)[0] {
		t.Fatalf("rules did not round-trip: %s", raw)
	}
}

func TestPermissionRule_DenyBeatsAllow(t *testing.T) {
	t.Parallel()

	rules := []PermissionRule{
		permissionRule("bash(git *)", PermissionEffectAllow),
		permissionRule("bash(git *)", PermissionEffectDeny),
	}
	if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": "git status"}, t.TempDir()); got != PermissionEffectDeny {
		t.Fatalf("got %q, want deny", got)
	}
}

func TestPermissionRule_MostSpecificWins(t *testing.T) {
	t.Parallel()

	rules := []PermissionRule{
		permissionRule("bash(git status)", PermissionEffectDeny),
		permissionRule("bash(git status --short)", PermissionEffectAllow),
	}
	if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": "git status --short"}, t.TempDir()); got != PermissionEffectAllow {
		t.Fatalf("got %q, want allow", got)
	}
}

func TestPermissionRule_NoMatchDefaultsToAllow(t *testing.T) {
	t.Parallel()

	rules := []PermissionRule{permissionRule("bash(git status)", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": "git diff"}, t.TempDir()); got != PermissionEffectAllow {
		t.Fatalf("got %q, want default allow", got)
	}
}

func TestPermissionRule_BareToolBackCompat(t *testing.T) {
	t.Parallel()

	rules := []PermissionRule{permissionRule("bash", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": "git status"}, t.TempDir()); got != PermissionEffectDeny {
		t.Fatalf("got %q, want deny for any bash invocation", got)
	}
	if got := evaluatePermissionRule(t, rules, "read", map[string]string{"path": "notes.txt"}, t.TempDir()); got != PermissionEffectAllow {
		t.Fatalf("got %q, want allow for unrelated tool", got)
	}
}

func TestPermissionRule_BashNormalizationPreventsWhitespaceQuotingAndBinEvasion(t *testing.T) {
	t.Parallel()

	rules := []PermissionRule{permissionRule("bash(rm *)", PermissionEffectDeny)}
	commands := []string{
		"rm file.txt",
		"  rm    file.txt  ",
		"rm 'file.txt'",
		`rm "file.txt"`,
		"/bin/rm file.txt",
	}
	for _, command := range commands {
		if got := evaluatePermissionRule(t, rules, "bash", map[string]string{"command": command}, t.TempDir()); got != PermissionEffectDeny {
			t.Errorf("command %q got %q, want deny", command, got)
		}
	}
}

func TestPermissionRule_PathGlobRejectsDotDotTraversal(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := []PermissionRule{permissionRule("read(./src/**)", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "read", map[string]string{"path": "../src/secret.go"}, workspace); got != PermissionEffectAllow {
		t.Fatalf("got %q, want default allow for traversal", got)
	}
}

func TestPermissionRule_PathGlobMatchesWorkspaceRelativePath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "src", "nested", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules := []PermissionRule{permissionRule("read(./src/**)", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "read", map[string]string{"path": "src/nested/main.go"}, workspace); got != PermissionEffectDeny {
		t.Fatalf("got %q, want deny for workspace glob", got)
	}
}

func TestPermissionRule_PathGlobRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "src")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rules := []PermissionRule{permissionRule("read(./src/**)", PermissionEffectDeny)}
	if got := evaluatePermissionRule(t, rules, "read", map[string]string{"path": "src/secret.go"}, workspace); got != PermissionEffectAllow {
		t.Fatalf("got %q, want default allow for symlink escape", got)
	}
}

func TestPermissionRule_DispatchDenyStopsToolBeforeHandler(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{Name: "bash"}, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return `{"ok":true}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(&stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "deny-1", Name: "bash", Arguments: `{"command":"rm file.txt"}`}}},
		{Content: "done"},
	}}, registry, RunnerConfig{})
	run, err := runner.StartRun(RunRequest{
		Prompt: "deny a command",
		Rules:  []PermissionRule{permissionRule("bash(rm *)", PermissionEffectDeny)},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForPermissionRuleTerminal(t, runner, run.ID)
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls = %d, want 0", got)
	}
}

func TestPermissionRule_DispatchAskUsesApprovalBroker(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{Name: "bash"}, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return `{"ok":true}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(&stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "ask-1", Name: "bash", Arguments: `{"command":"git status"}`}}},
		{Content: "done"},
	}}, registry, RunnerConfig{ApprovalBroker: broker})
	run, err := runner.StartRun(RunRequest{
		Prompt: "ask before command",
		Permissions: &PermissionConfig{Rules: permissionRuleSet(
			permissionRule("bash(git status)", PermissionEffectAsk),
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := broker.Pending(run.ID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for permission-rule approval")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := broker.Approve(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForPermissionRuleTerminal(t, runner, run.ID)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func waitForPermissionRuleTerminal(t *testing.T, runner *Runner, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, ok := runner.GetRun(runID)
		if ok && (run.Status == RunStatusCompleted || run.Status == RunStatusFailed || run.Status == RunStatusCancelled) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal run status: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
