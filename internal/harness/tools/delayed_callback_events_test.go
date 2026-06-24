package tools

import (
	"sync"
	"testing"
	"time"
)

// capturingSink records every callback event emitted by the CallbackManager so
// the lifecycle (scheduled/fired/canceled) can be asserted deterministically.
type capturingSink struct {
	mu     sync.Mutex
	events []sinkEvent
}

type sinkEvent struct {
	Event string
	Info  CallbackInfo
}

func (s *capturingSink) Emit(event string, info CallbackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, sinkEvent{Event: event, Info: info})
}

func (s *capturingSink) snapshot() []sinkEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sinkEvent, len(s.events))
	copy(out, s.events)
	return out
}

func (s *capturingSink) byType(event string) []sinkEvent {
	out := make([]sinkEvent, 0)
	for _, e := range s.snapshot() {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

// fixedClock returns a deterministic fake clock seeded at t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestCallbackEventsLifecycle(t *testing.T) {
	base := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)

	t.Run("scheduled emitted on Set with fires_at", func(t *testing.T) {
		sink := &capturingSink{}
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter, WithEventSink(sink))
		mgr.now = fixedClock(base)
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 30*time.Second, "check deploy"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		scheduled := sink.byType("callback.scheduled")
		if len(scheduled) != 1 {
			t.Fatalf("expected 1 callback.scheduled event, got %d", len(scheduled))
		}
		got := scheduled[0].Info
		if got.ID != info.ID {
			t.Errorf("scheduled event ID = %q, want %q", got.ID, info.ID)
		}
		if got.State != CallbackStatePending {
			t.Errorf("scheduled event state = %q, want pending", got.State)
		}
		wantFiresAt := base.Add(30 * time.Second)
		if !got.FiresAt.Equal(wantFiresAt) {
			t.Errorf("scheduled event fires_at = %v, want %v", got.FiresAt, wantFiresAt)
		}
		if got.ConversationID != "conv-1" {
			t.Errorf("scheduled event conv = %q, want conv-1", got.ConversationID)
		}
	})

	t.Run("no scheduled event when Set fails validation", func(t *testing.T) {
		sink := &capturingSink{}
		mgr := NewCallbackManager(&mockRunStarter{}, WithEventSink(sink))
		defer mgr.Shutdown()

		if _, err := mgr.Set(setReq("conv-1", 1*time.Second, "too short")); err == nil {
			t.Fatal("expected error for too-short delay")
		}
		if n := len(sink.byType("callback.scheduled")); n != 0 {
			t.Errorf("expected 0 scheduled events on failed Set, got %d", n)
		}
	})

	t.Run("fired emitted when callback fires", func(t *testing.T) {
		sink := &capturingSink{}
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter, WithEventSink(sink))
		mgr.now = fixedClock(base)
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 5*time.Second, "wake up"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Stop the real timer; drive fire directly (no real sleeps).
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)

		fired := sink.byType("callback.fired")
		if len(fired) != 1 {
			t.Fatalf("expected 1 callback.fired event, got %d", len(fired))
		}
		if fired[0].Info.ID != info.ID {
			t.Errorf("fired event ID = %q, want %q", fired[0].Info.ID, info.ID)
		}
		if fired[0].Info.State != CallbackStateFired {
			t.Errorf("fired event state = %q, want fired", fired[0].Info.State)
		}
	})

	t.Run("no duplicate fired on second fire", func(t *testing.T) {
		sink := &capturingSink{}
		mgr := NewCallbackManager(&mockRunStarter{}, WithEventSink(sink))
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 5*time.Second, "wake up"))
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)
		mgr.fire(info.ID) // no-op

		if n := len(sink.byType("callback.fired")); n != 1 {
			t.Errorf("expected exactly 1 fired event, got %d", n)
		}
	})

	t.Run("canceled emitted on Cancel", func(t *testing.T) {
		sink := &capturingSink{}
		mgr := NewCallbackManager(&mockRunStarter{}, WithEventSink(sink))
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		if _, err := mgr.Cancel(info.ID); err != nil {
			t.Fatalf("unexpected cancel error: %v", err)
		}

		canceled := sink.byType("callback.canceled")
		if len(canceled) != 1 {
			t.Fatalf("expected 1 callback.canceled event, got %d", len(canceled))
		}
		if canceled[0].Info.ID != info.ID {
			t.Errorf("canceled event ID = %q, want %q", canceled[0].Info.ID, info.ID)
		}
		if canceled[0].Info.State != CallbackStateCanceled {
			t.Errorf("canceled event state = %q, want canceled", canceled[0].Info.State)
		}
	})

	t.Run("no canceled event when Cancel fails", func(t *testing.T) {
		sink := &capturingSink{}
		mgr := NewCallbackManager(&mockRunStarter{}, WithEventSink(sink))
		defer mgr.Shutdown()

		if _, err := mgr.Cancel("does-not-exist"); err == nil {
			t.Fatal("expected error canceling nonexistent callback")
		}
		if n := len(sink.byType("callback.canceled")); n != 0 {
			t.Errorf("expected 0 canceled events on failed Cancel, got %d", n)
		}
	})

	t.Run("canceled emitted for each pending callback on Shutdown", func(t *testing.T) {
		sink := &capturingSink{}
		mgr := NewCallbackManager(&mockRunStarter{}, WithEventSink(sink))

		_, _ = mgr.Set(setReq("conv-1", 30*time.Second, "a"))
		_, _ = mgr.Set(setReq("conv-1", 30*time.Second, "b"))

		mgr.Shutdown()

		canceled := sink.byType("callback.canceled")
		if len(canceled) != 2 {
			t.Fatalf("expected 2 canceled events on shutdown, got %d", len(canceled))
		}
		for _, e := range canceled {
			if e.Info.State != CallbackStateCanceled {
				t.Errorf("shutdown canceled event state = %q, want canceled", e.Info.State)
			}
		}
	})

	t.Run("nil sink is a no-op (default constructor)", func(t *testing.T) {
		mgr := NewCallbackManager(&mockRunStarter{})
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 5*time.Second, "check"))
		if err != nil {
			t.Fatalf("unexpected error with nil sink: %v", err)
		}
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
		mgr.fire(info.ID)          // must not panic
		_, _ = mgr.Cancel(info.ID) // already fired -> error, must not panic
	})
}
