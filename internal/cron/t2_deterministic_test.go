package cron

// T2: deterministic cron behavior tests.
//
// (a) NextRunTime arithmetic — pure function, no I/O, no sleeps, fully
//     deterministic for a fixed input time and schedule string.
// (b) fireJob-driven execution — N sequential fireJob calls record the
//     expected number of executions and advance LastRunAt/NextRunAt each
//     time. No real sleeps; no reliance on the tick loop.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// (a) NextRunTime arithmetic
// ---------------------------------------------------------------------------

func TestNextRunTime_TableDriven(t *testing.T) {
	// All fixed reference times and expected values are computed by hand from
	// standard cron semantics (5-field, robfig/cron v3 parser, UTC).
	cases := []struct {
		name     string
		schedule string
		from     time.Time
		want     time.Time
	}{
		{
			name:     "every-5-minutes_on_minute_boundary",
			schedule: "*/5 * * * *",
			from:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			want:     time.Date(2025, 1, 1, 0, 5, 0, 0, time.UTC),
		},
		{
			name:     "every-5-minutes_mid-interval",
			schedule: "*/5 * * * *",
			from:     time.Date(2025, 1, 1, 0, 3, 30, 0, time.UTC),
			want:     time.Date(2025, 1, 1, 0, 5, 0, 0, time.UTC),
		},
		{
			name:     "every-5-minutes_just-after-slot",
			schedule: "*/5 * * * *",
			from:     time.Date(2025, 1, 1, 0, 5, 1, 0, time.UTC),
			want:     time.Date(2025, 1, 1, 0, 10, 0, 0, time.UTC),
		},
		{
			name:     "every-hour_on-the-hour",
			schedule: "0 * * * *",
			from:     time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC),
			want:     time.Date(2025, 6, 15, 15, 0, 0, 0, time.UTC),
		},
		{
			name:     "every-hour_mid-hour",
			schedule: "0 * * * *",
			from:     time.Date(2025, 6, 15, 14, 45, 0, 0, time.UTC),
			want:     time.Date(2025, 6, 15, 15, 0, 0, 0, time.UTC),
		},
		{
			name:     "daily-at-midnight",
			schedule: "0 0 * * *",
			from:     time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC),
			want:     time.Date(2025, 3, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "daily-at-midnight_just-after-midnight",
			schedule: "0 0 * * *",
			from:     time.Date(2025, 3, 10, 0, 0, 1, 0, time.UTC),
			want:     time.Date(2025, 3, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "monthly-first-of-month",
			schedule: "0 0 1 * *",
			from:     time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
			want:     time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "monthly-first-of-month_on-the-day",
			schedule: "0 0 1 * *",
			from:     time.Date(2025, 2, 1, 0, 0, 1, 0, time.UTC),
			want:     time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "every-10-minutes_wrap-hour",
			schedule: "*/10 * * * *",
			from:     time.Date(2025, 4, 1, 9, 55, 0, 0, time.UTC),
			want:     time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:     "every-10-minutes_on-exact-slot",
			schedule: "*/10 * * * *",
			from:     time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC),
			want:     time.Date(2025, 4, 1, 10, 10, 0, 0, time.UTC),
		},
		{
			name:     "specific-minute-and-hour",
			schedule: "30 9 * * *",
			from:     time.Date(2025, 7, 4, 9, 30, 1, 0, time.UTC),
			want:     time.Date(2025, 7, 5, 9, 30, 0, 0, time.UTC),
		},
		{
			name:     "specific-minute-and-hour_before-slot",
			schedule: "30 9 * * *",
			from:     time.Date(2025, 7, 4, 9, 29, 59, 0, time.UTC),
			want:     time.Date(2025, 7, 4, 9, 30, 0, 0, time.UTC),
		},
		{
			name:     "end-of-year-rollover",
			schedule: "0 0 1 1 *",
			from:     time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC),
			want:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextRunTime(tc.schedule, tc.from)
			if err != nil {
				t.Fatalf("NextRunTime(%q, %v) unexpected error: %v", tc.schedule, tc.from, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("NextRunTime(%q, %v)\n  got  %v\n  want %v",
					tc.schedule, tc.from, got, tc.want)
			}
		})
	}
}

