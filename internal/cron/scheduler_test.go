package cron

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	robfigcron "github.com/robfig/cron/v3"
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
	var touchedJobID string
	var mu sync.Mutex

	job := testJob("fire-test")

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
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
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
			mu.Lock()
			touchedJobID = jobID
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

	s.fireJob(job, 0)
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

	if touchedJobID != job.ID {
		t.Fatalf("expected TouchJobRun for %s, got %s", job.ID, touchedJobID)
	}
}

func TestFireJob_ExecutorError(t *testing.T) {
	var updatedExecs []Execution
	var mu sync.Mutex

	job := testJob("error-test")

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			mu.Lock()
			updatedExecs = append(updatedExecs, exec)
			mu.Unlock()
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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

	s.fireJob(job, 0)
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

	job := testJob("timeout-test")

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			mu.Lock()
			updatedExecs = append(updatedExecs, exec)
			mu.Unlock()
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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

	s.fireJob(job, 0)
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
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{ID: id, Status: StatusActive, Schedule: "*/5 * * * *"}, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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
		s.fireJob(job, 0)
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
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{ID: id, Status: StatusActive, Schedule: "*/5 * * * *"}, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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
	s.fireJob(job, 0)

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
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{ID: id, Status: StatusActive, Schedule: "*/5 * * * *"}, nil
		},
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
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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
		s.fireJob(job, 0)
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
	job := testJob("create-exec-error")
	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return Execution{}, fmt.Errorf("db error")
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, &mockExecutor{}, clock, SchedulerConfig{MaxConcurrent: 1})

	// Should not panic; just logs the error.
	s.fireJob(job, 0)
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

	job := testJob("jitter-fire")

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			createdExec = exec
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
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

	// The jitter offset is passed directly to fireJob, as AddJob would do
	// (fireJob no longer reads s.jitterCache itself).
	knownJitter := 1234 * time.Millisecond

	// fireJob should call sleepFn with the jitter duration, then proceed.
	s.fireJob(job, knownJitter)
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

// TestFireJobAdvancesNextRunAt (T1) — fireJob must recompute NextRunAt after execution.
// Before P1, fireJob never sets NextRunAt, so the stored job keeps the stale original value.
func TestFireJobAdvancesNextRunAt(t *testing.T) {
	var touchedLastRun, touchedNextRun time.Time
	var touchCalled bool
	var mu sync.Mutex

	job := testJob("advance-next-run-at")
	job.Schedule = "*/5 * * * *"
	// Set an obviously stale NextRunAt so we can detect if it is unchanged.
	job.NextRunAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		TouchJobRunFunc: func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
			mu.Lock()
			touchCalled = true
			touchedLastRun = lastRun
			touchedNextRun = nextRun
			mu.Unlock()
			return nil
		},
	}

	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			return "ok", nil
		},
	}

	// Use a fixed clock so endTime is deterministic.
	fireTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := newMockClock(fireTime)

	// Disable jitter so sleepFn is not called and no extra complexity.
	cfg := SchedulerConfig{
		MaxConcurrent: 1,
		Jitter:        JitterConfig{Enabled: false},
	}
	s := NewScheduler(store, executor, clock, cfg)
	s.sleepFn = func(d time.Duration) {} // no-op

	s.fireJob(job, 0)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !touchCalled {
		t.Fatal("expected TouchJobRun to be called")
	}
	if !touchedLastRun.Equal(fireTime) {
		t.Fatalf("expected TouchJobRun lastRun %v, got %v", fireTime, touchedLastRun)
	}

	// Compute the expected NextRunAt: first occurrence of "*/5 * * * *" after fireTime.
	want, err := NextRunTime(job.Schedule, fireTime)
	if err != nil {
		t.Fatalf("NextRunTime: %v", err)
	}

	if touchedNextRun.IsZero() {
		t.Fatal("NextRunAt was not set via TouchJobRun")
	}
	if !touchedNextRun.Equal(want) {
		t.Fatalf("NextRunAt = %v, want %v (schedule %q, fire time %v)",
			touchedNextRun, want, job.Schedule, fireTime)
	}
}

// --- BUG 1: jitterCache concurrent access ---

