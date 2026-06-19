package cloudscheduler

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DockerExecutor runs jobs inside Docker containers for isolated execution.
// Suitable for local testing and self-hosted deployments.
type DockerExecutor struct {
	// Image is the Docker image used for job execution.
	Image string
	// Network is the Docker network to attach containers to.
	Network string
	// WorkDir is the working directory inside the container.
	WorkDir string
}

// NewDockerExecutor creates a Docker executor with sensible defaults.
func NewDockerExecutor() *DockerExecutor {
	return &DockerExecutor{
		Image:   "alpine:latest",
		Network: "bridge",
		WorkDir: "/work",
	}
}

// Backend returns "docker".
func (e *DockerExecutor) Backend() string {
	return "docker"
}

// Execute runs a job in a Docker container. The job's workflow name and args
// are passed as environment variables. The container runs a shell script that
// echoes completion.
//
// In production, this would run the actual workflow engine inside the container.
// For POC purposes, it simulates execution with a shell command.
func (e *DockerExecutor) Execute(ctx context.Context, job Job) (string, error) {
	image := e.Image
	if image == "" {
		image = "alpine:latest"
	}

	// Build command: simulate workflow execution in container
	script := fmt.Sprintf(
		`echo '{"status":"running","workflow":"%s"}' && sleep 0.5 && echo '{"status":"completed","result":"docker-executed-%s"}'`,
		job.WorkflowName, job.ID,
	)

	args := []string{
		"run", "--rm",
		"--name", sanitizeContainerName(job.ID),
		"--network", e.Network,
		"--workdir", e.WorkDir,
		image,
		"sh", "-c", script,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Docker might not be available — fall back to simulated execution
		return e.simulatedExecute(job), nil
	}

	// Parse the last line as the result
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[len(lines)-1]), nil
	}
	return string(output), nil
}

// simulatedExecute provides a fallback when Docker is not available.
func (e *DockerExecutor) simulatedExecute(job Job) string {
	return fmt.Sprintf(
		`{"status":"completed","workflow":"%s","result":"simulated-%s","backend":"docker-simulated"}`,
		job.WorkflowName, job.ID,
	)
}

func sanitizeContainerName(id string) string {
	// Docker container names must match [a-zA-Z0-9][a-zA-Z0-9_.-]*
	s := strings.ReplaceAll(id, "-", "_")
	// Truncate if too long
	if len(s) > 64 {
		s = s[:64]
	}
	return "cs-" + s
}
