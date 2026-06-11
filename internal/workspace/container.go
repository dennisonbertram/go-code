package workspace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const defaultImage = "go-agent-harness:latest"
const defaultContainerPort = "8080/tcp"

// ContainerWorkspace implements Workspace using Docker containers.
// Each workspace provisions a Docker container running harnessd, exposing it
// on a dynamically allocated host port. The workspace directory is bind-mounted
// into the container at /workspace.
type ContainerWorkspace struct {
	harnessURL    string
	workspacePath string
	containerID   string
	imageName     string
	dockerClient  *client.Client
}

// NewContainer returns a new, unprovisioned ContainerWorkspace.
// imageName is the Docker image to use; if empty it defaults to
// "go-agent-harness:latest". The Docker client is created lazily during
// Provision.
func NewContainer(imageName string) *ContainerWorkspace {
	if imageName == "" {
		imageName = defaultImage
	}
	return &ContainerWorkspace{imageName: imageName}
}

// Provision creates a workspace directory and starts a Docker container
// running harnessd. The container exposes its internal port 8080 on a
// dynamically allocated host port.
//
// Provision returns ErrInvalidID if opts.ID is empty. It polls
// ContainerInspect for up to 30 seconds waiting for the container to reach
// the running state.
func (w *ContainerWorkspace) Provision(ctx context.Context, opts Options) error {
	if opts.ID == "" {
		return ErrInvalidID
	}

	// Determine workspace dir.
	baseDir := opts.BaseDir
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	wsPath := filepath.Join(baseDir, opts.ID)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		return fmt.Errorf("workspace: create dir: %w", err)
	}

	// Get image from env if provided.
	imageName := w.imageName
	if v := opts.Env["HARNESS_IMAGE"]; v != "" {
		imageName = v
	}

	// Find a free host port.
	hostPort, err := getFreePort()
	if err != nil {
		return fmt.Errorf("workspace: find free port: %w", err)
	}
	hostPortStr := strconv.Itoa(hostPort)

	// Create Docker client.
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("workspace: docker client: %w", err)
	}
	w.dockerClient = cli

	// Configure port bindings.
	containerPort := nat.Port(defaultContainerPort)
	portBindings := nat.PortMap{
		containerPort: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPortStr}},
	}

	// Build environment variable list.
	var envList []string
	for k, v := range opts.Env {
		envList = append(envList, k+"="+v)
	}
	envList = append(envList, "HARNESS_WORKSPACE=/workspace")

	// Create the container.
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        imageName,
			ExposedPorts: nat.PortSet{containerPort: struct{}{}},
			Env:          envList,
		},
		&container.HostConfig{
			PortBindings: portBindings,
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: wsPath, Target: "/workspace"},
			},
		},
		nil, nil,
		"workspace-"+sanitizeBranch(opts.ID),
	)
	if err != nil {
		return fmt.Errorf("workspace: container create: %w", err)
	}
	w.containerID = resp.ID

	// Start the container.
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("workspace: container start: %w", err)
	}

	// Poll until the container is running (max 30s).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		info, inspectErr := cli.ContainerInspect(ctx, resp.ID)
		if inspectErr != nil {
			return fmt.Errorf("workspace: container inspect: %w", inspectErr)
		}
		if info.State != nil && info.State.Running {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	w.harnessURL = "http://localhost:" + hostPortStr
	w.workspacePath = wsPath

	// Write harness.toml to the bind-mounted workspace directory so it is
	// visible inside the container at /workspace/harness.toml.
	// API keys are passed via opts.Env (container env), never written here.
	if opts.ConfigTOML != "" {
		cfgPath := filepath.Join(wsPath, "harness.toml")
		if err := os.WriteFile(cfgPath, []byte(opts.ConfigTOML), 0o600); err != nil {
			return fmt.Errorf("workspace: write harness.toml: %w", err)
		}
	}

	return nil
}

// HarnessURL returns the HTTP endpoint of the harnessd instance running in the
// container. Returns an empty string if Provision has not been called.
func (w *ContainerWorkspace) HarnessURL() string { return w.harnessURL }

// WorkspacePath returns the host filesystem path that is bind-mounted into the
// container. Returns an empty string if Provision has not been called.
func (w *ContainerWorkspace) WorkspacePath() string { return w.workspacePath }

// WaitReady polls the harnessd /healthz endpoint inside the container with
// exponential backoff. It returns nil when /healthz responds with 200 OK,
// or a descriptive error if the container never becomes ready within 2 minutes.
func (w *ContainerWorkspace) WaitReady(ctx context.Context) error {
	if w.harnessURL == "" {
		return fmt.Errorf("workspace: container not provisioned")
	}
	return waitForHealthz(ctx, w.harnessURL, "container")
}

// Destroy stops and removes the Docker container. It is a no-op if the
// workspace has not been provisioned.
func (w *ContainerWorkspace) Destroy(ctx context.Context) error {
	if w.containerID == "" {
		return nil
	}
	if w.dockerClient == nil {
		return nil
	}
	timeout := 5
	_ = w.dockerClient.ContainerStop(ctx, w.containerID, container.StopOptions{Timeout: &timeout})
	if err := w.dockerClient.ContainerRemove(ctx, w.containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("workspace: container remove: %w", err)
	}
	w.containerID = ""
	return nil
}

// getFreePort asks the OS for an available TCP port by binding to :0,
// then immediately releases the listener and returns the port number.
func getFreePort() (int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitForHealthz polls harnessURL+"/healthz" with exponential backoff up to a
// 2-minute timeout. It returns nil on the first 200 OK response, or a
// descriptive error if the deadline expires with the last probe error.
func waitForHealthz(ctx context.Context, harnessURL, wsType string) error {
	const (
		initialBackoff = 200 * time.Millisecond
		maxBackoff     = 10 * time.Second
		timeout        = 2 * time.Minute
	)

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	backoff := initialBackoff

	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("workspace: WaitReady %s cancelled: %w", wsType, ctx.Err())
		}
		if time.Now().After(deadline) {
			detail := "no response"
			if lastErr != nil {
				detail = lastErr.Error()
			}
			return fmt.Errorf("harnessd inside %s never became ready: %s", wsType, detail)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, harnessURL+"/healthz", nil)
		if err != nil {
			return fmt.Errorf("workspace: WaitReady %s request: %w", wsType, err)
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("workspace: WaitReady %s cancelled: %w", wsType, ctx.Err())
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func init() {
	_ = Register("container", func() Workspace {
		return NewContainer("")
	})
}
