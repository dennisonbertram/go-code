package modelswitcher_test

// Regression coverage for user-reported BUG A (P1): in a long model list the
// highlighted cursor stops moving and never returns, while the "... N more
// below" counter keeps ticking down on each keypress.
//
// Root cause: a scroll-window budget mismatch. model.go's
// maxVisibleContentRows() computes one row budget, and adjustScroll() uses
// that budget verbatim to decide when to shift scrollOffset. But each of the
// three render loops in view.go independently shrinks the *rendered* window
// by up to two rows to make room for the "... more above" / "... more below"
// indicator lines. adjustScroll never learns about that shrinkage, so once
// both indicators are showing, the selected row can fall permanently outside
// the window actually rendered — freezing the visible cursor.

import (
	"fmt"
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// longSyntheticModelList returns n synthetic server models all under a single
// provider, so drilling into that provider produces a list far longer than
// any reasonable terminal window.
func longSyntheticModelList(n int) []modelswitcher.ServerModelEntry {
	entries := make([]modelswitcher.ServerModelEntry, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, modelswitcher.ServerModelEntry{
			ID:       fmt.Sprintf("test-model-%03d", i),
			Provider: "testprov",
		})
	}
	return entries
}

// TestBugA_CursorNeverLosesVisibility_LongList is the required regression
// test: with a list much longer than the window, call SelectDown()
// repeatedly through the entire list and assert the "> " cursor marker is
// present in View() output at EVERY step.
func TestBugA_CursorNeverLosesVisibility_LongList(t *testing.T) {
	const total = 80
	m := modelswitcher.New("test-model-000").
		WithModels(longSyntheticModelList(total)).
		Open().
		WithMaxHeight(30) // matches the MaxHeight used in the verified bug reproduction

	provs := m.Providers()
	if len(provs) != 1 {
		t.Fatalf("test setup: expected exactly one synthetic provider, got %d: %+v", len(provs), provs)
	}
	m = m.DrillIntoProvider()

	for i := 0; i < total; i++ {
		m = m.SelectDown()
		view := m.View(80)
		if !strings.Contains(view, "> ") {
			t.Fatalf("SelectDown() press %d of %d: cursor marker '> ' missing from View() — cursor is stuck/invisible:\n%s", i+1, total, view)
		}
	}
}

// TestBugA_SelectUpFromZeroWrapsAndStaysVisible verifies that SelectUp() from
// index 0 wraps around to the last entry and that entry remains visible
// (rather than landing outside the rendered scroll window).
func TestBugA_SelectUpFromZeroWrapsAndStaysVisible(t *testing.T) {
	const total = 80
	m := modelswitcher.New("test-model-000").
		WithModels(longSyntheticModelList(total)).
		Open().
		WithMaxHeight(30)

	m = m.DrillIntoProvider() // Selected == 0

	m = m.SelectUp() // wraps to the last entry (index total-1)
	view := m.View(80)
	if !strings.Contains(view, "> ") {
		t.Fatalf("SelectUp() wrap from index 0 to last entry: cursor marker '> ' missing from View():\n%s", view)
	}
}

// TestBugA_ScrollBudgetSharedBetweenModelAndView is a narrower reproduction
// of the reported symptom: at the exact MaxHeight from the bug report, once
// both scroll indicators are showing, continuing to press SelectDown() must
// never leave the cursor stuck at the same rendered position while the
// "more below" counter keeps changing.
func TestBugA_ScrollBudgetSharedBetweenModelAndView(t *testing.T) {
	const total = 60
	m := modelswitcher.New("test-model-000").
		WithModels(longSyntheticModelList(total)).
		Open().
		WithMaxHeight(30)
	m = m.DrillIntoProvider()

	sawBothIndicators := false
	for i := 0; i < total-1; i++ {
		m = m.SelectDown()
		view := m.View(80)
		if strings.Contains(view, "more above") && strings.Contains(view, "more below") {
			sawBothIndicators = true
		}
		if !strings.Contains(view, "> ") {
			t.Fatalf("press %d: cursor vanished from view (both indicators seen so far: %v):\n%s", i+1, sawBothIndicators, view)
		}
	}
	if !sawBothIndicators {
		t.Fatalf("test setup did not exercise the both-indicators-showing case that triggers the bug; adjust total/MaxHeight")
	}
}
