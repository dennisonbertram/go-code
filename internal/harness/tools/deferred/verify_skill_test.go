package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tools "go-agent-harness/internal/harness/tools"
)

// ---------- mock SkillVerifier ----------

type mockSkillVerifier struct {
	mu        sync.Mutex
	skills    map[string]tools.SkillInfo
	bodies    map[string]string
	files     map[string]string // skill name -> SKILL.md content
	filePaths map[string]string // skill name -> file path
	updateErr error
	updated   map[string]skillUpdateRecord
}

type skillUpdateRecord struct {
	verified   bool
	verifiedAt time.Time
	verifiedBy string
}

func (m *mockSkillVerifier) GetSkill(name string) (tools.SkillInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.skills[name]
	return s, ok
}

func (m *mockSkillVerifier) ListSkills() []tools.SkillInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]tools.SkillInfo, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

func (m *mockSkillVerifier) ResolveSkill(_ context.Context, name, args, workspace string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.bodies[name]
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return body, nil
}

func (m *mockSkillVerifier) GetSkillFilePath(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, ok := m.filePaths[name]
	return path, ok
}

func (m *mockSkillVerifier) UpdateSkillVerification(ctx context.Context, name string, verified bool, verifiedAt time.Time, verifiedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	if m.updated == nil {
		m.updated = make(map[string]skillUpdateRecord)
	}
	m.updated[name] = skillUpdateRecord{verified: verified, verifiedAt: verifiedAt, verifiedBy: verifiedBy}
	return nil
}

// buildValidSkillFile writes a valid SKILL.md file to a temp dir and returns the path.
func buildValidSkillFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Note: description is quoted to avoid YAML parsing issues with colons.
	content := fmt.Sprintf("---\nname: %s\ndescription: \"A test skill. Trigger: test this skill\"\nversion: 1\n---\n%s", name, body)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return path
}

// buildMockVerifier creates a mock verifier with a single valid skill backed by a temp file.
func buildMockVerifier(t *testing.T) (*mockSkillVerifier, string) {
	t.Helper()
	dir := t.TempDir()
	body := strings.Repeat("This is a valid skill body that has enough content. ", 3)
	path := buildValidSkillFile(t, dir, "my-skill", body)

	verifier := &mockSkillVerifier{
		skills: map[string]tools.SkillInfo{
			"my-skill": {
				Name:        "my-skill",
				Description: "A test skill",
				Source:      "local",
			},
		},
		bodies: map[string]string{
			"my-skill": body,
		},
		filePaths: map[string]string{
			"my-skill": path,
		},
	}
	return verifier, dir
}

// ---------- VerifySkillTool definition tests ----------

func TestVerifySkillTool_Definition(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	if tool.Definition.Name != "verify_skill" {
		t.Fatalf("expected name=verify_skill, got %s", tool.Definition.Name)
	}
	if tool.Definition.Tier != tools.TierDeferred {
		t.Fatalf("expected tier=deferred, got %s", tool.Definition.Tier)
	}
	if tool.Handler == nil {
		t.Fatal("handler is nil")
	}
	if tool.Definition.Parameters == nil {
		t.Fatal("parameters is nil")
	}
}

func TestVerifySkillTool_HasTags(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)
	assertHasTags(t, tool, "skill", "verify", "quality")
}

func TestVerifySkillTool_Description(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)
	if tool.Definition.Description == "" {
		t.Fatal("expected non-empty description")
	}
}

// ---------- handler: missing name ----------