// TestScheduler_ConcurrentAddJobAndFireJob_NoDataRace (BT-001, P1) reproduces
// the fatal, unrecoverable Go runtime error that crashes the daemon when
// fireJob reads s.jitterCache without holding s.mu while AddJob concurrently
// writes to the same map under s.mu.
//
// fireJob currently reads `s.jitterCache[jitterKey]` with no lock at all
// (scheduler.go ~141-142), while AddJob writes that map under s.mu
// (scheduler.go ~93). A concurrent unsynchronized map read + write is a
// fatal Go runtime error ("concurrent map read and map write") that cannot
// be recovered with panic/recover — it kills the whole process.
//
// This test must be run with `-race` to reliably surface the problem
// (`go test ./internal/cron/... -race -run TestScheduler_ConcurrentAddJobAndFireJob_NoDataRace`).
// Before the fix, this crashes the test binary with a fatal runtime error
// (or is flagged by the race detector) because fireJob's map read at
// scheduler.go:142 races with AddJob's map write at scheduler.go:93.
// After the fix, fireJob never touches s.jitterCache (the jitter offset is
// passed in directly by AddJob's closure), so there is nothing to race on.
func TestScheduler_ConcurrentAddJobAndFireJob_NoDataRace(t *testing.T) {
	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{}, ErrJobNotFound
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
	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 10})
	s.sleepFn = func(time.Duration) {} // no-op so fireJob returns quickly

	// One job that's already registered, so fireJob has a real jitterCache
	// entry to read while other goroutines mutate the map.
	fireTarget := testJob("race-fire-target")
	if err := s.AddJob(fireTarget); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	var wg sync.WaitGroup
	const iterations = 200

	// Goroutines that repeatedly AddJob (writes s.jitterCache under s.mu).
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				job := testJob(fmt.Sprintf("race-writer-%d-%d", gid, i))
				_ = s.AddJob(job)
			}
		}(g)
	}

	// Goroutines that repeatedly call fireJob for the pre-registered job,
	// exercising the unsynchronized jitterCache read on the old code path.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s.fireJob(fireTarget, 0)
			}
		}()
	}

	wg.Wait()
	s.wg.Wait()
}

// TestScheduler_ConcurrentUpdateJobScheduleAndFireJob_NoDataRace (regression
// for BUG 1) exercises UpdateJobSchedule concurrently with fireJob under
// -race. UpdateJobSchedule takes a different path than plain AddJob (it
// removes the old entry, recomputes and re-writes s.jitterCache, then calls
// AddJob again), so it is a distinct angle from the original red test: it
// would fail (fatal "concurrent map read and map write", or be flagged by
// -race) if fireJob ever went back to reading s.jitterCache directly instead
// of using the jitter value captured in AddJob's/UpdateJobSchedule's closure.
func TestScheduler_ConcurrentUpdateJobScheduleAndFireJob_NoDataRace(t *testing.T) {
	store := &mockStore{
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error {
			return nil
		},
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{}, ErrJobNotFound
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
	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 10})
	s.sleepFn = func(time.Duration) {} // no-op so fireJob returns quickly

	fireTarget := testJob("update-schedule-race-target")
	if err := s.AddJob(fireTarget); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	var wg sync.WaitGroup
	const iterations = 200

	// Goroutines that repeatedly UpdateJobSchedule for an unrelated job,
	// exercising RemoveJob + jitterCache re-write + AddJob.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			job := testJob(fmt.Sprintf("update-schedule-race-%d", gid))
			if err := s.AddJob(job); err != nil {
				return
			}
			for i := 0; i < iterations; i++ {
				job.Schedule = "*/5 * * * *"
				_ = s.UpdateJobSchedule(job)
				job.Schedule = "0 * * * *"
				_ = s.UpdateJobSchedule(job)
			}
		}(g)
	}

	// Goroutines that repeatedly call fireJob for a stable, already
	// registered job.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s.fireJob(fireTarget, 0)
			}
		}()
	}

	wg.Wait()
	s.wg.Wait()
}

// --- BUG 2: fireJob writes back a stale Job snapshot ---

