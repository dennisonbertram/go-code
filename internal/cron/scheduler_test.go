package cron

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewScheduler_DefaultMaxConcurrent(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	if cap(s.sem) != 5 {
		t.Fatalf("expected default MaxConcurrent 5, got %d", cap(s.sem))
	}
}

func TestNewScheduler_CustomMaxConcurrent(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{MaxConcurrent: 3})
	if cap(s.sem) != 3 {
		t.Fatalf("expected MaxConcurrent 3, got %d", cap(s.sem))
	}
}

func TestAddJob_ValidSchedule(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	job := testJob("add-test")

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.mu.Lock()
	_, exists := s.entries[job.ID]
	s.mu.Unlock()
	if !exists {
		t.Fatal("expected job entry to exist after AddJob")
	}
}

func TestAddJob_InvalidSchedule(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	job := testJob("bad-schedule")
	job.Schedule = "invalid"

	if err := s.AddJob(job); err == nil {
		t.Fatal("expected error for invalid schedule")
	}
}

func TestRemoveJob(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	job := testJob("remove-test")

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.RemoveJob(job.ID)

	s.mu.Lock()
	_, exists := s.entries[job.ID]
	s.mu.Unlock()
	if exists {
		t.Fatal("expected job entry to be removed")
	}
}

func TestRemoveJob_Nonexistent(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	// Should not panic.
	s.RemoveJob("nonexistent")
}

func TestUpdateJobSchedule(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	job := testJob("update-schedule")

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.mu.Lock()
	oldEntry := s.entries[job.ID]
	s.mu.Unlock()

	job.Schedule = "0 * * * *"
	if err := s.UpdateJobSchedule(job); err != nil {
		t.Fatalf("UpdateJobSchedule: %v", err)
	}

	s.mu.Lock()
	newEntry := s.entries[job.ID]
	s.mu.Unlock()

	if oldEntry == newEntry {
		t.Fatal("expected new entry ID after UpdateJobSchedule")
	}
}

func TestFireJob_CreatesExecution(t *testing.T) {
	var createdExec Execution
	var updatedExecs []Execution
	var updatedJob Job
	var mu sync.Mutex

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			mu.Lock()
			createdExec = exec
			mu.Unlock()
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			mu.Lock()
			updatedExecs = append(updatedExecs, exec)
			mu.Unlock()
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			mu.Lock()
			updatedJob = job
			mu.Unlock()
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			return "test output", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	job := testJob("fire-test")

	s.fireJob(job)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if createdExec.JobID != job.ID {
		t.Fatalf("expected execution job_id %s, got %s", job.ID, createdExec.JobID)
	}
	if createdExec.Status != ExecStatusPending {
		t.Fatalf("expected initial status %s, got %s", ExecStatusPending, createdExec.Status)
	}

	// Should have two updates: running -> success.
	if len(updatedExecs) != 2 {
		t.Fatalf("expected 2 execution updates, got %d", len(updatedExecs))
	}
	if updatedExecs[0].Status != ExecStatusRunning {
		t.Fatalf("expected first update to running, got %s", updatedExecs[0].Status)
	}
	if updatedExecs[1].Status != ExecStatusSuccess {
		t.Fatalf("expected second update to success, got %s", updatedExecs[1].Status)
	}
	if updatedExecs[1].OutputSummary != "test output" {
		t.Fatalf("expected output 'test output', got %q", updatedExecs[1].OutputSummary)
	}

	if updatedJob.ID != job.ID {
		t.Fatalf("expected job update for %s, got %s", job.ID, updatedJob.ID)
	}
}

func TestFireJob_ExecutorError(t *testing.T) {
	var updatedExecs []Execution
	var mu sync.Mutex

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			mu.Lock()
			updatedExecs = append(updatedExecs, exec)
			mu.Unlock()
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			return "partial output", fmt.Errorf("command failed: exit status 1")
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	job := testJob("error-test")

	s.fireJob(job)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(updatedExecs) < 2 {
		t.Fatalf("expected at least 2 execution updates, got %d", len(updatedExecs))
	}
	lastExec := updatedExecs[len(updatedExecs)-1]
	if lastExec.Status != ExecStatusFailed {
		t.Fatalf("expected status %s, got %s", ExecStatusFailed, lastExec.Status)
	}
	if lastExec.Error == "" {
		t.Fatal("expected error message to be set")
	}
}

func TestFireJob_TimeoutError(t *testing.T) {
	var updatedExecs []Execution
	var mu sync.Mutex

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			mu.Lock()
			updatedExecs = append(updatedExecs, exec)
			mu.Unlock()
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			return "", fmt.Errorf("command timed out after 30 seconds")
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	job := testJob("timeout-test")

	s.fireJob(job)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	lastExec := updatedExecs[len(updatedExecs)-1]
	if lastExec.Status != ExecStatusTimeout {
		t.Fatalf("expected status %s, got %s", ExecStatusTimeout, lastExec.Status)
	}
}

