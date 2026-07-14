package tui_test

// Tests for user-reported BUG C (P2) — the model switcher's search key
// routing in the model-overlay region of model.go:
//
//   (1) '/' is advertised by the level-0 footer ("/ search") but was a dead
//       key — nothing in model.go ever entered a discoverable search mode.
//   (2) At browse level 1 with no active search, 's' toggles star, so typing
//       a query starting with "sonnet" starred the highlighted model on the
//       very first keystroke instead of starting a filter.
//   (3) The level-1 footer never mentioned search at all, even though any
//       other printable key silently started one.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestBugC_SlashEntersSearchMode verifies that pressing '/' while the model
// overlay is open activates search mode (matching what the footer already
// advertises), even before any character has been typed.
func TestBugC_SlashEntersSearchMode(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")
	if !m.OverlayActive() || m.ActiveOverlay() != "model" {
		t.Fatal("precondition: model overlay must be open after /model")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	after := m2.(tui.Model)

	if !after.ModelSwitcher().SearchActive() {
		t.Error("pressing '/' should activate search mode (ModelSwitcher().SearchActive())")
	}
	if after.ModelSwitcher().SearchQuery() != "" {
		t.Errorf("pressing '/' alone must not add text to the query, got %q", after.ModelSwitcher().SearchQuery())
	}

	v := after.View()
	if !strings.Contains(v, "Filter:") {
		t.Errorf("View() after pressing '/' should show the Filter search bar:\n%s", v)
	}
}

// TestBugC_TypingSonnetAfterSlashDoesNotStar reproduces the exact user
// complaint: typing "sonnet" to search for a Claude model must not star the
// highlighted model on the first keystroke ('s'). Once search mode is
// explicitly entered via '/', every printable rune (including 's') is a
// literal query character.
func TestBugC_TypingSonnetAfterSlashDoesNotStar(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// Drill into the first provider so we're at browse level 1, where 's'
	// would otherwise toggle star.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)
	if m.ModelSwitcher().BrowseLevel() != 1 {
		t.Fatal("precondition: expected browseLevel == 1 after drilling into a provider")
	}

	selected, _ := m.ModelSwitcher().Accept()
	wasStarred := m.ModelSwitcher().IsStarred(selected.ID)

	// Enter search mode explicitly via '/'.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m3.(tui.Model)

	// Now type "sonnet" — the leading 's' must be treated as a literal query
	// character, not a star toggle.
	m = typeIntoModel(m, "sonnet")

	got := m.ModelSwitcher().SearchQuery()
	if got != "sonnet" {
		t.Errorf("after '/' then typing 'sonnet', SearchQuery() = %q, want %q", got, "sonnet")
	}
	if m.ModelSwitcher().IsStarred(selected.ID) != wasStarred {
		t.Errorf("typing 'sonnet' after explicit search entry must not toggle star on %q", selected.ID)
	}
}

// TestBugC_SKeyStillStarsWhileMerelyBrowsing is the regression guard for
// existing behavior: without pressing '/' first, 's' at browse level 1 must
// still toggle star exactly as before.
func TestBugC_SKeyStillStarsWhileMerelyBrowsing(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)
	if m.ModelSwitcher().BrowseLevel() != 1 {
		t.Fatal("precondition: expected browseLevel == 1 after drilling into a provider")
	}
	if m.ModelSwitcher().SearchActive() {
		t.Fatal("precondition: search must not be active yet")
	}

	selected, _ := m.ModelSwitcher().Accept()
	wasStarred := m.ModelSwitcher().IsStarred(selected.ID)

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m3.(tui.Model)

	if m.ModelSwitcher().SearchQuery() != "" {
		t.Errorf("'s' while merely browsing should not start a search, got query %q", m.ModelSwitcher().SearchQuery())
	}
	if m.ModelSwitcher().IsStarred(selected.ID) == wasStarred {
		t.Error("'s' while merely browsing (no search active) should still toggle star")
	}
}

// TestBugC_Level1FooterMentionsSearch verifies the level-1 footer documents
// the '/' search shortcut, instead of leaving it as an undocumented side
// effect of typing any other character.
func TestBugC_Level1FooterMentionsSearch(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)
	if m.ModelSwitcher().BrowseLevel() != 1 {
		t.Fatal("precondition: expected browseLevel == 1 after drilling into a provider")
	}

	v := m.View()
	if !strings.Contains(v, "/ search") {
		t.Errorf("level-1 footer should document the '/' search shortcut:\n%s", v)
	}
}

// TestBugC_EscapeExitsEmptySearchMode verifies that pressing Escape right
// after '/' (before any character is typed) exits search mode rather than
// leaving an orphaned, empty search bar or jumping back an extra level.
func TestBugC_EscapeExitsEmptySearchMode(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(tui.Model)
	if !m.ModelSwitcher().SearchActive() {
		t.Fatal("precondition: search must be active after '/'")
	}

	m, _ = sendEscape(m)

	if m.ModelSwitcher().SearchActive() {
		t.Error("Escape right after '/' should exit search mode")
	}
	// The overlay itself must still be open (Escape only backs out one level).
	if !m.OverlayActive() || m.ActiveOverlay() != "model" {
		t.Error("Escape out of empty search mode should not close the whole model overlay")
	}
}
