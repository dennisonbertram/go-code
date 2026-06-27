package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJobManagerRunForegroundStreaming(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)

	var mu sync.Mutex
	var chunks []string
	streamer := func(chunk string) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk)
	}

	ctx := context.WithValue(context.Background(), ContextKeyOutputStreamer, streamer)

	result, err := mgr.runForeground(ctx, "echo hello; echo world", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}

	output, _ := result["output"].(string)
	if output != "hello\nworld" {
		t.Fatalf("expected output %q, got %q", "hello\nworld", output)
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have received streaming chunks for both lines.
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 streaming chunks, got %d: %v", len(chunks), chunks)
	}
	combined := strings.Join(chunks, "")
	if !strings.Contains(combined, "hello") || !strings.Contains(combined, "world") {
		t.Fatalf("streaming output missing expected content; chunks: %v", chunks)
	}
}

func TestJobManagerRunForegroundNoStreamer(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)

	// No output streamer in context — should behave exactly as before.
	result, err := mgr.runForeground(context.Background(), "echo hello", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}

	output, _ := result["output"].(string)
	if output != "hello" {
		t.Fatalf("expected output %q, got %q", "hello", output)
	}
}

func TestJobManagerRunForegroundStreamingCapturesFull(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)

	var mu sync.Mutex
	var chunks []string
	streamer := func(chunk string) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk)
	}
	ctx := context.WithValue(context.Background(), ContextKeyOutputStreamer, streamer)

	// Command produces multiple lines; the full output must still be correct.
	result, err := mgr.runForeground(ctx, "printf 'line1\\nline2\\nline3\\n'", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}

	output, _ := result["output"].(string)
	if output != "line1\nline2\nline3" {
		t.Fatalf("expected trimmed output %q, got %q", "line1\nline2\nline3", output)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks for 3 lines, got %d", len(chunks))
	}
}

func TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly(t *testing.T) {
	mgr := NewJobManager(t.TempDir(), nil)
	var streamed int
	streamer := func(chunk string) {
		streamed += len(chunk)
	}
	ctx := context.WithValue(context.Background(), ContextKeyOutputStreamer, streamer)

	start := time.Now()
	result, err := mgr.runForeground(ctx, "head -c 4194304 /dev/zero | tr '\\000' A; printf '\\nEOF\\n'", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("streaming overlong line took %s, want prompt return", elapsed)
	}
	if timedOut, _ := result["timed_out"].(bool); timedOut {
		t.Fatalf("overlong streaming line timed out: %#v", result)
	}
	if truncated, _ := result["stream_truncated"].(bool); !truncated {
		t.Fatalf("expected stream_truncated metadata, got %#v", result)
	}
	if _, ok := result["stream_error"].(string); !ok {
		t.Fatalf("expected stream_error metadata, got %#v", result)
	}
	if streamed == 0 {
		t.Fatal("expected streamer to receive truncated output")
	}
}

func TestJobManagerRunForegroundStreamingConcurrency(t *testing.T) {
	t.Parallel()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			mgr := NewJobManager(t.TempDir(), nil)

			var mu sync.Mutex
			var chunks []string
			streamer := func(chunk string) {
				mu.Lock()
				defer mu.Unlock()
				chunks = append(chunks, chunk)
			}
			ctx := context.WithValue(context.Background(), ContextKeyOutputStreamer, streamer)

			result, err := mgr.runForeground(ctx, "echo concurrent", 5, "")
			if err != nil {
				t.Errorf("runForeground: %v", err)
				return
			}
			output, _ := result["output"].(string)
			if output != "concurrent" {
				t.Errorf("expected %q, got %q", "concurrent", output)
			}
		}()
	}
	wg.Wait()
}

func TestRunForegroundTruncationMetadataAbsentWhenNotTruncated(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	result, err := mgr.runForeground(context.Background(), "echo short", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}
	if _, ok := result["truncated"]; ok {
		t.Fatal("truncated key should be absent for small output")
	}
	if _, ok := result["max_bytes"]; ok {
		t.Fatal("max_bytes key should be absent for small output")
	}
	if _, ok := result["truncation_strategy"]; ok {
		t.Fatal("truncation_strategy key should be absent for small output")
	}
	if _, ok := result["hint"]; ok {
		t.Fatal("hint key should be absent for small output")
	}
}

