package checkpoints

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceStoreDenyExpireAndNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store, time.Now)
	if service.Store() != store {
		t.Fatal("Store did not return backing store")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	denyRecord, err := service.Create(ctx, CreateRequest{
		Kind:  KindApproval,
		RunID: "run-deny",
	})
	if err != nil {
		t.Fatalf("Create deny: %v", err)
	}
	if err := service.Deny(ctx, denyRecord.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	got, err := service.Get(ctx, denyRecord.ID)
	if err != nil {
		t.Fatalf("Get denied: %v", err)
	}
	if got.Status != StatusDenied {
		t.Fatalf("status = %q, want %q", got.Status, StatusDenied)
	}

	expireRecord, err := service.Create(ctx, CreateRequest{
		Kind:  KindApproval,
		RunID: "run-expire",
	})
	if err != nil {
		t.Fatalf("Create expire: %v", err)
	}
	if err := service.Expire(ctx, expireRecord.ID); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	got, err = service.Get(ctx, expireRecord.ID)
	if err != nil {
		t.Fatalf("Get expired: %v", err)
	}
	if got.Status != StatusExpired {
		t.Fatalf("status = %q, want %q", got.Status, StatusExpired)
	}

	_, err = store.Get(ctx, "missing")
	if !IsNotFound(err) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error %q does not include missing id", err.Error())
	}
}

func TestSQLiteStoreUpdateAndPendingByWorkflowRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoints.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	record := &Record{
		ID:            "checkpoint-1",
		Kind:          KindExternalResume,
		Status:        StatusPending,
		WorkflowRunID: "workflow-1",
		CallID:        "step-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(ctx, record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	record.Status = StatusDenied
	record.UpdatedAt = now.Add(time.Minute)
	if err := store.Update(ctx, record); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := store.Get(ctx, record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusDenied {
		t.Fatalf("status = %q, want %q", got.Status, StatusDenied)
	}

	pending := &Record{
		ID:            "checkpoint-2",
		Kind:          KindExternalResume,
		Status:        StatusPending,
		WorkflowRunID: "workflow-1",
		CallID:        "step-2",
		CreatedAt:     now,
		UpdatedAt:     now.Add(2 * time.Minute),
	}
	if err := store.Create(ctx, pending); err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	found, err := store.PendingByWorkflowRun(ctx, "workflow-1")
	if err != nil {
		t.Fatalf("PendingByWorkflowRun: %v", err)
	}
	if found == nil || found.ID != pending.ID {
		t.Fatalf("pending = %#v, want id %q", found, pending.ID)
	}
}

func TestServiceWaitCanceledContextUnregistersWaiter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(NewMemoryStore(), time.Now)
	record, err := service.Create(ctx, CreateRequest{
		Kind:  KindApproval,
		RunID: "run-cancel",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Wait(waitCtx, record.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if waiters := service.waiters[record.ID]; len(waiters) != 0 {
		t.Fatalf("waiter count = %d, want 0", len(waiters))
	}
}
