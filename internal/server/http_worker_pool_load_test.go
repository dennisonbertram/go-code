package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// TestWorkerPoolLoad is a load test that proves the daemon survives concurrent
// submissions against a bounded worker pool. It verifies queuing, event
// ordering, SSE keepalives, and goroutine cleanup under -race.
func TestWorkerPoolLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}

	const (
		poolSize    = 4
		totalRuns   = 50
		turnDelay   = 40 * time.Millisecond
		runDeadline = 30 * time.Second
	)

	// Baseline goroutine count BEFORE the runner is created.
	baseline := runtime.NumGoroutine()

	prov := fakeprovider.New(
		[]fakeprovider.Turn{{Content: "done", Delay: turnDelay}},
		fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustRepeatLast),
	)

	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		WorkerPoolSize:        poolSize,
		DefaultModel:          "test-model",
		MaxSteps:              1,
		MaxCompletedRetention: totalRuns,
	})

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// -----------------------------------------------------------------------
	// Assertion 1: fire 50 concurrent POSTs and collect all run IDs.
	// -----------------------------------------------------------------------
	runIDs := make([]string, totalRuns)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var submitErrors int32

	wg.Add(totalRuns)
	for i := 0; i < totalRuns; i++ {
		go func(idx int) {
			defer wg.Done()
			res, err := http.Post(
				ts.URL+"/v1/runs",
				"application/json",
				bytes.NewBufferString(`{"prompt":"load-test"}`),
			)
			if err != nil {
				atomic.AddInt32(&submitErrors, 1)
				return
			}
			defer res.Body.Close()
			var created struct {
				RunID string `json:"run_id"`
			}
			if err := json.NewDecoder(res.Body).Decode(&created); err != nil || created.RunID == "" {
				atomic.AddInt32(&submitErrors, 1)
				return
			}
			mu.Lock()
			runIDs[idx] = created.RunID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	require.Zero(t, submitErrors, "all 50 POST /v1/runs must succeed")
	for i, id := range runIDs {
		require.NotEmpty(t, id, "run_id[%d] must not be empty", i)
	}

	// -----------------------------------------------------------------------
	// Assertion 2: run.queued precedes run.started for excess runs; count
	// that at least (totalRuns - poolSize) runs emitted run.queued.
	// -----------------------------------------------------------------------
	var queuedCount int32

	var checkWg sync.WaitGroup
	checkWg.Add(totalRuns)
	for _, id := range runIDs {
		runID := id
		go func() {
			defer checkWg.Done()
			history, stream, cancel, err := runner.Subscribe(runID)
			if err != nil {
				// Run completed before Subscribe was called — already terminal,
				// so it can't have emitted run.queued that we'd still count.
				// This is fine: those runs went straight to run.started (no queue).
				// Failing here would be wrong; just skip.
				t.Errorf("run %s: Subscribe error (unexpected): %v", runID, err)
				return
			}
			defer cancel()

			var events []harness.Event
			events = append(events, history...)

			// Drain until terminal or stream closes.
			timeout := time.After(runDeadline)
			for {
				select {
				case ev, ok := <-stream:
					if !ok {
						goto done
					}
					events = append(events, ev)
					if harness.IsTerminalEvent(ev.Type) {
						goto done
					}
				case <-timeout:
					goto done
				}
			}
		done:
			// Check for run.queued and ordering.
			queuedIdx := -1
			startedIdx := -1
			for i, ev := range events {
				if ev.Type == harness.EventRunQueued && queuedIdx < 0 {
					queuedIdx = i
				}
				if ev.Type == harness.EventRunStarted && startedIdx < 0 {
					startedIdx = i
				}
			}
			if queuedIdx >= 0 {
				atomic.AddInt32(&queuedCount, 1)
				// run.queued must precede run.started.
				if startedIdx >= 0 && queuedIdx >= startedIdx {
					t.Errorf("run %s: run.queued (idx=%d) does not precede run.started (idx=%d)",
						runID, queuedIdx, startedIdx)
				}
			}
		}()
	}
	checkWg.Wait()

	// totalRuns-poolSize is the theoretical maximum number of queued events:
	// exactly that many runs cannot get a slot immediately when all 4 workers
	// are occupied. In practice the count can be lower because a worker may
	// finish a 40 ms turn and free its slot between consecutive dispatches,
	// letting the next run bypass the queue and go straight to run.started.
	// We use a conservative queueEpsilon to stay well below the floor even on a
	// loaded CI machine, while still catching a broken queuing path.
	const queueEpsilon = 4
	minQueued := int32(totalRuns - poolSize - queueEpsilon)
	if queuedCount < minQueued {
		t.Errorf("expected >= %d runs to emit run.queued, got %d (max possible: %d)",
			minQueued, queuedCount, totalRuns-poolSize)
	}
	t.Logf("runs that emitted run.queued: %d (expected >= %d, max possible: %d)",
		queuedCount, minQueued, totalRuns-poolSize)

	// -----------------------------------------------------------------------
	// Assertion 3: EVERY run eventually reaches a terminal state.
	// -----------------------------------------------------------------------
	deadline := time.Now().Add(runDeadline)
	for _, id := range runIDs {
		runID := id
		for {
			if time.Now().After(deadline) {
				t.Fatalf("run %s stuck: did not reach terminal state within %s", runID, runDeadline)
			}
			res, err := http.Get(ts.URL + "/v1/runs/" + runID)
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			var runState struct {
				Status string `json:"status"`
			}
			json.NewDecoder(res.Body).Decode(&runState) //nolint:errcheck
			res.Body.Close()
			switch runState.Status {
			case "completed", "failed", "cancelled":
				goto terminalOK
			}
			time.Sleep(25 * time.Millisecond)
		}
	terminalOK:
	}

	// -----------------------------------------------------------------------
	// Assertion 4: SSE keepalive pings.
	//
	// Spin up a fresh server with a Hang turn so the stream stays open >1s.
	// Then verify ": ping" lines appear within ~4s.
	// -----------------------------------------------------------------------
	t.Run("SSEKeepalives", func(t *testing.T) {
		t.Setenv("HARNESS_SSE_KEEPALIVE_SECONDS", "1")

		hangProv := fakeprovider.New(
			[]fakeprovider.Turn{{Hang: true}},
		)
		hangRunner := harness.NewRunner(hangProv, harness.NewRegistry(), harness.RunnerConfig{
			DefaultModel: "test-model",
			MaxSteps:     1,
		})
		hangHandler := New(hangRunner)
		hangTS := httptest.NewServer(hangHandler)
		defer hangTS.Close()
		defer func() { _ = hangRunner.Shutdown(context.Background()) }()

		// Start the run.
		createRes, err := http.Post(
			hangTS.URL+"/v1/runs",
			"application/json",
			bytes.NewBufferString(`{"prompt":"ping-test"}`),
		)
		require.NoError(t, err)
		defer createRes.Body.Close()

		var created struct {
			RunID string `json:"run_id"`
		}
		require.NoError(t, json.NewDecoder(createRes.Body).Decode(&created))
		require.NotEmpty(t, created.RunID)

		// Open SSE stream with a 4s read deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET",
			hangTS.URL+"/v1/runs/"+created.RunID+"/events", nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Read lines until we find a ping or deadline expires.
		found := false
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ": ping") {
				found = true
				break
			}
		}

		// Release the hang so the run can terminate cleanly.
		hangProv.Release()

		if !found {
			t.Error("expected at least one SSE ': ping' keepalive comment within 4s")
		}
	})

	// -----------------------------------------------------------------------
	// Assertion 5: no goroutine leak after Shutdown.
	// -----------------------------------------------------------------------
	ts.Close()
	err := runner.Shutdown(context.Background())
	require.NoError(t, err, "runner.Shutdown must not error")

	// Allow up to 3s for goroutines to settle, checking with GC.
	const epsilon = 5
	leakDeadline := time.Now().Add(3 * time.Second)
	var finalCount int
	for time.Now().Before(leakDeadline) {
		runtime.GC()
		finalCount = runtime.NumGoroutine()
		if finalCount <= baseline+epsilon {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalCount > baseline+epsilon {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Errorf("goroutine leak after Shutdown: NumGoroutine()=%d > baseline+epsilon=%d\n%s",
			finalCount, baseline+epsilon, buf[:n])
	} else {
		t.Logf("goroutine count after Shutdown: %d (baseline=%d, epsilon=%d)", finalCount, baseline, epsilon)
	}
}

// TestWorkerPoolLoad_QueuedEventConstant is a small sanity check (not gated
// behind testing.Short) that EventRunQueued equals the literal string
// "run.queued" so that the load test's string assertions remain valid even if
// the constant is renamed in the future.
func TestWorkerPoolLoad_QueuedEventConstant(t *testing.T) {
	const want = "run.queued"
	if string(harness.EventRunQueued) != want {
		t.Errorf("EventRunQueued = %q, want %q", harness.EventRunQueued, want)
	}
}

// TestWorkerPoolLoad_AllRunsTerminate is a lighter version of the load test
// that exercises the pool bounded-queue path without the 50-goroutine burst.
// It is NOT gated behind testing.Short so it runs in normal CI.
func TestWorkerPoolLoad_AllRunsTerminate(t *testing.T) {
	t.Parallel()

	const poolSize = 2
	const totalRuns = 8

	prov := fakeprovider.New(
		[]fakeprovider.Turn{{Content: "done", Delay: 20 * time.Millisecond}},
		fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustRepeatLast),
	)
	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		WorkerPoolSize: poolSize,
		DefaultModel:   "test-model",
		MaxSteps:       1,
	})
	defer func() { _ = runner.Shutdown(context.Background()) }()

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Start all runs concurrently.
	runIDs := make([]string, totalRuns)
	var wg sync.WaitGroup
	wg.Add(totalRuns)
	for i := 0; i < totalRuns; i++ {
		go func(idx int) {
			defer wg.Done()
			res, err := http.Post(
				ts.URL+"/v1/runs",
				"application/json",
				bytes.NewBufferString(`{"prompt":"small-load"}`),
			)
			if err != nil {
				t.Errorf("run %d: submit error: %v", idx, err)
				return
			}
			defer res.Body.Close()
			var created struct {
				RunID string `json:"run_id"`
			}
			if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
				t.Errorf("run %d: decode error: %v", idx, err)
				return
			}
			runIDs[idx] = created.RunID
		}(i)
	}
	wg.Wait()

	// All runs must reach a terminal status within 15s.
	deadline := time.Now().Add(15 * time.Second)
	for i, id := range runIDs {
		if id == "" {
			t.Errorf("run[%d] has empty run_id", i)
			continue
		}
		for {
			if time.Now().After(deadline) {
				t.Errorf("run[%d] %s: stuck before terminal", i, id)
				break
			}
			res, err := http.Get(fmt.Sprintf("%s/v1/runs/%s", ts.URL, id))
			if err != nil {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			var state struct {
				Status string `json:"status"`
			}
			json.NewDecoder(res.Body).Decode(&state) //nolint:errcheck
			res.Body.Close()
			if state.Status == "completed" || state.Status == "failed" || state.Status == "cancelled" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}
