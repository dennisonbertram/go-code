package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	"go-agent-harness/internal/skills"
)

// writeTUISkill writes a SKILL.md under dir/name and returns the file path.
func writeTUISkill(t *testing.T, dir, name, frontmatterExtra, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: \"%s skill\"\nversion: 1\n%s---\n%s\n", name, name, frontmatterExtra, body)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestSkillResolver loads every skill under dir into a registry and returns
// it with a resolver.
func newTestSkillResolver(t *testing.T, dir string) (*skills.Registry, *skills.Resolver) {
	t.Helper()
	reg := skills.NewRegistry()
	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}
	return reg, skills.NewResolver(reg)
}

// TestSkillInvocationTarget_Precedence pins the claim order: builtin > plugin >
// shorthand skill, with skill:<name> always resolving the skill.
func TestSkillInvocationTarget_Precedence(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "stats", "", "skill body for stats")
	writeTUISkill(t, dir, "pluggy", "", "skill body for pluggy")
	writeTUISkill(t, dir, "greet", "", "skill body for greet")
	skillReg, _ := newTestSkillResolver(t, dir)

	reg := NewCommandRegistry()                // builtins (includes "stats")
	reg.Register(CommandEntry{Name: "pluggy"}) // plugin-claimed name

	tests := []struct {
		name      string
		input     string
		wantSkill string
		wantOK    bool
	}{
		{"builtin blocks shorthand", "stats", "", false},
		{"namespace beats builtin", "skill:stats", "stats", true},
		{"plugin blocks shorthand", "pluggy", "", false},
		{"namespace beats plugin", "skill:pluggy", "pluggy", true},
		{"unclaimed shorthand resolves", "greet", "greet", true},
		{"namespace on unclaimed resolves", "skill:greet", "greet", true},
		{"unknown name", "nosuch", "", false},
		{"unknown namespaced skill", "skill:nosuch", "", false},
		{"empty namespace", "skill:", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skillName, ok := skillInvocationTarget(reg, skillReg, tt.input)
			if ok != tt.wantOK {
				t.Fatalf("skillInvocationTarget(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if skillName != tt.wantSkill {
				t.Errorf("skillInvocationTarget(%q) = %q, want %q", tt.input, skillName, tt.wantSkill)
			}
		})
	}

	t.Run("nil skill registry never resolves", func(t *testing.T) {
		if skillName, ok := skillInvocationTarget(reg, nil, "greet"); ok || skillName != "" {
			t.Errorf("expected no resolution with nil registry, got %q, %v", skillName, ok)
		}
	})
}

// TestBuildSlashComplete_Skills verifies skill: entries always appear and the
// bare shorthand appears only when the name is unclaimed.
func TestBuildSlashComplete_Skills(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "greet", "", "body")
	writeTUISkill(t, dir, "stats", "", "body") // collides with builtin
	skillReg, _ := newTestSkillResolver(t, dir)

	reg := NewCommandRegistry()
	sc := buildSlashComplete(reg, skillReg).Open().SetQuery("")

	names := map[string]int{}
	descriptions := map[string]string{}
	for _, s := range sc.Filtered() {
		names[s.Name]++
		descriptions[s.Name] = s.Description
	}

	if names["skill:greet"] != 1 {
		t.Errorf("expected one skill:greet entry, got %d", names["skill:greet"])
	}
	if names["greet"] != 1 {
		t.Errorf("expected one bare greet shorthand entry, got %d", names["greet"])
	}
	if names["skill:stats"] != 1 {
		t.Errorf("expected one skill:stats entry, got %d", names["skill:stats"])
	}
	// "stats" is a builtin: the only bare entry is the builtin's own, never a
	// skill shorthand duplicate.
	if names["stats"] != 1 {
		t.Errorf("expected exactly one bare stats entry (the builtin), got %d", names["stats"])
	}
	if descriptions["skill:greet"] != "greet skill" {
		t.Errorf("skill:greet description = %q, want skill description", descriptions["skill:greet"])
	}

	// Typing /skill: lists skills.
	scSkills := sc.SetQuery("skill:")
	if len(scSkills.Filtered()) != 2 {
		t.Errorf("query skill: should list 2 skill entries, got %d", len(scSkills.Filtered()))
	}

	// Nil skill registry degrades to command-only suggestions.
	scNil := buildSlashComplete(reg, nil).Open().SetQuery("")
	for _, s := range scNil.Filtered() {
		if strings.HasPrefix(s.Name, "skill:") {
			t.Errorf("unexpected skill entry with nil registry: %q", s.Name)
		}
	}
}

