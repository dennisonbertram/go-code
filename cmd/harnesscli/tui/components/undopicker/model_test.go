package undopicker_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/undopicker"
)

// conversationFixture returns a message history with three real prompts,
// interleaved meta noise, and a compaction summary, exercising every filter
// rule of EntriesFromMessages:
//
//	0 user      "first question"
//	1 assistant "first answer"
//	2 system    compaction summary (is_compact_summary)
//	3 user      "second question"
//	4 user      meta note (is_meta — not a prompt)
//	5 assistant "second answer"
//	6 user      "third question"
//	7 assistant "third answer"
func conversationFixture() []undopicker.MessageView {
	return []undopicker.MessageView{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "system", Content: "summary of earlier context", IsCompactSummary: true},
		{Role: "user", Content: "second question"},
		{Role: "user", Content: "hidden note", IsMeta: true},
		{Role: "assistant", Content: "second answer"},
		{Role: "user", Content: "third question"},
		{Role: "assistant", Content: "third answer"},
	}
}

func pickerEntries() []undopicker.UndoEntry {
	return []undopicker.UndoEntry{
		{Step: 6, Count: 1, Preview: "third question"},
		{Step: 3, Count: 2, Preview: "second question"},
		{Step: 0, Count: 3, Preview: "first question", Disabled: true},
	}
}

// ─── EntriesFromMessages ─────────────────────────────────────────────────────

func TestEntriesFromMessages_NewestFirstWithCounts(t *testing.T) {
	t.Parallel()

	msgs := []undopicker.MessageView{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
	entries := undopicker.EntriesFromMessages(msgs)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	want := []struct {
		step    int
		count   int
		preview string
	}{
		{4, 1, "q3"},
		{2, 2, "q2"},
		{0, 3, "q1"},
	}
	for i, w := range want {
		e := entries[i]
		if e.Step != w.step || e.Count != w.count || e.Preview != w.preview {
			t.Errorf("entries[%d] = %+v, want step=%d count=%d preview=%q", i, e, w.step, w.count, w.preview)
		}
		if e.Disabled {
			t.Errorf("entries[%d] unexpectedly disabled", i)
		}
	}
}

func TestEntriesFromMessages_SkipsMetaAndBoundary(t *testing.T) {
	t.Parallel()

	entries := undopicker.EntriesFromMessages(conversationFixture())
	// Three real prompts: third (step 6, count 1), second (step 3, count 2),
	// first (step 0, count 3 — at/below the compaction summary at step 2).
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (meta user message must not count): %+v", len(entries), entries)
	}
	if entries[0].Count != 1 || entries[0].Preview != "third question" || entries[0].Disabled {
		t.Errorf("entries[0] = %+v, want enabled count=1 third question", entries[0])
	}
	if entries[1].Count != 2 || entries[1].Preview != "second question" || entries[1].Disabled {
		t.Errorf("entries[1] = %+v, want enabled count=2 second question", entries[1])
	}
	if entries[2].Count != 3 || entries[2].Preview != "first question" || !entries[2].Disabled {
		t.Errorf("entries[2] = %+v, want DISABLED count=3 first question (below compaction)", entries[2])
	}
}

func TestEntriesFromMessages_DisablesAtTheSummaryStep(t *testing.T) {
	t.Parallel()

	// A prompt exactly at the summary's step cannot happen (steps are unique),
	// but multiple summaries are possible: the boundary is the MAX summary step.
	msgs := []undopicker.MessageView{
		{Role: "system", Content: "older summary", IsCompactSummary: true},
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "newer summary", IsCompactSummary: true},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	entries := undopicker.EntriesFromMessages(msgs)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Preview != "q2" || entries[0].Disabled {
		t.Errorf("entries[0] = %+v, want enabled q2 (above newer summary)", entries[0])
	}
	if entries[1].Preview != "q1" || !entries[1].Disabled {
		t.Errorf("entries[1] = %+v, want DISABLED q1 (below newer summary)", entries[1])
	}
}