func TestNextRunTime_InvalidSchedule_ReturnsError(t *testing.T) {
	badSchedules := []string{
		"",
		"invalid",
		"* * * *",     // only 4 fields (need 5)
		"* * * * * *", // 6 fields — not accepted by 5-field parser
		"60 * * * *",  // minute out of range
		"* 25 * * *",  // hour out of range
	}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, bad := range badSchedules {
		t.Run(bad, func(t *testing.T) {
			_, err := NextRunTime(bad, from)
			if err == nil {
				t.Fatalf("NextRunTime(%q) expected error, got nil", bad)
			}
		})
	}
}

// TestNextRunTime_Idempotent confirms that calling NextRunTime twice with the
// same inputs returns the same result (pure function, no hidden state).
func TestNextRunTime_Idempotent(t *testing.T) {
	schedule := "*/15 * * * *"
	from := time.Date(2025, 5, 20, 13, 7, 0, 0, time.UTC)
	a, err := NextRunTime(schedule, from)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	b, err := NextRunTime(schedule, from)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if !a.Equal(b) {
		t.Fatalf("NextRunTime is not idempotent: %v vs %v", a, b)
	}
}

// TestNextRunTime_MonotonicSequence verifies that successive calls using the
// previous result as the new 'from' produce strictly increasing times.
func TestNextRunTime_MonotonicSequence(t *testing.T) {
	schedule := "*/5 * * * *"
	prev := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		next, err := NextRunTime(schedule, prev)
		if err != nil {
			t.Fatalf("step %d NextRunTime error: %v", i, err)
		}
		if !next.After(prev) {
			t.Fatalf("step %d: next %v is not after prev %v", i, next, prev)
		}
		prev = next
	}
}

// ---------------------------------------------------------------------------
// (b) fireJob-driven execution across N manual calls
// ---------------------------------------------------------------------------

// touchCall records one TouchJobRun invocation.
type touchCall struct {
	jobID     string
	lastRun   time.Time
	nextRun   time.Time
	updatedAt time.Time
}

// fireJobTracker collects the full sequence of TouchJobRun calls so each
// iteration's state can be inspected independently. It also serves a live
// Job via GetJobFunc, as fireJob now re-reads current job state before
// firing (BUG 2 fix) instead of trusting the stale snapshot captured at
// schedule time.
type fireJobTracker struct {
	mu          sync.Mutex
	execUpdates []Execution // all UpdateExecution calls (both status transitions)
	touches     []touchCall // one per fireJob call that actually fires
	job         Job         // the "live" job state returned by GetJob
}

