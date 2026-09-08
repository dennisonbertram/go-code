package spinner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTUI025_SpinningToCompletionLine verifies that after Stop(), View()
// returns the completion line text rather than the spinner animation.
func TestTUI025_SpinningToCompletionLine(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(100)

	view := m.View(80)
	if view == "" {
		t.Fatal("View() after Stop() should return completion line, not empty string")
	}
	if !strings.Contains(view, "Worked for") {
		t.Errorf("View() after Stop() should contain 'Worked for', got: %q", view)
	}
}

// TestTUI025_CompletionPersistsForNFrames verifies that the completion line
// is visible for completionFrames ticks, then becomes silent ("").
func TestTUI025_CompletionPersistsForNFrames(t *testing.T) {
	m := New(42)
	m = m.Start()
	// Advance startTime a couple seconds back so ElapsedSeconds() is non-zero.
	m.startTime = time.Now().Add(-2 * time.Second)
	m = m.Stop(100)

	// Check default completionFrames is 10.
	if m.completionFrames != 10 {
		t.Fatalf("expected completionFrames=10 after Stop(), got %d", m.completionFrames)
	}

	// Tick 9 times — completion line should still be visible.
	for i := 0; i < 9; i++ {
		m = m.Tick()
		view := m.View(80)
		if !strings.Contains(view, "Worked for") {
			t.Errorf("tick %d: View() should still show completion, got: %q", i+1, view)
		}
	}

	// Tick once more (10th tick) — now completionFrames should reach 0, view silent.
	m = m.Tick()
	view := m.View(80)
	if view != "" {
		t.Errorf("after 10 ticks post-Stop, View() should be empty (silent), got: %q", view)
	}
}

// TestTUI025_CompletionLineDuration verifies that the duration appears in the
// completion line rendered by View() after Stop().
func TestTUI025_CompletionLineDuration(t *testing.T) {
	m := New(42)
	m = m.Start()
	// Set startTime 5 seconds in the past.
	m.startTime = time.Now().Add(-5 * time.Second)
	m = m.Stop(50)

	view := m.View(80)
	if !strings.Contains(view, "s") {
		t.Errorf("View() after Stop() should contain a duration with 's', got: %q", view)
	}
	if !strings.Contains(view, "Worked for") {
		t.Errorf("View() after Stop() should contain 'Worked for', got: %q", view)
	}
}

// TestTUI025_CompletionWithZeroTokens verifies Stop(0) works and produces a
// valid completion line.
func TestTUI025_CompletionWithZeroTokens(t *testing.T) {
	m := New(42)
	m = m.Start()
	m.startTime = time.Now().Add(-1 * time.Second)
	m = m.Stop(0)

	view := m.View(80)
	if view == "" {
		t.Error("View() after Stop(0) should show completion line, not empty")
	}
	if !strings.Contains(view, "Worked for") {
		t.Errorf("View() after Stop(0) should contain 'Worked for', got: %q", view)
	}
}

// TestTUI025_ShowsCompletionFlagBehavior verifies ShowsCompletion() is true
// after Stop() and transitions to false after completionFrames ticks.
func TestTUI025_ShowsCompletionFlagBehavior(t *testing.T) {
	m := New(42)
	m = m.Start()
	m.startTime = time.Now().Add(-1 * time.Second)

	if m.ShowsCompletion() {
		t.Error("ShowsCompletion() should be false before Stop()")
	}

	m = m.Stop(100)
	if !m.ShowsCompletion() {
		t.Error("ShowsCompletion() should be true immediately after Stop()")
	}

	// Tick completionFrames-1 times — still showing.
	for i := 0; i < 9; i++ {
		m = m.Tick()
		if !m.ShowsCompletion() {
			t.Errorf("ShowsCompletion() should be true at tick %d", i+1)
		}
	}

	// One more tick — should become false.
	m = m.Tick()
	if m.ShowsCompletion() {
		t.Error("ShowsCompletion() should be false after completionFrames ticks")
	}
}

// TestTUI025_ElapsedSecondsAccuracy verifies that ElapsedSeconds() returns a
// reasonable non-negative value after Start(), and 0 before Start().
func TestTUI025_ElapsedSecondsAccuracy(t *testing.T) {
	m := New(42)

	// Before Start: should return 0.
	if m.ElapsedSeconds() != 0 {
		t.Errorf("ElapsedSeconds() before Start should be 0, got %f", m.ElapsedSeconds())
	}

	m = m.Start()
	// After Start: sleep briefly and check.
	time.Sleep(50 * time.Millisecond)
	elapsed := m.ElapsedSeconds()
	if elapsed < 0 {
		t.Errorf("ElapsedSeconds() should be >= 0, got %f", elapsed)
	}
	// Should be less than 5 seconds (we only slept 50ms).
	if elapsed > 5.0 {
		t.Errorf("ElapsedSeconds() after 50ms sleep should be < 5s, got %f", elapsed)
	}
}

// TestTUI025_ConcurrentCompletion verifies that 10 goroutines each operating
// on their own Model instance in completion mode do not race.
func TestTUI025_ConcurrentCompletion(t *testing.T) {
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		seed := int64(i)
		go func(s int64) {
			defer wg.Done()
			m := New(s)
			m = m.Start()
			m.startTime = time.Now().Add(-time.Duration(s+1) * time.Second)
			for j := 0; j < 5; j++ {
				m = m.Tick()
				_ = m.View(80)
			}
			m = m.Stop(int(s * 10))
			_ = m.ShowsCompletion()
			_ = m.ElapsedSeconds()
			for k := 0; k < 12; k++ {
				m = m.Tick()
				_ = m.View(80)
				_ = m.ShowsCompletion()
			}
		}(seed)
	}

	wg.Wait()
}

