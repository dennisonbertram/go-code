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

// TestTUI024_SpinnerCyclesFrames verifies that Tick() advances frame index
// through all 6 frames in order and wraps back to 0.
func TestTUI024_SpinnerCyclesFrames(t *testing.T) {
	m := New(42)
	m = m.Start()

	// Frame starts at 0 after Start.
	if m.frame != 0 {
		t.Fatalf("expected frame=0 after Start, got %d", m.frame)
	}

	for i := 1; i <= 6; i++ {
		m = m.Tick()
		expected := i % len(frames)
		if m.frame != expected {
			t.Errorf("after Tick %d: expected frame=%d, got %d", i, expected, m.frame)
		}
	}
}

// TestTUI024_SpinnerAddsDurationAfterThreshold verifies that View() includes
// a duration string once the spinner has been active for 2 seconds or more.
func TestTUI024_SpinnerAddsDurationAfterThreshold(t *testing.T) {
	m := New(42)
	m = m.Start()

	// Before the threshold: no duration string should appear.
	viewBefore := m.View(80)
	if strings.Contains(viewBefore, "s)") {
		t.Errorf("View() before threshold should not contain duration, got: %q", viewBefore)
	}

	// Manually set startTime 3 seconds in the past to exceed threshold.
	m.startTime = time.Now().Add(-3 * time.Second)
	viewAfter := m.View(80)
	if !strings.Contains(viewAfter, "s)") && !strings.Contains(viewAfter, "m") {
		t.Errorf("View() after 3s should contain duration, got: %q", viewAfter)
	}
}

// TestTUI024_SpinnerVerbFromSeed verifies that using the same seed always
// produces the same initial verb after Start().
func TestTUI024_SpinnerVerbFromSeed(t *testing.T) {
	const seed = int64(12345)

	m1 := New(seed)
	m1 = m1.Start()

	m2 := New(seed)
	m2 = m2.Start()

	if m1.verb != m2.verb {
		t.Errorf("same seed should produce same verb: m1=%q, m2=%q", m1.verb, m2.verb)
	}

	if m1.verb == "" {
		t.Error("verb should not be empty after Start()")
	}
}

// TestTUI024_SpinnerStopsCleanly verifies that Stop() transitions the model to
// done=true, active=false and stores the token count.
func TestTUI024_SpinnerStopsCleanly(t *testing.T) {
	m := New(42)
	m = m.Start()

	if !m.IsActive() {
		t.Fatal("expected IsActive()=true after Start()")
	}
	if m.IsDone() {
		t.Fatal("expected IsDone()=false after Start()")
	}

	const tokenCount = 1234
	m = m.Stop(tokenCount)

	if m.IsActive() {
		t.Error("expected IsActive()=false after Stop()")
	}
	if !m.IsDone() {
		t.Error("expected IsDone()=true after Stop()")
	}
	if m.tokens != tokenCount {
		t.Errorf("expected tokens=%d, got %d", tokenCount, m.tokens)
	}
}

// TestTUI024_CompletionLineFormat verifies the CompletionLine() format.
func TestTUI024_CompletionLineFormat(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(100)

	// Test with a known duration — integer seconds.
	line := m.CompletionLine(5.0)
	if !strings.Contains(line, "5.0s") {
		t.Errorf("CompletionLine(5.0) should contain '5.0s', got: %q", line)
	}
	if !strings.Contains(line, "Worked for") {
		t.Errorf("CompletionLine(5.0) should contain 'Worked for', got: %q", line)
	}

	// Frame glyph must appear.
	foundGlyph := false
	for _, f := range frames {
		if strings.Contains(line, f) {
			foundGlyph = true
			break
		}
	}
	if !foundGlyph {
		t.Errorf("CompletionLine should contain a frame glyph, got: %q", line)
	}
}

// TestTUI024_CompletionLineFormatMinutes verifies minute formatting.
func TestTUI024_CompletionLineFormatMinutes(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(100)

	// 90 seconds should render as "1m 30s".
	line := m.CompletionLine(90.0)
	if !strings.Contains(line, "1m") {
		t.Errorf("CompletionLine(90.0) should contain '1m', got: %q", line)
	}
	if !strings.Contains(line, "30s") {
		t.Errorf("CompletionLine(90.0) should contain '30s', got: %q", line)
	}
}

