package hooks_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/hooks"
)

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// echoRegistry returns a registry with a single echo tool plus a call counter.
func echoRegistry(t *testing.T) (*harness.Registry, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	reg := harness.NewRegistry()
	err := reg.Register(harness.ToolDefinition{
		Name:        "echo_tool",
		Description: "echoes input",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, raw json.RawMessage) (string, error) {
		calls.Add(1)
		var args struct{ Message string }
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		return args.Message, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg, calls
}

// runWithHook drives one run to completion and returns the collected events
// plus final run status. The fake provider issues one echo_tool call, then
// completes on the next turn.
func runWithHook(t *testing.T, runner *harness.Runner) ([]harness.Event, harness.RunStatus) {
	t.Helper()
	run, err := runner.StartRun(harness.RunRequest{Prompt: "go"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	history, ch, unsubscribe, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	events := append([]harness.Event(nil), history...)
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events, harness.RunStatusFailed
			}
			events = append(events, ev)
			if ev.Type == harness.EventRunCompleted || ev.Type == harness.EventRunFailed {
				st, _ := runner.GetRun(run.ID)
				return events, st.Status
			}
		case <-timeout:
			t.Fatal("run did not reach a terminal event within 30s")
			return nil, ""
		}
	}
}

func toolCallProvider() *fakeprovider.Provider {
	return fakeprovider.New([]fakeprovider.Turn{
		{ToolCalls: []harness.ToolCall{{ID: "call_1", Name: "echo_tool", Arguments: `{"message":"hello"}`}}},
		{Content: "done"},
	})
}

// TestCommandHookDenyEndToEnd proves the core user story of the epic: a shell
// script denies a tool call, the tool never executes, and the deny reason
// flows back to the LLM as the tool result.
func TestCommandHookDenyEndToEnd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "deny.sh", `echo '{"decision":"deny","reason":"rm is not allowed"}'`)
	def := hooks.HookDef{
		Name: "deny-rm", Event: hooks.EventPreToolUse, Kind: hooks.KindCommand,
		Command: []string{script},
	}
	hook := hooks.NewCommandHook(def)

	reg, calls := echoRegistry(t)
	runner := harness.NewRunner(toolCallProvider(), reg, harness.RunnerConfig{
		DefaultModel:    "m",
		PreToolUseHooks: []harness.PreToolUseHook{hook},
	})

	events, status := runWithHook(t, runner)
	if status != harness.RunStatusCompleted {
		t.Fatalf("status: got %q, want completed", status)
	}
	if calls.Load() != 0 {
		t.Fatalf("denied tool executed %d times", calls.Load())
	}

	var denyEvent *harness.Event
	for i := range events {
		if events[i].Type == harness.EventToolHookCompleted {
			if decision, _ := events[i].Payload["decision"].(string); decision == "deny" {
				denyEvent = &events[i]
			}
		}
	}
	if denyEvent == nil {
		t.Fatal("no tool_hook.completed event with decision=deny")
	}
	if hookName, _ := denyEvent.Payload["hook"].(string); hookName != "deny-rm" {
		t.Fatalf("deny not attributable to hook name: payload=%v", denyEvent.Payload)
	}
	// Observability contract: every hook execution reports how long it took.
	if _, ok := denyEvent.Payload["duration_ms"]; !ok {
		t.Fatalf("tool_hook.completed event missing duration_ms: payload=%v", denyEvent.Payload)
	}
}

