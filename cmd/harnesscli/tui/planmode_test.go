package tui_test

// planmode_test.go — TUI #660
// Behavioral tests for plan mode: ctrl+o toggle (when idle), and regression
// guard that ctrl+o still expands an active tool call when one is in-flight.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ─── ctrl+o idle toggle ───────────────────────────────────────────────────────

// TestPlanMode_IdleCtrlO_TogglesOn verifies that pressing ctrl+o when idle
// (no active run, no activeToolCallID) sets planMode=true and shows a status
// message indicating plan mode is on.
func TestPlanMode_IdleCtrlO_TogglesOn(t *testing.T) {
	m := initModel(t, 80, 24)

	// Precondition: not running, no active tool.
	if m.RunActive() {
		t.Fatal("precondition: expected no active run")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model := m2.(tui.Model)

	if !model.PlanMode() {
		t.Error("expected PlanMode()=true after first ctrl+o in idle state")
	}

	status := model.StatusMsg()
	if !strings.Contains(status, "Plan mode") {
		t.Errorf("expected status message to mention 'Plan mode'; got %q", status)
	}
	if !strings.Contains(status, "ON") {
		t.Errorf("expected status message to contain 'ON'; got %q", status)
	}
}

// TestPlanMode_IdleCtrlO_TogglesOff verifies that pressing ctrl+o twice (when
// idle) returns planMode to false and shows "Plan mode: OFF" in the status bar.
func TestPlanMode_IdleCtrlO_TogglesOff(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model := m2.(tui.Model)
	if !model.PlanMode() {
		t.Fatal("precondition: expected PlanMode()=true after first ctrl+o")
	}

	m3, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = m3.(tui.Model)

	if model.PlanMode() {
		t.Error("expected PlanMode()=false after second ctrl+o (toggle off)")
	}

	status := model.StatusMsg()
	if !strings.Contains(status, "Plan mode") {
		t.Errorf("expected status message to mention 'Plan mode'; got %q", status)
	}
	if !strings.Contains(status, "OFF") {
		t.Errorf("expected status message to contain 'OFF'; got %q", status)
	}
}

// ─── ctrl+o with active tool — regression guard ───────────────────────────────

// TestPlanMode_ActiveTool_CtrlOExpandsNotPlanMode is a regression guard: when
// an active tool call is in-flight (activeToolCallID != ""), ctrl+o must expand
// the tool rather than toggle plan mode. PlanMode() must remain false.
func TestPlanMode_ActiveTool_CtrlOExpandsNotPlanMode(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})

	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-planreg-1"})
	model := m2.(tui.Model)

	// Trigger a tool.call.started so activeToolCallID is set.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"bash","call_id":"call-planreg","arguments":"echo hi"}`),
	})
	model = m3.(tui.Model)

	// Send ctrl+o — should expand the tool, NOT toggle plan mode.
	m4, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = m4.(tui.Model)

	if model.PlanMode() {
		t.Error("regression: ctrl+o with activeToolCallID set must expand tool, not toggle plan mode")
	}

	// The tool should now be expanded — visible in the view.
	view := model.View()
	if !strings.Contains(view, "$ echo hi") {
		t.Errorf("expected expanded bash header after ctrl+o with active tool; view=%q", view)
	}
}
