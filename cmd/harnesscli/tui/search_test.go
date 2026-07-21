package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/transcriptexport"
)

// ─── searchTranscript unit tests ────────────────────────────────────────────

// TestSearch_BT001_BasicMatchFound verifies that when the transcript contains
// "hello world" and the query is "hello", the result includes that entry.
// BT-001: When /search hello is issued and transcript contains "hello world", results include that entry.
func TestSearch_BT001_BasicMatchFound(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "hello world", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "hello")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result, got 0")
	}
	if results[0].EntryIndex != 0 {
		t.Errorf("EntryIndex = %d, want 0", results[0].EntryIndex)
	}
	if results[0].Role != "user" {
		t.Errorf("Role = %q, want %q", results[0].Role, "user")
	}
	if !strings.Contains(results[0].Snippet, "hello") {
		t.Errorf("Snippet %q does not contain 'hello'", results[0].Snippet)
	}
}

// TestSearch_BT002_EmptyQueryReturnsNil verifies that an empty query produces no results.
// BT-002: /search with no query is handled by the command handler (covered in model integration).
// This unit test verifies the search function itself returns nil for empty queries.
func TestSearch_BT002_EmptyQueryReturnsNil(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "hello world", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "")
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

// TestSearch_BT003_NoMatchReturnsEmpty verifies that a non-matching query returns empty results.
// BT-003: When /search xyz finds no matches, overlay shows "No matches found".
func TestSearch_BT003_NoMatchReturnsEmpty(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "hello world", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "xyz_no_match_xyz")
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(results))
	}
}

// TestSearch_BT004_CaseInsensitive verifies that search is case-insensitive.
// BT-004: Search is case-insensitive: /search HELLO matches "hello".
func TestSearch_BT004_CaseInsensitive(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "assistant", Content: "hello world", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "HELLO")
	if len(results) == 0 {
		t.Fatal("expected 1 result for case-insensitive search, got 0")
	}
	if results[0].Role != "assistant" {
		t.Errorf("Role = %q, want %q", results[0].Role, "assistant")
	}
}

// TestSearch_BT005_MultipleMatchesChronological verifies that all matching entries
// are returned in chronological order (by index).
// BT-005: When multiple transcript entries match, all are returned in chronological order.
func TestSearch_BT005_MultipleMatchesChronological(t *testing.T) {
	now := time.Now()
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "deploy to prod", Timestamp: now},
		{Role: "assistant", Content: "I will deploy now", Timestamp: now.Add(time.Second)},
		{Role: "user", Content: "unrelated message", Timestamp: now.Add(2 * time.Second)},
		{Role: "assistant", Content: "deploy completed", Timestamp: now.Add(3 * time.Second)},
	}
	results := tui.SearchTranscript(entries, "deploy")
	if len(results) != 3 {
		t.Fatalf("expected 3 results for 'deploy', got %d", len(results))
	}
	// Verify chronological order by EntryIndex.
	if results[0].EntryIndex >= results[1].EntryIndex {
		t.Errorf("results not in chronological order: [0].EntryIndex=%d, [1].EntryIndex=%d",
			results[0].EntryIndex, results[1].EntryIndex)
	}
	if results[1].EntryIndex >= results[2].EntryIndex {
		t.Errorf("results not in chronological order: [1].EntryIndex=%d, [2].EntryIndex=%d",
			results[1].EntryIndex, results[2].EntryIndex)
	}
}

// TestSearch_BT008_ResultIncludesRoleAndSnippet verifies that each result contains
// the role and a snippet of context around the match.
// BT-008: Search results include the role (user/assistant) and a context snippet around the match.
func TestSearch_BT008_ResultIncludesRoleAndSnippet(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "the quick brown fox jumps over the lazy dog", Timestamp: time.Now()},
		{Role: "assistant", Content: "indeed the fox is quite quick", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "fox")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Role == "" {
			t.Errorf("result[%d].Role is empty", i)
		}
		if r.Snippet == "" {
			t.Errorf("result[%d].Snippet is empty", i)
		}
		if !strings.Contains(strings.ToLower(r.Snippet), "fox") {
			t.Errorf("result[%d].Snippet %q does not contain 'fox'", i, r.Snippet)
		}
		// Snippet should be at most 80 chars around the match.
		if len(r.Snippet) > 160 {
			t.Errorf("result[%d].Snippet too long: %d chars (want <= 160)", i, len(r.Snippet))
		}
	}
}

