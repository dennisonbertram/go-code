package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestApprovalBrokerApproveLifecycle verifies the full pause/approve lifecycle:
// Ask() blocks until Approve() is called, then returns (true, nil).
func TestApprovalBrokerApproveLifecycle(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	resultCh := make(chan approvalBrokerResult, 1)

	go func() {
		approved, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run_1",
			CallID:  "call_1",
			Tool:    "bash",
			Args:    `{"command":"rm -rf /tmp/test"}`,
			Timeout: 2 * time.Second,
		})
		resultCh <- approvalBrokerResult{approved: approved, err: err}
	}()

	// Wait for pending to appear.
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := broker.Pending("run_1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending approval did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Check pending fields.
	pending, ok := broker.Pending("run_1")
	if !ok {
		t.Fatal("expected pending approval")
	}
	if pending.RunID != "run_1" {
		t.Errorf("pending RunID = %q, want %q", pending.RunID, "run_1")
	}
	if pending.CallID != "call_1" {
		t.Errorf("pending CallID = %q, want %q", pending.CallID, "call_1")
	}
	if pending.Tool != "bash" {
		t.Errorf("pending Tool = %q, want %q", pending.Tool, "bash")
	}

	// Approve the pending request.
	if err := broker.Approve("run_1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("Ask returned error: %v", res.err)
		}
		if !res.approved {
			t.Fatal("Ask returned approved=false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}

	// Pending should be gone after approval.
	if _, ok := broker.Pending("run_1"); ok {
		t.Fatal("expected no pending approval after Approve()")
	}
}

type approvalBrokerResult struct {
	approved bool
	err      error
}

// TestApprovalBrokerDenyLifecycle verifies that Deny() causes Ask() to return
// (false, nil) — the tool call is not executed but the run continues.
func TestApprovalBrokerDenyLifecycle(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	resultCh := make(chan approvalBrokerResult, 1)

	go func() {
		approved, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run_2",
			CallID:  "call_2",
			Tool:    "write",
			Args:    `{"path":"secret.txt","content":"data"}`,
			Timeout: 2 * time.Second,
		})
		resultCh <- approvalBrokerResult{approved: approved, err: err}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := broker.Pending("run_2"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending approval did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := broker.Deny("run_2"); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("Ask returned error: %v", res.err)
		}
		if res.approved {
			t.Fatal("Ask returned approved=true after Deny, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}
}

// TestApprovalBrokerTimeout verifies that Ask() returns an error when the
// deadline passes without an approve or deny.
func TestApprovalBrokerTimeout(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	_, err := broker.Ask(context.Background(), ApprovalRequest{
		RunID:   "run_timeout",
		CallID:  "call_timeout",
		Tool:    "bash",
		Args:    `{}`,
		Timeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !IsApprovalTimeout(err) {
		t.Errorf("expected ApprovalTimeoutError, got %T: %v", err, err)
	}
}

// TestApprovalBrokerContextCancellation verifies that Ask() returns ctx.Err()
// when the context is cancelled before an approve or deny arrives.
func TestApprovalBrokerContextCancellation(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := broker.Ask(ctx, ApprovalRequest{
			RunID:   "run_ctx",
			CallID:  "call_ctx",
			Tool:    "bash",
			Args:    `{}`,
			Timeout: 5 * time.Second,
		})
		errCh <- err
	}()

	// Wait for pending.
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := broker.Pending("run_ctx"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending approval did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ask to return after cancel")
	}
}

// TestApprovalBrokerRejectsDuplicate verifies that a second Ask() for the same
// run ID while one is already pending returns an error immediately.
func TestApprovalBrokerRejectsDuplicate(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_, _ = broker.Ask(ctx, ApprovalRequest{
			RunID:   "run_dup",
			CallID:  "call_1",
			Tool:    "bash",
			Args:    `{}`,
			Timeout: 2 * time.Second,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := broker.Pending("run_dup"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending approval did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}

	_, err := broker.Ask(context.Background(), ApprovalRequest{
		RunID:   "run_dup",
		CallID:  "call_2",
		Tool:    "write",
		Args:    `{}`,
		Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for duplicate pending, got nil")
	}
	if !strings.Contains(err.Error(), "pending approval already exists") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestApprovalBrokerApproveUnknownRun verifies that Approve() on a run with
// no pending approval returns ErrNoPendingApproval.
func TestApprovalBrokerApproveUnknownRun(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	err := broker.Approve("no_such_run")
	if !errors.Is(err, ErrNoPendingApproval) {
		t.Errorf("expected ErrNoPendingApproval, got %v", err)
	}
}

// TestApprovalBrokerDenyUnknownRun verifies that Deny() on a run with no
// pending approval returns ErrNoPendingApproval.
func TestApprovalBrokerDenyUnknownRun(t *testing.T) {
	t.Parallel()

	broker := NewInMemoryApprovalBroker()
	err := broker.Deny("no_such_run")
	if !errors.Is(err, ErrNoPendingApproval) {
		t.Errorf("expected ErrNoPendingApproval, got %v", err)
	}
}

// TestApprovalTimeoutError_Error verifies the Error() method formats the
// timeout message correctly (exercises the ApprovalTimeoutError.Error path
// required by the zero-coverage gate).
func TestApprovalTimeoutError_Error(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	e := &ApprovalTimeoutError{
		RunID:      "run_abc",
		CallID:     "call_xyz",
		DeadlineAt: deadline,
	}
	msg := e.Error()
	if !strings.Contains(msg, "run_abc") {
		t.Errorf("Error() missing run ID: %q", msg)
	}
	if !strings.Contains(msg, "call_xyz") {
		t.Errorf("Error() missing call ID: %q", msg)
	}
	if !strings.Contains(msg, "approval timeout") {
		t.Errorf("Error() missing 'approval timeout': %q", msg)
	}

	// IsApprovalTimeout should return true for this error.
	if !IsApprovalTimeout(e) {
		t.Error("IsApprovalTimeout should return true for *ApprovalTimeoutError")
	}
	if IsApprovalTimeout(errors.New("some other error")) {
		t.Error("IsApprovalTimeout should return false for non-timeout errors")
	}
}
