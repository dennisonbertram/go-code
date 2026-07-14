package modelswitcher_test

// Additional regression coverage for BUG A that exercises the two render
// loops NOT covered by bugA_scroll_test.go's level-1 (viewModelsForProvider)
// reproduction: the level-0 provider list (viewProviderList) and the flat
// cross-provider search view (viewFlatModelList). All three loops had the
// same independent "shrink windowEnd by up to two rows" duplication before
// the fix, so a regression in any one of them would reintroduce the stuck
// cursor for that specific view even if the other two remained correct.
// These tests would fail if scrollWindow()/effectiveContentRows() stopped
// being the single shared budget for all three render loops.

import (
	"fmt"
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// manyProviderModels returns n synthetic models spread across n distinct
// providers (one model each), so the provider list itself — not any single
// provider's model list — is long enough to require scrolling.
func manyProviderModels(n int) []modelswitcher.ServerModelEntry {
	entries := make([]modelswitcher.ServerModelEntry, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, modelswitcher.ServerModelEntry{
			ID:       fmt.Sprintf("provmodel-%03d", i),
			Provider: fmt.Sprintf("prov%03d", i),
		})
	}
	return entries
}

// TestBugA_Regression_ProviderListCursorNeverLosesVisibility covers the
// level-0 provider list scroll window (viewProviderList), which is a
// separate render loop from the level-1 model list covered by
// bugA_scroll_test.go.
func TestBugA_Regression_ProviderListCursorNeverLosesVisibility(t *testing.T) {
	const total = 50
	m := modelswitcher.New("provmodel-000").
		WithModels(manyProviderModels(total)).
		Open().
		WithMaxHeight(20)

	for i := 0; i < total; i++ {
		m = m.ProviderDown()
		view := m.View(80)
		if !strings.Contains(view, "> ") {
			t.Fatalf("ProviderDown() press %d of %d: cursor marker '> ' missing from provider-list View():\n%s", i+1, total, view)
		}
	}
}

// TestBugA_Regression_SearchViewCursorNeverLosesVisibility covers the flat
// cross-provider search view (viewFlatModelList), the third render loop
// that independently duplicated the scroll-indicator reservation logic.
func TestBugA_Regression_SearchViewCursorNeverLosesVisibility(t *testing.T) {
	const total = 80
	m := modelswitcher.New("test-model-000").
		WithModels(longSyntheticModelList(total)).
		Open().
		WithMaxHeight(25).
		SetSearch("test-model") // matches every synthetic entry -> flat search view

	for i := 0; i < total; i++ {
		m = m.SelectDown()
		view := m.View(80)
		if !strings.Contains(view, "> ") {
			t.Fatalf("SelectDown() press %d of %d in search view: cursor marker '> ' missing from View():\n%s", i+1, total, view)
		}
	}
}