// TestSearch_SnippetAroundMatch verifies the snippet is correctly windowed around the match.
func TestSearch_SnippetAroundMatch(t *testing.T) {
	// Long content where match is in the middle.
	content := "start of message with lots of padding before the match keyword appears and more text after"
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "match keyword")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	if !strings.Contains(strings.ToLower(results[0].Snippet), "match keyword") {
		t.Errorf("snippet %q does not contain the match", results[0].Snippet)
	}
}

// TestSearch_MatchStartEndPositions verifies MatchStart and MatchEnd are set.
func TestSearch_MatchStartEndPositions(t *testing.T) {
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: "hello world", Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "world")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	r := results[0]
	if r.MatchStart < 0 {
		t.Errorf("MatchStart = %d, want >= 0", r.MatchStart)
	}
	if r.MatchEnd <= r.MatchStart {
		t.Errorf("MatchEnd (%d) must be > MatchStart (%d)", r.MatchEnd, r.MatchStart)
	}
}

// ─── highlightMatch unit tests ────────────────────────────────────────────────

// TestHighlightMatch_WrapsMatch verifies that highlightMatch returns a string containing
// the original match term. In TTY environments the result will include ANSI markup;
// in non-TTY test environments lipgloss strips ANSI codes, so we verify that
// the match is preserved and the rest of the text is also preserved.
func TestHighlightMatch_WrapsMatch(t *testing.T) {
	result := tui.HighlightMatch("hello world", "world")
	// The result must contain the matched word (either raw or with ANSI codes stripped).
	if !strings.Contains(result, "world") {
		t.Errorf("highlightMatch result %q must still contain 'world'", result)
	}
	// The un-matched prefix must be preserved.
	if !strings.HasPrefix(result, "hello ") {
		t.Errorf("highlightMatch result %q must have 'hello ' prefix", result)
	}
}

// TestHighlightMatch_CaseInsensitiveWrap verifies case-insensitive matching in highlight.
func TestHighlightMatch_CaseInsensitiveWrap(t *testing.T) {
	result := tui.HighlightMatch("Hello World", "hello")
	// Result must still contain the original casing of the match.
	if !strings.Contains(result, "Hello") {
		t.Errorf("highlightMatch result %q must preserve original casing", result)
	}
}

// TestHighlightMatch_NoMatchReturnsOriginal verifies no-op on non-matching query.
func TestHighlightMatch_NoMatchReturnsOriginal(t *testing.T) {
	result := tui.HighlightMatch("hello world", "xyz")
	if result != "hello world" {
		t.Errorf("highlightMatch with no match: got %q, want %q", result, "hello world")
	}
}

// ─── /search command integration tests ───────────────────────────────────────

// TestSearch_BT002_EmptyQueryStatusMessage verifies that /search with no query
// sets a status message about usage, not an overlay.
// BT-002: When /search is issued with no query, a status message says "Usage: /search <query>".
func TestSearch_BT002_EmptyQueryStatusMessage(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/search")
	if m.StatusMsg() == "" {
		t.Fatal("StatusMsg must be set when /search is issued with no query")
	}
	if !strings.Contains(m.StatusMsg(), "Usage") || !strings.Contains(m.StatusMsg(), "/search") {
		t.Errorf("StatusMsg = %q, want it to contain 'Usage' and '/search'", m.StatusMsg())
	}
	// Overlay should NOT be open.
	if m.OverlayActive() {
		t.Error("OverlayActive must be false when /search has no query")
	}
}

