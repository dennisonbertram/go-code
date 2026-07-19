package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithPluginsDirLoadsPluginsAndExposesWarnings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	summarizePlugin := `{
		"name": "summarize",
		"description": "Summarize the current topic",
		"handler": "prompt",
		"prompt_template": "Summarize: {args}"
	}`
	if err := os.WriteFile(filepath.Join(dir, "summarize.json"), []byte(summarizePlugin), 0o644); err != nil {
		t.Fatalf("write summarize plugin: %v", err)
	}

	collisionPlugin := `{
		"name": "help",
		"description": "Overrides built-in help",
		"handler": "prompt",
		"prompt_template": "nope"
	}`
	if err := os.WriteFile(filepath.Join(dir, "help.json"), []byte(collisionPlugin), 0o644); err != nil {
		t.Fatalf("write collision plugin: %v", err)
	}

	model := New(DefaultTUIConfig()).WithPluginsDir(dir)

	if model.pluginsDir != dir {
		t.Fatalf("expected pluginsDir %q, got %q", dir, model.pluginsDir)
	}

	warnings := model.PluginWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "already registered") {
		t.Fatalf("expected collision warning, got %q", warnings[0])
	}

	entry, ok := model.commandRegistry.Lookup("summarize")
	if !ok {
		t.Fatal("expected summarize command to be registered")
	}
	result := entry.Handler(Command{Name: "summarize", Args: []string{"release", "notes"}})
	if result.Status != CmdOK {
		t.Fatalf("expected CmdOK, got %v with output %q", result.Status, result.Output)
	}
	if result.Output != "Summarize: release notes" {
		t.Fatalf("unexpected plugin output: %q", result.Output)
	}
}

// Legacy JSON plugins under ~/.config/harnesscli/plugins must keep registering
// (deprecation is a warning, not a removal), and a non-empty legacy dir must
// surface a startup warning pointing at the installable bundle format.
func TestLegacyPluginsDirStillRegistersAndWarnsAtStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir := filepath.Join(home, ".config", "harnesscli", "plugins")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("create legacy plugins dir: %v", err)
	}
	legacyPlugin := `{
		"name": "summarize",
		"description": "Summarize the current topic",
		"handler": "prompt",
		"prompt_template": "Summarize: {args}"
	}`
	if err := os.WriteFile(filepath.Join(legacyDir, "summarize.json"), []byte(legacyPlugin), 0o644); err != nil {
		t.Fatalf("write legacy plugin: %v", err)
	}

	model := New(DefaultTUIConfig())

	// The legacy plugin still registers as a working slash command.
	entry, ok := model.commandRegistry.Lookup("summarize")
	if !ok {
		t.Fatal("expected legacy JSON plugin command to remain registered")
	}
	result := entry.Handler(Command{Name: "summarize", Args: []string{"release", "notes"}})
	if result.Status != CmdOK {
		t.Fatalf("expected legacy plugin command to run, got %v with output %q", result.Status, result.Output)
	}
	if result.Output != "Summarize: release notes" {
		t.Fatalf("unexpected legacy plugin output: %q", result.Output)
	}

	// ...and the non-empty legacy dir surfaces a deprecation warning that
	// points at the installable bundle format.
	warnings := model.PluginWarnings()
	var found bool
	for _, w := range warnings {
		if strings.Contains(w, "deprecated") && strings.Contains(w, ".go-harness/plugins") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a legacy-dir deprecation warning pointing at the bundle home, got %v", warnings)
	}
}

// An absent legacy dir must not produce the deprecation warning.
func TestNoLegacyPluginsDirNoDeprecationWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	model := New(DefaultTUIConfig())
	for _, w := range model.PluginWarnings() {
		if strings.Contains(w, "deprecated") {
			t.Fatalf("expected no deprecation warning without a legacy dir, got %q", w)
		}
	}
}