// TestFireJob_SkipsExecutionWhenJobPausedInStore (BT-002, P1) reproduces the
// "resurrects paused jobs" half of BUG 2: fireJob captures a full Job at
// AddJob/schedule time and, before the fix, never re-checks the job's
// current status before firing. If a job is paused via the store after it
// was scheduled but before the timer fires, fireJob must NOT execute it.
//
// Before the fix, fireJob has no way to observe the pause (it never calls
// store.GetJob), so it fires anyway. This test fails before the fix
// (executor.Execute is called) and passes after the fix (fireJob re-reads
// the job from the store and skips firing when the live status isn't
// active).
func TestFireJob_SkipsExecutionWhenJobPausedInStore(t *testing.T) {
	var executed bool
	var mu sync.Mutex

	// job is the STALE snapshot fireJob receives — captured as active at
	// schedule time via AddJob's closure.
	job := testJob("pause-skip-test")
	job.Status = StatusActive

	// pausedJob is what the store now reports: the user paused the job
	// after it was scheduled but before this fire.
	pausedJob := job
	pausedJob.Status = StatusPaused

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return pausedJob, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error { return nil },
		UpdateJobFunc: func(ctx context.Context, job Job) error { return nil },
	}
	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			mu.Lock()
			executed = true
			mu.Unlock()
			return "ok", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1, Jitter: JitterConfig{Enabled: false}})
	s.sleepFn = func(time.Duration) {}

	s.fireJob(job, 0)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if executed {
		t.Fatal("expected fireJob to skip execution for a job that is now paused in the store, but the executor ran (paused job was resurrected)")
	}
}

// TestFireJob_DoesNotWriteBackStaleFullSnapshot (BT-003, P1) reproduces the
// "silently reverts user edits" half of BUG 2: fireJob previously called
// store.UpdateJob with the full Job struct it captured at schedule time,
// clobbering any edits (schedule, execution config, timeout, tags) made to
// the job in the store since it was scheduled.
//
// The fix must stop calling store.UpdateJob for run-tracking bookkeeping —
// it should use a narrower method (TouchJobRun, added in the next commit)
// that only touches last_run_at/next_run_at/updated_at. This test asserts
// that fireJob never calls store.UpdateJob at all as part of a normal fire,
// and that execution uses the CURRENT (re-read) job state rather than the
// stale snapshot. Before the fix, UpdateJobFunc IS invoked (with the stale
// snapshot) and the stale ExecConfig/Tags are used, so this test fails.
// After the fix, UpdateJob is never called and the live state is used.
func TestFireJob_DoesNotWriteBackStaleFullSnapshot(t *testing.T) {
	var updateJobCalled bool
	var mu sync.Mutex

	// job is the STALE snapshot captured at schedule time.
	job := testJob("stale-snapshot-test")
	job.Tags = "stale-tag"
	job.TimeoutSec = 30

	// current is what the store now reports: the user edited tags and
	// timeout after the job was scheduled.
	current := job
	current.Tags = "edited-tag"
	current.TimeoutSec = 999

	var executedTags string
	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return current, nil
		},
		CreateExecutionFunc: func(ctx context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(ctx context.Context, exec Execution) error { return nil },
		UpdateJobFunc: func(ctx context.Context, job Job) error {
			mu.Lock()
			updateJobCalled = true
			mu.Unlock()
			return nil
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, execJob Job) (string, error) {
			mu.Lock()
			executedTags = execJob.Tags
			mu.Unlock()
			return "ok", nil
		},
	}

	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1, Jitter: JitterConfig{Enabled: false}})
	s.sleepFn = func(time.Duration) {}

	s.fireJob(job, 0)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if updateJobCalled {
		t.Fatal("fireJob must not call store.UpdateJob with a full job snapshot (it can silently revert concurrent edits); it should only touch run-tracking columns via TouchJobRun")
	}
	if executedTags != "edited-tag" {
		t.Fatalf("expected fireJob to execute with the current (edited) job state %q, got %q", "edited-tag", executedTags)
	}
}

// --- BUG 3: shutdown blocks up to the full jitter window ---