// TestExpandSkillInvocation pins the expansion contract for TUI dispatch:
// raw remainder tokenized quote-aware, named bindings, fallback append, and
// workspace passthrough — the same contract as the harness skill tool.
func TestExpandSkillInvocation(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "deploy", "", "Deploy $0 to $1.")
	writeTUISkill(t, dir, "greet", "arguments: [target, env]\n", "Deploy $target to $env.")
	writeTUISkill(t, dir, "plain", "", "You are a helper.")
	writeTUISkill(t, dir, "where", "", "WS: $WORKSPACE")
	skillReg, resolver := newTestSkillResolver(t, dir)
	reg := NewCommandRegistry()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"shorthand quoted args", `/deploy "staging eu" fast`, "Deploy staging eu to fast."},
		{"namespaced quoted args", `/skill:deploy "staging eu" fast`, "Deploy staging eu to fast."},
		{"named args", "/greet prod eu", "Deploy prod to eu."},
		{"fallback append", "/plain focus now", "You are a helper.\nARGUMENTS: focus now"},
		{"workspace passthrough", "/where", "WS: /tmp/ws"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := expandSkillInvocation(reg, skillReg, resolver, "/tmp/ws", tt.input)
			if !ok {
				t.Fatalf("expandSkillInvocation(%q) not resolved", tt.input)
			}
			if got != tt.want {
				t.Errorf("expandSkillInvocation(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestExpandSkillInvocation_NotSkill covers the negative paths.
func TestExpandSkillInvocation_NotSkill(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "greet", "", "Hello $0!")
	skillReg, resolver := newTestSkillResolver(t, dir)
	reg := NewCommandRegistry()

	tests := []struct {
		name  string
		input string
	}{
		{"builtin not intercepted", "/stats"},
		{"unknown command", "/nosuchcmd"},
		{"unknown namespaced skill", "/skill:nosuch"},
		{"plain message", "hello world"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := expandSkillInvocation(reg, skillReg, resolver, "/tmp/ws", tt.input); ok {
				t.Errorf("expandSkillInvocation(%q) = %q, expected no resolution", tt.input, got)
			}
		})
	}

	if got, ok := expandSkillInvocation(reg, skillReg, nil, "/tmp/ws", "/greet world"); ok {
		t.Errorf("expected no resolution with nil resolver, got %q", got)
	}
	if got, ok := expandSkillInvocation(reg, nil, nil, "/tmp/ws", "/greet world"); ok {
		t.Errorf("expected no resolution with nil registry, got %q", got)
	}
}

// TestCommandSubmittedMsg_SkillShorthandExpands wires the shorthand through the
// Model submit path: the expanded skill body becomes the user message.
func TestCommandSubmittedMsg_SkillShorthandExpands(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "greet", "", "Hello $0, welcome.")
	skillReg, resolver := newTestSkillResolver(t, dir)

	m := New(DefaultTUIConfig())
	m = m.WithPluginsDir(t.TempDir()) // isolate from the developer's plugin dirs
	m.skillRegistry = skillReg
	m.skillResolver = resolver
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(Model)

	updated, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "/greet world"})
	m = updated.(Model)

	if view := m.View(); !strings.Contains(view, "Hello world, welcome.") {
		t.Errorf("expected expanded skill body in view, got:\n%s", view)
	}
}

// TestCommandSubmittedMsg_BuiltinBeatsSkillShorthand proves a builtin name
// never resolves to a same-named skill.
func TestCommandSubmittedMsg_BuiltinBeatsSkillShorthand(t *testing.T) {
	dir := t.TempDir()
	writeTUISkill(t, dir, "stats", "", "SKILL-BODY-MARKER")
	skillReg, resolver := newTestSkillResolver(t, dir)

	m := New(DefaultTUIConfig())
	m = m.WithPluginsDir(t.TempDir())
	m.skillRegistry = skillReg
	m.skillResolver = resolver
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(Model)

	updated, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "/stats"})
	m = updated.(Model)

	if view := m.View(); strings.Contains(view, "SKILL-BODY-MARKER") {
		t.Errorf("builtin /stats must not resolve to the stats skill, got:\n%s", view)
	}
}

// TestLoadTUISkills_LoadsFromGlobalDir covers the TUI-side loading contract:
// skills under <global>/skills become resolvable.
func TestLoadTUISkills_LoadsFromGlobalDir(t *testing.T) {
	global := t.TempDir()
	workspace := t.TempDir()
	writeTUISkill(t, filepath.Join(global, "skills"), "greet", "", "Hello $0!")
	writeTUISkill(t, filepath.Join(workspace, ".go-harness", "skills"), "local-thing", "", "Local body")
	t.Setenv("HARNESS_GLOBAL_DIR", global)

	reg, resolver := loadTUISkills(workspace)
	if reg == nil || resolver == nil {
		t.Fatal("expected non-nil registry and resolver")
	}
	if _, ok := reg.Get("greet"); !ok {
		t.Error("expected global skill greet to load")
	}
	if _, ok := reg.Get("local-thing"); !ok {
		t.Error("expected workspace skill local-thing to load")
	}
}

// TestLoadTUISkills_MissingDirsDegrade covers the no-skills-on-disk case.
func TestLoadTUISkills_MissingDirsDegrade(t *testing.T) {
	t.Setenv("HARNESS_GLOBAL_DIR", t.TempDir())
	reg, resolver := loadTUISkills(t.TempDir())
	if reg == nil || resolver == nil {
		t.Fatal("expected non-nil registry and resolver even with no skills on disk")
	}
	if len(reg.List()) != 0 {
		t.Errorf("expected empty registry, got %d skills", len(reg.List()))
	}
}