// TestSearch_BT003_NoMatchOverlayMessage verifies that /search with a non-matching query
// opens an overlay showing "No matches found".
// BT-003: When /search xyz finds no matches, overlay shows "No matches found".
func TestSearch_BT003_NoMatchOverlayMessage(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/search xyz_no_match_xyz_abc")
	// Overlay should be active showing "No matches found".
	if !m.OverlayActive() {
		t.Fatal("OverlayActive must be true after /search with no matches")
	}
	v := m.View()
	if !strings.Contains(v, "No matches found") {
		t.Errorf("View must contain 'No matches found'; got:\n%s", v)
	}
}

// TestSearch_BT001_MatchOpensOverlayWithResult verifies /search opens the search overlay
// when a matching transcript entry exists.
// BT-001: When /search hello is issued and transcript contains "hello world", results include that entry.
func TestSearch_BT001_MatchOpensOverlayWithResult(t *testing.T) {
	m := initModel(t, 80, 24)
	// Inject a transcript entry containing "hello world".
	m = injectTranscriptEntry(m, "user", "hello world")
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("OverlayActive must be true after /search with matching results")
	}
	if m.ActiveOverlay() != "search" {
		t.Errorf("ActiveOverlay = %q, want %q", m.ActiveOverlay(), "search")
	}
	v := m.View()
	if !strings.Contains(v, "hello") {
		t.Errorf("View must contain the matched text 'hello'; got:\n%s", v)
	}
}

// TestSearch_BT007_EscapeClosesSearchOverlay verifies that Escape closes the search overlay.
// BT-007: When the search overlay is open, Escape closes it.
func TestSearch_BT007_EscapeClosesSearchOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "hello world")
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("precondition: search overlay must be open")
	}
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("OverlayActive must be false after Escape from search overlay")
	}
}

// TestSearch_SearchCommandRegistered verifies that /search is registered in the command registry.
func TestSearch_SearchCommandRegistered(t *testing.T) {
	r := tui.NewCommandRegistry()
	if !r.IsRegistered("search") {
		t.Error("'search' command must be registered in the command registry")
	}
}

// TestSearch_HistoryCommandRegistered verifies that /history is registered in the command registry.
func TestSearch_HistoryCommandRegistered(t *testing.T) {
	r := tui.NewCommandRegistry()
	if !r.IsRegistered("history") {
		t.Error("'history' command must be registered in the command registry")
	}
}

// TestSearch_BT006_HistorySearchMatchesSessionLastMsg verifies that /history searches
// session metadata's LastMsg field. This tests a no-op result since there are no
// stored sessions in unit tests, but verifies the overlay is opened.
// BT-006: When /history deploy matches a session's LastMsg containing "deploy", that session appears.
func TestSearch_BT006_HistorySearchOpensOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	// Even with no sessions, the overlay should open (showing "No matches found").
	m = sendSlashCommand(m, "/history deploy")
	if !m.OverlayActive() {
		t.Fatal("OverlayActive must be true after /history command")
	}
	v := m.View()
	if !strings.Contains(v, "No matches found") {
		t.Errorf("View must contain 'No matches found' when no session matches; got:\n%s", v)
	}
}

// TestSearch_HistoryNoQueryShowsUsage verifies /history with no query shows usage hint.
func TestSearch_HistoryNoQueryShowsUsage(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/history")
	if m.StatusMsg() == "" {
		t.Fatal("StatusMsg must be set when /history is issued with no query")
	}
	if !strings.Contains(m.StatusMsg(), "Usage") {
		t.Errorf("StatusMsg = %q, want it to contain 'Usage'", m.StatusMsg())
	}
}

// TestSearch_CaseInsensitiveInOverlay verifies case-insensitive matching reflected in overlay.
// BT-004: /search HELLO matches "hello".
func TestSearch_BT004_CaseInsensitiveInOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "assistant", "hello world from the assistant")
	m = sendSlashCommand(m, "/search HELLO")
	if !m.OverlayActive() {
		t.Fatal("OverlayActive must be true for case-insensitive match")
	}
	v := m.View()
	if !strings.Contains(strings.ToLower(v), "hello") {
		t.Errorf("View must contain matched text 'hello' (case-insensitive); got:\n%s", v)
	}
}