func TestConcurrencySemaphore(t *testing.T) {
	var running int32
	var maxRunning int32
	var mu sync.Mutex

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			current := atomic.AddInt32(&running, 1)
			mu.Lock()
			if current > maxRunning {
				maxRunning = current
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return "ok", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 2})

	// Fire 5 jobs concurrently.
	for i := 0; i < 5; i++ {
		job := testJob(fmt.Sprintf("concurrent-%d", i))
		s.fireJob(job)
	}
	s.wg.Wait()

	mu.Lock()
	peak := maxRunning
	mu.Unlock()

	if peak > 2 {
		t.Fatalf("expected max 2 concurrent executions, got %d", peak)
	}
}

func TestStart_LoadsActiveJobs(t *testing.T) {
	jobs := []Job{
		{ID: uuid.New().String(), Name: "active-1", Schedule: "*/5 * * * *", Status: StatusActive},
		{ID: uuid.New().String(), Name: "paused-1", Schedule: "*/5 * * * *", Status: StatusPaused},
		{ID: uuid.New().String(), Name: "active-2", Schedule: "0 * * * *", Status: StatusActive},
	}

	store := &mockStore{
		ListJobsFunc: func(ctx context.Context) ([]Job, error) {
			return jobs, nil
		},
	}

	s := NewScheduler(store, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	s.mu.Lock()
	numEntries := len(s.entries)
	s.mu.Unlock()

	// Only active jobs should be registered.
	if numEntries != 2 {
		t.Fatalf("expected 2 entries (active jobs only), got %d", numEntries)
	}
}

func TestStart_StoreError(t *testing.T) {
	store := &mockStore{
		ListJobsFunc: func(ctx context.Context) ([]Job, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	s := NewScheduler(store, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected error from Start when store fails")
	}
}

func TestStop_WaitsForInFlight(t *testing.T) {
	var completed int32

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			time.Sleep(100 * time.Millisecond)
			atomic.AddInt32(&completed, 1)
			return "done", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 5})

	// Fire a job.
	job := testJob("inflight")
	s.fireJob(job)

	// Stop should wait for the in-flight execution.
	s.Stop()

	if atomic.LoadInt32(&completed) != 1 {
		t.Fatal("expected in-flight job to complete before Stop returns")
	}
}

func TestScheduler_ConcurrentAddRemove(t *testing.T) {
	store := &mockStore{}
	executor := &mockExecutor{}
	s := NewScheduler(store, executor, RealClock{}, SchedulerConfig{MaxConcurrent: 5})

	var wg sync.WaitGroup
	// Spawn goroutines that simultaneously AddJob and RemoveJob.
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			job := testJob(fmt.Sprintf("concurrent-sched-%d", gID))
			if err := s.AddJob(job); err != nil {
				// Invalid schedule errors are acceptable.
				return
			}
			// Immediately remove it.
			s.RemoveJob(job.ID)
		}(g)
	}
	wg.Wait()

	// All entries should be cleaned up.
	s.mu.Lock()
	remaining := len(s.entries)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 entries after concurrent add/remove, got %d", remaining)
	}
}

func TestScheduler_StopWithInflightExecutions(t *testing.T) {
	var completed int32
	var mu sync.Mutex
	var storedExecs []Execution

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			if exec.Status == ExecStatusSuccess {
				mu.Lock()
				storedExecs = append(storedExecs, exec)
				mu.Unlock()
			}
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			time.Sleep(500 * time.Millisecond)
			atomic.AddInt32(&completed, 1)
			return "done", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 10})

	// Fire 3 jobs.
	for i := 0; i < 3; i++ {
		job := testJob(fmt.Sprintf("inflight-%d", i))
		s.fireJob(job)
	}

	// Immediately call Stop() — should block until all 3 finish.
	s.Stop()

	if c := atomic.LoadInt32(&completed); c != 3 {
		t.Fatalf("expected 3 completed executions, got %d", c)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(storedExecs) != 3 {
		t.Fatalf("expected 3 success execution updates in store, got %d", len(storedExecs))
	}
}

func TestFireJob_CreateExecutionError(t *testing.T) {
	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return Execution{}, fmt.Errorf("db error")
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, &mockExecutor{}, clock, SchedulerConfig{MaxConcurrent: 1})
	job := testJob("create-exec-error")

	// Should not panic; just logs the error.
	s.fireJob(job)
	s.wg.Wait()
}

// --- Jitter integration tests ---

func TestAddJob_CachesJitterOffset(t *testing.T) {
	cfg := SchedulerConfig{
		Jitter: DefaultJitterConfig(),
	}
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, cfg)
	job := testJob("jitter-cache-test")

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.mu.Lock()
	cached, exists := s.jitterCache[jitterCacheKey(job.ID, job.Schedule)]
	s.mu.Unlock()

	if !exists {
		t.Fatal("expected jitter cache entry after AddJob")
	}
	// Jitter should be non-zero when enabled with default config.
	if cached == 0 {
		t.Fatal("expected non-zero jitter offset when jitter is enabled")
	}
}

