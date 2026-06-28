package descriptions

import (
	"strings"
	"testing"
)

func TestLoadReturnsContentForExistingFile(t *testing.T) {
	t.Parallel()

	// cron_create.md is known to exist in the embedded filesystem.
	result := Load("cron_create")
	if result == "" {
		t.Fatalf("expected non-empty content for cron_create")
	}
	// The description should reference cron/scheduling concepts.
	lower := strings.ToLower(result)
	if !strings.Contains(lower, "cron") && !strings.Contains(lower, "schedul") {
		t.Fatalf("expected cron-related content, got %q", result)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	t.Parallel()

	result := Load("cron_create")
	if result != strings.TrimSpace(result) {
		t.Fatalf("expected trimmed output, got leading/trailing whitespace")
	}
}

func TestLoadPanicsForMissingFile(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic for missing tool description")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "missing tool description") {
			t.Fatalf("expected 'missing tool description' in panic message, got %q", msg)
		}
		if !strings.Contains(msg, "nonexistent_tool.md") {
			t.Fatalf("expected filename in panic message, got %q", msg)
		}
	}()

	Load("nonexistent_tool")
}

func TestLoadAllKnownDescriptions(t *testing.T) {
	t.Parallel()

	// Verify all known embedded descriptions load without panic.
	// This list must be kept in sync with the .md files in this directory.
	names := []string{
		"AskUserQuestion",
		"agent",
		"agentic_fetch",
		"apply_patch",
		"bash",
		"cancel_delayed_callback",
		"compact_history",
		"connect_mcp",
		"context_status",
		"create_profile",
		"create_prompt_extension",
		"create_skill",
		"create_workflow",
		"cron_create",
		"cron_delete",
		"cron_get",
		"cron_list",
		"cron_pause",
		"cron_resume",
		"deploy",
		"download",
		"edit",
		"fetch",
		"file_inspect",
		"find_tool",
		"get_efficiency_report",
		"get_profile",
		"get_profile_manifest",
		"git_blame_context",
		"git_contributor_context",
		"git_diff",
		"git_diff_range",
		"git_file_history",
		"git_log_search",
		"git_status",
		"glob",
		"grep",
		"job_kill",
		"job_output",
		"list_conversations",
		"list_delayed_callbacks",
		"list_mcp_resources",
		"list_models",
		"list_profiles",
		"ls",
		"lsp_diagnostics",
		"lsp_references",
		"lsp_restart",
		"manage_skill_packs",
		"observational_memory",
		"read",
		"read_mcp_resource",
		"delete_profile",
		"recommend_profile",
		"reset_context",
		"run_agent",
		"run_recipe",
		"run_workflow",
		"search_conversations",
		"set_delayed_callback",
		"skill",
		"sourcegraph",
		"todos",
		"update_profile",
		"validate_profile",
		"verify_skill",
		"web_fetch",
		"web_search",
		"write",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			result := Load(name)
			if result == "" {
				t.Fatalf("expected non-empty content for %s", name)
			}
		})
	}
}

// TestAllEmbeddedDescriptionsAreNonEmpty dynamically discovers every .md file
// in the embedded FS and verifies it loads to a non-empty string. This catches
// newly-added files that are accidentally empty without requiring a manual update
// to TestLoadAllKnownDescriptions.
func TestAllEmbeddedDescriptionsAreNonEmpty(t *testing.T) {
	t.Parallel()

	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("embedded FS is empty — no description files found")
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		toolName := strings.TrimSuffix(name, ".md")
		t.Run(toolName, func(t *testing.T) {
			result := Load(toolName)
			if result == "" {
				t.Fatalf("description file %s exists but Load(%q) returned empty string", name, toolName)
			}
		})
	}
}

