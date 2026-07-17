package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

// writeScript writes an executable shell script to dir/name and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func commandDef(t *testing.T, script string) HookDef {
	t.Helper()
	return HookDef{
		Name:    "test-hook",
		Event:   EventPreToolUse,
		Kind:    KindCommand,
		Command: []string{script},
	}
}

func preEvent() harness.PreToolUseEvent {
	return harness.PreToolUseEvent{
		ToolName: "bash",
		CallID:   "call_1",
		Args:     json.RawMessage(`{"command":"rm -rf /"}`),
		RunID:    "run_1",
	}
}

func TestCommandHook_PreToolUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		script       string
		wantDecision harness.ToolHookDecision
		wantReason   string
		wantModified string // expected ModifiedArgs substring; empty = none
		wantNil      bool   // nil result expected (allow, no modification)
		wantErr      string // substring of expected error; empty = no error
	}{
		{
			name:    "explicit allow returns nil result",
			script:  `echo '{"decision":"allow"}'`,
			wantNil: true,
		},
		{
			name:    "empty stdout is allow with no modification",
			script:  `exit 0`,
			wantNil: true,
		},
		{
			name:         "deny with reason",
			script:       `echo '{"decision":"deny","reason":"nope"}'`,
			wantDecision: harness.ToolHookDeny,
			wantReason:   "nope",
		},
		{
			name:         "modified args pass through",
			script:       `echo '{"decision":"allow","modified_args":{"command":"ls"}}'`,
			wantDecision: harness.ToolHookAllow,
			wantModified: `"command":"ls"`,
		},
		{
			name:    "garbage stdout is an error not a decision",
			script:  `echo 'this is not json'`,
			wantErr: "parse",
		},
		{
			name:    "unknown decision is an error",
			script:  `echo '{"decision":"maybe"}'`,
			wantErr: "unknown decision",
		},
		{
			name:    "non-zero exit is an error",
			script:  `echo '{"decision":"deny"}'; exit 3`,
			wantErr: "exit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := writeScript(t, t.TempDir(), "hook.sh", tc.script)
			hook := NewCommandHook(commandDef(t, script))

			result, err := hook.PreToolUse(context.Background(), preEvent())

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%+v)", tc.wantErr, result)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if result != nil {
					t.Fatalf("expected nil result, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Decision != tc.wantDecision {
				t.Errorf("Decision: got %v, want %v", result.Decision, tc.wantDecision)
			}
			if result.Reason != tc.wantReason {
				t.Errorf("Reason: got %q, want %q", result.Reason, tc.wantReason)
			}
			if tc.wantModified != "" && !strings.Contains(string(result.ModifiedArgs), tc.wantModified) {
				t.Errorf("ModifiedArgs: got %s, want substring %q", result.ModifiedArgs, tc.wantModified)
			}
		})
	}
}

func TestCommandHook_PostToolUse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	t.Run("modified result", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, dir, "post.sh", `echo '{"modified_result":"wrapped output"}'`)
		hook := NewCommandHook(commandDef(t, script))
		result, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{
			ToolName: "bash", CallID: "c1", Args: json.RawMessage(`{}`),
			Result: "original", Duration: 120 * time.Millisecond, RunID: "r1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.ModifiedResult != "wrapped output" {
			t.Fatalf("ModifiedResult: got %+v", result)
		}
	})

	t.Run("empty stdout is no modification", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, dir, "post-empty.sh", `exit 0`)
		hook := NewCommandHook(commandDef(t, script))
		result, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{ToolName: "bash"})
		if err != nil || result != nil {
			t.Fatalf("got result=%+v err=%v, want nil/nil", result, err)
		}
	})

	t.Run("non-zero exit is an error", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, dir, "post-fail.sh", `exit 1`)
		hook := NewCommandHook(commandDef(t, script))
		if _, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{ToolName: "bash"}); err == nil {
			t.Fatal("expected error from failing post hook")
		}
	})
}

// TestCommandHook_StdinGoldenFields pins the documented wire protocol: the
// JSON the script receives on stdin must carry the documented field names.
func TestCommandHook_StdinGoldenFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	capture := filepath.Join(dir, "stdin.json")
	script := writeScript(t, dir, "capture.sh", "cat > \"$1\"\nexit 0")
	hook := NewCommandHook(HookDef{
		Name: "capture", Event: EventPreToolUse, Kind: KindCommand,
		Command: []string{script, capture},
	})

	_, err := hook.PreToolUse(context.Background(), preEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("script did not receive stdin: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("stdin payload is not JSON: %v", err)
	}
	for _, field := range []string{"event", "run_id", "hook_name", "tool_name", "call_id", "args"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("stdin payload missing documented field %q (got %s)", field, data)
		}
	}
	if payload["event"] != "pre_tool_use" {
		t.Errorf("event: got %v, want pre_tool_use", payload["event"])
	}
	if payload["tool_name"] != "bash" {
		t.Errorf("tool_name: got %v, want bash", payload["tool_name"])
	}
	if payload["hook_name"] != "capture" {
		t.Errorf("hook_name: got %v, want capture", payload["hook_name"])
	}
}