func TestVerifySkillTool_Handler_MissingName(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifySkillTool_Handler_EmptyName(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":""}`))
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifySkillTool_Handler_InvalidJSON(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------- handler: skill not found ----------

func TestVerifySkillTool_Handler_SkillNotFound(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"nonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for nonexistent skill")
	}
	if !strings.Contains(fmt.Sprint(result["error"]), "not found") {
		t.Fatalf("expected 'not found' in error, got: %v", result["error"])
	}
}

// ---------- handler: happy path (all checks pass) ----------

func TestVerifySkillTool_Handler_HappyPath(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result["passed"].(bool) {
		t.Fatalf("expected passed=true, checks=%v error=%v", result["checks"], result["error"])
	}
	if result["skill"].(string) != "my-skill" {
		t.Fatalf("expected skill=my-skill, got %v", result["skill"])
	}
	if result["verified_by"].(string) != "automated" {
		t.Fatalf("expected verified_by=automated, got %v", result["verified_by"])
	}
}

func TestVerifySkillTool_Handler_UpdatesVerification(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	verifier.mu.Lock()
	rec, ok := verifier.updated["my-skill"]
	verifier.mu.Unlock()
	if !ok {
		t.Fatal("expected UpdateSkillVerification to be called for my-skill")
	}
	if !rec.verified {
		t.Fatal("expected verified=true after passing checks")
	}
	if rec.verifiedBy != "automated" {
		t.Fatalf("expected verifiedBy=automated, got %q", rec.verifiedBy)
	}
	if rec.verifiedAt.IsZero() {
		t.Fatal("expected non-zero verifiedAt")
	}
}

// ---------- handler: structural validation failures ----------

func TestVerifySkillTool_Handler_MissingFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "bad-skill"

	// Write a SKILL.md with no frontmatter
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "No frontmatter here, just plain text."
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills: map[string]tools.SkillInfo{
			name: {Name: name, Description: "a bad skill", Source: "local"},
		},
		bodies:    map[string]string{name: "body"},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"bad-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for skill with missing frontmatter")
	}
}

func TestVerifySkillTool_Handler_EmptyBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "empty-body"

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Valid frontmatter but empty body (description quoted to avoid YAML colon issue)
	content := fmt.Sprintf("---\nname: %s\ndescription: \"A skill with an empty body. Trigger: test\"\nversion: 1\n---\n", name)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills:    map[string]tools.SkillInfo{name: {Name: name, Description: "empty body skill", Source: "local"}},
		bodies:    map[string]string{name: ""},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for skill with empty body")
	}
}

func TestVerifySkillTool_Handler_ShortBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "short-body"

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: \"Short body. Trigger: test\"\nversion: 1\n---\nToo short.", name)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills:    map[string]tools.SkillInfo{name: {Name: name, Description: "short body", Source: "local"}},
		bodies:    map[string]string{name: "Too short."},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for skill with body < 50 chars")
	}
}

func TestVerifySkillTool_Handler_MissingName_InFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "no-name-skill"

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Frontmatter with no name field (description quoted to avoid YAML colon issue)
	content := "---\ndescription: \"A skill without a name. Trigger: test\"\nversion: 1\n---\nThis body is long enough to pass the length check. It has sufficient content here."
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills:    map[string]tools.SkillInfo{name: {Name: name, Description: "no name", Source: "local"}},
		bodies:    map[string]string{name: "long enough body content that should pass length check"},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false when frontmatter name is empty")
	}
}

func TestVerifySkillTool_Handler_MissingDescription_InFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "no-desc"

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Frontmatter with no description
	content := fmt.Sprintf(`---
name: %s
version: 1
---
This body is long enough to pass the length check. It has sufficient content here.`, name)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills:    map[string]tools.SkillInfo{name: {Name: name, Description: "", Source: "local"}},
		bodies:    map[string]string{name: "long enough body content that should pass length check"},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false when frontmatter description is empty")
	}
}

// ---------- handler: no file path (file not accessible) ----------

