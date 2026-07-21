package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// startTrackedJob starts a background job on the manager and returns its
// shell_id.
func startTrackedJob(t *testing.T, mgr *htools.JobManager, command string) string {
	t.Helper()
	result, err := mgr.RunBackground(command, 60, "")
	if err != nil {
		t.Fatalf("RunBackground(%q): %v", command, err)
	}
	shellID, _ := result["shell_id"].(string)
	if shellID == "" {
		t.Fatalf("RunBackground(%q) returned no shell_id: %v", command, result)
	}
	return shellID
}

func TestJobTrackerRegisterIdempotentAndUnique(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgrA := htools.NewJobManager(t.TempDir(), nil)
	mgrB := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgrA.Shutdown(context.Background()) }()
	defer func() { _ = mgrB.Shutdown(context.Background()) }()

	refA1 := tracker.Register(mgrA)
	refA2 := tracker.Register(mgrA)
	if refA1 != refA2 {
		t.Fatalf("re-registering the same manager returned %q then %q, want identical refs", refA1, refA2)
	}
	refB := tracker.Register(mgrB)
	if refA1 == refB {
		t.Fatalf("distinct managers got the same ref %q", refA1)
	}
}

func TestJobTrackerListUnionsWithNamespacedIDs(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgrA := htools.NewJobManager(t.TempDir(), nil)
	mgrB := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgrA.Shutdown(context.Background()) }()
	defer func() { _ = mgrB.Shutdown(context.Background()) }()

	refA := tracker.Register(mgrA)
	refB := tracker.Register(mgrB)
	jobA := startTrackedJob(t, mgrA, "sleep 30")
	jobB := startTrackedJob(t, mgrB, "sleep 31")

	// Both managers number their first job job_1; the tracker must namespace.
	if jobA != jobB {
		t.Fatalf("test premise broken: expected colliding shell ids, got %q and %q", jobA, jobB)
	}

	listed := tracker.List()
	if len(listed) != 2 {
		t.Fatalf("List returned %d jobs, want 2: %+v", len(listed), listed)
	}
	byTaskID := make(map[string]TrackedJob, 2)
	for _, tj := range listed {
		byTaskID[tj.TaskID] = tj
		if tj.Info.Command == "" || !tj.Info.Running {
			t.Errorf("tracked job %+v missing command or not running", tj)
		}
	}
	wantA := refA + ":" + jobA
	wantB := refB + ":" + jobB
	if _, ok := byTaskID[wantA]; !ok {
		t.Errorf("missing task %q in union; got %v", wantA, byTaskID)
	}
	if _, ok := byTaskID[wantB]; !ok {
		t.Errorf("missing task %q in union; got %v", wantB, byTaskID)
	}
}

func TestJobTrackerUnregisterRemovesManager(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgr := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	ref := tracker.Register(mgr)
	startTrackedJob(t, mgr, "sleep 30")
	if got := len(tracker.List()); got != 1 {
		t.Fatalf("List before unregister returned %d jobs, want 1", got)
	}

	tracker.Unregister(ref)
	if got := tracker.List(); len(got) != 0 {
		t.Fatalf("List after unregister returned %d jobs, want 0: %+v", len(got), got)
	}
	// Unknown refs unregister cleanly (no panic).
	tracker.Unregister(ref)
	tracker.Unregister("jm999")
}

func TestJobTrackerGet(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgr := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	ref := tracker.Register(mgr)
	shellID := startTrackedJob(t, mgr, "sleep 30")
	taskID := ref + ":" + shellID

	tj, ok := tracker.Get(taskID)
	if !ok {
		t.Fatalf("Get(%q) not found", taskID)
	}
	if tj.Info.ID != shellID || tj.Info.Command != "sleep 30" {
		t.Errorf("Get(%q) = %+v, want job %s command 'sleep 30'", taskID, tj, shellID)
	}

	if _, ok := tracker.Get("jm999:job_1"); ok {
		t.Error("Get with unknown manager ref should report not found")
	}
	if _, ok := tracker.Get(ref + ":job_999"); ok {
		t.Error("Get with unknown job id should report not found")
	}
	if _, ok := tracker.Get("no-separator"); ok {
		t.Error("Get with malformed task id should report not found")
	}
}