// TestSearch_SearchOverlayShowsRoleAndSnippet verifies the overlay contains role and snippet info.
// BT-008: Search results include the role and a context snippet around the match.
func TestSearch_BT008_OverlayShowsRoleAndSnippet(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "the fox ran across the field")
	m = sendSlashCommand(m, "/search fox")
	if !m.OverlayActive() {
		t.Fatal("OverlayActive must be true for matching search")
	}
	v := m.View()
	// The overlay should show the role.
	if !strings.Contains(v, "user") {
		t.Errorf("View must contain role 'user'; got:\n%s", v)
	}
	// The overlay should show a snippet containing the match.
	if !strings.Contains(v, "fox") {
		t.Errorf("View must contain matched term 'fox' in snippet; got:\n%s", v)
	}
}

// TestSearch_RegressionClearStillWorks verifies /clear still works after adding /search.
func TestSearch_RegressionClearStillWorks(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "some content")
	m = sendSlashCommand(m, "/clear")
	if m.OverlayActive() {
		t.Error("/clear must not open any overlay")
	}
	if !strings.Contains(m.StatusMsg(), "cleared") {
		t.Errorf("StatusMsg = %q, want it to contain 'cleared'", m.StatusMsg())
	}
}

// TestSearch_RegressionSearchRegisteredAlongsideBuiltins verifies that adding /search
// and /history does not remove existing commands from the registry.
func TestSearch_RegressionSearchRegisteredAlongsideBuiltins(t *testing.T) {
	r := tui.NewCommandRegistry()
	required := []string{"clear", "context", "export", "help", "keys", "model", "quit", "stats", "subagents", "tasks", "profiles", "search", "history"}
	for _, name := range required {
		if !r.IsRegistered(name) {
			t.Errorf("command %q must be registered", name)
		}
	}
}

// TestSearch_RegressionSessionsSessionsNotBroken verifies /sessions is not broken (it's not in the registry, fine).
// This verifies existing commands produce expected behavior.
func TestSearch_RegressionExistingCommandsUnaffected(t *testing.T) {
	m := initModel(t, 80, 24)

	// /help should still work.
	m2 := sendSlashCommand(m, "/help")
	if !m2.OverlayActive() || m2.ActiveOverlay() != "help" {
		t.Error("/help must still open help overlay after search feature added")
	}

	// /stats should still work.
	m3 := sendSlashCommand(m, "/stats")
	if !m3.OverlayActive() || m3.ActiveOverlay() != "stats" {
		t.Error("/stats must still open stats overlay after search feature added")
	}
}

// TestSearch_SearchResultsNavigable verifies the search overlay can be navigated with Up/Down.
func TestSearch_SearchResultsNavigable(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "hello from user")
	m = injectTranscriptEntry(m, "assistant", "hello from assistant")
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("precondition: search overlay must be open")
	}
	// Press Down — should not panic.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(tui.Model)
	// Still active after navigation.
	if !m.OverlayActive() {
		t.Error("OverlayActive must remain true after Down key in search overlay")
	}
}

// injectTranscriptEntry is a test helper that adds a transcript entry to the model
// by simulating the SSE message flow. It uses the exposed TranscriptInjector message
// for testing purposes.
func injectTranscriptEntry(m tui.Model, role, content string) tui.Model {
	m2, _ := m.Update(tui.TranscriptEntryMsg{
		Role:    role,
		Content: content,
	})
	return m2.(tui.Model)
}

// ─── SessionStore unit tests (search-relevant behaviour) ─────────────────────

// TestSessionStore_NewAndList verifies that NewSessionStore stores and returns sessions.
func TestSessionStore_NewAndList(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)
	now := time.Now()
	store.Add(tui.StoredSessionEntry{ID: "s1", LastMsg: "deploy to production", StartedAt: now})
	store.Add(tui.StoredSessionEntry{ID: "s2", LastMsg: "fix the bug", StartedAt: now.Add(-time.Second)})
	got := store.List()
	if len(got) != 2 {
		t.Fatalf("SessionStore.List() = %d entries, want 2", len(got))
	}
	// List returns entries sorted most-recent first: s1 has the later StartedAt.
	if got[0].ID != "s1" || got[1].ID != "s2" {
		t.Errorf("List() returned wrong sessions: %+v", got)
	}
}

