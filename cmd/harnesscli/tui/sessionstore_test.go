package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ─── BT-001: Session store Add + Get ─────────────────────────────────────────

// TestSS001_AddAndGetByID verifies that a session added to the store can be
// retrieved by its ID.
func TestSS001_AddAndGetByID(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())
	entry := tui.StoredSessionEntry{
		ID:        "abc-123",
		StartedAt: time.Now(),
		Model:     "gpt-4o",
		TurnCount: 2,
		LastMsg:   "hello world",
	}
	store.Add(entry)

	got, ok := store.Get("abc-123")
	if !ok {
		t.Fatal("Get returned false for entry that was just added")
	}
	if got.ID != "abc-123" {
		t.Errorf("Get ID: want %q, got %q", "abc-123", got.ID)
	}
	if got.Model != "gpt-4o" {
		t.Errorf("Get Model: want %q, got %q", "gpt-4o", got.Model)
	}
}

// ─── BT-002: Max 100 sessions, oldest evicted ─────────────────────────────────

// TestSS002_MaxSessionsEvictsOldest verifies that when 101 sessions are added,
// the oldest (first-added) session is evicted, keeping the store at 100 entries.
func TestSS002_MaxSessionsEvictsOldest(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())

	base := time.Now()
	// Add 100 sessions.
	for i := 0; i < 100; i++ {
		store.Add(tui.StoredSessionEntry{
			ID:        string(rune('a'+i%26)) + time.Duration(i).String(),
			StartedAt: base.Add(time.Duration(i) * time.Minute),
			TurnCount: i,
		})
	}

	firstID := "first-session-ever"
	store.Add(tui.StoredSessionEntry{
		ID:        firstID,
		StartedAt: base.Add(-time.Hour), // oldest by time
	})

	// Add one more to trigger eviction.
	store.Add(tui.StoredSessionEntry{
		ID:        "newest-session",
		StartedAt: base.Add(200 * time.Minute),
	})

	if len(store.List()) != 100 {
		t.Errorf("List length after eviction: want 100, got %d", len(store.List()))
	}

	_, ok := store.Get(firstID)
	if ok {
		t.Error("oldest session should have been evicted but is still present")
	}
}

// ─── BT-003: Delete removes session ──────────────────────────────────────────

// TestSS003_DeleteRemovesSession verifies that a deleted session no longer
// appears in List() or Get().
func TestSS003_DeleteRemovesSession(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())
	store.Add(tui.StoredSessionEntry{ID: "to-delete", StartedAt: time.Now()})
	store.Add(tui.StoredSessionEntry{ID: "keep", StartedAt: time.Now()})

	store.Delete("to-delete")

	_, ok := store.Get("to-delete")
	if ok {
		t.Error("Get returned true for deleted session")
	}

	for _, e := range store.List() {
		if e.ID == "to-delete" {
			t.Error("deleted session still present in List()")
		}
	}

	// The surviving session must still be present.
	_, ok = store.Get("keep")
	if !ok {
		t.Error("non-deleted session missing from store")
	}
}

// ─── BT-008: Missing sessions.json initializes empty ─────────────────────────

// TestSS008_MissingFileInitializesEmpty verifies that when sessions.json does
// not exist, Load returns no error and the store starts empty.
func TestSS008_MissingFileInitializesEmpty(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)

	// Load from a directory with no sessions.json.
	if err := store.Load(); err != nil {
		t.Fatalf("Load on missing file returned error: %v", err)
	}

	if len(store.List()) != 0 {
		t.Errorf("Expected empty store, got %d entries", len(store.List()))
	}
}

// ─── Save + Load round-trip ───────────────────────────────────────────────────

