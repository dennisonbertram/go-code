package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	tuiplugin "go-agent-harness/cmd/harnesscli/tui/plugin"
	"go-agent-harness/internal/plugins"
)

// Slice 4 (epic #821): bundle markdown command files as namespaced slash
// commands with skills argument expansion.

func writeMarkdownCommand(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExpandMarkdownCommand_MatchesSkillsSemantics(t *testing.T) {
	def := tuiplugin.MarkdownCommand{
		Name:        "greet",
		Description: "Greet someone",
		Body:        "Hello $0! All: $ARGUMENTS. Literal: $HOME. Dir: $SKILL_DIR",
		FilePath:    "/bundles/greeter/commands/greet.md",
	}
	// Quotes are preserved in the raw args string; $0 uses SplitArgs
	// tokenization, $ARGUMENTS is verbatim, unknown $HOME stays literal.
	cmd := Command{Name: "greet", Args: []string{`"hello`, `world"`}, Raw: `/greet "hello world"`}
	got := expandMarkdownCommand(def, "/ws", cmd)
	want := `Hello hello world! All: "hello world". Literal: $HOME. Dir: /bundles/greeter/commands`
	if got != want {
		t.Fatalf("expandMarkdownCommand() =\n%q\nwant\n%q", got, want)
	}
}

func TestExpandMarkdownCommand_ArgumentsFallback(t *testing.T) {
	def := tuiplugin.MarkdownCommand{
		Name:     "summarize",
		Body:     "Summarize the notes.",
		FilePath: "/b/commands/summarize.md",
	}
	cmd := Command{Name: "summarize", Args: []string{"these", "notes"}, Raw: "/summarize these notes"}
	got := expandMarkdownCommand(def, "/ws", cmd)
	want := "Summarize the notes.\nARGUMENTS: these notes"
	if got != want {
		t.Fatalf("expandMarkdownCommand() = %q, want %q", got, want)
	}
}

func TestLoadAndRegisterBundleCommands_RegistersMarkdownCommands(t *testing.T) {
	dir := t.TempDir()
	writeMarkdownCommand(t, dir, "greet.md", "---\nname: greet\ndescription: Greet someone\n---\nSay hello to $ARGUMENTS.\n")

	registry := NewCommandRegistry()
	warnings := LoadAndRegisterBundleCommands(registry, BundleCommandSource{BundleName: "greeter", Dir: dir})
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	entry, ok := registry.Lookup("greet")
	if !ok {
		t.Fatal("greet command not registered")
	}
	if entry.Description != "Greet someone" {
		t.Fatalf("Description = %q", entry.Description)
	}
	// Markdown commands submit through the built-in Execute path, not the
	// async plugin-handler path.
	if entry.Execute == nil {
		t.Fatal("markdown command must set Execute (submits the expanded prompt)")
	}
}

func TestLoadAndRegisterBundleCommands_CollisionNamespaces(t *testing.T) {
	dirA := t.TempDir()
	writeMarkdownCommand(t, dirA, "greet.md", "---\nname: greet\ndescription: From bundle alpha\n---\nalpha\n")
	dirB := t.TempDir()
	writeMarkdownCommand(t, dirB, "greet.md", "---\nname: greet\ndescription: From bundle beta\n---\nbeta\n")
	dirHelp := t.TempDir()
	writeMarkdownCommand(t, dirHelp, "help.md", "---\nname: help\ndescription: Bundle help\n---\nbundle help\n")

	registry := NewCommandRegistry() // includes the built-in help command
	warnings := LoadAndRegisterBundleCommands(registry,
		BundleCommandSource{BundleName: "alpha", Dir: dirA},
		BundleCommandSource{BundleName: "beta", Dir: dirB},
		BundleCommandSource{BundleName: "helpertools", Dir: dirHelp},
	)

	// First bundle keeps the plain name; the second is namespaced.
	if _, ok := registry.Lookup("greet"); !ok {
		t.Fatal("plain greet not registered")
	}
	if _, ok := registry.Lookup("beta:greet"); !ok {
		t.Fatal("beta:greet not registered")
	}
	// A collision with a built-in namespaces too, leaving the built-in intact.
	if _, ok := registry.Lookup("helpertools:help"); !ok {
		t.Fatal("helpertools:help not registered")
	}
	if entry, _ := registry.Lookup("help"); entry.Description == "Bundle help" {
		t.Fatal("built-in help was replaced by the bundle command")
	}
	// Both namespacings are logged via the warnings slice.
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "beta:greet") || !strings.Contains(joined, "helpertools:help") {
		t.Fatalf("expected namespacing warnings, got %v", warnings)
	}
}

