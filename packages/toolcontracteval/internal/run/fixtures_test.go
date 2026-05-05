package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixturesAreStructuredLearningArtifacts(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one fixture")
	}
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Name                   string `json:"name"`
			Model                  string `json:"model"`
			Provider               string `json:"provider"`
			Scenario               string `json:"scenario"`
			Tool                   string `json:"tool"`
			ArgumentsRaw           string `json:"arguments_raw"`
			Issues                 []any  `json:"issues"`
			Learning               string `json:"learning"`
			CandidateHarnessChange string `json:"candidate_harness_change"`
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("%s: %v", match, err)
		}
		if fixture.Name == "" || fixture.Model == "" || fixture.Provider == "" || fixture.Scenario == "" || fixture.Tool == "" {
			t.Fatalf("%s: missing fixture identity fields: %+v", match, fixture)
		}
		if fixture.ArgumentsRaw == "" || len(fixture.Issues) == 0 {
			t.Fatalf("%s: missing replayable failure fields: %+v", match, fixture)
		}
		if fixture.Learning == "" || fixture.CandidateHarnessChange == "" {
			t.Fatalf("%s: missing learning fields: %+v", match, fixture)
		}
	}
}
