package sessionpicker_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/sessionpicker"
)

// testTime is a fixed time used across tests for deterministic output.
var testTime = time.Date(2026, time.March, 14, 10, 0, 0, 0, time.UTC)

// testEntries returns a standard set of session entries for use across tests.
func testEntries() []sessionpicker.SessionEntry {
	return []sessionpicker.SessionEntry{
		{
			ID:        "abc12345-0000-0000-0000-000000000000",
			StartedAt: testTime,
			Model:     "gpt-4.1-mini",
			TurnCount: 5,
			LastMsg:   "How do I implement a binary search tree?",
		},
		{
			ID:        "def67890-0000-0000-0000-000000000000",
			StartedAt: testTime.Add(-24 * time.Hour),
			Model:     "gpt-4o",
			TurnCount: 12,
			LastMsg:   "Write a concurrent queue in Go",
		},
		{
			ID:        "fed11111-0000-0000-0000-000000000000",
			StartedAt: testTime.Add(-48 * time.Hour),
			Model:     "claude-3-opus",
			TurnCount: 3,
			LastMsg:   "Explain the actor model",
		},
	}
}

// writeSnapshot writes a visual snapshot to the package-local testdata/snapshots directory.
func writeSnapshot(t *testing.T, name, content string) {
	t.Helper()
	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing snapshot %s: %v", path, err)
	}
	t.Logf("snapshot written to %s", path)
}

// ─── Model state tests ────────────────────────────────────────────────────────

// TestTUI053_NewStartsClosed verifies that a newly created model is closed.
func TestTUI053_NewStartsClosed(t *testing.T) {
	m := sessionpicker.New(testEntries())
	if m.IsOpen() {
		t.Fatal("New() model should start closed")
	}
}

// TestTUI053_OpenSetsIsOpen verifies Open() makes the model open.
func TestTUI053_OpenSetsIsOpen(t *testing.T) {
	m := sessionpicker.New(testEntries())
	m = m.Open()
	if !m.IsOpen() {
		t.Fatal("Open() should set IsOpen() to true")
	}
}

// TestTUI053_CloseSetsIsOpen verifies Close() makes the model closed.
func TestTUI053_CloseSetsIsOpen(t *testing.T) {
	m := sessionpicker.New(testEntries())
	m = m.Open()
	m = m.Close()
	if m.IsOpen() {
		t.Fatal("Close() should set IsOpen() to false")
	}
}

// TestTUI053_SelectedReturnsFirstOnNew verifies Selected() returns the first entry by default.
func TestTUI053_SelectedReturnsFirstOnNew(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries)

	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false on non-empty list")
	}
	if e.ID != entries[0].ID {
		t.Errorf("Selected().ID = %q, want %q", e.ID, entries[0].ID)
	}
}

// TestTUI053_SelectedReturnsFalseOnEmpty verifies Selected() returns false for empty list.
func TestTUI053_SelectedReturnsFalseOnEmpty(t *testing.T) {
	m := sessionpicker.New(nil)
	_, ok := m.Selected()
	if ok {
		t.Error("Selected() should return ok=false on empty list")
	}
}

// TestTUI053_SelectDownAdvancesSelection verifies SelectDown() moves to the next entry.
func TestTUI053_SelectDownAdvancesSelection(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries)
	m = m.SelectDown()

	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after SelectDown()")
	}
	if e.ID != entries[1].ID {
		t.Errorf("After SelectDown(): ID = %q, want %q", e.ID, entries[1].ID)
	}
}

// TestTUI053_SelectDownWrapsAtEnd verifies SelectDown() wraps from last to first.
func TestTUI053_SelectDownWrapsAtEnd(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries)

	// Move to the last entry.
	for i := 0; i < len(entries)-1; i++ {
		m = m.SelectDown()
	}

	// One more SelectDown should wrap to first.
	m = m.SelectDown()
	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after wrap")
	}
	if e.ID != entries[0].ID {
		t.Errorf("SelectDown() wrap: ID = %q, want %q", e.ID, entries[0].ID)
	}
}

// TestTUI053_SelectUpWrapsAtStart verifies SelectUp() wraps from first to last.
func TestTUI053_SelectUpWrapsAtStart(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries)
	// At index 0 — SelectUp should wrap to last.
	m = m.SelectUp()

	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after SelectUp() wrap")
	}
	if e.ID != entries[len(entries)-1].ID {
		t.Errorf("SelectUp() wrap: ID = %q, want %q", e.ID, entries[len(entries)-1].ID)
	}
}

// TestTUI053_SelectUpMovesUp verifies SelectUp() moves selection up by one.
func TestTUI053_SelectUpMovesUp(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries)
	m = m.SelectDown() // now at index 1
	m = m.SelectUp()   // back to index 0

	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after SelectUp()")
	}
	if e.ID != entries[0].ID {
		t.Errorf("After SelectDown then SelectUp: ID = %q, want %q", e.ID, entries[0].ID)
	}
}

