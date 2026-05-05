package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	pcatalog "go-agent-harness/internal/provider/catalog"
	"go-agent-harness/packages/toolcontracteval/internal/profile"
	"go-agent-harness/packages/toolcontracteval/internal/record"
	"go-agent-harness/packages/toolcontracteval/internal/repair"
	"go-agent-harness/packages/toolcontracteval/internal/report"
	evalrun "go-agent-harness/packages/toolcontracteval/internal/run"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "toolcontracteval:", err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:])
	case "report":
		return reportCommand(args[1:])
	case "profile":
		return profileCommand(args[1:])
	case "promote-profile":
		return promoteProfileCommand(args[1:])
	case "replay":
		return replayCommand(args[1:])
	case "list-suites":
		return listSuitesCommand(args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	suiteFlag := fs.String("suite", "api-harness-production", "suite id or path")
	model := fs.String("model", "deepseek-v4-pro", "model id or catalog alias")
	provider := fs.String("provider", "", "provider key; empty resolves from catalog")
	apiBaseURL := fs.String("api-base-url", "http://127.0.0.1:8080", "harnessd base URL")
	apiKey := fs.String("api-key", "", "optional harnessd bearer token")
	systemPrompt := fs.String("system-prompt", "", "optional explicit system prompt override")
	systemPromptFile := fs.String("system-prompt-file", "", "optional file containing a system prompt override")
	systemPromptLabel := fs.String("system-prompt-label", "", "optional label for prompt-variant reporting")
	out := fs.String("out", ".runs", "output directory")
	runID := fs.String("run-id", "", "optional run id")
	maxTurns := fs.Int("max-turns", 0, "override max turns")
	repoRoot := fs.String("repo-root", "", "repository root containing catalog/models.json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := resolveRepoRoot(*repoRoot)
	if err != nil {
		return err
	}
	suitePath := resolveSuitePath(*suiteFlag)
	resolvedProvider := *provider
	resolvedModel := *model
	if resolvedProvider == "" {
		resolvedProvider, resolvedModel, _ = resolveProviderModel(root, *model)
	}
	promptOverride, promptPath, err := resolveSystemPromptOverride(*systemPrompt, *systemPromptFile)
	if err != nil {
		return err
	}
	promptLabel := strings.TrimSpace(*systemPromptLabel)
	if promptOverride != "" && promptLabel == "" {
		if promptPath != "" {
			promptLabel = strings.TrimSuffix(filepath.Base(promptPath), filepath.Ext(promptPath))
		} else {
			promptLabel = "inline"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	result, err := evalrun.Execute(ctx, evalrun.Options{
		SuitePath:         suitePath,
		OutDir:            *out,
		RunID:             *runID,
		Model:             resolvedModel,
		Provider:          resolvedProvider,
		Mode:              "api",
		APIBaseURL:        *apiBaseURL,
		APIKey:            *apiKey,
		SystemPrompt:      promptOverride,
		SystemPromptLabel: promptLabel,
		SystemPromptPath:  promptPath,
		MaxTurns:          *maxTurns,
	})
	if err != nil {
		return err
	}
	fmt.Printf("run_id=%s\nrun_dir=%s\nreport=%s\n", result.RunID, result.RunDir, filepath.Join(result.RunDir, "report.md"))
	return nil
}

func reportCommand(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	runDir := fs.String("run", "", "run directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return fmt.Errorf("--run is required")
	}
	_, err := report.Generate(*runDir)
	if err != nil {
		return err
	}
	if _, err := profile.Generate(*runDir); err != nil {
		return err
	}
	fmt.Println(filepath.Join(*runDir, "report.md"))
	return nil
}

func profileCommand(args []string) error {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	runDir := fs.String("run", "", "run directory")
	profilesDir := fs.String("profiles-dir", "", "optional durable profiles directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return fmt.Errorf("--run is required")
	}
	p, err := profile.Generate(*runDir)
	if err != nil {
		return err
	}
	fmt.Println(filepath.Join(*runDir, "model-profile.md"))
	if *profilesDir != "" {
		path, err := profile.WriteSnapshot(*profilesDir, p)
		if err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

func promoteProfileCommand(args []string) error {
	fs := flag.NewFlagSet("promote-profile", flag.ContinueOnError)
	runDir := fs.String("run", "", "run directory with model-profile artifacts")
	promptsDir := fs.String("prompts-dir", "", "runtime prompts directory containing catalog.yaml")
	profileName := fs.String("profile-name", "", "approved runtime profile name to write, for example deepseek")
	match := fs.String("match", "", "model glob to add to prompts catalog; defaults to <profile-name>-*")
	promptFile := fs.String("prompt-file", "", "candidate prompt file; defaults to <run>/system-prompt.md")
	dryRun := fs.Bool("dry-run", false, "show the promotion plan without writing files")
	force := fs.Bool("force", false, "allow promotion from a run with validation issues or incomplete scenarios")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*runDir) == "" {
		return fmt.Errorf("--run is required")
	}
	if strings.TrimSpace(*promptsDir) == "" {
		return fmt.Errorf("--prompts-dir is required")
	}
	name := strings.TrimSpace(*profileName)
	if name == "" {
		return fmt.Errorf("--profile-name is required")
	}
	if safeProfileName(name) != name {
		return fmt.Errorf("--profile-name must be lowercase file-safe text, got %q", name)
	}
	modelMatch := strings.TrimSpace(*match)
	if modelMatch == "" {
		modelMatch = name + "-*"
	}

	p, err := profile.Generate(*runDir)
	if err != nil {
		return err
	}
	if !*force && !profileIsPromotable(p) {
		return fmt.Errorf("run %q is not clean enough to promote: completed=%d/%d invalid_tool_calls=%d validation_issues=%d (use --force to override)",
			p.RunID,
			p.Summary.CompletedCount,
			p.Summary.ScenarioCount,
			p.Summary.InvalidToolCalls,
			p.Summary.ValidationIssues,
		)
	}

	sourcePrompt := strings.TrimSpace(*promptFile)
	if sourcePrompt == "" {
		sourcePrompt = filepath.Join(*runDir, "system-prompt.md")
	}
	content, err := os.ReadFile(sourcePrompt)
	if err != nil {
		return fmt.Errorf("read candidate prompt %s: %w", sourcePrompt, err)
	}
	promptContent := strings.TrimSpace(string(content))
	if promptContent == "" {
		return fmt.Errorf("candidate prompt %s is empty", sourcePrompt)
	}

	profileRelPath := filepath.ToSlash(filepath.Join("models", name+".md"))
	profilePath := filepath.Join(*promptsDir, filepath.FromSlash(profileRelPath))
	catalogPath := filepath.Join(*promptsDir, "catalog.yaml")

	if *dryRun {
		fmt.Printf("dry_run=true\nprofile=%s\nmatch=%s\nprompt_file=%s\ncatalog=%s\n", profilePath, modelMatch, sourcePrompt, catalogPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(profilePath, []byte(ensureTrailingNewline(promptContent)), 0o644); err != nil {
		return err
	}
	if err := upsertPromptCatalogProfile(catalogPath, name, modelMatch, profileRelPath); err != nil {
		return err
	}
	fmt.Printf("profile=%s\ncatalog=%s\n", profilePath, catalogPath)
	return nil
}

func replayCommand(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	runDir := fs.String("run", "", "run directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return fmt.Errorf("--run is required")
	}
	defs, err := loadDefinitions(filepath.Join(*runDir, "tool-definitions.json"))
	if err != nil {
		return err
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(*runDir, "validation-failures.jsonl"))
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(*runDir, "repair-simulation.jsonl"))
	for _, failure := range failures {
		def, ok := defs[failure.Tool]
		if !ok {
			continue
		}
		for _, sim := range repair.SimulateAll(failure.Tool, json.RawMessage(failure.ArgumentsRaw), def.Parameters) {
			if err := record.AppendJSONL(filepath.Join(*runDir, "repair-simulation.jsonl"), map[string]any{
				"run_id":                 failure.RunID,
				"scenario":               failure.Scenario,
				"turn":                   failure.Turn,
				"tool":                   failure.Tool,
				"call_id":                failure.CallID,
				"repair":                 sim.Repair,
				"safety":                 sim.Safety,
				"before_valid":           sim.BeforeValid,
				"applied":                sim.Applied,
				"after_valid":            sim.AfterValid,
				"semantic_note_required": sim.SemanticNoteRequired,
				"repaired_arguments":     sim.RepairedArguments,
				"issues_after":           sim.IssuesAfter,
			}); err != nil {
				return err
			}
		}
	}
	_, err = report.Generate(*runDir)
	if err != nil {
		return err
	}
	fmt.Println(filepath.Join(*runDir, "repair-simulation.jsonl"))
	return nil
}

func listSuitesCommand(args []string) error {
	fs := flag.NewFlagSet("list-suites", flag.ContinueOnError)
	dir := fs.String("dir", "suites", "suite directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(*dir, "*.json"))
	if err != nil {
		return err
	}
	for _, match := range matches {
		fmt.Println(strings.TrimSuffix(filepath.Base(match), ".json"))
	}
	return nil
}

func resolveProviderModel(repoRoot, modelFlag string) (string, string, error) {
	cat, err := pcatalog.LoadCatalog(filepath.Join(repoRoot, "catalog", "models.json"))
	if err != nil {
		return "", modelFlag, err
	}
	registry := pcatalog.NewProviderRegistry(cat)
	providerName, modelID, found := registry.ResolveProviderAndModel(modelFlag)
	if !found {
		return "", modelFlag, nil
	}
	return providerName, modelID, nil
}

func resolveSystemPromptOverride(inlinePrompt, promptFile string) (string, string, error) {
	inlinePrompt = strings.TrimSuffix(inlinePrompt, "\r\n")
	promptFile = strings.TrimSpace(promptFile)
	if strings.TrimSpace(inlinePrompt) != "" && promptFile != "" {
		return "", "", fmt.Errorf("--system-prompt and --system-prompt-file are mutually exclusive")
	}
	if promptFile == "" {
		return inlinePrompt, "", nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", "", err
	}
	return string(data), promptFile, nil
}

func loadDefinitions(path string) (map[string]harness.ToolDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var defs []harness.ToolDefinition
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, err
	}
	out := make(map[string]harness.ToolDefinition, len(defs))
	for _, def := range defs {
		out[def.Name] = def.Clone()
	}
	return out, nil
}

func resolveRepoRoot(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "catalog", "models.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", wd)
		}
	}
}

func resolveSuitePath(value string) string {
	if strings.HasSuffix(value, ".json") || strings.ContainsRune(value, filepath.Separator) {
		return value
	}
	return filepath.Join("suites", value+".json")
}

type promptCatalog struct {
	Version       int                    `yaml:"version"`
	Defaults      map[string]string      `yaml:"defaults"`
	Intents       map[string]string      `yaml:"intents"`
	ModelProfiles []promptCatalogProfile `yaml:"model_profiles"`
	Extensions    map[string]string      `yaml:"extensions"`
}

type promptCatalogProfile struct {
	Name  string `yaml:"name"`
	Match string `yaml:"match"`
	File  string `yaml:"file"`
}

func upsertPromptCatalogProfile(catalogPath, name, match, file string) error {
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return err
	}
	var cfg promptCatalog
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", catalogPath, err)
	}
	entry := promptCatalogProfile{Name: name, Match: match, File: file}
	for i := range cfg.ModelProfiles {
		if cfg.ModelProfiles[i].Name == name {
			cfg.ModelProfiles[i] = entry
			return writePromptCatalog(catalogPath, cfg)
		}
	}
	insertAt := len(cfg.ModelProfiles)
	defaultName := strings.TrimSpace(cfg.Defaults["model_profile"])
	for i, existing := range cfg.ModelProfiles {
		if existing.Name == defaultName {
			insertAt = i
			break
		}
	}
	cfg.ModelProfiles = append(cfg.ModelProfiles, promptCatalogProfile{})
	copy(cfg.ModelProfiles[insertAt+1:], cfg.ModelProfiles[insertAt:])
	cfg.ModelProfiles[insertAt] = entry
	return writePromptCatalog(catalogPath, cfg)
}

func writePromptCatalog(path string, cfg promptCatalog) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func profileIsPromotable(p *profile.Profile) bool {
	if p == nil ||
		p.Summary.ScenarioCount == 0 ||
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

func safeProfileName(s string) string {
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
	return strings.Trim(b.String(), "-")
}

func ensureTrailingNewline(s string) string {
	s = strings.TrimRight(s, "\r\n")
	return s + "\n"
}

func usage() {
	fmt.Println(`toolcontracteval commands:
  run          run a live eval suite
  report       regenerate report.md for a run directory
  profile      regenerate model-profile artifacts for a run directory
  promote-profile promote a clean run's system-prompt.md into prompts/models and catalog.yaml
  replay       rerun offline repair simulation for a run directory
  list-suites  list bundled suite ids`)
}
