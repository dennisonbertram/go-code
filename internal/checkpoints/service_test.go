package checkpoints

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsCheckpointAcrossReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "checkpoints.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	svc := NewService(store, func() time.Time { return now })
	record, err := svc.Create(context.Background(), CreateRequest{
		Kind:       KindApproval,
		RunID:      "run-1",
		CallID:     "call-1",
		Tool:       "write",
		Args:       `{"path":"README.md"}`,
		DeadlineAt: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore reopen: %v", err)
	}
	if err := reopened.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate reopen: %v", err)
	}
	defer reopened.Close()

	svc = NewService(reopened, func() time.Time { return now })
	loaded, err := svc.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Status != StatusPending {
		t.Fatalf("status = %q, want %q", loaded.Status, StatusPending)
	}
	if loaded.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", loaded.RunID)
	}
	if loaded.Tool != "write" {
		t.Fatalf("tool = %q, want write", loaded.Tool)
	}

	pending, ok, err := svc.PendingByRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("PendingByRun: %v", err)
	}
	if !ok {
		t.Fatal("expected pending checkpoint for run")
	}
	if pending.ID != record.ID {
		t.Fatalf("pending id = %q, want %q", pending.ID, record.ID)
	}
}

func TestServiceResumeWakesWaiterAndPersistsPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), func() time.Time { return now })
	record, err := svc.Create(context.Background(), CreateRequest{
		Kind:           KindExternalResume,
		WorkflowRunID:  "wf-1",
		RunID:          "run-1",
		SuspendPayload: `{"prompt":"Need human confirmation"}`,
		ResumeSchema:   `{"type":"object"}`,
		DeadlineAt:     now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitCh := make(chan WaitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := svc.Wait(context.Background(), record.ID)
		if err != nil {
			errCh <- err
			return
		}
		waitCh <- result
	}()

	if err := svc.Resume(context.Background(), record.ID, map[string]any{"decision": "approved"}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Wait error: %v", err)
	case result := <-waitCh:
		if result.Status != StatusResumed {
			t.Fatalf("wait status = %q, want %q", result.Status, StatusResumed)
		}
		if got := result.Payload["decision"]; got != "approved" {
			t.Fatalf("payload decision = %v, want approved", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resume")
	}

	loaded, err := svc.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Status != StatusResumed {
		t.Fatalf("stored status = %q, want %q", loaded.Status, StatusResumed)
	}
	if loaded.ResumePayload == "" {
		t.Fatal("expected persisted resume payload")
	}
}

func TestServiceStoreReturnsConfiguredStore(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	svc := NewService(store, nil)
	if svc.Store() != store {
		t.Fatal("Store() did not return configured store")
	}
}

func TestMemoryStoreCloseIsNoop(t *testing.T) {
	t.Parallel()

	if err := NewMemoryStore().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestServiceDenyAndExpireResolveWaiters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		action func(*Service, string) error
		want   Status
	}{
		{name: "deny", action: func(s *Service, id string) error { return s.Deny(ctx, id) }, want: StatusDenied},
		{name: "expire", action: func(s *Service, id string) error { return s.Expire(ctx, id) }, want: StatusExpired},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := NewService(NewMemoryStore(), func() time.Time { return now })
			record, err := svc.Create(ctx, CreateRequest{
				Kind:  KindApproval,
				RunID: "run-" + tc.name,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			waitCh := make(chan WaitResult, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := svc.Wait(ctx, record.ID)
				if err != nil {
					errCh <- err
					return
				}
				waitCh <- result
			}()

			if err := tc.action(svc, record.ID); err != nil {
				t.Fatalf("resolve action: %v", err)
			}
			select {
			case err := <-errCh:
				t.Fatalf("Wait error: %v", err)
			case result := <-waitCh:
				if result.Status != tc.want {
					t.Fatalf("status = %q, want %q", result.Status, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for checkpoint resolution")
			}
		})
	}
}

func TestServiceWaitContextCancellationUnregistersWaiter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), func() time.Time { return now })
	record, err := svc.Create(context.Background(), CreateRequest{
		Kind:  KindExternalResume,
		RunID: "run-cancel",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Wait(waitCtx, record.ID); err != context.Canceled {
		t.Fatalf("Wait canceled error = %v, want context.Canceled", err)
	}

	if err := svc.Resume(context.Background(), record.ID, map[string]any{"ok": true}); err != nil {
		t.Fatalf("Resume after canceled wait: %v", err)
	}
	result, err := svc.Wait(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Wait after resume: %v", err)
	}
	if result.Status != StatusResumed {
		t.Fatalf("status = %q, want resumed", result.Status)
	}
}

func TestCheckpointNotFoundHelpers(t *testing.T) {
	t.Parallel()

	err := &NotFoundError{ID: "checkpoint_missing"}
	if got := err.Error(); got != "checkpoint not found: checkpoint_missing" {
		t.Fatalf("Error() = %q", got)
	}
	if !IsNotFound(err) {
		t.Fatal("IsNotFound must identify NotFoundError")
	}
	if IsNotFound(context.Canceled) {
		t.Fatal("IsNotFound must reject unrelated errors")
	}
}

func TestSQLiteStoreUpdateAndPendingByWorkflowRun(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "checkpoints.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	record := &Record{
		ID:            "checkpoint_sqlite",
		Kind:          KindExternalResume,
		Status:        StatusPending,
		RunID:         "run-sqlite",
		WorkflowRunID: "workflow-sqlite",
		DeadlineAt:    now.Add(time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	pending, err := store.PendingByWorkflowRun(context.Background(), "workflow-sqlite")
	if err != nil {
		t.Fatalf("PendingByWorkflowRun: %v", err)
	}
	if pending == nil || pending.ID != record.ID {
		t.Fatalf("pending = %#v, want %s", pending, record.ID)
	}

	record.Status = StatusDenied
	record.ResumePayload = `{"reason":"no"}`
	record.UpdatedAt = now.Add(time.Second)
	if err := store.Update(context.Background(), record); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded, err := store.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Status != StatusDenied || loaded.ResumePayload != `{"reason":"no"}` {
		t.Fatalf("loaded = %#v", loaded)
	}
	pending, err = store.PendingByWorkflowRun(context.Background(), "workflow-sqlite")
	if err != nil {
		t.Fatalf("PendingByWorkflowRun after update: %v", err)
	}
	if pending != nil {
		t.Fatalf("expected no pending workflow checkpoint after deny, got %#v", pending)
	}
}
