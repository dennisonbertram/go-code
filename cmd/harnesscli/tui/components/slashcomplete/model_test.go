package slashcomplete_test

import (
	"os"
	"strings"
	"sync"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/slashcomplete"
)

// testSuggestions is a standard set of suggestions used across tests.
func testSuggestions() []slashcomplete.Suggestion {
	return []slashcomplete.Suggestion{
		{Name: "clear", Description: "Clear conversation history"},
		{Name: "context", Description: "Show context usage grid"},
		{Name: "help", Description: "Show help dialog"},
		{Name: "quit", Description: "Quit the TUI"},
		{Name: "stats", Description: "Show usage statistics"},
	}
}

// TestTUI042_AutocompleteOpensOnSlashPrefix verifies Open() sets IsActive to true.
func TestTUI042_AutocompleteOpensOnSlashPrefix(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	if m.IsActive() {
		t.Fatal("model should start inactive")
	}
	m = m.Open()
	if !m.IsActive() {
		t.Fatal("Open() should make model active")
	}
}

// TestTUI042_EnterSelectsSuggestion verifies Selected() returns the current cursor position.
func TestTUI042_EnterSelectsSuggestion(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()

	s, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false but model has suggestions")
	}
	// With empty query all suggestions shown; first should be "clear" (index 0)
	if s.Name != "clear" {
		t.Errorf("Selected().Name = %q, want %q", s.Name, "clear")
	}
}

// TestTUI042_DownMovesSelection verifies Down() advances the selected index.
func TestTUI042_DownMovesSelection(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()

	first, _ := m.Selected()
	m = m.Down()
	second, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after Down()")
	}
	if second.Name == first.Name {
		t.Errorf("Down() did not advance selection: still %q", second.Name)
	}
	// second should be "context" (index 1)
	if second.Name != "context" {
		t.Errorf("After Down(): got %q, want %q", second.Name, "context")
	}
}

// TestTUI042_UpWrapsSelection verifies Up() at pos=0 wraps to the last item.
func TestTUI042_UpWrapsSelection(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open() // selected = 0

	m = m.Up() // should wrap to last
	s, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after Up() wrap")
	}
	filtered := m.Filtered()
	last := filtered[len(filtered)-1]
	if s.Name != last.Name {
		t.Errorf("Up() from 0 should wrap to last (%q), got %q", last.Name, s.Name)
	}
}

// TestTUI042_FilterByQuery verifies query "cl" returns only "clear".
func TestTUI042_FilterByQuery(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	m = m.SetQuery("cl")

	filtered := m.Filtered()
	if len(filtered) != 1 {
		t.Fatalf("SetQuery(\"cl\"): got %d results, want 1; results=%v", len(filtered), filtered)
	}
	if filtered[0].Name != "clear" {
		t.Errorf("SetQuery(\"cl\"): got %q, want %q", filtered[0].Name, "clear")
	}
}

// TestTUI042_EmptyQueryShowsAll verifies empty query returns all suggestions.
func TestTUI042_EmptyQueryShowsAll(t *testing.T) {
	suggestions := testSuggestions()
	m := slashcomplete.New(suggestions)
	m = m.Open()
	m = m.SetQuery("")

	filtered := m.Filtered()
	if len(filtered) != len(suggestions) {
		t.Errorf("empty query: got %d results, want %d", len(filtered), len(suggestions))
	}
}

// TestTUI042_CloseDeactivates verifies Close() sets IsActive to false.
func TestTUI042_CloseDeactivates(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	if !m.IsActive() {
		t.Fatal("model should be active after Open()")
	}
	m = m.Close()
	if m.IsActive() {
		t.Fatal("Close() should deactivate model")
	}
}

// TestTUI042_AcceptReturnsText verifies Accept() returns "/name " text.
func TestTUI042_AcceptReturnsText(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()

	first, _ := m.Selected()
	newM, text := m.Accept()

	want := "/" + first.Name + " "
	if text != want {
		t.Errorf("Accept() text = %q, want %q", text, want)
	}
	// Accept closes the dropdown
	if newM.IsActive() {
		t.Error("Accept() should close the dropdown")
	}
}

