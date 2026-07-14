package tui_test

// Additional regression coverage for BUG C: pressing '/' while a search
// query is already in progress must not wipe the text the user already
// typed. EnterSearch() only sets searchActive = true and does not touch
// searchQuery, but this guards the actual key-routing wiring in
// tui/model.go end-to-end, since a user might plausibly hit '/' again out
// of habit (e.g. thinking of it as a generic "focus search" key) while
// already mid-query.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestBugC_Regression_SlashWhileAlreadySearchingKeepsQuery verifies that
// pressing '/' again after some text has already been typed does not clear
// or otherwise corrupt the in-progress query.
func TestBugC_Regression_SlashWhileAlreadySearchingKeepsQuery(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(tui.Model)
	m = typeIntoModel(m, "claude")

	if got := m.ModelSwitcher().SearchQuery(); got != "claude" {
		t.Fatalf("precondition: expected query %q, got %q", "claude", got)
	}

	// Press '/' again mid-query.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m3.(tui.Model)

	if got := m.ModelSwitcher().SearchQuery(); got != "claude" {
		t.Errorf("pressing '/' again mid-query should not change the query; got %q, want %q", got, "claude")
	}
	if !m.ModelSwitcher().SearchActive() {
		t.Error("search should still be active after pressing '/' again mid-query")
	}
}
