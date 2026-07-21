package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Tests in this file cover epic #817 slice 4: compaction summary blocks in the
// transcript with a ctrl+o expand/collapse toggle, for both manual /compact
// results (CompactResultMsg) and auto_compact.* SSE events.
// Helper testRunControlModel lives in run_control_command_test.go.

// TestCompactResultMsg_RendersCollapsedBlock verifies a successful /compact
// result renders a collapsed-by-default transcript block: the one-line title
// is visible while the summary stays hidden, and the slice-3 status line is
// unchanged.
func TestCompactResultMsg_RendersCollapsedBlock(t *testing.T) {
	m := testRunControlModel("http://localhost:8080")

	m2, _ := m.Update(CompactResultMsg{
		RunID:           "run-1",
		Mode:            "hybrid",
		Summary:         "kept the SQL schema",
		MessagesRemoved: 3,
	})
	m = m2.(Model)

	view := m.View()
	if !strings.Contains(view, "Compacted context — 3 messages removed") {
		t.Errorf("view missing collapsed compaction title:\n%s", view)
	}
	if strings.Contains(view, "kept the SQL schema") {
		t.Errorf("summary must be hidden while collapsed:\n%s", view)
	}
	// Slice-3 status line is preserved.
	if !strings.Contains(m.StatusMsg(), "3 messages removed") {
		t.Errorf("StatusMsg() = %q, want messages-removed report", m.StatusMsg())
	}

	if len(m.compactionBlocks) != 1 {
		t.Fatalf("expected 1 compaction block, got %d", len(m.compactionBlocks))
	}
	if m.compactionBlocks[0].expanded {
		t.Error("block must start collapsed")
	}
}

// TestCompactionBlock_CtrlOTogglesExpandCollapse verifies ctrl+o expands the
// collapsed block to reveal the summary, then collapses it again — and that
// with a block present, ctrl+o toggles the block rather than plan mode.
func TestCompactionBlock_CtrlOTogglesExpandCollapse(t *testing.T) {
	m := testRunControlModel("http://localhost:8080")

	m2, _ := m.Update(CompactResultMsg{
		RunID:           "run-1",
		Mode:            "hybrid",
		Summary:         "kept the SQL schema",
		MessagesRemoved: 3,
	})
	m = m2.(Model)

	// Expand.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = m3.(Model)
	if !m.compactionBlocks[0].expanded {
		t.Fatal("expected block expanded after first ctrl+o")
	}
	if v := m.View(); !strings.Contains(v, "kept the SQL schema") {
		t.Errorf("expanded view must show the summary:\n%s", v)
	}
	if m.PlanMode() {
		t.Error("ctrl+o must toggle the compaction block, not plan mode, when a block exists")
	}

	// Collapse again.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = m4.(Model)
	if m.compactionBlocks[0].expanded {
		t.Fatal("expected block collapsed after second ctrl+o")
	}
	if v := m.View(); strings.Contains(v, "kept the SQL schema") {
		t.Errorf("collapsed view must hide the summary again:\n%s", v)
	}
}

// TestAutoCompactEventsRenderAndUpdateBlock verifies auto_compact.started
// renders an in-progress block and auto_compact.completed updates that same
// block (not a duplicate) with before/after token counts.
func TestAutoCompactEventsRenderAndUpdateBlock(t *testing.T) {
	m := testRunControlModel("http://localhost:8080")

	m2, _ := m.Update(SSEEventMsg{
		EventType: "auto_compact.started",
		Raw:       []byte(`{"estimated_tokens":1200,"context_window":2000,"threshold":0.8,"ratio":0.9,"mode":"hybrid"}`),
	})
	m = m2.(Model)

	if v := m.View(); !strings.Contains(v, "Auto-compacting context") {
		t.Fatalf("view missing auto-compact started block:\n%s", v)
	}
	if len(m.compactionBlocks) != 1 {
		t.Fatalf("expected 1 block after started, got %d", len(m.compactionBlocks))
	}

	m3, _ := m.Update(SSEEventMsg{
		EventType: "auto_compact.completed",
		Raw:       []byte(`{"before_tokens":1200,"after_tokens":300,"mode":"hybrid"}`),
	})
	m = m3.(Model)

	if len(m.compactionBlocks) != 1 {
		t.Fatalf("completed must update the started block, not append: got %d blocks", len(m.compactionBlocks))
	}
	if v := m.View(); !strings.Contains(v, "Auto-compacted context — 1200 → 300 tokens (hybrid)") {
		t.Errorf("view missing completed title with token counts:\n%s", v)
	}

	// Expanded details carry the token counts.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = m4.(Model)
	v := m.View()
	for _, want := range []string{"Before: 1200 tokens", "After: 300 tokens"} {
		if !strings.Contains(v, want) {
			t.Errorf("expanded auto-compact block missing %q:\n%s", want, v)
		}
	}
}

// TestAutoCompactCompletedWithoutStartedAppendsBlock verifies a completed
// event that arrives without a matching started (e.g. after an SSE reconnect)
// still renders a block.
func TestAutoCompactCompletedWithoutStartedAppendsBlock(t *testing.T) {
	m := testRunControlModel("http://localhost:8080")

	m2, _ := m.Update(SSEEventMsg{
		EventType: "auto_compact.completed",
		Raw:       []byte(`{"before_tokens":900,"after_tokens":200,"mode":"strip"}`),
	})
	m = m2.(Model)

	if len(m.compactionBlocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.compactionBlocks))
	}
	if v := m.View(); !strings.Contains(v, "Auto-compacted context — 900 → 200 tokens (strip)") {
		t.Errorf("view missing completed title:\n%s", v)
	}
}

// TestCompactionBlock_CtrlOPrefersActiveToolOverBlock is a precedence
// regression guard: with an active tool call in flight, ctrl+o expands the
// tool card and leaves the compaction block collapsed.
func TestCompactionBlock_CtrlOPrefersActiveToolOverBlock(t *testing.T) {
	m := testRunControlModel("http://localhost:8080")
	m = m.WithCancelRun(func() {})

	m2, _ := m.Update(RunStartedMsg{RunID: "run-compact-reg"})
	m = m2.(Model)
	m3, _ := m.Update(SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"bash","call_id":"call-compactreg","arguments":"echo hi"}`),
	})
	m = m3.(Model)
	m4, _ := m.Update(CompactResultMsg{
		RunID:           "run-compact-reg",
		Mode:            "hybrid",
		Summary:         "kept the SQL schema",
		MessagesRemoved: 2,
	})
	m = m4.(Model)

	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = m5.(Model)

	if m.compactionBlocks[0].expanded {
		t.Error("active tool expansion must keep priority: block must stay collapsed")
	}
	if !m.toolExpanded["call-compactreg"] {
		t.Error("expected the active tool call to be expanded by ctrl+o")
	}
	if m.PlanMode() {
		t.Error("ctrl+o with an active tool must not toggle plan mode")
	}
}
