package tui_test

// interrupt_two_stage_test.go — explicit TDD tests for ticket #669.
//
// These tests document and enforce the two-stage ctrl+c interrupt behavior:
//   - First ctrl+c during an active run: show the confirmation banner only.
//   - Second ctrl+c with banner showing:  call cancelRun, clear runActive, hide banner.
//   - Esc with banner showing:           dismiss banner, do not cancel.
//   - ctrl+c with no active run:         return tea.Quit (unchanged).

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/interruptui"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// activeModel returns a Model with a running run and the supplied cancelFn wired.
func activeModel(t *testing.T, cancelFn func()) tui.Model {
	t.Helper()
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-669"})
	return m4.(tui.Model)
}

// ─── two-stage state machine ──────────────────────────────────────────────────

// TestTUI669_FirstCtrlC_BannerVisible_NoCancelYet asserts the first ctrl+c during
// an active run transitions to the Confirm state without calling cancel.
func TestTUI669_FirstCtrlC_BannerVisible_NoCancelYet(t *testing.T) {
	cancelled := false
	m := activeModel(t, func() { cancelled = true })

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m2.(tui.Model)

	if cancelled {
		t.Fatal("cancelRun must NOT be called on the first ctrl+c")
	}
	if !after.RunActive() {
		t.Error("RunActive() must still be true after first ctrl+c")
	}
	if !after.InterruptBannerVisible() {
		t.Error("interrupt banner must be visible after first ctrl+c")
	}
	if after.InterruptBannerState() != interruptui.StateConfirm {
		t.Errorf("banner state = %v, want StateConfirm", after.InterruptBannerState())
	}
}

// TestTUI669_FirstCtrlC_StatusHint asserts a hint status message appears on first ctrl+c.
func TestTUI669_FirstCtrlC_StatusHint(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m2.(tui.Model)

	if after.StatusMsg() == "" {
		t.Error("a status hint must be shown after first ctrl+c")
	}
	if strings.Contains(after.StatusMsg(), "Interrupted") {
		t.Errorf("status after first ctrl+c must NOT say 'Interrupted' yet, got: %q", after.StatusMsg())
	}
}

// TestTUI669_SecondCtrlC_CancelsRun asserts the second ctrl+c (banner visible) calls
// cancel and clears runActive.
func TestTUI669_SecondCtrlC_CancelsRun(t *testing.T) {
	cancelled := false
	m := activeModel(t, func() { cancelled = true })

	// First ctrl+c.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Second ctrl+c.
	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m3.(tui.Model)

	if !cancelled {
		t.Error("cancelRun must be called on the second ctrl+c")
	}
	if after.RunActive() {
		t.Error("RunActive() must be false after second ctrl+c")
	}
}

// TestTUI669_SecondCtrlC_BannerHidden asserts the banner is hidden after the second ctrl+c.
func TestTUI669_SecondCtrlC_BannerHidden(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m3.(tui.Model)

	if after.InterruptBannerVisible() {
		t.Error("interrupt banner must be hidden after second ctrl+c")
	}
}

// TestTUI669_SecondCtrlC_StatusInterrupted asserts the status message reports
// the run was interrupted after the second ctrl+c, and invites a further
// ctrl+c to quit the app.
func TestTUI669_SecondCtrlC_StatusInterrupted(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m3.(tui.Model)

	if !strings.Contains(after.StatusMsg(), "interrupted") {
		t.Errorf("statusMsg after second ctrl+c = %q, want it to mention the run was interrupted", after.StatusMsg())
	}
	if !strings.Contains(after.StatusMsg(), "ctrl+c again to quit") {
		t.Errorf("statusMsg after second ctrl+c = %q, want it to invite another ctrl+c to quit", after.StatusMsg())
	}
}

// ─── banner view content ──────────────────────────────────────────────────────

// TestTUI669_BannerViewContainsConfirmText asserts that the banner View() rendered
// inside the model's View() contains the Ctrl+C confirmation text.
func TestTUI669_BannerViewContainsConfirmText(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m2.(tui.Model)

	view := after.View()
	// The banner renders "⚠  Press Ctrl+C again to stop, or Esc to continue".
	if !strings.Contains(view, "Ctrl+C") && !strings.Contains(view, "ctrl+c") {
		t.Errorf("View() after first ctrl+c must contain Ctrl+C hint; got:\n%s", view)
	}
}

// ─── Esc dismissal ────────────────────────────────────────────────────────────

// TestTUI669_Esc_DismissesBannerWithoutCancel asserts Esc dismisses the banner
// without cancelling the run.
func TestTUI669_Esc_DismissesBannerWithoutCancel(t *testing.T) {
	cancelled := false
	m := activeModel(t, func() { cancelled = true })

	// First ctrl+c: banner visible.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m2.(tui.Model).InterruptBannerVisible() {
		t.Fatal("precondition: banner must be visible before Esc test")
	}

	// Esc: dismiss.
	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	after := m3.(tui.Model)

	if cancelled {
		t.Error("cancelRun must NOT be called when Esc dismisses the banner")
	}
	if after.InterruptBannerVisible() {
		t.Error("banner must be hidden after Esc")
	}
	if !after.RunActive() {
		t.Error("RunActive() must still be true after Esc (run continues)")
	}
}

// TestTUI669_Esc_AfterDismiss_NewCtrlCRestartsBanner asserts that after Esc dismissal
// a fresh ctrl+c shows the banner again (stateless restart).
func TestTUI669_Esc_AfterDismiss_NewCtrlCRestartsBanner(t *testing.T) {
	m := activeModel(t, func() {})

	// First ctrl+c.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Esc: dismiss.
	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m3.(tui.Model).InterruptBannerVisible() {
		t.Fatal("precondition: banner must be hidden after Esc")
	}

	// Second independent ctrl+c: banner should reappear.
	m4, _ := m3.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m4.(tui.Model)

	if !after.InterruptBannerVisible() {
		t.Error("banner must reappear after a fresh ctrl+c following Esc dismissal")
	}
}

// ─── idle quit (unchanged behavior) ──────────────────────────────────────────

// TestTUI669_IdleCtrlC_ReturnsQuit asserts that ctrl+c with no active run returns
// tea.Quit and does not call cancel.
func TestTUI669_IdleCtrlC_ReturnsQuit(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelled := false
	m3 := m2.(tui.Model).WithCancelRun(func() { cancelled = true })

	_, cmd := m3.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if cancelled {
		t.Error("cancelRun must NOT be called when no run is active")
	}

	// Verify the returned cmd is tea.Quit.
	// tea.Quit is a func() tea.Msg — execute it and check for tea.QuitMsg.
	if cmd == nil {
		t.Fatal("cmd must not be nil when ctrl+c with no active run (expected tea.Quit)")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T, want tea.QuitMsg", msg)
	}
}

// ─── banner not shown when run is idle ───────────────────────────────────────

// TestTUI669_NoActiveRun_BannerNeverShown asserts the banner does not appear when
// ctrl+c is pressed while no run is active.
func TestTUI669_NoActiveRun_BannerNeverShown(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m3, _ := m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	after := m3.(tui.Model)

	if after.InterruptBannerVisible() {
		t.Error("interrupt banner must NOT be shown when ctrl+c pressed with no active run")
	}
}
