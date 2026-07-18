package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tools "go-agent-harness/internal/harness/tools"
)

// writeRecipeFile writes a recipe YAML file to dir.
func writeRecipeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeRecipeFile %s: %v", name, err)
	}
}

// buildCatalogWithRecipes creates a minimal catalog with recipes enabled.
func buildCatalogWithRecipes(t *testing.T, recipesDir string) []tools.Tool {
	t.Helper()
	cat, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableRecipes: true,
		RecipesDir:    recipesDir,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	return cat
}

// findTool finds a tool by name in the catalog.
func findToolInCatalog(cat []tools.Tool, name string) (tools.Tool, bool) {
	for _, t := range cat {
		if t.Definition.Name == name {
			return t, true
		}
	}
	return tools.Tool{}, false
}

// ---------------------------------------------------------------------------
// run_recipe tool tests
// ---------------------------------------------------------------------------

func TestRunRecipeTool_RegisteredInCatalog(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Say hello"
steps:
  - name: greet
    tool: bash
    args:
      command: "echo hello"
    capture: result
`)

	cat := buildCatalogWithRecipes(t, dir)
	_, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Error("expected run_recipe tool to be registered in catalog")
	}
}

func TestRunRecipeTool_IsDeferred(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Say hello"
steps:
  - name: greet
    tool: bash
    args:
      command: "echo hello"
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found in catalog")
	}
	if tool.Definition.Tier != tools.TierDeferred {
		t.Errorf("expected TierDeferred, got %q", tool.Definition.Tier)
	}
}

func TestRunRecipeTool_NotRegisteredWhenNoRecipes(t *testing.T) {
	dir := t.TempDir() // empty dir — no recipes

	cat := buildCatalogWithRecipes(t, dir)
	_, ok := findToolInCatalog(cat, "run_recipe")
	if ok {
		t.Error("expected run_recipe NOT to be registered when no recipes are loaded")
	}
}

func TestRunRecipeTool_NotRegisteredWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Say hello"
steps:
  - name: greet
    tool: bash
    args:
      command: "echo hello"
`)

	cat, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableRecipes: false, // disabled
		RecipesDir:    dir,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	_, ok := findToolInCatalog(cat, "run_recipe")
	if ok {
		t.Error("expected run_recipe NOT to be registered when EnableRecipes=false")
	}
}

func TestRunRecipeTool_InvokesRecipe(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "greeter.yaml", `
name: greeter
description: "Greet someone"
steps:
  - name: say_hi
    tool: bash
    args:
      command: "echo hi {{target}}"
    capture: greeting
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found")
	}

	args, _ := json.Marshal(map[string]any{
		"name": "greeter",
		"args": map[string]string{"target": "world"},
	})

	ctx := context.WithValue(context.Background(), tools.ContextKeyRunID, "test-run")
	out, err := tool.Handler(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunRecipeTool_UnknownRecipeError(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Hello"
steps:
  - name: s1
    tool: bash
    args:
      command: "echo hi"
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found")
	}

	args, _ := json.Marshal(map[string]any{"name": "nonexistent"})
	_, err := tool.Handler(context.Background(), args)
	if err == nil {
		t.Error("expected error for unknown recipe, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected 'nonexistent' in error, got %q", err.Error())
	}
}

func TestRunRecipeTool_MissingNameError(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Hello"
steps:
  - name: s1
    tool: bash
    args:
      command: "echo hi"
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, _ := findToolInCatalog(cat, "run_recipe")

	args, _ := json.Marshal(map[string]any{})
	_, err := tool.Handler(context.Background(), args)
	if err == nil {
		t.Error("expected error for missing name, got nil")
	}
}

func TestRunRecipeTool_MissingDirectory(t *testing.T) {
	// A missing recipes dir should not cause an error — just no recipes loaded
	cat, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableRecipes: true,
		RecipesDir:    "/tmp/no-such-recipes-dir-xyz",
	})
	if err != nil {
		t.Fatalf("expected no error for missing recipes dir, got: %v", err)
	}
	_, ok := findToolInCatalog(cat, "run_recipe")
	if ok {
		t.Error("expected run_recipe NOT to be registered for missing dir")
	}
}

func TestRunRecipeTool_HasRecipeTags(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "lint.yaml", `
name: lint_and_fix
description: "Lint and fix"
steps:
  - name: s1
    tool: bash
    args:
      command: "echo lint"
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found")
	}

	// Should have "recipe" tag and the recipe name as a tag
	hasRecipeTag := false
	hasNameTag := false
	for _, tag := range tool.Definition.Tags {
		if tag == "recipe" {
			hasRecipeTag = true
		}
		if tag == "lint_and_fix" {
			hasNameTag = true
		}
	}
	if !hasRecipeTag {
		t.Errorf("expected 'recipe' tag, got %v", tool.Definition.Tags)
	}
	if !hasNameTag {
		t.Errorf("expected 'lint_and_fix' tag, got %v", tool.Definition.Tags)
	}
}

func TestRunRecipeTool_InvalidJSONArgs(t *testing.T) {
	dir := t.TempDir()
	writeRecipeFile(t, dir, "hello.yaml", `
name: hello
description: "Hello"
steps:
  - name: s1
    tool: bash
    args:
      command: "echo hi"
`)

	cat := buildCatalogWithRecipes(t, dir)
	tool, _ := findToolInCatalog(cat, "run_recipe")

	_, err := tool.Handler(context.Background(), json.RawMessage("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON args, got nil")
	}
}

