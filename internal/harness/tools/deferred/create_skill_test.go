package deferred

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestCreateSkillToolHappyPath verifies that a valid skill is created on disk.
func TestCreateSkillToolHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":        "code-review",
		"description": "Review code for quality and correctness",
		"trigger":     "When user asks to review code",
		"content":     "## Instructions\n\nReview the code carefully.",
	})

	out, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["status"] != "created" {
		t.Fatalf("expected status=created, got %v", result["status"])
	}
	if result["name"] != "code-review" {
		t.Fatalf("expected name=code-review, got %v", result["name"])
	}
	wantPath := filepath.Join(dir, "code-review", "SKILL.md")
	if result["path"] != wantPath {
		t.Fatalf("expected path=%s, got %v", wantPath, result["path"])
	}

	// Verify file was actually written
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: code-review") {
		t.Errorf("expected frontmatter name, got:\n%s", content)
	}
	if !strings.Contains(content, "version: 1") {
		t.Errorf("expected version: 1 in frontmatter, got:\n%s", content)
	}
	if !strings.Contains(content, "Review the code carefully.") {
		t.Errorf("expected body content, got:\n%s", content)
	}
	// Bug 1 fix: trigger must be embedded inside the description, not a separate YAML key.
	if !strings.Contains(content, "Trigger: When user asks to review code") {
		t.Errorf("expected trigger embedded in description, got:\n%s", content)
	}
	if strings.Contains(content, "\ntrigger:") {
		t.Errorf("trigger must NOT be a separate YAML key, got:\n%s", content)
	}
}

// TestCreateSkillToolDuplicateRejected ensures creating a skill that already exists fails.
func TestCreateSkillToolDuplicateRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create the skill directory and SKILL.md manually
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := CreateSkillTool(dir)
	raw := mustMarshal(t, map[string]any{
		"name":        "my-skill",
		"description": "A skill",
		"trigger":     "When needed",
		"content":     "Do things.",
	})

	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for duplicate skill, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' error, got: %v", err)
	}
}

// TestCreateSkillToolInvalidName verifies name validation.
func TestCreateSkillToolInvalidName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	cases := []string{
		"My Skill",  // spaces
		"MySkill",   // uppercase
		"my_skill",  // underscore
		"-my-skill", // leading hyphen
		"my-skill-", // trailing hyphen
		"",          // empty
		"my skill!", // special char
	}

	for _, name := range cases {
		t.Run("name="+name, func(t *testing.T) {
			raw := mustMarshal(t, map[string]any{
				"name":        name,
				"description": "A skill",
				"trigger":     "When needed",
				"content":     "Do things.",
			})
			_, err := tool.Handler(context.Background(), raw)
			if err == nil {
				t.Fatalf("expected validation error for name %q", name)
			}
		})
	}
}

