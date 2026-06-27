package cloudscheduler

import (
	"strings"
	"testing"
)

func TestDockerExecutorSimulatedExecuteIncludesJobAndBackend(t *testing.T) {
	t.Parallel()

	executor := NewDockerExecutor()
	out := executor.simulatedExecute(Job{ID: "job-1", WorkflowName: "nightly"})
	for _, want := range []string{
		`"status":"completed"`,
		`"workflow":"nightly"`,
		`"result":"simulated-job-1"`,
		`"backend":"docker-simulated"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("simulated output %q missing %s", out, want)
		}
	}
}