func TestJobTrackerKill(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgrA := htools.NewJobManager(t.TempDir(), nil)
	mgrB := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgrA.Shutdown(context.Background()) }()
	defer func() { _ = mgrB.Shutdown(context.Background()) }()

	refA := tracker.Register(mgrA)
	refB := tracker.Register(mgrB)
	jobA := startTrackedJob(t, mgrA, "sleep 30")
	jobB := startTrackedJob(t, mgrB, "sleep 30")

	// Kill manager A's job via the namespaced task ID.
	if err := tracker.Kill(refA + ":" + jobA); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := mgrA.Output(jobA, false)
		if err != nil {
			t.Fatalf("Output(%s): %v", jobA, err)
		}
		if running, _ := out["running"].(bool); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still running after kill", jobA)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Manager B's identically-named job must be untouched.
	outB, err := mgrB.Output(jobB, false)
	if err != nil {
		t.Fatalf("Output(%s): %v", jobB, err)
	}
	if running, _ := outB["running"].(bool); !running {
		t.Error("kill routed to the wrong manager: B's job is not running")
	}

	// Unknown task IDs surface ErrJobNotFound.
	for _, bad := range []string{"jm999:job_1", refB + ":job_999", "no-separator", ""} {
		if err := tracker.Kill(bad); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("Kill(%q) error = %v, want ErrJobNotFound", bad, err)
		}
	}
}

// TestDefaultRegistryJobManagerRegistersWithTracker proves the production
// wiring path (epic #814 slice 2): a registry built with
// DefaultRegistryOptions.JobTracker exposes its background bash jobs to the
// tracker, and unregisters on shutdown.
func TestDefaultRegistryJobManagerRegistersWithTracker(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
		JobTracker:   tracker,
	})

	if got := tracker.List(); len(got) != 0 {
		t.Fatalf("tracker has %d jobs before any bash call, want 0", len(got))
	}

	if _, err := registry.Execute(context.Background(), "bash", json.RawMessage(`{"command":"sleep 30","run_in_background":true}`)); err != nil {
		t.Fatalf("bash run_in_background: %v", err)
	}

	listed := tracker.List()
	if len(listed) != 1 {
		t.Fatalf("tracker has %d jobs after background bash, want 1: %+v", len(listed), listed)
	}
	if listed[0].Info.Command != "sleep 30" || !listed[0].Info.Running {
		t.Errorf("tracked job = %+v, want running 'sleep 30'", listed[0])
	}

	// Kill through the tracker, then confirm via the job_output tool.
	if err := tracker.Kill(listed[0].TaskID); err != nil {
		t.Fatalf("tracker.Kill: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := registry.Execute(context.Background(), "job_output", json.RawMessage(`{"shell_id":"`+listed[0].Info.ID+`"}`))
		if err != nil {
			t.Fatalf("job_output: %v", err)
		}
		if strings.Contains(out, `"running":false`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job_output still reports running after kill: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Registry shutdown unregisters the manager from the tracker.
	if err := registry.Shutdown(context.Background()); err != nil {
		t.Fatalf("registry.Shutdown: %v", err)
	}
	if got := tracker.List(); len(got) != 0 {
		t.Fatalf("tracker has %d jobs after registry shutdown, want 0: %+v", len(got), got)
	}
}

// TestJobTrackerOutput verifies Output routes to the owning manager and
// returns the job's captured output (epic #814 slice 4: view output from the
// /tasks panel).
func TestJobTrackerOutput(t *testing.T) {
	t.Parallel()

	tracker := NewJobTracker()
	mgr := htools.NewJobManager(t.TempDir(), nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	ref := tracker.Register(mgr)
	shellID := startTrackedJob(t, mgr, "echo hello-from-job")

	// Wait for the job to finish so output is present.
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := mgr.Output(shellID, false)
		if err != nil {
			t.Fatalf("Output(%s): %v", shellID, err)
		}
		if running, _ := out["running"].(bool); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("echo job did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}

	out, err := tracker.Output(ref + ":" + shellID)
	if err != nil {
		t.Fatalf("tracker.Output: %v", err)
	}
	text, _ := out["output"].(string)
	if !strings.Contains(text, "hello-from-job") {
		t.Errorf("tracker.Output output = %q, want it to contain 'hello-from-job'", text)
	}
	if running, _ := out["running"].(bool); running {
		t.Error("finished job should report running=false")
	}

	for _, bad := range []string{"jm999:job_1", ref + ":job_999", "no-separator"} {
		if _, err := tracker.Output(bad); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("Output(%q) error = %v, want ErrJobNotFound", bad, err)
		}
	}
}
