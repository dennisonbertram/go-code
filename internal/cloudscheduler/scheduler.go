// Package cloudscheduler provides a Docker-backed cloud workflow scheduling system.
// It enables submitting workflows for asynchronous cloud execution, monitoring
// progress via SSE, and retrieving results. Supports pluggable backends:
// Docker (local testing), Cloudflare Workers, Vercel Functions, or plain SSH boxes.
//
// Architecture:
//
//	Client → REST API → Scheduler Queue → Docker Executor → Result Store
//	                    ↑ SSE events streamed back to client
//
// A Job represents a workflow submitted for cloud execution:
//   - Created via POST /v1/cloud-jobs with a workflow name + args
//   - Queued, then picked up by a worker
//   - Executed in an isolated Docker container
//   - Results stored and retrievable via GET /v1/cloud-jobs/{id}
//   - Progress events streamed via SSE at GET /v1/cloud-jobs/{id}/events
package cloudscheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Types
// =============================================================================

// JobStatus represents the lifecycle of a cloud-scheduled job.
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// Job represents a workflow submitted for cloud execution.
type Job struct {
	ID           string            `json:"id"`
	WorkflowName string            `json:"workflow_name"`
	Args         map[string]any    `json:"args,omitempty"`
	Status       JobStatus         `json:"status"`
	Result       string            `json:"result,omitempty"`
	Error        string            `json:"error,omitempty"`
	Backend      string            `json:"backend"` // "docker", "cloudflare", "vercel", "ssh"
	Tags         map[string]string `json:"tags,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
	// Schedule is an optional future time to delay execution.
	Schedule *time.Time `json:"schedule,omitempty"`
	// RetryPolicy configures retry behavior on failure.
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`
}

// RetryPolicy configures automatic retry behavior.
type RetryPolicy struct {
	MaxRetries int           `json:"max_retries"` // default 0 (no retries)
	Backoff    time.Duration `json:"backoff"`     // delay between retries
}

// Event represents a job lifecycle event for SSE streaming.
type Event struct {
	Seq       int64          `json:"seq"`
	JobID     string         `json:"job_id"`
	Type      string         `json:"type"` // queued, started, progress, completed, failed
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// Executor runs a job on a specific backend.
type Executor interface {
	// Execute runs the job and returns the result. Blocks until completion.
	Execute(ctx context.Context, job Job) (string, error)
	// Backend returns the backend name (e.g., "docker", "cloudflare").
	Backend() string
}

// =============================================================================
// Scheduler
// =============================================================================

// Scheduler manages cloud job submission, queueing, and execution.
type Scheduler struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	events    map[string][]Event
	subs      map[string]map[chan Event]struct{}
	seqs      map[string]int64
	idCounter atomic.Int64
	queue     chan string // job IDs waiting for execution
	executors map[string]Executor
	workers   int
	stopCh    chan struct{}
}

// New creates a new Scheduler.
func New(workers int) *Scheduler {
	if workers <= 0 {
		workers = 2
	}
	s := &Scheduler{
		jobs:      make(map[string]*Job),
		events:    make(map[string][]Event),
		subs:      make(map[string]map[chan Event]struct{}),
		seqs:      make(map[string]int64),
		queue:     make(chan string, 1000),
		executors: make(map[string]Executor),
		workers:   workers,
		stopCh:    make(chan struct{}),
	}
	return s
}

// RegisterExecutor adds a backend executor to the scheduler.
func (s *Scheduler) RegisterExecutor(exec Executor) {
	s.executors[exec.Backend()] = exec
}

// Start begins processing the job queue with worker goroutines.
func (s *Scheduler) Start(ctx context.Context) {
	for i := 0; i < s.workers; i++ {
		go s.worker(ctx, i)
	}
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	close(s.stopCh)
}

// Submit queues a job for execution. Returns the created job.
func (s *Scheduler) Submit(job Job) (*Job, error) {
	if job.Backend == "" {
		job.Backend = "docker"
	}
	if _, ok := s.executors[job.Backend]; !ok {
		return nil, fmt.Errorf("no executor registered for backend %q", job.Backend)
	}
	// Auto-generate unique ID (atomic counter avoids collisions under concurrency)
	if job.ID == "" {
		job.ID = fmt.Sprintf("cloudjob-%d-%d", time.Now().UnixNano(), s.idCounter.Add(1))
	}
	job.Status = JobStatusQueued
	job.CreatedAt = time.Now().UTC()

	s.mu.Lock()
	cp := job
	s.jobs[job.ID] = &cp
	s.mu.Unlock()

	s.emit(job.ID, "queued", map[string]any{"workflow": job.WorkflowName})

	// If scheduled, delay queueing
	if job.Schedule != nil && job.Schedule.After(time.Now()) {
		go func() {
			delay := time.Until(*job.Schedule)
			log.Printf("[cloudscheduler] job %s scheduled in %v", job.ID, delay)
			time.Sleep(delay)
			s.queue <- job.ID
		}()
	} else {
		s.queue <- job.ID
	}

	return &cp, nil
}

