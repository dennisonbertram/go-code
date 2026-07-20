package themepicker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sampleEntries() []ThemeEntry {
	return []ThemeEntry{
		{Name: "default-dark", Builtin: true, Active: true},
		{Name: "default-light", Builtin: true},
		{Name: "ocean"},
	}
}

// TestNavigationWraps verifies up/down selection wraps around both ends.
func TestNavigationWraps(t *testing.T) {
	m := New(sampleEntries()).Open()

	m = m.SelectUp()
	if e, _ := m.Selected(); e.Name != "ocean" {
		t.Errorf("SelectUp from first entry should wrap to last, got %q", e.Name)
	}
	m = m.SelectDown()
	if e, _ := m.Selected(); e.Name != "default-dark" {
		t.Errorf("SelectDown from last entry should wrap to first, got %q", e.Name)
	}
	m = m.SelectDown()
	if e, _ := m.Selected(); e.Name != "default-light" {
		t.Errorf("SelectDown should move to second entry, got %q", e.Name)
	}
}

// TestEnterEmitsThemeSelectedMsg verifies Enter produces a ThemeSelectedMsg
// carrying the highlighted entry.
func TestEnterEmitsThemeSelectedMsg(t *testing.T) {
	m := New(sampleEntries()).Open().SelectDown()

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = m2
	if cmd == nil {
		t.Fatal("Enter on a selection must emit a command")
	}
	msg, ok := cmd().(ThemeSelectedMsg)
	if !ok {
		t.Fatalf("command message type = %T, want ThemeSelectedMsg", cmd())
	}
	if msg.Entry.Name != "default-light" {
		t.Errorf("ThemeSelectedMsg.Entry = %q, want default-light", msg.Entry.Name)
	}
}

// TestEscCloses verifies Escape closes the overlay without selecting.
func TestEscCloses(t *testing.T) {
	m := New(sampleEntries()).Open()
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.IsOpen() {
		t.Error("picker should be closed after Esc")
	}
	if cmd != nil {
		t.Error("Esc must not emit a selection command")
	}
}

// TestSetEntriesResetsSelection verifies re-scanning resets the cursor.
func TestSetEntriesResetsSelection(t *testing.T) {
	m := New(sampleEntries()).Open().SelectDown().SelectDown()
	m = m.SetEntries([]ThemeEntry{{Name: "default-dark", Builtin: true, Active: true}})
	if e, _ := m.Selected(); e.Name != "default-dark" {
		t.Errorf("SetEntries must reset selection to first entry, got %q", e.Name)
	}
	if got := len(m.Entries()); got != 1 {
		t.Errorf("Entries() len = %d, want 1", got)
	}
}

// TestViewListsEntries verifies names, built-in tags, and the active marker
// are visible, and that a closed picker renders nothing.
func TestViewListsEntries(t *testing.T) {
	m := New(sampleEntries()).Open()
	m.Width = 80
	out := m.View()
	for _, want := range []string{"default-dark", "default-light", "ocean", "built-in", "(active)"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q: %q", want, out)
		}
	}

	closed := New(sampleEntries())
	if got := closed.View(); got != "" {
		t.Errorf("closed picker View() = %q, want empty", got)
	}
}

// TestViewEmptyState verifies the picker explains when no themes exist.
func TestViewEmptyState(t *testing.T) {
	m := New(nil).Open()
	m.Width = 80
	if out := m.View(); !strings.Contains(out, "No themes found") {
		t.Errorf("empty picker View() missing empty-state message: %q", out)
	}
}
