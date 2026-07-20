package cron

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	robfigcron "github.com/robfig/cron/v3"
)

// Scheduler manages scheduled jobs using robfig/cron.
type Scheduler struct {
	store       Store
	executor    Executor
	clock       Clock
	cron        *robfigcron.Cron
	sem         chan struct{} // concurrency semaphore
	wg          sync.WaitGroup
	mu          sync.Mutex
	entries     map[string]robfigcron.EntryID // jobID -> entryID
	jitterCfg   JitterConfig
	jitterCache map[string]time.Duration // jobID|schedule -> jitter offset
	sleepFn     func(time.Duration)      // injectable sleep for testing; defaults to time.Sleep
	done        chan struct{}            // closed by Stop to interrupt in-flight jitter waits
	stopOnce    sync.Once                // guards closing done so a double Stop cannot panic
}

// SchedulerConfig holds scheduler configuration.
type SchedulerConfig struct {
	MaxConcurrent int
	Jitter        JitterConfig
}

// NewScheduler creates a new Scheduler.
func NewScheduler(store Store, executor Executor, clock Clock, cfg SchedulerConfig) *Scheduler {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 5
	}
	if cfg.Jitter.MinSec <= 0 && cfg.Jitter.MaxSec <= 0 {
		cfg.Jitter = DefaultJitterConfig()
	}
	c := robfigcron.New(
		robfigcron.WithLocation(time.UTC),
		robfigcron.WithParser(robfigcron.NewParser(
			robfigcron.Minute|robfigcron.Hour|robfigcron.Dom|robfigcron.Month|robfigcron.Dow,
		)),
	)
	return &Scheduler{
		store:       store,
		executor:    executor,
		clock:       clock,
		cron:        c,
		sem:         make(chan struct{}, cfg.MaxConcurrent),
		entries:     make(map[string]robfigcron.EntryID),
		jitterCfg:   cfg.Jitter,
		jitterCache: make(map[string]time.Duration),
		sleepFn:     time.Sleep,
		done:        make(chan struct{}),
	}
}

// Start loads all active jobs from the store and starts the cron scheduler.
func (s *Scheduler) Start(ctx context.Context) error {
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return fmt.Errorf("load jobs: %w", err)
	}
	for _, job := range jobs {
		if job.Status != StatusActive {
			continue
		}
		if err := s.AddJob(job); err != nil {
			log.Printf("cron: failed to add job %s (%s): %v", job.ID, job.Name, err)
		}
	}
	s.cron.Start()
	return nil
}

// Stop stops the cron scheduler and waits for in-flight executions.
//
// Stop first tells robfig/cron to stop dispatching new ticks (s.cron.Stop
// returns a context that becomes Done once any invocation already in
// progress returns). It then closes s.done — BEFORE waiting on that
// context — so that any fireJob call currently blocked in its jitter wait
// is interrupted immediately and returns without executing the job. Only
// after that does Stop wait for cron's context and then for s.wg, which
// tracks the async goroutines doing the actual execution work (those are
// allowed to run to completion; only the pre-execution jitter wait is
// abandoned on shutdown). Closing done is idempotent (sync.Once) so a
// double Stop call cannot panic.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	s.stopOnce.Do(func() { close(s.done) })
	<-ctx.Done()
	s.wg.Wait()
}

// AddJob registers a job with the cron scheduler.
func (s *Scheduler) AddJob(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Compute a deterministic jitter offset for this job. jitterCache is
	// retained (and still populated here, under s.mu) so tests and any
	// future callers can introspect the cached value, but fireJob itself
	// no longer reads this map — the computed jitter is captured directly
	// in the closure below and passed to fireJob as a parameter. This
	// avoids the unsynchronized read that previously raced with this write
	// (fireJob ran on the robfig/cron goroutine with no lock held).
	jitter := computeJitter(s.jitterCfg, job.ID, job.Schedule)
	s.jitterCache[jitterCacheKey(job.ID, job.Schedule)] = jitter

	// Capture job and its jitter offset for the closure.
	j := job
	entryID, err := s.cron.AddFunc(job.Schedule, func() {
		s.fireJob(j, jitter)
	})
	if err != nil {
		return fmt.Errorf("add cron entry: %w", err)
	}
	s.entries[job.ID] = entryID
	return nil
}

// HasEntry reports whether jobID is currently registered with the live
// cron dispatcher — i.e. it will fire on its schedule. A paused or
// removed job is not registered. Exposed primarily so callers (including
// tests outside this package) can observe live scheduler state without
// reaching into unexported fields.
func (s *Scheduler) HasEntry(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[jobID]
	return ok
}

// RemoveJob removes a job from the cron scheduler.
func (s *Scheduler) RemoveJob(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.entries[jobID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, jobID)
	}
}

// UpdateJobSchedule removes the old cron entry and adds a new one.
func (s *Scheduler) UpdateJobSchedule(job Job) error {
	s.RemoveJob(job.ID)
	// Recompute jitter for the new schedule.
	s.mu.Lock()
	s.jitterCache[jitterCacheKey(job.ID, job.Schedule)] = computeJitter(
		s.jitterCfg, job.ID, job.Schedule,
	)
	s.mu.Unlock()
	return s.AddJob(job)
}

