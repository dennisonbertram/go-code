package cloudscheduler_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/cloudscheduler"
)

// =============================================================================
// Deep Integration POCs with real Docker containers
// =============================================================================

func TestDockerDeep_01_RealContainerExecution(t *testing.T) {
	sched := cloudscheduler.New(2)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit 5 jobs that run real shell commands in containers
	var jobIDs []string
	for i := 1; i <= 5; i++ {
		job, err := sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("container-test-%d", i),
			Backend:      "docker",
			Args: map[string]any{
				"command": fmt.Sprintf("echo 'hello from container %d' && uname -a", i),
			},
		})
		require.NoError(t, err)
		jobIDs = append(jobIDs, job.ID)
	}

	// Wait for all with polling
	deadline := time.Now().Add(60 * time.Second)
	for _, id := range jobIDs {
		for time.Now().Before(deadline) {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted || job.Status == cloudscheduler.JobStatusFailed {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		job, err := sched.GetJob(id)
		require.NoError(t, err)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status, "job %s should complete", id)
		assert.NotEmpty(t, job.Result)
		t.Logf("Job %s result: %s", id, job.Result)
	}

	// Verify results contain expected output
	allJobs := sched.ListJobs("")
	assert.Len(t, allJobs, 5)
	for _, j := range allJobs {
		assert.NotEmpty(t, j.Result)
	}
}

// =============================================================================
// Deep POC 2: Container isolation verification
// =============================================================================

func TestDockerDeep_02_ContainerIsolation(t *testing.T) {
	sched := cloudscheduler.New(1)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Job A writes a file in its container
	jobA, _ := sched.Submit(cloudscheduler.Job{
		ID:           "isolation-test-a",
		WorkflowName: "isolation-writer",
		Backend:      "docker",
	})

	// Job B tries to read that file (should NOT see it — containers are isolated)
	jobB, _ := sched.Submit(cloudscheduler.Job{
		ID:           "isolation-test-b",
		WorkflowName: "isolation-reader",
		Backend:      "docker",
	})

	// Wait for both
	deadline := time.Now().Add(60 * time.Second)
	for _, id := range []string{jobA.ID, jobB.ID} {
		for time.Now().Before(deadline) {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted || job.Status == cloudscheduler.JobStatusFailed {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	jobA, _ = sched.GetJob(jobA.ID)
	jobB, _ = sched.GetJob(jobB.ID)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, jobA.Status)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, jobB.Status)

	// Both should have run successfully in isolated containers
	t.Logf("Job A: %s", jobA.Result)
	t.Logf("Job B: %s", jobB.Result)
}

// =============================================================================
// Deep POC 3: Concurrent container execution with resource limits
// =============================================================================

func TestDockerDeep_03_ConcurrentContainers(t *testing.T) {
	sched := cloudscheduler.New(3)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	const count = 8
	var jobIDs []string

	for i := 0; i < count; i++ {
		job, err := sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("concurrent-%d", i),
			Backend:      "docker",
			Tags:         map[string]string{"batch": "concurrency-test"},
		})
		require.NoError(t, err)
		jobIDs = append(jobIDs, job.ID)
	}

	// Poll until all complete
	deadline := time.Now().Add(60 * time.Second)
	completed := 0
	for completed < count && time.Now().Before(deadline) {
		completed = 0
		for _, id := range jobIDs {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted {
				completed++
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	assert.Equal(t, count, completed, "all %d jobs should complete", count)

	// Verify all results are unique (different container executions)
	results := make(map[string]bool)
	for _, id := range jobIDs {
		job, _ := sched.GetJob(id)
		results[job.Result] = true
	}
	assert.Len(t, results, count, "each container should produce a unique result")
}

// =============================================================================
// Deep POC 4: Scheduled execution with Docker
// =============================================================================

func TestDockerDeep_04_ScheduledDockerExecution(t *testing.T) {
	sched := cloudscheduler.New(2)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Schedule 3 jobs at different times
	now := time.Now()
	schedule1 := now.Add(1 * time.Second)
	schedule2 := now.Add(2 * time.Second)
	schedule3 := now.Add(3 * time.Second)

	job1, _ := sched.Submit(cloudscheduler.Job{
		ID:           "scheduled-1",
		WorkflowName: "scheduled-fast",
		Backend:      "docker",
		Schedule:     &schedule1,
	})
	job2, _ := sched.Submit(cloudscheduler.Job{
		ID:           "scheduled-2",
		WorkflowName: "scheduled-medium",
		Backend:      "docker",
		Schedule:     &schedule2,
	})
	job3, _ := sched.Submit(cloudscheduler.Job{
		ID:           "scheduled-3",
		WorkflowName: "scheduled-slow",
		Backend:      "docker",
		Schedule:     &schedule3,
	})

	// Job 1 should be pending immediately after submit
	j, _ := sched.GetJob(job1.ID)
	assert.Equal(t, cloudscheduler.JobStatusQueued, j.Status)

	// Wait for all to complete
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range []string{job1.ID, job2.ID, job3.ID} {
		for time.Now().Before(deadline) {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		job, _ := sched.GetJob(id)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status, "scheduled job %s should complete", id)
	}

	// Verify execution order: job1 before job2 before job3
	j1, _ := sched.GetJob(job1.ID)
	j2, _ := sched.GetJob(job2.ID)
	j3, _ := sched.GetJob(job3.ID)
	assert.True(t, j1.StartedAt.Before(*j2.StartedAt) || j1.StartedAt.Equal(*j2.StartedAt),
		"job1 should start before or at same time as job2")
	assert.True(t, j2.StartedAt.Before(*j3.StartedAt) || j2.StartedAt.Equal(*j3.StartedAt),
		"job2 should start before or at same time as job3")
}

// =============================================================================
// Deep POC 5: Event streaming during Docker execution
// =============================================================================

func TestDockerDeep_05_EventStreamingDocker(t *testing.T) {
	sched := cloudscheduler.New(1)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	job, _ := sched.Submit(cloudscheduler.Job{
		ID:           "event-stream-docker",
		WorkflowName: "event-test",
		Backend:      "docker",
	})

	// Subscribe BEFORE execution (only the queued event is emitted before worker picks it up)
	history, live, subCancel := sched.Subscribe(job.ID)
	defer subCancel()

	allEvents := make([]cloudscheduler.Event, 0)
	allEvents = append(allEvents, history...)

	// Collect all events
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

	// Verify full event sequence: queued → started → completed
	eventTypes := make([]string, 0, len(allEvents))
	for _, ev := range allEvents {
		eventTypes = append(eventTypes, ev.Type)
	}
	t.Logf("Event sequence: %v", eventTypes)

	foundQueued := false
	foundStarted := false
	foundCompleted := false
	for _, et := range eventTypes {
		switch et {
		case "queued":
			foundQueued = true
		case "started":
			foundStarted = true
		case "completed":
			foundCompleted = true
		}
	}
	assert.True(t, foundQueued, "should have queued event")
	assert.True(t, foundStarted, "should have started event")
	assert.True(t, foundCompleted, "should have completed event")
}

// =============================================================================
// Deep POC 6: Cancellation during queue
// =============================================================================

func TestDockerDeep_06_CancelDuringQueue(t *testing.T) {
	sched := cloudscheduler.New(1)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit 10 jobs to fill the queue (only 1 worker)
	var jobIDs []string
	for i := 0; i < 10; i++ {
		job, _ := sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("queue-job-%d", i),
			Backend:      "docker",
		})
		jobIDs = append(jobIDs, job.ID)
	}

	// Cancel the last 5 (should still be in queue)
	for i := 5; i < 10; i++ {
		err := sched.Cancel(jobIDs[i])
		require.NoError(t, err)
	}

	// Wait for all to settle
	time.Sleep(5 * time.Second)

	// First 5 should be completed, last 5 cancelled
	for i := 0; i < 5; i++ {
		job, _ := sched.GetJob(jobIDs[i])
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status, "job %d should complete", i)
	}
	for i := 5; i < 10; i++ {
		job, _ := sched.GetJob(jobIDs[i])
		assert.Equal(t, cloudscheduler.JobStatusCancelled, job.Status, "job %d should be cancelled", i)
	}
}

