package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/internal/systemprompt"
)

// initModelForInit creates a Model whose workspace is a temp directory, so
// /init's AGENTS.md write path stays inside test-controlled storage.
func initModelForInit(t *testing.T) (tui.Model, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	cfg := tui.DefaultTUIConfig()
	cfg.Workspace = ws
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(tui.Model), ws
}

// runInitToCompletion simulates the harness accepting the /init run and the
// assistant replying with the given markdown before the run completes.
func runInitToCompletion(t *testing.T, m tui.Model, assistantText string) tui.Model {
	t.Helper()
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-init"})
	m = m2.(tui.Model)
	if assistantText != "" {
		m2, _ = m.Update(tui.AssistantDeltaMsg{Delta: assistantText})
		m = m2.(tui.Model)
	}
	m2, _ = m.Update(tui.RunCompletedMsg{RunID: "run-init"})
	return m2.(tui.Model)
}

func readAgentsFile(t *testing.T, ws string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("ReadFile AGENTS.md: %v", err)
	}
	return string(data), true
}

// TestInitCommand_GeneratesAndWritesAgentsMd verifies the happy path: /init
// starts a run with the generation prompt, and when the run completes the
// assistant's markdown is written to <workspace>/AGENTS.md.
func TestInitCommand_GeneratesAndWritesAgentsMd(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init")
	if got := m.StatusMsg(); !strings.Contains(got, "Generating AGENTS.md") {
		t.Errorf("StatusMsg() = %q, want a 'Generating AGENTS.md' notice", got)
	}

	// The generation prompt must be recorded as the user turn (it is what the
	// run sends to the harness).
	tr := m.Transcript()
	if len(tr) == 0 {
		t.Fatal("transcript empty after /init; the generation prompt was not submitted")
	}
	last := tr[len(tr)-1]
	if last.Role != "user" {
		t.Errorf("last transcript role = %q, want user", last.Role)
	}
	for _, want := range []string{"AGENTS.md", "build", "test"} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("generation prompt must mention %q; got:\n%s", want, last.Content)
		}
	}

	markdown := "# My Project\n\n## Build\n\n`go build ./...`\n"
	m = runInitToCompletion(t, m, markdown)

	content, ok := readAgentsFile(t, ws)
	if !ok {
		t.Fatal("AGENTS.md was not written after the /init run completed")
	}
	if content != markdown {
		t.Errorf("AGENTS.md content = %q, want %q", content, markdown)
	}
	if got := m.StatusMsg(); !strings.Contains(got, "AGENTS.md") {
		t.Errorf("StatusMsg() = %q, want confirmation of the written path", got)
	}
}

// TestInitCommand_UnwrapsFencedMarkdown verifies that when the assistant wraps
// the document in a ```markdown fence, the file gets the inner content only.
func TestInitCommand_UnwrapsFencedMarkdown(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init")
	m = runInitToCompletion(t, m, "```markdown\n# Fenced Project\n\nbody\n```\n")

	content, ok := readAgentsFile(t, ws)
	if !ok {
		t.Fatal("AGENTS.md was not written")
	}
	if strings.Contains(content, "```") {
		t.Errorf("AGENTS.md must not contain the wrapping fence, got:\n%s", content)
	}
	if !strings.Contains(content, "# Fenced Project") {
		t.Errorf("AGENTS.md missing inner content, got:\n%s", content)
	}
}

// TestInitCommand_RefusesOverwriteWithoutConfirm verifies that /init with an
// existing AGENTS.md does not start a run and leaves the file untouched.
func TestInitCommand_RefusesOverwriteWithoutConfirm(t *testing.T) {
	m, ws := initModelForInit(t)
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}

	m = sendSlashCommand(m, "/init")

	if m.RunActive() {
		t.Error("/init must not start a run when AGENTS.md exists without confirm")
	}
	if got := m.StatusMsg(); !strings.Contains(got, "already exists") || !strings.Contains(got, "/init confirm") {
		t.Errorf("StatusMsg() = %q, want an 'already exists' + '/init confirm' hint", got)
	}
	if content, _ := readAgentsFile(t, ws); content != "ORIGINAL" {
		t.Errorf("AGENTS.md modified without confirm: %q", content)
	}
}

// TestInitCommand_ConfirmOverwrites verifies that /init confirm runs the
// generation and replaces the existing file on completion.
func TestInitCommand_ConfirmOverwrites(t *testing.T) {
	m, ws := initModelForInit(t)
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}

	m = sendSlashCommand(m, "/init confirm")
	m = runInitToCompletion(t, m, "# Regenerated\n")

	content, ok := readAgentsFile(t, ws)
	if !ok {
		t.Fatal("AGENTS.md missing after /init confirm")
	}
	if content != "# Regenerated\n" {
		t.Errorf("AGENTS.md = %q, want regenerated content", content)
	}
}

// TestInitCommand_RefusedWhileRunActive verifies /init does not hijack an
// in-flight run: it refuses, and the other run's completion writes nothing.
func TestInitCommand_RefusedWhileRunActive(t *testing.T) {
	m, ws := initModelForInit(t)
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-other"})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/init")
	if got := m.StatusMsg(); !strings.Contains(got, "run") {
		t.Errorf("StatusMsg() = %q, want a 'run already active' explanation", got)
	}

	// The other run finishes producing markdown — no AGENTS.md may be written.
	m2, _ = m.Update(tui.AssistantDeltaMsg{Delta: "# Some Doc\n"})
	m = m2.(tui.Model)
	m2, _ = m.Update(tui.RunCompletedMsg{RunID: "run-other"})
	m = m2.(tui.Model)

	if _, ok := readAgentsFile(t, ws); ok {
		t.Error("AGENTS.md must not be written when /init was refused")
	}
}