// TestTUI025_BoundaryDuration verifies that zero duration and large duration
// both format safely in the completion line.
func TestTUI025_BoundaryDuration(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(0)

	// Zero duration: startTime is zero value, ElapsedSeconds should return 0.
	// We won't force startTime to zero since Start() sets it; just check no panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("CompletionLine(0) panicked: %v", r)
			}
		}()
		line := m.CompletionLine(0)
		if line == "" {
			t.Error("CompletionLine(0) should not be empty")
		}
	}()

	// Large duration: 10 hours = 36000 seconds.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("CompletionLine(36000) panicked: %v", r)
			}
		}()
		line := m.CompletionLine(36000)
		if line == "" {
			t.Error("CompletionLine(36000) should not be empty")
		}
	}()
}

// ─── Visual Snapshot Tests ────────────────────────────────────────────────────

// renderCompletionSnapshot renders TUI-025 completion states at given dimensions.
func renderCompletionSnapshot(width, height int) string {
	m := New(42)
	m = m.Start()
	m.startTime = time.Now().Add(-5 * time.Second)
	// Tick a few times for visual variety.
	for i := 0; i < 3; i++ {
		m = m.Tick()
	}
	m = m.Stop(250)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# TUI-025 Completion Snapshot %dx%d\n", width, height))
	sb.WriteString(strings.Repeat("-", width))
	sb.WriteString("\n")

	// Immediately after Stop — completion line visible.
	sb.WriteString("## Immediately after Stop() — ShowsCompletion=true\n")
	sb.WriteString(fmt.Sprintf("ShowsCompletion: %v\n", m.ShowsCompletion()))
	sb.WriteString(m.View(width))
	sb.WriteString("\n")

	// Mid-completion (5 ticks in).
	sb.WriteString("\n## After 5 ticks (mid-completion)\n")
	for i := 0; i < 5; i++ {
		m = m.Tick()
	}
	sb.WriteString(fmt.Sprintf("ShowsCompletion: %v\n", m.ShowsCompletion()))
	sb.WriteString(m.View(width))
	sb.WriteString("\n")

	// After all completion frames (silent).
	sb.WriteString("\n## After 10 ticks (silent)\n")
	for i := 0; i < 5; i++ {
		m = m.Tick()
	}
	sb.WriteString(fmt.Sprintf("ShowsCompletion: %v\n", m.ShowsCompletion()))
	view := m.View(width)
	if view == "" {
		sb.WriteString("(silent — no output)\n")
	} else {
		sb.WriteString(view)
		sb.WriteString("\n")
	}

	// ElapsedSeconds.
	sb.WriteString(fmt.Sprintf("\n## ElapsedSeconds: %.3f\n", m.ElapsedSeconds()))

	sb.WriteString(strings.Repeat("-", width))
	sb.WriteString("\n")
	return sb.String()
}

func TestTUI025_VisualSnapshot_80x24(t *testing.T) {
	content := renderCompletionSnapshot(80, 24)
	path := filepath.Join(snapshotDir, "TUI-025-completion-80x24.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	t.Logf("Snapshot written: %s", path)
}

func TestTUI025_VisualSnapshot_120x40(t *testing.T) {
	content := renderCompletionSnapshot(120, 40)
	path := filepath.Join(snapshotDir, "TUI-025-completion-120x40.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	t.Logf("Snapshot written: %s", path)
}

func TestTUI025_VisualSnapshot_200x50(t *testing.T) {
	content := renderCompletionSnapshot(200, 50)
	path := filepath.Join(snapshotDir, "TUI-025-completion-200x50.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	t.Logf("Snapshot written: %s", path)
}

// TestCompletionDurationIsFrozenAtStop pins issue #1434: "Worked for <d>"
// states how long the run took, so it must not depend on when you look at it.
//
// ElapsedSeconds() reads the wall clock, and View re-rendered it on every tick
// of the completion window, so a finished run's duration kept climbing —
// 5.0s, 5.3s, 5.6s — inflating the reported figure by up to the window's
// length. For a short run that is a large relative error.
//
// The assertion is that successive renders are identical, not that the value
// equals some number: asserting a number would be timing-dependent and flaky,
// and would not capture the property that actually matters.
func TestCompletionDurationIsFrozenAtStop(t *testing.T) {
	m := New(0).Start()
	m.startTime = time.Now().Add(-5 * time.Second)
	m = m.Stop(100)

	first := m.View(80)
	if !strings.Contains(first, "Worked for") {
		t.Fatalf("expected a completion line, got %q", first)
	}
	// Control: the frozen value must still reflect the real elapsed time, so a
	// fix that freezes it at zero or drops the duration fails here.
	if !strings.Contains(first, "5.") {
		t.Fatalf("frozen duration should reflect the ~5s run, got %q", first)
	}

	for i := 0; i < 3; i++ {
		time.Sleep(250 * time.Millisecond)
		m = m.Tick()
		if got := m.View(80); got != first {
			t.Fatalf("completion duration changed while displayed:\n first: %q\n after tick %d: %q",
				first, i+1, got)
		}
	}
}
