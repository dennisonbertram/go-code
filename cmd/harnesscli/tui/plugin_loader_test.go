package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRegisterPlugins_RegistersPromptPlugin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginJSON := `{
		"name": "summarize",
		"description": "Summarize the current topic",
		"handler": "prompt",
		"prompt_template": "Summarize: {args}"
	}`
	if err := os.WriteFile(filepath.Join(dir, "summarize.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	registry := NewCommandRegistry()
	warnings := LoadAndRegisterPlugins(registry, dir)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	entry, ok := registry.Lookup("summarize")
	if !ok {
		t.Fatal("expected summarize plugin command to be registered")
	}
	result := entry.Handler(Command{Name: "summarize", Args: []string{"release", "notes"}, Raw: "/summarize release notes"})
	if result.Status != CmdOK {
		t.Fatalf("expected CmdOK, got %v with output %q", result.Status, result.Output)
	}
	if result.Output != "Summarize: release notes" {
		t.Fatalf("unexpected plugin output: %q", result.Output)
	}
}

func TestLoadAndRegisterPlugins_PromptPluginQuotedArgs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginJSON := `{
		"name": "deploy",
		"description": "Deploy somewhere",
		"handler": "prompt",
		"prompt_template": "Deploy: {args}"
	}`
	if err := os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	registry := NewCommandRegistry()
	if warnings := LoadAndRegisterPlugins(registry, dir); len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	entry, ok := registry.Lookup("deploy")
	if !ok {
		t.Fatal("expected deploy plugin command to be registered")
	}
	// Quoted multi-word arguments are tokenized quote-aware (shared SplitArgs
	// semantics): quote syntax is grouping, not literal output.
	result := entry.Handler(Command{Name: "deploy", Raw: `/deploy "staging eu" fast`})
	if result.Status != CmdOK {
		t.Fatalf("expected CmdOK, got %v with output %q", result.Status, result.Output)
	}
	if result.Output != "Deploy: staging eu fast" {
		t.Fatalf("unexpected plugin output: %q", result.Output)
	}
}

func TestLoadAndRegisterPlugins_SkipsCommandCollisions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginJSON := `{
		"name": "help",
		"description": "Overrides built-in help",
		"handler": "prompt",
		"prompt_template": "nope"
	}`
	if err := os.WriteFile(filepath.Join(dir, "help.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	registry := NewCommandRegistry()
	warnings := LoadAndRegisterPlugins(registry, dir)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "already registered") {
		t.Fatalf("expected collision warning, got %q", warnings[0])
	}

	entry, ok := registry.Lookup("help")
	if !ok {
		t.Fatal("expected built-in help command to remain registered")
	}
	result := entry.Handler(Command{Name: "help"})
	if result.Status != CmdOK {
		t.Fatalf("expected built-in help command to remain intact, got status %v", result.Status)
	}
}

func TestLegacyPluginsDirWarning_NonEmptyLegacyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginJSON := `{
		"name": "summarize",
		"description": "Summarize the current topic",
		"handler": "prompt",
		"prompt_template": "Summarize: {args}"
	}`
	if err := os.WriteFile(filepath.Join(dir, "summarize.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	warning := legacyPluginsDirWarning(dir)
	if warning == "" {
		t.Fatal("expected a deprecation warning for a legacy dir containing JSON plugins")
	}
	if !strings.Contains(warning, dir) {
		t.Fatalf("expected warning to name the legacy dir %q, got %q", dir, warning)
	}
	if !strings.Contains(warning, "deprecated") {
		t.Fatalf("expected warning to mark the legacy format deprecated, got %q", warning)
	}
	if !strings.Contains(warning, ".go-harness/plugins") {
		t.Fatalf("expected warning to point at the bundle home, got %q", warning)
	}
	if !strings.Contains(warning, "plugin.json") {
		t.Fatalf("expected warning to point at the bundle manifest, got %q", warning)
	}
}

func TestLegacyPluginsDirWarning_NoWarningCases(t *testing.T) {
	t.Parallel()

	t.Run("missing dir", func(t *testing.T) {
		t.Parallel()
		if warning := legacyPluginsDirWarning(filepath.Join(t.TempDir(), "does-not-exist")); warning != "" {
			t.Fatalf("expected no warning for a missing dir, got %q", warning)
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		t.Parallel()
		if warning := legacyPluginsDirWarning(t.TempDir()); warning != "" {
			t.Fatalf("expected no warning for an empty dir, got %q", warning)
		}
	})

	t.Run("no json files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hi"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if warning := legacyPluginsDirWarning(dir); warning != "" {
			t.Fatalf("expected no warning for a dir without JSON plugins, got %q", warning)
		}
	})
}
