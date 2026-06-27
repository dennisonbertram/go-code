package relay_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestSQLiteEventArtifactStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay_ea.db")
	ws, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	defer ws.Close()
	if err := ws.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store, err := relay.NewSQLiteEventArtifactStore(ws.DB())
	if err != nil {
		t.Fatalf("NewSQLiteEventArtifactStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()

	t.Run("placement record", func(t *testing.T) {
		record := &relay.PlacementRecord{
			RunID:           "run-1",
			SelectedWorker:  "w-1",
			EligibleWorkers: []string{"w-1", "w-2"},
			RejectedWorkers: []relay.RejectionReason{
				{WorkerID: "w-3", Reason: "offline", Category: "offline"},
			},
			RoutingReason: "selected w-1 (local, low load)",
			Timestamp:     time.Now(),
		}

		if err := store.SavePlacementRecord(ctx, record); err != nil {
			t.Fatalf("SavePlacementRecord: %v", err)
		}

		got, err := store.GetPlacementRecord(ctx, "run-1")
		if err != nil {
			t.Fatalf("GetPlacementRecord: %v", err)
		}
		if got.SelectedWorker != "w-1" {
			t.Errorf("SelectedWorker: got %q, want w-1", got.SelectedWorker)
		}
	})

	t.Run("placement record not found", func(t *testing.T) {
		_, err := store.GetPlacementRecord(ctx, "nonexistent")
		if err != relay.ErrArtifactNotFound {
			t.Errorf("expected ErrArtifactNotFound, got %v", err)
		}
	})

	t.Run("events", func(t *testing.T) {
		e1 := &relay.EventRecord{
			Seq: 1, RunID: "run-events", EventID: "ev-1",
			EventType: "run.started", Payload: `{"status":"started"}`,
			Timestamp: time.Now(), WorkerID: "w-1",
		}
		e2 := &relay.EventRecord{
			Seq: 2, RunID: "run-events", EventID: "ev-2",
			EventType: "run.completed", Payload: `{"status":"completed"}`,
			Timestamp: time.Now(), WorkerID: "w-1",
		}

		if err := store.AppendEvent(ctx, e1); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		if err := store.AppendEvent(ctx, e2); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}

		events, err := store.GetEvents(ctx, "run-events", -1)
		if err != nil {
			t.Fatalf("GetEvents: %v", err)
		}
		if len(events) != 2 {
			t.Errorf("event count: got %d, want 2", len(events))
		}

		// GetEvents with afterSeq should skip.
		after, err := store.GetEvents(ctx, "run-events", 1)
		if err != nil {
			t.Fatalf("GetEvents after: %v", err)
		}
		if len(after) != 1 {
			t.Errorf("after seq 1: got %d, want 1", len(after))
		}
	})

	t.Run("artifacts", func(t *testing.T) {
		a := &relay.Artifact{
			ID: "art-1", RunID: "run-art", Type: relay.ArtifactPatch,
			WorkerID: "w-1", MIMEType: "text/plain",
			Data: `diff --git a/test.go b/test.go`, Visibility: "tenant",
			CreatedAt: time.Now(),
		}
		if err := store.SaveArtifact(ctx, a); err != nil {
			t.Fatalf("SaveArtifact: %v", err)
		}

		got, err := store.GetArtifact(ctx, "art-1")
		if err != nil {
			t.Fatalf("GetArtifact: %v", err)
		}
		if got.Type != relay.ArtifactPatch {
			t.Errorf("Type: got %q, want patch", got.Type)
		}

		list, err := store.ListArtifacts(ctx, "run-art")
		if err != nil {
			t.Fatalf("ListArtifacts: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("artifact count: got %d, want 1", len(list))
		}
	})

	t.Run("artifact not found", func(t *testing.T) {
		_, err := store.GetArtifact(ctx, "nonexistent")
		if err != relay.ErrArtifactNotFound {
			t.Errorf("expected ErrArtifactNotFound, got %v", err)
		}
	})
}

func TestSQLiteEventArtifactStoreRejectsNilDB(t *testing.T) {
	_, err := relay.NewSQLiteEventArtifactStore(nil)
	if err == nil {
		t.Fatal("expected error for nil database")
	}
}

func TestPlacementRecordJSON(t *testing.T) {
	record := &relay.PlacementRecord{
		RunID:          "run-1",
		SelectedWorker: "w-1",
		RoutingReason:  "selected local worker",
		Timestamp:      time.Now(),
	}
	data, err := relay.PlacementRecordToJSON(record)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	restored, err := relay.PlacementRecordFromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if restored.SelectedWorker != "w-1" {
		t.Errorf("SelectedWorker: got %q, want w-1", restored.SelectedWorker)
	}
}
