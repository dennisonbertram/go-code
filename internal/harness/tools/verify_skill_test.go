package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
)

// ---------- mock SkillVerifier for flat tools package ----------

type mockFlatSkillVerifier struct {
	skills    map[string]SkillInfo
	bodies    map[string]string
	filePaths map[string]string
	updateErr error
	updated   map[string]flatUpdateRecord
}

type flatUpdateRecord struct {
	verified   bool
	verifiedAt time.Time
	verifiedBy string
}

func (m *mockFlatSkillVerifier) GetSkill(name string) (SkillInfo, bool) {
	s, ok := m.skills[name]
	return s, ok
}

func (m *mockFlatSkillVerifier) ListSkills() []SkillInfo {
	result := make([]SkillInfo, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

func (m *mockFlatSkillVerifier) ResolveSkill(_ context.Context, name, args, workspace string) (string, error) {
	body, ok := m.bodies[name]
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return body, nil
}

func (m *mockFlatSkillVerifier) GetSkillFilePath(name string) (string, bool) {
	path, ok := m.filePaths[name]
	return path, ok
}

func (m *mockFlatSkillVerifier) UpdateSkillVerification(_ context.Context, name string, verified bool, verifiedAt time.Time, verifiedBy string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if m.updated == nil {
		m.updated = make(map[string]flatUpdateRecord)
	}
	m.updated[name] = flatUpdateRecord{verified: verified, verifiedAt: verifiedAt, verifiedBy: verifiedBy}
	return nil
}

// buildFlatValidSkillFile writes a valid SKILL.md to a temp dir.
func buildFlatValidSkillFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: \"A test skill\"\nversion: 1\n---\n%s", name, body)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return path
}

// buildFlatMockVerifier creates a mock verifier with a single valid skill.
func buildFlatMockVerifier(t *testing.T) *mockFlatSkillVerifier {
	t.Helper()
	dir := t.TempDir()
	body := strings.Repeat("This is a valid skill body that has enough content. ", 3)
	path := buildFlatValidSkillFile(t, dir, "my-skill", body)
	return &mockFlatSkillVerifier{
		skills: map[string]SkillInfo{
			"my-skill": {Name: "my-skill", Description: "A test skill", Source: "local"},
		},
		bodies:    map[string]string{"my-skill": body},
		filePaths: map[string]string{"my-skill": path},
	}
}

// ---------- VerifySkillTool (public) tests ----------

func TestVerifySkillToolPublic_Definition(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)
	if tool.Definition.Name != "verify_skill" {
		t.Fatalf("expected name=verify_skill, got %s", tool.Definition.Name)
	}
	if tool.Definition.Tier != TierDeferred {
		t.Fatalf("expected tier=deferred, got %s", tool.Definition.Tier)
	}
	if tool.Handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestVerifySkillToolPublic_HappyPath(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result["passed"].(bool) {
		t.Fatalf("expected passed=true, got checks=%v", result["checks"])
	}
}

func TestVerifySkillToolPublic_MissingName(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifySkillToolPublic_SkillNotFound(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for nonexistent skill")
	}
}

func TestVerifySkillToolPublic_NoFilePath(t *testing.T) {
	t.Parallel()
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{"ghost": {Name: "ghost", Description: "no file", Source: "local"}},
		bodies:    map[string]string{"ghost": "body"},
		filePaths: map[string]string{},
	}
	tool := VerifySkillTool(verifier)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"ghost"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false when no file path")
	}
}

func TestVerifySkillToolPublic_StoreUpdateError(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	verifier.updateErr = fmt.Errorf("db error")
	tool := VerifySkillTool(verifier)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err == nil {
		t.Fatal("expected error when store update fails")
	}
	if !strings.Contains(err.Error(), "db error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifySkillToolPublic_ShortBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "short"
	path := buildFlatValidSkillFile(t, dir, name, "short")
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{name: {Name: name, Description: "short", Source: "local"}},
		bodies:    map[string]string{name: "short"},
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
		t.Fatal("expected passed=false for short body")
	}
}

func TestVerifySkillToolPublic_InvalidFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "bad-fm"
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "no frontmatter here at all"
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{name: {Name: name, Description: "bad", Source: "local"}},
		bodies:    map[string]string{name: "body"},
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
		t.Fatal("expected passed=false for missing frontmatter")
	}
}

func TestVerifySkillToolPublic_FileNotReadable(t *testing.T) {
	t.Parallel()
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{"x": {Name: "x", Description: "d", Source: "local"}},
		bodies:    map[string]string{"x": "b"},
		filePaths: map[string]string{"x": "/nonexistent/SKILL.md"},
	}
	tool := VerifySkillTool(verifier)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["passed"].(bool) {
		t.Fatal("expected passed=false for unreadable file")
	}
}

