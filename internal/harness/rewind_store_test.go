package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteConversationStoreRewindPointRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestConversationStore(t)
	if err := store.SaveConversation(ctx, "rewind-conv", []Message{{Role: "user", Content: "edit it"}}); err != nil {
		t.Fatal(err)
	}
	point := RewindPoint{ID: "point-1", ConversationID: "rewind-conv", Step: 1, Tool: "write", Files: []RewindFileSnapshot{{Path: "notes.txt", Content: []byte("before"), Exists: true}}}
	if err := store.SaveRewindPoint(ctx, point); err != nil {
		t.Fatalf("SaveRewindPoint: %v", err)
	}
	points, err := store.ListRewindPoints(ctx, "rewind-conv")
	if err != nil {
		t.Fatalf("ListRewindPoints: %v", err)
	}
	if len(points) != 1 || points[0].ID != point.ID || points[0].Files[0].Path != "notes.txt" || string(points[0].Files[0].Content) != "before" {
		t.Fatalf("points = %#v", points)
	}
}

func TestSQLiteConversationStoreRestoreRewindRefusesExternalModification(t *testing.T) {
	ctx := context.Background()
	store := newTestConversationStore(t)
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConversation(ctx, "restore-conv", []Message{{Role: "user", Content: "keep"}, {Role: "assistant", Content: "drop"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRewindPoint(ctx, RewindPoint{ID: "restore-point", ConversationID: "restore-conv", Step: 0, Tool: "write", Files: []RewindFileSnapshot{{Path: "notes.txt", Content: []byte("before"), Exists: true, ExpectedHash: RewindContentHash([]byte("agent"))}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreRewindPoint(ctx, "restore-conv", "restore-point", root, false); err == nil {
		t.Fatal("RestoreRewindPoint accepted externally modified file")
	}
	result, err := store.RestoreRewindPoint(ctx, "restore-conv", "restore-point", root, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesRestored != 1 {
		t.Fatalf("result=%+v", result)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "before" {
		t.Fatalf("file=%q", got)
	}
	msgs, err := store.LoadMessages(ctx, "restore-conv")
	if err != nil || len(msgs) != 1 || msgs[0].Content != "keep" {
		t.Fatalf("msgs=%#v err=%v", msgs, err)
	}
}

func TestCaptureRewindPreImageSkipsOversizedFiles(t *testing.T) {
	ctx := context.Background()
	store := newTestConversationStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), make([]byte, rewindMaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CaptureRewindPreImage(ctx, store, RewindPoint{ID: "cap", ConversationID: "capconv", Tool: "write"}, root, []byte(`{"path":"big.txt"}`)); err != nil {
		t.Fatal(err)
	}
	points, err := store.ListRewindPoints(ctx, "capconv")
	if err != nil || len(points) != 1 || !points[0].Files[0].Skipped {
		t.Fatalf("points=%#v err=%v", points, err)
	}
}

func TestCapturedAndFinalizedRewindRejectsExternalEdit(t *testing.T) {
	ctx := context.Background()
	store := newTestConversationStore(t)
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	point := RewindPoint{ID: "real", ConversationID: "real-conv", Step: 0, Tool: "write"}
	if err := CaptureRewindPreImage(ctx, store, point, root, []byte(`{"path":"notes.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeRewindPoint(ctx, store, "real", root); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreRewindPoint(ctx, "real-conv", "real", root, false); err != nil {
		t.Fatalf("unchanged restore: %v", err)
	}
	if err := os.WriteFile(path, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreRewindPoint(ctx, "real-conv", "real", root, false); err == nil {
		t.Fatal("external edit accepted")
	}
}

func TestConversationSnapshotCapSkipsAdditionalContent(t *testing.T) {
	ctx := context.Background()
	store := newTestConversationStore(t)
	first := make([]byte, rewindMaxConversationBytes)
	if err := store.SaveRewindPoint(ctx, RewindPoint{ID: "one", ConversationID: "cap-total", Files: []RewindFileSnapshot{{Path: "one", Content: first, Exists: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRewindPoint(ctx, RewindPoint{ID: "two", ConversationID: "cap-total", Files: []RewindFileSnapshot{{Path: "two", Content: []byte("x"), Exists: true}}}); err != nil {
		t.Fatal(err)
	}
	points, err := store.ListRewindPoints(ctx, "cap-total")
	if err != nil {
		t.Fatal(err)
	}
	if !points[0].Files[0].Skipped {
		t.Fatalf("expected cap skip: %#v", points[0])
	}
}

func TestExtractRewindPathsUsesWriteEditAndPatchArguments(t *testing.T) {
	paths := ExtractRewindPaths("apply_patch", []byte(`{"patch":"--- a/a.txt\n+++ b/a.txt\n--- a/b.txt\n+++ b/b.txt"}`))
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "b.txt" {
		t.Fatalf("paths = %#v", paths)
	}
}