// TestScheduler_Stop_InterruptsLongJitterWait (BT-004, P1) reproduces the
// shutdown stall: cronScheduler.Stop() blocks until in-flight fireJob
// invocations dispatched by robfig/cron finish, and fireJob's jitter wait
// was an UNINTERRUPTIBLE sleep that can be minutes long. So Stop() (and
// therefore the whole harnessd shutdown sequence, which calls Stop()
// before draining the HTTP server) stalls for as long as the jitter.
//
// This test registers a job directly with the underlying robfig/cron
// dispatcher (bypassing the 5-field, minute-resolution schedule parser
// Scheduler.AddJob uses) so that fireJob is invoked the same way
// production code invokes it: synchronously, from cron's own dispatch
// goroutine, tracked by cron.Stop()'s returned context. The job's jitter
// is deliberately long (a few seconds — long enough to prove the point
// without making the test suite slow, since production jitter can be up
// to 5 minutes by default).
//
// Before the fix: Stop() blocks for close to the full jitter duration
// (uninterruptible time.Sleep) and the job still fires afterward. After
// the fix: Stop() returns promptly (closing s.done interrupts the wait)
// and the job is skipped entirely rather than firing mid-shutdown.
func TestScheduler_Stop_InterruptsLongJitterWait(t *testing.T) {
	var executed bool
	var mu sync.Mutex

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{}, ErrJobNotFound
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			mu.Lock()
			executed = true
			mu.Unlock()
			return "ok", nil
		},
	}
	clock := newMockClock(time.Now())
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	// sleepFn is left at its default (time.Sleep) deliberately: this test
	// must exercise a REAL blocking sleep to prove Stop() interrupts it,
	// not just that a test double happens to return quickly.

	job := testJob("stop-interrupt-test")
	const longJitter = 3 * time.Second

	// Register directly with the underlying robfig cron dispatcher so
	// fireJob is invoked synchronously from cron's own goroutine — the
	// same mechanism Scheduler.Stop()'s `s.cron.Stop()` context waits on.
	s.cron.Schedule(robfigcron.Every(time.Second), robfigcron.FuncJob(func() {
		s.fireJob(job, longJitter)
	}))
	s.cron.Start()

	// Give the first tick (fires ~1s after Start, per robfig's Every()
	// minimum resolution) a chance to enter the jitter wait.
	time.Sleep(1500 * time.Millisecond)

	stopStart := time.Now()
	s.Stop()
	stopDuration := time.Since(stopStart)

	if stopDuration > 1500*time.Millisecond {
		t.Fatalf("Stop() took %v, expected it to return promptly (well under the %v jitter wait)", stopDuration, longJitter)
	}

	mu.Lock()
	defer mu.Unlock()
	if executed {
		t.Fatal("expected fireJob to skip execution when Stop() interrupts the jitter wait, but the executor ran")
	}
}

// TestScheduler_Stop_IsIdempotent (regression for BUG 3) verifies that
// calling Stop() twice does not panic. The fix closes s.done via
// sync.Once specifically so a double Stop is safe; a regression here
// would surface as a "close of closed channel" panic.
func TestScheduler_Stop_IsIdempotent(t *testing.T) {
	s := NewScheduler(&mockStore{}, &mockExecutor{}, RealClock{}, SchedulerConfig{})
	s.Stop()
	s.Stop() // must not panic
}

// TestScheduler_Stop_InterruptsMultipleConcurrentJitterWaits (regression
// for BUG 3) covers a different angle than the red test: several fireJob
// calls blocked in a long jitter wait AT ONCE (rather than one job
// dispatched through the real robfig cron mechanism). It would fail if
// Stop() only interrupted the first waiter, or if the done-channel signal
// were somehow consumed by one goroutine and unavailable to the others
// (e.g. a regression from `<-s.done` being changed to a receive from a
// non-broadcasting channel).
func TestScheduler_Stop_InterruptsMultipleConcurrentJitterWaits(t *testing.T) {
	var executedCount int32

	store := &mockStore{
		GetJobFunc: func(ctx context.Context, id string) (Job, error) {
			return Job{}, ErrJobNotFound
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(ctx context.Context, job Job) (string, error) {
			atomic.AddInt32(&executedCount, 1)
			return "ok", nil
		},
	}
	clock := newMockClock(time.Now())
	s := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 5})

	const n = 5
	const longJitter = 5 * time.Second
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job := testJob(fmt.Sprintf("multi-stop-%d", i))
			s.fireJob(job, longJitter)
		}(i)
	}

	// Give the goroutines a moment to enter their jitter waits.
	time.Sleep(100 * time.Millisecond)

	stopStart := time.Now()
	s.Stop()
	stopDuration := time.Since(stopStart)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fireJob goroutines did not return promptly after Stop()")
	}

	if stopDuration > 1500*time.Millisecond {
		t.Fatalf("Stop() took %v, expected all %d jitter waits to be interrupted promptly", stopDuration, n)
	}
	if c := atomic.LoadInt32(&executedCount); c != 0 {
		t.Fatalf("expected 0 executions (all interrupted by Stop()), got %d", c)
	}
}