// TestTUI042_ZeroResults verifies Selected() returns (zero, false) when no matches.
func TestTUI042_ZeroResults(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	m = m.SetQuery("zzznomatch")

	filtered := m.Filtered()
	if len(filtered) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(filtered))
	}

	_, ok := m.Selected()
	if ok {
		t.Error("Selected() should return ok=false when filtered is empty")
	}
}

// TestTUI042_ConcurrentDropdown verifies 10 goroutines each with their own Model have no race conditions.
func TestTUI042_ConcurrentDropdown(t *testing.T) {
	suggestions := testSuggestions()
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			m := slashcomplete.New(suggestions)
			m = m.Open()
			m = m.SetQuery("c")
			m = m.Down()
			m = m.Up()
			_, _ = m.Selected()
			_ = m.Filtered()
			_, _ = m.Accept()
			m = m.Close()
			_ = m.IsActive()
		}(i)
	}
	wg.Wait()
}

// TestTUI042_MaxVisibleRows verifies that more than maxVisible items trigger truncation in the view.
func TestTUI042_MaxVisibleRows(t *testing.T) {
	// Create more suggestions than the default maxVisible (8)
	many := make([]slashcomplete.Suggestion, 12)
	for i := range many {
		many[i] = slashcomplete.Suggestion{
			Name:        strings.Repeat("a", i+1), // a, aa, aaa, ...
			Description: "item",
		}
	}
	m := slashcomplete.New(many)
	m = m.Open()

	// Filtered returns all 12
	if len(m.Filtered()) != 12 {
		t.Fatalf("expected 12 filtered items, got %d", len(m.Filtered()))
	}

	// View with truncation marker: at most maxVisible rows + possible truncation line
	view := m.View(80)
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")

	// Should have at most maxVisible + 1 (for "... N more") lines
	maxAllowed := 9 // 8 visible + 1 truncation
	if len(lines) > maxAllowed {
		t.Errorf("View produced %d lines, want at most %d", len(lines), maxAllowed)
	}

	// Must contain truncation marker
	found := false
	for _, line := range lines {
		if strings.Contains(line, "more") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected truncation marker 'more' in view, lines=%v", lines)
	}
}

// TestTUI042_VisualSnapshot_80x24 writes the autocomplete dropdown view at width=80.
func TestTUI042_VisualSnapshot_80x24(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	output := m.View(80)

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-042-autocomplete-80x24.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)

	// Basic validity: non-empty output
	if strings.TrimSpace(output) == "" {
		t.Error("View() returned empty output")
	}
	// Must contain at least one suggestion name
	if !strings.Contains(output, "clear") {
		t.Error("snapshot should contain 'clear'")
	}
}

// TestTUI042_VisualSnapshot_120x40 writes the autocomplete dropdown view at width=120.
func TestTUI042_VisualSnapshot_120x40(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	output := m.View(120)

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-042-autocomplete-120x40.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)

	if strings.TrimSpace(output) == "" {
		t.Error("View() returned empty output at width 120")
	}
}

// TestTUI042_VisualSnapshot_200x50 writes the autocomplete dropdown view at width=200.
func TestTUI042_VisualSnapshot_200x50(t *testing.T) {
	m := slashcomplete.New(testSuggestions())
	m = m.Open()
	output := m.View(200)

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-042-autocomplete-200x50.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)

	if strings.TrimSpace(output) == "" {
		t.Error("View() returned empty output at width 200")
	}
}

// make14Suggestions returns 14 suggestions for scroll tests.
func make14Suggestions() []slashcomplete.Suggestion {
	names := []string{
		"aaa", "bbb", "ccc", "ddd", "eee", "fff", "ggg", "hhh",
		"iii", "jjj", "kkk", "lll", "mmm", "nnn",
	}
	out := make([]slashcomplete.Suggestion, len(names))
	for i, n := range names {
		out[i] = slashcomplete.Suggestion{Name: n, Description: "item " + n}
	}
	return out
}

