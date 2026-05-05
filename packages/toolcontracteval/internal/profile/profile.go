package profile

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

type Profile struct {
	RunID                         string                  `json:"run_id"`
	SuiteID                       string                  `json:"suite_id"`
	Model                         string                  `json:"model"`
	Provider                      string                  `json:"provider"`
	Mode                          string                  `json:"mode"`
	PromptVariant                 PromptVariant           `json:"prompt_variant,omitempty"`
	Summary                       Summary                 `json:"summary"`
	Scenarios                     []ScenarioProfile       `json:"scenarios"`
	Tools                         []ToolProfile           `json:"tools"`
	Capabilities                  map[string]string       `json:"capabilities"`
	HarnessTuning                 []HarnessTuning         `json:"harness_tuning"`
	CandidateRuntimePromptProfile *RuntimePromptCandidate `json:"candidate_runtime_prompt_profile,omitempty"`
	Notes                         []string                `json:"notes,omitempty"`
	Metadata                      map[string]interface{}  `json:"metadata,omitempty"`
}

type PromptVariant struct {
	Label  string `json:"label,omitempty"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Chars  int    `json:"chars,omitempty"`
}

type RuntimePromptCandidate struct {
	Name         string `json:"name"`
	Match        string `json:"match"`
	Source       string `json:"source"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Content      string `json:"content"`
}

type Summary struct {
	ScenarioCount      int `json:"scenario_count"`
	CompletedCount     int `json:"completed_count"`
	ToolCalls          int `json:"tool_calls"`
	InvalidToolCalls   int `json:"invalid_tool_calls"`
	ValidationIssues   int `json:"validation_issues"`
	SkippedToolPrompts int `json:"skipped_tool_prompts"`
}

type ScenarioProfile struct {
	ID             string `json:"id"`
	ToolCalls      int    `json:"tool_calls"`
	InvalidCalls   int    `json:"invalid_calls"`
	ValidationHits int    `json:"validation_hits"`
	Completed      bool   `json:"completed"`
	Assessment     string `json:"assessment"`
	Error          string `json:"error,omitempty"`
}

type ToolProfile struct {
	Tool                      string   `json:"tool"`
	Calls                     int      `json:"calls"`
	InvalidCalls              int      `json:"invalid_calls"`
	CanonicalPathCalls        int      `json:"canonical_path_calls,omitempty"`
	FilePathAliasCalls        int      `json:"file_path_alias_calls,omitempty"`
	MixedPathAndFilePathCalls int      `json:"mixed_path_and_file_path_calls,omitempty"`
	AliasConflictCalls        int      `json:"alias_conflict_calls,omitempty"`
	CommonIssueKinds          []string `json:"common_issue_kinds,omitempty"`
}

type HarnessTuning struct {
	Area           string `json:"area"`
	Recommendation string `json:"recommendation"`
	Evidence       string `json:"evidence"`
}

