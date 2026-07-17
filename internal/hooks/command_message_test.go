package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
)

// TestCommandHook_MessageEvents covers pre_message/post_message for the
// command adapter: continue, block, error, and the stdin payload contract.
func TestCommandHook_MessageEvents(t *testing.T) {
	t.Parallel()

	t.Run("pre_message block", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, t.TempDir(), "msg.sh", `echo '{"action":"block","reason":"nope"}'`)
		hook := NewCommandHook(commandDef(t, script))
		result, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			RunID: "r1", Step: 1,
			Request: harness.CompletionRequest{Model: "m", Messages: []harness.Message{{Role: "user", Content: "hi"}}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != harness.HookActionBlock || result.Reason != "nope" {
			t.Fatalf("got %+v, want block/nope", result)
		}
	})

	t.Run("pre_message empty stdout is continue", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, t.TempDir(), "msg-empty.sh", `exit 0`)
		hook := NewCommandHook(commandDef(t, script))
		result, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != "" && result.Action != harness.HookActionContinue {
			t.Fatalf("got %+v, want continue", result)
		}
	})

	t.Run("pre_message hook failure is an error", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, t.TempDir(), "msg-fail.sh", `exit 1`)
		hook := NewCommandHook(commandDef(t, script))
		if _, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m"},
		}); err == nil {
			t.Fatal("expected error from failing message hook")
		}
	})

	t.Run("pre_message unknown action is an error", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, t.TempDir(), "msg-weird.sh", `echo '{"action":"explode"}'`)
		hook := NewCommandHook(commandDef(t, script))
		_, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m"},
		})
		if err == nil || !strings.Contains(err.Error(), "unknown action") {
			t.Fatalf("got %v, want unknown action error", err)
		}
	})

	t.Run("post_message block", func(t *testing.T) {
		t.Parallel()
		script := writeScript(t, t.TempDir(), "post-msg.sh", `echo '{"action":"block","reason":"bad output"}'`)
		hook := NewCommandHook(commandDef(t, script))
		result, err := hook.AfterMessage(context.Background(), harness.PostMessageHookInput{
			RunID: "r2", Step: 4,
			Request:  harness.CompletionRequest{Model: "m"},
			Response: harness.CompletionResult{Content: "answer"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != harness.HookActionBlock || result.Reason != "bad output" {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("message stdin payload golden fields", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		capture := filepath.Join(dir, "stdin.json")
		script := writeScript(t, dir, "capture-msg.sh", "cat > \"$1\"\nexit 0")
		def := commandDef(t, script)
		def.Command = []string{script, capture}
		hook := NewCommandHook(def)
		_, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			RunID: "r9", Step: 7,
			Request: harness.CompletionRequest{Model: "gpt-gold", Messages: []harness.Message{
				{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"},
			}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := os.ReadFile(capture)
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("stdin not JSON: %s", data)
		}
		if payload["event"] != "pre_message" {
			t.Errorf("event: got %v", payload["event"])
		}
		if payload["step"] != float64(7) {
			t.Errorf("step: got %v", payload["step"])
		}
		if payload["model"] != "gpt-gold" {
			t.Errorf("model: got %v", payload["model"])
		}
		if payload["message_count"] != float64(2) {
			t.Errorf("message_count: got %v", payload["message_count"])
		}
		if _, ok := payload["messages"]; ok {
			t.Errorf("messages must be omitted without include_messages (got %v)", payload["messages"])
		}
	})
}