// TestTUI053_SelectUpOnEmptyNoOp verifies SelectUp() on empty list is a no-op.
func TestTUI053_SelectUpOnEmptyNoOp(t *testing.T) {
	m := sessionpicker.New(nil)
	m2 := m.SelectUp()
	_, ok := m2.Selected()
	if ok {
		t.Error("SelectUp() on empty list should still return ok=false from Selected()")
	}
}

// TestTUI053_SelectDownOnEmptyNoOp verifies SelectDown() on empty list is a no-op.
func TestTUI053_SelectDownOnEmptyNoOp(t *testing.T) {
	m := sessionpicker.New(nil)
	m2 := m.SelectDown()
	_, ok := m2.Selected()
	if ok {
		t.Error("SelectDown() on empty list should still return ok=false from Selected()")
	}
}

// TestTUI053_SetEntriesReplacesAndResets verifies SetEntries() replaces the list and resets selection.
func TestTUI053_SetEntriesReplacesAndResets(t *testing.T) {
	m := sessionpicker.New(testEntries())
	m = m.SelectDown() // move to index 1

	newEntries := []sessionpicker.SessionEntry{
		{ID: "new1", StartedAt: testTime, Model: "gpt-5", TurnCount: 1, LastMsg: "Hello"},
	}
	m = m.SetEntries(newEntries)

	e, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after SetEntries()")
	}
	if e.ID != "new1" {
		t.Errorf("After SetEntries(): ID = %q, want %q", e.ID, "new1")
	}
}

// TestTUI053_ImmutableValueSemantics verifies that methods return new copies and don't mutate the original.
func TestTUI053_ImmutableValueSemantics(t *testing.T) {
	m1 := sessionpicker.New(testEntries())
	m2 := m1.Open()
	m3 := m2.SelectDown()

	if m1.IsOpen() {
		t.Error("Open() should not mutate original model")
	}
	if !m2.IsOpen() {
		t.Error("m2 should be open")
	}

	e1, _ := m2.Selected()
	e3, _ := m3.Selected()
	if e1.ID == e3.ID {
		t.Error("SelectDown() should not mutate m2's selection")
	}
}

// ─── Key handling tests ───────────────────────────────────────────────────────

// TestTUI053_KeyUpCallsSelectUp verifies Up arrow key calls SelectUp.
func TestTUI053_KeyUpCallsSelectUp(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()
	m = m.SelectDown() // index 1

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	e, _ := m2.Selected()
	if e.ID != entries[0].ID {
		t.Errorf("Up key: ID = %q, want %q", e.ID, entries[0].ID)
	}
}

// TestTUI053_KeyDownCallsSelectDown verifies Down arrow key calls SelectDown.
func TestTUI053_KeyDownCallsSelectDown(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	e, _ := m2.Selected()
	if e.ID != entries[1].ID {
		t.Errorf("Down key: ID = %q, want %q", e.ID, entries[1].ID)
	}
}

// TestTUI053_KeyKCallsSelectUp verifies 'k' key calls SelectUp.
func TestTUI053_KeyKCallsSelectUp(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()
	m = m.SelectDown() // index 1

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	e, _ := m2.Selected()
	if e.ID != entries[0].ID {
		t.Errorf("'k' key: ID = %q, want %q", e.ID, entries[0].ID)
	}
}

// TestTUI053_KeyJCallsSelectDown verifies 'j' key calls SelectDown.
func TestTUI053_KeyJCallsSelectDown(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	e, _ := m2.Selected()
	if e.ID != entries[1].ID {
		t.Errorf("'j' key: ID = %q, want %q", e.ID, entries[1].ID)
	}
}

// TestTUI053_KeyEnterEmitsSessionSelectedMsg verifies Enter emits SessionSelectedMsg.
func TestTUI053_KeyEnterEmitsSessionSelectedMsg(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should return a non-nil command")
	}

	msg := cmd()
	selected, ok := msg.(sessionpicker.SessionSelectedMsg)
	if !ok {
		t.Fatalf("Enter command returned %T, want SessionSelectedMsg", msg)
	}
	if selected.Entry.ID != entries[0].ID {
		t.Errorf("SessionSelectedMsg.Entry.ID = %q, want %q", selected.Entry.ID, entries[0].ID)
	}
}

// TestTUI053_KeyEnterOnEmptyReturnsNilCmd verifies Enter on empty list returns nil command.
func TestTUI053_KeyEnterOnEmptyReturnsNilCmd(t *testing.T) {
	m := sessionpicker.New(nil).Open()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter on empty list should return nil command")
	}
}

// TestTUI053_KeyEscapeCloses verifies Escape closes the model.
func TestTUI053_KeyEscapeCloses(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.IsOpen() {
		t.Error("Escape should close the model")
	}
}

// TestTUI053_ClosedModelIgnoresKeys verifies that a closed model ignores key events.
func TestTUI053_ClosedModelIgnoresKeys(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries) // closed

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	e1, _ := m.Selected()
	e2, _ := m2.Selected()
	if e1.ID != e2.ID {
		t.Error("Closed model should ignore key events")
	}
}