// TestSessionStore_NilListIsNoop verifies that List on a nil *SessionStore returns nil.
func TestSessionStore_NilListIsNoop(t *testing.T) {
	var store *tui.SessionStore
	got := store.List()
	if got != nil {
		t.Errorf("nil SessionStore.List() = %v, want nil", got)
	}
}

// TestSessionStore_MutationIsolation verifies that mutating the slice returned by
// List() does not affect the stored sessions (caller cannot corrupt internal state).
func TestSessionStore_MutationIsolation(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)
	store.Add(tui.StoredSessionEntry{ID: "original", LastMsg: "content", StartedAt: time.Now()})
	got := store.List()
	// Mutate the returned slice.
	got[0].ID = "tampered"
	// A second call must return the original data.
	got2 := store.List()
	if got2[0].ID != "original" {
		t.Errorf("List() returned non-copy: internal state was mutated, got ID %q, want %q", got2[0].ID, "original")
	}
}

// ─── SessionStore.List() defensive copy ──────────────────────────────────────

// TestSessionStore_ListReturnsCopy verifies that mutating the slice returned by
// List() does not affect the stored sessions (caller cannot corrupt internal state).
func TestSessionStore_ListReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)
	now := time.Now()
	store.Add(tui.StoredSessionEntry{ID: "s1", LastMsg: "hello world", StartedAt: now})
	store.Add(tui.StoredSessionEntry{ID: "s2", LastMsg: "goodbye world", StartedAt: now.Add(-time.Second)})
	got := store.List()
	// Mutate the returned slice.
	got[0].ID = "tampered"
	// A second call to List() must return unmodified data.
	got2 := store.List()
	if got2[0].ID != "s1" {
		t.Errorf("List() returned non-copy: internal state was mutated, got ID %q, want %q", got2[0].ID, "s1")
	}
}

// ─── Unicode-safe search ──────────────────────────────────────────────────────

// TestSearch_Unicode_TurkishCapitalI verifies that SearchTranscript handles Turkish
// capital İ correctly. strings.ToLower("İ") produces a 2-byte sequence in Turkish locale,
// so byte offsets from the lowercased string must not be used to slice the original.
func TestSearch_Unicode_TurkishCapitalI(t *testing.T) {
	// "Hİllo" — İ is U+0130 LATIN CAPITAL LETTER I WITH DOT ABOVE (2 bytes in UTF-8)
	// strings.ToLower("İ") -> "i\u0307" (2 bytes) in some locales, but Go's
	// strings.ToLower maps İ (U+0130) to "i\u0307" (3 bytes in UTF-8: 'i' + combining dot).
	// The key requirement: search must find the match and not panic or garble the snippet.
	content := "Hİllo world"
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	// Query using ASCII "illo" which is definitely in the original after the İ.
	results := tui.SearchTranscript(entries, "world")
	if len(results) == 0 {
		t.Fatal("expected 1 result for Unicode content with ASCII query 'world'")
	}
	// Snippet must be valid UTF-8 and contain "world".
	if !strings.Contains(results[0].Snippet, "world") {
		t.Errorf("Snippet %q must contain 'world'", results[0].Snippet)
	}
	// Snippet must be valid UTF-8 (not garbled).
	if !isValidUTF8(results[0].Snippet) {
		t.Errorf("Snippet %q is not valid UTF-8", results[0].Snippet)
	}
}