// TestCommandHookErrorHonorsFailureMode regression-protects the runner's
// existing hook failure policy with a config-driven adapter: fail_closed
// denies the tool, fail_open lets it execute.
func TestCommandHookErrorHonorsFailureMode(t *testing.T) {
	t.Parallel()

	newFailHook := func(t *testing.T) *hooks.CommandHook {
		script := writeScript(t, t.TempDir(), "fail.sh", `exit 2`)
		return hooks.NewCommandHook(hooks.HookDef{
			Name: "broken", Event: hooks.EventPreToolUse, Kind: hooks.KindCommand,
			Command: []string{script},
		})
	}

	t.Run("fail_closed denies the tool", func(t *testing.T) {
		t.Parallel()
		reg, calls := echoRegistry(t)
		runner := harness.NewRunner(toolCallProvider(), reg, harness.RunnerConfig{
			DefaultModel:    "m",
			PreToolUseHooks: []harness.PreToolUseHook{newFailHook(t)},
			HookFailureMode: harness.HookFailureModeFailClosed,
		})
		events, status := runWithHook(t, runner)
		if status != harness.RunStatusCompleted {
			t.Fatalf("status: got %q, want completed", status)
		}
		if calls.Load() != 0 {
			t.Fatal("fail_closed: tool executed despite hook error")
		}
		found := false
		for _, ev := range events {
			if ev.Type == harness.EventToolHookFailed {
				found = true
			}
		}
		if !found {
			t.Fatal("expected tool_hook.failed event")
		}
	})

	t.Run("fail_open lets the tool execute", func(t *testing.T) {
		t.Parallel()
		reg, calls := echoRegistry(t)
		runner := harness.NewRunner(toolCallProvider(), reg, harness.RunnerConfig{
			DefaultModel:    "m",
			PreToolUseHooks: []harness.PreToolUseHook{newFailHook(t)},
			HookFailureMode: harness.HookFailureModeFailOpen,
		})
		_, status := runWithHook(t, runner)
		if status != harness.RunStatusCompleted {
			t.Fatalf("status: got %q, want completed", status)
		}
		if calls.Load() != 1 {
			t.Fatalf("fail_open: tool executed %d times, want 1", calls.Load())
		}
	})
}

// TestCommandHookDenyReasonReachesLLM asserts the deny reason lands in the
// tool-result message the provider sees on the next turn.
func TestCommandHookDenyReasonReachesLLM(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "deny.sh", `echo '{"decision":"deny","reason":"policy: no deletes"}'`)
	hook := hooks.NewCommandHook(hooks.HookDef{
		Name: "guard", Event: hooks.EventPreToolUse, Kind: hooks.KindCommand,
		Command: []string{script},
	})

	provider := toolCallProvider()
	reg, _ := echoRegistry(t)
	runner := harness.NewRunner(provider, reg, harness.RunnerConfig{
		DefaultModel:    "m",
		PreToolUseHooks: []harness.PreToolUseHook{hook},
	})
	_, status := runWithHook(t, runner)
	if status != harness.RunStatusCompleted {
		t.Fatalf("status: got %q", status)
	}

	lastReq, ok := provider.LastRequest()
	if !ok {
		t.Fatal("provider saw no requests")
	}
	var toolMsg *harness.Message
	for i := range lastReq.Messages {
		if lastReq.Messages[i].Role == "tool" {
			toolMsg = &lastReq.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message sent back to the provider")
	}
	if !strings.Contains(toolMsg.Content, "policy: no deletes") {
		t.Fatalf("deny reason not visible to LLM; tool message: %q", toolMsg.Content)
	}
}

// TestHTTPHookPreMessageBlocksRun is the harness-level integration test for
// message events: an HTTP pre_message hook that blocks stops the run with
// the hook reason (regression coverage for applyPreHooks).
func TestHTTPHookPreMessageBlocksRun(t *testing.T) {
	t.Parallel()
	srv := newBlockServer(t, `{"action":"block","reason":"content policy"}`)
	hook := hooks.NewHTTPHook(hooks.HookDef{
		Name: "content-guard", Event: hooks.EventPreMessage, Kind: hooks.KindHTTP, URL: srv,
	})

	reg, calls := echoRegistry(t)
	runner := harness.NewRunner(toolCallProvider(), reg, harness.RunnerConfig{
		DefaultModel:    "m",
		PreMessageHooks: []harness.PreMessageHook{hook},
	})

	events, status := runWithHook(t, runner)
	if status != harness.RunStatusFailed {
		t.Fatalf("status: got %q, want failed (blocked run)", status)
	}
	if calls.Load() != 0 {
		t.Fatal("tool executed despite pre_message block")
	}

	run, err := lastRunError(runner, events)
	if err != nil {
		t.Fatalf("could not read run error: %v", err)
	}
	if !strings.Contains(run, "content policy") {
		t.Fatalf("run error missing hook reason: %q", run)
	}
}

// newBlockServer starts an httptest server that always responds 200 with the
// given body and returns its URL.
func newBlockServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// lastRunError extracts the run error message from the run.failed event.
func lastRunError(_ *harness.Runner, events []harness.Event) (string, error) {
	for _, ev := range events {
		if ev.Type == harness.EventRunFailed {
			if msg, ok := ev.Payload["error"].(string); ok {
				return msg, nil
			}
			if msg, ok := ev.Payload["reason"].(string); ok {
				return msg, nil
			}
			return "", fmt.Errorf("run.failed event has no error field: %v", ev.Payload)
		}
	}
	return "", fmt.Errorf("no run.failed event collected")
}