// GetJob returns the current state of a job.
func (s *Scheduler) GetJob(jobID string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	cp := *job
	return &cp, nil
}

// ListJobs returns all jobs, optionally filtered by status.
func (s *Scheduler) ListJobs(status JobStatus) []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Job
	for _, j := range s.jobs {
		if status == "" || j.Status == status {
			result = append(result, *j)
		}
	}
	return result
}

// Cancel cancels a queued or running job.
func (s *Scheduler) Cancel(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %q not found", jobID)
	}
	if job.Status != JobStatusQueued && job.Status != JobStatusRunning {
		return fmt.Errorf("cannot cancel job with status %s", job.Status)
	}
	job.Status = JobStatusCancelled
	job.CompletedAt = timePtr(time.Now().UTC())
	s.emitLocked(jobID, "cancelled", nil)
	return nil
}

// Subscribe returns historical events and a live channel for a job.
func (s *Scheduler) Subscribe(jobID string) ([]Event, <-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history := make([]Event, len(s.events[jobID]))
	copy(history, s.events[jobID])

	ch := make(chan Event, 64)
	if _, ok := s.subs[jobID]; !ok {
		s.subs[jobID] = make(map[chan Event]struct{})
	}
	s.subs[jobID][ch] = struct{}{}

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs[jobID], ch)
		close(ch)
	}
	return history, ch, cancel
}

// =============================================================================
// Worker
// =============================================================================

func (s *Scheduler) worker(ctx context.Context, id int) {
	log.Printf("[cloudscheduler] worker %d started", id)
	for {
		select {
		case <-s.stopCh:
			log.Printf("[cloudscheduler] worker %d stopping", id)
			return
		case <-ctx.Done():
			return
		case jobID := <-s.queue:
			s.executeJob(ctx, jobID)
		}
	}
}

func (s *Scheduler) executeJob(ctx context.Context, jobID string) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if job.Status != JobStatusQueued {
		s.mu.Unlock()
		return
	}
	job.Status = JobStatusRunning
	now := time.Now().UTC()
	job.StartedAt = &now
	exec, ok := s.executors[job.Backend]
	if !ok {
		job.Status = JobStatusFailed
		job.Error = fmt.Sprintf("no executor for backend %q", job.Backend)
		s.mu.Unlock()
		s.emit(jobID, "failed", map[string]any{"error": job.Error})
		return
	}
	s.mu.Unlock()

	s.emit(jobID, "started", map[string]any{"backend": job.Backend})

	result, err := exec.Execute(ctx, *job)
	now = time.Now().UTC()

	s.mu.Lock()
	if j, ok := s.jobs[jobID]; ok {
		if err != nil {
			j.Status = JobStatusFailed
			j.Error = err.Error()
			j.CompletedAt = &now
			s.mu.Unlock()
			s.emit(jobID, "failed", map[string]any{"error": err.Error()})
			// Retry logic
			if j.RetryPolicy != nil && j.RetryPolicy.MaxRetries > 0 {
				go s.retryJob(jobID)
			}
			return
		}
		j.Status = JobStatusCompleted
		j.Result = result
		j.CompletedAt = &now
		s.mu.Unlock()
		s.emit(jobID, "completed", map[string]any{"result": result})
		return
	}
	s.mu.Unlock()
}

func (s *Scheduler) retryJob(jobID string) {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if job.RetryPolicy == nil || job.RetryPolicy.MaxRetries <= 0 {
		s.mu.Unlock()
		return
	}
	job.RetryPolicy.MaxRetries--
	backoff := job.RetryPolicy.Backoff
	if backoff == 0 {
		backoff = 5 * time.Second
	}
	job.Status = JobStatusQueued
	job.Error = ""
	s.mu.Unlock()

	time.Sleep(backoff)
	s.queue <- jobID
}

// =============================================================================
// Event emission
// =============================================================================

func (s *Scheduler) emit(jobID, typ string, payload map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitLocked(jobID, typ, payload)
}

func (s *Scheduler) emitLocked(jobID, typ string, payload map[string]any) {
	s.seqs[jobID]++
	ev := Event{
		Seq:       s.seqs[jobID],
		JobID:     jobID,
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}
	s.events[jobID] = append(s.events[jobID], ev)
	for ch := range s.subs[jobID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }
