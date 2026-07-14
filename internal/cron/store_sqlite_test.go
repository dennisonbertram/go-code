package cron

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test_cron.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNewSQLiteStore_EmptyPath(t *testing.T) {
	_, err := NewSQLiteStore("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestNewSQLiteStore_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "deep", "test.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	store.Close()
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	store := newTestStore(t)
	// Migrate again should not error.
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestMigrate_AddsTenantIDToExistingCronJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy_cron.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
CREATE TABLE cron_jobs (
	job_id TEXT PRIMARY KEY,
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
)`); err != nil {
		t.Fatalf("create legacy cron_jobs: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO cron_jobs (
	job_id, name, schedule, execution_type, execution_config,
	status, timeout_seconds, tags, next_run_at, last_run_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-job", "legacy", "* * * * *", ExecTypeShell, "{}", StatusActive, 30, "",
		nowString(now), nil, nowString(now), nowString(now),
	); err != nil {
		t.Fatalf("insert legacy job: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	got, err := store.GetJob(ctx, "legacy-job")
	if err != nil {
		t.Fatalf("GetJob legacy: %v", err)
	}
	if got.TenantID != "" {
		t.Fatalf("expected legacy tenant to backfill empty, got %q", got.TenantID)
	}
}

func TestCreateJob_GetJob(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("test-create")

	created, err := store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if created.ID != job.ID {
		t.Fatalf("expected ID %s, got %s", job.ID, created.ID)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Name != job.Name {
		t.Fatalf("expected name %s, got %s", job.Name, got.Name)
	}
	if got.Schedule != job.Schedule {
		t.Fatalf("expected schedule %s, got %s", job.Schedule, got.Schedule)
	}
	if got.ExecType != job.ExecType {
		t.Fatalf("expected exec_type %s, got %s", job.ExecType, got.ExecType)
	}
	if got.Status != StatusActive {
		t.Fatalf("expected status %s, got %s", StatusActive, got.Status)
	}
}

func TestCreateJob_PreservesTenantID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("tenant-owned")
	job.TenantID = "tenant-alpha"

	created, err := store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if created.TenantID != "tenant-alpha" {
		t.Fatalf("expected created tenant tenant-alpha, got %q", created.TenantID)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.TenantID != "tenant-alpha" {
		t.Fatalf("expected fetched tenant tenant-alpha, got %q", got.TenantID)
	}

	got.Tags = "updated"
	got.UpdatedAt = time.Now().UTC()
	if err := store.UpdateJob(ctx, got); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	updated, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob updated: %v", err)
	}
	if updated.TenantID != "tenant-alpha" {
		t.Fatalf("expected updated tenant tenant-alpha, got %q", updated.TenantID)
	}

	jobs, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].TenantID != "tenant-alpha" {
		t.Fatalf("expected listed tenant tenant-alpha, got %#v", jobs)
	}
}

func TestGetJobByName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("find-by-name")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := store.GetJobByName(ctx, "find-by-name")
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}
	if got.ID != job.ID {
		t.Fatalf("expected ID %s, got %s", job.ID, got.ID)
	}
}

func TestCreateJob_UniqueNameConstraint(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	job1 := testJob("unique-name")
	if _, err := store.CreateJob(ctx, job1); err != nil {
		t.Fatalf("CreateJob first: %v", err)
	}

	job2 := testJob("unique-name")
	_, err := store.CreateJob(ctx, job2)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestListJobs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		job := testJob("list-" + uuid.New().String()[:8])
		if _, err := store.CreateJob(ctx, job); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
	}

	jobs, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
}

func TestUpdateJob(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("update-me")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job.Schedule = "0 * * * *"
	job.Tags = "updated"
	job.UpdatedAt = time.Now().UTC()
	if err := store.UpdateJob(ctx, job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Schedule != "0 * * * *" {
		t.Fatalf("expected updated schedule, got %s", got.Schedule)
	}
	if got.Tags != "updated" {
		t.Fatalf("expected updated tags, got %s", got.Tags)
	}
}

// TestTouchJobRun_OnlyUpdatesRunTrackingColumns (BUG 2 fix) verifies that
// TouchJobRun updates last_run_at, next_run_at, and updated_at, but leaves
// every other column (schedule, status, tags, execution config, timeout)
// exactly as it was — even when the in-memory Job struct passed elsewhere
// disagrees. This is the core guarantee that lets the scheduler record a
// fire without risking a silent revert of concurrent edits or resurrecting
// a paused job.
func TestTouchJobRun_OnlyUpdatesRunTrackingColumns(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("touch-run-me")
	job.Status = StatusPaused
	job.Tags = "keep-me"
	job.Schedule = "0 * * * *"

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	lastRun := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	nextRun := time.Date(2025, 3, 1, 11, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 3, 1, 10, 0, 1, 0, time.UTC)

	if err := store.TouchJobRun(ctx, job.ID, lastRun, nextRun, updatedAt); err != nil {
		t.Fatalf("TouchJobRun: %v", err)
	}

	// GetJob excludes deleted jobs but not paused ones, so this should succeed.
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if !got.LastRunAt.Equal(lastRun) {
		t.Errorf("expected LastRunAt %v, got %v", lastRun, got.LastRunAt)
	}
	if !got.NextRunAt.Equal(nextRun) {
		t.Errorf("expected NextRunAt %v, got %v", nextRun, got.NextRunAt)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Errorf("expected UpdatedAt %v, got %v", updatedAt, got.UpdatedAt)
	}

	// Everything else must be untouched — this is the whole point of the
	// method existing instead of a full UpdateJob call.
	if got.Status != StatusPaused {
		t.Errorf("TouchJobRun must not change status; expected %q, got %q", StatusPaused, got.Status)
	}
	if got.Tags != "keep-me" {
		t.Errorf("TouchJobRun must not change tags; expected %q, got %q", "keep-me", got.Tags)
	}
	if got.Schedule != "0 * * * *" {
		t.Errorf("TouchJobRun must not change schedule; expected %q, got %q", "0 * * * *", got.Schedule)
	}
}

func TestTouchJobRun_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.TouchJobRun(context.Background(), "missing-job", time.Now(), time.Now(), time.Now())
	if !IsJobNotFound(err) {
		t.Fatalf("expected job not found, got %v", err)
	}
}

func TestDeleteJob_SoftDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("delete-me")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := store.DeleteJob(ctx, job.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	// GetJob should not find deleted jobs.
	_, err := store.GetJob(ctx, job.ID)
	if err == nil {
		t.Fatal("expected error for deleted job")
	}

	// ListJobs should not include deleted jobs.
	jobs, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for _, j := range jobs {
		if j.ID == job.ID {
			t.Fatal("deleted job should not appear in ListJobs")
		}
	}
}

func TestDeleteJob_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.DeleteJob(context.Background(), "missing")
	if !IsJobNotFound(err) {
		t.Fatalf("expected job not found, got %v", err)
	}
}

func TestGetJobByName_ExcludesDeleted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("deleted-name")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := store.DeleteJob(ctx, job.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	_, err := store.GetJobByName(ctx, "deleted-name")
	if err == nil {
		t.Fatal("expected error for deleted job by name")
	}
}

func TestCreateExecution_ListExecutions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("exec-job")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		exec := Execution{
			ID:        uuid.New().String(),
			JobID:     job.ID,
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			Status:    ExecStatusSuccess,
		}
		if _, err := store.CreateExecution(ctx, exec); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
	}

	execs, err := store.ListExecutions(ctx, job.ID, 3, 0)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(execs))
	}
	// Should be ordered by started_at DESC.
	if !execs[0].StartedAt.After(execs[1].StartedAt) {
		t.Fatal("expected descending order by started_at")
	}
}

func TestUpdateExecution(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("update-exec-job")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	now := time.Now().UTC()
	exec := Execution{
		ID:        uuid.New().String(),
		JobID:     job.ID,
		StartedAt: now,
		Status:    ExecStatusRunning,
	}
	if _, err := store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	exec.Status = ExecStatusSuccess
	exec.FinishedAt = now.Add(10 * time.Second)
	exec.DurationMs = 10000
	exec.OutputSummary = "done"
	if err := store.UpdateExecution(ctx, exec); err != nil {
		t.Fatalf("UpdateExecution: %v", err)
	}

	execs, err := store.ListExecutions(ctx, job.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(execs))
	}
	if execs[0].Status != ExecStatusSuccess {
		t.Fatalf("expected status %s, got %s", ExecStatusSuccess, execs[0].Status)
	}
	if execs[0].DurationMs != 10000 {
		t.Fatalf("expected duration_ms 10000, got %d", execs[0].DurationMs)
	}
}

func TestListExecutions_Offset(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("offset-job")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		exec := Execution{
			ID:        uuid.New().String(),
			JobID:     job.ID,
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			Status:    ExecStatusSuccess,
		}
		if _, err := store.CreateExecution(ctx, exec); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
	}

	execs, err := store.ListExecutions(ctx, job.ID, 2, 2)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 2 {
		t.Fatalf("expected 2 executions with offset, got %d", len(execs))
	}
}

func TestGetJob_NotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.GetJob(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestClose_Nil(t *testing.T) {
	var s *SQLiteStore
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil: %v", err)
	}
}

func TestSQLiteStore_DeleteAndRecreateSameName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 1. Create job "backup".
	job1 := testJob("backup")
	created1, err := store.CreateJob(ctx, job1)
	if err != nil {
		t.Fatalf("CreateJob first: %v", err)
	}

	// 2. Delete it.
	if err := store.DeleteJob(ctx, created1.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	// 3. Create a new job named "backup" — should succeed.
	job2 := testJob("backup")
	created2, err := store.CreateJob(ctx, job2)
	if err != nil {
		t.Fatalf("CreateJob second (same name after delete): %v", err)
	}
	if created2.Name != "backup" {
		t.Fatalf("expected name 'backup', got %q", created2.Name)
	}

	// 4. Verify the old deleted job's name was changed.
	var oldName string
	row := store.db.QueryRowContext(ctx, `SELECT name FROM cron_jobs WHERE job_id = ?`, created1.ID)
	if err := row.Scan(&oldName); err != nil {
		t.Fatalf("scan old job name: %v", err)
	}
	if oldName == "backup" {
		t.Fatal("expected deleted job's name to be changed, but it is still 'backup'")
	}
	if !strings.Contains(oldName, "backup_deleted_") {
		t.Fatalf("expected deleted job name to contain 'backup_deleted_', got %q", oldName)
	}
}

func TestSQLiteStore_ConcurrentReadWrite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create 5 jobs sequentially first.
	var jobIDs []string
	for i := 0; i < 5; i++ {
		job := testJob(fmt.Sprintf("concurrent-rw-%d", i))
		created, err := store.CreateJob(ctx, job)
		if err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
		jobIDs = append(jobIDs, created.ID)
	}

	// Spawn 10 goroutines that simultaneously read, create executions, and update jobs.
	// The goal is to detect data races (via -race flag), not to guarantee every write
	// succeeds — SQLITE_BUSY is an expected outcome under heavy contention.
	var wg sync.WaitGroup
	var panicCount int32

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt32(&panicCount, 1)
				}
			}()

			// Read all jobs — reads should always succeed under WAL.
			_, _ = store.ListJobs(ctx)

			// Create an execution (may get SQLITE_BUSY under contention — that's OK).
			exec := Execution{
				ID:        uuid.New().String(),
				JobID:     jobIDs[gID%len(jobIDs)],
				StartedAt: time.Now().UTC(),
				Status:    ExecStatusRunning,
			}
			_, _ = store.CreateExecution(ctx, exec)

			// Update a job (may get SQLITE_BUSY under contention — that's OK).
			job, err := store.GetJob(ctx, jobIDs[gID%len(jobIDs)])
			if err == nil {
				job.Tags = fmt.Sprintf("updated-by-%d", gID)
				job.UpdatedAt = time.Now().UTC()
				_ = store.UpdateJob(ctx, job)
			}
		}(g)
	}

	wg.Wait()

	if atomic.LoadInt32(&panicCount) > 0 {
		t.Fatalf("concurrent access caused %d panics", panicCount)
	}

	// Verify data consistency: all 5 jobs still exist.
	jobs, err := store.ListJobs(ctx)
	if err != nil {
		t.Fatalf("final ListJobs: %v", err)
	}
	if len(jobs) != 5 {
		t.Fatalf("expected 5 jobs after concurrent access, got %d", len(jobs))
	}
}

func TestListExecutions_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job := testJob("default-limit")

	if _, err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// With limit 0, should default to 20.
	execs, err := store.ListExecutions(ctx, job.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if execs != nil {
		t.Fatalf("expected nil executions, got %v", execs)
	}
}