func TestRunForegroundTruncationMetadataPresentWhenTruncated(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	// Set a small output cap so we can trigger truncation easily.
	mgr.maxOutputBytes = 64

	// Generate output that exceeds 64 bytes.
	result, err := mgr.runForeground(context.Background(), "printf '%0200d' 0", 5, "")
	if err != nil {
		t.Fatalf("runForeground: %v", err)
	}

	truncated, ok := result["truncated"].(bool)
	if !ok || !truncated {
		t.Fatal("expected truncated == true")
	}
	maxBytes, ok := result["max_bytes"].(int)
	if !ok || maxBytes != 64 {
		t.Fatalf("expected max_bytes == 64, got %v", result["max_bytes"])
	}
	strategy, ok := result["truncation_strategy"].(string)
	if !ok || strategy != "head_tail" {
		t.Fatalf("expected truncation_strategy == head_tail, got %v", result["truncation_strategy"])
	}
	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestRunForegroundConcurrentTruncation(t *testing.T) {
	t.Parallel()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()

			mgr := NewJobManager(t.TempDir(), nil)
			mgr.maxOutputBytes = 64

			cmd := fmt.Sprintf("printf '%%0200d' %d", idx)
			result, err := mgr.runForeground(context.Background(), cmd, 5, "")
			if err != nil {
				t.Errorf("goroutine %d: runForeground: %v", idx, err)
				return
			}
			truncated, ok := result["truncated"].(bool)
			if !ok || !truncated {
				t.Errorf("goroutine %d: expected truncated == true", idx)
			}
		}(i)
	}
	wg.Wait()
}

func TestOutputStreamerFromContext(t *testing.T) {
	t.Parallel()

	// nil context should return false.
	if _, ok := OutputStreamerFromContext(nil); ok {
		t.Fatal("expected false for nil context")
	}

	// Empty context should return false.
	if _, ok := OutputStreamerFromContext(context.Background()); ok {
		t.Fatal("expected false for context without streamer")
	}

	// Context with streamer should return the function.
	called := false
	fn := func(chunk string) { called = true }
	ctx := context.WithValue(context.Background(), ContextKeyOutputStreamer, fn)
	got, ok := OutputStreamerFromContext(ctx)
	if !ok {
		t.Fatal("expected true for context with streamer")
	}
	got("x")
	if !called {
		t.Fatal("streamer was not called")
	}
}

func TestRunBackgroundCancelsWithRunContext(t *testing.T) {
	mgr := NewJobManager(t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())

	result, err := mgr.runBackground(ctx, "sleep 60", 60, "")
	if err != nil {
		t.Fatalf("runBackground: %v", err)
	}
	shellID, _ := result["shell_id"].(string)
	if shellID == "" {
		t.Fatalf("missing shell_id in result: %#v", result)
	}
	defer mgr.kill(shellID)

	cancel()
	waitForBackgroundJobDone(t, mgr, shellID, time.Second)

	output, err := mgr.output(shellID, false)
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if running, _ := output["running"].(bool); running {
		t.Fatalf("background job %s still running after context cancellation: %#v", shellID, output)
	}
}

func TestJobManagerShutdownCancelsAndClearsJobs(t *testing.T) {
	mgr := NewJobManager(t.TempDir(), nil)

	first, err := mgr.runBackground(context.Background(), "sleep 60", 60, "")
	if err != nil {
		t.Fatalf("runBackground first: %v", err)
	}
	second, err := mgr.runBackground(context.Background(), "sleep 60", 60, "")
	if err != nil {
		t.Fatalf("runBackground second: %v", err)
	}
	defer mgr.kill(first["shell_id"].(string))
	defer mgr.kill(second["shell_id"].(string))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mgr.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if len(mgr.jobs) != 0 {
		t.Fatalf("expected jobs map to be cleared after shutdown, got %d jobs", len(mgr.jobs))
	}
}

func waitForBackgroundJobDone(t *testing.T, mgr *JobManager, shellID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job := mgr.get(shellID)
		if job == nil {
			return
		}
		job.mu.Lock()
		done := job.done
		job.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for background job %s to finish", shellID)
}
