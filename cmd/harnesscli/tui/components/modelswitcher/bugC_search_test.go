package modelswitcher_test

// Regression coverage for user-reported BUG C (P2): "search is invisible and
// collides with the star key".
//
// (1) The footer advertises "/ search" but '/' was a dead key — nothing in
//     the component or in tui/model.go ever entered a discoverable search
//     mode. Search only "worked" by accident: any other printable rune fell
//     through to HandleSearchKey.
// (2) At browse level 1 with no active search, 's' toggles star, so typing a
//     query like "sonnet" starred the highlighted model on the very first
//     keystroke instead of starting a query.
// (3) Search used plain substring matching only, which is poor for large
//     catalogs (OpenRouter has 300+ models) — a query like "gp4m" for
//     "gpt-4.1-mini" would find nothing.
//
// This file covers the component-level pieces of the fix: EnterSearch() /
// SearchActive() as an explicit, discoverable search-mode signal, and
// improved (fuzzy/subsequence) match ranking. The '/' key wiring and the
// s/star disambiguation live in tui/model.go and are covered by
// cmd/harnesscli/tui/model_bugC_search_test.go.

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestBugC_EnterSearchActivatesFlatViewEvenWithEmptyQuery verifies that
// EnterSearch() makes the search UI visible (SearchActive() true, and the
// "Filter:" search bar rendered) even before the user has typed a single
// character. Today there is no way to reach this state at all — the only
// way search becomes visible is by accident, once SearchQuery() is already
// non-empty.
func TestBugC_EnterSearchActivatesFlatViewEvenWithEmptyQuery(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	if m.SearchActive() {
		t.Fatal("precondition: SearchActive() should be false before EnterSearch()")
	}

	m2 := m.EnterSearch()
	if !m2.SearchActive() {
		t.Error("EnterSearch() should set SearchActive() to true")
	}
	if m2.SearchQuery() != "" {
		t.Errorf("EnterSearch() should not itself add any text to the query, got %q", m2.SearchQuery())
	}

	v := m2.View(80)
	if !strings.Contains(v, "Filter:") {
		t.Errorf("View() after EnterSearch() should render the 'Filter:' search bar even with an empty query:\n%s", v)
	}
}

// TestBugC_SetSearchEmptyClearsSearchActive verifies that clearing the query
// back to "" (e.g. via Escape in tui/model.go, which calls SetSearch(""))
// also exits explicit search mode, so browsing returns to the normal
// provider/model list view instead of leaving an empty, orphaned search bar.
func TestBugC_SetSearchEmptyClearsSearchActive(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open().EnterSearch().SetSearch("claude")
	if !m.SearchActive() {
		t.Fatal("precondition: SearchActive() should be true while a query is set")
	}

	m2 := m.SetSearch("")
	if m2.SearchActive() {
		t.Error("SetSearch(\"\") should clear SearchActive()")
	}
}

// TestBugC_MatchRanking_ExactPrefixSubstringFuzzy verifies the requested
// match-quality ordering (exact > prefix > substring/fuzzy) and that a
// subsequence ("fuzzy") match is surfaced at all — previously an entry that
// matched only as a non-contiguous subsequence was excluded from results
// entirely, which is the core complaint for large catalogs like OpenRouter.
func TestBugC_MatchRanking_ExactPrefixSubstringFuzzy(t *testing.T) {
	entries := []modelswitcher.ServerModelEntry{
		// Fuzzy: "gpt" appears as a subsequence (g_p_t) but not contiguously,
		// and its DisplayName has no relation to the query.
		{ID: "zzz-gxpxtx", Provider: "synth", DisplayName: "BBB Fuzzy"},
		// Substring: "gpt" appears mid-string but not as a prefix.
		{ID: "xxgptxx", Provider: "synth", DisplayName: "MMM Substring"},
		// Prefix: ID starts with "gpt" but is not an exact match. DisplayName
		// is deliberately alphabetically EARLIER than the exact entry's, so a
		// pass that only used alphabetical/DisplayName ordering (rather than
		// real match-quality tiering) would rank this ahead of the exact
		// match — which must not happen.
		{ID: "gpt-turbo", Provider: "synth", DisplayName: "AAA Prefix"},
		// Exact: ID is exactly the query.
		{ID: "gpt", Provider: "synth", DisplayName: "ZZZ Exact"},
	}

	m := modelswitcher.New("gpt").WithModels(entries).Open().EnterSearch().SetSearch("gpt")

	var order []string
	cur := m
	for i := 0; i < len(entries); i++ {
		e, _ := cur.Accept()
		order = append(order, e.ID)
		cur = cur.SelectDown()
	}

	want := []string{"gpt", "gpt-turbo", "xxgptxx", "zzz-gxpxtx"}
	if len(order) != len(want) {
		t.Fatalf("visible result count = %d, want %d; order=%v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("result[%d] = %q, want %q (full order: %v)", i, order[i], want[i], order)
		}
	}
}

// TestBugC_FuzzyMatchFindsNonContiguousSubsequence is a narrower,
// human-readable reproduction of the autocomplete complaint: a query typed
// as an abbreviation (letters in order, not contiguous) should still surface
// the model it identifies, which matters most for a 300+ entry list like
// OpenRouter's.
func TestBugC_FuzzyMatchFindsNonContiguousSubsequence(t *testing.T) {
	entries := []modelswitcher.ServerModelEntry{
		{ID: "openai/gpt-4.1-mini", Provider: "openrouter", DisplayName: "GPT-4.1 Mini (via OpenRouter)"},
		{ID: "anthropic/claude-opus-4-6", Provider: "openrouter", DisplayName: "Claude Opus 4.6 (via OpenRouter)"},
	}
	m := modelswitcher.New("openai/gpt-4.1-mini").WithModels(entries).Open().EnterSearch().SetSearch("gp4m")

	entry, _ := m.Accept()
	if entry.ID != "openai/gpt-4.1-mini" {
		t.Errorf("fuzzy query 'gp4m' should surface openai/gpt-4.1-mini, got %q", entry.ID)
	}
}
