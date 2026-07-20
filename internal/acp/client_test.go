package acp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunsClientStartRun(t *testing.T) {
	t.Run("posts prompt with bearer auth and returns run id", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "test-key")

		runID, err := c.StartRun(context.Background(), "say hi")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if runID == "" {
			t.Fatal("StartRun returned empty run id")
		}
		if got := fh.promptOf(runID); got != "say hi" {
			t.Fatalf("fake harness received prompt %q, want %q", got, "say hi")
		}
		fh.mu.Lock()
		auth := fh.runAuths[runID]
		fh.mu.Unlock()
		if auth != "Bearer test-key" {
			t.Fatalf("Authorization header = %q, want %q", auth, "Bearer test-key")
		}
	})

	t.Run("server error is surfaced", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"code":"nope","message":"boom"}}`, http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := NewRunsClient(srv.URL, "")
		if _, err := c.StartRun(context.Background(), "hi"); err == nil {
			t.Fatal("expected error for 500 response")
		}
	})

	t.Run("response without run_id is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"running"}`))
		}))
		defer srv.Close()
		c := NewRunsClient(srv.URL, "")
		if _, err := c.StartRun(context.Background(), "hi"); err == nil {
			t.Fatal("expected error for missing run_id")
		}
	})
}

func TestRunsClientCancelRun(t *testing.T) {
	t.Run("posts to the cancel route with auth", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "k")
		runID, err := c.StartRun(context.Background(), "hi")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if err := c.CancelRun(context.Background(), runID); err != nil {
			t.Fatalf("CancelRun: %v", err)
		}
		if !fh.cancelled(runID) {
			t.Fatalf("fake harness saw no cancel POST for %s", runID)
		}
		fh.mu.Lock()
		auth := fh.cancelAuths[runID]
		fh.mu.Unlock()
		if auth != "Bearer k" {
			t.Fatalf("cancel Authorization = %q, want Bearer k", auth)
		}
	})

	t.Run("unknown run is an error", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		if err := c.CancelRun(context.Background(), "run-999"); err == nil {
			t.Fatal("expected error cancelling unknown run")
		}
	})
}

func TestRunsClientWaitTerminal(t *testing.T) {
	ctx := context.Background()

	t.Run("run.completed yields completed outcome", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		run := fh.run(runID)
		go func() {
			run.emit("run.started", "{}")
			run.emit("assistant.message.delta", `{"text":"he"}`)
			run.finish("run.completed", `{"output":"hello"}`)
		}()
		out, err := c.WaitTerminal(ctx, runID)
		if err != nil {
			t.Fatalf("WaitTerminal: %v", err)
		}
		if out.eventType != "run.completed" || out.costLimit {
			t.Fatalf("outcome = %+v, want run.completed without cost limit", out)
		}
	})

	t.Run("cost limit event is tracked through completion", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		run := fh.run(runID)
		go func() {
			run.emit("run.cost_limit_reached", `{"max_cost_usd":1.0}`)
			run.finish("run.completed", `{"output":"partial"}`)
		}()
		out, err := c.WaitTerminal(ctx, runID)
		if err != nil {
			t.Fatalf("WaitTerminal: %v", err)
		}
		if out.eventType != "run.completed" || !out.costLimit {
			t.Fatalf("outcome = %+v, want run.completed WITH cost limit", out)
		}
	})

	t.Run("run.failed carries the error text", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		run := fh.run(runID)
		go run.finish("run.failed", `{"error":"provider exploded"}`)
		out, err := c.WaitTerminal(ctx, runID)
		if err != nil {
			t.Fatalf("WaitTerminal: %v", err)
		}
		if out.eventType != "run.failed" || !strings.Contains(out.errText, "provider exploded") {
			t.Fatalf("outcome = %+v, want run.failed with error text", out)
		}
	})

	t.Run("run.cancelled yields cancelled outcome", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		run := fh.run(runID)
		go run.finish("run.cancelled", `{}`)
		out, err := c.WaitTerminal(ctx, runID)
		if err != nil {
			t.Fatalf("WaitTerminal: %v", err)
		}
		if out.eventType != "run.cancelled" {
			t.Fatalf("outcome = %+v, want run.cancelled", out)
		}
	})

	t.Run("stream ending before a terminal event is an error", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		run := fh.run(runID)
		go func() {
			run.emit("run.started", "{}")
			close(run.events) // stream dies mid-run
		}()
		if _, err := c.WaitTerminal(ctx, runID); err == nil {
			t.Fatal("expected error when stream ends without terminal event")
		}
	})

	t.Run("non-2xx events response is an error", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		if _, err := c.WaitTerminal(ctx, "run-999"); err == nil {
			t.Fatal("expected error for unknown run events subscription")
		}
	})

	t.Run("context cancellation aborts the wait", func(t *testing.T) {
		fh := newFakeHarness(t)
		c := NewRunsClient(fh.URL, "")
		runID, _ := c.StartRun(ctx, "hi")
		waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if _, err := c.WaitTerminal(waitCtx, runID); err == nil {
			t.Fatal("expected error when context is cancelled mid-wait")
		}
	})
}

// TestRunsClientWaitTerminalSkipsNonDataLines pins tolerance for SSE comment
// (": ping") keepalives and retry lines, which harnessd emits periodically.
func TestRunsClientWaitTerminalSkipsNonDataLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"run-1","status":"running"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": ping\n\n"))
		_, _ = w.Write([]byte("retry: 3000\n"))
		_, _ = w.Write([]byte("id: run-1:1\nevent: run.completed\ndata: {\"type\":\"run.completed\",\"payload\":{\"output\":\"x\"}}\n\n"))
	}))
	defer srv.Close()

	c := NewRunsClient(srv.URL, "")
	if _, err := c.StartRun(context.Background(), "hi"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	out, err := c.WaitTerminal(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if out.eventType != "run.completed" {
		t.Fatalf("outcome = %+v", out)
	}
}
