package tui_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestTUI039_RunActiveFlagSetOnStart verifies that RunStartedMsg sets runActive=true.
func TestTUI039_RunActiveFlagSetOnStart(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3, _ := m2.(tui.Model).Update(tui.RunStartedMsg{RunID: "run-001"})
	model := m3.(tui.Model)
	if !model.RunActive() {
		t.Error("RunActive() must be true after RunStartedMsg")
	}
}

// TestTUI039_RunActiveFlagClearedOnComplete verifies that RunCompletedMsg sets runActive=false.
func TestTUI039_RunActiveFlagClearedOnComplete(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3, _ := m2.(tui.Model).Update(tui.RunStartedMsg{RunID: "run-001"})
	m4, _ := m3.(tui.Model).Update(tui.RunCompletedMsg{RunID: "run-001"})
	model := m4.(tui.Model)
	if model.RunActive() {
		t.Error("RunActive() must be false after RunCompletedMsg")
	}
}

// TestTUI039_RunActiveFlagClearedOnFailed verifies that RunFailedMsg sets runActive=false.
func TestTUI039_RunActiveFlagClearedOnFailed(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3, _ := m2.(tui.Model).Update(tui.RunStartedMsg{RunID: "run-001"})
	m4, _ := m3.(tui.Model).Update(tui.RunFailedMsg{RunID: "run-001", Error: "timeout"})
	model := m4.(tui.Model)
	if model.RunActive() {
		t.Error("RunActive() must be false after RunFailedMsg")
	}
}

// TestTUI039_FirstCtrlCShowsBannerDoesNotCancel verifies the TWO-STAGE behavior:
// the FIRST Ctrl+C during an active run shows the interrupt banner but does NOT
// call cancelRun and does NOT change runActive.
//
// Changed from original assertion (first ctrl+c cancels immediately) to reflect
// ticket #669: first ctrl+c → banner visible, run still active, no cancel called.
func TestTUI039_FirstCtrlCShowsBannerDoesNotCancel(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelled := false
	cancelFn := func() { cancelled = true }

	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// First Ctrl+C — should show banner only.
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m5.(tui.Model)

	if cancelled {
		t.Error("cancelRun must NOT be called on the FIRST Ctrl+C (two-stage: show banner first)")
	}
	if !model.RunActive() {
		t.Error("RunActive() must still be true after the FIRST Ctrl+C (run not yet cancelled)")
	}
	if !model.InterruptBannerVisible() {
		t.Error("interrupt banner must be visible after the FIRST Ctrl+C")
	}
}

// TestTUI039_SecondCtrlCCancelsRun verifies the TWO-STAGE behavior:
// the SECOND Ctrl+C (after banner is showing) calls cancelRun, clears runActive,
// and hides the banner.
//
// Changed from original assertion (first ctrl+c cancels) to reflect ticket #669:
// cancel only happens on the second ctrl+c.
func TestTUI039_SecondCtrlCCancelsRun(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelled := false
	cancelFn := func() { cancelled = true }

	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// First Ctrl+C: show banner.
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Second Ctrl+C: confirm cancel.
	m6, _ := m5.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m6.(tui.Model)

	if !cancelled {
		t.Error("cancelRun must be called on the SECOND Ctrl+C")
	}
	if model.RunActive() {
		t.Error("RunActive() must be false after the SECOND Ctrl+C")
	}
	if model.InterruptBannerVisible() {
		t.Error("interrupt banner must be hidden after the SECOND Ctrl+C")
	}
}

// TestTUI039_CancelNoRunIsNoOp verifies that Ctrl+C when !runActive does not call
// cancelRun (it returns tea.Quit instead).
func TestTUI039_CancelNoRunIsNoOp(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelled := false
	cancelFn := func() { cancelled = true }
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)

	// runActive is false — Ctrl+C should return tea.Quit, not call cancel.
	m4, _ := m3.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	_ = m4

	if cancelled {
		t.Error("cancelRun must NOT be called when Ctrl+C is pressed with no active run")
	}
}

// TestTUI039_SecondCtrlCSetsInterruptedStatus verifies statusMsg="Interrupted" after
// the second ctrl+c completes the cancel.
//
// Changed from original (which asserted "Interrupted" after the FIRST ctrl+c).
// Now "Interrupted" only appears after the SECOND ctrl+c.
func TestTUI039_SecondCtrlCSetsInterruptedStatus(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelFn := func() {}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// First ctrl+c: shows banner, status = "Press ctrl+c again to interrupt".
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	afterFirst := m5.(tui.Model)
	if afterFirst.StatusMsg() == "Interrupted" {
		t.Error("StatusMsg must NOT be 'Interrupted' after only the first Ctrl+C")
	}

	// Second ctrl+c: cancels, status = "Interrupted".
	m6, _ := m5.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m6.(tui.Model)

	statusMsg := model.StatusMsg()
	if statusMsg != "Interrupted" {
		t.Errorf("statusMsg must be 'Interrupted' after second ctrl+c cancel, got: %q", statusMsg)
	}
}

