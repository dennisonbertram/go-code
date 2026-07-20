package skills

import (
	"context"
	"strings"
	"testing"
)

func setupResolverRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	content := `---
name: greet
description: "Greeting skill"
version: 1
---
Hello $0! You said: $ARGUMENTS
Workspace: $WORKSPACE
Skill dir: $SKILL_DIR
`
	writeSkillFile(t, dir, "greet", content)

	r := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := r.Load(loader); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolverResolveSkill_HappyPath(t *testing.T) {
	reg := setupResolverRegistry(t)
	resolver := NewResolver(reg)

	result, err := resolver.ResolveSkill(context.Background(), "greet", "world today", "/my/workspace")
	if err != nil {
		t.Fatalf("ResolveSkill() error = %v", err)
	}

	if !strings.Contains(result, "Hello world!") {
		t.Errorf("expected $0=world, got: %s", result)
	}
	if !strings.Contains(result, "You said: world today") {
		t.Errorf("expected $ARGUMENTS=world today, got: %s", result)
	}
	if !strings.Contains(result, "Workspace: /my/workspace") {
		t.Errorf("expected $WORKSPACE=/my/workspace, got: %s", result)
	}
	if !strings.Contains(result, "Skill dir:") {
		t.Errorf("expected $SKILL_DIR to be set, got: %s", result)
	}
}

func TestResolverResolveSkill_NotFound(t *testing.T) {
	reg := NewRegistry()
	resolver := NewResolver(reg)

	_, err := resolver.ResolveSkill(context.Background(), "nonexistent", "", "/ws")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
}

func TestResolverResolveSkill_EmptyArgs(t *testing.T) {
	reg := setupResolverRegistry(t)
	resolver := NewResolver(reg)

	result, err := resolver.ResolveSkill(context.Background(), "greet", "", "/ws")
	if err != nil {
		t.Fatalf("ResolveSkill() error = %v", err)
	}

	// $0 should be empty, $ARGUMENTS should be empty
	if !strings.Contains(result, "Hello !") {
		t.Errorf("expected empty $0, got: %s", result)
	}
	if !strings.Contains(result, "You said: \n") {
		t.Errorf("expected empty $ARGUMENTS, got: %s", result)
	}
}

func TestResolverResolveSkill_ManyArgs(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: multi
description: "Multi arg skill"
version: 1
---
$0 $1 $2 $3 $4 $5 $6 $7 $8
`
	writeSkillFile(t, dir, "multi", content)

	reg := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(reg)
	result, err := resolver.ResolveSkill(context.Background(), "multi", "a b c d e f g h i", "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	expected := "a b c d e f g h i"
	result = strings.TrimSpace(result)
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestResolverImplementsInterface(t *testing.T) {
	reg := NewRegistry()
	var _ SkillResolver = NewResolver(reg)
}

func TestResolverResolveSkill_DoubleDigitPositional(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: wide
description: "Many arg skill"
version: 1
---
first=$0 eleventh=$10
`
	writeSkillFile(t, dir, "wide", content)

	reg := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(reg)
	result, err := resolver.ResolveSkill(context.Background(), "wide", "a b c d e f g h i j k", "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if !strings.Contains(result, "first=a eleventh=k") {
		t.Errorf("expected $0=a and $10=k, got: %q", result)
	}
}

func TestResolverResolveSkill_FallbackAppendsRawArguments(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: summarize
description: "Summarize skill"
version: 1
---
Summarize the transcript.
`
	writeSkillFile(t, dir, "summarize", content)

	reg := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(reg)
	result, err := resolver.ResolveSkill(context.Background(), "summarize", `focus on "error rates" please`, "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Placeholder-less body: raw args appended verbatim (untokenized, quotes preserved).
	want := `ARGUMENTS: focus on "error rates" please`
	if !strings.HasSuffix(result, want) {
		t.Errorf("expected result to end with %q, got: %q", want, result)
	}
	if !strings.Contains(result, "Summarize the transcript.\nARGUMENTS: ") {
		t.Errorf("expected ARGUMENTS line appended after body, got: %q", result)
	}
}

func TestResolverResolveSkill_FallbackSkippedWithPositional(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: greet2
description: "Greeting skill"
version: 1
---
Hello $0!
`
	writeSkillFile(t, dir, "greet2", content)

	reg := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(reg)
	result, err := resolver.ResolveSkill(context.Background(), "greet2", "world", "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if strings.Contains(result, "ARGUMENTS:") {
		t.Errorf("expected no fallback append when positional placeholder present, got: %q", result)
	}
	if !strings.Contains(result, "Hello world!") {
		t.Errorf("expected $0=world, got: %q", result)
	}
}

func TestResolverResolveSkill_FallbackSkippedWithArgumentsVar(t *testing.T) {
	reg := setupResolverRegistry(t) // body references $0 and $ARGUMENTS
	resolver := NewResolver(reg)

	result, err := resolver.ResolveSkill(context.Background(), "greet", "world today", "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if strings.Contains(result, "ARGUMENTS: world today") {
		t.Errorf("expected no fallback append when $ARGUMENTS present, got: %q", result)
	}
}

func TestResolverResolveSkill_FallbackSkippedWhenArgsEmpty(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: plain
description: "Plain skill"
version: 1
---
Just instructions, no placeholders.
`
	writeSkillFile(t, dir, "plain", content)

	reg := NewRegistry()
	loader := NewLoader(LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(reg)
	result, err := resolver.ResolveSkill(context.Background(), "plain", "", "/ws")
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if strings.Contains(result, "ARGUMENTS:") {
		t.Errorf("expected no fallback append for empty args, got: %q", result)
	}
}