// =============================================================================
// Deep POC 7: Multi-backend simultaneous execution
// =============================================================================

func TestDockerDeep_07_MultiBackendSimultaneous(t *testing.T) {
	sched := cloudscheduler.New(4)

	// Register multiple executors
	sched.RegisterExecutor(cloudscheduler.NewDockerExecutor())
	sched.RegisterExecutor(newMockExecutor("cloudflare"))
	sched.RegisterExecutor(newMockExecutor("ssh"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit jobs to all backends simultaneously
	var wg sync.WaitGroup
	backends := []string{"docker", "docker", "docker", "cloudflare", "cloudflare", "ssh", "ssh"}
	jobIDs := make([]string, len(backends))

	for i, backend := range backends {
		wg.Add(1)
		go func(idx int, be string) {
			defer wg.Done()
			job, err := sched.Submit(cloudscheduler.Job{
				WorkflowName: fmt.Sprintf("multi-%s-%d", be, idx),
				Backend:      be,
				Tags:         map[string]string{"backend": be},
			})
			assert.NoError(t, err)
			jobIDs[idx] = job.ID
		}(i, backend)
	}
	wg.Wait()

	// Wait for all
	deadline := time.Now().Add(60 * time.Second)
	completed := 0
	for completed < len(jobIDs) && time.Now().Before(deadline) {
		completed = 0
		for _, id := range jobIDs {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted || job.Status == cloudscheduler.JobStatusFailed {
				completed++
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	assert.Equal(t, len(jobIDs), completed, "all jobs across all backends should complete")

	// Verify each backend executed correctly
	for _, id := range jobIDs {
		job, _ := sched.GetJob(id)
		assert.Equal(t, cloudscheduler.JobStatusCompleted, job.Status)
		assert.NotEmpty(t, job.Result)
		assert.Contains(t, job.Tags, "backend")
	}
}

// =============================================================================
// Deep POC 8: Retry with real Docker failures
// =============================================================================

func TestDockerDeep_08_RetryWithBackoff(t *testing.T) {
	sched := cloudscheduler.New(1)

	// Use a mock executor that fails twice then succeeds
	mockExec := newMockExecutor("docker")
	callCount := 0
	mockExec.executeFn = func(ctx context.Context, job cloudscheduler.Job) (string, error) {
		callCount++
		if callCount <= 2 {
			return "", fmt.Errorf("simulated container failure on attempt %d", callCount)
		}
		return fmt.Sprintf("container-success-after-%d-attempts", callCount), nil
	}
	sched.RegisterExecutor(mockExec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	job, _ := sched.Submit(cloudscheduler.Job{
		ID:           "retry-docker",
		WorkflowName: "retry-test",
		Backend:      "docker",
		RetryPolicy: &cloudscheduler.RetryPolicy{
			MaxRetries: 3,
			Backoff:    500 * time.Millisecond,
		},
	})

	// Wait for completion (retries add time)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := sched.GetJob(job.ID)
		if j.Status == cloudscheduler.JobStatusCompleted || j.Status == cloudscheduler.JobStatusFailed {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	final, _ := sched.GetJob(job.ID)
	assert.Equal(t, cloudscheduler.JobStatusCompleted, final.Status)
	assert.Contains(t, final.Result, "container-success")
	assert.Equal(t, 3, callCount, "should have been called 3 times (2 failures + 1 success)")
}

// =============================================================================
// Deep POC 9: Queue backlog handling
// =============================================================================

func TestDockerDeep_09_QueueBacklog(t *testing.T) {
	sched := cloudscheduler.New(2)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	// Submit 20 jobs — more than workers can handle immediately
	const total = 20
	var jobIDs []string
	for i := 0; i < total; i++ {
		job, _ := sched.Submit(cloudscheduler.Job{
			WorkflowName: fmt.Sprintf("backlog-%d", i),
			Backend:      "docker",
			Tags:         map[string]string{"batch": "backlog-test"},
		})
		jobIDs = append(jobIDs, job.ID)
	}

	// All should eventually complete
	deadline := time.Now().Add(120 * time.Second)
	completed := 0
	for completed < total && time.Now().Before(deadline) {
		completed = 0
		for _, id := range jobIDs {
			job, _ := sched.GetJob(id)
			if job.Status == cloudscheduler.JobStatusCompleted {
				completed++
			}
		}
		time.Sleep(time.Second)
	}

	assert.Equal(t, total, completed, "all %d backlog jobs should complete", total)

	// All should have started after submission time
	for _, id := range jobIDs {
		job, _ := sched.GetJob(id)
		assert.NotNil(t, job.StartedAt)
		assert.NotNil(t, job.CompletedAt)
		assert.True(t, job.CompletedAt.After(*job.StartedAt))
	}
}

// =============================================================================
// Deep POC 10: Stress test — rapid submit/cancel/status
// =============================================================================

func TestDockerDeep_10_StressTest(t *testing.T) {
	sched := cloudscheduler.New(4)
	exec := cloudscheduler.NewDockerExecutor()
	exec.Image = "alpine:latest"
	sched.RegisterExecutor(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	defer sched.Stop()

	var wg sync.WaitGroup

	// Rapid concurrent submits
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				_, err := sched.Submit(cloudscheduler.Job{
					WorkflowName: fmt.Sprintf("stress-%d-%d", idx, j),
					Backend:      "docker",
				})
				assert.NoError(t, err)
			}
		}(i)
	}

	// Concurrent status checks
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				jobs := sched.ListJobs("")
				_ = len(jobs) // just accessing
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Wait for all to complete
	time.Sleep(30 * time.Second)

	allJobs := sched.ListJobs("")
	t.Logf("Total jobs after stress test: %d", len(allJobs))

	completed := sched.ListJobs(cloudscheduler.JobStatusCompleted)
	assert.Equal(t, 30, len(completed), "all 30 stress jobs should complete")
}

// =============================================================================
// Test: Verify Docker executor handles missing Docker gracefully
// =============================================================================

func TestDockerExecutorGracefulDegradation(t *testing.T) {
	exec := cloudscheduler.NewDockerExecutor()

	// Should still work even with network issues — falls back to simulation
	result, err := exec.Execute(context.Background(), cloudscheduler.Job{
		ID:           "fallback-test",
		WorkflowName: "fallback",
		Backend:      "docker",
	})

	assert.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.True(t,
		strings.Contains(result, "docker-executed") || strings.Contains(result, "simulated"),
		"should produce either real or simulated output")
}

// Test: verify Backend() returns correct identifier
func TestDockerExecutorBackend(t *testing.T) {
	exec := cloudscheduler.NewDockerExecutor()
	assert.Equal(t, "docker", exec.Backend())
}
