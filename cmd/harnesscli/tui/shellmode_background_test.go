package tui_test

// Tests for Ctrl+B background handoff of running shell-mode commands
// (epic #811, slice 4): Ctrl+B detaches a running command — the card
// collapses to a "backgrounded" line, the TUI stays usable, and exactly one
// completion card (exit code + output tail) appears when the command exits.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
)

// drainShellExecs drives the executor's poll cmd until no shell executors
// remain, simulating Bubble Tea's message loop.
func drainShellExecs(t *testing.T, m tui.Model, cmd tea.Cmd) tui.Model {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for m.ShellExecCount() > 0 {
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

func sendCtrlB(m tui.Model) tui.Model {
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	return m2.(tui.Model)
}

func TestShellBackground_CtrlBDetachesAndCompletes(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "sleep 0.2; echo done")
	if !m.ShellCommandRunning() {
		t.Fatal("precondition: shell command must be running")
	}

	m = sendCtrlB(m)

	// Detached: no longer foreground-running, but the executor is alive and
	// the command was NOT killed.
	if m.ShellCommandRunning() {
		t.Error("ctrl+b must detach the command from the foreground")
	}
	if m.ShellExecCount() != 1 {
		t.Fatalf("detached command must keep running, executors=%d", m.ShellExecCount())
	}
	if view := m.View(); !strings.Contains(view, "backgrounded") {
		t.Errorf("the live card must collapse to a backgrounded line, got:\n%s", view)
	}

	// The input is usable immediately while the command runs in the background.
	m = typeIntoModel(m, "hello")
	if m.Input() != "hello" {
		t.Errorf("input must be usable immediately after ctrl+b, got %q", m.Input())
	}

	m = drainShellExecs(t, m, pollCmd)

	view := m.View()
	if !strings.Contains(view, "done") {
		t.Errorf("completion card must show the command output, got:\n%s", view)
	}
	// Exactly one shell card remains (the backgrounded line is replaced by the
	// completion card) — one notice, not two.
	if n := strings.Count(view, "shell("); n != 1 {
		t.Errorf("expected exactly 1 shell card after completion, got %d:\n%s", n, view)
	}
	if got := m.ActiveToolCallStatus(); got != "completed" {
		t.Errorf("completion card must be completed, got %q", got)
	}
}

func TestShellBackground_FailedCommandShowsNonZeroExit(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "sleep 0.2; exit 2")
	m = sendCtrlB(m)
	m = drainShellExecs(t, m, pollCmd)

	if got := m.ActiveToolCallStatus(); got != "error" {
		t.Errorf("failed background command must render an error card, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "exit status 2") {
		t.Errorf("completion card must surface the non-zero exit, got:\n%s", view)
	}
}

func TestShellBackground_CtrlBNoOpWhenIdle(t *testing.T) {
	m := initModel(t, 80, 24)

	before := m.View()
	m = sendCtrlB(m)

	if m.ShellExecCount() != 0 {
		t.Error("ctrl+b with nothing running must not start or detach anything")
	}
	if strings.Contains(m.View(), "backgrounded") {
		t.Error("ctrl+b with nothing running must not render a backgrounded line")
	}
	if m.View() != before {
		t.Error("ctrl+b with nothing running must be a no-op")
	}
}

func TestShellBackground_EscDoesNotKillAfterDetach(t *testing.T) {
	m := initModel(t, 80, 24)

	m, pollCmd := submitShellCommand(t, m, "sleep 0.2; echo safe")
	m = sendCtrlB(m)

	// Esc after detach must not kill the backgrounded command.
	m, _ = sendEscape(m)
	m = drainShellExecs(t, m, pollCmd)

	if got := m.ActiveToolCallStatus(); got != "completed" {
		t.Errorf("backgrounded command must survive Esc and complete, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "safe") {
		t.Errorf("backgrounded command output must arrive, got:\n%s", view)
	}
}

func TestShellBackground_CompletionFeedsNextPrompt(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "sleep 0.2; echo bg-result")
	m = sendCtrlB(m)
	m = drainShellExecs(t, m, pollCmd)

	m = submitPrompt(t, m, "what happened?")

	got := prompts()
	if len(got) != 1 {
		t.Fatalf("expected 1 run request, got %d", len(got))
	}
	for _, want := range []string{"<shell-command", `exit-code="0"`, "bg-result"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("background completion must feed the next prompt's context block (%q), got:\n%s", want, got[0])
		}
	}
}
