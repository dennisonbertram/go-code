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
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.wg.Wait()
}

// AddJob registers a job with the cron scheduler.
func (s *Scheduler) AddJob(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Compute and cache a deterministic jitter offset for this job.
	s.jitterCache[jitterCacheKey(job.ID, job.Schedule)] = computeJitter(
		s.jitterCfg, job.ID, job.Schedule,
	)

	// Capture job for the closure.
	j := job
	entryID, err := s.cron.AddFunc(job.Schedule, func() {
		s.fireJob(j)
	})
	if err != nil {
		return fmt.Errorf("add cron entry: %w", err)
	}
	s.entries[job.ID] = entryID
	return nil
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
func (s *Scheduler) fireJob(job Job) {
	ctx := context.Background()
	now := s.clock.Now()

	// Apply jitter delay before execution work.
	// The base jitter offset is computed deterministically at registration time.
	// Minute-mark avoidance is applied now using the actual fire time.
	jitterKey := jitterCacheKey(job.ID, job.Schedule)
	baseJitter := s.jitterCache[jitterKey]
	if baseJitter > 0 {
		jitterOffset := avoidMinuteMarks(baseJitter, now, s.jitterCfg.AvoidMarks)
		if s.jitterCfg.LogJitteredTimes {
			log.Printf("cron: job %s jittered by %v (original schedule: %s, base jitter: %v)",
				job.ID, jitterOffset, job.Schedule, baseJitter)
		}
		s.sleepFn(jitterOffset)
	}

	exec := Execution{
		ID:        uuid.New().String(),
		JobID:     job.ID,
		StartedAt: now,
		Status:    ExecStatusPending,
	}

	exec, err := s.store.CreateExecution(ctx, exec)
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

		// Update job's last_run_at.
		job.LastRunAt = endTime
		job.UpdatedAt = endTime
		if updateErr := s.store.UpdateJob(ctx, job); updateErr != nil {
			log.Printf("cron: failed to update job %s last_run_at: %v", job.ID, updateErr)
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