func TestBuildCatalog_RecipeLoadError(t *testing.T) {
	dir := t.TempDir()
	// Write a malformed YAML file
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: [broken"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableRecipes: true,
		RecipesDir:    dir,
	})
	if err == nil {
		t.Error("expected BuildCatalog to return error for malformed recipe, got nil")
	}
}

// ---------------------------------------------------------------------------
// run_recipe policy enforcement (issue #788)
// ---------------------------------------------------------------------------

// denyBashPolicy denies the bash tool and allows everything else.
type denyBashPolicy struct{}

func (denyBashPolicy) Allow(_ context.Context, in tools.PolicyInput) (tools.PolicyDecision, error) {
	if in.ToolName == "bash" {
		return tools.PolicyDecision{Allow: false, Reason: "bash denied by test policy"}, nil
	}
	return tools.PolicyDecision{Allow: true, Reason: "ok"}, nil
}

// allowAllPolicy allows every tool call.
type allowAllPolicy struct{}

func (allowAllPolicy) Allow(_ context.Context, _ tools.PolicyInput) (tools.PolicyDecision, error) {
	return tools.PolicyDecision{Allow: true, Reason: "ok"}, nil
}

// TestRunRecipeTool_PolicyAppliesToSteps is the regression test for issue
// #788: the recipe HandlerMap must snapshot policy-wrapped handlers so a
// recipe step is subject to the same policy checks as a direct invocation.
// applyPolicy reports a denial as a marshaled JSON result (not a Go error),
// so the assertions are on output content and absent side effects.
func TestRunRecipeTool_PolicyAppliesToSteps(t *testing.T) {
	ws := t.TempDir()
	dir := t.TempDir()
	pwned := filepath.Join(ws, "pwned")
	writeRecipeFile(t, dir, "pwn.yaml", fmt.Sprintf(`
name: pwn
description: "Attempt to create a file via a bash step"
steps:
  - name: s1
    tool: bash
    args:
      command: "touch '%s'"
`, pwned))

	cat, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: ws,
		ApprovalMode:  tools.ApprovalModePermissions,
		Policy:        denyBashPolicy{},
		EnableRecipes: true,
		RecipesDir:    dir,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	ctx := context.WithValue(context.Background(), tools.ContextKeyRunID, "test-run")

	// Sanity: a direct bash invocation through the same catalog is denied.
	// This proves the policy machinery itself is wired correctly, so a
	// failure below can only mean the recipe path bypasses it.
	bashTool, ok := findToolInCatalog(cat, "bash")
	if !ok {
		t.Fatal("bash not found in catalog")
	}
	directPwned := filepath.Join(ws, "pwned_direct")
	directArgs, _ := json.Marshal(map[string]any{"command": "touch '" + directPwned + "'"})
	directOut, err := bashTool.Handler(ctx, directArgs)
	if err != nil {
		t.Fatalf("direct bash: unexpected Go error (denial is a marshaled result): %v", err)
	}
	if !strings.Contains(directOut, "permission_denied") {
		t.Fatalf("expected direct bash denial to contain permission_denied, got %q", directOut)
	}
	if _, statErr := os.Stat(directPwned); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to not exist after denied direct bash call", directPwned)
	}

	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found in catalog")
	}
	args, _ := json.Marshal(map[string]any{"name": "pwn"})
	out, err := tool.Handler(ctx, args)
	if err != nil {
		t.Fatalf("run_recipe: unexpected error: %v", err)
	}
	if !strings.Contains(out, "permission_denied") {
		t.Errorf("expected recipe step denial to contain permission_denied, got %q", out)
	}
	if _, statErr := os.Stat(pwned); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (policy-denied recipe step must not execute)", pwned)
	}
}

// TestRunRecipeTool_PolicyAllowsSteps is the positive control: under an
// allow-all policy the same recipe step executes normally.
func TestRunRecipeTool_PolicyAllowsSteps(t *testing.T) {
	ws := t.TempDir()
	dir := t.TempDir()
	created := filepath.Join(ws, "created")
	writeRecipeFile(t, dir, "mk.yaml", fmt.Sprintf(`
name: mk
description: "Create a file via a bash step"
steps:
  - name: s1
    tool: bash
    args:
      command: "touch '%s'"
`, created))

	cat, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: ws,
		ApprovalMode:  tools.ApprovalModePermissions,
		Policy:        allowAllPolicy{},
		EnableRecipes: true,
		RecipesDir:    dir,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	tool, ok := findToolInCatalog(cat, "run_recipe")
	if !ok {
		t.Fatal("run_recipe not found in catalog")
	}
	args, _ := json.Marshal(map[string]any{"name": "mk"})
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunID, "test-run")
	if _, err := tool.Handler(ctx, args); err != nil {
		t.Fatalf("run_recipe: unexpected error: %v", err)
	}
	if _, statErr := os.Stat(created); statErr != nil {
		t.Errorf("expected %s to exist (policy-allowed recipe step should execute): %v", created, statErr)
	}
}
