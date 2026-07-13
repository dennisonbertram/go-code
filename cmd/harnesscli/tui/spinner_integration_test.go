package tui_test

import (
	"encoding/json"
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/spinner"
)

// TestSpinnerRouting_ShowsCancelHintWhileThinking verifies that once a run
// starts (and before any tool call or thinking delta arrives), the persistent
// chrome shows the spinner's cancel hint so the user always knows how to
// interrupt an in-flight run.
func TestSpinnerRouting_ShowsCancelHintWhileThinking(t *testing.T) {
	m := initModel(t, 80, 24)

	started, _ := m.Update(tui.RunStartedMsg{RunID: "run-spinner-1"})
	m = started.(tui.Model)

	view := m.View()
	if !strings.Contains(view, spinner.CancelHint) {
		t.Fatalf("expected cancel hint in view while a run is active, got: %q", view)
	}
}

// TestSpinnerRouting_ShowsRunningToolAsAction verifies that once a tool call
// starts, the spinner surfaces the running tool's name as its action label
// (instead of the generic rotating verb) alongside the cancel hint.
func TestSpinnerRouting_ShowsRunningToolAsAction(t *testing.T) {
	m := initModel(t, 80, 24)

	started, _ := m.Update(tui.RunStartedMsg{RunID: "run-spinner-2"})
	m = started.(tui.Model)

	toolStarted, _ := m.Update(tui.ToolStartMsg{
		CallID: "call-1",
		Name:   "bash",
		Input:  json.RawMessage(`"ls -l"`),
	})
	m = toolStarted.(tui.Model)

	ticked, _ := m.Update(spinner.SpinnerTickMsg{})
	m = ticked.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "Running bash") {
		t.Fatalf("expected spinner to show the running tool name, got: %q", view)
	}
	if !strings.Contains(view, spinner.CancelHint) {
		t.Fatalf("expected cancel hint alongside the running tool action, got: %q", view)
	}
}

// TestSpinnerRouting_HiddenWhenRunNotActive verifies that the spinner (and
// its cancel hint) does not appear when no run is in flight.
func TestSpinnerRouting_HiddenWhenRunNotActive(t *testing.T) {
	m := initModel(t, 80, 24)

	view := m.View()
	if strings.Contains(view, spinner.CancelHint) {
		t.Fatalf("expected no cancel hint when no run is active, got: %q", view)
	}
}

// TestSpinnerRouting_StopsAnimatingAfterRunCompletes verifies that the
// self-rescheduling spinner tick loop stops once a run completes: a tick
// received after RunCompletedMsg must not re-arm another tick or resurrect
// the spinner in the view.
func TestSpinnerRouting_StopsAnimatingAfterRunCompletes(t *testing.T) {
	m := initModel(t, 80, 24)

	started, _ := m.Update(tui.RunStartedMsg{RunID: "run-spinner-3"})
	m = started.(tui.Model)

	completed, _ := m.Update(tui.RunCompletedMsg{})
	m = completed.(tui.Model)

	_, cmd := m.Update(spinner.SpinnerTickMsg{})
	if cmd != nil {
		t.Fatalf("expected no rescheduled tick command once the run has completed")
	}

	view := m.View()
	if strings.Contains(view, spinner.CancelHint) {
		t.Fatalf("expected no cancel hint after the run completes, got: %q", view)
	}
}