// TestSearch_Unicode_Emoji verifies that SearchTranscript correctly handles
// emoji in content. An emoji like 🚀 is 4 bytes; byte-offset slicing can panic.
func TestSearch_Unicode_Emoji(t *testing.T) {
	content := "deploy 🚀 complete"
	entries := []transcriptexport.TranscriptEntry{
		{Role: "assistant", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "complete")
	if len(results) == 0 {
		t.Fatal("expected 1 result for emoji content with query 'complete'")
	}
	if !strings.Contains(results[0].Snippet, "complete") {
		t.Errorf("Snippet %q must contain 'complete'", results[0].Snippet)
	}
	if !isValidUTF8(results[0].Snippet) {
		t.Errorf("Snippet %q is not valid UTF-8", results[0].Snippet)
	}
}

// TestSearch_Unicode_Chinese verifies that SearchTranscript correctly handles
// multi-byte CJK characters. Each CJK char is 3 bytes; mixed indexing panics.
func TestSearch_Unicode_Chinese(t *testing.T) {
	content := "搜索功能测试"
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "功能")
	if len(results) == 0 {
		t.Fatal("expected 1 result for Chinese content with Chinese query")
	}
	if !strings.Contains(results[0].Snippet, "功能") {
		t.Errorf("Snippet %q must contain '功能'", results[0].Snippet)
	}
	if !isValidUTF8(results[0].Snippet) {
		t.Errorf("Snippet %q is not valid UTF-8", results[0].Snippet)
	}
}

// TestSearch_Unicode_EmojiMatchStart verifies MatchStart/MatchEnd are valid rune
// offsets in the snippet when emoji appear before the match.
func TestSearch_Unicode_EmojiMatchStart(t *testing.T) {
	// Emoji before the match: each emoji is 4 bytes.
	content := "🎉🎉🎉 hello world"
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "hello")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	r := results[0]
	snippet := []rune(r.Snippet)
	// MatchStart and MatchEnd must be valid indices into the rune slice of Snippet.
	if r.MatchStart < 0 || r.MatchStart >= len(snippet) {
		t.Errorf("MatchStart=%d is out of rune bounds for snippet %q (rune len=%d)", r.MatchStart, r.Snippet, len(snippet))
	}
	if r.MatchEnd <= r.MatchStart || r.MatchEnd > len(snippet) {
		t.Errorf("MatchEnd=%d is out of rune bounds for snippet %q (rune len=%d), MatchStart=%d", r.MatchEnd, r.Snippet, len(snippet), r.MatchStart)
	}
	// The runes from MatchStart to MatchEnd must spell "hello" (case-insensitive).
	matchedRunes := string(snippet[r.MatchStart:r.MatchEnd])
	if !strings.EqualFold(matchedRunes, "hello") {
		t.Errorf("runes[MatchStart:MatchEnd] = %q, want 'hello'", matchedRunes)
	}
}

// TestHighlightMatch_Unicode_Chinese verifies HighlightMatch handles CJK correctly.
func TestHighlightMatch_Unicode_Chinese(t *testing.T) {
	text := "搜索功能测试完成"
	result := tui.HighlightMatch(text, "功能")
	// Must still contain both surrounding characters, not garbled.
	if !strings.Contains(result, "搜索") {
		t.Errorf("HighlightMatch result %q lost prefix '搜索'", result)
	}
	if !strings.Contains(result, "测试") {
		t.Errorf("HighlightMatch result %q lost suffix '测试'", result)
	}
}

// TestHighlightMatch_Unicode_Emoji verifies HighlightMatch handles emoji correctly.
func TestHighlightMatch_Unicode_Emoji(t *testing.T) {
	text := "launch 🚀 now"
	result := tui.HighlightMatch(text, "now")
	if !strings.Contains(result, "launch") {
		t.Errorf("HighlightMatch result %q lost prefix 'launch'", result)
	}
	if !strings.Contains(result, "now") {
		t.Errorf("HighlightMatch result %q lost 'now'", result)
	}
}

// TestExtractSnippet_RuneSafe verifies extractSnippet does not panic or
// produce invalid UTF-8 when content contains multi-byte characters at boundaries.
func TestExtractSnippet_RuneSafe(t *testing.T) {
	// Content where byte position 40 would fall inside a multi-byte rune.
	// "A" * 38 + "🚀" (4 bytes) + "B" * 40 — match on "BBB" starting at rune 39.
	content := strings.Repeat("A", 38) + "🚀" + strings.Repeat("B", 40)
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "BBB")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	if !isValidUTF8(results[0].Snippet) {
		t.Errorf("Snippet is not valid UTF-8: %q", results[0].Snippet)
	}
}

