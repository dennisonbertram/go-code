package cluster

import (
	"testing"

	"go-agent-harness/packages/toolcontracteval/internal/record"
	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

func TestFromFailuresClustersByShapeNotRawValue(t *testing.T) {
	failures := []record.ValidationFailure{
		{Model: "grok", Provider: "xai", Scenario: "a", Tool: "probe", ArgumentsRaw: `{"paths":"a"}`, Issue: schema.Issue{Path: []string{"paths"}, Code: "invalid_type", Expected: "array", Received: "string"}},
		{Model: "grok", Provider: "xai", Scenario: "b", Tool: "probe", ArgumentsRaw: `{"paths":"b"}`, Issue: schema.Issue{Path: []string{"paths"}, Code: "invalid_type", Expected: "array", Received: "string"}},
	}
	clusters := FromFailures(failures)
	if len(clusters) != 1 {
		t.Fatalf("clusters len = %d, want 1", len(clusters))
	}
	if clusters[0].Count != 2 || len(clusters[0].Scenarios) != 2 {
		t.Fatalf("cluster = %+v", clusters[0])
	}
}