// TestCommandHook_PostToolUseStdinGoldenFields pins the post_tool_use wire
// fields: result, duration_ms, and error.
func TestCommandHook_PostToolUseStdinGoldenFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	capture := filepath.Join(dir, "stdin.json")
	script := writeScript(t, dir, "capture.sh", "cat > \"$1\"\nexit 0")
	hook := NewCommandHook(HookDef{
		Name: "capture", Event: EventPostToolUse, Kind: KindCommand,
		Command: []string{script, capture},
	})

	_, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{
		ToolName: "bash", CallID: "c9", Args: json.RawMessage(`{"a":1}`),
		Result: "the output", Duration: 250 * time.Millisecond,
		Error: context.DeadlineExceeded, RunID: "r9",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(capture)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("stdin payload is not JSON: %v (%s)", err, data)
	}
	if payload["event"] != "post_tool_use" {
		t.Errorf("event: got %v, want post_tool_use", payload["event"])
	}
	if payload["result"] != "the output" {
		t.Errorf("result: got %v", payload["result"])
	}
	if payload["duration_ms"] != float64(250) {
		t.Errorf("duration_ms: got %v, want 250", payload["duration_ms"])
	}
	errStr, _ := payload["error"].(string)
	if !strings.Contains(errStr, "deadline") {
		t.Errorf("error: got %v, want deadline message", payload["error"])
	}
}

// TestCommandHook_MatcherSkipsExec verifies a non-matching tool name performs
// no exec at all (asserted via a sentinel file the script would create).
func TestCommandHook_MatcherSkipsExec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "execed")
	script := writeScript(t, dir, "sentinel.sh", "touch \"$1\"\necho '{\"decision\":\"deny\"}'")
	def := commandDef(t, script)
	def.Command = []string{script, sentinel}
	def.Matcher = "bash"
	hook := NewCommandHook(def)

	// Non-matching tool: no exec, allow.
	result, err := hook.PreToolUse(context.Background(), harness.PreToolUseEvent{
		ToolName: "write_file", CallID: "c1", Args: json.RawMessage(`{}`), RunID: "r1",
	})
	if err != nil || result != nil {
		t.Fatalf("non-matching tool: got result=%+v err=%v, want nil/nil", result, err)
	}
	if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
		t.Fatal("hook executed despite non-matching tool name (sentinel exists)")
	}

	// Matching tool: execs and denies.
	result, err = hook.PreToolUse(context.Background(), preEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Decision != harness.ToolHookDeny {
		t.Fatalf("matching tool: got %+v, want deny", result)
	}
}

// TestCommandHook_TimeoutKillsProcess verifies a hung hook is interrupted at
// its per-hook timeout and the child process tree is actually dead afterward
// (no orphan).
func TestCommandHook_TimeoutKillsProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive in short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "parent.pid")
	childPidFile := filepath.Join(dir, "child.pid")
	// The script records its own PID and the PID of a background grandchild,
	// then hangs. Group kill must reap both.
	script := writeScript(t, dir, "hang.sh",
		"echo $$ > \"$1\"\nsleep 60 & echo $! > \"$2\"\nwait")
	def := HookDef{
		Name: "hang", Event: EventPreToolUse, Kind: KindCommand,
		Command:        []string{script, pidFile, childPidFile},
		TimeoutSeconds: 1,
	}
	hook := NewCommandHook(def)

	start := time.Now()
	_, err := hook.PreToolUse(context.Background(), preEvent())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention timeout, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("hook was not interrupted promptly: %v", elapsed)
	}

	// Both the script and its background child must be dead. Process reaping
	// can lag the kill (the grandchild is reparented and reaped by init), so
	// poll briefly rather than asserting a single instantaneous state.
	for _, pf := range []string{pidFile, childPidFile} {
		data, readErr := os.ReadFile(pf)
		if readErr != nil {
			t.Fatalf("pid file %s missing: %v", pf, readErr)
		}
		var pid int
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); scanErr != nil {
			t.Fatalf("parse pid from %s: %v", pf, scanErr)
		}
		deadline := time.Now().Add(5 * time.Second)
		alive := true
		for time.Now().Before(deadline) {
			if killErr := syscall.Kill(pid, 0); killErr != nil {
				alive = false
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if alive {
			t.Fatalf("process %d from %s still alive 5s after hook timeout (orphan)", pid, pf)
		}
	}
}
