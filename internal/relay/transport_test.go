package relay_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestTransportManagerRegisterAndGet(t *testing.T) {
	tm := relay.NewTransportManager()

	session := &relay.TransportSession{
		ID:          "sess-1",
		WorkerID:    "w-1",
		TenantID:    "t1",
		ConnectedAt: time.Now(),
	}

	if err := tm.RegisterSession(session); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	got, err := tm.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.WorkerID != "w-1" {
		t.Errorf("WorkerID: got %q, want w-1", got.WorkerID)
	}

	// Get by worker ID.
	ws, err := tm.GetWorkerSession("w-1")
	if err != nil {
		t.Fatalf("GetWorkerSession: %v", err)
	}
	if ws.ID != "sess-1" {
		t.Errorf("Session ID: got %q, want sess-1", ws.ID)
	}
}

func TestTransportManagerReconnect(t *testing.T) {
	tm := relay.NewTransportManager()

	s1 := &relay.TransportSession{
		ID: "sess-old", WorkerID: "w-1", TenantID: "t1",
		ConnectedAt: time.Now(),
	}
	tm.RegisterSession(s1)

	// Reconnect with a new session for the same worker.
	s2 := &relay.TransportSession{
		ID: "sess-new", WorkerID: "w-1", TenantID: "t1",
		ConnectedAt: time.Now(),
	}
	tm.RegisterSession(s2)

	// Old session should be gone.
	_, err := tm.GetSession("sess-old")
	if err == nil {
		t.Error("old session should be removed on reconnect")
	}

	// New session should be the one for the worker.
	ws, err := tm.GetWorkerSession("w-1")
	if err != nil {
		t.Fatalf("GetWorkerSession: %v", err)
	}
	if ws.ID != "sess-new" {
		t.Errorf("expected sess-new, got %s", ws.ID)
	}
}

func TestTransportManagerRemoveSession(t *testing.T) {
	tm := relay.NewTransportManager()

	s := &relay.TransportSession{
		ID: "sess-1", WorkerID: "w-1", TenantID: "t1",
		ConnectedAt: time.Now(),
	}
	tm.RegisterSession(s)
	tm.RemoveSession("sess-1")

	_, err := tm.GetSession("sess-1")
	if err == nil {
		t.Error("session should be removed")
	}
	_, err = tm.GetWorkerSession("w-1")
	if err == nil {
		t.Error("worker session should be removed")
	}
}

func TestTransportManagerAddAndRemoveRun(t *testing.T) {
	tm := relay.NewTransportManager()

	s := &relay.TransportSession{
		ID: "sess-1", WorkerID: "w-1", TenantID: "t1",
		ConnectedAt: time.Now(),
	}
	tm.RegisterSession(s)

	if err := tm.AddRunToSession("sess-1", "run-1"); err != nil {
		t.Fatalf("AddRunToSession: %v", err)
	}
	if err := tm.AddRunToSession("sess-1", "run-2"); err != nil {
		t.Fatalf("AddRunToSession: %v", err)
	}

	// Duplicate shouldn't error.
	if err := tm.AddRunToSession("sess-1", "run-1"); err != nil {
		t.Fatalf("AddRunToSession duplicate: %v", err)
	}

	if count := tm.ActiveRunCount("w-1"); count != 2 {
		t.Errorf("ActiveRunCount: got %d, want 2", count)
	}

	tm.RemoveRunFromSession("sess-1", "run-1")
	if count := tm.ActiveRunCount("w-1"); count != 1 {
		t.Errorf("ActiveRunCount after remove: got %d, want 1", count)
	}
}

func TestTransportManagerListSessions(t *testing.T) {
	tm := relay.NewTransportManager()

	tm.RegisterSession(&relay.TransportSession{
		ID: "sess-1", WorkerID: "w-1", TenantID: "t1", ConnectedAt: time.Now(),
	})
	tm.RegisterSession(&relay.TransportSession{
		ID: "sess-2", WorkerID: "w-2", TenantID: "t1", ConnectedAt: time.Now(),
	})

	sessions := tm.ListSessions()
	if len(sessions) != 2 {
		t.Errorf("ListSessions: got %d, want 2", len(sessions))
	}
}