// TestTUI024_ConcurrentIndependentState verifies that 10 goroutines each
// operating on their own Model instance do not race or share global state.
func TestTUI024_ConcurrentIndependentState(t *testing.T) {
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		seed := int64(i)
		go func(s int64) {
			defer wg.Done()
			m := New(s)
			m = m.Start()
			for j := 0; j < 20; j++ {
				m = m.Tick()
				_ = m.View(80)
			}
			m = m.Stop(int(s * 100))
			_ = m.CompletionLine(float64(s))
		}(seed)
	}

	wg.Wait()
}

// TestTUI024_EmptyVerbFallback verifies that an empty verb pool falls back
// to "Thinking".
func TestTUI024_EmptyVerbFallback(t *testing.T) {
	m := New(42)
	// Override verbs to empty slice via test helper.
	m.testVerbs = []string{}
	m = m.Start()

	if m.verb != "Thinking" {
		t.Errorf("empty verb pool should fall back to 'Thinking', got %q", m.verb)
	}
}

// TestTUI024_BoundaryWidths verifies that View() does not panic at various
// terminal widths including very narrow and very wide.
func TestTUI024_BoundaryWidths(t *testing.T) {
	m := New(42)
	m = m.Start()

	widths := []int{5, 10, 80, 120, 200}
	for _, w := range widths {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View(%d) panicked: %v", w, r)
				}
			}()
			result := m.View(w)
			// Must be non-empty for active spinner.
			if result == "" {
				t.Errorf("View(%d) returned empty string for active spinner", w)
			}
		}()
	}
}

// TestSpinnerShowsCancelHintWhileActive verifies that View() always surfaces
// the cancel hint while the spinner is active, using the rotating verb when
// no current action has been set.
func TestSpinnerShowsCancelHintWhileActive(t *testing.T) {
	m := New(42)
	m = m.Start()

	view := m.View(80)
	if !strings.Contains(view, CancelHint) {
		t.Errorf("active View() should contain cancel hint %q, got: %q", CancelHint, view)
	}
	if !strings.Contains(view, m.verb) {
		t.Errorf("active View() with no action set should still show the verb %q, got: %q", m.verb, view)
	}
}

// TestSpinnerShowsCurrentActionInsteadOfVerb verifies that once SetAction is
// called with a non-empty label, View() displays that label instead of the
// rotating verb, while still showing the cancel hint.
func TestSpinnerShowsCurrentActionInsteadOfVerb(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.SetAction("Running bash")

	view := m.View(80)
	if !strings.Contains(view, "Running bash") {
		t.Errorf("View() should show the current action, got: %q", view)
	}
	if strings.Contains(view, m.verb+"...") {
		t.Errorf("View() should not show the rotating verb once an action is set, got: %q", view)
	}
	if !strings.Contains(view, CancelHint) {
		t.Errorf("View() with an action set should still contain cancel hint %q, got: %q", CancelHint, view)
	}
}

// TestSpinnerClearingActionRestoresVerb verifies that SetAction("") reverts
// View() back to the rotating verb.
func TestSpinnerClearingActionRestoresVerb(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.SetAction("Running bash")
	m = m.SetAction("")

	view := m.View(80)
	if strings.Contains(view, "Running bash") {
		t.Errorf("View() should not show a cleared action, got: %q", view)
	}
	if !strings.Contains(view, m.verb) {
		t.Errorf("View() should fall back to the verb once action is cleared, got: %q", view)
	}
}

// TestSpinnerNoCancelHintAfterCompletion verifies that the cancel hint does
// not linger in the completion line — there is nothing left to cancel once
// the spinner has stopped.
func TestSpinnerNoCancelHintAfterCompletion(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(10)

	view := m.View(80)
	if strings.Contains(view, CancelHint) {
		t.Errorf("completion View() should not contain the cancel hint, got: %q", view)
	}
}

// ─── Regression Tests ────────────────────────────────────────────────────────

// TestTUI024_Regression_NoGoroutineStateAfterStop verifies that a stopped
// spinner has no dangling active state.
func TestTUI024_Regression_NoGoroutineStateAfterStop(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(0)

	if m.active {
		t.Error("regression: active should be false after Stop()")
	}
	if !m.done {
		t.Error("regression: done should be true after Stop()")
	}
	// Tick after Stop should not re-activate the spinner.
	m = m.Tick()
	if m.active {
		t.Error("regression: Tick() after Stop() must not set active=true")
	}
}