func TestEntriesFromMessages_Empty(t *testing.T) {
	t.Parallel()

	if entries := undopicker.EntriesFromMessages(nil); len(entries) != 0 {
		t.Errorf("got %d entries for empty history, want 0", len(entries))
	}
}

func TestEntriesFromMessages_CapsAtMaxEntries(t *testing.T) {
	t.Parallel()

	var msgs []undopicker.MessageView
	for i := 0; i < 15; i++ {
		msgs = append(msgs,
			undopicker.MessageView{Role: "user", Content: "question"},
			undopicker.MessageView{Role: "assistant", Content: "answer"},
		)
	}
	entries := undopicker.EntriesFromMessages(msgs)
	if len(entries) != 10 {
		t.Fatalf("got %d entries, want the 10 newest", len(entries))
	}
	// Newest-first: counts run 1..10 (never 11+ — older prompts are unreachable).
	for i, e := range entries {
		if e.Count != i+1 {
			t.Errorf("entries[%d].Count = %d, want %d", i, e.Count, i+1)
		}
	}
}

// ─── Model state ─────────────────────────────────────────────────────────────

func TestModelStartsClosed(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries())
	if m.IsOpen() {
		t.Fatal("New() model should start closed")
	}
}

func TestModelOpenClose(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	if !m.IsOpen() {
		t.Fatal("Open() should set IsOpen")
	}
	m = m.Close()
	if m.IsOpen() {
		t.Fatal("Close() should clear IsOpen")
	}
}

func TestModelNavigationSkipsDisabled(t *testing.T) {
	t.Parallel()

	// Entries: [enabled count=1, enabled count=2, DISABLED count=3].
	m := undopicker.New(pickerEntries()).Open()

	sel, ok := m.Selected()
	if !ok || sel.Count != 1 {
		t.Fatalf("initial selection = %+v, want count=1", sel)
	}

	// Down from count=2 must skip the disabled count=3 and wrap to count=1.
	m = m.SelectDown()
	if sel, _ := m.Selected(); sel.Count != 2 {
		t.Fatalf("after down: selection = %+v, want count=2", sel)
	}
	m = m.SelectDown()
	if sel, _ := m.Selected(); sel.Count != 1 {
		t.Fatalf("down past disabled: selection = %+v, want wrap to count=1", sel)
	}

	// Up from count=1 must skip the disabled count=3 and wrap to count=2.
	m = m.SelectUp()
	if sel, _ := m.Selected(); sel.Count != 2 {
		t.Fatalf("up past disabled: selection = %+v, want wrap to count=2", sel)
	}
}

func TestModelEnterEmitsSelectedWithCount(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	m = m.SelectDown() // select count=2 ("second question")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on an enabled row must emit a command")
	}
	msg, ok := cmd().(undopicker.UndoSelectedMsg)
	if !ok {
		t.Fatalf("expected UndoSelectedMsg, got %T", cmd())
	}
	if msg.Entry.Count != 2 || msg.Entry.Preview != "second question" {
		t.Errorf("UndoSelectedMsg.Entry = %+v, want count=2 second question", msg.Entry)
	}
}

func TestModelEnterOnAllDisabledEmitsNothing(t *testing.T) {
	t.Parallel()

	disabled := []undopicker.UndoEntry{
		{Step: 0, Count: 1, Preview: "old question", Disabled: true},
	}
	m := undopicker.New(disabled).Open()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("Enter with only disabled rows must emit nothing, got %T", cmd())
	}
}

func TestModelEscapeCloses(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsOpen() {
		t.Fatal("Esc should close the picker")
	}
}

func TestModelVimKeysNavigate(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if sel, _ := m.Selected(); sel.Count != 2 {
		t.Fatalf("j: selection = %+v, want count=2", sel)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if sel, _ := m.Selected(); sel.Count != 1 {
		t.Fatalf("k: selection = %+v, want count=1", sel)
	}
}
