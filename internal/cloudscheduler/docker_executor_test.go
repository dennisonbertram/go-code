package cloudscheduler

import (
	"encoding/json"
	"testing"
)

func TestDockerExecutorSimulatedExecuteIncludesJobAndBackend(t *testing.T) {
	t.Parallel()

	executor := &DockerExecutor{}
	out := executor.simulatedExecute(Job{
		ID:           "job-123",
		WorkflowName: "nightly-regression",
	})

	var payload map[string]string
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("simulated output is not JSON: %v; output=%q", err, out)
	}
	if payload["status"] != "completed" {
		t.Fatalf("status = %q, want completed", payload["status"])
	}
	if payload["workflow"] != "nightly-regression" {
		t.Fatalf("workflow = %q, want nightly-regression", payload["workflow"])
	}
	if payload["result"] != "simulated-job-123" {
		t.Fatalf("result = %q, want simulated-job-123", payload["result"])
	}
	if payload["backend"] != "docker-simulated" {
		t.Fatalf("backend = %q, want docker-simulated", payload["backend"])
	}
}