// TestCreateSkillToolMissingDescription verifies description is required.
func TestCreateSkillToolMissingDescription(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":    "my-skill",
		"trigger": "When needed",
		"content": "Do things.",
	})
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
	if !strings.Contains(err.Error(), "description is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateSkillToolMissingTrigger verifies trigger is required.
func TestCreateSkillToolMissingTrigger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":        "my-skill",
		"description": "A skill",
		"content":     "Do things.",
	})
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for missing trigger")
	}
	if !strings.Contains(err.Error(), "trigger is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateSkillToolMissingContent verifies content is required.
func TestCreateSkillToolMissingContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":        "my-skill",
		"description": "A skill",
		"trigger":     "When needed",
		"content":     "",
	})
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateSkillToolNoSkillsDir verifies an error when no directory is configured.
func TestCreateSkillToolNoSkillsDir(t *testing.T) {
	t.Parallel()
	tool := CreateSkillTool("") // empty dir

	raw := mustMarshal(t, map[string]any{
		"name":        "my-skill",
		"description": "A skill",
		"trigger":     "When needed",
		"content":     "Do things.",
	})
	_, err := tool.Handler(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for empty skills dir")
	}
	if !strings.Contains(err.Error(), "no skills directory configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateSkillToolInvalidJSON verifies invalid JSON input is rejected.
func TestCreateSkillToolInvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestCreateSkillToolDefinition checks the tool metadata.
func TestCreateSkillToolDefinition(t *testing.T) {
	t.Parallel()
	tool := CreateSkillTool("/tmp/skills")

	if tool.Definition.Name != "create_skill" {
		t.Fatalf("expected name=create_skill, got %s", tool.Definition.Name)
	}
	if !tool.Definition.Mutating {
		t.Fatal("expected mutating=true")
	}
	if tool.Definition.ParallelSafe {
		t.Fatal("expected parallel_safe=false")
	}
	if tool.Definition.Tier != "deferred" {
		t.Fatalf("expected tier=deferred, got %s", tool.Definition.Tier)
	}
}

// TestCreateSkillToolFileContainsFrontmatter verifies the output file has valid SKILL.md structure.
func TestCreateSkillToolFileContainsFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":        "deploy",
		"description": "Deploy to production",
		"trigger":     "When user asks to deploy",
		"content":     "Run the deploy script.",
	})

	if _, err := tool.Handler(context.Background(), raw); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "deploy", "SKILL.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	content := string(data)

	// Must start with ---
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		t.Errorf("expected file to start with ---, got:\n%s", content)
	}
	// Must have closing ---
	if strings.Count(content, "---") < 2 {
		t.Errorf("expected at least two --- delimiters in:\n%s", content)
	}
	// Must have name, version
	if !strings.Contains(content, "name: deploy") {
		t.Errorf("missing name in frontmatter:\n%s", content)
	}
	if !strings.Contains(content, "version: 1") {
		t.Errorf("missing version in frontmatter:\n%s", content)
	}
	// Trigger must be embedded in description field, not a separate key
	if !strings.Contains(content, "Trigger: When user asks to deploy") {
		t.Errorf("expected trigger in description, got:\n%s", content)
	}
	if strings.Contains(content, "\ntrigger:") {
		t.Errorf("trigger must NOT be a separate YAML key, got:\n%s", content)
	}
}

// TestCreateSkillToolDescriptionWithSpecialChars verifies descriptions with special chars are quoted.
func TestCreateSkillToolDescriptionWithSpecialChars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	raw := mustMarshal(t, map[string]any{
		"name":        "my-skill",
		"description": "Deploy: production #1",
		"trigger":     "When needed",
		"content":     "Do things.",
	})
	if _, err := tool.Handler(context.Background(), raw); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "my-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	// Description with colon should be quoted in YAML; trigger is appended
	if !strings.Contains(string(data), `"Deploy: production #1 Trigger: When needed"`) {
		t.Errorf("expected quoted description with trigger, got:\n%s", string(data))
	}
}

// TestCreateSkillToolConcurrentCreation tests race-condition safety.
func TestCreateSkillToolConcurrentCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	// Create different skills concurrently - should all succeed
	type result struct {
		err error
	}
	results := make(chan result, 5)

	for i := 0; i < 5; i++ {
		go func(i int) {
			name := strings.ToLower(string(rune('a'+i))) + "-skill"
			raw := mustMarshal(t, map[string]any{
				"name":        name,
				"description": "Skill " + name,
				"trigger":     "When needed",
				"content":     "Body for " + name,
			})
			_, err := tool.Handler(context.Background(), raw)
			results <- result{err: err}
		}(i)
	}

	for i := 0; i < 5; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent create failed: %v", r.err)
		}
	}
}

// TestCreateSkillToolConcurrentDuplicate verifies that concurrent attempts to
// create the same skill result in exactly one success and the rest getting
// "already exists" errors (atomic O_EXCL, no TOCTOU race).
func TestCreateSkillToolConcurrentDuplicate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := CreateSkillTool(dir)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	successes := make(chan string, goroutines)
	duplicates := make(chan error, goroutines)
	other := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			raw := mustMarshal(t, map[string]any{
				"name":        "dup-skill",
				"description": "A duplicate skill",
				"trigger":     "When needed",
				"content":     "Body.",
			})
			out, err := tool.Handler(context.Background(), raw)
			if err == nil {
				successes <- out
			} else if strings.Contains(err.Error(), "already exists") {
				duplicates <- err
			} else {
				other <- err
			}
		}()
	}
	wg.Wait()
	close(successes)
	close(duplicates)
	close(other)

	successCount := 0
	for range successes {
		successCount++
	}
	dupCount := 0
	for range duplicates {
		dupCount++
	}
	for err := range other {
		t.Errorf("unexpected error: %v", err)
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 success, got %d", successCount)
	}
	if dupCount != goroutines-1 {
		t.Fatalf("expected %d duplicate errors, got %d", goroutines-1, dupCount)
	}
}

// mustMarshal marshals v to JSON, failing the test on error.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