func TestLoadAndRegisterBundleCommands_DoubleCollisionSkips(t *testing.T) {
	dir := t.TempDir()
	writeMarkdownCommand(t, dir, "dup.md", "---\nname: dup\ndescription: Duplicate\n---\nbody\n")

	registry := NewCommandRegistry()
	registry.Register(CommandEntry{Name: "dup", Description: "pre-existing"})
	registry.Register(CommandEntry{Name: "beta:dup", Description: "pre-existing namespaced"})

	warnings := LoadAndRegisterBundleCommands(registry, BundleCommandSource{BundleName: "beta", Dir: dir})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped") {
		t.Fatalf("expected one skip warning, got %v", warnings)
	}
	if entry, _ := registry.Lookup("dup"); entry.Description != "pre-existing" {
		t.Fatal("pre-existing dup command was replaced")
	}
}

func TestInstallablePluginCommandSources_UntrustedBundlesExcluded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".go-harness", "plugins")
	bundleDir := filepath.Join(root, "greeter", "1.0.0")
	writeMarkdownCommand(t, filepath.Join(bundleDir, "commands"), "greet.md", "---\nname: greet\ndescription: x\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(bundleDir, "plugin.json"), []byte(`{"schema_version":1,"name":"greeter","version":"1.0.0","commands":"commands"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := plugins.NewStateStore(filepath.Join(root, "state.json"))
	// Remote bundle: installed untrusted — its commands dir must not appear.
	if err := store.RecordInstall(plugins.InstalledPlugin{Name: "greeter", Version: "1.0.0", Source: "https://example.com/x.git", Remote: true}); err != nil {
		t.Fatal(err)
	}
	if sources := installablePluginCommandSources(); len(sources) != 0 {
		t.Fatalf("untrusted bundle contributed sources: %v", sources)
	}

	// Trusting the bundle exposes its commands dir under the bundle name.
	if err := store.SetTrusted("greeter", true); err != nil {
		t.Fatal(err)
	}
	sources := installablePluginCommandSources()
	if len(sources) != 1 || sources[0].BundleName != "greeter" || sources[0].Dir != filepath.Join(bundleDir, "commands") {
		t.Fatalf("sources = %+v", sources)
	}

	// Disabled bundles contribute nothing even when trusted.
	if err := store.SetEnabled("greeter", false); err != nil {
		t.Fatal(err)
	}
	if sources := installablePluginCommandSources(); len(sources) != 0 {
		t.Fatalf("disabled bundle contributed sources: %v", sources)
	}
}

// Acceptance: install + trust a bundle with commands/greet.md, submit
// "/greet hello world" in the TUI, and the expanded prompt is POSTed as a run.
func TestMarkdownCommandSubmitsExpandedPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".go-harness", "plugins")
	bundleDir := filepath.Join(root, "greeter", "1.0.0")
	writeMarkdownCommand(t, filepath.Join(bundleDir, "commands"), "greet.md",
		"---\nname: greet\ndescription: Greet someone\n---\nSay hi to $0. Everyone: $ARGUMENTS\n")
	if err := os.WriteFile(filepath.Join(bundleDir, "plugin.json"), []byte(`{"schema_version":1,"name":"greeter","version":"1.0.0","commands":"commands"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := plugins.NewStateStore(filepath.Join(root, "state.json"))
	// Local source: trusted by default.
	if err := store.RecordInstall(plugins.InstalledPlugin{Name: "greeter", Version: "1.0.0", Source: bundleDir, Remote: false}); err != nil {
		t.Fatal(err)
	}

	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/runs" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				gotPrompt, _ = body["prompt"].(string)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"run_1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(Model)

	// The command is registered (which also feeds /help and slash completion,
	// both built from the same registry).
	if _, ok := m.commandRegistry.Lookup("greet"); !ok {
		t.Fatal("greet not registered from the trusted bundle")
	}

	updated, cmd := m.Update(inputarea.CommandSubmittedMsg{Value: "/greet hello world"})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a run command for the markdown slash command")
	}
	if msg := cmd(); msg != nil {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				c()
			}
		}
	}

	want := "Say hi to hello. Everyone: hello world"
	if gotPrompt != want {
		t.Fatalf("submitted prompt = %q, want %q", gotPrompt, want)
	}
}
