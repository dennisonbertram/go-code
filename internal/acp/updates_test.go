package acp

import (
	"encoding/json"
	"testing"
)

func evJSON(typ string, payload map[string]any) runEvent {
	p, _ := json.Marshal(payload)
	data, _ := json.Marshal(map[string]any{"id": "run-1:1", "run_id": "run-1", "type": typ, "payload": json.RawMessage(p)})
	return runEvent{Type: typ, Data: string(data)}
}

func TestTranslateRunEvent(t *testing.T) {
	t.Run("message delta becomes agent_message_chunk", func(t *testing.T) {
		u, kind, ok := translateRunEvent(evJSON("assistant.message.delta", map[string]any{"step": 1, "content": "Hello"}))
		if !ok || kind != kindDelta {
			t.Fatalf("got ok=%v kind=%v", ok, kind)
		}
		if u["sessionUpdate"] != "agent_message_chunk" {
			t.Fatalf("sessionUpdate = %v", u["sessionUpdate"])
		}
		content, _ := u["content"].(map[string]any)
		if content["type"] != "text" || content["text"] != "Hello" {
			t.Fatalf("content = %v", content)
		}
	})

	t.Run("thinking delta becomes agent_thought_chunk", func(t *testing.T) {
		u, kind, ok := translateRunEvent(evJSON("assistant.thinking.delta", map[string]any{"step": 1, "content": "hmm"}))
		if !ok || kind != kindDelta {
			t.Fatalf("got ok=%v kind=%v", ok, kind)
		}
		if u["sessionUpdate"] != "agent_thought_chunk" {
			t.Fatalf("sessionUpdate = %v", u["sessionUpdate"])
		}
		content, _ := u["content"].(map[string]any)
		if content["text"] != "hmm" {
			t.Fatalf("content = %v", content)
		}
	})

	t.Run("empty deltas are not translated", func(t *testing.T) {
		for _, typ := range []string{"assistant.message.delta", "assistant.thinking.delta"} {
			if _, _, ok := translateRunEvent(evJSON(typ, map[string]any{"step": 1, "content": ""})); ok {
				t.Fatalf("empty %s delta must not produce an update", typ)
			}
		}
	})

	t.Run("tool.call.started becomes tool_call with stable id and in_progress", func(t *testing.T) {
		u, kind, ok := translateRunEvent(evJSON("tool.call.started", map[string]any{"call_id": "call-1", "tool": "bash", "arguments": "ls -la"}))
		if !ok || kind != kindLifecycle {
			t.Fatalf("got ok=%v kind=%v", ok, kind)
		}
		if u["sessionUpdate"] != "tool_call" {
			t.Fatalf("sessionUpdate = %v", u["sessionUpdate"])
		}
		if u["toolCallId"] != "call-1" {
			t.Fatalf("toolCallId = %v, want call-1", u["toolCallId"])
		}
		if u["title"] != "bash" {
			t.Fatalf("title = %v", u["title"])
		}
		if u["status"] != "in_progress" {
			t.Fatalf("status = %v, want in_progress", u["status"])
		}
		if u["kind"] != "execute" {
			t.Fatalf("kind = %v, want execute", u["kind"])
		}
	})

	t.Run("tool.call.completed becomes tool_call_update completed with output", func(t *testing.T) {
		u, kind, ok := translateRunEvent(evJSON("tool.call.completed", map[string]any{"call_id": "call-1", "tool": "bash", "output": "file.txt\n", "duration_ms": 12}))
		if !ok || kind != kindLifecycle {
			t.Fatalf("got ok=%v kind=%v", ok, kind)
		}
		if u["sessionUpdate"] != "tool_call_update" {
			t.Fatalf("sessionUpdate = %v", u["sessionUpdate"])
		}
		if u["toolCallId"] != "call-1" {
			t.Fatalf("toolCallId = %v, want call-1 (stable across start/complete)", u["toolCallId"])
		}
		if u["status"] != "completed" {
			t.Fatalf("status = %v", u["status"])
		}
		content, _ := u["content"].([]map[string]any)
		if len(content) == 0 {
			t.Fatalf("expected tool output content, got %v", u["content"])
		}
	})

	t.Run("tool.call.completed with error becomes failed", func(t *testing.T) {
		u, _, ok := translateRunEvent(evJSON("tool.call.completed", map[string]any{"call_id": "call-9", "tool": "bash", "error": "sandbox violation", "duration_ms": 3}))
		if !ok {
			t.Fatal("expected translation")
		}
		if u["status"] != "failed" {
			t.Fatalf("status = %v, want failed", u["status"])
		}
		if u["toolCallId"] != "call-9" {
			t.Fatalf("toolCallId = %v", u["toolCallId"])
		}
	})

	t.Run("unmapped events are ignored", func(t *testing.T) {
		for _, typ := range []string{"run.started", "usage.delta", "todos.updated", "llm.turn.requested", "run.completed", "tool.call.delta"} {
			if _, _, ok := translateRunEvent(evJSON(typ, map[string]any{"x": 1})); ok {
				t.Fatalf("%s must not produce an update in slice 3", typ)
			}
		}
	})
}

