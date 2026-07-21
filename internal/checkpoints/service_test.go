package checkpoints

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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

func TestServiceStoreDenyExpireAndWaitCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	if err := store.Close(); err != nil {
		t.Fatalf("MemoryStore Close: %v", err)
	}
	svc := NewService(store, func() time.Time { return now })
	if svc.Store() != store {
		t.Fatal("Store did not return configured store")
	}

	denied, err := svc.Create(context.Background(), CreateRequest{
		Kind:       KindApproval,
		RunID:      "run-deny",
		DeadlineAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Create denied record: %v", err)
	}
	if err := svc.Deny(context.Background(), denied.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	result, err := svc.Wait(context.Background(), denied.ID)
	if err != nil {
		t.Fatalf("Wait denied: %v", err)
	}
	if result.Status != StatusDenied {
		t.Fatalf("Wait denied status = %q, want %q", result.Status, StatusDenied)
	}

	expired, err := svc.Create(context.Background(), CreateRequest{
		Kind:       KindExternalResume,
		RunID:      "run-expire",
		DeadlineAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Create expired record: %v", err)
	}
	if err := svc.Expire(context.Background(), expired.ID); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	result, err = svc.Wait(context.Background(), expired.ID)
	if err != nil {
		t.Fatalf("Wait expired: %v", err)
	}
	if result.Status != StatusExpired {
		t.Fatalf("Wait expired status = %q, want %q", result.Status, StatusExpired)
	}

	cancelled, err := svc.Create(context.Background(), CreateRequest{
		Kind:       KindUserInput,
		RunID:      "run-cancel-wait",
		DeadlineAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Create cancellation record: %v", err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := svc.Wait(waitCtx, cancelled.ID)
		errCh <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		waiterCount := len(svc.waiters[cancelled.ID])
		svc.mu.Unlock()
		if waiterCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for service waiter registration")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait cancellation error = %v, want context.Canceled", err)
	}
	svc.mu.Lock()
	_, stillRegistered := svc.waiters[cancelled.ID]
	svc.mu.Unlock()
	if stillRegistered {
		t.Fatal("cancelled waiter was not unregistered")
	}
}

func TestSQLiteStoreUpdatePendingByWorkflowRunAndNotFound(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "checkpoints.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 5, 12, 0, 0, 123, time.UTC)
	older := &Record{
		ID:            "checkpoint-old",
		Kind:          KindExternalResume,
		Status:        StatusPending,
		WorkflowRunID: "workflow-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	newer := &Record{
		ID:            "checkpoint-new",
		Kind:          KindExternalResume,
		Status:        StatusPending,
		WorkflowRunID: "workflow-1",
		CreatedAt:     now,
		UpdatedAt:     now.Add(time.Minute),
	}
	if err := store.Create(context.Background(), older); err != nil {
		t.Fatalf("Create older: %v", err)
	}
	if err := store.Create(context.Background(), newer); err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	pending, err := store.PendingByWorkflowRun(context.Background(), "workflow-1")
	if err != nil {
		t.Fatalf("PendingByWorkflowRun: %v", err)
	}
	if pending == nil || pending.ID != newer.ID {
		t.Fatalf("pending workflow checkpoint = %+v, want %s", pending, newer.ID)
	}

	newer.Status = StatusResumed
	newer.ResumePayload = `{"approved":true}`
	newer.UpdatedAt = now.Add(2 * time.Minute)
	if err := store.Update(context.Background(), newer); err != nil {
		t.Fatalf("Update: %v", err)
	}
	loaded, err := store.Get(context.Background(), newer.ID)
	if err != nil {
		t.Fatalf("Get updated: %v", err)
	}
	if loaded.Status != StatusResumed {
		t.Fatalf("updated status = %q, want %q", loaded.Status, StatusResumed)
	}
	if loaded.ResumePayload != newer.ResumePayload {
		t.Fatalf("resume payload = %q, want %q", loaded.ResumePayload, newer.ResumePayload)
	}
	if !loaded.UpdatedAt.Equal(newer.UpdatedAt) {
		t.Fatalf("updated_at = %s, want %s", loaded.UpdatedAt, newer.UpdatedAt)
	}

	pending, err = store.PendingByWorkflowRun(context.Background(), "workflow-1")
	if err != nil {
		t.Fatalf("PendingByWorkflowRun after update: %v", err)
	}
	if pending == nil || pending.ID != older.ID {
		t.Fatalf("pending workflow checkpoint after update = %+v, want %s", pending, older.ID)
	}

	_, err = store.Get(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("Get missing error = %v, want NotFoundError", err)
	}
	if !strings.Contains(err.Error(), "checkpoint not found: missing") {
		t.Fatalf("NotFoundError text = %q", err.Error())
	}
	if IsNotFound(context.Canceled) {
		t.Fatal("IsNotFound returned true for unrelated error")
	}
}

// TestServiceApproveWithPayloadWakesWaiterWithPayload covers the
// approve-with-payload path used by plan-exit approach options: the waiter
// observes an approved status and the operator's selected option.
func TestServiceApproveWithPayloadWakesWaiterWithPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), func() time.Time { return now })
	record, err := svc.Create(context.Background(), CreateRequest{
		Kind:       KindApproval,
		RunID:      "run-1",
		CallID:     "plan_exit",
		Tool:       "plan_exit",
		DeadlineAt: now.Add(5 * time.Minute),
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

	if err := svc.ApproveWithPayload(context.Background(), record.ID, map[string]any{"option": "b"}); err != nil {
		t.Fatalf("ApproveWithPayload: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Wait error: %v", err)
	case result := <-waitCh:
		if result.Status != StatusApproved {
			t.Fatalf("wait status = %q, want %q", result.Status, StatusApproved)
		}
		if got := result.Payload["option"]; got != "b" {
			t.Fatalf("payload option = %v, want b", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approve")
	}

	loaded, err := svc.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Status != StatusApproved {
		t.Fatalf("stored status = %q, want %q", loaded.Status, StatusApproved)
	}
}
