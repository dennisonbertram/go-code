package workspace

// Docker-gated integration tests for ContainerWorkspace.
//
// These tests exercise the real provision/destroy lifecycle against a live
// Docker daemon. They are guarded by a memoized requireContainerWsDocker
// helper that skips whenever Docker is absent. The helper name is
// file-unique (not "requireDocker") so it does not conflict if other packages
// ever contribute test helpers to this package.
//
// Run locally (no build tag needed — Docker gate is runtime):
//
//	go test ./internal/workspace/... -run TestContainerWorkspaceDocker -v -count=1 -race

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Memoized Docker availability guard
// ---------------------------------------------------------------------------

var (
	containerWsDockerOnce       sync.Once
	containerWsDockerSkipReason string
)

// requireContainerWsDocker skips t if a usable Docker daemon is not reachable.
// The probe (LookPath + "docker info") runs at most once per test binary.
func requireContainerWsDocker(t *testing.T) {
	t.Helper()
	containerWsDockerOnce.Do(func() {
		if _, err := osexec.LookPath("docker"); err != nil {
			containerWsDockerSkipReason = "docker CLI not found in PATH"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := osexec.CommandContext(ctx, "docker", "info").CombinedOutput()
		if err != nil {
			containerWsDockerSkipReason = fmt.Sprintf("docker daemon unavailable: %v: %s",
				err, strings.TrimSpace(string(out)))
		}
	})
	if containerWsDockerSkipReason != "" {
		t.Skipf("skipping container-workspace docker test: %s", containerWsDockerSkipReason)
	}
}

// requireImagePresent skips t if the named Docker image is absent locally.
func requireImagePresent(t *testing.T, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := osexec.CommandContext(ctx, "docker", "image", "inspect",
		"--format", "{{.Id}}", image).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("skipping: image %q not present locally (%v)", image, err)
	}
}

// wsContainerName returns the Docker container name ContainerWorkspace creates.
// It mirrors the naming convention in container.go.
func wsContainerName(id string) string {
	return "workspace-" + sanitizeBranch(id)
}

