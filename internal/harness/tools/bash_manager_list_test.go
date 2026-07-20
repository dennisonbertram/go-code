package tools

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// waitJobDone polls the job until it reports not-running or the deadline
// passes. It fails the test on timeout.
func waitJobDone(t *testing.T, mgr *JobManager, shellID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		result, err := mgr.Output(shellID, false)
		if err != nil {
			t.Fatalf("Output(%s): %v", shellID, err)
		}
		if running, _ := result["running"].(bool); !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still running after 10s", shellID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func startBackgroundJob(t *testing.T, mgr *JobManager, ctx context.Context, command string, timeoutSeconds int) string {
	t.Helper()
	result, err := mgr.runBackground(ctx, command, timeoutSeconds, "")
	if err != nil {
		t.Fatalf("runBackground(%q): %v", command, err)
	}
	shellID, _ := result["shell_id"].(string)
	if shellID == "" {
		t.Fatalf("runBackground(%q) returned no shell_id: %v", command, result)
	}
	return shellID
}

func TestJobManagerListEmpty(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	if got := mgr.List(); len(got) != 0 {
		t.Fatalf("List on empty manager returned %d jobs: %+v", len(got), got)
	}
}

func TestJobManagerListRunningThenKilled(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	shellID := startBackgroundJob(t, mgr, context.Background(), "sleep 30", 60)

	jobs := mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("List returned %d jobs, want 1: %+v", len(jobs), jobs)
	}
	job := jobs[0]
	if job.ID != shellID {
		t.Errorf("job ID = %q, want %q", job.ID, shellID)
	}
	if job.Command != "sleep 30" {
		t.Errorf("job Command = %q, want %q", job.Command, "sleep 30")
	}
	if !job.Running {
		t.Error("job should be running")
	}
	if job.Status() != JobStatusRunning {
		t.Errorf("job Status() = %q, want %q", job.Status(), JobStatusRunning)
	}
	if job.StartedAt.IsZero() {
		t.Error("job StartedAt is zero")
	}

	if _, err := mgr.Kill(shellID); err != nil {
		t.Fatalf("Kill(%s): %v", shellID, err)
	}
	waitJobDone(t, mgr, shellID)

	jobs = mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("List after kill returned %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].Running {
		t.Error("killed job should not be running")
	}
	if jobs[0].Status() != JobStatusExited {
		t.Errorf("killed job Status() = %q, want %q", jobs[0].Status(), JobStatusExited)
	}
}

func TestJobManagerListExitedWithExitCode(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	shellID := startBackgroundJob(t, mgr, context.Background(), "exit 3", 30)
	waitJobDone(t, mgr, shellID)

	jobs := mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("List returned %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].Running {
		t.Error("finished job should not be running")
	}
	if jobs[0].ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", jobs[0].ExitCode)
	}
	if jobs[0].TimedOut {
		t.Error("TimedOut should be false for a normally exited job")
	}
	if jobs[0].Status() != JobStatusExited {
		t.Errorf("Status() = %q, want %q", jobs[0].Status(), JobStatusExited)
	}
}

func TestJobManagerListTimedOut(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	shellID := startBackgroundJob(t, mgr, context.Background(), "sleep 30", 1)
	waitJobDone(t, mgr, shellID)

	jobs := mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("List returned %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if !jobs[0].TimedOut {
		t.Error("TimedOut should be true for a job that hit its timeout")
	}
	if jobs[0].Status() != JobStatusTimedOut {
		t.Errorf("Status() = %q, want %q", jobs[0].Status(), JobStatusTimedOut)
	}
}

func TestJobManagerListExcludesTTLEvicted(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }
	mgr := NewJobManager(t.TempDir(), clock)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	shellID := startBackgroundJob(t, mgr, context.Background(), "true", 30)
	waitJobDone(t, mgr, shellID)

	if got := mgr.List(); len(got) != 1 {
		t.Fatalf("List before eviction returned %d jobs, want 1", len(got))
	}

	// Advance past the 30-minute TTL and trigger cleanup; the evicted job must
	// no longer be listed.
	now = now.Add(31 * time.Minute)
	mgr.cleanupExpired()

	if got := mgr.List(); len(got) != 0 {
		t.Fatalf("List after TTL eviction returned %d jobs, want 0: %+v", len(got), got)
	}
}

func TestJobManagerListCapturesTenant(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	tenantCtx := context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{
		RunID:    "run-1",
		TenantID: "tenant-x",
	})
	tenantJob := startBackgroundJob(t, mgr, tenantCtx, "sleep 30", 60)
	plainJob := startBackgroundJob(t, mgr, context.Background(), "sleep 30", 60)

	byID := make(map[string]JobInfo, 2)
	for _, job := range mgr.List() {
		byID[job.ID] = job
	}
	if got := byID[tenantJob].TenantID; got != "tenant-x" {
		t.Errorf("tenant job TenantID = %q, want tenant-x", got)
	}
	if got := byID[plainJob].TenantID; got != "" {
		t.Errorf("metadata-less job TenantID = %q, want empty", got)
	}
}

func TestJobManagerListConcurrent(t *testing.T) {
	t.Parallel()

	mgr := NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	var wg sync.WaitGroup
	// Writers: start and kill jobs.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				result, err := mgr.runBackground(context.Background(), fmt.Sprintf("sleep %d", 30+j), 60, "")
				if err != nil {
					continue
				}
				if shellID, _ := result["shell_id"].(string); shellID != "" && j%2 == 0 {
					_, _ = mgr.Kill(shellID)
				}
			}
		}(i)
	}
	// Readers: list continuously.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				for _, job := range mgr.List() {
					_ = job.Status()
				}
			}
		}()
	}
	wg.Wait()
}

func TestJobInfoStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		info JobInfo
		want string
	}{
		{"running", JobInfo{Running: true}, JobStatusRunning},
		{"exited", JobInfo{Running: false, TimedOut: false, ExitCode: 0}, JobStatusExited},
		{"failed still exited", JobInfo{Running: false, TimedOut: false, ExitCode: 1}, JobStatusExited},
		{"timed out", JobInfo{Running: false, TimedOut: true}, JobStatusTimedOut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.Status(); got != tc.want {
				t.Errorf("Status() = %q, want %q", got, tc.want)
			}
		})
	}
}