// TestSS_SaveLoadRoundtrip verifies that sessions survive a Save()/Load() cycle.
func TestSS_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)

	now := time.Now().Truncate(time.Second)
	store.Add(tui.StoredSessionEntry{
		ID:        "persist-me",
		StartedAt: now,
		Model:     "claude-opus-4-6",
		TurnCount: 5,
		LastMsg:   "the last message",
	})

	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify the file was written.
	_, err := os.Stat(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.json not written: %v", err)
	}

	store2 := tui.NewSessionStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got, ok := store2.Get("persist-me")
	if !ok {
		t.Fatal("session not found after Load")
	}
	if got.Model != "claude-opus-4-6" {
		t.Errorf("Model after load: want %q, got %q", "claude-opus-4-6", got.Model)
	}
	if got.TurnCount != 5 {
		t.Errorf("TurnCount after load: want 5, got %d", got.TurnCount)
	}
	if got.LastMsg != "the last message" {
		t.Errorf("LastMsg after load: want %q, got %q", "the last message", got.LastMsg)
	}
}

// ─── Update modifies an existing entry ───────────────────────────────────────

// TestSS_UpdateModifiesEntry verifies that Update mutates an existing entry.
func TestSS_UpdateModifiesEntry(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())
	store.Add(tui.StoredSessionEntry{ID: "upd", TurnCount: 1})

	store.Update("upd", func(e *tui.StoredSessionEntry) {
		e.TurnCount = 7
		e.LastMsg = "updated"
	})

	got, ok := store.Get("upd")
	if !ok {
		t.Fatal("entry missing after Update")
	}
	if got.TurnCount != 7 {
		t.Errorf("TurnCount after Update: want 7, got %d", got.TurnCount)
	}
	if got.LastMsg != "updated" {
		t.Errorf("LastMsg after Update: want %q, got %q", "updated", got.LastMsg)
	}
}

// ─── Load: corrupt JSON starts fresh ─────────────────────────────────────────

// TestSS_LoadCorruptJSONStartsFresh verifies that Load returns nil (not an
// error) when sessions.json contains invalid JSON, initializing the store empty.
func TestSS_LoadCorruptJSONStartsFresh(t *testing.T) {
	dir := t.TempDir()
	// Write invalid JSON to the sessions file.
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte("not-valid-json{{{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := tui.NewSessionStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load with corrupt JSON must not return error, got: %v", err)
	}
	if len(store.List()) != 0 {
		t.Errorf("after corrupt-JSON load, want 0 entries, got %d", len(store.List()))
	}
}

// ─── Load: non-NotExist error is returned ─────────────────────────────────────

// TestSS_LoadReadError verifies that Load returns an error when the sessions
// file exists but cannot be read (e.g. it is a directory).
func TestSS_LoadReadError(t *testing.T) {
	dir := t.TempDir()
	// Create a directory at the sessions.json path so ReadFile fails with a
	// non-IsNotExist error.
	if err := os.Mkdir(filepath.Join(dir, "sessions.json"), 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	store := tui.NewSessionStore(dir)
	err := store.Load()
	if err == nil {
		t.Fatal("Load must return an error when sessions.json is a directory")
	}
}

// ─── Save: MkdirAll error propagated ─────────────────────────────────────────

// TestSS_SaveMkdirAllError verifies that Save returns an error when it cannot
// create the config directory (e.g. the parent is a file, not a directory).
func TestSS_SaveMkdirAllError(t *testing.T) {
	// Create a file at the path where the directory would be created so that
	// MkdirAll fails.
	parent := t.TempDir()
	dirAsFile := filepath.Join(parent, "blockingfile")
	if err := os.WriteFile(dirAsFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Use a path like <file>/subdir as the store dir so MkdirAll cannot succeed.
	store := tui.NewSessionStore(filepath.Join(dirAsFile, "subdir"))
	store.Add(tui.StoredSessionEntry{ID: "x", StartedAt: time.Now()})
	if err := store.Save(); err == nil {
		t.Fatal("Save must return an error when MkdirAll fails")
	}
}

// ─── Save: WriteFile error propagated ────────────────────────────────────────

// TestSS_SaveWriteFileError verifies that Save returns an error when it cannot
// write the temporary file (e.g. the directory is read-only).
func TestSS_SaveWriteFileError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write to read-only directories")
	}

	dir := t.TempDir()
	// Make the directory read-only so WriteFile fails on the .tmp path.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	store := tui.NewSessionStore(dir)
	store.Add(tui.StoredSessionEntry{ID: "y", StartedAt: time.Now()})
	if err := store.Save(); err == nil {
		t.Fatal("Save must return an error when WriteFile fails on read-only dir")
	}
}

