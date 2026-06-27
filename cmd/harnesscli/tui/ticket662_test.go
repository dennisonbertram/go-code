package tui_test

// ticket662_test.go — TDD tests for #662
// Part A: collapsed tool-call cards show summary, not raw JSON / file content.
// Part B: "ghost" running cards do not persist after completion.

import (
	"encoding/json"
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ---------------------------------------------------------------------------
// Part A — args summary (no raw JSON / file content in collapsed card)
// ---------------------------------------------------------------------------

// TestArgSummary_WriteToolShowsPathAndLineCount verifies that when a write/edit/create
// tool is started with a large "content" field, the collapsed card shows
// "<path> (N lines)" and does NOT include the file body text.
func TestArgSummary_WriteToolShowsPathAndLineCount(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662a-write"})
	m = m2.(tui.Model)

	// Build a large content with 25 lines that must NOT appear in the collapsed card.
	body := strings.Repeat("line content here\n", 25)
	input := map[string]interface{}{
		"path":    "/tmp/foo.go",
		"content": body,
	}
	raw, _ := json.Marshal(input)

	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw: func() []byte {
			payload := map[string]interface{}{
				"tool":      "write_file",
				"call_id":   "call-662a",
				"arguments": json.RawMessage(raw),
			}
			b, _ := json.Marshal(payload)
			return b
		}(),
	})
	m = m3.(tui.Model)

	view := m.View()

	// Must contain the path.
	if !strings.Contains(view, "/tmp/foo.go") {
		t.Errorf("collapsed card must show path; got view=%q", view)
	}
	// Must contain "(25 lines)" or similar line count indicator.
	if !strings.Contains(view, "(25 lines)") {
		t.Errorf("collapsed card must show line count like '(25 lines)'; got view=%q", view)
	}
	// Must NOT contain the file body.
	if strings.Contains(view, "line content here") {
		t.Errorf("collapsed card must NOT contain file body text; got view=%q", view)
	}
}

// TestArgSummary_ReadToolShowsPathOnly verifies that read/view tools show just the path.
func TestArgSummary_ReadToolShowsPathOnly(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662a-read"})
	m = m2.(tui.Model)

	input := map[string]interface{}{
		"path": "/tmp/bar.go",
	}
	raw, _ := json.Marshal(input)

	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw: func() []byte {
			payload := map[string]interface{}{
				"tool":      "read_file",
				"call_id":   "call-662a-read",
				"arguments": json.RawMessage(raw),
			}
			b, _ := json.Marshal(payload)
			return b
		}(),
	})
	m = m3.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "/tmp/bar.go") {
		t.Errorf("collapsed card for read_file must show path; view=%q", view)
	}
}

// TestArgSummary_BashToolShowsCommand verifies that bash tools show the command string.
func TestArgSummary_BashToolShowsCommand(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662a-bash"})
	m = m2.(tui.Model)

	input := map[string]interface{}{
		"command": "echo hello world",
	}
	raw, _ := json.Marshal(input)

	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw: func() []byte {
			payload := map[string]interface{}{
				"tool":      "bash",
				"call_id":   "call-662a-bash",
				"arguments": json.RawMessage(raw),
			}
			b, _ := json.Marshal(payload)
			return b
		}(),
	})
	m = m3.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "echo hello world") {
		t.Errorf("collapsed card for bash must show command; view=%q", view)
	}
}

// TestArgSummary_DoubleEncodedBashArgs reproduces the REAL wire format: the
// harness sends tool arguments as a JSON STRING whose contents are JSON
// ("arguments":"{\"command\":\"ls -l\"}"). The collapsed card must show the
// unwrapped command, not the escaped inner JSON.
func TestArgSummary_DoubleEncodedBashArgs(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662a-dbl"})
	m = m2.(tui.Model)

	// arguments is a JSON string of JSON (double-encoded), exactly as emitted.
	argsObject, _ := json.Marshal(map[string]interface{}{"command": "ls -l"})
	argsString, _ := json.Marshal(string(argsObject)) // -> "\"{\\\"command\\\":\\\"ls -l\\\"}\""

	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw: func() []byte {
			payload := map[string]interface{}{
				"tool":      "bash",
				"call_id":   "call-662a-dbl",
				"arguments": json.RawMessage(argsString),
			}
			b, _ := json.Marshal(payload)
			return b
		}(),
	})
	m = m3.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "ls -l") {
		t.Errorf("collapsed bash card must show unwrapped command 'ls -l'; view=%q", view)
	}
	if strings.Contains(view, `\"command\"`) || strings.Contains(view, `{"command"`) {
		t.Errorf("collapsed bash card must NOT show raw/escaped inner JSON; view=%q", view)
	}
}