// TestTUI039_SecondCtrlCRunActiveFlagCleared verifies runActive=false only after
// the SECOND Ctrl+C.
//
// Changed from original (which asserted runActive=false after the FIRST ctrl+c).
func TestTUI039_SecondCtrlCRunActiveFlagCleared(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelFn := func() {}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// First ctrl+c — run must still be active.
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m5.(tui.Model).RunActive() {
		t.Error("RunActive() must still be true after the FIRST Ctrl+C")
	}

	// Second ctrl+c — now run must be inactive.
	m6, _ := m5.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m6.(tui.Model)

	if model.RunActive() {
		t.Error("RunActive() must be false after the SECOND Ctrl+C cancel")
	}
}

// TestTUI039_CancelConcurrentSafe verifies cancel called from multiple goroutines does not panic.
func TestTUI039_CancelConcurrentSafe(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	var mu sync.Mutex
	callCount := 0
	cancelFn := func() {
		mu.Lock()
		callCount++
		mu.Unlock()
	}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})
	// Advance to banner-visible state (first ctrl+c).
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m5.(tui.Model)

	// Concurrently send second ctrl+c — each goroutine has its own copy of the
	// model (value semantics), so each sees the banner visible and calls cancel.
	// The important thing is no panic and the cancel func is safe to call concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localModel := model
			localModel.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		}()
	}
	wg.Wait()
	// No panic means concurrent invocations are safe.
}

// TestTUI039_InterruptedMsgHasTime verifies InterruptedMsg has a non-zero At field.
func TestTUI039_InterruptedMsgHasTime(t *testing.T) {
	msg := tui.InterruptedMsg{At: time.Now()}
	if msg.At.IsZero() {
		t.Error("InterruptedMsg.At must not be zero")
	}
}

// TestTUI039_EscDismissesBanner verifies that Esc dismisses the interrupt banner
// without cancelling the run.
func TestTUI039_EscDismissesBanner(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelled := false
	cancelFn := func() { cancelled = true }

	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// First ctrl+c: show banner.
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m5.(tui.Model).InterruptBannerVisible() {
		t.Fatal("precondition: banner must be visible after first ctrl+c")
	}

	// Esc: dismiss banner, do NOT cancel.
	m6, _ := m5.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := m6.(tui.Model)

	if cancelled {
		t.Error("cancelRun must NOT be called when Esc dismisses the interrupt banner")
	}
	if model.InterruptBannerVisible() {
		t.Error("banner must be hidden after Esc dismissal")
	}
	if !model.RunActive() {
		t.Error("RunActive() must still be true after Esc dismissal (run continues)")
	}
}

// TestTUI039_VisualSnapshot_80x24 renders the TUI with the interrupt confirmation
// banner visible (first ctrl+c state) and after the second ctrl+c (Interrupted).
func TestTUI039_VisualSnapshot_80x24(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cancelFn := func() {}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})

	// After first ctrl+c: banner should be visible.
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	bannerModel := m5.(tui.Model)

	// After second ctrl+c: interrupted status.
	m6, _ := m5.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	interruptedModel := m6.(tui.Model)

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}

	// Snapshot 1: banner visible (first ctrl+c).
	bannerOutput := bannerModel.View()
	bannerPath := dir + "/TUI-039-cancel-80x24.txt"
	if err := os.WriteFile(bannerPath, []byte(bannerOutput), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("banner snapshot written to %s", bannerPath)

	if !strings.Contains(bannerOutput, "ctrl+c") && !strings.Contains(bannerOutput, "Ctrl+C") {
		t.Errorf("banner snapshot must contain ctrl+c hint, got:\n%s", bannerOutput)
	}

	// Snapshot 2: after second ctrl+c — "Interrupted" status.
	interruptedOutput := interruptedModel.View()
	if !strings.Contains(interruptedOutput, "Interrupted") {
		t.Errorf("interrupted snapshot must contain 'Interrupted', got:\n%s", interruptedOutput)
	}
}

// TestTUI039_VisualSnapshot_120x40 renders the TUI at 120x40.
func TestTUI039_VisualSnapshot_120x40(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	cancelFn := func() {}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m5.(tui.Model)

	output := model.View()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-039-cancel-120x40.txt"
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI039_VisualSnapshot_200x50 renders the TUI at 200x50.
func TestTUI039_VisualSnapshot_200x50(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})

	cancelFn := func() {}
	m3 := m2.(tui.Model).WithCancelRun(cancelFn)
	m4, _ := m3.Update(tui.RunStartedMsg{RunID: "run-001"})
	m5, _ := m4.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model := m5.(tui.Model)

	output := model.View()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-039-cancel-200x50.txt"
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}