func TestEventBusPubSub(t *testing.T) {
	eb := relay.NewEventBus()

	ch1 := eb.Subscribe("run-1")
	ch2 := eb.Subscribe("run-1")
	ch3 := eb.Subscribe("run-2")

	event := relay.TransportEvent{
		RunID:     "run-1",
		EventType: "run.started",
		Timestamp: time.Now(),
	}

	ctx := context.Background()
	eb.Publish(ctx, event)

	// Both subscribers for run-1 should receive the event.
	select {
	case e := <-ch1:
		if e.EventType != "run.started" {
			t.Errorf("ch1: got %q, want run.started", e.EventType)
		}
	default:
		t.Error("ch1 did not receive event")
	}

	select {
	case e := <-ch2:
		if e.EventType != "run.started" {
			t.Errorf("ch2: got %q, want run.started", e.EventType)
		}
	default:
		t.Error("ch2 did not receive event")
	}

	// ch3 (run-2) should NOT receive the event.
	select {
	case <-ch3:
		t.Error("ch3 should not receive run-1 event")
	default:
		// Expected.
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	eb := relay.NewEventBus()

	ch := eb.Subscribe("run-1")
	eb.Unsubscribe("run-1", ch)

	event := relay.TransportEvent{
		RunID: "run-1", EventType: "test", Timestamp: time.Now(),
	}
	ctx := context.Background()
	eb.Publish(ctx, event)

	// Channel should be closed; reading should return zero value.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after unsubscribe")
		}
	default:
		// Channel is closed and empty.
	}
}

func TestEventBusConcurrentPublishAndUnsubscribe(t *testing.T) {
	eb := relay.NewEventBus()
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		ch := eb.Subscribe("run-concurrent")
		wg.Add(2)
		go func() {
			defer wg.Done()
			eb.Publish(ctx, relay.TransportEvent{
				RunID: "run-concurrent", EventType: "test", Timestamp: time.Now(),
			})
		}()
		go func(ch chan relay.TransportEvent) {
			defer wg.Done()
			eb.Unsubscribe("run-concurrent", ch)
		}(ch)
	}
	wg.Wait()
}

func TestCommandQueueEnqueueDequeue(t *testing.T) {
	cq := relay.NewCommandQueue()

	cmd1 := &relay.TransportCommand{
		ID: "cmd-1", RunID: "run-1", Command: "cancel", CreatedAt: time.Now(),
	}
	cmd2 := &relay.TransportCommand{
		ID: "cmd-2", RunID: "run-1", Command: "steer", Payload: "check logs", CreatedAt: time.Now(),
	}

	cq.Enqueue("w-1", cmd1)
	cq.Enqueue("w-1", cmd2)

	if count := cq.PendingCount("w-1"); count != 2 {
		t.Errorf("PendingCount: got %d, want 2", count)
	}

	cmds := cq.Dequeue("w-1")
	if len(cmds) != 2 {
		t.Errorf("Dequeue: got %d, want 2", len(cmds))
	}
	if cmds[0].Command != "cancel" {
		t.Errorf("first command: got %q, want cancel", cmds[0].Command)
	}
	if cmds[1].Command != "steer" {
		t.Errorf("second command: got %q, want steer", cmds[1].Command)
	}

	// Queue should be empty after dequeue.
	if count := cq.PendingCount("w-1"); count != 0 {
		t.Errorf("PendingCount after dequeue: got %d, want 0", count)
	}
}

func TestCommandQueueEmptyDequeue(t *testing.T) {
	cq := relay.NewCommandQueue()
	cmds := cq.Dequeue("unknown")
	if len(cmds) != 0 {
		t.Errorf("empty dequeue: got %d, want 0", len(cmds))
	}
}

func TestTransportSessionValidation(t *testing.T) {
	tm := relay.NewTransportManager()

	// Empty session ID.
	err := tm.RegisterSession(&relay.TransportSession{
		WorkerID: "w-1",
	})
	if err == nil {
		t.Error("expected error for empty session ID")
	}

	// Empty worker ID.
	err = tm.RegisterSession(&relay.TransportSession{
		ID: "sess-1",
	})
	if err == nil {
		t.Error("expected error for empty worker ID")
	}
}