func TestToolKindMapping(t *testing.T) {
	cases := map[string]string{
		"bash":          "execute",
		"exec_command":  "execute",
		"read":          "read",
		"read_file":     "read",
		"ls":            "read",
		"glob":          "read",
		"edit":          "edit",
		"write":         "edit",
		"apply_patch":   "edit",
		"grep":          "search",
		"web_fetch":     "fetch",
		"think":         "think",
		"deploy":        "other",
		"some_new_tool": "other",
	}
	for tool, want := range cases {
		if got := toolKind(tool); got != want {
			t.Errorf("toolKind(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestUpdateQueueCoalescesDeltas(t *testing.T) {
	t.Run("no coalescing while the queue has room", func(t *testing.T) {
		q := newUpdateQueue(2)
		q.push(chunkUpdate("agent_message_chunk", "Hello"), kindDelta)
		q.push(chunkUpdate("agent_message_chunk", ", world"), kindDelta)
		if got := q.len(); got != 2 {
			t.Fatalf("healthy queue must stream 1:1, got %d items", got)
		}
		first, _ := q.pop()
		second, _ := q.pop()
		if first["content"].(map[string]any)["text"] != "Hello" || second["content"].(map[string]any)["text"] != ", world" {
			t.Fatalf("deltas mutated without backpressure: %v %v", first, second)
		}
	})

	t.Run("full queue coalesces same-kind deltas into the tail", func(t *testing.T) {
		q := newUpdateQueue(1)
		q.push(chunkUpdate("agent_message_chunk", "Hello"), kindDelta)
		q.push(chunkUpdate("agent_message_chunk", ", world"), kindDelta)
		if got := q.len(); got != 1 {
			t.Fatalf("full queue must coalesce same-kind deltas, got %d items", got)
		}
		if q.droppedCount() != 0 {
			t.Fatalf("coalescing is not a drop, dropped = %d", q.droppedCount())
		}
		item, _ := q.pop()
		if text := item["content"].(map[string]any)["text"]; text != "Hello, world" {
			t.Fatalf("coalesced text = %q, want %q", text, "Hello, world")
		}
	})

	t.Run("message and thought deltas never coalesce", func(t *testing.T) {
		q := newUpdateQueue(1)
		q.push(chunkUpdate("agent_message_chunk", "a"), kindDelta)
		q.push(chunkUpdate("agent_thought_chunk", "t"), kindDelta)
		if q.droppedCount() != 1 {
			t.Fatalf("different-kind delta on a full queue must drop, dropped = %d", q.droppedCount())
		}
		item, _ := q.pop()
		if item["content"].(map[string]any)["text"] != "a" {
			t.Fatalf("queued delta was polluted: %v", item)
		}
	})
}

func TestUpdateQueueLifecycleBreaksCoalescing(t *testing.T) {
	// A lifecycle update at the tail means a following delta has nothing to
	// merge into: on a full queue it drops rather than merging across.
	q := newUpdateQueue(2)
	q.push(chunkUpdate("agent_message_chunk", "a"), kindDelta)
	q.push(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1"}, kindLifecycle)
	q.push(chunkUpdate("agent_message_chunk", "b"), kindDelta)
	if q.droppedCount() != 1 {
		t.Fatalf("delta after lifecycle must drop on a full queue, dropped = %d", q.droppedCount())
	}
	first, _ := q.pop()
	second, _ := q.pop()
	if first["content"].(map[string]any)["text"] != "a" || second["sessionUpdate"] != "tool_call" {
		t.Fatalf("unexpected queue contents: %v %v", first, second)
	}
}

func TestUpdateQueueBackpressure(t *testing.T) {
	t.Run("deltas are dropped (counted) when full of lifecycle updates", func(t *testing.T) {
		q := newUpdateQueue(2)
		q.push(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1"}, kindLifecycle)
		q.push(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1"}, kindLifecycle)
		q.push(chunkUpdate("agent_message_chunk", "dropped"), kindDelta)
		if q.droppedCount() != 1 {
			t.Fatalf("dropped = %d, want 1", q.droppedCount())
		}
		if got := q.len(); got != 2 {
			t.Fatalf("queue mutated on drop, len = %d", got)
		}
	})

	t.Run("lifecycle updates evict buffered deltas, never get dropped", func(t *testing.T) {
		q := newUpdateQueue(2)
		q.push(chunkUpdate("agent_message_chunk", "old"), kindDelta)
		q.push(chunkUpdate("agent_message_chunk", "older"), kindDelta)
		q.push(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1"}, kindLifecycle)
		if got := q.len(); got != 2 {
			t.Fatalf("len = %d, want 2 (evicted oldest delta)", got)
		}
		first, _ := q.pop()
		if first["sessionUpdate"] != "agent_message_chunk" {
			t.Fatalf("oldest surviving item = %v, want the remaining delta", first["sessionUpdate"])
		}
		second, _ := q.pop()
		if second["sessionUpdate"] != "tool_call" {
			t.Fatalf("lifecycle update lost: %v", second)
		}
	})

	t.Run("pop drains after close then reports done", func(t *testing.T) {
		q := newUpdateQueue(4)
		q.push(chunkUpdate("agent_message_chunk", "tail"), kindDelta)
		q.close()
		if _, ok := q.pop(); !ok {
			t.Fatal("buffered item must drain after close")
		}
		if _, ok := q.pop(); ok {
			t.Fatal("empty closed queue must report done")
		}
	})
}

// chunkUpdate builds an agent_message_chunk/agent_thought_chunk update object.
func chunkUpdate(kind, text string) map[string]any {
	return map[string]any{
		"sessionUpdate": kind,
		"content":       map[string]any{"type": "text", "text": text},
	}
}
