package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoader_LoadsEnabledPluginSkillDirectory(t *testing.T) {
	pluginDir := t.TempDir()
	skillDir := filepath.Join(pluginDir, "plugin-skill")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: plugin-skill\ndescription: Plugin skill\nversion: 1\n---\nBody"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewLoader(LoaderConfig{PluginDirs: []string{pluginDir}}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Source != SourcePlugin {
		t.Fatalf("loaded = %#v", loaded)
	}
}

const validSkillMD = `---
name: my-skill
description: "A test skill. Trigger: do my thing"
version: 1
---
# My Skill Body

Use $ARGUMENTS here.
`

func TestLoaderLoad_ValidSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "my-skill", validSkillMD)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("Load() returned %d skills, want 1", len(skills))
	}

	s := skills[0]
	if s.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", s.Name, "my-skill")
	}
	if s.Description != "A test skill. Trigger: do my thing" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Version != 1 {
		t.Errorf("Version = %d, want 1", s.Version)
	}
	if !s.AutoInvoke {
		t.Error("AutoInvoke should default to true")
	}
	if s.Source != SourceGlobal {
		t.Errorf("Source = %q, want %q", s.Source, SourceGlobal)
	}
	if len(s.Triggers) != 1 || s.Triggers[0] != "do my thing" {
		t.Errorf("Triggers = %v, want [do my thing]", s.Triggers)
	}
	if s.Body == "" {
		t.Error("Body should not be empty")
	}
}

func TestLoaderLoad_AutoInvokeFalse(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: no-auto
description: "No auto invoke skill"
version: 1
auto-invoke: false
---
Body here.
`
	writeSkillFile(t, dir, "no-auto", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills", len(skills))
	}
	if skills[0].AutoInvoke != false {
		t.Error("AutoInvoke should be false")
	}
}

func TestLoaderLoad_AllowedTools(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: limited
description: "Limited tools skill"
version: 1
allowed-tools:
  - bash
  - read
---
Body.
`
	writeSkillFile(t, dir, "limited", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills[0].AllowedTools) != 2 {
		t.Errorf("AllowedTools = %v, want [bash read]", skills[0].AllowedTools)
	}
}

