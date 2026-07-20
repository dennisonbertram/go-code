package tui_test

// Tests for shell-mode local execution (epic #811, slice 2):
// submitting in shell mode runs the command locally from the TUI process and
// streams stdout/stderr into a tool-style "shell" card in the conversation
// view; Esc interrupts a running command; non-zero exits are reported.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
)

// unwrapSingle executes a tea.Cmd expected to wrap at most one real command
// (the shell-mode flow only ever batches a single poll/submit cmd) and returns
// the resulting message.
func unwrapSingle(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a cmd, got nil")
	}
	msg := cmd()
	bm, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	if len(bm) != 1 {
		t.Fatalf("expected a single-cmd batch, got %d cmds", len(bm))
	}
	return bm[0]()
}

// submitShellCommand enters shell mode, types the command, presses Enter, and
// feeds the resulting CommandSubmittedMsg back into the model — the full real
// key flow. Returns the updated model and the executor's poll cmd.
func submitShellCommand(t *testing.T, m tui.Model, command string) (tui.Model, tea.Cmd) {
	t.Helper()
	m = typeIntoModel(m, "!")
	m = typeIntoModel(m, command)
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)
	msg := unwrapSingle(t, cmd)
	m3, pollCmd := m.Update(msg)
	return m3.(tui.Model), pollCmd
}

// drainShell drives the executor's poll cmd until the active shell card leaves
// the running state, simulating Bubble Tea's message loop.
func drainShell(t *testing.T, m tui.Model, cmd tea.Cmd) tui.Model {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for m.ActiveToolCallStatus() == "running" {
		if time.Now().After(deadline) {
			t.Fatal("shell command did not finish in time")
		}
		msg := unwrapSingle(t, cmd)
		m2, next := m.Update(msg)
		m = m2.(tui.Model)
		cmd = next
	}
	return m
}

func TestShellMode_ExecutesAndStreamsIntoCard(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "echo hello")

	if m.ShellMode() {
		t.Error("submitting in shell mode must return to normal mode")
	}
	if got := m.ActiveToolCallStatus(); got != "running" {
		t.Fatalf("after submit the shell card must be running, got %q", got)
	}

	m = drainShell(t, m, pollCmd)

	if got := m.ActiveToolCallStatus(); got != "completed" {
		t.Errorf("shell card must complete after the command exits, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "hello") {
		t.Errorf("shell card must contain the command output, got:\n%s", view)
	}
	if view := m.View(); !strings.Contains(view, "shell") {
		t.Errorf("output must render as a 'shell' tool card, got:\n%s", view)
	}
}

func TestShellMode_NonZeroExitShownInCard(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "exit 1")
	m = drainShell(t, m, pollCmd)

	if got := m.ActiveToolCallStatus(); got != "error" {
		t.Errorf("a failing command must render an error card, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "exit status 1") {
		t.Errorf("the card must report the non-zero exit, got:\n%s", view)
	}
}

func TestShellMode_EscInterruptsRunningCommand(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "sleep 999")
	if got := m.ActiveToolCallStatus(); got != "running" {
		t.Fatalf("precondition: shell command must be running, got %q", got)
	}

	start := time.Now()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(tui.Model)
	m = drainShell(t, m, pollCmd)

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Esc interrupt took too long (%v) — the process must be killed, not awaited", elapsed)
	}
	if got := m.ActiveToolCallStatus(); got != "error" {
		t.Errorf("an interrupted command must render an error card, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "interrupted") {
		t.Errorf("the card must report the interruption, got:\n%s", view)
	}
}

func TestShellMode_CtrlCKillsInsteadOfQuitting(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "sleep 999")
	if got := m.ActiveToolCallStatus(); got != "running" {
		t.Fatalf("precondition: shell command must be running, got %q", got)
	}

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = m2.(tui.Model)

	// Ctrl+C while a shell command runs must kill the command, NOT quit the
	// TUI. tea.Quit would return a QuitMsg immediately; any other cmd (e.g.
	// the status tick) blocks, so probe with a timeout instead of executing
	// it inline.
	if cmd != nil {
		result := make(chan tea.Msg, 1)
		go func() { result <- cmd() }()
		select {
		case msg := <-result:
			if _, isQuit := msg.(tea.QuitMsg); isQuit {
				t.Fatal("ctrl+c while a shell command runs must not quit the TUI")
			}
		case <-time.After(500 * time.Millisecond):
			// Still blocking — not a quit.
		}
	}
	m = drainShell(t, m, pollCmd)
	if got := m.ActiveToolCallStatus(); got != "error" {
		t.Errorf("the killed command must render an error card, got %q", got)
	}
}