func (tr *fireJobTracker) store() *mockStore {
	return &mockStore{
		GetJobFunc: func(_ context.Context, id string) (Job, error) {
			tr.mu.Lock()
			j := tr.job
			tr.mu.Unlock()
			return j, nil
		},
		CreateExecutionFunc: func(_ context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(_ context.Context, exec Execution) error {
			tr.mu.Lock()
			tr.execUpdates = append(tr.execUpdates, exec)
			tr.mu.Unlock()
			return nil
		},
		TouchJobRunFunc: func(_ context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
			tr.mu.Lock()
			tr.touches = append(tr.touches, touchCall{jobID, lastRun, nextRun, updatedAt})
			tr.mu.Unlock()
			return nil
		},
	}
}

// TestFireJob_NSequentialCallsAdvanceState fires a job N times and asserts:
//   - exactly N execution pairs (running+success) are recorded, i.e. 2*N UpdateExecution calls
//   - exactly N TouchJobRun calls are recorded
//   - each successive touch's lastRun is >= the previous one (monotonically non-decreasing)
//   - each successive touch's nextRun is computed from the stored schedule and is
//     strictly after the corresponding lastRun
func TestFireJob_NSequentialCallsAdvanceState(t *testing.T) {
	const N = 5
	schedule := "*/5 * * * *"

	tr := &fireJobTracker{}
	store := tr.store()
	executor := &mockExecutor{
		ExecuteFunc: func(_ context.Context, _ Job) (string, error) {
			return "output", nil
		},
	}

	// Start clock at a minute boundary so NextRunTime results are easy to reason about.
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := newMockClock(baseTime)

	cfg := SchedulerConfig{
		MaxConcurrent: 1,
		Jitter:        JitterConfig{Enabled: false},
	}
	s := NewScheduler(store, executor, clock, cfg)
	s.sleepFn = func(time.Duration) {} // no-op: jitter disabled but guard anyway

	job := Job{
		ID:         "sequential-fire-job",
		Name:       "sequential-fire",
		Schedule:   schedule,
		ExecType:   ExecTypeShell,
		ExecConfig: `{"command":"echo test"}`,
		Status:     StatusActive,
		TimeoutSec: 30,
		NextRunAt:  baseTime.Add(5 * time.Minute),
	}
	tr.mu.Lock()
	tr.job = job
	tr.mu.Unlock()

	// Fire N times sequentially, advancing the mock clock by 5 minutes each time
	// so each iteration has a distinct and predictable endTime.
	for i := 0; i < N; i++ {
		fireTime := baseTime.Add(time.Duration(i+1) * 5 * time.Minute)
		clock.Set(fireTime)
		s.fireJob(job, 0)
		s.wg.Wait() // ensure goroutine completes before next iteration
	}

	tr.mu.Lock()
	execUpdates := make([]Execution, len(tr.execUpdates))
	copy(execUpdates, tr.execUpdates)
	touches := make([]touchCall, len(tr.touches))
	copy(touches, tr.touches)
	tr.mu.Unlock()

	// --- Assertion 1: execution update count ---
	// Each fireJob produces exactly 2 UpdateExecution calls: running then success.
	if got := len(execUpdates); got != 2*N {
		t.Fatalf("expected %d execution updates (2 per fireJob), got %d", 2*N, got)
	}

	// --- Assertion 2: TouchJobRun call count ---
	if got := len(touches); got != N {
		t.Fatalf("expected %d TouchJobRun calls (1 per fireJob), got %d", N, got)
	}

	// --- Assertion 3: execution status pairs ---
	for i := 0; i < N; i++ {
		running := execUpdates[2*i]
		success := execUpdates[2*i+1]
		if running.Status != ExecStatusRunning {
			t.Errorf("iteration %d: first exec update should be running, got %q", i, running.Status)
		}
		if success.Status != ExecStatusSuccess {
			t.Errorf("iteration %d: second exec update should be success, got %q", i, success.Status)
		}
	}

	// --- Assertion 4: lastRun is monotonically non-decreasing ---
	for i := 1; i < N; i++ {
		prev := touches[i-1].lastRun
		cur := touches[i].lastRun
		if cur.Before(prev) {
			t.Errorf("iteration %d: lastRun went backwards: %v < %v", i, cur, prev)
		}
	}

	// --- Assertion 5: nextRun is correctly recomputed after each execution ---
	// nextRun = NextRunTime(schedule, endTime) where endTime = clock.Now() at fire time.
	for i, tc := range touches {
		endTime := baseTime.Add(time.Duration(i+1) * 5 * time.Minute)
		want, err := NextRunTime(schedule, endTime)
		if err != nil {
			t.Fatalf("iteration %d: NextRunTime error: %v", i, err)
		}
		if tc.nextRun.IsZero() {
			t.Errorf("iteration %d: nextRun is zero", i)
			continue
		}
		if !tc.nextRun.Equal(want) {
			t.Errorf("iteration %d: nextRun = %v, want %v (endTime %v, schedule %q)",
				i, tc.nextRun, want, endTime, schedule)
		}
	}

	// --- Assertion 6: each nextRun is strictly after the corresponding lastRun ---
	for i, tc := range touches {
		if !tc.nextRun.After(tc.lastRun) {
			t.Errorf("iteration %d: nextRun %v is not after lastRun %v",
				i, tc.nextRun, tc.lastRun)
		}
	}
}

// TestFireJob_FailedExecution_NextRunAtStillAdvanced verifies that on executor
// failure the touched nextRun is still recomputed (P1 fix applies regardless
// of success/failure — scheduler.go unconditionally calls TouchJobRun after
// endTime is known; only a schedule parse error leaves nextRun unchanged).
func TestFireJob_FailedExecution_NextRunAtStillAdvanced(t *testing.T) {
	var mu sync.Mutex
	var touched touchCall
	var touchCalled bool

	schedule := "0 * * * *"
	job := Job{
		ID:         "fail-job",
		Name:       "fail",
		Schedule:   schedule,
		ExecType:   ExecTypeShell,
		ExecConfig: `{"command":"false"}`,
		Status:     StatusActive,
	}

	store := &mockStore{
		GetJobFunc: func(_ context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(_ context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(_ context.Context, exec Execution) error { return nil },
		TouchJobRunFunc: func(_ context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
			mu.Lock()
			touched = touchCall{jobID, lastRun, nextRun, updatedAt}
			touchCalled = true
			mu.Unlock()
			return nil
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(_ context.Context, _ Job) (string, error) {
			return "", errTestFailure("executor failed deliberately")
		},
	}

	fireTime := time.Date(2025, 2, 14, 8, 0, 0, 0, time.UTC)
	clock := newMockClock(fireTime)
	cfg := SchedulerConfig{MaxConcurrent: 1, Jitter: JitterConfig{Enabled: false}}
	s := NewScheduler(store, executor, clock, cfg)
	s.sleepFn = func(time.Duration) {}

	s.fireJob(job, 0)
	s.wg.Wait()

	mu.Lock()
	got := touched
	calledOK := touchCalled
	mu.Unlock()

	if !calledOK {
		t.Fatal("expected TouchJobRun to be called even after a failed execution")
	}

	want, err := NextRunTime(schedule, fireTime)
	if err != nil {
		t.Fatalf("NextRunTime: %v", err)
	}
	if got.nextRun.IsZero() {
		t.Fatal("nextRun is zero after failed execution")
	}
	if !got.nextRun.Equal(want) {
		t.Fatalf("nextRun = %v, want %v", got.nextRun, want)
	}
}

// errTestFailure is a lightweight error for test use.
type errTestFailure string

func (e errTestFailure) Error() string { return string(e) }

// TestFireJob_InvalidSchedule_NextRunAtLeftUnchanged verifies that when the
// job's schedule cannot be parsed by NextRunTime, TouchJobRun is still
// called but nextRun is left at the job's current NextRunAt value (zero in
// this case).
func TestFireJob_InvalidSchedule_NextRunAtLeftUnchanged(t *testing.T) {
	var mu sync.Mutex
	var touched touchCall
	touchCalled := false

	// We can't add an invalid schedule via AddJob (it would error), so we
	// call fireJob directly with a job whose Schedule field is
	// intentionally invalid.
	job := Job{
		ID:         "bad-sched-job",
		Name:       "bad-schedule",
		Schedule:   "INVALID_SCHED",
		ExecType:   ExecTypeShell,
		ExecConfig: `{"command":"echo ok"}`,
		Status:     StatusActive,
	}

	store := &mockStore{
		GetJobFunc: func(_ context.Context, id string) (Job, error) {
			return job, nil
		},
		CreateExecutionFunc: func(_ context.Context, exec Execution) (Execution, error) {
			return exec, nil
		},
		UpdateExecutionFunc: func(_ context.Context, exec Execution) error { return nil },
		TouchJobRunFunc: func(_ context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
			mu.Lock()
			touched = touchCall{jobID, lastRun, nextRun, updatedAt}
			touchCalled = true
			mu.Unlock()
			return nil
		},
	}
	executor := &mockExecutor{
		ExecuteFunc: func(_ context.Context, _ Job) (string, error) {
			return "ok", nil
		},
	}

	fireTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newMockClock(fireTime)
	cfg := SchedulerConfig{MaxConcurrent: 1, Jitter: JitterConfig{Enabled: false}}
	s := NewScheduler(store, executor, clock, cfg)
	s.sleepFn = func(time.Duration) {}

	s.fireJob(job, 0)
	s.wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !touchCalled {
		t.Fatal("expected TouchJobRun to be called even with an invalid schedule")
	}
	// nextRun should remain zero because NextRunTime returned an error and
	// job.NextRunAt (the fallback) was never set.
	if !touched.nextRun.IsZero() {
		t.Fatalf("expected nextRun to remain zero for invalid schedule, got %v", touched.nextRun)
	}
}
