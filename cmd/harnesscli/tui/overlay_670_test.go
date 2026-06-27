package tui_test

// Tests for #670-keys: Up/Down (and j/k) keys should scroll the help dialog
// when the help overlay is open. Previously only Tab/Shift+Tab/h/l were wired.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestIssue670_DownScrollsHelpDialog verifies that pressing KeyDown while the
// help overlay is open increments the help dialog scroll offset.
func TestIssue670_DownScrollsHelpDialog(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	if !m.OverlayActive() || m.ActiveOverlay() != "help" {
		t.Fatal("precondition: help overlay must be open")
	}

	initialOffset := m.HelpDialogScrollOffset()

	// Press Down — scroll offset must increase.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)

	afterDown := m.HelpDialogScrollOffset()
	if afterDown <= initialOffset {
		t.Errorf("KeyDown should increase help dialog scroll offset: before=%d after=%d", initialOffset, afterDown)
	}
}

// TestIssue670_JScrollsHelpDialog verifies that pressing 'j' (vim-style) while
// the help overlay is open scrolls down.
func TestIssue670_JScrollsHelpDialog(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	initialOffset := m.HelpDialogScrollOffset()

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = m3.(tui.Model)

	afterJ := m.HelpDialogScrollOffset()
	if afterJ <= initialOffset {
		t.Errorf("'j' key should increase help dialog scroll offset: before=%d after=%d", initialOffset, afterJ)
	}
}

// TestIssue670_UpScrollsClampsAtZero verifies that pressing KeyUp when already
// at the top (offset=0) does not go negative.
func TestIssue670_UpScrollsClampsAtZero(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	// Offset is 0 at open; pressing Up should stay at 0.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m3.(tui.Model)

	if m.HelpDialogScrollOffset() < 0 {
		t.Errorf("scroll offset must not go negative on Up at top; got %d", m.HelpDialogScrollOffset())
	}
}

// TestIssue670_DownThenUpScrolls verifies that pressing Down then Up decreases
// the offset back toward 0 (round-trip check).
func TestIssue670_DownThenUpScrolls(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	// Press Down twice.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m4.(tui.Model)
	afterTwoDown := m.HelpDialogScrollOffset()

	// Press Up once.
	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m5.(tui.Model)
	afterUp := m.HelpDialogScrollOffset()

	if afterUp >= afterTwoDown {
		t.Errorf("Up should decrease scroll offset: after-2-down=%d after-up=%d", afterTwoDown, afterUp)
	}
}

// TestIssue670_TabStillWorksAfterScrolling verifies that Tab navigation still
// works after scrolling (regression guard for the existing tab behaviour).
func TestIssue670_TabStillWorksAfterScrolling(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	initialTab := m.HelpDialogActiveTab()

	// Scroll down first.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)

	// Then Tab — should still advance tab.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = m4.(tui.Model)

	afterTab := m.HelpDialogActiveTab()
	if afterTab == initialTab {
		t.Errorf("Tab should still advance tab after scrolling: before=%d after=%d", initialTab, afterTab)
	}
}

// TestIssue670_ViewChangesOnScroll verifies that the rendered view actually
// changes after pressing Down (e.g. overflow indicator appears or content shifts).
func TestIssue670_ViewChangesOnScroll(t *testing.T) {
	// Use a small height so the help dialog definitely clips content.
	m := initModel(t, 80, 20)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	viewBefore := m.View()

	// Press Down several times to scroll past the top content.
	for i := 0; i < 3; i++ {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}

	viewAfter := m.View()
	if viewBefore == viewAfter {
		t.Error("View() should change after pressing Down in the help dialog")
	}
}

// TestIssue670_ReopenResetsScroll verifies that closing and reopening /help
// resets the scroll offset to 0 (behaviour from Open() reset in helpdialog).
func TestIssue670_ReopenResetsScroll(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)

	// Scroll down.
	for i := 0; i < 5; i++ {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}
	if m.HelpDialogScrollOffset() == 0 {
		t.Skip("scroll offset did not increase — content may not be tall enough to clip")
	}

	// Close and reopen.
	m, _ = sendEscape(m)
	m4, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m4.(tui.Model)

	if m.HelpDialogScrollOffset() != 0 {
		t.Errorf("reopening /help should reset scroll offset to 0; got %d", m.HelpDialogScrollOffset())
	}

	// Tab should start at Commands (0) again.
	if m.HelpDialogActiveTab() != 0 {
		t.Errorf("reopening /help should reset to Commands tab (0); got %d", m.HelpDialogActiveTab())
	}

	// The first command should be visible (not scrolled past).
	view := m.View()
	if !strings.Contains(view, "Commands") {
		t.Errorf("after reopen, help dialog must show Commands tab; got:\n%s", view)
	}
}
