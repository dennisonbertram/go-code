package tui_test

// Tests for shell-mode input state (epic #811, slice 1):
//   - "!" typed on an empty input enters shell mode (flag + rendered marker/border)
//   - "!" typed on a non-empty input stays literal text
//   - Backspace/Esc on an empty shell-mode input exits shell mode
//   - Esc on a non-empty shell-mode input clears the text but stays in shell mode
//   - pasted text starting with "!" enters shell mode, keeping the remainder
//   - submit in shell mode is a stub (status message, no run) and returns to normal mode
//   - shell mode survives window resizes (the input component is re-created on resize)

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

func TestShellMode_BangOnEmptyInputEntersShellMode(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(tui.Model)

	if !m.ShellMode() {
		t.Error("'!' on an empty input must enter shell mode")
	}
	if m.Input() != "" {
		t.Errorf("the '!' that enters shell mode must not be inserted into the input, got %q", m.Input())
	}
}

func TestShellMode_ViewShowsMarkerAndBorder(t *testing.T) {
	m := initModel(t, 80, 24)

	// Normal mode: ❯ prompt, no rounded border.
	view := m.View()
	if !strings.Contains(view, "❯") {
		t.Fatalf("normal view must contain the '❯' prompt, got %q", view)
	}
	if strings.Contains(view, "╭") {
		t.Fatalf("normal view must not render the shell-mode border, got %q", view)
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(tui.Model)

	view = m.View()
	if !strings.Contains(view, "╭") {
		t.Errorf("shell-mode view must render the violet rounded border, got %q", view)
	}
	if !strings.Contains(view, "!") {
		t.Errorf("shell-mode view must render the '!' prompt marker, got %q", view)
	}
}

func TestShellMode_BangOnNonEmptyInputStaysLiteral(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "x")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(tui.Model)

	if m.ShellMode() {
		t.Error("'!' on a non-empty input must not enter shell mode")
	}
	if m.Input() != "x!" {
		t.Errorf("'!' on a non-empty input must be inserted literally, got %q", m.Input())
	}
}

func TestShellMode_BackspaceOnEmptyInputExits(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "!")
	if !m.ShellMode() {
		t.Fatal("precondition: shell mode must be active")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(tui.Model)

	if m.ShellMode() {
		t.Error("Backspace on an empty shell-mode input must exit shell mode")
	}
}

func TestShellMode_EscOnEmptyInputExits(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "!")
	if !m.ShellMode() {
		t.Fatal("precondition: shell mode must be active")
	}

	m, _ = sendEscape(m)

	if m.ShellMode() {
		t.Error("Esc on an empty shell-mode input must exit shell mode")
	}
}

func TestShellMode_NonEmptyInputSurvivesEditing(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "!")
	m = typeIntoModel(m, "ls")

	if !m.ShellMode() {
		t.Fatal("precondition: shell mode must be active")
	}
	if m.Input() != "ls" {
		t.Fatalf("precondition: input must hold the shell command, got %q", m.Input())
	}

	// Ordinary editing keeps shell mode active.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(tui.Model)
	if !m.ShellMode() {
		t.Error("Backspace on a non-empty shell-mode input must edit, not exit shell mode")
	}
	if m.Input() != "l" {
		t.Errorf("Backspace must delete a character in shell mode, got %q", m.Input())
	}

	// Esc with text clears the input but does NOT exit shell mode — exit only
	// happens on an already-empty input (kimi-code behavior).
	m, _ = sendEscape(m)
	if m.Input() != "" {
		t.Errorf("Esc with text must clear the input, got %q", m.Input())
	}
	if !m.ShellMode() {
		t.Error("Esc on a non-empty shell-mode input must not exit shell mode")
	}

	// A second Esc on the now-empty input exits.
	m, _ = sendEscape(m)
	if m.ShellMode() {
		t.Error("Esc on the emptied shell-mode input must exit shell mode")
	}
}

func TestShellMode_PastedBangCommandEntersShellMode(t *testing.T) {
	m := initModel(t, 80, 24)

	// Bracketed paste arrives as a single multi-rune KeyRunes message.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!ls -la")})
	m = m2.(tui.Model)

	if !m.ShellMode() {
		t.Error("pasted text starting with '!' must enter shell mode")
	}
	if m.Input() != "ls -la" {
		t.Errorf("the pasted command after the leading '!' must land in the input, got %q", m.Input())
	}
}

func TestShellMode_SubmitExecutesAndReturnsToNormalMode(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "!")
	m = typeIntoModel(m, "echo hi")
	if !m.ShellMode() {
		t.Fatal("precondition: shell mode must be active")
	}

	// Slice 2: submitting in shell mode executes the command locally and shows
	// a running "shell" card (the slice-1 stub status message is gone).
	m2, cmd := m.Update(inputarea.CommandSubmittedMsg{Value: "echo hi"})
	m = m2.(tui.Model)

	if m.ShellMode() {
		t.Error("submitting in shell mode must return to normal mode")
	}
	if strings.Contains(m.StatusMsg(), "not executed") {
		t.Errorf("shell commands must execute now — stale stub status %q", m.StatusMsg())
	}
	if got := m.ActiveToolCallStatus(); got != "running" {
		t.Errorf("submit must start local execution as a running shell card, got %q", got)
	}
	// Reap the spawned process before the test exits.
	_ = drainShell(t, m, cmd)
}

func TestShellMode_SurvivesWindowResize(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "!")
	if !m.ShellMode() {
		t.Fatal("precondition: shell mode must be active")
	}

	// The input component is re-created on every WindowSizeMsg — shell mode
	// must be re-applied so the mode and its rendering survive resizes.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(tui.Model)

	if !m.ShellMode() {
		t.Error("shell mode must survive a window resize")
	}
	if view := m.View(); !strings.Contains(view, "╭") {
		t.Errorf("shell-mode border must still render after a resize, got %q", view)
	}
}

func TestShellMode_BangIgnoredWhileOverlayActive(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m3.(tui.Model)

	if m.ShellMode() {
		t.Error("'!' must not enter shell mode while an overlay is active")
	}
	if !m.OverlayActive() {
		t.Error("overlay must remain open after '!'")
	}
}
