package tui_test

// unknown_command_test.go — regression coverage for unknown slash command
// feedback. Previously, pressing Enter on an unrecognized slash command while
// the autocomplete dropdown was active (with no matching suggestion to accept)
// silently swallowed the keypress: no feedback and the stale text stayed in the
// input. Users must always get a hint and a cleared input.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func TestUnknownSlashCommand_ShowsFeedbackAndClearsInput(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "/notacommand")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	if got := m.Input(); got != "" {
		t.Errorf("input must be cleared after submitting an unknown command; got %q", got)
	}
	if !strings.Contains(m.StatusMsg(), "Unknown command") {
		t.Errorf("expected an 'Unknown command' hint in the status bar; got %q", m.StatusMsg())
	}
}