// TestTUI063_ScrollWindowAdvances verifies that navigating Down() 9 times with 14
// suggestions keeps the selected index within the visible window [windowStart, windowStart+8).
// This is the primary regression test for #663.
func TestTUI063_ScrollWindowAdvances(t *testing.T) {
	m := slashcomplete.New(make14Suggestions())
	m = m.Open()

	for i := 0; i < 9; i++ {
		m = m.Down()
	}

	// After 9 Down() calls, selected should be at index 9.
	s, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false after 9 Down() calls")
	}
	want := "jjj" // 10th item (index 9)
	if s.Name != want {
		t.Errorf("After 9 Down(), selected = %q, want %q", s.Name, want)
	}

	// The scroll window must have advanced so selected (9) is within [start, start+8).
	windowStart := m.ScrollOffset()
	if windowStart > 9 || windowStart+8 <= 9 {
		t.Errorf("ScrollOffset()=%d does not contain selected=9; window=[%d,%d)",
			windowStart, windowStart, windowStart+8)
	}
}

// TestTUI063_ViewContains9thItemAfterNav verifies that View() renders the 9th
// suggestion's name after navigating to it (the regression: previously item 9
// was not rendered at all because only filtered[0:8] was shown).
func TestTUI063_ViewContains9thItemAfterNav(t *testing.T) {
	m := slashcomplete.New(make14Suggestions())
	m = m.Open()

	for i := 0; i < 9; i++ {
		m = m.Down()
	}

	view := m.View(80)
	// "jjj" is the 10th item (index 9); it should appear in the rendered view.
	if !strings.Contains(view, "jjj") {
		t.Errorf("View() does not contain 'jjj' after navigating to index 9.\nView:\n%s", view)
	}
}

// TestTUI063_SetQueryResetsScrollOffset verifies that calling SetQuery() resets
// the scroll offset back to 0.
func TestTUI063_SetQueryResetsScrollOffset(t *testing.T) {
	m := slashcomplete.New(make14Suggestions())
	m = m.Open()

	// Navigate deep to advance the scroll window.
	for i := 0; i < 10; i++ {
		m = m.Down()
	}
	if m.ScrollOffset() == 0 {
		// scroll might not have moved yet if window didn't need to scroll; still valid
		// but let's make sure we actually did move it past 8 before asserting reset.
	}

	// SetQuery resets offset.
	m = m.SetQuery("")
	if m.ScrollOffset() != 0 {
		t.Errorf("SetQuery reset: ScrollOffset()=%d, want 0", m.ScrollOffset())
	}
}

// TestTUI063_ScrollUpKeepsSelectedVisible verifies that navigating Up() from a
// scrolled position brings the selected item back into view.
func TestTUI063_ScrollUpKeepsSelectedVisible(t *testing.T) {
	m := slashcomplete.New(make14Suggestions())
	m = m.Open()

	// Go down 10 times to scroll the window.
	for i := 0; i < 10; i++ {
		m = m.Down()
	}

	// Now go back up 3 times — window must follow.
	for i := 0; i < 3; i++ {
		m = m.Up()
	}

	// selected = 7
	s, ok := m.Selected()
	if !ok {
		t.Fatal("Selected() returned ok=false")
	}
	want := "hhh" // index 7
	if s.Name != want {
		t.Errorf("After 10 Down + 3 Up, selected = %q, want %q", s.Name, want)
	}

	// Window must include selected (7).
	ws := m.ScrollOffset()
	if ws > 7 || ws+8 <= 7 {
		t.Errorf("ScrollOffset=%d does not contain selected=7; window=[%d,%d)",
			ws, ws, ws+8)
	}
}

// TestTUI063_MoreIndicatorsPresent verifies that "▲"/"▼" scroll indicators appear
// when the list extends beyond the visible window in either direction.
func TestTUI063_MoreIndicatorsPresent(t *testing.T) {
	m := slashcomplete.New(make14Suggestions())
	m = m.Open()

	// At index 0 with 14 items: only "below" indicator should appear.
	view := m.View(80)
	if !strings.Contains(view, "▼") {
		t.Errorf("expected ▼ indicator when items extend below; view:\n%s", view)
	}

	// Navigate down 10: both above and below should be visible.
	for i := 0; i < 10; i++ {
		m = m.Down()
	}
	view = m.View(80)
	if !strings.Contains(view, "▲") {
		t.Errorf("expected ▲ indicator when items extend above; view:\n%s", view)
	}
	if !strings.Contains(view, "▼") {
		t.Errorf("expected ▼ indicator when items extend below; view:\n%s", view)
	}
}