// fireJob executes a job: creates an execution record, runs the executor,
// and updates the execution and job records.
//
// jitter is the base jitter offset computed once by AddJob at registration
// time. fireJob deliberately does NOT read s.jitterCache itself: fireJob
// runs on the robfig/cron dispatch goroutine outside of s.mu, and reading
// the cache concurrently with AddJob's locked write previously caused a
// fatal, unrecoverable "concurrent map read and map write" runtime error.
func (s *Scheduler) fireJob(job Job, jitter time.Duration) {
	ctx := context.Background()
	now := s.clock.Now()

	// Apply jitter delay before execution work.
	// The base jitter offset is computed deterministically at registration time.
	// Minute-mark avoidance is applied now using the actual fire time.
	baseJitter := jitter
	if baseJitter > 0 {
		jitterOffset := avoidMinuteMarks(baseJitter, now, s.jitterCfg.AvoidMarks)
		if s.jitterCfg.LogJitteredTimes {
			log.Printf("cron: job %s jittered by %v (original schedule: %s, base jitter: %v)",
				job.ID, jitterOffset, job.Schedule, baseJitter)
		}

		// The jitter wait must be interruptible by Stop(), otherwise
		// shutdown blocks for up to the full jitter window (which can be
		// minutes with the default jitter config) before the HTTP server
		// even begins draining. s.sleepFn is run in a background
		// goroutine so tests can keep injecting a fast/no-op sleep, while
		// production (sleepFn == time.Sleep) still returns from fireJob
		// promptly on shutdown: if s.done closes first, fireJob abandons
		// this fire entirely rather than executing the job mid-shutdown.
		sleepDone := make(chan struct{})
		go func() {
			s.sleepFn(jitterOffset)
			close(sleepDone)
		}()
		select {
		case <-sleepDone:
		case <-s.done:
			log.Printf("cron: shutdown in progress, skipping fire for job %s", job.ID)
			return
		}
	}

	// Re-read the job's current state from the store before firing. The
	// job value captured in AddJob's closure can be arbitrarily stale by
	// the time the timer/jitter wait elapses: the schedule, execution
	// config, timeout, tags, or status may have changed (e.g. the job may
	// have been paused or deleted). Firing based on the stale snapshot
	// would ignore those edits and could resurrect a paused job.
	current, err := s.store.GetJob(ctx, job.ID)
	if err != nil {
		log.Printf("cron: skipping fire for job %s: failed to reload current state: %v", job.ID, err)
		return
	}
	if current.Status != StatusActive {
		log.Printf("cron: skipping fire for job %s: no longer active (status=%s)", job.ID, current.Status)
		return
	}
	job = current

	exec := Execution{
		ID:        uuid.New().String(),
		JobID:     job.ID,
		StartedAt: now,
		Status:    ExecStatusPending,
	}

	exec, err = s.store.CreateExecution(ctx, exec)
	if err != nil {
		log.Printf("cron: failed to create execution for job %s: %v", job.ID, err)
		return
	}

	// Acquire semaphore to limit concurrency.
	s.sem <- struct{}{}
	s.wg.Add(1)

	go func() {
		defer func() {
			<-s.sem
			s.wg.Done()
		}()

		// Mark as running.
		exec.Status = ExecStatusRunning
		if updateErr := s.store.UpdateExecution(ctx, exec); updateErr != nil {
			log.Printf("cron: failed to update execution %s to running: %v", exec.ID, updateErr)
		}

		startTime := s.clock.Now()
		output, execErr := s.executor.Execute(ctx, job)
		endTime := s.clock.Now()

		exec.FinishedAt = endTime
		exec.DurationMs = endTime.Sub(startTime).Milliseconds()
		exec.OutputSummary = output

		if execErr != nil {
			exec.Error = execErr.Error()
			if isTimeoutError(execErr) {
				exec.Status = ExecStatusTimeout
			} else {
				exec.Status = ExecStatusFailed
			}
		} else {
			exec.Status = ExecStatusSuccess
		}

		if updateErr := s.store.UpdateExecution(ctx, exec); updateErr != nil {
			log.Printf("cron: failed to update execution %s: %v", exec.ID, updateErr)
		}

		// Record last_run_at and recompute next_run_at using a targeted
		// update that only touches run-tracking columns. This must NOT be
		// a full-object UpdateJob write: job is still the state read at
		// the top of fireJob, and by the time execution finishes it may
		// again be stale relative to concurrent edits. TouchJobRun never
		// clobbers schedule, execution config, status, timeout, or tags.
		nextRun := job.NextRunAt
		if next, parseErr := NextRunTime(job.Schedule, endTime); parseErr == nil {
			nextRun = next
		}
		// On schedule parse error, NextRunAt is left unchanged.
		if touchErr := s.store.TouchJobRun(ctx, job.ID, endTime, nextRun, endTime); touchErr != nil {
			log.Printf("cron: failed to touch job %s last_run_at: %v", job.ID, touchErr)
		}
	}()
}

// isTimeoutError checks if an error message indicates a timeout.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return len(msg) >= 7 && containsTimeout(msg)
}

func containsTimeout(s string) bool {
	for i := 0; i <= len(s)-7; i++ {
		if s[i:i+7] == "timed o" {
			return true
		}
	}
	return false
}
