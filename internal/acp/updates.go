package acp

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// updateKind classifies a session/update update object for queue treatment.
type updateKind int

const (
	// kindDelta is a coalescable text delta (agent_message_chunk,
	// agent_thought_chunk): cheap to merge and safe to drop under pressure.
	kindDelta updateKind = iota
	// kindLifecycle is a tool-call lifecycle update (tool_call,
	// tool_call_update): never coalesced, dropped only in the pathological
	// case of a queue full of lifecycle updates.
	kindLifecycle
)

// updateQueueCapacity bounds one turn's buffered notifications. It is a var
// so tests can shrink it.
var updateQueueCapacity = 256

// translateRunEvent converts one harness SSE event into the ACP update object
// carried in a session/update notification's "update" member. ok is false for
// events that have no ACP mapping in this slice (including terminal run
// events, which the prompt handler maps to stop reasons instead).
func translateRunEvent(ev runEvent) (map[string]any, updateKind, bool) {
	switch ev.Type {
	case "assistant.message.delta", "assistant.thinking.delta":
		content := payloadString(ev.Data, "content")
		if content == "" {
			return nil, kindDelta, false
		}
		chunk := "agent_message_chunk"
		if ev.Type == "assistant.thinking.delta" {
			chunk = "agent_thought_chunk"
		}
		return map[string]any{
			"sessionUpdate": chunk,
			"content":       map[string]any{"type": "text", "text": content},
		}, kindDelta, true

	case "tool.call.started":
		callID := payloadString(ev.Data, "call_id")
		tool := payloadString(ev.Data, "tool")
		if callID == "" {
			return nil, kindLifecycle, false
		}
		return map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    callID,
			"title":         tool,
			"kind":          toolKind(tool),
			"status":        "in_progress",
		}, kindLifecycle, true

	case "tool.call.completed":
		callID := payloadString(ev.Data, "call_id")
		if callID == "" {
			return nil, kindLifecycle, false
		}
		update := map[string]any{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    callID,
		}
		if errText := payloadString(ev.Data, "error"); errText != "" {
			update["status"] = "failed"
			update["content"] = toolOutputContent(errText)
		} else {
			update["status"] = "completed"
			if out := payloadString(ev.Data, "output"); out != "" {
				update["content"] = toolOutputContent(out)
			}
		}
		return update, kindLifecycle, true
	}
	return nil, kindDelta, false
}

// toolOutputContent wraps tool output text in the ACP tool-call content
// shape: a single content block of type "content".
func toolOutputContent(text string) []map[string]any {
	return []map[string]any{{
		"type":    "content",
		"content": map[string]any{"type": "text", "text": text},
	}}
}

// payloadString extracts a string field from the "payload" member of a raw
// harness event JSON document. Missing fields and malformed JSON yield "".
func payloadString(data, field string) string {
	var doc struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		return ""
	}
	v, _ := doc.Payload[field].(string)
	return v
}

// toolKind maps a harness tool name onto an ACP ToolCallKind.
func toolKind(tool string) string {
	switch tool {
	case "bash", "exec_command", "shell", "job":
		return "execute"
	case "read", "read_file", "file_inspect", "ls", "glob":
		return "read"
	case "write", "write_file", "edit", "apply_patch":
		return "edit"
	case "grep", "search":
		return "search"
	case "web_fetch", "fetch":
		return "fetch"
	case "think":
		return "think"
	default:
		return "other"
	}
}

// updateQueue is one prompt turn's bounded notification buffer. It decouples
// the SSE reader from a slow editor: pushes never block — same-kind text
// deltas coalesce, deltas are dropped (counted) under pressure, and lifecycle
// updates evict buffered deltas rather than being dropped themselves.
type updateQueue struct {
	mu      sync.Mutex
	cond    sync.Cond
	items   []map[string]any
	kinds   []updateKind
	cap     int
	closed  bool
	dropped int
}

func newUpdateQueue(capacity int) *updateQueue {
	q := &updateQueue{cap: capacity}
	q.cond.L = &q.mu
	return q
}

// push enqueues an update without blocking. Under backpressure (a full
// queue) same-kind text deltas coalesce into the tail item; deltas that
// cannot coalesce are dropped (counted); lifecycle updates evict the oldest
// buffered delta rather than being dropped themselves. A non-full queue
// appends 1:1, so a healthy editor sees every event.
func (q *updateQueue) push(update map[string]any, kind updateKind) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if len(q.items) < q.cap {
		q.items = append(q.items, update)
		q.kinds = append(q.kinds, kind)
		q.cond.Signal()
		return
	}
	// Queue full: the editor is behind.
	if kind == kindDelta && q.kinds[len(q.kinds)-1] == kindDelta {
		last := q.items[len(q.items)-1]
		if last["sessionUpdate"] == update["sessionUpdate"] {
			if lc, ok := last["content"].(map[string]any); ok {
				if nc, ok := update["content"].(map[string]any); ok {
					lct, _ := lc["text"].(string)
					nct, _ := nc["text"].(string)
					lc["text"] = lct + nct
					return
				}
			}
		}
	}
	if kind == kindDelta {
		q.dropped++
		return
	}
	// Lifecycle updates are precious: evict the oldest buffered delta.
	for i, k := range q.kinds {
		if k == kindDelta {
			q.items = append(q.items[:i], q.items[i+1:]...)
			q.kinds = append(q.kinds[:i], q.kinds[i+1:]...)
			q.items = append(q.items, update)
			q.kinds = append(q.kinds, kind)
			q.cond.Signal()
			return
		}
	}
	q.dropped++ // pathological: queue full of lifecycle updates
}

// pop removes the oldest update, blocking while the queue is empty. ok is
// false once the queue is closed and fully drained.
func (q *updateQueue) pop() (map[string]any, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil, false
	}
	u := q.items[0]
	q.items = q.items[1:]
	q.kinds = q.kinds[1:]
	return u, true
}

// close marks the queue finished; buffered items still drain.
func (q *updateQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

func (q *updateQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *updateQueue) droppedCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// updateWriter drains one turn's queue onto the protocol channel as
// session/update notifications. Exactly one writer per turn keeps
// notifications strictly ordered.
type updateWriter struct {
	q         *updateQueue
	srv       *Server
	sessionID string
	diag      io.Writer
	done      chan struct{}
}

func startUpdateWriter(q *updateQueue, srv *Server, sessionID string, diag io.Writer) *updateWriter {
	w := &updateWriter{q: q, srv: srv, sessionID: sessionID, diag: diag, done: make(chan struct{})}
	go w.run()
	return w
}

func (w *updateWriter) run() {
	defer close(w.done)
	for {
		u, ok := w.q.pop()
		if !ok {
			return
		}
		err := w.srv.writeNotification("session/update", map[string]any{
			"sessionId": w.sessionID,
			"update":    u,
		})
		if err != nil {
			fmt.Fprintf(w.diag, "acp: write session/update: %v\n", err)
			return
		}
	}
}

// wait blocks until the queue is closed and fully written out.
func (w *updateWriter) wait() { <-w.done }
