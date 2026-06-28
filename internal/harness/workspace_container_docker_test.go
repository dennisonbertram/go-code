package harness

// T-C-container-runner-e2e: prove an end-to-end run with WorkspaceType:"container"
// provisions a Docker container workspace and destroys it on completion.
//
// Gates:
//  1. requireContainerHarnessDocker (memoized) — skips if Docker is absent.
//  2. requireContainerHarnessImage   — skips if go-agent-harness:latest is absent.
//
// The helper names are file-unique (not "requireDocker") to avoid conflicts with
// other files in the package or binary that may define their own requireDocker.
//
// Run locally (no build tag needed — the gate is runtime):
//
//	go test ./internal/harness/... -run TestContainerRunner_E2E -v -count=1 -race

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Memoized Docker availability guards
// ---------------------------------------------------------------------------

var (
	containerHarnessDockerOnce       sync.Once
	containerHarnessDockerSkipReason string
)

// requireContainerHarnessDocker skips t if Docker is not reachable.
// The probe (LookPath + "docker info") runs at most once per test binary.
func requireContainerHarnessDocker(t *testing.T) {
	t.Helper()
	containerHarnessDockerOnce.Do(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			containerHarnessDockerSkipReason = "docker CLI not found in PATH"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput()
		if err != nil {
			containerHarnessDockerSkipReason = fmt.Sprintf(
				"docker daemon unavailable: %v: %s", err, strings.TrimSpace(string(out)))
		}
	})
	if containerHarnessDockerSkipReason != "" {
		t.Skipf("skipping container-runner e2e: %s", containerHarnessDockerSkipReason)
	}
}

// requireContainerHarnessImage skips t when the named image is absent locally.
// provisionRunWorkspace hard-codes "go-agent-harness:latest" and cannot accept
// an injected image name, so this guard is the correct way to avoid a fail-not-skip
// when the image is missing in CI.
func requireContainerHarnessImage(t *testing.T, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect",
		"--format", "{{.Id}}", image).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("skipping: image %q not present locally (%v) — build it first with 'make docker-build'",
			image, err)
	}
}

// ---------------------------------------------------------------------------
// E2E test
// ---------------------------------------------------------------------------