// ─── View tests ───────────────────────────────────────────────────────────────

// TestTUI053_ViewReturnsEmptyWhenClosed verifies View() returns "" when closed.
func TestTUI053_ViewReturnsEmptyWhenClosed(t *testing.T) {
	m := sessionpicker.New(testEntries())
	v := m.View(80)
	if v != "" {
		t.Errorf("View() should return empty string when closed, got %q", v)
	}
}

// TestTUI053_ViewContainsSessionsTitle verifies View() contains "Sessions" title.
func TestTUI053_ViewContainsSessionsTitle(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	v := m.View(80)
	if !strings.Contains(v, "Sessions") {
		t.Errorf("View() should contain 'Sessions' title:\n%s", v)
	}
}

// TestTUI053_ViewContainsEntryData verifies View() contains entry data (ID prefix, model, etc).
func TestTUI053_ViewContainsEntryData(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()
	v := m.View(80)

	// Short ID (first 8 chars) should appear.
	shortID := entries[0].ID[:8]
	if !strings.Contains(v, shortID) {
		t.Errorf("View() should contain short ID %q:\n%s", shortID, v)
	}
	// Model name should appear.
	if !strings.Contains(v, entries[0].Model) {
		t.Errorf("View() should contain model %q:\n%s", entries[0].Model, v)
	}
}

// TestTUI053_ViewShowsNoSessionsOnEmptyList verifies View() shows "No sessions found" for empty list.
func TestTUI053_ViewShowsNoSessionsOnEmptyList(t *testing.T) {
	m := sessionpicker.New(nil).Open()
	v := m.View(80)
	if !strings.Contains(v, "No sessions found") {
		t.Errorf("View() should contain 'No sessions found' for empty list:\n%s", v)
	}
}

// TestTUI053_ViewScrollFooterOnLargeList verifies "... N more" footer appears when list exceeds 10 rows.
func TestTUI053_ViewScrollFooterOnLargeList(t *testing.T) {
	// Build 15 entries — more than maxVisibleRows (10).
	entries := make([]sessionpicker.SessionEntry, 15)
	for i := range entries {
		entries[i] = sessionpicker.SessionEntry{
			ID:        "aaaaaaaa-0000-0000-0000-000000000000",
			StartedAt: testTime,
			Model:     "gpt-4.1",
			TurnCount: i + 1,
			LastMsg:   "msg",
		}
	}

	m := sessionpicker.New(entries).Open()
	v := m.View(80)

	if !strings.Contains(v, "more") {
		t.Errorf("View() should contain 'more' footer for large list:\n%s", v)
	}
}

// TestTUI053_ViewNoPanicAtExtremeWidths verifies View() does not panic at extreme widths.
func TestTUI053_ViewNoPanicAtExtremeWidths(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()

	for _, w := range []int{10, 20, 80, 200} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View(%d) panicked: %v", w, r)
				}
			}()
			_ = m.View(w)
		}()
	}
}

// ─── Concurrency test ─────────────────────────────────────────────────────────

// TestTUI053_ConcurrentModels verifies 10 goroutines each with their own Model have no data races.
func TestTUI053_ConcurrentModels(t *testing.T) {
	entries := testEntries()
	var wg sync.WaitGroup
	wg.Add(10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			m := sessionpicker.New(entries)
			m = m.Open()
			m = m.SelectDown()
			m = m.SelectUp()
			_, _ = m.Selected()
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			_ = m.View(80)
			m = m.Close()
			_ = m.IsOpen()
		}(i)
	}
	wg.Wait()
}

// ─── Snapshot tests ───────────────────────────────────────────────────────────

// TestTUI053_VisualSnapshot_80x24 writes a visual snapshot at width=80.
func TestTUI053_VisualSnapshot_80x24(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	snapshot := m.View(80)
	writeSnapshot(t, "TUI-053-sessionpicker-80x24.txt", snapshot)

	if strings.TrimSpace(snapshot) == "" {
		t.Error("View() returned empty output at width 80")
	}
	if !strings.Contains(snapshot, "Sessions") {
		t.Error("snapshot should contain 'Sessions'")
	}
}

// TestTUI053_VisualSnapshot_120x40 writes a visual snapshot at width=120.
func TestTUI053_VisualSnapshot_120x40(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	snapshot := m.View(120)
	writeSnapshot(t, "TUI-053-sessionpicker-120x40.txt", snapshot)

	if strings.TrimSpace(snapshot) == "" {
		t.Error("View() returned empty output at width 120")
	}
}

// TestTUI053_VisualSnapshot_200x50 writes a visual snapshot at width=200.
func TestTUI053_VisualSnapshot_200x50(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	snapshot := m.View(200)
	writeSnapshot(t, "TUI-053-sessionpicker-200x50.txt", snapshot)

	if strings.TrimSpace(snapshot) == "" {
		t.Error("View() returned empty output at width 200")
	}
}
