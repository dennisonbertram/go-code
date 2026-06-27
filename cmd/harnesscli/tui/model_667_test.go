package tui_test

// Tests for #667c: "/" typed while the model overlay is open must not leak into
// the model-switcher search query. Route through HandleSearchKey instead of
// raw SetSearch so the component's "/" swallow is respected.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestIssue667_SlashNotInSearch verifies that typing "/" while the model overlay
// is open does NOT append "/" to the search query.
func TestIssue667_SlashNotInSearch(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")
	if !m.OverlayActive() || m.ActiveOverlay() != "model" {
		t.Fatal("precondition: model overlay must be open after /model")
	}

	// Initial search query must be empty.
	if q := m.ModelSwitcher().SearchQuery(); q != "" {
		t.Fatalf("precondition: SearchQuery must be empty at open, got %q", q)
	}

	// Type "/" — this must NOT be appended to the search query.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	after := m2.(tui.Model)

	got := after.ModelSwitcher().SearchQuery()
	if strings.Contains(got, "/") {
		t.Errorf("'/' must not leak into model search query; got %q", got)
	}
}

// TestIssue667_NormalCharDoesAppend verifies that a regular printable character
// (e.g. 'g') IS still appended to the search query (regression guard).
func TestIssue667_NormalCharDoesAppend(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")
	if !m.OverlayActive() || m.ActiveOverlay() != "model" {
		t.Fatal("precondition: model overlay must be open after /model")
	}

	// Type 'g' — should appear in search query.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	after := m2.(tui.Model)

	got := after.ModelSwitcher().SearchQuery()
	if !strings.Contains(got, "g") {
		t.Errorf("'g' must be appended to model search query; got %q", got)
	}
}