func TestLoaderLoad_MissingName(t *testing.T) {
	dir := t.TempDir()
	content := `---
description: "No name"
version: 1
---
Body.
`
	writeSkillFile(t, dir, "no-name", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoaderLoad_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: no-desc
version: 1
---
Body.
`
	writeSkillFile(t, dir, "no-desc", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestLoaderLoad_InvalidVersion(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: bad-ver
description: "Bad version"
version: 2
---
Body.
`
	writeSkillFile(t, dir, "bad-ver", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestLoaderLoad_NotKebabCase(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: NotKebab
description: "Bad name"
version: 1
---
Body.
`
	writeSkillFile(t, dir, "NotKebab", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for non-kebab-case name")
	}
}

func TestLoaderLoad_NameDirMismatch(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: wrong-name
description: "Name doesn't match dir"
version: 1
---
Body.
`
	writeSkillFile(t, dir, "actual-dir", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for name/dir mismatch")
	}
}

func TestLoaderLoad_MissingDirectory(t *testing.T) {
	loader := NewLoader(LoaderConfig{GlobalDir: "/nonexistent/path/skills"})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v (should skip missing dirs)", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestLoaderLoad_EmptyDirs(t *testing.T) {
	loader := NewLoader(LoaderConfig{})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestLoaderLoad_DirWithoutSkillMD(t *testing.T) {
	dir := t.TempDir()
	// Create a directory but no SKILL.md inside
	if err := os.MkdirAll(filepath.Join(dir, "some-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestLoaderLoad_BothDirs(t *testing.T) {
	globalDir := t.TempDir()
	localDir := t.TempDir()

	writeSkillFile(t, globalDir, "my-skill", validSkillMD)

	localContent := `---
name: local-skill
description: "A local skill"
version: 1
---
Local body.
`
	writeSkillFile(t, localDir, "local-skill", localContent)

	loader := NewLoader(LoaderConfig{GlobalDir: globalDir, WorkspaceDir: localDir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Check sources
	sourceMap := map[string]SkillSource{}
	for _, s := range skills {
		sourceMap[s.Name] = s.Source
	}
	if sourceMap["my-skill"] != SourceGlobal {
		t.Errorf("my-skill source = %q, want global", sourceMap["my-skill"])
	}
	if sourceMap["local-skill"] != SourceLocal {
		t.Errorf("local-skill source = %q, want local", sourceMap["local-skill"])
	}
}

func TestLoaderLoad_MissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := `# No frontmatter here
Just markdown.
`
	writeSkillFile(t, dir, "bad-skill", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestLoaderLoad_ArgumentHint(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: with-hint
description: "Has argument hint"
version: 1
argument-hint: "<filename> [options]"
---
Body.
`
	writeSkillFile(t, dir, "with-hint", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if skills[0].ArgumentHint != "<filename> [options]" {
		t.Errorf("ArgumentHint = %q", skills[0].ArgumentHint)
	}
}

func TestLoaderLoad_ContextFork(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: deep-research
description: "Research a topic thoroughly"
version: 1
context: fork
agent: Explore
---
Research $ARGUMENTS thoroughly.
`
	writeSkillFile(t, dir, "deep-research", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Context != ContextFork {
		t.Errorf("Context = %q, want %q", skills[0].Context, ContextFork)
	}
	if skills[0].Agent != "Explore" {
		t.Errorf("Agent = %q, want %q", skills[0].Agent, "Explore")
	}
}

func TestLoaderLoad_ContextConversationExplicit(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: explicit-conv
description: "Explicit conversation context"
version: 1
context: conversation
---
Body here.
`
	writeSkillFile(t, dir, "explicit-conv", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if skills[0].Context != ContextConversation {
		t.Errorf("Context = %q, want %q", skills[0].Context, ContextConversation)
	}
}

func TestLoaderLoad_ContextDefault(t *testing.T) {
	dir := t.TempDir()
	// No context field -- should default to "conversation"
	writeSkillFile(t, dir, "my-skill", validSkillMD)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if skills[0].Context != ContextConversation {
		t.Errorf("Context = %q, want default %q", skills[0].Context, ContextConversation)
	}
}

func TestLoaderLoad_ContextInvalid(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: bad-context
description: "Invalid context value"
version: 1
context: invalid
---
Body.
`
	writeSkillFile(t, dir, "bad-context", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid context value")
	}
}

func TestLoaderLoad_AgentWithoutFork(t *testing.T) {
	dir := t.TempDir()
	// agent field set but context is not fork -- should succeed (agent is just metadata)
	content := `---
name: with-agent
description: "Has agent but no fork context"
version: 1
agent: Explore
---
Body.
`
	writeSkillFile(t, dir, "with-agent", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v (agent without fork should not be an error)", err)
	}
	if skills[0].Context != ContextConversation {
		t.Errorf("Context = %q, want %q", skills[0].Context, ContextConversation)
	}
	if skills[0].Agent != "Explore" {
		t.Errorf("Agent = %q, want %q", skills[0].Agent, "Explore")
	}
}

func TestLoaderLoad_ForkWithAllowedTools(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: safe-research
description: "Research with restricted tools"
version: 1
context: fork
agent: Explore
allowed-tools:
  - read
  - grep
---
Research carefully.
`
	writeSkillFile(t, dir, "safe-research", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if skills[0].Context != ContextFork {
		t.Errorf("Context = %q, want %q", skills[0].Context, ContextFork)
	}
	if len(skills[0].AllowedTools) != 2 {
		t.Errorf("AllowedTools = %v, want [read grep]", skills[0].AllowedTools)
	}
}

func TestLoaderLoad_VerifiedFields(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: verified-skill
description: "A verified skill"
version: 1
verified: true
verified_at: "2026-03-09T12:00:00Z"
verified_by: "dennisonbertram"
---
Body.
`
	writeSkillFile(t, dir, "verified-skill", content)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if !s.Verified {
		t.Error("Verified should be true")
	}
	if s.VerifiedAt != "2026-03-09T12:00:00Z" {
		t.Errorf("VerifiedAt = %q, want %q", s.VerifiedAt, "2026-03-09T12:00:00Z")
	}
	if s.VerifiedBy != "dennisonbertram" {
		t.Errorf("VerifiedBy = %q, want %q", s.VerifiedBy, "dennisonbertram")
	}
}

func TestLoaderLoad_UnverifiedByDefault(t *testing.T) {
	dir := t.TempDir()
	// validSkillMD has no verified fields
	writeSkillFile(t, dir, "my-skill", validSkillMD)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if s.Verified {
		t.Error("Verified should default to false when not specified")
	}
	if s.VerifiedAt != "" {
		t.Errorf("VerifiedAt should be empty by default, got %q", s.VerifiedAt)
	}
	if s.VerifiedBy != "" {
		t.Errorf("VerifiedBy should be empty by default, got %q", s.VerifiedBy)
	}
}

func TestWriteVerification(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const skillMD = `---
name: check-skill
description: "A skill to verify. Trigger: check it"
version: 1
---
# Body content here.
`
	path := writeSkillFile(t, dir, "check-skill", skillMD)

	if err := WriteVerification(path, "2025-01-15T12:00:00Z", "claude-agent-1"); err != nil {
		t.Fatalf("WriteVerification: %v", err)
	}

	// Reload and check fields.
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() after WriteVerification: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if !s.Verified {
		t.Error("expected Verified=true after WriteVerification")
	}
	if s.VerifiedAt != "2025-01-15T12:00:00Z" {
		t.Errorf("VerifiedAt = %q, want %q", s.VerifiedAt, "2025-01-15T12:00:00Z")
	}
	if s.VerifiedBy != "claude-agent-1" {
		t.Errorf("VerifiedBy = %q, want %q", s.VerifiedBy, "claude-agent-1")
	}
}

func TestWriteVerification_MissingFile(t *testing.T) {
	t.Parallel()
	err := WriteVerification("/nonexistent/path/SKILL.md", "2025-01-15T12:00:00Z", "agent")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoaderLoad_ArgumentsField(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "deploy", `---
name: deploy
description: "Deploy skill"
version: 1
arguments: [target, env]
---
Deploy $target to $env.
`)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("Load() returned %d skills, want 1", len(skills))
	}
	want := []string{"target", "env"}
	got := skills[0].Arguments
	if len(got) != len(want) {
		t.Fatalf("Arguments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Arguments[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoaderLoad_ArgumentsFieldNoDeclaration(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "my-skill", validSkillMD)

	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(skills[0].Arguments) != 0 {
		t.Errorf("expected no declared arguments, got %v", skills[0].Arguments)
	}
}

func TestLoaderLoad_ArgumentsFieldInvalid(t *testing.T) {
	tests := []struct {
		name        string
		arguments   string
		wantErrPart string
	}{
		{"bad identifier hyphen", "[foo-bar]", `"foo-bar"`},
		{"bad identifier space", `["foo bar"]`, `"foo bar"`},
		{"numeric name", "[123]", `"123"`},
		{"numeric zero", "[0]", `"0"`},
		{"reserved ARGUMENTS", "[ARGUMENTS]", `"ARGUMENTS"`},
		{"reserved WORKSPACE", "[WORKSPACE]", `"WORKSPACE"`},
		{"reserved SKILL_DIR", "[SKILL_DIR]", `"SKILL_DIR"`},
		{"duplicate name", "[target, env, target]", `"target"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSkillFile(t, dir, "bad", `---
name: bad
description: "Bad skill"
version: 1
arguments: `+tt.arguments+`
---
Body.
`)

			loader := NewLoader(LoaderConfig{GlobalDir: dir})
			_, err := loader.Load()
			if err == nil {
				t.Fatalf("expected load error for arguments %s, got nil", tt.arguments)
			}
			if !strings.Contains(err.Error(), "arguments") {
				t.Errorf("error should mention the arguments field, got: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Errorf("error should name the offending entry %s, got: %v", tt.wantErrPart, err)
			}
		})
	}
}
