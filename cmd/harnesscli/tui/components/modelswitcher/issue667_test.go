package modelswitcher_test

// Tests for issue #667:
// (a) Fixed count-column offset: selected vs unselected rows must have the '(' of
//     the count at the SAME rune offset (no column jump as cursor moves).
// (b) Star re-anchor: after ToggleStar causes re-sort, Highlighted().ID must
//     remain the same as the model that was starred.
// (c) '/' swallow: HandleSearchKey must not append '/' to the search query.

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// countColOffset returns the rune offset of the first '(' character in a
// stripped (no-ANSI) line, or -1 if not found. This is where the count column starts.
func countColOffset(line string) int {
	clean := stripANSITest(line)
	runes := []rune(clean)
	for i, r := range runes {
		if r == '(' {
			return i
		}
	}
	return -1
}

// ─── (a) Count column fixed-offset tests ─────────────────────────────────────

// TestIssue667_ProviderList_CountColumnFixedOffset asserts that for the provider
// list (level 0), the '(' of the count sits at the SAME rune offset regardless
// of whether the row is selected or unselected.
func TestIssue667_ProviderList_CountColumnFixedOffset(t *testing.T) {
	// Use a model where the current model is NOT in the first provider so that
	// we can compare selected vs unselected rows within the same view.
	// "gpt-4.1" is OpenAI — at level 0 the cursor starts at OpenAI's position.
	// Open puts providerCursor at the current model's provider.
	m := modelswitcher.New("gpt-4.1").Open()

	// View the level-0 provider list.
	v := m.View(80)
	lines := strings.Split(v, "\n")

	// Collect offsets for lines that contain a count "(N)".
	var offsets []int
	var providerLines []string
	for _, line := range lines {
		clean := stripANSITest(line)
		// Only consider lines that look like provider rows (have a '(' somewhere
		// and are not the title, footer, or scroll indicator lines).
		if strings.Contains(clean, "(") && !strings.Contains(clean, "Switch Model") &&
			!strings.Contains(clean, "navigate") && !strings.Contains(clean, "more") {
			off := countColOffset(line)
			if off >= 0 {
				offsets = append(offsets, off)
				providerLines = append(providerLines, clean)
			}
		}
	}

	if len(offsets) < 2 {
		t.Fatalf("expected at least 2 provider rows with count column, got %d\nView:\n%s", len(offsets), v)
	}

	// All offsets must be equal.
	first := offsets[0]
	for i, off := range offsets {
		if off != first {
			t.Errorf("count-column offset mismatch: row 0 at %d, row %d at %d\nrow 0: %q\nrow %d: %q\nView:\n%s",
				first, i, off, providerLines[0], i, providerLines[i], v)
		}
	}
}

// TestIssue667_ProviderList_CurrentMarkerDoesNotShiftCountColumn asserts that
// a provider row marked "← current" does not shift the count column position.
func TestIssue667_ProviderList_CurrentMarkerDoesNotShiftCountColumn(t *testing.T) {
	// Open with gpt-4.1 as current; OpenAI provider shows "← current".
	m := modelswitcher.New("gpt-4.1").Open()
	v := m.View(80)
	lines := strings.Split(v, "\n")

	var offsets []int
	for _, line := range lines {
		clean := stripANSITest(line)
		if strings.Contains(clean, "(") && !strings.Contains(clean, "Switch Model") &&
			!strings.Contains(clean, "navigate") && !strings.Contains(clean, "more") {
			off := countColOffset(line)
			if off >= 0 {
				offsets = append(offsets, off)
			}
		}
	}

	if len(offsets) < 2 {
		t.Fatalf("need at least 2 provider count rows, got %d\nView:\n%s", len(offsets), v)
	}

	first := offsets[0]
	for i, off := range offsets {
		if off != first {
			t.Errorf("← current marker shifted count column: offset[0]=%d, offset[%d]=%d\nView:\n%s",
				first, i, off, v)
		}
	}
}

// TestIssue667_ProviderList_SelectedRowCountAligns verifies that the selected
// provider row places the count "(N)" at the SAME rune offset as unselected rows.
// This is the core column-jump regression: before the fix, the selected row
// put the count right after the label text, while unselected rows right-aligned it.
func TestIssue667_ProviderList_SelectedRowCountAligns(t *testing.T) {
	// Open with gpt-4.1 current — OpenAI will be selected (has ">" prefix).
	// Navigate so that one row is selected (highlighted) and others are not.
	m := modelswitcher.New("gpt-4.1").Open()
	v := m.View(80)
	lines := strings.Split(v, "\n")

	var selectedOffset, unselectedOffset int
	selectedOffset = -1
	unselectedOffset = -1

	for _, line := range lines {
		clean := stripANSITest(line)
		// Skip non-provider rows.
		if strings.Contains(clean, "Switch Model") || strings.Contains(clean, "navigate") ||
			strings.Contains(clean, "more") || !strings.Contains(clean, "(") {
			continue
		}
		off := countColOffset(line)
		if off < 0 {
			continue
		}
		if strings.Contains(clean, "> ") {
			// Selected row.
			if selectedOffset < 0 {
				selectedOffset = off
			}
		} else {
			// Unselected row.
			if unselectedOffset < 0 {
				unselectedOffset = off
			}
		}
	}

	if selectedOffset < 0 {
		t.Fatalf("no selected provider row found with count column\nView:\n%s", v)
	}
	if unselectedOffset < 0 {
		t.Fatalf("no unselected provider row found with count column\nView:\n%s", v)
	}

	if selectedOffset != unselectedOffset {
		t.Errorf("count-column offset: selected row at %d, unselected row at %d (should be equal — column jump bug)\nView:\n%s",
			selectedOffset, unselectedOffset, v)
	}
}

