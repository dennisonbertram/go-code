package cron

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// mockStore implements Store for testing.
type mockStore struct {
	MigrateFunc        func(ctx context.Context) error
	CreateJobFunc      func(ctx context.Context, job Job) (Job, error)
	GetJobFunc         func(ctx context.Context, id string) (Job, error)
	GetJobByNameFunc   func(ctx context.Context, name string) (Job, error)
	ListJobsFunc       func(ctx context.Context) ([]Job, error)
	UpdateJobFunc      func(ctx context.Context, job Job) error
	TouchJobRunFunc    func(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error
	DeleteJobFunc      func(ctx context.Context, id string) error
	CreateExecutionFunc func(ctx context.Context, exec Execution) (Execution, error)
	UpdateExecutionFunc func(ctx context.Context, exec Execution) error
	ListExecutionsFunc  func(ctx context.Context, jobID string, limit, offset int) ([]Execution, error)
	CloseFunc          func() error
}

func (m *mockStore) Migrate(ctx context.Context) error {
	if m.MigrateFunc != nil {
		return m.MigrateFunc(ctx)
	}
	return nil
}

func (m *mockStore) CreateJob(ctx context.Context, job Job) (Job, error) {
	if m.CreateJobFunc != nil {
		return m.CreateJobFunc(ctx, job)
	}
	return job, nil
}

func (m *mockStore) GetJob(ctx context.Context, id string) (Job, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, id)
	}
	return Job{}, nil
}

func (m *mockStore) GetJobByName(ctx context.Context, name string) (Job, error) {
	if m.GetJobByNameFunc != nil {
		return m.GetJobByNameFunc(ctx, name)
	}
	return Job{}, nil
}

func (m *mockStore) ListJobs(ctx context.Context) ([]Job, error) {
	if m.ListJobsFunc != nil {
		return m.ListJobsFunc(ctx)
	}
	return nil, nil
}

func (m *mockStore) UpdateJob(ctx context.Context, job Job) error {
	if m.UpdateJobFunc != nil {
		return m.UpdateJobFunc(ctx, job)
	}
	return nil
}

func (m *mockStore) TouchJobRun(ctx context.Context, jobID string, lastRun, nextRun, updatedAt time.Time) error {
	if m.TouchJobRunFunc != nil {
		return m.TouchJobRunFunc(ctx, jobID, lastRun, nextRun, updatedAt)
	}
	return nil
}

func (m *mockStore) DeleteJob(ctx context.Context, id string) error {
	if m.DeleteJobFunc != nil {
		return m.DeleteJobFunc(ctx, id)
	}
	return nil
}

func (m *mockStore) CreateExecution(ctx context.Context, exec Execution) (Execution, error) {
	if m.CreateExecutionFunc != nil {
		return m.CreateExecutionFunc(ctx, exec)
	}
	return exec, nil
}

func (m *mockStore) UpdateExecution(ctx context.Context, exec Execution) error {
	if m.UpdateExecutionFunc != nil {
		return m.UpdateExecutionFunc(ctx, exec)
	}
	return nil
}

func (m *mockStore) ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]Execution, error) {
	if m.ListExecutionsFunc != nil {
		return m.ListExecutionsFunc(ctx, jobID, limit, offset)
	}
	return nil, nil
}

func (m *mockStore) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

// mockExecutor implements Executor for testing.
type mockExecutor struct {
	ExecuteFunc func(ctx context.Context, job Job) (string, error)
	mu          sync.Mutex
	calls       []Job
}

func (m *mockExecutor) Execute(ctx context.Context, job Job) (string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, job)
	m.mu.Unlock()
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, job)
	}
	return "ok", nil
}

// mockClock implements Clock for testing.
type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{now: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// testJob creates a job with sensible defaults for testing.
func testJob(name string) Job {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return Job{
		ID:         uuid.New().String(),
		Name:       name,
		Schedule:   "*/5 * * * *",
		ExecType:   ExecTypeShell,
		ExecConfig: `{"command":"echo hello"}`,
		Status:     StatusActive,
		TimeoutSec: 30,
		Tags:       "",
		NextRunAt:  now.Add(5 * time.Minute),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
