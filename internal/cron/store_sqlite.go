package cron

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS cron_jobs (
	job_id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL UNIQUE,
	schedule TEXT NOT NULL,
	execution_type TEXT NOT NULL,
	execution_config TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL DEFAULT 'active',
	timeout_seconds INTEGER NOT NULL DEFAULT 30,
	tags TEXT NOT NULL DEFAULT '',
	next_run_at TIMESTAMP NOT NULL,
	last_run_at TIMESTAMP,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS cron_executions (
	execution_id TEXT PRIMARY KEY,
	job_id TEXT NOT NULL,
	started_at TIMESTAMP NOT NULL,
	finished_at TIMESTAMP,
	status TEXT NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	output_summary TEXT NOT NULL DEFAULT '',
	error_text TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (job_id) REFERENCES cron_jobs(job_id)
);

CREATE INDEX IF NOT EXISTS idx_cron_executions_job_id ON cron_executions(job_id);
CREATE INDEX IF NOT EXISTS idx_cron_executions_started_at ON cron_executions(started_at);
`

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Migrate creates the schema tables.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, sqliteSchema)
	if err != nil {
		return fmt.Errorf("sqlite migrate: %w", err)
	}
	if err := s.ensureCronJobsTenantColumn(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) ensureCronJobsTenantColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(cron_jobs)`)
	if err != nil {
		return fmt.Errorf("inspect cron_jobs schema: %w", err)
	}

	hasTenantID := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan cron_jobs schema: %w", err)
		}
		if name == "tenant_id" {
			hasTenantID = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("inspect cron_jobs schema rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close cron_jobs schema rows: %w", err)
	}
	if !hasTenantID {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE cron_jobs ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add cron_jobs tenant_id: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_cron_jobs_tenant_id ON cron_jobs(tenant_id)`); err != nil {
		return fmt.Errorf("index cron_jobs tenant_id: %w", err)
	}
	return nil
}

// CreateJob inserts a new job.
func (s *SQLiteStore) CreateJob(ctx context.Context, job Job) (Job, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO cron_jobs (
	job_id, tenant_id, name, schedule, execution_type, execution_config,
	status, timeout_seconds, tags, next_run_at, last_run_at,
	created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		job.ID,
		job.TenantID,
		job.Name,
		job.Schedule,
		job.ExecType,
		job.ExecConfig,
		job.Status,
		job.TimeoutSec,
		job.Tags,
		nowString(job.NextRunAt),
		nullableTimeString(job.LastRunAt),
		nowString(job.CreatedAt),
		nowString(job.UpdatedAt),
	)
	if err != nil {
		return Job{}, fmt.Errorf("insert job: %w", err)
	}
	return job, nil
}

// GetJob retrieves a job by ID.
func (s *SQLiteStore) GetJob(ctx context.Context, id string) (Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, `
SELECT job_id, tenant_id, name, schedule, execution_type, execution_config,
	status, timeout_seconds, tags, next_run_at, last_run_at,
	created_at, updated_at
FROM cron_jobs
WHERE job_id = ? AND status != ?
`, id, StatusDeleted))
}

// GetJobByName retrieves a job by name.
func (s *SQLiteStore) GetJobByName(ctx context.Context, name string) (Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, `
SELECT job_id, tenant_id, name, schedule, execution_type, execution_config,
	status, timeout_seconds, tags, next_run_at, last_run_at,
	created_at, updated_at
FROM cron_jobs
WHERE name = ? AND status != ?
`, name, StatusDeleted))
}

// ListJobs returns all non-deleted jobs.
func (s *SQLiteStore) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT job_id, tenant_id, name, schedule, execution_type, execution_config,
	status, timeout_seconds, tags, next_run_at, last_run_at,
	created_at, updated_at
FROM cron_jobs
WHERE status != ?
ORDER BY created_at DESC
`, StatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		job, err := s.scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// UpdateJob updates a job record.
func (s *SQLiteStore) UpdateJob(ctx context.Context, job Job) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE cron_jobs
SET tenant_id = ?, name = ?, schedule = ?, execution_type = ?, execution_config = ?,
	status = ?, timeout_seconds = ?, tags = ?, next_run_at = ?,
	last_run_at = ?, updated_at = ?
WHERE job_id = ?
`,
		job.TenantID,
		job.Name,
		job.Schedule,
		job.ExecType,
		job.ExecConfig,
		job.Status,
		job.TimeoutSec,
		job.Tags,
		nowString(job.NextRunAt),
		nullableTimeString(job.LastRunAt),
		nowString(job.UpdatedAt),
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return nil
}

// DeleteJob performs a soft delete by setting status to deleted.
// It also renames the job to free the unique name constraint,
// allowing a new job with the same name to be created later.
func (s *SQLiteStore) DeleteJob(ctx context.Context, id string) error {
	now := time.Now().UTC()
	suffix := fmt.Sprintf("_deleted_%d", now.UnixNano())
	res, err := s.db.ExecContext(ctx, `
UPDATE cron_jobs SET status = ?, name = name || ?, updated_at = ? WHERE job_id = ?
`, StatusDeleted, suffix, nowString(now), id)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete job rows affected: %w", err)
	}
	if rows == 0 {
		return ErrJobNotFound
	}
	return nil
}

// CreateExecution inserts a new execution record.
func (s *SQLiteStore) CreateExecution(ctx context.Context, exec Execution) (Execution, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO cron_executions (
	execution_id, job_id, started_at, finished_at, status,
	run_id, output_summary, error_text, duration_ms
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		exec.ID,
		exec.JobID,
		nowString(exec.StartedAt),
		nullableTimeString(exec.FinishedAt),
		exec.Status,
		exec.RunID,
		exec.OutputSummary,
		exec.Error,
		exec.DurationMs,
	)
	if err != nil {
		return Execution{}, fmt.Errorf("insert execution: %w", err)
	}
	return exec, nil
}

// UpdateExecution updates an execution record.
func (s *SQLiteStore) UpdateExecution(ctx context.Context, exec Execution) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE cron_executions
SET finished_at = ?, status = ?, run_id = ?, output_summary = ?,
	error_text = ?, duration_ms = ?
WHERE execution_id = ?
`,
		nullableTimeString(exec.FinishedAt),
		exec.Status,
		exec.RunID,
		exec.OutputSummary,
		exec.Error,
		exec.DurationMs,
		exec.ID,
	)
	if err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	return nil
}