// ─── (b) Star re-anchor tests ─────────────────────────────────────────────────

// highlighted returns the currently highlighted (Selected) entry from the visible list.
// It's a test helper analogous to a "Highlighted()" getter.
func highlighted(m modelswitcher.Model) modelswitcher.ModelEntry {
	entry, _ := m.Accept()
	return entry
}

// TestIssue667_ToggleStar_ReanchorsToSameModelID verifies that after starring a
// non-top model (which triggers a re-sort that moves it to position 0),
// the cursor still points at the model that was just starred.
func TestIssue667_ToggleStar_ReanchorsToSameModelID(t *testing.T) {
	// Start with gpt-4.1. Navigate to gpt-4.1-mini (index 1 in unstarred full list).
	m := modelswitcher.New("gpt-4.1").Open()
	m = m.SelectDown() // now pointing at gpt-4.1-mini (index 1)

	wantID := highlighted(m).ID
	if wantID != "gpt-4.1-mini" {
		t.Fatalf("setup: expected gpt-4.1-mini at cursor, got %q", wantID)
	}

	// Toggle star. This should move gpt-4.1-mini to index 0 (starred models float up).
	// After the re-sort the cursor must STILL point at gpt-4.1-mini.
	m2 := m.ToggleStar()
	gotID := highlighted(m2).ID
	if gotID != wantID {
		t.Errorf("after ToggleStar, highlighted ID = %q, want %q (cursor should re-anchor to starred model)",
			gotID, wantID)
	}
}

// TestIssue667_ToggleStar_UnstarReanchors verifies re-anchor when un-starring a
// top-positioned starred model (which will move it down after unstar).
func TestIssue667_ToggleStar_UnstarReanchors(t *testing.T) {
	// Star claude-sonnet-4-6 and gpt-4.1-mini so they appear first.
	m := modelswitcher.New("gpt-4.1").Open().WithStarred([]string{"claude-sonnet-4-6", "gpt-4.1-mini"})

	// The starred models come first. Navigate to the second starred model.
	m = m.SelectDown()
	wantID := highlighted(m).ID // should be the second starred model

	// Unstar it. It will drop out of the starred section to its natural position.
	m2 := m.ToggleStar()
	gotID := highlighted(m2).ID
	if gotID != wantID {
		t.Errorf("after ToggleStar (unstar), highlighted ID = %q, want %q",
			gotID, wantID)
	}
}

// ─── (c) '/' swallow tests ───────────────────────────────────────────────────

// TestIssue667_HandleSearchKey_SlashNotAppended verifies that sending '/' via
// HandleSearchKey does NOT append '/' to the search query.
func TestIssue667_HandleSearchKey_SlashNotAppended(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	if m.SearchQuery() != "" {
		t.Fatal("search should start empty")
	}

	m2 := m.HandleSearchKey("/")
	if strings.Contains(m2.SearchQuery(), "/") {
		t.Errorf("HandleSearchKey('/') should NOT append '/' to search query, got %q", m2.SearchQuery())
	}
}

// TestIssue667_HandleSearchKey_NormalCharsAppended verifies that normal chars
// DO get appended via HandleSearchKey.
func TestIssue667_HandleSearchKey_NormalCharsAppended(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	m2 := m.HandleSearchKey("c")
	if m2.SearchQuery() != "c" {
		t.Errorf("HandleSearchKey('c') should append 'c', got %q", m2.SearchQuery())
	}

	m3 := m2.HandleSearchKey("l")
	if m3.SearchQuery() != "cl" {
		t.Errorf("HandleSearchKey('l') should append 'l', got %q", m3.SearchQuery())
	}
}

// TestIssue667_HandleSearchKey_BackspaceRemovesLast verifies backspace behaviour.
func TestIssue667_HandleSearchKey_BackspaceRemovesLast(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	m = m.HandleSearchKey("c")
	m = m.HandleSearchKey("l")
	if m.SearchQuery() != "cl" {
		t.Fatalf("setup: expected 'cl', got %q", m.SearchQuery())
	}

	m2 := m.HandleSearchKey("backspace")
	if m2.SearchQuery() != "c" {
		t.Errorf("HandleSearchKey('backspace') should remove last char, got %q", m2.SearchQuery())
	}
}

// TestIssue667_HandleSearchKey_SlashAfterTypingNotAppended verifies '/' is
// still ignored even when the search query already has characters.
func TestIssue667_HandleSearchKey_SlashAfterTypingNotAppended(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	m = m.HandleSearchKey("g")
	m = m.HandleSearchKey("p")
	m = m.HandleSearchKey("t")

	m2 := m.HandleSearchKey("/")
	if strings.Contains(m2.SearchQuery(), "/") {
		t.Errorf("HandleSearchKey('/') should never append '/', got %q", m2.SearchQuery())
	}
	if m2.SearchQuery() != "gpt" {
		t.Errorf("after '/' key, query should remain 'gpt', got %q", m2.SearchQuery())
	}
}
