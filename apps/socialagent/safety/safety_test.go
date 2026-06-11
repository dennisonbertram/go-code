package safety_test

import (
	"context"
	"testing"

	"go-agent-harness/apps/socialagent/safety"
)

type mockChecker struct {
	result *safety.Result
	err    error
}

func (m *mockChecker) Check(ctx context.Context, message string) (*safety.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestCheckSafe(t *testing.T) {
	mock := &mockChecker{result: &safety.Result{Safe: true}}
	safe, category, err := safety.Check(context.Background(), mock, "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe {
		t.Error("expected safe=true, got false")
	}
	if category != "" {
		t.Errorf("expected empty category, got %q", category)
	}
}

func TestCheckUnsafe(t *testing.T) {
	mock := &mockChecker{result: &safety.Result{Safe: false, Category: "S2"}}
	safe, category, err := safety.Check(context.Background(), mock, "harmful")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if safe {
		t.Error("expected safe=false, got true")
	}
	if category != "S2" {
		t.Errorf("expected category='S2', got %q", category)
	}
}

func TestCheckUnsafeNoCategory(t *testing.T) {
	mock := &mockChecker{result: &safety.Result{Safe: false}}
	safe, category, err := safety.Check(context.Background(), mock, "bad")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if safe {
		t.Error("expected safe=false, got true")
	}
	if category != "" {
		t.Errorf("expected empty category, got %q", category)
	}
}

func TestCheckError(t *testing.T) {
	mock := &mockChecker{err: context.DeadlineExceeded}
	_, _, err := safety.Check(context.Background(), mock, "timeout")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRefusalTextIsNotEmpty(t *testing.T) {
	if safety.RefusalText == "" {
		t.Error("RefusalText must not be empty")
	}
}
