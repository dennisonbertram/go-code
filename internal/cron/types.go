package cron

import (
	"database/sql"
	"errors"
	"time"
)

var ErrJobNotFound = errors.New("cron job not found")

func IsJobNotFound(err error) bool {
	return errors.Is(err, ErrJobNotFound) || errors.Is(err, sql.ErrNoRows)
}

// Job status constants
const (
	StatusActive  = "active"
	StatusPaused  = "paused"
	StatusDeleted = "deleted"
)

// Execution status constants
const (
	ExecStatusPending = "pending"
	ExecStatusRunning = "running"
	ExecStatusSuccess = "success"
	ExecStatusFailed  = "failed"
	ExecStatusTimeout = "timeout"
)

// Execution type constants
const (
	ExecTypeShell   = "shell"
	ExecTypeHarness = "harness"
)

// Job represents a scheduled job.
type Job struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Name       string    `json:"name"`
	Schedule   string    `json:"schedule"`
	ExecType   string    `json:"execution_type"`
	ExecConfig string    `json:"execution_config"` // JSON blob
	Status     string    `json:"status"`
	TimeoutSec int       `json:"timeout_seconds"`
	Tags       string    `json:"tags"` // comma-separated
	NextRunAt  time.Time `json:"next_run_at"`
	LastRunAt  time.Time `json:"last_run_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Execution represents a single run of a job.
type Execution struct {
	ID            string    `json:"id"`
	JobID         string    `json:"job_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Status        string    `json:"status"`
	RunID         string    `json:"run_id,omitempty"` // harness run ID
	OutputSummary string    `json:"output_summary,omitempty"`
	Error         string    `json:"error,omitempty"`
	DurationMs    int64     `json:"duration_ms"`
}

// CreateJobRequest is the request payload for creating a job.
type CreateJobRequest struct {
	TenantID   string `json:"tenant_id,omitempty"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	ExecType   string `json:"execution_type"`
	ExecConfig string `json:"execution_config"`
	TimeoutSec int    `json:"timeout_seconds,omitempty"`
	Tags       string `json:"tags,omitempty"`
}

// UpdateJobRequest is the request payload for updating a job.
type UpdateJobRequest struct {
	Schedule   *string `json:"schedule,omitempty"`
	ExecConfig *string `json:"execution_config,omitempty"`
	Status     *string `json:"status,omitempty"`
	TimeoutSec *int    `json:"timeout_seconds,omitempty"`
	Tags       *string `json:"tags,omitempty"`
}

// ListExecutionsRequest is the request payload for listing executions.
type ListExecutionsRequest struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}