// TestInitCommand_EmptyOutputDoesNotWrite verifies that a run producing no
// usable markdown leaves the workspace without AGENTS.md.
func TestInitCommand_EmptyOutputDoesNotWrite(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init")
	m = runInitToCompletion(t, m, "")

	if _, ok := readAgentsFile(t, ws); ok {
		t.Error("AGENTS.md must not be written when the run produced no markdown")
	}
	if got := m.StatusMsg(); !strings.Contains(got, "AGENTS.md") {
		t.Errorf("StatusMsg() = %q, want an explanation that nothing was written", got)
	}
}

// TestInitCommand_RunFailureWritesNothing verifies that a failed run clears
// the pending write: no file appears, and a later normal run completing with
// markdown does not trigger a stale write.
func TestInitCommand_RunFailureWritesNothing(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init")
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-init"})
	m = m2.(tui.Model)
	m2, _ = m.Update(tui.RunFailedMsg{RunID: "run-init", Error: "boom"})
	m = m2.(tui.Model)

	if _, ok := readAgentsFile(t, ws); ok {
		t.Error("AGENTS.md must not be written after a failed run")
	}

	// A subsequent ordinary run completing with markdown must not write either.
	m2, _ = m.Update(tui.RunStartedMsg{RunID: "run-next"})
	m = m2.(tui.Model)
	m2, _ = m.Update(tui.AssistantDeltaMsg{Delta: "# Not Agents\n"})
	m = m2.(tui.Model)
	m2, _ = m.Update(tui.RunCompletedMsg{RunID: "run-next"})
	m = m2.(tui.Model)

	if _, ok := readAgentsFile(t, ws); ok {
		t.Error("stale /init state leaked into a later run and wrote AGENTS.md")
	}
}

// TestInitCommand_UnknownArgShowsUsage verifies /init rejects unknown args.
func TestInitCommand_UnknownArgShowsUsage(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init bogus")

	if got := m.StatusMsg(); !strings.Contains(got, "Usage: /init") {
		t.Errorf("StatusMsg() = %q, want a usage hint", got)
	}
	if _, ok := readAgentsFile(t, ws); ok {
		t.Error("no file may be written for an invalid invocation")
	}
}

// TestInitCommand_WrittenFilePickedUpBySystemPrompt verifies the file /init
// writes is exactly what internal/systemprompt injects into the next run's
// static prompt (the AGENTS_MD section via readAgentsMd).
func TestInitCommand_WrittenFilePickedUpBySystemPrompt(t *testing.T) {
	m, ws := initModelForInit(t)

	m = sendSlashCommand(m, "/init")
	markdown := "# Picked Up Project\n\n## Test\n\n`go test ./...`\n"
	m = runInitToCompletion(t, m, markdown)

	engine, err := systemprompt.NewFileEngine(makeInitPromptFixture(t))
	if err != nil {
		t.Fatalf("NewFileEngine: %v", err)
	}
	out, err := engine.Resolve(systemprompt.ResolveRequest{Model: "gpt-4o", WorkspacePath: ws})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out.StaticPrompt, "[SECTION AGENTS_MD]") {
		t.Errorf("resolved prompt missing AGENTS_MD section:\n%s", out.StaticPrompt)
	}
	if !strings.Contains(out.StaticPrompt, "# Picked Up Project") {
		t.Errorf("resolved prompt missing written AGENTS.md content:\n%s", out.StaticPrompt)
	}
}

// TestInitCommand_Registered verifies the init command is registered.
func TestInitCommand_Registered(t *testing.T) {
	r := tui.NewCommandRegistry()
	if !r.IsRegistered("init") {
		t.Fatal("built-in registry must register the init command")
	}
	entry, ok := r.Lookup("init")
	if !ok || entry.Description == "" {
		t.Fatal("init command must have a description for /help and autocomplete")
	}
}

// TestInitCommand_InSlashComplete verifies /init appears in autocomplete.
func TestInitCommand_InSlashComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "/ini")
	if v := m.View(); !strings.Contains(v, "init") {
		t.Errorf("slash-complete must contain 'init' when typing '/ini'; got:\n%s", v)
	}
}

// makeInitPromptFixture writes a minimal prompt catalog usable by
// systemprompt.NewFileEngine (mirrors internal/harness's runner fixture).
func makeInitPromptFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	write("catalog.yaml", `version: 1
defaults:
  intent: general
  model_profile: default
intents:
  general: intents/general.md
model_profiles:
  - name: default
    match: "*"
    file: models/default.md
extensions:
  behaviors_dir: extensions/behaviors
  talents_dir: extensions/talents
`)
	write("base/main.md", "BASE")
	write("intents/general.md", "INTENT")
	write("models/default.md", "MODEL_DEFAULT")
	write("extensions/behaviors/.keep", "")
	write("extensions/talents/.keep", "")
	return root
}
