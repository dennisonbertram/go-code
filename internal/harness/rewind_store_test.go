package harness

import (
	"context"
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
