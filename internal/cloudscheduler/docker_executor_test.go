package cloudscheduler

import (
	"encoding/json"
	"testing"
)

func TestDockerExecutorSimulatedExecuteIncludesJobIdentity(t *testing.T) {
	t.Parallel()

	exec := &DockerExecutor{}
	raw := exec.simulatedExecute(Job{ID: "job-123", WorkflowName: "nightly"})

	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("simulatedExecute returned invalid JSON: %v", err)
	}
	if got["status"] != "completed" {
		t.Fatalf("status = %q, want completed", got["status"])
	}
	if got["workflow"] != "nightly" {
		t.Fatalf("workflow = %q, want nightly", got["workflow"])
	}
	if got["result"] != "simulated-job-123" {
		t.Fatalf("result = %q, want simulated-job-123", got["result"])
	}
	if got["backend"] != "docker-simulated" {
		t.Fatalf("backend = %q, want docker-simulated", got["backend"])
	}
}
