package spinner

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestSpinnerLabelDoesNotRotateOnTicks pins the core of issue #1415: the label
// changes when the run's state changes, never because a timer advanced.
//
// The old model re-rolled a random verb every 8 ticks (~960ms at the 120ms tick
// rate), so the line changed roughly once a second while telling the user
// nothing new. Motion without novelty reads as a stuck animation.
func TestSpinnerLabelDoesNotRotateOnTicks(t *testing.T) {
	m := New(42).Start().SetAction("Running bash")

	first := m.View(80)
	for i := 0; i < 30; i++ { // well past the old verbRotateEvery = 8
		m = m.Tick()
		if got := m.View(80); labelOf(got) != labelOf(first) {
			t.Fatalf("label changed on tick %d without any state change:\n before: %q\n after:  %q",
				i+1, first, got)
		}
	}
}

// TestSpinnerGlyphStillAnimates is the control for the test above: freezing the
// whole line would satisfy "label does not change" while making the spinner look
// hung. Only the word is allowed to hold still.
func TestSpinnerGlyphStillAnimates(t *testing.T) {
	m := New(42).Start().SetAction("Running bash")

	// The bound is the longest hold in the eased cadence (issue #1420), not one
	// tick per frame: the extremes of the breath are held for several ticks.
	seen := map[string]bool{}
	ticks := len(holds) * 3
	for i := 0; i < ticks; i++ {
		seen[strings.Fields(m.View(80))[0]] = true
		m = m.Tick()
	}
	if len(seen) < 2 {
		t.Fatalf("glyph never advanced across %d ticks; the spinner would look frozen", ticks)
	}
}

// TestSpinnerLabelIsSeedIndependent is the deliberate inversion of the old
// TestTUI024_SpinnerVerbFromSeed. Once the label is a function of run state, two
// spinners in the same state must read identically regardless of seed — there is
// no longer a random source to seed.
func TestSpinnerLabelIsSeedIndependent(t *testing.T) {
	a := New(1).Start().SetAction("Thinking")
	b := New(999999).Start().SetAction("Thinking")

	if labelOf(a.View(80)) != labelOf(b.View(80)) {
		t.Fatalf("label depends on seed: %q vs %q", a.View(80), b.View(80))
	}
}

// TestSpinnerFallbackLabelWhenNoActionKnown pins that an empty action degrades
// to one neutral, truthful word rather than a random verb.
func TestSpinnerFallbackLabelWhenNoActionKnown(t *testing.T) {
	m := New(7).Start()
	got := labelOf(m.View(80))
	if got != fallbackLabel {
		t.Fatalf("empty action should render %q, got %q", fallbackLabel, got)
	}
	if strings.Contains(m.View(80), "...") {
		t.Errorf("label should not carry an ellipsis: %q", m.View(80))
	}
}

// labelOf extracts the label from a rendered spinner line, dropping the leading
// glyph and any trailing duration or cancel hint.
func labelOf(view string) string {
	fields := strings.Fields(view)
	if len(fields) < 2 {
		return ""
	}
	var out []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "(") {
			break
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// TestSpinnerKeepsCancelHintAtNarrowWidth pins the width trade-off introduced
// with truthful labels: "Waiting for gpt-4.1-mini" is much longer than the
// "Computing..." it replaced, and a right-truncated line would eat the cancel
// hint first — leaving "(esc to inter" and no way to learn how to stop the run.
// The label yields; the hint does not.
func TestSpinnerKeepsCancelHintAtNarrowWidth(t *testing.T) {
	m := New(0).Start().SetAction("Waiting for some-extremely-long-model-name-v2")

	for _, width := range []int{40, 50, 60} {
		view := m.View(width)
		if !strings.Contains(view, CancelHint) {
			t.Errorf("width %d: cancel hint dropped, got %q", width, view)
		}
		if got := lipgloss.Width(view); got > width {
			t.Errorf("width %d: line is %d columns wide: %q", width, got, view)
		}
	}
}

// TestSpinnerHintSurvivesEvenWhenLabelCannotFit covers the degenerate case: when
// not even a stub of a label fits, the actionable half is what remains.
func TestSpinnerHintSurvivesEvenWhenLabelCannotFit(t *testing.T) {
	m := New(0).Start().SetAction("Waiting for a model with an absurd name")
	view := m.View(24)
	if !strings.Contains(view, CancelHint) {
		t.Errorf("cancel hint should outrank the label when space is scarce, got %q", view)
	}
}

// TestSpinnerKeepsCancelHintWithWideRunes pins issue #1432: the hint survives
// regardless of the label's script.
//
// shortenLabel budgeted in display columns but truncated by rune count. For
// CJK and emoji, which occupy two columns per rune, the label overran its
// budget and the final MaxWidth clamp ate the hint — the exact outcome
// shortenLabel exists to prevent. Every existing test passed throughout,
// because they were all ASCII, where columns and runes coincide.
//
// The assertion is on the hint's survival, not on the line's width: the width
// was always correct, which is why this went unnoticed.
func TestSpinnerKeepsCancelHintWithWideRunes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
	}{
		{name: "cjk", label: "Waiting for 模型模型模型模型模型模型模型模型"},
		{name: "emoji", label: "Running 🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀"},
		// ASCII control: a fix that over-truncates everything to satisfy the
		// hint assertion would show up here as a needlessly stunted label.
		{name: "ascii control", label: "Waiting for some-long-model-name-v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(0).Start().SetAction(tc.label)
			for _, width := range []int{40, 50} {
				view := m.View(width)
				if !strings.Contains(view, CancelHint) {
					t.Errorf("width %d: cancel hint truncated, got %q", width, view)
				}
				if got := lipgloss.Width(view); got > width {
					t.Errorf("width %d: line is %d columns wide: %q", width, got, view)
				}
			}
		})
	}
}