func Generate(runDir string) (*Profile, error) {
	manifest, calls, failures, scenarios, err := loadArtifacts(runDir)
	if err != nil {
		return nil, err
	}
	profile := Build(manifest, calls, failures, scenarios)
	attachRuntimePromptCandidate(runDir, profile)
	if err := Write(runDir, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func Build(manifest record.Manifest, calls []record.ToolCall, failures []record.ValidationFailure, scenarios []record.ScenarioResult) *Profile {
	p := &Profile{
		RunID:    manifest.RunID,
		SuiteID:  manifest.SuiteID,
		Model:    manifest.Model,
		Provider: manifest.Provider,
		Mode:     manifest.Mode,
		PromptVariant: PromptVariant{
			Label:  manifest.SystemPromptLabel,
			Path:   manifest.SystemPromptPath,
			SHA256: manifest.SystemPromptSHA256,
			Chars:  manifest.SystemPromptChars,
		},
		Capabilities: map[string]string{},
		Metadata:     map[string]interface{}{"profile_version": "v0.1.0"},
	}
	p.Summary.ScenarioCount = len(scenarios)
	p.Summary.ToolCalls = len(calls)
	p.Summary.InvalidToolCalls = countInvalidCalls(calls)
	p.Summary.ValidationIssues = len(failures)

	for _, s := range scenarios {
		sp := ScenarioProfile{
			ID:             s.Scenario,
			ToolCalls:      s.ToolCalls,
			InvalidCalls:   s.InvalidCalls,
			ValidationHits: s.ValidationHits,
			Completed:      s.Completed,
			Error:          s.Error,
			Assessment:     scenarioAssessment(s),
		}
		if s.Completed {
			p.Summary.CompletedCount++
		}
		if s.ToolCalls == 0 {
			p.Summary.SkippedToolPrompts++
		}
		p.Scenarios = append(p.Scenarios, sp)
	}

	p.Tools = buildToolProfiles(calls, failures)
	p.Capabilities = inferCapabilities(calls, failures, scenarios)
	p.HarnessTuning = inferHarnessTuning(p, failures)
	p.Notes = inferNotes(p)
	return p
}

func Write(runDir string, p *Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "model-profile.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "model-profile.md"), []byte(Markdown(p)), 0o644)
}

func WriteSnapshot(profilesDir string, p *Profile) (string, error) {
	dir := filepath.Join(profilesDir, fileSafe(p.Provider))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(dir, fileSafe(p.Model))
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(base+".json", append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	mdPath := base + ".md"
	return mdPath, os.WriteFile(mdPath, []byte(Markdown(p)), 0o644)
}

func Markdown(p *Profile) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Model Contract Profile\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", p.Model)
	fmt.Fprintf(&b, "- Provider: `%s`\n", p.Provider)
	fmt.Fprintf(&b, "- Suite: `%s`\n", p.SuiteID)
	fmt.Fprintf(&b, "- Run: `%s`\n", p.RunID)
	if p.PromptVariant.Label != "" || p.PromptVariant.SHA256 != "" {
		fmt.Fprintf(&b, "- Prompt variant: `%s`", p.PromptVariant.Label)
		if p.PromptVariant.SHA256 != "" {
			fmt.Fprintf(&b, " (`sha256=%s`, `chars=%d`)", p.PromptVariant.SHA256, p.PromptVariant.Chars)
		}
		if p.PromptVariant.Path != "" {
			fmt.Fprintf(&b, " from `%s`", p.PromptVariant.Path)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "- Tool calls: `%d`\n", p.Summary.ToolCalls)
	fmt.Fprintf(&b, "- Invalid tool calls: `%d`\n", p.Summary.InvalidToolCalls)
	fmt.Fprintf(&b, "- Validation issues: `%d`\n", p.Summary.ValidationIssues)
	fmt.Fprintf(&b, "- Completed scenarios: `%d/%d`\n\n", p.Summary.CompletedCount, p.Summary.ScenarioCount)

	fmt.Fprintf(&b, "## Capabilities\n\n")
	for _, key := range sortedKeys(p.Capabilities) {
		fmt.Fprintf(&b, "- `%s`: %s\n", key, p.Capabilities[key])
	}
	fmt.Fprintf(&b, "\n## Scenario Behavior\n\n")
	for _, s := range p.Scenarios {
		fmt.Fprintf(&b, "- `%s`: %s (`tool_calls=%d`, `invalid=%d`, `validation_hits=%d`, `completed=%t`)\n", s.ID, s.Assessment, s.ToolCalls, s.InvalidCalls, s.ValidationHits, s.Completed)
	}
	fmt.Fprintf(&b, "\n## Tool Behavior\n\n")
	if len(p.Tools) == 0 {
		fmt.Fprintf(&b, "No tool calls were observed.\n")
	} else {
		for _, t := range p.Tools {
			fmt.Fprintf(&b, "- `%s`: `%d` calls, `%d` invalid", t.Tool, t.Calls, t.InvalidCalls)
			if t.CanonicalPathCalls > 0 || t.FilePathAliasCalls > 0 {
				fmt.Fprintf(&b, ", canonical_path=%d, file_path_alias=%d", t.CanonicalPathCalls, t.FilePathAliasCalls)
			}
			if t.MixedPathAndFilePathCalls > 0 {
				fmt.Fprintf(&b, ", mixed_path_alias=%d", t.MixedPathAndFilePathCalls)
			}
			if t.AliasConflictCalls > 0 {
				fmt.Fprintf(&b, ", alias_conflicts=%d", t.AliasConflictCalls)
			}
			if len(t.CommonIssueKinds) > 0 {
				fmt.Fprintf(&b, ", issues: `%s`", strings.Join(t.CommonIssueKinds, "`, `"))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	fmt.Fprintf(&b, "\n## Harness Tuning\n\n")
	if len(p.HarnessTuning) == 0 {
		fmt.Fprintf(&b, "No tuning recommendations from this run.\n")
	} else {
		for _, tuning := range p.HarnessTuning {
			fmt.Fprintf(&b, "- `%s`: %s Evidence: %s\n", tuning.Area, tuning.Recommendation, tuning.Evidence)
		}
	}
	if p.CandidateRuntimePromptProfile != nil {
		candidate := p.CandidateRuntimePromptProfile
		fmt.Fprintf(&b, "\n## Candidate Runtime Prompt Profile\n\n")
		fmt.Fprintf(&b, "- Suggested profile: `%s`\n", candidate.Name)
		fmt.Fprintf(&b, "- Suggested match: `%s`\n", candidate.Match)
		fmt.Fprintf(&b, "- Source: `%s`\n", candidate.Source)
		fmt.Fprintf(&b, "\nPromote manually after review:\n\n")
		fmt.Fprintf(&b, "```bash\n")
		fmt.Fprintf(&b, "go run ./cmd/toolcontracteval promote-profile --run .runs/%s --prompts-dir ../../prompts --profile-name %s --match '%s'\n", p.RunID, candidate.Name, candidate.Match)
		fmt.Fprintf(&b, "```\n")
	}
	if len(p.Notes) > 0 {
		fmt.Fprintf(&b, "\n## Notes\n\n")
		for _, note := range p.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	return b.String()
}

func loadArtifacts(runDir string) (record.Manifest, []record.ToolCall, []record.ValidationFailure, []record.ScenarioResult, error) {
	var manifest record.Manifest
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return manifest, nil, nil, nil, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, nil, nil, nil, err
	}
	calls, err := record.ReadJSONL[record.ToolCall](filepath.Join(runDir, "tool-calls.jsonl"))
	if err != nil {
		return manifest, nil, nil, nil, err
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(runDir, "validation-failures.jsonl"))
	if err != nil {
		return manifest, nil, nil, nil, err
	}
	scenarios, err := record.ReadJSONL[record.ScenarioResult](filepath.Join(runDir, "scenario-results.jsonl"))
	if err != nil {
		return manifest, nil, nil, nil, err
	}
	return manifest, calls, failures, scenarios, nil
}

func attachRuntimePromptCandidate(runDir string, p *Profile) {
	if p == nil || !isCleanProfile(p) {
		return
	}
	data, err := os.ReadFile(filepath.Join(runDir, "system-prompt.md"))
	if err != nil {
		return
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return
	}
	name := suggestedProfileName(p)
	p.CandidateRuntimePromptProfile = &RuntimePromptCandidate{
		Name:         name,
		Match:        suggestedProfileMatch(p, name),
		Source:       "system-prompt.md",
		SourceSHA256: p.PromptVariant.SHA256,
		Content:      content,
	}
}

func isCleanProfile(p *Profile) bool {
	if p.Summary.ScenarioCount == 0 ||
		p.Summary.CompletedCount != p.Summary.ScenarioCount ||
		p.Summary.InvalidToolCalls != 0 ||
		p.Summary.ValidationIssues != 0 {
		return false
	}
	for _, scenario := range p.Scenarios {
		if scenario.ValidationHits != 0 {
			return false
		}
	}
	return true
}

func suggestedProfileName(p *Profile) string {
	family := modelFamily(p.Model)
	if family != "" {
		return fileSafe(family)
	}
	if p.Provider != "" {
		return fileSafe(p.Provider)
	}
	return fileSafe(p.Model)
}

func suggestedProfileMatch(p *Profile, name string) string {
	family := modelFamily(p.Model)
	if family == "" {
		return strings.TrimSpace(p.Model)
	}
	if family == name {
		return family + "-*"
	}
	return strings.TrimSpace(p.Model)
}

func modelFamily(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return ""
	}
	if i := strings.IndexAny(model, "-_"); i > 0 {
		return model[:i]
	}
	return model
}

func scenarioAssessment(s record.ScenarioResult) string {
	if s.Error != "" {
		return "provider or runner error"
	}
	if s.ToolCalls == 0 {
		return "skipped tool use"
	}
	if s.InvalidCalls > 0 {
		return "tool contract mismatch"
	}
	if s.ValidationHits > 0 {
		return "scenario contract mismatch"
	}
	if !s.Completed {
		return "valid tool use but did not finish within step budget"
	}
	return "clean"
}

func buildToolProfiles(calls []record.ToolCall, failures []record.ValidationFailure) []ToolProfile {
	byTool := map[string]*ToolProfile{}
	issueKinds := map[string]map[string]int{}
	for _, call := range calls {
		tp := byTool[call.Tool]
		if tp == nil {
			tp = &ToolProfile{Tool: call.Tool}
			byTool[call.Tool] = tp
		}
		tp.Calls++
		recordPathAliasUse(tp, call.ArgumentsRaw)
		if !call.Valid {
			tp.InvalidCalls++
		}
	}
	for _, failure := range failures {
		if issueKinds[failure.Tool] == nil {
			issueKinds[failure.Tool] = map[string]int{}
		}
		issueKinds[failure.Tool][failure.Issue.Code]++
	}
	out := make([]ToolProfile, 0, len(byTool))
	for tool, tp := range byTool {
		for kind := range issueKinds[tool] {
			tp.CommonIssueKinds = append(tp.CommonIssueKinds, kind)
		}
		sort.Strings(tp.CommonIssueKinds)
		out = append(out, *tp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

func inferCapabilities(calls []record.ToolCall, failures []record.ValidationFailure, scenarios []record.ScenarioResult) map[string]string {
	caps := map[string]string{
		"json_shape_validity":   "unobserved",
		"required_tool_use":     "unobserved",
		"read_window_intent":    "unobserved",
		"markdown_path_leakage": "unobserved",
		"retry_recovery":        "unobserved",
	}
	if len(calls) > 0 {
		if countInvalidCalls(calls) == 0 {
			caps["json_shape_validity"] = "clean"
		} else {
			caps["json_shape_validity"] = "mixed"
		}
	}
	if scenarioByID(scenarios, "array-container-pressure").Scenario != "" || scenarioByID(scenarios, "optional-null-pressure").Scenario != "" {
		if scenarioByID(scenarios, "array-container-pressure").ToolCalls == 0 || scenarioByID(scenarios, "optional-null-pressure").ToolCalls == 0 {
			caps["required_tool_use"] = "weak"
		} else {
			caps["required_tool_use"] = "observed"
		}
	} else if len(calls) > 0 && skippedToolPrompts(scenarios) == 0 {
		caps["required_tool_use"] = "observed"
	}
	if hasScenario(scenarios, "read-window-relational") {
		if scenarioByID(scenarios, "read-window-relational").InvalidCalls > 0 {
			caps["read_window_intent"] = "weak"
		} else if scenarioByID(scenarios, "read-window-relational").ToolCalls > 0 {
			caps["read_window_intent"] = "clean"
		}
	} else if hasScenario(scenarios, "read-first-lines-contract") {
		if scenarioByID(scenarios, "read-first-lines-contract").InvalidCalls > 0 {
			caps["read_window_intent"] = "weak"
		} else if scenarioByID(scenarios, "read-first-lines-contract").ToolCalls > 0 {
			caps["read_window_intent"] = "clean"
		}
	}
	if hasScenario(scenarios, "markdown-path-leakage") {
		if failureCodeForScenario(failures, "markdown-path-leakage", "path_markdown_autolink") {
			caps["markdown_path_leakage"] = "leaky"
		} else if scenarioByID(scenarios, "markdown-path-leakage").ToolCalls > 0 {
			caps["markdown_path_leakage"] = "clean"
		}
	} else if hasScenario(scenarios, "path-string-no-markdown") {
		if failureCodeForScenario(failures, "path-string-no-markdown", "path_markdown_autolink") {
			caps["markdown_path_leakage"] = "leaky"
		} else if scenarioByID(scenarios, "path-string-no-markdown").ToolCalls > 0 {
			caps["markdown_path_leakage"] = "clean"
		}
	}
	if retryFailures := failuresByScenario(failures); len(retryFailures) > 0 {
		caps["retry_recovery"] = "needs profiling"
	} else if hasScenario(scenarios, "read-bad-path-recovery") {
		if s := scenarioByID(scenarios, "read-bad-path-recovery"); s.Completed && s.ToolCalls > 1 {
			caps["retry_recovery"] = "clean"
		}
	}
	return caps
}

func inferHarnessTuning(p *Profile, failures []record.ValidationFailure) []HarnessTuning {
	var out []HarnessTuning
	if p.Capabilities["read_window_intent"] == "clean" {
		out = append(out, HarnessTuning{
			Area:           "read",
			Recommendation: "Keep semantic convenience fields such as first_lines visible for this model.",
			Evidence:       "Read-window scenario completed without validation failures.",
		})
	}
	if p.Capabilities["read_window_intent"] == "weak" {
		out = append(out, HarnessTuning{
			Area:           "read",
			Recommendation: "Do not rely on this model to preserve first-N-line intent from prose; consider stronger prompts or semantic fallback.",
			Evidence:       "Read-window scenario produced repeated scenario_expected_argument failures.",
		})
	}
	if p.Capabilities["required_tool_use"] == "weak" {
		out = append(out, HarnessTuning{
			Area:           "tool-choice",
			Recommendation: "Profile whether this model needs explicit tool-choice forcing for exact tool-use tasks.",
			Evidence:       "At least one exact-once tool scenario completed without any tool calls.",
		})
	}
	for _, c := range cluster.FromFailures(failures) {
		if c.IssueKind == "path_markdown_autolink" {
			out = append(out, HarnessTuning{
				Area:           "path-strings",
				Recommendation: "Keep markdown path unwrapping or stronger pathString schema hints for this model.",
				Evidence:       fmt.Sprintf("%d markdown path failures for %s.", c.Count, c.Tool),
			})
		}
		if c.Tool == "read" && c.IssueKind == "required" && strings.Contains(c.ExampleArgs, `"file_path"`) {
			out = append(out, HarnessTuning{
				Area:           "read",
				Recommendation: "Rerun with the alias-aware schema; file_path is now valid and should be recorded as model preference telemetry.",
				Evidence:       fmt.Sprintf("%d historical read calls used file_path where path was previously required.", c.Count),
			})
		}
	}
	for _, t := range p.Tools {
		if t.FilePathAliasCalls > 0 {
			out = append(out, HarnessTuning{
				Area:           t.Tool,
				Recommendation: "Treat file_path as an observed model alias preference while keeping path as the canonical contract field.",
				Evidence:       fmt.Sprintf("%d/%d %s calls used file_path; %d calls used path.", t.FilePathAliasCalls, t.Calls, t.Tool, t.CanonicalPathCalls),
			})
		}
		if t.AliasConflictCalls > 0 {
			out = append(out, HarnessTuning{
				Area:           t.Tool,
				Recommendation: "Keep detecting path/file_path conflicts; do not silently choose between different path values.",
				Evidence:       fmt.Sprintf("%d %s calls supplied conflicting path and file_path values.", t.AliasConflictCalls, t.Tool),
			})
		}
	}
	return out
}

func inferNotes(p *Profile) []string {
	var notes []string
	if p.Summary.ToolCalls == 0 {
		notes = append(notes, "No tool calls were observed; profile is not sufficient for tool-contract tuning.")
	}
	if p.Summary.SkippedToolPrompts > 0 {
		notes = append(notes, "Some scenarios completed without required tool calls; add required-tool-call assertions before treating this as a full pass.")
	}
	if p.Summary.InvalidToolCalls == 0 && p.Summary.ToolCalls > 0 {
		notes = append(notes, "Observed tool calls were schema-valid; remaining profile questions are semantic or behavioral.")
	}
	return notes
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

func recordPathAliasUse(tp *ToolProfile, raw string) {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return
	}
	pathValue, hasPath := args["path"].(string)
	filePathValue, hasFilePath := args["file_path"].(string)
	if hasPath {
		tp.CanonicalPathCalls++
	}
	if hasFilePath {
		tp.FilePathAliasCalls++
	}
	if hasPath && hasFilePath {
		tp.MixedPathAndFilePathCalls++
		if pathValue != filePathValue {
			tp.AliasConflictCalls++
		}
	}
}

func scenarioByID(scenarios []record.ScenarioResult, id string) record.ScenarioResult {
	for _, scenario := range scenarios {
		if scenario.Scenario == id {
			return scenario
		}
	}
	return record.ScenarioResult{}
}

func hasScenario(scenarios []record.ScenarioResult, id string) bool {
	return scenarioByID(scenarios, id).Scenario != ""
}

func failureCodeForScenario(failures []record.ValidationFailure, scenario, code string) bool {
	for _, failure := range failures {
		if failure.Scenario == scenario && failure.Issue.Code == code {
			return true
		}
	}
	return false
}

func failuresByScenario(failures []record.ValidationFailure) map[string]int {
	out := map[string]int{}
	for _, failure := range failures {
		out[failure.Scenario]++
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func skippedToolPrompts(scenarios []record.ScenarioResult) int {
	n := 0
	for _, scenario := range scenarios {
		if scenario.ToolCalls == 0 {
			n++
		}
	}
	return n
}

func fileSafe(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