// TestTUI024_Regression_ZeroTokens verifies that Stop(0) is valid.
func TestTUI024_Regression_ZeroTokens(t *testing.T) {
	m := New(42)
	m = m.Start()
	m = m.Stop(0)

	if m.tokens != 0 {
		t.Errorf("regression: Stop(0) should store 0 tokens, got %d", m.tokens)
	}
	line := m.CompletionLine(1.0)
	if line == "" {
		t.Error("regression: CompletionLine should not be empty even with 0 tokens")
	}
}

// TestTUI024_Regression_LargeTokenCount verifies large token counts (1M+) do
// not cause panics or incorrect formatting.
func TestTUI024_Regression_LargeTokenCount(t *testing.T) {
	const large = 1_000_000
	m := New(42)
	m = m.Start()
	m = m.Stop(large)

	if m.tokens != large {
		t.Errorf("regression: expected tokens=%d, got %d", large, m.tokens)
	}
	// CompletionLine must not panic.
	line := m.CompletionLine(120.0)
	if line == "" {
		t.Error("regression: CompletionLine must not be empty for large token count")
	}
}

// TestTUI024_Regression_MultipleInstances verifies that multiple Model
// instances are fully independent (no global state pollution).
func TestTUI024_Regression_MultipleInstances(t *testing.T) {
	a := New(1)
	b := New(2)

	a = a.Start()
	b = b.Start()

	// Advance a several times.
	for i := 0; i < 3; i++ {
		a = a.Tick()
	}

	// b should still be at frame 0.
	if b.frame != 0 {
		t.Errorf("regression: b.frame should be 0, got %d (a.frame=%d)", b.frame, a.frame)
	}
}

// ─── Visual Snapshot Tests ────────────────────────────────────────────────────

// snapshotDir is relative to the package directory.
const snapshotDir = "testdata/snapshots"

func writeSnapshot(t *testing.T, name string, content string) {
	t.Helper()
	path := filepath.Join(snapshotDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write snapshot %s: %v", path, err)
	}
}

// renderSnapshot renders the spinner at the given width and returns a framed
// visual snapshot string.
func renderSnapshot(width, height int) string {
	m := New(42)
	m = m.Start()
	// Simulate a few ticks for visual variety.
	for i := 0; i < 3; i++ {
		m = m.Tick()
	}

	var sb strings.Builder
	// Header.
	sb.WriteString(fmt.Sprintf("# TUI-024 Spinner Snapshot %dx%d\n", width, height))
	sb.WriteString(strings.Repeat("-", width))
	sb.WriteString("\n")

	// Active spinner view.
	sb.WriteString("## Active (no duration)\n")
	sb.WriteString(m.View(width))
	sb.WriteString("\n")

	// With duration.
	m.startTime = time.Now().Add(-5 * time.Second)
	sb.WriteString("\n## Active (5s elapsed)\n")
	sb.WriteString(m.View(width))
	sb.WriteString("\n")

	// Completion line.
	m = m.Stop(500)
	sb.WriteString("\n## Completion Line\n")
	sb.WriteString(m.CompletionLine(5.0))
	sb.WriteString("\n")

	sb.WriteString(strings.Repeat("-", width))
	sb.WriteString("\n")
	return sb.String()
}

func TestTUI024_VisualSnapshot_80x24(t *testing.T) {
	content := renderSnapshot(80, 24)
	writeSnapshot(t, "TUI-024-spinner-80x24.txt", content)
	t.Logf("Snapshot written: %s/TUI-024-spinner-80x24.txt", snapshotDir)
}

func TestTUI024_VisualSnapshot_120x40(t *testing.T) {
	content := renderSnapshot(120, 40)
	writeSnapshot(t, "TUI-024-spinner-120x40.txt", content)
	t.Logf("Snapshot written: %s/TUI-024-spinner-120x40.txt", snapshotDir)
}

func TestTUI024_VisualSnapshot_200x50(t *testing.T) {
	content := renderSnapshot(200, 50)
	writeSnapshot(t, "TUI-024-spinner-200x50.txt", content)
	t.Logf("Snapshot written: %s/TUI-024-spinner-200x50.txt", snapshotDir)
}