// TestEmbeddedFSAndKnownListAreInSync verifies that the hardcoded list in
// TestLoadAllKnownDescriptions matches exactly the .md files in the embedded FS.
// This prevents the two lists from drifting apart silently.
func TestEmbeddedFSAndKnownListAreInSync(t *testing.T) {
	t.Parallel()

	knownNames := map[string]bool{
		"AskUserQuestion":         true,
		"agent":                   true,
		"agentic_fetch":           true,
		"apply_patch":             true,
		"bash":                    true,
		"cancel_delayed_callback": true,
		"compact_history":         true,
		"cancel_subagent":         true,
		"connect_mcp":             true,
		"context_status":          true,
		"create_profile":          true,
		"create_prompt_extension": true,
		"create_skill":            true,
		"create_workflow":         true,
		"cron_create":             true,
		"cron_delete":             true,
		"cron_get":                true,
		"cron_list":               true,
		"cron_pause":              true,
		"cron_resume":             true,
		"delete_profile":          true,
		"deploy":                  true,
		"download":                true,
		"edit":                    true,
		"fetch":                   true,
		"file_inspect":            true,
		"find_tool":               true,
		"get_efficiency_report":   true,
		"get_subagent":            true,
		"get_profile":             true,
		"get_profile_manifest":    true,
		"git_blame_context":       true,
		"git_contributor_context": true,
		"git_diff":                true,
		"git_diff_range":          true,
		"git_file_history":        true,
		"git_log_search":          true,
		"git_status":              true,
		"glob":                    true,
		"grep":                    true,
		"job_kill":                true,
		"job_output":              true,
		"list_conversations":      true,
		"list_delayed_callbacks":  true,
		"list_mcp_resources":      true,
		"list_models":             true,
		"list_profiles":           true,
		"ls":                      true,
		"lsp_diagnostics":         true,
		"lsp_references":          true,
		"lsp_restart":             true,
		"manage_skill_packs":      true,
		"observational_memory":    true,
		"read":                    true,
		"read_mcp_resource":       true,
		"recommend_profile":       true,
		"reset_context":           true,
		"run_agent":               true,
		"start_subagent":          true,
		"run_recipe":              true,
		"run_workflow":            true,
		"wait_subagent":           true,
		"search_conversations":    true,
		"set_delayed_callback":    true,
		"skill":                   true,
		"sourcegraph":             true,
		"todos":                   true,
		"update_profile":          true,
		"validate_profile":        true,
		"verify_skill":            true,
		"web_fetch":               true,
		"web_search":              true,
		"write":                   true,
	}

	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded directory: %v", err)
	}

	fsNames := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".md") {
			fsNames[strings.TrimSuffix(name, ".md")] = true
		}
	}

	for name := range fsNames {
		if !knownNames[name] {
			t.Errorf("FS contains %q but it is missing from the known list — add it to TestLoadAllKnownDescriptions", name)
		}
	}
	for name := range knownNames {
		if !fsNames[name] {
			t.Errorf("known list contains %q but no corresponding .md file exists in the embedded FS", name)
		}
	}
}

func TestFSContainsEmbeddedFiles(t *testing.T) {
	t.Parallel()

	// Verify the embedded FS is accessible and contains at least one .md file.
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded directory: %v", err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected at least one .md file in embedded FS")
	}
}

// TestToolDescriptionsContainBehavioralDirectives verifies that high-priority
// tool descriptions include behavioral guidance sections — WHEN TO USE,
// WHEN NOT TO USE, common mistakes, cross-tool disambiguation, or
// behavioral rules. These directives help the LLM choose the right tool
// for each task and avoid common misuse patterns.
// See: https://github.com/dennisonbertram/go-agent-harness/issues/495
func TestToolDescriptionsContainBehavioralDirectives(t *testing.T) {
	t.Parallel()

	behavioralPatterns := []string{
		"when not to use",
		"when to use",
		"common mistakes",
		"behavioral rules",
		"do not use",
		"do not write",
		"do not run",
		"do not call",
		"prefer",
		"instead of",
		"use this tool when",
		"use this tool if",
	}

	// Tools that agents rely on heavily and that have the highest impact
	// on correctness when misused.
	highPriority := []string{
		"agent",
		"apply_patch",
		"bash",
		"edit",
		"fetch",
		"find_tool",
		"git_diff",
		"git_status",
		"glob",
		"grep",
		"read",
		"write",
	}

	for _, name := range highPriority {
		t.Run(name, func(t *testing.T) {
			desc := strings.ToLower(Load(name))
			found := false
			for _, pattern := range behavioralPatterns {
				if strings.Contains(desc, pattern) {
					found = true
					break
				}
			}
			if !found {
				// Show which patterns were searched to aid diagnosis.
				t.Errorf("tool %q description must contain at least one behavioral directive "+
					"(e.g. 'when not to use', 'when to use', 'common mistakes', 'behavioral rules', "+
					"cross-tool disambiguation like 'prefer X instead of Y', or "+
					"'do not use this tool for'). Searched %d patterns.", name, len(behavioralPatterns))
			}
		})
	}
}

// TestBashDescriptionContainsGoTestGuidance verifies that the bash tool
// description includes guidance on interpreting Go test output correctly.
// Without -v, go test produces only a single "ok" summary line per package.
// Agents misread this as "1 test passed" when many tests may have run.
// The description must instruct the agent to use -v when test count matters.
// See: https://github.com/dennisonbertram/go-agent-harness/issues/94
func TestBashDescriptionContainsGoTestGuidance(t *testing.T) {
	t.Parallel()

	desc := Load("bash")
	lower := strings.ToLower(desc)

	// Must mention go test verbosity guidance.
	if !strings.Contains(lower, "go test") {
		t.Errorf("bash description must mention 'go test' to guide agents on test output interpretation")
	}

	// Must mention -v flag to get per-test output.
	if !strings.Contains(desc, "-v") {
		t.Errorf("bash description must mention '-v' flag for go test verbose output")
	}

	// Must explain that the "ok" line is a package-level summary, not a single test.
	if !strings.Contains(lower, "package") {
		t.Errorf("bash description must mention 'package' to clarify that go test output is per-package")
	}
}