func TestAddJob_JitterDisabledReturnsZero(t *testing.T) {
	cfg := SchedulerConfig{
		Jitter: JitterConfig{
			Enabled: false,
			MinSec:  60,
			MaxSec:  300,
		},
	}
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, cfg)
	job := testJob("jitter-disabled")

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.mu.Lock()
	cached := s.jitterCache[jitterCacheKey(job.ID, job.Schedule)]
	s.mu.Unlock()

	if cached != 0 {
		t.Fatalf("expected zero jitter when disabled, got %v", cached)
	}
}

func TestNewScheduler_DefaultJitterConfigUsed(t *testing.T) {
	// SchedulerConfig{} has zero Jitter fields; NewScheduler should apply defaults.
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})

	if !s.jitterCfg.Enabled {
		t.Error("expected default jitter to be enabled")
	}
	if s.jitterCfg.MinSec != 60 {
		t.Errorf("expected MinSec 60, got %d", s.jitterCfg.MinSec)
	}
	if s.jitterCfg.MaxSec != 300 {
		t.Errorf("expected MaxSec 300, got %d", s.jitterCfg.MaxSec)
	}
}

func TestNewScheduler_CustomJitterConfig(t *testing.T) {
	cfg := SchedulerConfig{
		Jitter: JitterConfig{
			Enabled: false,
			MinSec:  30,
			MaxSec:  120,
		},
	}
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, cfg)

	if s.jitterCfg.Enabled {
		t.Error("expected custom jitter to be disabled")
	}
	if s.jitterCfg.MinSec != 30 {
		t.Errorf("expected MinSec 30, got %d", s.jitterCfg.MinSec)
	}
}

func TestUpdateJobSchedule_RecomputesJitter(t *testing.T) {
	cfg := SchedulerConfig{
		Jitter: DefaultJitterConfig(),
	}
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, cfg)
	job := testJob("update-jitter")
	originalSchedule := job.Schedule

	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	s.mu.Lock()
	cached1 := s.jitterCache[jitterCacheKey(job.ID, originalSchedule)]
	s.mu.Unlock()

	// Change schedule and update.
	job.Schedule = "0 * * * *"
	if err := s.UpdateJobSchedule(job); err != nil {
		t.Fatalf("UpdateJobSchedule: %v", err)
	}

	s.mu.Lock()
	cached2 := s.jitterCache[jitterCacheKey(job.ID, job.Schedule)]
	s.mu.Unlock()

	if cached2 == 0 {
		t.Fatal("expected non-zero jitter after schedule update")
	}
	// The new schedule should have a different jitter value (deterministic but almost certainly different).
	if cached1 == cached2 && originalSchedule != job.Schedule {
		t.Logf("note: jitter unchanged after schedule change (collision, unlikely)")
	}
}

func TestFireJob_JitterAppliedToExecutionTiming(t *testing.T) {
	// Verify that fireJob calls sleepFn with the cached jitter offset.
	var createdExec Execution
	var sleptDuration time.Duration

	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			createdExec = exec
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			return nil
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			return "ok", nil
		},
	}

	// Use a config with jitter enabled.
	cfg := SchedulerConfig{Jitter: DefaultJitterConfig()}
	// Use a clock time where adding 1234ms doesn't land on minute 0 or 30.
	clock := newMockClock(time.Date(2025, 1, 1, 12, 15, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, cfg)

	// Replace sleepFn with a spy that records the duration without sleeping.
	s.sleepFn = func(d time.Duration) {
		sleptDuration = d
	}

	// Pre-populate the jitter cache with a known value (as AddJob would).
	job := testJob("jitter-fire")
	knownJitter := 1234 * time.Millisecond
	s.mu.Lock()
	s.jitterCache[jitterCacheKey(job.ID, job.Schedule)] = knownJitter
	s.mu.Unlock()

	// fireJob should call sleepFn with the jitter duration, then proceed.
	s.fireJob(job)
	s.wg.Wait()

	if createdExec.JobID != job.ID {
		t.Fatalf("expected execution for job %s, got %s", job.ID, createdExec.JobID)
	}
	if sleptDuration != knownJitter {
		t.Fatalf("expected sleepFn to be called with %v, got %v", knownJitter, sleptDuration)
	}
}

func TestJitter_SameJobSameSchedule_SameJitter(t *testing.T) {
	// Determinism: same job ID + schedule always produces the same jitter.
	cfg := DefaultJitterConfig()
	jobID := "deterministic-job"
	schedule := "*/10 * * * *"

	j1 := computeJitter(cfg, jobID, schedule)
	j2 := computeJitter(cfg, jobID, schedule)

	if j1 != j2 {
		t.Fatalf("same inputs should produce same jitter: %v vs %v", j1, j2)
	}
}
