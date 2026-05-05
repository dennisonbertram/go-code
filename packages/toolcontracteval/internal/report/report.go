package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-agent-harness/packages/toolcontracteval/internal/cluster"
	"go-agent-harness/packages/toolcontracteval/internal/record"
)

func Generate(runDir string) (string, error) {
	var manifest record.Manifest
	if data, err := os.ReadFile(filepath.Join(runDir, "manifest.json")); err == nil {
		_ = json.Unmarshal(data, &manifest)
	}
	calls, err := record.ReadJSONL[record.ToolCall](filepath.Join(runDir, "tool-calls.jsonl"))
	if err != nil {
		return "", err
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(runDir, "validation-failures.jsonl"))
	if err != nil {
		return "", err
	}
	repairs, err := record.ReadJSONL[repairRecord](filepath.Join(runDir, "repair-simulation.jsonl"))
	if err != nil {
		return "", err
	}
	clusters := cluster.FromFailures(failures)

	var b bytes.Buffer
	fmt.Fprintf(&b, "# Tool Contract Eval Report\n\n")
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Run: `%s`\n", manifest.RunID)
	fmt.Fprintf(&b, "- Suite: `%s`\n", manifest.SuiteID)
	fmt.Fprintf(&b, "- Model: `%s`\n", manifest.Model)
	fmt.Fprintf(&b, "- Provider: `%s`\n", manifest.Provider)
	fmt.Fprintf(&b, "- Mode: `%s`\n", manifest.Mode)
	if manifest.SystemPromptLabel != "" || manifest.SystemPromptSHA256 != "" {
		fmt.Fprintf(&b, "- Prompt variant: `%s`", manifest.SystemPromptLabel)
		if manifest.SystemPromptSHA256 != "" {
			fmt.Fprintf(&b, " (`sha256=%s`, `chars=%d`)", manifest.SystemPromptSHA256, manifest.SystemPromptChars)
		}
		if manifest.SystemPromptPath != "" {
			fmt.Fprintf(&b, " from `%s`", manifest.SystemPromptPath)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "- Tool calls: `%d`\n", len(calls))
	fmt.Fprintf(&b, "- Invalid tool calls: `%d`\n", countInvalidCalls(calls))
	fmt.Fprintf(&b, "- Validation issues: `%d`\n\n", len(failures))

	fmt.Fprintf(&b, "## Top Failure Clusters\n\n")
	if len(clusters) == 0 {
		fmt.Fprintf(&b, "No validation failures were observed.\n\n")
	} else {
		for i, c := range clusters {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "### %d. `%s` `%s`\n\n", i+1, c.Tool, strings.Join(c.SchemaPath, "."))
			fmt.Fprintf(&b, "- Count: `%d`\n", c.Count)
			fmt.Fprintf(&b, "- Issue: `%s`\n", c.IssueKind)
			fmt.Fprintf(&b, "- Expected: `%s`\n", c.Expected)
			fmt.Fprintf(&b, "- Received: `%s`\n", c.ReceivedShape)
			fmt.Fprintf(&b, "- Scenarios: `%s`\n", strings.Join(c.Scenarios, "`, `"))
			fmt.Fprintf(&b, "- Example args: `%s`\n\n", truncate(c.ExampleArgs, 260))
		}
	}

	fmt.Fprintf(&b, "## Candidate Repairs\n\n")
	repairStats := summarizeRepairs(repairs)
	if len(repairStats) == 0 {
		fmt.Fprintf(&b, "No repair simulations were generated.\n\n")
	} else {
		for _, stat := range repairStats {
			fmt.Fprintf(&b, "- `%s`: applied `%d`, valid after repair `%d`, semantic notes `%d`\n", stat.Name, stat.Applied, stat.AfterValid, stat.SemanticNotes)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Recommended Harness Changes\n\n")
	recommended := false
	if len(repairStats) == 0 {
		fmt.Fprintf(&b, "No repair simulations were generated.\n")
	} else {
		for _, stat := range repairStats {
			if stat.Applied > 0 && stat.AfterValid == stat.Applied {
				fmt.Fprintf(&b, "- Consider `%s`; every applied simulation validated successfully in this run.\n", stat.Name)
				recommended = true
			}
		}
	}
	for _, c := range clusters {
		if c.IssueKind != "scenario_expected_argument" {
			continue
		}
		fmt.Fprintf(&b, "- Review `%s` `%s`: the model satisfied the JSON schema but missed a scenario-level contract. Consider clearer schema descriptions, a model-readable retry template, or an explicit semantic default if that intent is safe.\n", c.Tool, strings.Join(c.SchemaPath, "."))
		recommended = true
	}
	if !recommended {
		fmt.Fprintf(&b, "No recommendations yet.")
	}
	fmt.Fprintf(&b, "\n\n")

	fmt.Fprintf(&b, "## Regression Fixtures To Add\n\n")
	if len(clusters) == 0 {
		fmt.Fprintf(&b, "None from this run.\n")
	} else {
		for _, c := range clusters {
			fmt.Fprintf(&b, "- `%s/%s/%s`: freeze example `%s`\n", c.Tool, strings.Join(c.SchemaPath, "."), c.IssueKind, truncate(c.ExampleArgs, 180))
		}
	}

	report := b.String()
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), []byte(report), 0o644); err != nil {
		return "", err
	}
	return report, nil
}

type repairRecord struct {
	Repair               string `json:"repair"`
	Applied              bool   `json:"applied"`
	AfterValid           bool   `json:"after_valid"`
	SemanticNoteRequired bool   `json:"semantic_note_required,omitempty"`
}

type repairStat struct {
	Name          string
	Applied       int
	AfterValid    int
	SemanticNotes int
}

func summarizeRepairs(records []repairRecord) []repairStat {
	byName := map[string]*repairStat{}
	for _, r := range records {
		stat := byName[r.Repair]
		if stat == nil {
			stat = &repairStat{Name: r.Repair}
			byName[r.Repair] = stat
		}
		if r.Applied {
			stat.Applied++
		}
		if r.AfterValid && r.Applied {
			stat.AfterValid++
		}
		if r.SemanticNoteRequired {
			stat.SemanticNotes++
		}
	}
	out := make([]repairStat, 0, len(byName))
	for _, stat := range byName {
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Applied != out[j].Applied {
			return out[i].Applied > out[j].Applied
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func countInvalidCalls(calls []record.ToolCall) int {
	n := 0
	for _, call := range calls {
		if !call.Valid {
			n++
		}
	}
	return n
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