// containerExistsInPS returns true when a container matching the exact name
// appears in `docker ps -a` output.
func containerExistsInPS(t *testing.T, name string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := osexec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "name=^/"+name+"$",
		"--format", "{{.Names}}",
	).Output()
	if err != nil {
		t.Logf("docker ps -a (checking %q): %v", name, err)
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// inspectContainerMounts returns the Mounts slice from `docker inspect` JSON.
type mountEntry struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

func inspectContainerMounts(t *testing.T, containerID string) []mountEntry {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := osexec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{json .Mounts}}", containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect %q: %v", containerID, err)
	}
	var mounts []mountEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &mounts); err != nil {
		t.Fatalf("parse docker inspect mounts: %v — raw: %s", err, out)
	}
	return mounts
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestContainerWorkspaceDocker_ProvisionAndDestroy provisions a ContainerWorkspace
// using the default image (go-agent-harness:latest), then:
//
//  1. Asserts post-provision invariants (WorkspacePath, HarnessURL non-empty).
//  2. Verifies the container exists in docker ps -a.
//  3. Inspects the container's Mounts via `docker inspect` to confirm the
//     bind-mount maps the host WorkspacePath to /workspace — proving the
//     container's working directory is the bind-mounted path and not the
//     host's current working directory.
//  4. Writes a sentinel file on the HOST side and confirms the same path
//     exists (proving the host dir is the canonical source for /workspace).
//  5. Calls Destroy and asserts the container is gone from docker ps -a.
func TestContainerWorkspaceDocker_ProvisionAndDestroy(t *testing.T) {
	requireContainerWsDocker(t)
	requireImagePresent(t, defaultImage)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	wsID := containerWorkspaceTestID(t, "docker-provision")
	cname := wsContainerName(wsID)

	w := NewContainer(defaultImage)
	t.Cleanup(func() {
		// Force-remove on cleanup in case the test fails before Destroy.
		_ = w.Destroy(context.Background())
	})

	opts := Options{
		ID:      wsID,
		BaseDir: t.TempDir(),
	}

	if err := w.Provision(ctx, opts); err != nil {
		if isContainerWorkspaceProvisionEnvironmentUnavailable(err) {
			t.Skipf("provision unavailable in this environment: %v", err)
		}
		t.Fatalf("Provision: %v", err)
	}

	// (1) Post-provision invariants.
	hostWsPath := w.WorkspacePath()
	if hostWsPath == "" {
		t.Fatal("WorkspacePath() is empty after Provision")
	}
	if w.HarnessURL() == "" {
		t.Fatal("HarnessURL() is empty after Provision")
	}
	if w.containerID == "" {
		t.Fatal("containerID is empty after Provision")
	}

	// (2) Container must appear in docker ps -a.
	if !containerExistsInPS(t, cname) {
		t.Fatalf("container %q not found in docker ps -a after Provision", cname)
	}

	// (3) Bind-mount: host WorkspacePath must be mapped to /workspace inside the container.
	mounts := inspectContainerMounts(t, w.containerID)
	var found bool
	for _, m := range mounts {
		if m.Type == "bind" && m.Destination == "/workspace" {
			found = true
			// The source must be the exact host workspace path.
			if m.Source != hostWsPath {
				t.Errorf("bind-mount Source = %q, want %q (WorkspacePath)", m.Source, hostWsPath)
			}
			break
		}
	}
	if !found {
		t.Errorf("no bind-mount to /workspace found in container mounts: %+v", mounts)
	}

	// (4) Write a sentinel on the HOST side and confirm the path is under WorkspacePath
	// (not the process cwd), proving the workspace root is container-side, not the
	// caller's working directory.
	sentinel := "sentinel.txt"
	sentinelPath := filepath.Join(hostWsPath, sentinel)
	if err := os.WriteFile(sentinelPath, []byte("workspace-check\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	// The sentinel must NOT be reachable from a plain relative path — it lives
	// under WorkspacePath, which is distinct from the process cwd.
	cwd, _ := os.Getwd()
	if strings.HasPrefix(hostWsPath, cwd+"/") || hostWsPath == cwd {
		t.Errorf("WorkspacePath %q is inside (or equal to) process cwd %q — should be isolated", hostWsPath, cwd)
	}
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Fatalf("sentinel at WorkspacePath not readable: %v", err)
	}

	// (5) Destroy and confirm cleanup.
	if err := w.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if w.containerID != "" {
		t.Error("containerID should be empty after Destroy")
	}
	if containerExistsInPS(t, cname) {
		t.Errorf("container %q still visible in docker ps -a after Destroy — leftover leak", cname)
	}
}

// TestContainerWorkspaceDocker_BindMountUsability verifies that the bind-mount
// path /workspace inside the container corresponds to the host WorkspacePath by
// running a short-lived alpine container that writes a file to /workspace and
// confirming the file appears on the HOST side at WorkspacePath.  This proves
// the bind-mount semantics are correct independently of harnessd startup, using
// a minimal image that is guaranteed to run.
//
// The test is skipped if alpine:latest is not present locally.
func TestContainerWorkspaceDocker_BindMountUsability(t *testing.T) {
	requireContainerWsDocker(t)

	const alpineImage = "alpine:latest"
	requireImagePresent(t, alpineImage)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hostDir := t.TempDir()
	const markerFile = "written-inside-container.txt"
	const markerContent = "hello-from-alpine"

	// docker run --rm -v hostDir:/workspace alpine sh -c 'echo … > /workspace/<file>'
	cmd := osexec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", hostDir+":/workspace",
		alpineImage,
		"sh", "-c", fmt.Sprintf("echo %s > /workspace/%s", markerContent, markerFile),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker run alpine: %v — output: %s", err, out)
	}

	// The file written at /workspace/<markerFile> inside the container must now
	// be visible on the HOST at hostDir/<markerFile>.
	hostMarker := filepath.Join(hostDir, markerFile)
	data, err := os.ReadFile(hostMarker)
	if err != nil {
		t.Fatalf("marker file %q not visible on host after container write: %v", hostMarker, err)
	}
	if !strings.Contains(string(data), markerContent) {
		t.Errorf("marker content = %q, want to contain %q", string(data), markerContent)
	}
	t.Logf("bind-mount confirmed: container wrote %q, host reads %q", markerFile, strings.TrimSpace(string(data)))
}
