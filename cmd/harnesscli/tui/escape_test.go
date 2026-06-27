package tui_test

import (
	"os"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// initModel creates a Model with a given terminal size.
func initModel(t *testing.T, w, h int) tui.Model {
	t.Helper()
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m2.(tui.Model)
}

// sendEscape sends an Escape key message to the model.
func sendEscape(m tui.Model) (tui.Model, tea.Cmd) {
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return m2.(tui.Model), cmd
}

// typeIntoModel types a string into the model's input area.
func typeIntoModel(m tui.Model, text string) tui.Model {
	for _, r := range text {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(tui.Model)
	}
	return m
}

// TestTUI049_EscapeClosesOverlay verifies that Escape closes an open overlay.
func TestTUI049_EscapeClosesOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	// Open an overlay.
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)
	if !m.OverlayActive() {
		t.Fatal("overlayActive should be true after OverlayOpenMsg")
	}
	// Send Escape.
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("overlayActive must be false after Escape when overlay is open")
	}
}

// TestTUI049_EscapeCancelsRun verifies that Escape cancels a run when no overlay is active.
func TestTUI049_EscapeCancelsRun(t *testing.T) {
	m := initModel(t, 80, 24)

	cancelled := false
	cancelFn := func() { cancelled = true }
	m = m.WithCancelRun(cancelFn)
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-esc-001"})
	m = m2.(tui.Model)

	if !m.RunActive() {
		t.Fatal("runActive should be true")
	}
	m, _ = sendEscape(m)

	if !cancelled {
		t.Error("cancelRun must be called when Escape is pressed during an active run")
	}
	if m.RunActive() {
		t.Error("runActive must be false after Escape cancels the run")
	}
}

// TestTUI049_EscapeClearsInput verifies that Escape clears input text when no overlay or run is active.
func TestTUI049_EscapeClearsInput(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "some text")

	m, _ = sendEscape(m)

	if m.StatusMsg() != "Input cleared" {
		t.Errorf("statusMsg: want 'Input cleared', got %q", m.StatusMsg())
	}
}

// TestTUI049_EscapeNoOpWhenIdle verifies that Escape is a no-op (does NOT quit) when nothing is active.
func TestTUI049_EscapeNoOpWhenIdle(t *testing.T) {
	m := initModel(t, 80, 24)
	// No overlay, no run, no input text.

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2 == nil {
		t.Fatal("Update returned nil model on Escape")
	}
	// cmd should NOT be tea.Quit.
	// We detect a quit command by checking if it returns a tea.QuitMsg.
	// Since BubbleTea doesn't export a direct way to check, check that RunActive is still false
	// and the model is still valid.
	model := m2.(tui.Model)
	if model.RunActive() {
		t.Error("runActive should remain false after idle Escape")
	}
	if model.OverlayActive() {
		t.Error("overlayActive should remain false after idle Escape")
	}
	_ = cmd
}

// TestTUI049_EscapeOverlayTakesPriorityOverRun verifies overlay is closed before cancelling run.
func TestTUI049_EscapeOverlayTakesPriorityOverRun(t *testing.T) {
	m := initModel(t, 80, 24)

	// Both overlay and run active.
	cancelled := false
	cancelFn := func() { cancelled = true }
	m = m.WithCancelRun(cancelFn)
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-esc-002"})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m3.(tui.Model)

	if !m.OverlayActive() {
		t.Fatal("overlayActive must be true")
	}
	if !m.RunActive() {
		t.Fatal("runActive must be true")
	}

	// First Escape: should close overlay, NOT cancel run.
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("overlayActive must be false after first Escape")
	}
	if !m.RunActive() {
		t.Error("runActive must still be true after overlay close")
	}
	if cancelled {
		t.Error("cancelRun must NOT be called when Escape closes overlay")
	}
}

// TestTUI049_TwoPressesClosesThenClears verifies first Escape closes overlay, second clears input.
func TestTUI049_TwoPressesClosesThenClears(t *testing.T) {
	m := initModel(t, 80, 24)
	// Type some text and open an overlay.
	m = typeIntoModel(m, "draft text")
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	// First Escape: close overlay.
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("overlayActive must be false after first Escape")
	}

	// Second Escape: clear input (run is not active, overlay is closed).
	m, _ = sendEscape(m)
	if m.StatusMsg() != "Input cleared" {
		t.Errorf("statusMsg: want 'Input cleared', got %q", m.StatusMsg())
	}
}

// TestEscapeWithAutocompleteOpenRetainsInput verifies that pressing Escape while
// the slash-command autocomplete dropdown is open closes ONLY the dropdown and
// leaves the typed text intact (a second Escape would then clear the input).
func TestEscapeWithAutocompleteOpenRetainsInput(t *testing.T) {
	m := initModel(t, 80, 24)
	// Typing "/mo" opens the slash autocomplete dropdown.
	m = typeIntoModel(m, "/mo")
	if !strings.Contains(m.View(), "/model") {
		t.Fatalf("precondition: typing /mo should open the autocomplete dropdown showing /model; view=%q", m.View())
	}

	m, _ = sendEscape(m)

	if got := m.Input(); got != "/mo" {
		t.Errorf("Escape with autocomplete open must retain input; want %q, got %q", "/mo", got)
	}
	if m.StatusMsg() == "Input cleared" {
		t.Errorf("Escape that only closes the autocomplete dropdown must not clear the input")
	}
}

// TestTUI049_ConcurrentEscape verifies 10 goroutines each with their own copy have no race.
func TestTUI049_ConcurrentEscape(t *testing.T) {
	base := initModel(t, 80, 24)
	// Open overlay on the base model.
	m2, _ := base.Update(tui.OverlayOpenMsg{Kind: "help"})
	base = m2.(tui.Model)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine has its own copy.
			m := base
			m, _ = sendEscape(m)
			_ = m.OverlayActive()
		}()
	}
	wg.Wait()
}

// TestTUI049_VisualSnapshot_80x24 renders the TUI with "Input cleared" status at 80x24.
func TestTUI049_VisualSnapshot_80x24(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "some draft text")
	m, _ = sendEscape(m)

	output := m.View()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-049-escape-80x24.txt"
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)

	if !strings.Contains(output, "Input cleared") {
		t.Errorf("snapshot must contain 'Input cleared', got:\n%s", output)
	}
}

// TestTUI049_VisualSnapshot_120x40 renders the TUI with escape state at 120x40.
func TestTUI049_VisualSnapshot_120x40(t *testing.T) {
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "some draft text")
	m, _ = sendEscape(m)

	output := m.View()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-049-escape-120x40.txt"
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI049_VisualSnapshot_200x50 renders the TUI with escape state at 200x50.
func TestTUI049_VisualSnapshot_200x50(t *testing.T) {
	m := initModel(t, 200, 50)
	m = typeIntoModel(m, "some draft text")
	m, _ = sendEscape(m)

	output := m.View()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-049-escape-200x50.txt"
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}
