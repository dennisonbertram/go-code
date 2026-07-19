package inputarea_test

// Tests for shell-mode rendering in the input area (epic #811, slice 1):
// while shell mode is active the component renders a "!" prompt marker and a
// distinct border; the normal "❯" prompt is restored when shell mode exits.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

func TestShellMode_RendersBangMarker(t *testing.T) {
	m := inputarea.New(80).SetShellMode(true)
	view := m.View()
	if !strings.Contains(view, "!") {
		t.Errorf("shell-mode view must contain the '!' prompt marker, got %q", view)
	}
	if strings.Contains(view, "❯") {
		t.Errorf("shell-mode view must not show the normal '❯' prompt, got %q", view)
	}
}

func TestShellMode_RendersBorder(t *testing.T) {
	m := inputarea.New(80).SetShellMode(true)
	view := m.View()
	// The distinct shell-mode frame is a rounded border; its corner/edge runes
	// must be present regardless of color profile.
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╯") {
		t.Errorf("shell-mode view must render a rounded border, got %q", view)
	}
}

func TestShellMode_NormalModeHasNoBorder(t *testing.T) {
	m := inputarea.New(80)
	view := m.View()
	if !strings.Contains(view, "❯") {
		t.Errorf("normal view must contain the '❯' prompt, got %q", view)
	}
	if strings.Contains(view, "╭") {
		t.Errorf("normal view must not render the shell-mode border, got %q", view)
	}
}

func TestShellMode_ToggleOffRestoresNormalRendering(t *testing.T) {
	m := inputarea.New(80).SetShellMode(true)
	m = m.SetShellMode(false)
	view := m.View()
	if !strings.Contains(view, "❯") {
		t.Errorf("after exiting shell mode the '❯' prompt must be restored, got %q", view)
	}
	if strings.Contains(view, "╭") {
		t.Errorf("after exiting shell mode the border must be gone, got %q", view)
	}
}

func TestShellMode_PreservesValueAndEditing(t *testing.T) {
	m := inputarea.New(80).SetShellMode(true)
	for _, r := range "ls -la" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.Value() != "ls -la" {
		t.Fatalf("shell-mode editing must behave like normal editing, got %q", m.Value())
	}
	// Toggling shell mode must not disturb the buffer.
	m = m.SetShellMode(false)
	if m.Value() != "ls -la" {
		t.Errorf("toggling shell mode must preserve the input value, got %q", m.Value())
	}
}

func TestShellMode_ShellModeAccessor(t *testing.T) {
	m := inputarea.New(80)
	if m.ShellMode() {
		t.Error("new input area must not start in shell mode")
	}
	m = m.SetShellMode(true)
	if !m.ShellMode() {
		t.Error("ShellMode() must report true after SetShellMode(true)")
	}
}
