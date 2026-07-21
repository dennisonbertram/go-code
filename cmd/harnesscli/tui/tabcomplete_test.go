package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// typeTab sends a Tab key to the model and returns the updated model.
func typeTab(m tui.Model) tui.Model {
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	return m2.(tui.Model)
}

// TestTabCompletion_SlashCommand verifies that typing "/he" + Tab completes to "/help ".
// This is the core BUG-5 regression: autocomplete was wired in inputarea but
// SetAutocompleteProvider was never called at startup so Tab was always a no-op.
// Note: "/h" is ambiguous now that "/history" is registered; "/he" uniquely matches "/help".
func TestTabCompletion_SlashCommand(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "/he")
	m = typeTab(m)

	// The only slash command starting with "/he" is "/help".
	// Single match → input becomes "/help " (with trailing space).
	got := m.Input()
	if got != "/help " {
		t.Errorf("Tab on /he: want %q, got %q", "/help ", got)
	}
}

// TestTabCompletion_MultiMatch verifies that typing "/" + Tab leaves the input
// unchanged (or completes to a common prefix if one exists) since all commands
// share the "/" prefix already.
func TestTabCompletion_MultiMatch(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "/")
	before := m.Input()
	m = typeTab(m)
	after := m.Input()

	// All commands start with "/" so the common prefix is "/" itself — the input
	// must not get longer or shorter after Tab when already at the common prefix.
	if after != before {
		// Accept either no change or a valid common-prefix extension.
		// The only valid extension would be a string that is a prefix of every
		// registered command. Validate that the result is a prefix of at least
		// one known command.
		knownCmds := []string{"/clear", "/help", "/context", "/stats", "/quit", "/export", "/subagents", "/model", "/history"}
		for _, cmd := range knownCmds {
			if strings.HasPrefix(cmd, after) {
				// A valid partial completion — acceptable.
				return
			}
		}
		t.Errorf("Tab on /: unexpected result %q (was %q)", after, before)
	}
}

// TestTabCompletion_NoMatchIsNoop verifies that Tab on a non-matching prefix is a no-op.
func TestTabCompletion_NoMatchIsNoop(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "/zzz")
	m = typeTab(m)

	if m.Input() != "/zzz" {
		t.Errorf("Tab with no match: want unchanged %q, got %q", "/zzz", m.Input())
	}
}

// TestTabCompletion_NonSlashIsNoop verifies that Tab on non-slash input is a no-op.
func TestTabCompletion_NonSlashIsNoop(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "hello")
	m = typeTab(m)

	if m.Input() != "hello" {
		t.Errorf("Tab on non-slash: want unchanged %q, got %q", "hello", m.Input())
	}
}

// TestRegression_TabCompletionWired is an explicit regression test for BUG-5.
// It verifies that the autocomplete provider is wired at startup (not nil) so
// that Tab key has an effect on slash-command input immediately after init,
// without any manual SetAutocompleteProvider call.
func TestRegression_TabCompletionWired(t *testing.T) {
	m := initModel(t, 80, 24)

	// Try all known prefix → expected completion pairs.
	cases := []struct {
		input string
		want  string
	}{
		{"/cl", "/clear "},
		{"/mod", "/model "},
		{"/suba", "/subagents "},
		{"/tas", "/tasks "},
		{"/qu", "/quit "},
		{"/ex", "/export "},
		{"/st", "/stats "},
	}

	for _, tc := range cases {
		// Fresh model for each sub-case.
		mc := initModel(t, 80, 24)
		mc = typeIntoModel(mc, tc.input)
		mc = typeTab(mc)
		got := mc.Input()
		if got != tc.want {
			t.Errorf("Tab on %q: want %q, got %q", tc.input, tc.want, got)
		}
	}

	_ = m // suppress unused variable
}

// TestTabCompletion_PersistsAfterResize verifies that autocomplete still works
// after a terminal resize (WindowSizeMsg re-creates the input component).
func TestTabCompletion_PersistsAfterResize(t *testing.T) {
	m := initModel(t, 80, 24)

	// Simulate a terminal resize.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(tui.Model)

	// Type a slash command prefix and Tab.
	m = typeIntoModel(m, "/cl")
	m = typeTab(m)

	if m.Input() != "/clear " {
		t.Errorf("Tab after resize: want %q, got %q", "/clear ", m.Input())
	}
}

// TestTabCompletion_AllRegisteredCommands verifies that every registered command
// can be completed from a unique enough prefix.
func TestTabCompletion_AllRegisteredCommands(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/cle", "/clear "},
		{"/hel", "/help "},
		{"/cont", "/context "},
		{"/mod", "/model "},
		{"/sta", "/stats "},
		{"/suba", "/subagents "},
		{"/qui", "/quit "},
		{"/exp", "/export "},
	}

	for _, tc := range cases {
		mc := initModel(t, 80, 24)
		mc = typeIntoModel(mc, tc.input)
		mc = typeTab(mc)
		got := mc.Input()
		if got != tc.want {
			t.Errorf("Tab on %q: want %q, got %q", tc.input, tc.want, got)
		}
	}
}
