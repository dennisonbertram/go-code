package cluster

import (
	"sort"
	"strings"

	"go-agent-harness/packages/toolcontracteval/internal/record"
)

type FailureCluster struct {
	Key           string   `json:"key"`
	Model         string   `json:"model"`
	Provider      string   `json:"provider"`
	Tool          string   `json:"tool"`
	SchemaPath    []string `json:"schema_path"`
	IssueKind     string   `json:"issue_kind"`
	Expected      string   `json:"expected,omitempty"`
	ReceivedShape string   `json:"received_shape,omitempty"`
	Count         int      `json:"count"`
	Scenarios     []string `json:"scenarios"`
	ExampleArgs   string   `json:"example_arguments_raw,omitempty"`
}

func FromFailures(failures []record.ValidationFailure) []FailureCluster {
	byKey := map[string]*FailureCluster{}
	scenarioSets := map[string]map[string]bool{}
	for _, failure := range failures {
		key := strings.Join([]string{
			failure.Model,
			failure.Provider,
			failure.Tool,
			strings.Join(failure.Issue.Path, "."),
			failure.Issue.Code,
			failure.Issue.Received,
		}, "\x00")
		cluster := byKey[key]
		if cluster == nil {
			cluster = &FailureCluster{
				Key:           key,
				Model:         failure.Model,
				Provider:      failure.Provider,
				Tool:          failure.Tool,
				SchemaPath:    normalizePath(failure.Issue.Path),
				IssueKind:     failure.Issue.Code,
				Expected:      failure.Issue.Expected,
				ReceivedShape: failure.Issue.Received,
				ExampleArgs:   failure.ArgumentsRaw,
			}
			byKey[key] = cluster
			scenarioSets[key] = map[string]bool{}
		}
		cluster.Count++
		scenarioSets[key][failure.Scenario] = true
	}

	out := make([]FailureCluster, 0, len(byKey))
	for key, cluster := range byKey {
		for scenario := range scenarioSets[key] {
			cluster.Scenarios = append(cluster.Scenarios, scenario)
		}
		sort.Strings(cluster.Scenarios)
		out = append(out, *cluster)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func normalizePath(path []string) []string {
	if len(path) == 0 {
		return []string{"$"}
	}
	return append([]string(nil), path...)
}
