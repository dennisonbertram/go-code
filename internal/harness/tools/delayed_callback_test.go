package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Mock ---

type mockRunStarter struct {
	mu      sync.Mutex
	calls   []startRunCall
	err     error
	startFn func(prompt, convID, tenantID, agentID string) error
}

// setReq is a small helper for tests that schedule callbacks via the manager
// directly (not through the tool handler). It builds a SetRequest with the
// given conversation/delay/prompt and an empty (default/unscoped) tenant+agent.
func setReq(convID string, delay time.Duration, prompt string) SetRequest {
	return SetRequest{ConversationID: convID, Delay: delay, Prompt: prompt}
}

type startRunCall struct {
	Prompt         string
	ConversationID string
	TenantID       string
	AgentID        string
}

func (m *mockRunStarter) StartRun(prompt, conversationID, tenantID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, startRunCall{
		Prompt:         prompt,
		ConversationID: conversationID,
		TenantID:       tenantID,
		AgentID:        agentID,
	})
	if m.startFn != nil {
		return m.startFn(prompt, conversationID, tenantID, agentID)
	}
	return m.err
}

func (m *mockRunStarter) getCalls() []startRunCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]startRunCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// --- CallbackManager Tests ---

func TestCallbackManagerSet(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 10*time.Second, "check status"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.ID == "" {
			t.Fatal("expected non-empty ID")
		}
		if info.ConversationID != "conv-1" {
			t.Errorf("expected conv-1, got %s", info.ConversationID)
		}
		if info.State != CallbackStatePending {
			t.Errorf("expected pending, got %s", info.State)
		}
		if info.Prompt != "check status" {
			t.Errorf("expected 'check status', got %s", info.Prompt)
		}
		if info.Delay != "10s" {
			t.Errorf("expected '10s', got %s", info.Delay)
		}
	})

	t.Run("delay too short", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", 1*time.Second, "check"))
		if err == nil {
			t.Fatal("expected error for short delay")
		}
	})

	t.Run("delay too long", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", 2*time.Hour, "check"))
		if err == nil {
			t.Fatal("expected error for long delay")
		}
	})

	t.Run("empty prompt", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", 10*time.Second, ""))
		if err == nil {
			t.Fatal("expected error for empty prompt")
		}
	})

	t.Run("max callbacks per conversation", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		for i := 0; i < MaxCallbacksPerConv; i++ {
			_, err := mgr.Set(setReq("conv-1", 30*time.Second, fmt.Sprintf("check %d", i)))
			if err != nil {
				t.Fatalf("unexpected error on callback %d: %v", i, err)
			}
		}

		_, err := mgr.Set(setReq("conv-1", 30*time.Second, "one too many"))
		if err == nil {
			t.Fatal("expected error exceeding max callbacks")
		}
	})

	t.Run("max callbacks per conversation does not affect other conversations", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		for i := 0; i < MaxCallbacksPerConv; i++ {
			_, err := mgr.Set(setReq("conv-1", 30*time.Second, fmt.Sprintf("check %d", i)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}

		// Different conversation should still work
		_, err := mgr.Set(setReq("conv-2", 30*time.Second, "check"))
		if err != nil {
			t.Fatalf("unexpected error for different conversation: %v", err)
		}
	})

	t.Run("set after shutdown", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", 10*time.Second, "check"))
		if err == nil {
			t.Fatal("expected error after shutdown")
		}
	})
}

func TestCallbackManagerCancel(t *testing.T) {
	t.Run("cancel pending", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		canceled, err := mgr.Cancel(info.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if canceled.State != CallbackStateCanceled {
			t.Errorf("expected canceled, got %s", canceled.State)
		}
	})

	t.Run("cancel nonexistent", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Cancel("nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent callback")
		}
	})

	t.Run("cancel already canceled", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.Cancel(info.ID)

		_, err := mgr.Cancel(info.ID)
		if err == nil {
			t.Fatal("expected error for already canceled callback")
		}
	})
}

func TestCallbackManagerList(t *testing.T) {
	t.Run("list empty", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		callbacks := mgr.List("conv-1")
		if len(callbacks) != 0 {
			t.Errorf("expected empty list, got %d", len(callbacks))
		}
	})

	t.Run("list multiple", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		mgr.Set(setReq("conv-1", 10*time.Second, "check 1"))
		mgr.Set(setReq("conv-1", 20*time.Second, "check 2"))
		mgr.Set(setReq("conv-2", 10*time.Second, "check 3")) // different conv

		callbacks := mgr.List("conv-1")
		if len(callbacks) != 2 {
			t.Errorf("expected 2 callbacks, got %d", len(callbacks))
		}

		callbacks2 := mgr.List("conv-2")
		if len(callbacks2) != 1 {
			t.Errorf("expected 1 callback, got %d", len(callbacks2))
		}
	})
}