// TestArgSummary_FallbackCapsBigJSON verifies that unknown tools with a big JSON args
// do NOT dump the whole JSON but instead show a capped snippet.
func TestArgSummary_FallbackCapsBigJSON(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662a-big"})
	m = m2.(tui.Model)

	// Build a large JSON blob that would overflow the collapsed line.
	bigValue := strings.Repeat("x", 200)
	input := map[string]interface{}{
		"someparam": bigValue,
	}
	raw, _ := json.Marshal(input)

	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw: func() []byte {
			payload := map[string]interface{}{
				"tool":      "custom_tool",
				"call_id":   "call-662a-big",
				"arguments": json.RawMessage(raw),
			}
			b, _ := json.Marshal(payload)
			return b
		}(),
	})
	m = m3.(tui.Model)

	view := m.View()
	// The full 200-char value must NOT appear verbatim.
	if strings.Contains(view, bigValue) {
		n := len(view)
		if n > 200 {
			n = 200
		}
		t.Errorf("collapsed card must NOT dump the full big JSON value; view=%q", view[:n])
	}
}

// ---------------------------------------------------------------------------
// Part B — ghost cards (running card persists after completion)
// ---------------------------------------------------------------------------

// TestGhostCard_NoGhostAfterAssistantDeltaThenComplete is the key regression test.
// Sequence: tool A started (running) → assistant delta "thinking..." (tail changes)
// → tool A completed. Expected: exactly one write_file card in view, no ghost running card.
//
// The ghost bug manifests as two separate write_file lines: one running (trailing …)
// and one completed — count of "write_file(" in the raw view must be exactly 1.
func TestGhostCard_NoGhostAfterAssistantDeltaThenComplete(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662b-ghost"})
	m = m2.(tui.Model)

	// 1. Tool A starts (running) — it will be the viewport tail.
	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"write_file","call_id":"cGhost","arguments":{"path":"/tmp/ghost.go","content":"hello\n"}}`),
	})
	m = m3.(tui.Model)

	// 2. Assistant delta arrives — appended to viewport, so write_file card is no
	//    longer the viewport tail. renderedToolCallID != "cGhost" after this.
	m4, _ := m.Update(tui.SSEEventMsg{
		EventType: "assistant.message.delta",
		Raw:       []byte(`{"content":"thinking..."}`),
	})
	m = m4.(tui.Model)

	// 3. Tool A completes — without the fix this appends a NEW completed card
	//    instead of updating the existing running one → ghost.
	m5, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"write_file","call_id":"cGhost","output":"ok","duration_ms":10}`),
	})
	m = m5.(tui.Model)

	view := m.View()

	// Count "write_file(" occurrences — the ghost bug produces 2 (one running, one completed).
	occurrences := strings.Count(view, "write_file(")
	if occurrences == 0 {
		t.Errorf("expected write_file card in view; view=%q", view)
	}
	if occurrences > 1 {
		t.Errorf("ghost card bug: write_file( appears %d times (want 1); view=%q", occurrences, view)
	}
}

// TestGhostCard_TwoSequentialToolsBothAppearOnce verifies that two sequential tool
// calls (read_file then write_file) both complete and both appear exactly once in the
// correct order.
func TestGhostCard_TwoSequentialToolsBothAppearOnce(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-662b-seq"})
	m = m2.(tui.Model)

	// Tool A (read_file): started then completed.
	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"read_file","call_id":"cSeqA","arguments":{"path":"/a.go"}}`),
	})
	m = m3.(tui.Model)

	m4, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"read_file","call_id":"cSeqA","output":"content of a","duration_ms":5}`),
	})
	m = m4.(tui.Model)

	// Tool B (write_file): started then completed.
	m5, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"write_file","call_id":"cSeqB","arguments":{"path":"/b.go","content":"hello\n"}}`),
	})
	m = m5.(tui.Model)

	m6, _ := m.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"write_file","call_id":"cSeqB","output":"ok","duration_ms":5}`),
	})
	m = m6.(tui.Model)

	view := m.View()

	// Each tool name must appear exactly once.
	countRead := strings.Count(view, "read_file(")
	countWrite := strings.Count(view, "write_file(")

	if countRead == 0 {
		t.Errorf("read_file card not found in view; view=%q", view)
	}
	if countRead > 1 {
		t.Errorf("read_file( appears %d times (want 1) — ghost card bug; view=%q", countRead, view)
	}
	if countWrite == 0 {
		t.Errorf("write_file card not found in view; view=%q", view)
	}
	if countWrite > 1 {
		t.Errorf("write_file( appears %d times (want 1) — ghost card bug; view=%q", countWrite, view)
	}

	// read_file must appear before write_file in the view.
	posRead := strings.Index(view, "read_file(")
	posWrite := strings.Index(view, "write_file(")
	if posRead >= 0 && posWrite >= 0 && posRead >= posWrite {
		t.Errorf("expected read_file( before write_file( in view; posRead=%d posWrite=%d", posRead, posWrite)
	}
}