// TestExtractSnippet_EllipsisWhenTruncated verifies that extractSnippet adds
// ellipsis markers when the snippet is truncated (not at content boundaries).
func TestExtractSnippet_EllipsisWhenTruncated(t *testing.T) {
	// Match in the middle of long content — both sides should be truncated.
	prefix := strings.Repeat("X", 60)
	suffix := strings.Repeat("Y", 60)
	content := prefix + "MATCH" + suffix
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "MATCH")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	snippet := results[0].Snippet
	// Snippet must contain the ellipsis on both sides since content is truncated.
	if !strings.Contains(snippet, "…") {
		t.Errorf("snippet %q must contain ellipsis '…' when truncated on both sides", snippet)
	}
}

// TestExtractSnippet_NoEllipsisAtStart verifies no leading ellipsis when match is at start.
func TestExtractSnippet_NoEllipsisAtStart(t *testing.T) {
	// Match at beginning — no leading truncation.
	content := "MATCH" + strings.Repeat("Y", 60)
	entries := []transcriptexport.TranscriptEntry{
		{Role: "user", Content: content, Timestamp: time.Now()},
	}
	results := tui.SearchTranscript(entries, "MATCH")
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	snippet := results[0].Snippet
	// Must NOT start with ellipsis.
	if strings.HasPrefix(snippet, "…") {
		t.Errorf("snippet %q must not start with ellipsis when match is at start", snippet)
	}
}

// ─── viewSearchOverlay UX tests ───────────────────────────────────────────────

// TestSearchOverlay_TitleIncludesResultCount verifies the overlay title shows
// "Search: query (N results)" rather than just "Search: query".
func TestSearchOverlay_TitleIncludesResultCount(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "hello world")
	m = injectTranscriptEntry(m, "assistant", "hello from assistant")
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}
	v := m.View()
	// Title must include "(2 results)" or similar count.
	if !strings.Contains(v, "results") {
		t.Errorf("overlay view must contain 'results' count in title; got:\n%s", v)
	}
}

// TestSearchOverlay_EnterLabelSaysJumpToMatch verifies the hint line says
// "Enter jump to match" not "Enter dismiss".
func TestSearchOverlay_EnterLabelSaysJumpToMatch(t *testing.T) {
	m := initModel(t, 80, 24)
	m = injectTranscriptEntry(m, "user", "hello world")
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}
	v := m.View()
	if strings.Contains(v, "Enter dismiss") {
		t.Errorf("overlay must not say 'Enter dismiss'; got:\n%s", v)
	}
	if !strings.Contains(v, "Enter jump") {
		t.Errorf("overlay must say 'Enter jump to match'; got:\n%s", v)
	}
}

// TestSearchOverlay_CapsAt20Results verifies that when there are more than 20 results,
// only 20 are shown in the overlay at a time (scroll window).
func TestSearchOverlay_CapsAt20Results(t *testing.T) {
	m := initModel(t, 120, 60)
	// Inject 25 matching entries.
	for i := 0; i < 25; i++ {
		m = injectTranscriptEntry(m, "user", "hello world message")
	}
	m = sendSlashCommand(m, "/search hello")
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}
	v := m.View()
	// Count occurrences of "[user]" — should be at most 20.
	count := strings.Count(v, "[user]")
	if count > 20 {
		t.Errorf("overlay shows %d results, want at most 20", count)
	}
	// And should indicate more results exist.
	if !strings.Contains(v, "more") {
		t.Errorf("overlay must show 'more' indicator when results > 20; got:\n%s", v)
	}
}

// isValidUTF8 returns true if s is valid UTF-8.
func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			// Check if the original truly has this replacement character or if it's garbled.
			// Actually iterate byte-by-byte check.
			_ = r
		}
	}
	// Use standard library validation.
	return strings.ToValidUTF8(s, "\uFFFD") == s
}