func TestCallbackManagerListAll(t *testing.T) {
	t.Run("empty manager returns empty", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		callbacks := mgr.ListAll()
		if len(callbacks) != 0 {
			t.Errorf("expected empty list, got %d", len(callbacks))
		}
	})

	t.Run("returns pending callbacks across conversations", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info1, err := mgr.Set(setReq("conv-1", 10*time.Second, "check 1"))
		if err != nil {
			t.Fatalf("Set: %v", err)
		}
		info2, err := mgr.Set(setReq("conv-2", 20*time.Second, "check 2"))
		if err != nil {
			t.Fatalf("Set: %v", err)
		}
		if _, err := mgr.Set(setReq("conv-2", 30*time.Second, "check 3")); err != nil {
			t.Fatalf("Set: %v", err)
		}

		callbacks := mgr.ListAll()
		if len(callbacks) != 3 {
			t.Fatalf("expected 3 callbacks across conversations, got %d", len(callbacks))
		}
		byID := make(map[string]CallbackInfo, len(callbacks))
		for _, cb := range callbacks {
			byID[cb.ID] = cb
		}
		for _, want := range []CallbackInfo{info1, info2} {
			got, ok := byID[want.ID]
			if !ok {
				t.Fatalf("ListAll missing callback %s", want.ID)
			}
			if got.ConversationID != want.ConversationID || got.Prompt != want.Prompt || got.State != CallbackStatePending {
				t.Errorf("callback %s = %+v, want conversation %q prompt %q state pending", want.ID, got, want.ConversationID, want.Prompt)
			}
		}
	})

	t.Run("excludes fired and canceled callbacks", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		keep, err := mgr.Set(setReq("conv-1", time.Hour, "keep"))
		if err != nil {
			t.Fatalf("Set: %v", err)
		}
		canceled, err := mgr.Set(setReq("conv-1", time.Hour, "cancel me"))
		if err != nil {
			t.Fatalf("Set: %v", err)
		}
		fired, err := mgr.Set(setReq("conv-2", 5*time.Second, "fire me"))
		if err != nil {
			t.Fatalf("Set: %v", err)
		}

		if _, err := mgr.Cancel(canceled.ID); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		mgr.fire(fired.ID)

		callbacks := mgr.ListAll()
		if len(callbacks) != 1 {
			t.Fatalf("expected only the pending callback, got %d: %+v", len(callbacks), callbacks)
		}
		if callbacks[0].ID != keep.ID {
			t.Errorf("remaining callback = %s, want %s", callbacks[0].ID, keep.ID)
		}
	})
}

func TestCallbackManagerFire(t *testing.T) {
	t.Run("fire calls StartRun", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		mgr.now = func() time.Time { return time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC) }
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 5*time.Second, "check deployment"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Cancel the real timer, we'll fire manually
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		// Fire manually
		mgr.fire(info.ID)

		calls := starter.getCalls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 StartRun call, got %d", len(calls))
		}
		if calls[0].Prompt != "check deployment" {
			t.Errorf("expected prompt 'check deployment', got %s", calls[0].Prompt)
		}
		if calls[0].ConversationID != "conv-1" {
			t.Errorf("expected conv-1, got %s", calls[0].ConversationID)
		}

		// Verify state is fired
		mgr.mu.Lock()
		cb := mgr.callbacks[info.ID]
		mgr.mu.Unlock()
		if cb.info.State != CallbackStateFired {
			t.Errorf("expected fired, got %s", cb.info.State)
		}
	})

	t.Run("fire with StartRun error still marks as fired", func(t *testing.T) {
		starter := &mockRunStarter{err: fmt.Errorf("runner busy")}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 5*time.Second, "check"))

		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)

		mgr.mu.Lock()
		cb := mgr.callbacks[info.ID]
		mgr.mu.Unlock()
		if cb.info.State != CallbackStateFired {
			t.Errorf("expected fired even after error, got %s", cb.info.State)
		}
	})

	t.Run("fire already fired is no-op", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 5*time.Second, "check"))

		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)
		mgr.fire(info.ID) // second fire should be no-op

		calls := starter.getCalls()
		if len(calls) != 1 {
			t.Errorf("expected 1 call, got %d", len(calls))
		}
	})

	t.Run("cancel already fired", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 5*time.Second, "check"))

		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)

		_, err := mgr.Cancel(info.ID)
		if err == nil {
			t.Fatal("expected error canceling fired callback")
		}
	})

	t.Run("fire via timer integration", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		// Use minimum delay so it fires quickly
		_, err := mgr.Set(setReq("conv-1", 5*time.Second, "check"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Wait for it to fire (with timeout)
		deadline := time.After(10 * time.Second)
		for {
			calls := starter.getCalls()
			if len(calls) >= 1 {
				if calls[0].Prompt != "check" {
					t.Errorf("expected 'check', got %s", calls[0].Prompt)
				}
				break
			}
			select {
			case <-deadline:
				t.Fatal("timed out waiting for callback to fire")
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
	})
}

func TestCallbackManagerShutdown(t *testing.T) {
	t.Run("shutdown cancels pending", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)

		mgr.Set(setReq("conv-1", 30*time.Second, "check 1"))
		mgr.Set(setReq("conv-1", 30*time.Second, "check 2"))

		mgr.Shutdown()

		callbacks := mgr.List("conv-1")
		for _, cb := range callbacks {
			if cb.State != CallbackStateCanceled {
				t.Errorf("expected all canceled after shutdown, got %s for %s", cb.State, cb.ID)
			}
		}
	})

	t.Run("shutdown is idempotent", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		mgr.Shutdown()
		mgr.Shutdown() // should not panic
	})
}