func TestVerifySkillTool_Handler_NoFilePath(t *testing.T) {
	t.Parallel()
	verifier := &mockSkillVerifier{
		skills: map[string]tools.SkillInfo{
			"in-memory": {Name: "in-memory", Description: "skill with no file", Source: "local"},
		},
		bodies:    map[string]string{"in-memory": "body"},
		filePaths: map[string]string{}, // no file path registered
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"in-memory"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// No file path means we cannot do structural checks; should fail
	if result["passed"].(bool) {
		t.Fatal("expected passed=false when no file path available")
	}
}

// ---------- handler: store update error ----------

func TestVerifySkillTool_Handler_StoreUpdateError(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	verifier.updateErr = fmt.Errorf("db locked")
	tool := VerifySkillTool(verifier)

	// Even if store update fails, the tool should return an error
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err == nil {
		t.Fatal("expected error when store update fails")
	}
	if !strings.Contains(err.Error(), "db locked") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- handler: checks report structure ----------

func TestVerifySkillTool_Handler_ChecksReported(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	checks, ok := result["checks"].([]any)
	if !ok {
		t.Fatalf("expected checks to be an array, got %T: %v", result["checks"], result["checks"])
	}
	if len(checks) == 0 {
		t.Fatal("expected at least one check reported")
	}

	// Each check should have name and passed fields
	for i, c := range checks {
		cm, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("check[%d] is not a map: %T", i, c)
		}
		if _, ok := cm["name"]; !ok {
			t.Errorf("check[%d] missing 'name' field", i)
		}
		if _, ok := cm["passed"]; !ok {
			t.Errorf("check[%d] missing 'passed' field", i)
		}
	}
}

// ---------- concurrent access ----------

func TestVerifySkillTool_Handler_ConcurrentVerification(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)

	// Add a second skill
	dir := t.TempDir()
	body := strings.Repeat("Another valid skill body with enough content. ", 3)
	path := buildValidSkillFile(t, dir, "other-skill", body)
	verifier.mu.Lock()
	verifier.skills["other-skill"] = tools.SkillInfo{Name: "other-skill", Description: "Another skill", Source: "local"}
	verifier.bodies["other-skill"] = body
	verifier.filePaths["other-skill"] = path
	verifier.mu.Unlock()

	tool := VerifySkillTool(verifier)

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
			if err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"other-skill"}`))
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

// ---------- invalid YAML frontmatter ----------

func TestVerifySkillTool_Handler_InvalidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "bad-yaml"

	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Invalid YAML in frontmatter
	content := `---
name: [unclosed bracket
description: bad yaml
version: 1
---
This body is long enough to pass length checks for sure.`
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	verifier := &mockSkillVerifier{
		skills:    map[string]tools.SkillInfo{name: {Name: name, Description: "bad yaml", Source: "local"}},
		bodies:    map[string]string{name: "long enough body content that should pass length check easily"},
		filePaths: map[string]string{name: path},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for skill with invalid YAML frontmatter")
	}
}

// ---------- file not readable ----------

func TestVerifySkillTool_Handler_FileNotReadable(t *testing.T) {
	t.Parallel()
	verifier := &mockSkillVerifier{
		skills: map[string]tools.SkillInfo{
			"ghost-skill": {Name: "ghost-skill", Description: "a skill file that was deleted", Source: "local"},
		},
		bodies:    map[string]string{"ghost-skill": "some body"},
		filePaths: map[string]string{"ghost-skill": "/nonexistent/path/SKILL.md"},
	}
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"ghost-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false when skill file is not readable")
	}
}

// ---------- VerifySkillTool registered in catalog ----------

func TestVerifySkillTool_RegisteredWhenEnabled(t *testing.T) {
	t.Parallel()
	verifier, _ := buildMockVerifier(t)
	list, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableSkills:  true,
		SkillLister:   verifier,
		SkillVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	found := false
	for _, tool := range list {
		if tool.Definition.Name == "verify_skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected verify_skill tool in catalog when skills enabled and verifier provided")
	}
}

func TestVerifySkillTool_NotRegisteredWhenVerifierNil(t *testing.T) {
	t.Parallel()
	lister, _ := buildMockVerifier(t)
	list, err := tools.BuildCatalog(tools.BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableSkills:  true,
		SkillLister:   lister,
		SkillVerifier: nil, // no verifier
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	for _, tool := range list {
		if tool.Definition.Name == "verify_skill" {
			t.Fatal("verify_skill should not be registered when SkillVerifier is nil")
		}
	}
}
