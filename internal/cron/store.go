package cron

import (
	"context"
	"time"
)

// Store is the persistence interface for cron jobs and executions.
type Store interface {
	Migrate(ctx context.Context) error

	CreateJob(ctx context.Context, job Job) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	GetJobByName(ctx context.Context, name string) (Job, error)
	ListJobs(ctx context.Context) ([]Job, error)
	UpdateJob(ctx context.Context, job Job) error
	// TouchJobRun updates only the run-tracking columns (last_run_at,
	// next_run_at, updated_at) for a job. Unlike UpdateJob, it never
	// touches schedule, execution config, status, timeout, or tags, so it
	// is safe to call from the scheduler after a fire without risking a
	// silent revert of concurrent user edits or resurrecting a paused job.
	TouchJobRun(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error
	DeleteJob(ctx context.Context, id string) error // soft delete

	CreateExecution(ctx context.Context, exec Execution) (Execution, error)
	UpdateExecution(ctx context.Context, exec Execution) error
	ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]Execution, error)

	Close() error
}