func TestCallbackManagerConcurrent(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	var wg sync.WaitGroup
	var setCount int32

	// Concurrent sets
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			convID := fmt.Sprintf("conv-%d", i%3)
			_, err := mgr.Set(setReq(convID, 30*time.Second, fmt.Sprintf("check %d", i)))
			if err == nil {
				atomic.AddInt32(&setCount, 1)
			}
		}(i)
	}

	// Concurrent lists
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mgr.List(fmt.Sprintf("conv-%d", i%3))
		}(i)
	}

	wg.Wait()

	if setCount == 0 {
		t.Error("expected some successful sets")
	}
}

// --- Tool Handler Tests ---

func testContextWithConversation(convID string) context.Context {
	return context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{
		ConversationID: convID,
	})
}

func TestSetDelayedCallbackTool(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := setDelayedCallbackTool(mgr)
		if tool.Definition.Name != "set_delayed_callback" {
			t.Errorf("expected name set_delayed_callback, got %s", tool.Definition.Name)
		}

		ctx := testContextWithConversation("conv-1")
		args, _ := json.Marshal(map[string]string{"delay": "30s", "prompt": "check deploy"})
		result, err := tool.Handler(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var info CallbackInfo
		if err := json.Unmarshal([]byte(result), &info); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		if info.State != CallbackStatePending {
			t.Errorf("expected pending, got %s", info.State)
		}
		if info.ConversationID != "conv-1" {
			t.Errorf("expected conv-1, got %s", info.ConversationID)
		}
	})

	t.Run("invalid delay format", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := setDelayedCallbackTool(mgr)
		ctx := testContextWithConversation("conv-1")
		args, _ := json.Marshal(map[string]string{"delay": "not-a-duration", "prompt": "check"})
		_, err := tool.Handler(ctx, args)
		if err == nil {
			t.Fatal("expected error for invalid delay")
		}
	})

	t.Run("no run metadata", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := setDelayedCallbackTool(mgr)
		args, _ := json.Marshal(map[string]string{"delay": "30s", "prompt": "check"})
		_, err := tool.Handler(context.Background(), args)
		if err == nil {
			t.Fatal("expected error for missing run metadata")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := setDelayedCallbackTool(mgr)
		ctx := testContextWithConversation("conv-1")
		_, err := tool.Handler(ctx, json.RawMessage(`{invalid`))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestCancelDelayedCallbackTool(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))

		tool := cancelDelayedCallbackTool(mgr)
		ctx := testContextWithConversation("conv-1")
		args, _ := json.Marshal(map[string]string{"callback_id": info.ID})
		result, err := tool.Handler(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var canceled CallbackInfo
		json.Unmarshal([]byte(result), &canceled)
		if canceled.State != CallbackStateCanceled {
			t.Errorf("expected canceled, got %s", canceled.State)
		}
	})

	t.Run("cancel nonexistent", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := cancelDelayedCallbackTool(mgr)
		ctx := testContextWithConversation("conv-1")
		args, _ := json.Marshal(map[string]string{"callback_id": "nonexistent"})
		_, err := tool.Handler(ctx, args)
		if err == nil {
			t.Fatal("expected error for nonexistent callback")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := cancelDelayedCallbackTool(mgr)
		ctx := testContextWithConversation("conv-1")
		_, err := tool.Handler(ctx, json.RawMessage(`{bad`))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestListDelayedCallbacksTool(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		mgr.Set(setReq("conv-1", 10*time.Second, "check 1"))
		mgr.Set(setReq("conv-1", 20*time.Second, "check 2"))

		tool := listDelayedCallbacksTool(mgr)
		ctx := testContextWithConversation("conv-1")
		result, err := tool.Handler(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var callbacks []CallbackInfo
		json.Unmarshal([]byte(result), &callbacks)
		if len(callbacks) != 2 {
			t.Errorf("expected 2 callbacks, got %d", len(callbacks))
		}
	})

	t.Run("no run metadata", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tool := listDelayedCallbacksTool(mgr)
		_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
		if err == nil {
			t.Fatal("expected error for missing run metadata")
		}
	})
}

// --- Regression Tests ---

// ============================================================
// Category 1: Concurrency Safety
// ============================================================

func TestRegression_ConcurrentFireAndCancel(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	info, err := mgr.Set(setReq("conv-1", 10*time.Second, "check deploy"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stop the real timer so it doesn't fire on its own
	mgr.mu.Lock()
	mgr.callbacks[info.ID].timer.Stop()
	mgr.mu.Unlock()

	// Race fire() against Cancel() concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		mgr.fire(info.ID)
	}()

	go func() {
		defer wg.Done()
		mgr.Cancel(info.ID) // error is expected if fire wins
	}()

	wg.Wait()

	// State must be consistent: either fired or canceled, never something else
	mgr.mu.Lock()
	state := mgr.callbacks[info.ID].info.State
	mgr.mu.Unlock()

	if state != CallbackStateFired && state != CallbackStateCanceled {
		t.Errorf("expected fired or canceled, got %s", state)
	}
}

func TestRegression_ConcurrentShutdownAndSet(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)

	var wg sync.WaitGroup
	var successCount int32
	var errCount int32

	// Spawn 50 goroutines that call Set()
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := mgr.Set(setReq("conv-1", 30*time.Second, fmt.Sprintf("check %d", i)))
			if err != nil {
				atomic.AddInt32(&errCount, 1)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	// Concurrently call Shutdown()
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.Shutdown()
	}()

	wg.Wait()

	// After shutdown completes, every callback should be canceled
	mgr.mu.Lock()
	for _, cb := range mgr.callbacks {
		if cb.info.State != CallbackStateCanceled {
			t.Errorf("expected all callbacks canceled after shutdown, got %s for %s", cb.info.State, cb.info.ID)
		}
	}
	mgr.mu.Unlock()

	// Some sets should have succeeded (before shutdown) and some may have failed (after shutdown)
	total := atomic.LoadInt32(&successCount) + atomic.LoadInt32(&errCount)
	if total != 50 {
		t.Errorf("expected 50 total outcomes, got %d", total)
	}
}