// ListExecutions returns executions for a job, ordered by started_at desc.
func (s *SQLiteStore) ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]Execution, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT execution_id, job_id, started_at, finished_at, status,
	run_id, output_summary, error_text, duration_ms
FROM cron_executions
WHERE job_id = ?
ORDER BY started_at DESC
LIMIT ? OFFSET ?
`, jobID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()

	var execs []Execution
	for rows.Next() {
		var e Execution
		var startedText string
		var finishedText sql.NullString
		if err := rows.Scan(
			&e.ID, &e.JobID, &startedText, &finishedText,
			&e.Status, &e.RunID, &e.OutputSummary, &e.Error, &e.DurationMs,
		); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.StartedAt, _ = time.Parse(time.RFC3339Nano, startedText)
		if finishedText.Valid {
			e.FinishedAt, _ = time.Parse(time.RFC3339Nano, finishedText.String)
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

// scanJob scans a single job row from QueryRow.
func (s *SQLiteStore) scanJob(row *sql.Row) (Job, error) {
	var job Job
	var nextRunText, createdText, updatedText string
	var lastRunText sql.NullString
	if err := row.Scan(
		&job.ID, &job.TenantID, &job.Name, &job.Schedule, &job.ExecType, &job.ExecConfig,
		&job.Status, &job.TimeoutSec, &job.Tags, &nextRunText, &lastRunText,
		&createdText, &updatedText,
	); err != nil {
		return Job{}, err
	}
	job.NextRunAt, _ = time.Parse(time.RFC3339Nano, nextRunText)
	if lastRunText.Valid {
		job.LastRunAt, _ = time.Parse(time.RFC3339Nano, lastRunText.String)
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdText)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedText)
	return job, nil
}

// scanJobRow scans a single job from sql.Rows.
func (s *SQLiteStore) scanJobRow(rows *sql.Rows) (Job, error) {
	var job Job
	var nextRunText, createdText, updatedText string
	var lastRunText sql.NullString
	if err := rows.Scan(
		&job.ID, &job.TenantID, &job.Name, &job.Schedule, &job.ExecType, &job.ExecConfig,
		&job.Status, &job.TimeoutSec, &job.Tags, &nextRunText, &lastRunText,
		&createdText, &updatedText,
	); err != nil {
		return Job{}, fmt.Errorf("scan job: %w", err)
	}
	job.NextRunAt, _ = time.Parse(time.RFC3339Nano, nextRunText)
	if lastRunText.Valid {
		job.LastRunAt, _ = time.Parse(time.RFC3339Nano, lastRunText.String)
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdText)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedText)
	return job, nil
}

func nowString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableTimeString(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return nowString(t)
}
