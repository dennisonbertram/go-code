package tui_test

// Tests for #666-View: /stats and /context overlays must render with a
// RoundedBorder box so they match the chrome of /help, /model, /keys etc.

import (
	"strings"
	"testing"
)

// TestIssue666_StatsOverlayHasBorderRune verifies that the /stats overlay
// renders a rounded border rune ("╭") in its View() output.
func TestIssue666_StatsOverlayHasBorderRune(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/stats")
	if !m.OverlayActive() || m.ActiveOverlay() != "stats" {
		t.Fatal("precondition: stats overlay must be open")
	}

	view := m.View()
	if !strings.Contains(view, "╭") && !strings.Contains(view, "│") {
		t.Errorf("/stats overlay must contain a border rune ('╭' or '│'); got:\n%s", view)
	}
}

// TestIssue666_ContextOverlayHasBorderRune verifies that the /context overlay
// renders a rounded border rune ("╭") in its View() output.
func TestIssue666_ContextOverlayHasBorderRune(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/context")
	if !m.OverlayActive() || m.ActiveOverlay() != "context" {
		t.Fatal("precondition: context overlay must be open")
	}

	view := m.View()
	if !strings.Contains(view, "╭") && !strings.Contains(view, "│") {
		t.Errorf("/context overlay must contain a border rune ('╭' or '│'); got:\n%s", view)
	}
}

// TestIssue666_StatsContentStillPresent verifies the box doesn't hide the
// stats content ("Activity" heading still visible inside the box).
func TestIssue666_StatsContentStillPresent(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/stats")

	view := m.View()
	if !strings.Contains(view, "Activity") {
		t.Errorf("/stats overlay must still contain 'Activity' inside the box; got:\n%s", view)
	}
}

// TestIssue666_ContextContentStillPresent verifies the context content is still
// accessible inside the box.
func TestIssue666_ContextContentStillPresent(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/context")

	view := m.View()
	if !strings.Contains(view, "Context Window Usage") {
		t.Errorf("/context overlay must still contain 'Context Window Usage' inside the box; got:\n%s", view)
	}
}
