package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/packages/toolcontracteval/internal/scenario"
	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

type Options struct {
	SuitePath         string
	OutDir            string
	RunID             string
	Model             string
	Provider          string
	Mode              string
	APIBaseURL        string
	APIKey            string
	SystemPrompt      string
	SystemPromptLabel string
	SystemPromptPath  string
	MaxTurns          int
	Now               func() time.Time
}

type Result struct {
	RunID  string
	RunDir string
}

func Execute(ctx context.Context, opts Options) (Result, error) {
	if opts.Mode == "" {
		opts.Mode = "api"
	}
	if opts.Mode != "api" {
		return Result{}, fmt.Errorf("toolcontracteval profiles models only through harnessd API; unsupported mode %q", opts.Mode)
	}
	return ExecuteAPI(ctx, opts)
}

func expectationIssues(sc scenario.Scenario, toolName string, args map[string]any) []schema.Issue {
	if len(sc.Expectations) == 0 {
		return nil
	}
	var out []schema.Issue
	for _, expectation := range sc.Expectations {
		if expectation.Tool != toolName {
			continue
		}
		if len(expectation.AnyOf) > 0 {
			if expectationAlternativeSatisfied(expectation.AnyOf, args) {
				continue
			}
			out = append(out, schema.Issue{
				Code:     "scenario_expected_argument",
				Expected: "one accepted argument pattern",
				Received: "no accepted pattern",
				Message:  fmt.Sprintf("scenario %q expected %s to match one accepted argument pattern", sc.ID, toolName),
			})
			continue
		}
		for _, key := range expectation.RequiredKeys {
			if _, ok := args[key]; !ok {
				out = append(out, schema.Issue{
					Path:     []string{key},
					Code:     "scenario_expected_argument",
					Expected: "argument required by scenario",
					Received: "missing",
					Message:  fmt.Sprintf("scenario %q expected %s.%s to be provided", sc.ID, toolName, key),
				})
			}
		}
		for _, key := range expectation.ForbiddenKeys {
			if _, ok := args[key]; ok {
				out = append(out, schema.Issue{
					Path:     []string{key},
					Code:     "scenario_forbidden_argument_key",
					Expected: "argument key to be omitted",
					Received: "present",
					Message:  fmt.Sprintf("scenario %q expected %s.%s to be omitted", sc.ID, toolName, key),
				})
			}
		}
		for key, want := range expectation.ExactArgs {
			got, ok := args[key]
			if !ok {
				continue
			}
			if !jsonValuesEqual(got, want) {
				out = append(out, schema.Issue{
					Path:     []string{key},
					Code:     "scenario_expected_argument",
					Expected: fmt.Sprintf("%v", want),
					Received: fmt.Sprintf("%v", got),
					Message:  fmt.Sprintf("scenario %q expected %s.%s to equal %v", sc.ID, toolName, key, want),
				})
			}
		}
	}
	return out
}

func expectationAlternativeSatisfied(alternatives []scenario.Expectation, args map[string]any) bool {
	for _, alternative := range alternatives {
		missing := false
		for _, key := range alternative.RequiredKeys {
			if _, ok := args[key]; !ok {
				missing = true
				break
			}
		}
		if missing {
			continue
		}
		matches := true
		for _, key := range alternative.ForbiddenKeys {
			if _, ok := args[key]; ok {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		for key, want := range alternative.ExactArgs {
			got, ok := args[key]
			if !ok || !jsonValuesEqual(got, want) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func forbiddenArgumentIssues(sc scenario.Scenario, toolName, argsRaw string) []schema.Issue {
	if len(sc.ForbiddenArgumentSubstrings) == 0 {
		return nil
	}
	var out []schema.Issue
	for _, forbidden := range sc.ForbiddenArgumentSubstrings {
		if forbidden == "" {
			continue
		}
		if strings.Contains(argsRaw, forbidden) {
			out = append(out, schema.Issue{
				Code:     "scenario_forbidden_argument_substring",
				Expected: "argument string not to contain forbidden substring",
				Received: forbidden,
				Message:  fmt.Sprintf("scenario %q forbade %s arguments containing %q", sc.ID, toolName, forbidden),
			})
		}
	}
	return out
}

func jsonValuesEqual(got, want any) bool {
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return false
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(gotJSON) == string(wantJSON)
}

func seedWorkspace(sc scenario.Scenario) (string, func(), error) {
	dir, err := os.MkdirTemp("", "toolcontracteval-workspace-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for rel, content := range sc.WorkspaceFiles {
		clean := filepath.Clean(rel)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			cleanup()
			return "", func() {}, fmt.Errorf("scenario %q has unsafe workspace path %q", sc.ID, rel)
		}
		abs := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			cleanup()
			return "", func() {}, err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