func TestRegression_HighConcurrency(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	var wg sync.WaitGroup
	const goroutines = 200
	const conversations = 10

	// Collect IDs from successful sets for cancel/fire operations
	var idMu sync.Mutex
	var ids []string

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			convID := fmt.Sprintf("conv-%d", i%conversations)

			switch i % 4 {
			case 0: // Set
				info, err := mgr.Set(setReq(convID, 30*time.Second, fmt.Sprintf("check %d", i)))
				if err == nil {
					idMu.Lock()
					ids = append(ids, info.ID)
					idMu.Unlock()
				}
			case 1: // Cancel (might fail if ID doesn't exist yet)
				idMu.Lock()
				var id string
				if len(ids) > 0 {
					id = ids[len(ids)-1]
				}
				idMu.Unlock()
				if id != "" {
					mgr.Cancel(id) // error OK
				}
			case 2: // List
				mgr.List(convID)
			case 3: // Fire (might be no-op if ID doesn't exist)
				idMu.Lock()
				var id string
				if len(ids) > 0 {
					id = ids[len(ids)-1]
				}
				idMu.Unlock()
				if id != "" {
					// Stop the timer first, then fire manually
					mgr.mu.Lock()
					cb, ok := mgr.callbacks[id]
					if ok {
						cb.timer.Stop()
					}
					mgr.mu.Unlock()
					if ok {
						mgr.fire(id)
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify state consistency: every callback should be in a valid state
	mgr.mu.Lock()
	for id, cb := range mgr.callbacks {
		switch cb.info.State {
		case CallbackStatePending, CallbackStateFired, CallbackStateCanceled:
			// valid
		default:
			t.Errorf("callback %s has invalid state: %s", id, cb.info.State)
		}
	}
	mgr.mu.Unlock()
}

// ============================================================
// Category 2: Error Path Coverage
// ============================================================

func TestRegression_FireWithSlowStartRun(t *testing.T) {
	starter := &mockRunStarter{
		startFn: func(prompt, convID, tenantID, agentID string) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		},
	}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	const count = 5
	infos := make([]CallbackInfo, count)
	for i := 0; i < count; i++ {
		info, err := mgr.Set(setReq("conv-1", 30*time.Second, fmt.Sprintf("check %d", i)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		infos[i] = info

		// Stop the real timer
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
	}

	// Fire all concurrently — each fire() releases the lock before calling StartRun,
	// so they shouldn't block each other on the manager lock.
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			mgr.fire(id)
		}(infos[i].ID)
	}
	wg.Wait()

	// Verify all fired
	calls := starter.getCalls()
	if len(calls) != count {
		t.Errorf("expected %d StartRun calls, got %d", count, len(calls))
	}

	// Verify all states are fired
	for _, info := range infos {
		mgr.mu.Lock()
		state := mgr.callbacks[info.ID].info.State
		mgr.mu.Unlock()
		if state != CallbackStateFired {
			t.Errorf("callback %s expected fired, got %s", info.ID, state)
		}
	}
}

func TestRegression_CancelDuringFire(t *testing.T) {
	// StartRun that takes 200ms, giving us time to attempt Cancel
	fireDone := make(chan struct{})
	starter := &mockRunStarter{
		startFn: func(prompt, convID, tenantID, agentID string) error {
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	info, err := mgr.Set(setReq("conv-1", 30*time.Second, "check deploy"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	mgr.callbacks[info.ID].timer.Stop()
	mgr.mu.Unlock()

	// Start fire in goroutine
	go func() {
		mgr.fire(info.ID)
		close(fireDone)
	}()

	// Give fire() a moment to acquire lock and set state to "fired"
	time.Sleep(10 * time.Millisecond)

	// Now try to cancel — should fail because fire() already marked state as fired
	_, cancelErr := mgr.Cancel(info.ID)
	if cancelErr == nil {
		t.Error("expected error canceling a callback that is being fired")
	}

	<-fireDone

	// Confirm final state is fired
	mgr.mu.Lock()
	state := mgr.callbacks[info.ID].info.State
	mgr.mu.Unlock()
	if state != CallbackStateFired {
		t.Errorf("expected fired, got %s", state)
	}
}

// ============================================================
// Category 3: Boundary Conditions
// ============================================================

func TestRegression_ExactBoundaryDelays(t *testing.T) {
	starter := &mockRunStarter{}

	t.Run("exact minimum delay succeeds", func(t *testing.T) {
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", MinCallbackDelay, "check"))
		if err != nil {
			t.Errorf("expected success at exact minimum delay, got: %v", err)
		}
	})

	t.Run("exact maximum delay succeeds", func(t *testing.T) {
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", MaxCallbackDelay, "check"))
		if err != nil {
			t.Errorf("expected success at exact maximum delay, got: %v", err)
		}

		// Stop the long timer immediately to avoid leaked goroutine
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
	})

	t.Run("one nanosecond below minimum fails", func(t *testing.T) {
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", MinCallbackDelay-1*time.Nanosecond, "check"))
		if err == nil {
			t.Error("expected error for delay just below minimum")
		}
	})

	t.Run("one nanosecond above maximum fails", func(t *testing.T) {
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		_, err := mgr.Set(setReq("conv-1", MaxCallbackDelay+1*time.Nanosecond, "check"))
		if err == nil {
			t.Error("expected error for delay just above maximum")
		}
	})
}

func TestRegression_EmptyConversationID(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	// Empty conversation ID should still work — no validation on conv ID
	info, err := mgr.Set(setReq("", 10*time.Second, "check"))
	if err != nil {
		t.Fatalf("expected empty conv ID to work, got error: %v", err)
	}

	if info.ConversationID != "" {
		t.Errorf("expected empty conv ID, got %q", info.ConversationID)
	}

	// List should return the callback under empty conv ID
	callbacks := mgr.List("")
	if len(callbacks) != 1 {
		t.Errorf("expected 1 callback for empty conv ID, got %d", len(callbacks))
	}

	// Fire should pass empty conv ID to StartRun
	mgr.mu.Lock()
	mgr.callbacks[info.ID].timer.Stop()
	mgr.mu.Unlock()

	mgr.fire(info.ID)

	calls := starter.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 StartRun call, got %d", len(calls))
	}
	if calls[0].ConversationID != "" {
		t.Errorf("expected empty conv ID in StartRun, got %q", calls[0].ConversationID)
	}
}

func TestRegression_MaxCallbacksBoundary(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	// Set exactly MaxCallbacksPerConv callbacks — should all succeed.
	ids := make([]string, MaxCallbacksPerConv)
	for i := 0; i < MaxCallbacksPerConv; i++ {
		info, err := mgr.Set(setReq("conv-1", 30*time.Second, fmt.Sprintf("check %d", i)))
		if err != nil {
			t.Fatalf("unexpected error on callback %d: %v", i, err)
		}
		ids[i] = info.ID
	}

	// One more should fail — slot is full.
	_, err := mgr.Set(setReq("conv-1", 30*time.Second, "one too many"))
	if err == nil {
		t.Fatal("expected error exceeding max callbacks")
	}

	// Cancel one callback — this must free the slot.
	_, err = mgr.Cancel(ids[0])
	if err != nil {
		t.Fatalf("unexpected cancel error: %v", err)
	}

	// After canceling, a new Set for the same conversation MUST succeed (slot freed).
	_, err = mgr.Set(setReq("conv-1", 30*time.Second, "after cancel"))
	if err != nil {
		t.Fatalf("expected Set to succeed after cancel freed a slot, got: %v", err)
	}

	// Fire one of the remaining pending callbacks manually and verify slot is freed.
	// ids[1] is still pending — stop its real timer then fire it directly.
	mgr.mu.Lock()
	mgr.callbacks[ids[1]].timer.Stop()
	mgr.mu.Unlock()
	mgr.fire(ids[1])

	// After firing, another new Set must also succeed.
	_, err = mgr.Set(setReq("conv-1", 30*time.Second, "after fire"))
	if err != nil {
		t.Fatalf("expected Set to succeed after fire freed a slot, got: %v", err)
	}

	// List must still show only non-pruned entries for this conversation.
	listed := mgr.List("conv-1")
	// ids[0] was canceled (pruned from byConv), ids[1] was fired (pruned from byConv);
	// remaining entries are ids[2..9] (8 pending) + "after cancel" + "after fire" = 10 total.
	if len(listed) != MaxCallbacksPerConv {
		t.Errorf("expected List to return %d callbacks after prune, got %d", MaxCallbacksPerConv, len(listed))
	}
}

// ============================================================
// Category 4: Integration Seams
// ============================================================

func TestRegression_MultiConversationConcurrentFire(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	const convCount = 5
	type convCallback struct {
		convID string
		prompt string
		cbID   string
	}
	cbs := make([]convCallback, convCount)

	for i := 0; i < convCount; i++ {
		convID := fmt.Sprintf("conv-%d", i)
		prompt := fmt.Sprintf("check deployment %d", i)
		info, err := mgr.Set(setReq(convID, 10*time.Second, prompt))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cbs[i] = convCallback{convID: convID, prompt: prompt, cbID: info.ID}

		// Stop real timer
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
	}

	// Fire all concurrently
	var wg sync.WaitGroup
	for i := 0; i < convCount; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			mgr.fire(id)
		}(cbs[i].cbID)
	}
	wg.Wait()

	// Verify each StartRun call has correct conversation ID and prompt pairing
	calls := starter.getCalls()
	if len(calls) != convCount {
		t.Fatalf("expected %d StartRun calls, got %d", convCount, len(calls))
	}

	// Build a set of expected (prompt, convID) pairs
	expected := make(map[string]string)
	for _, cb := range cbs {
		expected[cb.prompt] = cb.convID
	}

	for _, call := range calls {
		expectedConv, ok := expected[call.Prompt]
		if !ok {
			t.Errorf("unexpected prompt in StartRun call: %s", call.Prompt)
			continue
		}
		if call.ConversationID != expectedConv {
			t.Errorf("for prompt %q: expected conv %s, got %s", call.Prompt, expectedConv, call.ConversationID)
		}
		delete(expected, call.Prompt) // mark as seen
	}

	if len(expected) > 0 {
		for prompt := range expected {
			t.Errorf("missing StartRun call for prompt: %s", prompt)
		}
	}
}

func TestRegression_ListDuringFire(t *testing.T) {
	// Use slow StartRun so fire() is still in progress when we call List()
	starter := &mockRunStarter{
		startFn: func(prompt, convID, tenantID, agentID string) error {
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	info, err := mgr.Set(setReq("conv-1", 30*time.Second, "check deploy"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	mgr.callbacks[info.ID].timer.Stop()
	mgr.mu.Unlock()

	fireDone := make(chan struct{})
	go func() {
		mgr.fire(info.ID)
		close(fireDone)
	}()

	// Wait a bit for fire() to acquire lock and set state
	time.Sleep(20 * time.Millisecond)

	// P3 (BUG A fix): fire() removes the id from byConv before releasing the
	// lock, so List returns 0 entries — the slot is freed immediately.
	callbacks := mgr.List("conv-1")
	if len(callbacks) != 0 {
		t.Fatalf("expected 0 callbacks after fire() removed from byConv, got %d", len(callbacks))
	}

	// The callbacks map entry is still present with state "fired" (for state
	// querying) until it is explicitly garbage-collected in a future pass.
	mgr.mu.Lock()
	cb := mgr.callbacks[info.ID]
	mgr.mu.Unlock()
	if cb == nil {
		t.Fatal("callbacks map entry should still exist after fire")
	}
	if cb.info.State != CallbackStateFired {
		t.Errorf("expected fired state in callbacks map, got %s", cb.info.State)
	}

	<-fireDone
}

func TestRegression_ToolHandlerConversationIsolation(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)
	defer mgr.Shutdown()

	// Set callback via tool handler on conv-1
	setTool := setDelayedCallbackTool(mgr)
	ctx1 := testContextWithConversation("conv-1")
	args, _ := json.Marshal(map[string]string{"delay": "30s", "prompt": "check deploy"})
	_, err := setTool.Handler(ctx1, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// List via tool handler on conv-2 — should see nothing
	listTool := listDelayedCallbacksTool(mgr)
	ctx2 := testContextWithConversation("conv-2")
	result, err := listTool.Handler(ctx2, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var callbacks []CallbackInfo
	if err := json.Unmarshal([]byte(result), &callbacks); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(callbacks) != 0 {
		t.Errorf("expected 0 callbacks for conv-2, got %d", len(callbacks))
	}

	// List via tool handler on conv-1 — should see 1
	result1, err := listTool.Handler(ctx1, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var callbacks1 []CallbackInfo
	if err := json.Unmarshal([]byte(result1), &callbacks1); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(callbacks1) != 1 {
		t.Errorf("expected 1 callback for conv-1, got %d", len(callbacks1))
	}
}

// ============================================================
// Category 5: Constraint Enforcement
// ============================================================

func TestRegression_StateTransitions(t *testing.T) {
	t.Run("pending to fired via fire()", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)

		mgr.mu.Lock()
		state := mgr.callbacks[info.ID].info.State
		mgr.mu.Unlock()
		if state != CallbackStateFired {
			t.Errorf("expected fired, got %s", state)
		}
	})

	t.Run("pending to canceled via Cancel()", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		canceled, err := mgr.Cancel(info.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if canceled.State != CallbackStateCanceled {
			t.Errorf("expected canceled, got %s", canceled.State)
		}
	})

	t.Run("fired then cancel attempt returns error", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)

		_, err := mgr.Cancel(info.ID)
		if err == nil {
			t.Fatal("expected error canceling fired callback")
		}
	})

	t.Run("canceled then cancel attempt returns error", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.Cancel(info.ID)

		_, err := mgr.Cancel(info.ID)
		if err == nil {
			t.Fatal("expected error canceling already-canceled callback")
		}
	})

	t.Run("fired then fire attempt is no-op", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.fire(info.ID)
		mgr.fire(info.ID) // should be no-op

		calls := starter.getCalls()
		if len(calls) != 1 {
			t.Errorf("expected exactly 1 StartRun call, got %d", len(calls))
		}
	})

	t.Run("canceled then fire attempt is no-op", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, _ := mgr.Set(setReq("conv-1", 30*time.Second, "check"))
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()

		mgr.Cancel(info.ID)
		mgr.fire(info.ID) // should be no-op since state is canceled

		calls := starter.getCalls()
		if len(calls) != 0 {
			t.Errorf("expected 0 StartRun calls after firing canceled callback, got %d", len(calls))
		}
	})
}

// ============================================================
// Category 6: Graceful Degradation
// ============================================================

func TestRegression_ShutdownDuringActiveFire(t *testing.T) {
	fireDone := make(chan struct{})
	startRunStarted := make(chan struct{})

	starter := &mockRunStarter{
		startFn: func(prompt, convID, tenantID, agentID string) error {
			close(startRunStarted) // signal that fire() is now inside StartRun
			time.Sleep(500 * time.Millisecond)
			return nil
		},
	}
	mgr := NewCallbackManager(starter)

	// Create one callback that will be fired (with slow StartRun)
	firedInfo, err := mgr.Set(setReq("conv-1", 30*time.Second, "fired callback"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create another callback that should be canceled by shutdown
	pendingInfo, err := mgr.Set(setReq("conv-1", 30*time.Second, "pending callback"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stop timers to control manually
	mgr.mu.Lock()
	mgr.callbacks[firedInfo.ID].timer.Stop()
	mgr.callbacks[pendingInfo.ID].timer.Stop()
	mgr.mu.Unlock()

	// Start fire in goroutine
	go func() {
		mgr.fire(firedInfo.ID)
		close(fireDone)
	}()

	// Wait until StartRun is actually in progress
	<-startRunStarted

	// Now call Shutdown while fire() is still in StartRun
	mgr.Shutdown()

	// Wait for fire to complete
	<-fireDone

	// Verify: the fired callback should stay "fired" (not overwritten to canceled)
	mgr.mu.Lock()
	firedState := mgr.callbacks[firedInfo.ID].info.State
	pendingState := mgr.callbacks[pendingInfo.ID].info.State
	mgr.mu.Unlock()

	if firedState != CallbackStateFired {
		t.Errorf("fired callback should stay fired, got %s", firedState)
	}
	if pendingState != CallbackStateCanceled {
		t.Errorf("pending callback should be canceled by shutdown, got %s", pendingState)
	}
}

func TestRegression_ShutdownWithManyCallbacks(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)

	const convCount = 10
	const perConv = MaxCallbacksPerConv // 10 each = 100 total

	for c := 0; c < convCount; c++ {
		convID := fmt.Sprintf("conv-%d", c)
		for i := 0; i < perConv; i++ {
			_, err := mgr.Set(setReq(convID, 30*time.Second, fmt.Sprintf("check %d-%d", c, i)))
			if err != nil {
				t.Fatalf("unexpected error on conv-%d callback %d: %v", c, i, err)
			}
		}
	}

	// Time the shutdown — it should complete promptly
	start := time.Now()
	mgr.Shutdown()
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("shutdown took too long: %v", elapsed)
	}

	// Verify all callbacks are canceled
	mgr.mu.Lock()
	for id, cb := range mgr.callbacks {
		if cb.info.State != CallbackStateCanceled {
			t.Errorf("callback %s expected canceled, got %s", id, cb.info.State)
		}
	}
	totalCallbacks := len(mgr.callbacks)
	mgr.mu.Unlock()

	expected := convCount * perConv
	if totalCallbacks != expected {
		t.Errorf("expected %d total callbacks, got %d", expected, totalCallbacks)
	}
}

func TestRegression_ResourceExhaustion(t *testing.T) {
	starter := &mockRunStarter{}
	mgr := NewCallbackManager(starter)

	const convCount = 100
	const perConv = MaxCallbacksPerConv // 10 each = 1000 total

	start := time.Now()
	for c := 0; c < convCount; c++ {
		convID := fmt.Sprintf("conv-%d", c)
		for i := 0; i < perConv; i++ {
			_, err := mgr.Set(setReq(convID, 30*time.Second, fmt.Sprintf("check %d-%d", c, i)))
			if err != nil {
				t.Fatalf("unexpected error on conv-%d callback %d: %v", c, i, err)
			}
		}
	}
	setupElapsed := time.Since(start)

	if setupElapsed > 5*time.Second {
		t.Errorf("creating 1000 callbacks took too long: %v", setupElapsed)
	}

	// Verify all can be listed
	totalListed := 0
	for c := 0; c < convCount; c++ {
		callbacks := mgr.List(fmt.Sprintf("conv-%d", c))
		totalListed += len(callbacks)
	}
	if totalListed != convCount*perConv {
		t.Errorf("expected %d total listed callbacks, got %d", convCount*perConv, totalListed)
	}

	// Shutdown should clean them all
	shutdownStart := time.Now()
	mgr.Shutdown()
	shutdownElapsed := time.Since(shutdownStart)

	if shutdownElapsed > 2*time.Second {
		t.Errorf("shutdown of 1000 callbacks took too long: %v", shutdownElapsed)
	}

	// Verify all are canceled
	mgr.mu.Lock()
	for id, cb := range mgr.callbacks {
		if cb.info.State != CallbackStateCanceled {
			t.Errorf("callback %s expected canceled after shutdown, got %s", id, cb.info.State)
		}
	}
	mgr.mu.Unlock()
}

// --- Catalog Integration Tests ---

func TestDelayedCallbackCatalogIntegration(t *testing.T) {
	t.Run("callback tools included when enabled", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		tools, err := BuildCatalog(BuildOptions{
			WorkspaceRoot:   t.TempDir(),
			EnableCallbacks: true,
			CallbackManager: mgr,
		})
		if err != nil {
			t.Fatalf("BuildCatalog error: %v", err)
		}

		names := make(map[string]bool)
		for _, tool := range tools {
			names[tool.Definition.Name] = true
		}

		expected := []string{"set_delayed_callback", "cancel_delayed_callback", "list_delayed_callbacks"}
		for _, name := range expected {
			if !names[name] {
				t.Errorf("expected tool %s in catalog", name)
			}
		}
	})

	t.Run("callback tools excluded when disabled", func(t *testing.T) {
		tools, err := BuildCatalog(BuildOptions{
			WorkspaceRoot:   t.TempDir(),
			EnableCallbacks: false,
		})
		if err != nil {
			t.Fatalf("BuildCatalog error: %v", err)
		}

		for _, tool := range tools {
			if tool.Definition.Name == "set_delayed_callback" ||
				tool.Definition.Name == "cancel_delayed_callback" ||
				tool.Definition.Name == "list_delayed_callbacks" {
				t.Errorf("unexpected callback tool %s in catalog when disabled", tool.Definition.Name)
			}
		}
	})

	t.Run("callback tools excluded when manager nil", func(t *testing.T) {
		tools, err := BuildCatalog(BuildOptions{
			WorkspaceRoot:   t.TempDir(),
			EnableCallbacks: true,
			CallbackManager: nil,
		})
		if err != nil {
			t.Fatalf("BuildCatalog error: %v", err)
		}

		for _, tool := range tools {
			if tool.Definition.Name == "set_delayed_callback" {
				t.Error("unexpected set_delayed_callback tool when manager is nil")
			}
		}
	})
}

// --- T5: tenant + agent threaded through fire -> StartRun ---

// testContextWithScope builds a tool context carrying the full run scope
// (tenant + conversation + agent) so callbacks scheduled inside a tenant-scoped
// conversation can be exercised end to end.
func testContextWithScope(tenantID, convID, agentID string) context.Context {
	return context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{
		TenantID:       tenantID,
		ConversationID: convID,
		AgentID:        agentID,
	})
}

// TestCallbackFireCarriesTenantAndAgent is the BUG B regression (T5): a callback
// scheduled from a tenant+agent-scoped conversation MUST fire its follow-up run
// on the SAME tenant and agent. Before the fix, fire() called StartRun with an
// empty tenant+agent, so a tenant-scoped conversation got access-denied at fire
// time — a direct autonomy breaker.
func TestCallbackFireCarriesTenantAndAgent(t *testing.T) {
	t.Run("fire via Set captures tenant+agent from run metadata", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		// Schedule the callback through the real tool handler so it reads the
		// tenant + agent from the originating run's metadata (the only place
		// they are available at Set time).
		tool := setDelayedCallbackTool(mgr)
		ctx := testContextWithScope("tenant-x", "conv-1", "agent-y")
		args, _ := json.Marshal(map[string]string{"delay": "5s", "prompt": "wake up"})
		result, err := tool.Handler(ctx, args)
		if err != nil {
			t.Fatalf("unexpected error scheduling callback: %v", err)
		}

		var info CallbackInfo
		if err := json.Unmarshal([]byte(result), &info); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}

		// Stop the real timer and fire directly.
		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
		mgr.fire(info.ID)

		calls := starter.getCalls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 StartRun call, got %d", len(calls))
		}
		if calls[0].TenantID != "tenant-x" {
			t.Errorf("StartRun tenant = %q, want tenant-x", calls[0].TenantID)
		}
		if calls[0].AgentID != "agent-y" {
			t.Errorf("StartRun agent = %q, want agent-y", calls[0].AgentID)
		}
		if calls[0].ConversationID != "conv-1" {
			t.Errorf("StartRun conv = %q, want conv-1", calls[0].ConversationID)
		}
		if calls[0].Prompt != "wake up" {
			t.Errorf("StartRun prompt = %q, want 'wake up'", calls[0].Prompt)
		}
	})

	t.Run("empty tenant+agent preserved for default case", func(t *testing.T) {
		starter := &mockRunStarter{}
		mgr := NewCallbackManager(starter)
		defer mgr.Shutdown()

		info, err := mgr.Set(setReq("conv-1", 5*time.Second, "check"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		mgr.mu.Lock()
		mgr.callbacks[info.ID].timer.Stop()
		mgr.mu.Unlock()
		mgr.fire(info.ID)

		calls := starter.getCalls()
		if len(calls) != 1 {
			t.Fatalf("expected 1 StartRun call, got %d", len(calls))
		}
		if calls[0].TenantID != "" {
			t.Errorf("StartRun tenant = %q, want empty", calls[0].TenantID)
		}
		if calls[0].AgentID != "" {
			t.Errorf("StartRun agent = %q, want empty", calls[0].AgentID)
		}
	})
}
