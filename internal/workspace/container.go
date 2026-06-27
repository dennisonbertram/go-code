package workspace

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const defaultImage = "go-agent-harness:latest"
const defaultContainerPort = "8080/tcp"

// ContainerWorkspace implements Workspace using Docker containers.
// Each workspace provisions a Docker container running harnessd, exposing it
// on a dynamically allocated host port. The workspace directory is bind-mounted
// into the container at /workspace.
type ContainerWorkspace struct {
	harnessURL      string
	workspacePath   string
	containerID     string
	imageName       string
	dockerClient    containerDockerClient
	newDockerClient func() (containerDockerClient, error)
}

type containerDockerClient interface {
	ContainerCreate(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error)
	ContainerStart(context.Context, string, container.StartOptions) error
	ContainerInspect(context.Context, string) (container.InspectResponse, error)
	ContainerStop(context.Context, string, container.StopOptions) error
	ContainerRemove(context.Context, string, container.RemoveOptions) error
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
	w.workspacePath = wsPath
	cleanupFailedProvision := func() {
		forceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = w.Destroy(forceCtx)
	}

	// Get image from env if provided.
	imageName := w.imageName
	if v := opts.Env["HARNESS_IMAGE"]; v != "" {
		imageName = v
	}

	// Find a free host port.
	hostPort, err := getFreePort()
	if err != nil {
		cleanupFailedProvision()
		return fmt.Errorf("workspace: find free port: %w", err)
	}
	hostPortStr := strconv.Itoa(hostPort)

	// Create Docker client.
	cli := w.dockerClient
	if cli == nil {
		var err error
		if w.newDockerClient != nil {
			cli, err = w.newDockerClient()
		} else {
			cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		}
		if err != nil {
			cleanupFailedProvision()
			return fmt.Errorf("workspace: docker client: %w", err)
		}
		w.dockerClient = cli
	}

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
		cleanupFailedProvision()
		return fmt.Errorf("workspace: container create: %w", err)
	}
	w.containerID = resp.ID

	// Start the container.
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		cleanupFailedProvision()
		return fmt.Errorf("workspace: container start: %w", err)
	}

	// Poll until the container is running (max 30s).
	deadline := time.Now().Add(30 * time.Second)
	running := false
	for time.Now().Before(deadline) {
		info, inspectErr := cli.ContainerInspect(ctx, resp.ID)
		if inspectErr != nil {
			cleanupFailedProvision()
			return fmt.Errorf("workspace: container inspect: %w", inspectErr)
		}
		if info.State != nil && info.State.Running {
			running = true
			break
		}
		select {
		case <-ctx.Done():
			cleanupFailedProvision()
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !running {
		cleanupFailedProvision()
		return fmt.Errorf("workspace: container did not reach running state before timeout")
	}

	w.harnessURL = "http://localhost:" + hostPortStr

	// Write harness.toml to the bind-mounted workspace directory so it is
	// visible inside the container at /workspace/harness.toml.
	// API keys are passed via opts.Env (container env), never written here.
	if opts.ConfigTOML != "" {
		cfgPath := filepath.Join(wsPath, "harness.toml")
		if err := os.WriteFile(cfgPath, []byte(opts.ConfigTOML), 0o600); err != nil {
			cleanupFailedProvision()
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

// Destroy stops and removes the Docker container. It is a no-op if the
// workspace has not been provisioned.
func (w *ContainerWorkspace) Destroy(ctx context.Context) error {
	forceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if w.containerID != "" && w.dockerClient != nil {
		timeout := 5
		_ = w.dockerClient.ContainerStop(forceCtx, w.containerID, container.StopOptions{Timeout: &timeout})
		if err := w.dockerClient.ContainerRemove(forceCtx, w.containerID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("workspace: container remove: %w", err)
		}
		w.containerID = ""
	}
	if w.workspacePath != "" {
		if err := os.RemoveAll(w.workspacePath); err != nil {
			return fmt.Errorf("workspace: remove dir: %w", err)
		}
		w.workspacePath = ""
	}
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

func init() {
	_ = Register("container", func() Workspace {
		return NewContainer("")
	})
}