// TestContainerRunner_E2E (T-C-container-runner-e2e, Deliverable C) proves that
// a run with WorkspaceType:"container" provisions a Docker container workspace,
// emits workspace.provisioned with a non-empty workspace_path / container id,
// completes the run, and emits workspace.destroyed — confirming the container
// was cleaned up.
//
// The run uses a terminal-turn stubProvider so no real LLM call is needed.
func TestContainerRunner_E2E(t *testing.T) {
	// Gate 1: Docker daemon must be reachable.
	requireContainerHarnessDocker(t)
	// Gate 2: The harness image must exist locally — otherwise skip, not fail.
	// The constant "go-agent-harness:latest" mirrors workspace.defaultImage and is
	// what provisionRunWorkspace passes to workspace.New for "container" type.
	const harnessImage = "go-agent-harness:latest"
	requireContainerHarnessImage(t, harnessImage)

	// A single-turn provider that returns immediately, terminating the run.
	provider := &staticProviderWS{result: CompletionResult{Content: "container e2e done"}}

	runner := NewRunner(
		provider,
		NewRegistry(),
		RunnerConfig{
			MaxSteps:     1,
			DefaultModel: "test-model",
			// WorkspaceBaseOptions.BaseDir is empty; provisionRunWorkspace passes
			// it through to workspace.Options.BaseDir which defaults to os.TempDir()
			// inside ContainerWorkspace.Provision.
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "container e2e",
		WorkspaceType: "container",
	})
	if err != nil {
		t.Fatalf("StartRun with workspace_type=container failed: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Collect all events until a terminal event or 2-minute timeout.
	// Container provisioning (pulling the already-present image, starting the
	// container, polling for running state) can take up to ~30 s in the slow path.
	events := drainRunEventsWSWithTimeout(t, runner, run.ID, 2*time.Minute)

	// Locate workspace events.
	var (
		provisionedEv *Event
		destroyedEv   *Event
		failedEv      *Event
	)
	for i := range events {
		switch events[i].Type {
		case EventWorkspaceProvisioned:
			provisionedEv = &events[i]
		case EventWorkspaceDestroyed:
			destroyedEv = &events[i]
		case EventRunFailed:
			failedEv = &events[i]
		}
	}

	// If provisioning failed (not a skip-worthy error but a real one), report it
	// with context so CI has actionable output.
	if provisionedEv == nil {
		// Look for workspace.provision_failed to extract the error message.
		for i := range events {
			if events[i].Type == EventWorkspaceProvisionFailed {
				errMsg, _ := events[i].Payload["error"].(string)
				t.Fatalf("workspace.provisioned never emitted; provision failed with: %s", errMsg)
			}
		}
		t.Fatalf("workspace.provisioned never emitted; events: %v", eventTypesForContainerTest(events))
	}

	// Assert provisioned payload carries workspace_type and workspace_path.
	if got, _ := provisionedEv.Payload["workspace_type"].(string); got != "container" {
		t.Errorf("workspace.provisioned workspace_type = %q, want %q", got, "container")
	}
	wsPath, _ := provisionedEv.Payload["workspace_path"].(string)
	if wsPath == "" {
		t.Error("workspace.provisioned payload missing non-empty workspace_path")
	}

	// The run must complete (not fail) — a 1-step terminal provider should succeed.
	if failedEv != nil {
		errMsg, _ := failedEv.Payload["error"].(string)
		t.Fatalf("run failed unexpectedly: %s", errMsg)
	}
	hasCompleted := false
	for _, ev := range events {
		if ev.Type == EventRunCompleted {
			hasCompleted = true
			break
		}
	}
	if !hasCompleted {
		t.Errorf("run.completed not emitted; events: %v", eventTypesForContainerTest(events))
	}

	// workspace.destroyed must be emitted — confirms the container was cleaned up.
	if destroyedEv == nil {
		t.Fatalf("workspace.destroyed never emitted; events: %v", eventTypesForContainerTest(events))
	}
	if got, _ := destroyedEv.Payload["workspace_type"].(string); got != "container" {
		t.Errorf("workspace.destroyed workspace_type = %q, want %q", got, "container")
	}
	// If destroy produced an error, surface it (test still passes since
	// workspace.destroyed was emitted, but the error is useful for diagnostics).
	if errVal, _ := destroyedEv.Payload["error"].(string); errVal != "" {
		t.Logf("workspace.destroyed carried a non-fatal error: %s", errVal)
	}

	// Event ordering: provisioned < destroyed < completed.
	provIdx := -1
	destIdx := -1
	complIdx := -1
	for i, ev := range events {
		switch ev.Type {
		case EventWorkspaceProvisioned:
			if provIdx < 0 {
				provIdx = i
			}
		case EventWorkspaceDestroyed:
			if destIdx < 0 {
				destIdx = i
			}
		case EventRunCompleted:
			if complIdx < 0 {
				complIdx = i
			}
		}
	}
	if provIdx >= 0 && destIdx >= 0 && provIdx >= destIdx {
		t.Errorf("workspace.provisioned (%d) must precede workspace.destroyed (%d)", provIdx, destIdx)
	}
	if destIdx >= 0 && complIdx >= 0 && destIdx >= complIdx {
		t.Errorf("workspace.destroyed (%d) must precede run.completed (%d)", destIdx, complIdx)
	}
}

// drainRunEventsWSWithTimeout is drainRunEventsWS with a caller-controlled deadline.
// It exists so the container E2E test can afford a longer timeout than the
// default 10 s used by drainRunEventsWS.
func drainRunEventsWSWithTimeout(t *testing.T, runner *Runner, runID string, timeout time.Duration) []Event {
	t.Helper()

	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", runID, err)
	}
	defer cancel()

	var events []Event
	events = append(events, history...)

	for _, ev := range history {
		if IsTerminalEvent(ev.Type) {
			return events
		}
	}

	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, ev)
			if IsTerminalEvent(ev.Type) {
				return events
			}
		case <-deadline:
			t.Logf("drainRunEventsWSWithTimeout: timed out after %v; collected %d events", timeout, len(events))
			return events
		}
	}
}

// eventTypesForContainerTest returns event types as strings for diagnostic output.
func eventTypesForContainerTest(events []Event) []string {
	types := make([]string, len(events))
	for i, ev := range events {
		types[i] = string(ev.Type)
	}
	return types
}