func TestVerifySkillToolPublic_MissingNameInFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "no-name"
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\ndescription: \"A skill without name\"\nversion: 1\n---\nThis body is long enough to satisfy the minimum length requirement for verification."
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{name: {Name: name, Description: "d", Source: "local"}},
		bodies:    map[string]string{name: "body"},
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
		t.Fatal("expected passed=false when frontmatter name is missing")
	}
}

func TestVerifySkillToolPublic_MissingDescriptionInFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "no-desc"
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := fmt.Sprintf("---\nname: %s\nversion: 1\n---\nThis body is long enough to satisfy the minimum length requirement for verification.", name)
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{name: {Name: name, Description: "", Source: "local"}},
		bodies:    map[string]string{name: "body"},
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
		t.Fatal("expected passed=false when frontmatter description is missing")
	}
}

func TestVerifySkillToolPublic_InvalidJSON(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestVerifySkillToolPublic_RegisteredInCatalog(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	list, err := BuildCatalog(BuildOptions{
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
		t.Fatal("expected verify_skill in catalog when skills+verifier enabled")
	}
}

func TestVerifySkillToolPublic_NotRegisteredWhenVerifierNil(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	list, err := BuildCatalog(BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableSkills:  true,
		SkillLister:   verifier,
		SkillVerifier: nil,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	for _, tool := range list {
		if tool.Definition.Name == "verify_skill" {
			t.Fatal("verify_skill should not be in catalog when SkillVerifier is nil")
		}
	}
}

// ---------- Verdict field tests ----------

func TestVerifySkillToolPublic_VerdictPass(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"my-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := result["verdict"].(string)
	if !ok {
		t.Fatalf("verdict field is missing or not a string in result: %v", result)
	}
	if v != "PASS" {
		t.Fatalf("expected verdict=PASS, got %q", v)
	}
	if result["passed"].(bool) != true {
		t.Fatalf("expected passed=true")
	}
}

func TestVerifySkillToolPublic_VerdictFail(t *testing.T) {
	t.Parallel()
	verifier := buildFlatMockVerifier(t)
	tool := VerifySkillTool(verifier)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := result["verdict"].(string)
	if !ok {
		t.Fatalf("verdict field is missing or not a string in result: %v", result)
	}
	if v != "FAIL" {
		t.Fatalf("expected verdict=FAIL for nonexistent skill, got %q", v)
	}
}

func TestVerifySkillToolPublic_VerdictPartial(t *testing.T) {
	t.Parallel()
	verifier := &mockFlatSkillVerifier{
		skills:    map[string]SkillInfo{"ghost": {Name: "ghost", Description: "no file", Source: "local"}},
		bodies:    map[string]string{"ghost": "body"},
		filePaths: map[string]string{},
	}
	tool := VerifySkillTool(verifier)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"ghost"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := result["verdict"].(string)
	if !ok {
		t.Fatalf("verdict field is missing or not a string in result: %v", result)
	}
	if v != "PARTIAL" {
		t.Fatalf("expected verdict=PARTIAL when skill exists but no file path, got %q", v)
	}
}

// ---------- Description anti-pattern keyword tests ----------

func TestVerifySkillDescription_ContainsAntiPatternKeywords(t *testing.T) {
	t.Parallel()
	desc := descriptions.Load("verify_skill")
	if desc == "" {
		t.Fatal("verify_skill description is empty")
	}
	lower := strings.ToLower(desc)

	// Named anti-patterns
	if !strings.Contains(lower, "verification_avoidance") {
		t.Error("description missing anti-pattern: verification_avoidance")
	}
	if !strings.Contains(lower, "first_80_seduction") {
		t.Error("description missing anti-pattern: first_80_seduction")
	}

	// Verdict output format
	if !strings.Contains(lower, "verdict: pass") {
		t.Error("description missing VERDICT: PASS")
	}
	if !strings.Contains(lower, "verdict: fail") {
		t.Error("description missing VERDICT: FAIL")
	}
	if !strings.Contains(lower, "verdict: partial") {
		t.Error("description missing VERDICT: PARTIAL")
	}

	// Evidence requirement
	if !strings.Contains(lower, "evidence requirement") {
		t.Error("description missing evidence requirement section")
	}
	if !strings.Contains(lower, "command run:") {
		t.Error("description missing Command run: evidence block reference")
	}
}

// splitAndParseVerifyFrontmatter edge case: missing closing delimiter
func TestSplitAndParseVerifyFrontmatter_MissingClosingDelimiter(t *testing.T) {
	t.Parallel()
	content := "---\nname: test\ndescription: d\nversion: 1\n"
	_, _, err := splitAndParseVerifyFrontmatter(content)
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}