// ─── Title: set + persisted round-trip ────────────────────────────────────────

// TestSS_TitleRoundtrip verifies that a session title survives a Save/Load
// cycle in sessions.json.
func TestSS_TitleRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)
	store.Add(tui.StoredSessionEntry{
		ID:        "titled-session",
		StartedAt: time.Now(),
		Title:     "fix auth bug",
	})

	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	store2 := tui.NewSessionStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	got, ok := store2.Get("titled-session")
	if !ok {
		t.Fatal("session missing after Load")
	}
	if got.Title != "fix auth bug" {
		t.Errorf("Title after round-trip: want %q, got %q", "fix auth bug", got.Title)
	}
}

// ─── Title: backward compatibility with pre-title sessions.json ───────────────

// TestSS_LoadLegacyJSONWithoutTitle verifies that a sessions.json written
// before the Title field existed loads cleanly with an empty Title.
func TestSS_LoadLegacyJSONWithoutTitle(t *testing.T) {
	dir := t.TempDir()
	legacy := `[{"id":"old-1","started_at":"2026-01-02T03:04:05Z","model":"gpt-4o","turn_count":3,"last_msg":"hi"}]`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := tui.NewSessionStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load of legacy file failed: %v", err)
	}
	got, ok := store.Get("old-1")
	if !ok {
		t.Fatal("legacy entry missing after Load")
	}
	if got.Title != "" {
		t.Errorf("legacy entry Title: want empty, got %q", got.Title)
	}
	if got.TurnCount != 3 || got.Model != "gpt-4o" || got.LastMsg != "hi" {
		t.Errorf("legacy fields not preserved: %+v", got)
	}
}

// TestSS_TitleOmittedFromJSONWhenEmpty verifies that an entry with no title
// serializes without the "title" key, keeping sessions.json identical in shape
// to what pre-title versions wrote.
func TestSS_TitleOmittedFromJSONWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	store := tui.NewSessionStore(dir)
	store.Add(tui.StoredSessionEntry{ID: "no-title", StartedAt: time.Now(), TurnCount: 1})
	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), `"title":`) {
		t.Errorf("sessions.json must omit the title key when empty, got: %s", data)
	}
}

// ─── Title: SetTitle setter ───────────────────────────────────────────────────

// TestSS_SetTitleUpdatesExistingEntry verifies SetTitle sets and clears the
// title on a stored entry.
func TestSS_SetTitleUpdatesExistingEntry(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())
	store.Add(tui.StoredSessionEntry{ID: "s1", StartedAt: time.Now()})

	if ok := store.SetTitle("s1", "rename me"); !ok {
		t.Fatal("SetTitle returned false for an existing entry")
	}
	got, _ := store.Get("s1")
	if got.Title != "rename me" {
		t.Errorf("Title after SetTitle: want %q, got %q", "rename me", got.Title)
	}

	if ok := store.SetTitle("s1", ""); !ok {
		t.Fatal("SetTitle(clear) returned false for an existing entry")
	}
	got, _ = store.Get("s1")
	if got.Title != "" {
		t.Errorf("Title after clearing SetTitle: want empty, got %q", got.Title)
	}
}

// TestSS_SetTitleUnknownIDReturnsFalse verifies SetTitle reports failure for a
// session ID that is not in the store.
func TestSS_SetTitleUnknownIDReturnsFalse(t *testing.T) {
	store := tui.NewSessionStore(t.TempDir())
	if ok := store.SetTitle("missing", "x"); ok {
		t.Error("SetTitle must return false for an unknown session ID")
	}
}
