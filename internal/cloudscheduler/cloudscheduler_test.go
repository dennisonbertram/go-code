package cloudscheduler_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/cloudscheduler"
)

// =============================================================================
// Mock Executor for testing
// =============================================================================

type mockExecutor struct {
	backend   string
	executeFn func(ctx context.Context, job cloudscheduler.Job) (string, error)
	callCount int
	mu        sync.Mutex
	delay     time.Duration
}

func (m *mockExecutor) Backend() string { return m.backend }
func (m *mockExecutor) Execute(ctx context.Context, job cloudscheduler.Job) (string, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if m.executeFn != nil {
		return m.executeFn(ctx, job)
	}
	return fmt.Sprintf("executed-%s", job.ID), nil
}

func newMockExecutor(backend string) *mockExecutor {
	return &mockExecutor{backend: backend}
}

// =============================================================================
// POC 1: Submit job, wait for completion, retrieve result
// =============================================================================

func TestCloudPOC1_SubmitAndComplete(t *testing.T) {
	sched := cloudscheduler.New(2)
	sched.RegisterExecutor(newMockExecutor("docker"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Submit BEFORE Start so the submit-time status is deterministically observable.
	// If we Start() first, the worker can complete the (instant) mock job before this
	// assertion runs, racing the transient "queued" state. Submit enqueues into the
	// buffered channel and the deferred Start() drains it. (Submit now also returns a
	// by-value snapshot, so job.Status is a stable submit-time view regardless.)
	job, err := sched.Submit(cloudscheduler.Job{
		WorkflowName: "test-workflow",
		Args:         map[string]any{"target": "production"},
		Backend:      "docker",
	})
	require.NoError(t, err)
	assert.Equal(t, cloudscheduler.JobStatusQueued, job.Status)

	sched.Start(ctx)
	defer sched.Stop()

	// Wait for completion. Generous deadline to survive heavy parallel load.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := sched.GetJob(job.ID)
		if j.Status == cloudscheduler.JobStatusCompleted || j.Status == cloudscheduler.JobStatusFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify completion
	final, err := sched.GetJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, final.Status)
	assert.Contains(t, final.Result, "executed")
	assert.NotNil(t, final.StartedAt)
	assert.NotNil(t, final.CompletedAt)
}

// =============================================================================
// POC 2: Multiple backends (docker, cloudflare, ssh)
// =============================================================================

func TestCloudPOC2_MultipleBackends(t *testing.T) {
	sched := cloudscheduler.New(4)
	sched.RegisterExecutor(newMockExecutor("docker"))
	sched.RegisterExecutor(newMockExecutor("cloudflare"))
	sched.RegisterExecutor(newMockExecutor("ssh"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit to each backend
	backends := []string{"docker", "cloudflare", "ssh"}
	var jobIDs []string

	for _, backend := range backends {
		job, err := sched.Submit(cloudscheduler.Job{
			WorkflowName: "multi-backend-test",
			Backend:      backend,
		})
		require.NoError(t, err)
		jobIDs = append(jobIDs, job.ID)
	}

	// Wait for all to complete via polling (fixed sleeps race a saturated machine).
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range jobIDs {
		for time.Now().Before(deadline) {
			job, err := sched.GetJob(id)
			if err == nil && (job.Status == cloudscheduler.JobStatusCompleted || job.Status == cloudscheduler.JobStatusFailed) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		job, err := sched.GetJob(id)
		require.NoError(t, err)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status)
	}

	// Verify unknown backend fails
	_, err := sched.Submit(cloudscheduler.Job{
		WorkflowName: "bad-backend",
		Backend:      "unsupported",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no executor registered")
}

// =============================================================================
// POC 3: Scheduled (delayed) execution
// =============================================================================

func TestCloudPOC3_ScheduledExecution(t *testing.T) {
	sched := cloudscheduler.New(2)
	sched.RegisterExecutor(newMockExecutor("docker"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Use a generous schedule delay so the "still queued" check below is robust
	// even if this goroutine is starved under heavy parallel load: the job is not
	// enqueued until the delay elapses, so it stays queued until then.
	future := time.Now().Add(2 * time.Second)
	job, err := sched.Submit(cloudscheduler.Job{
		WorkflowName: "scheduled-workflow",
		Backend:      "docker",
		Schedule:     &future,
	})
	require.NoError(t, err)

	// Immediately, it should still be queued (the delayed enqueue has not fired).
	j, _ := sched.GetJob(job.ID)
	assert.Equal(t, cloudscheduler.JobStatusQueued, j.Status)

	// After the schedule time it should complete; poll with a generous deadline
	// instead of a fixed sleep so the terminal assertion does not race a slow,
	// saturated scheduler.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, _ = sched.GetJob(job.ID)
		if j.Status == cloudscheduler.JobStatusCompleted || j.Status == cloudscheduler.JobStatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	j, err = sched.GetJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, j.Status)
}

// =============================================================================
// POC 4: Event streaming during execution
// =============================================================================

func TestCloudPOC4_EventStreaming(t *testing.T) {
	exec := newMockExecutor("docker")
	exec.delay = 200 * time.Millisecond // ensure we can observe events

	sched := cloudscheduler.New(2)
	sched.RegisterExecutor(exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	job, _ := sched.Submit(cloudscheduler.Job{
		WorkflowName: "event-stream-test",
		Backend:      "docker",
	})

	// Subscribe before starting
	history, live, subCancel := sched.Subscribe(job.ID)
	defer subCancel()

	allEvents := make([]cloudscheduler.Event, 0, len(history))
	allEvents = append(allEvents, history...)

	// Collect live events. Generous timeout so a saturated scheduler still
	// delivers the terminal event before we give up (early-exit on terminal).
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-live:
			if !ok {
				break loop
			}
			allEvents = append(allEvents, ev)
			if ev.Type == "completed" || ev.Type == "failed" {
				break loop
			}
		case <-timeout:
			break loop
		}
	}

	// Verify event types
	eventTypes := map[string]bool{}
	for _, ev := range allEvents {
		eventTypes[ev.Type] = true
	}
	assert.True(t, eventTypes["queued"], "should have queued event")
	assert.True(t, eventTypes["started"], "should have started event")
	assert.True(t, eventTypes["completed"], "should have completed event")
}

// =============================================================================
// POC 5: Concurrent job submission and execution
// =============================================================================

func TestCloudPOC5_ConcurrentJobs(t *testing.T) {
	sched := cloudscheduler.New(3)
	sched.RegisterExecutor(newMockExecutor("docker"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	const jobCount = 10
	var wg sync.WaitGroup
	jobIDs := make([]string, jobCount)

	for i := 0; i < jobCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job, err := sched.Submit(cloudscheduler.Job{
				WorkflowName: fmt.Sprintf("concurrent-%d", idx),
				Backend:      "docker",
				Tags:         map[string]string{"batch": "test", "index": fmt.Sprintf("%d", idx)},
			})
			assert.NoError(t, err)
			jobIDs[idx] = job.ID
		}(i)
	}
	wg.Wait()

	// Wait for all to complete with polling
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range jobIDs {
		for time.Now().Before(deadline) {
			job, err := sched.GetJob(id)
			if err == nil && (job.Status == cloudscheduler.JobStatusCompleted || job.Status == cloudscheduler.JobStatusFailed) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		job, err := sched.GetJob(id)
		require.NoError(t, err)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status, "job %s should complete", id)
	}

	// List all completed jobs
	completed := sched.ListJobs(cloudscheduler.JobStatusCompleted)
	assert.Len(t, completed, jobCount)
}

// =============================================================================
// POC 6: Job cancellation
// =============================================================================

func TestCloudPOC6_JobCancellation(t *testing.T) {
	exec := newMockExecutor("docker")
	exec.delay = 2 * time.Second // slow executor so we can cancel

	sched := cloudscheduler.New(1)
	sched.RegisterExecutor(exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit many jobs to fill the queue
	var jobIDs []string
	for i := 0; i < 5; i++ {
		job, _ := sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("cancel-test-%d", i),
			Backend:      "docker",
		})
		jobIDs = append(jobIDs, job.ID)
	}

	// Cancel the last job (should be queued)
	err := sched.Cancel(jobIDs[4])
	require.NoError(t, err)

	job, _ := sched.GetJob(jobIDs[4])
	assert.Equal(t, cloudscheduler.JobStatusCancelled, job.Status)

	// Cannot cancel an already cancelled job
	err = sched.Cancel(jobIDs[4])
	assert.Error(t, err)
}

// =============================================================================
// POC 7: Retry on failure
// =============================================================================

func TestCloudPOC7_RetryOnFailure(t *testing.T) {
	exec := newMockExecutor("docker")
	callCount := 0
	exec.executeFn = func(ctx context.Context, job cloudscheduler.Job) (string, error) {
		callCount++
		if callCount <= 2 {
			return "", fmt.Errorf("transient error on attempt %d", callCount)
		}
		return "success-after-retry", nil
	}

	sched := cloudscheduler.New(1)
	sched.RegisterExecutor(exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	job, _ := sched.Submit(cloudscheduler.Job{
		WorkflowName: "retry-test",
		Backend:      "docker",
		RetryPolicy: &cloudscheduler.RetryPolicy{
			MaxRetries: 3,
			Backoff:    100 * time.Millisecond,
		},
	})

	// Wait for retries to complete. Poll specifically for Completed — NOT Failed —
	// because executeJob transiently sets the status to Failed during each retry
	// backoff window before retryJob re-queues the job. Breaking on Failed would
	// race that transient window under load. This mock succeeds on the 3rd attempt,
	// so Completed is the stable terminal outcome; the deadline bounds a real hang.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := sched.GetJob(job.ID)
		if j.Status == cloudscheduler.JobStatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	final, err := sched.GetJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, final.Status)
	assert.Equal(t, "success-after-retry", final.Result)
}

// =============================================================================
// POC 8: List and filter jobs
// =============================================================================

func TestCloudPOC8_ListAndFilter(t *testing.T) {
	sched := cloudscheduler.New(2)
	sched.RegisterExecutor(newMockExecutor("docker"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit jobs with different workflows
	for i := 0; i < 5; i++ {
		sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("wf-%d", i%2), // wf-0 and wf-1
			Backend:      "docker",
		})
	}

	// Poll until all 5 jobs reach Completed instead of a fixed sleep, so the
	// terminal-state assertions below do not race a slow scheduler under load.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(sched.ListJobs(cloudscheduler.JobStatusCompleted)) == 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// List all
	all := sched.ListJobs("")
	assert.Len(t, all, 5, "all 5 jobs should exist")

	// List only completed
	completed := sched.ListJobs(cloudscheduler.JobStatusCompleted)
	assert.Len(t, completed, 5, "all 5 jobs should complete")

	// List queued (should be empty now — all jobs have reached a terminal state)
	queued := sched.ListJobs(cloudscheduler.JobStatusQueued)
	assert.Len(t, queued, 0)
}

// =============================================================================
// POC 9: Docker executor (simulated when Docker unavailable)
// =============================================================================

func TestCloudPOC9_DockerExecutor(t *testing.T) {
	exec := cloudscheduler.NewDockerExecutor()
	assert.Equal(t, "docker", exec.Backend())

	// Test simulated execution (works without Docker)
	result, err := exec.Execute(context.Background(), cloudscheduler.Job{
		ID:           "test-job-001",
		WorkflowName: "docker-test",
	})
	// Should always succeed (falls back to simulation)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "docker")
}

// =============================================================================
// POC 10: Full cloud workflow lifecycle
// =============================================================================

func TestCloudPOC10_FullCloudLifecycle(t *testing.T) {
	sched := cloudscheduler.New(2)
	sched.RegisterExecutor(newMockExecutor("docker"))
	sched.RegisterExecutor(newMockExecutor("cloudflare"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit 3 jobs to different backends with different configs
	jobs := []struct {
		name     string
		backend  string
		schedule time.Duration
		retry    *cloudscheduler.RetryPolicy
	}{
		{"immediate-job", "docker", 0, nil},
		{"scheduled-job", "docker", 300 * time.Millisecond, nil},
		{"retry-job", "cloudflare", 0, &cloudscheduler.RetryPolicy{MaxRetries: 2, Backoff: 200 * time.Millisecond}},
	}

	var jobIDs []string
	for _, j := range jobs {
		job := cloudscheduler.Job{
			WorkflowName: j.name,
			Backend:      j.backend,
			RetryPolicy:  j.retry,
		}
		if j.schedule > 0 {
			t := time.Now().Add(j.schedule)
			job.Schedule = &t
		}
		created, err := sched.Submit(job)
		require.NoError(t, err)
		jobIDs = append(jobIDs, created.ID)
	}

	// Wait for all to complete with polling. This test submits exactly 3 jobs
	// (the wait condition previously checked for 5 — a copy/paste bug that made
	// the early-exit dead code, so the loop always burned the full deadline).
	const wantJobs = 3
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		all := sched.ListJobs("")
		if len(all) >= wantJobs {
			completed := 0
			for _, j := range all {
				if j.Status == cloudscheduler.JobStatusCompleted {
					completed++
				}
			}
			if completed == wantJobs {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify all completed
	for _, id := range jobIDs {
		job, err := sched.GetJob(id)
		require.NoError(t, err)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status,
			"job %s (%s) should be completed", job.WorkflowName, id)
	}

	// Verify listing by status
	allJobs := sched.ListJobs("")
	assert.Len(t, allJobs, 3)
}
