package core

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

// TestReadTool_Definition verifies the read tool constructor returns a valid tool.
func TestReadTool_Definition(t *testing.T) {
	tool := ReadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "read", tools.TierCore)
}

// TestReadTool_Handler_MissingPath verifies read returns an error when path is empty.
func TestReadTool_Handler_MissingPath(t *testing.T) {
	tool := ReadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestReadTool_Handler_Success verifies read returns file content.
func TestReadTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ReadTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestReadTool_Handler_WorkspaceScope_BlocksAbsoluteEscape is a regression
// test for BUG-1 exercised through the PRODUCTION tool (core.ReadTool, as
// wired by harness.NewDefaultRegistryWithOptions), not just the shared
// ConfineWorkspacePath helper: if the SandboxScope wiring in core/read.go is
// ever dropped, this test starts passing again and catches it.
func TestReadTool_Handler_WorkspaceScope_BlocksAbsoluteEscape(t *testing.T) {
	dir := t.TempDir()
	secret := t.TempDir()
	secretFile := filepath.Join(secret, "id_rsa")
	if err := os.WriteFile(secretFile, []byte("private key material"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := ReadTool(tools.BuildOptions{WorkspaceRoot: dir, SandboxScope: tools.SandboxScopeWorkspace})
	args, _ := json.Marshal(map[string]string{"path": secretFile})
	if _, err := tool.Handler(context.Background(), args); err == nil {
		t.Fatal("expected read of an absolute path outside the workspace to be rejected under workspace sandbox scope")
	}

	// Legitimate in-workspace reads must still work under the same scope.
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"ok.txt"}`))
	if err != nil {
		t.Fatalf("expected legitimate in-workspace read to still succeed under workspace scope: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result for in-workspace read")
	}
}

// TestWriteTool_Definition verifies the write tool constructor.
func TestWriteTool_Definition(t *testing.T) {
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "write", tools.TierCore)
}

// TestWriteTool_Handler_MissingPath verifies write returns an error when path is empty.
func TestWriteTool_Handler_MissingPath(t *testing.T) {
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"content":"x"}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestWriteTool_Handler_MissingContent verifies write returns an error when content is missing.
func TestWriteTool_Handler_MissingContent(t *testing.T) {
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"foo.txt"}`))
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

// TestWriteTool_Handler_Success verifies write creates a file.
func TestWriteTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"out.txt","content":"hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

// TestWriteTool_Handler_WorkspaceScope_BlocksAbsoluteEscape is a regression
// test for BUG-1's write-side coverage through the PRODUCTION tool
// (core.WriteTool): an absolute path outside the workspace must be rejected
// under workspace sandbox scope, so an attacker cannot overwrite arbitrary
// host files (e.g. ~/.ssh/authorized_keys) via the write tool. Uses the same
// scope wiring as core.ReadTool but exercises the WRITE code path
// specifically, since read and write resolve paths independently.
func TestWriteTool_Handler_WorkspaceScope_BlocksAbsoluteEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	targetFile := filepath.Join(outside, "authorized_keys")
	if err := os.WriteFile(targetFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir, SandboxScope: tools.SandboxScopeWorkspace})
	args, _ := json.Marshal(map[string]string{"path": targetFile, "content": "attacker-controlled-key"})
	if _, err := tool.Handler(context.Background(), args); err == nil {
		t.Fatal("expected write to an absolute path outside the workspace to be rejected under workspace sandbox scope")
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("target file should be untouched: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("expected target file to remain unmodified, got %q", string(data))
	}
}

// TestWriteTool_Handler_ValidJSON verifies that writing valid JSON to a .json file succeeds.
func TestWriteTool_Handler_ValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	validJSON := `{"key": "value", "count": 42}`
	args, _ := json.Marshal(map[string]string{"path": "config.json", "content": validJSON})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error for valid JSON: %v", err)
	}
	// Must not contain an error field
	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("unexpected error in result: %s", result)
	}
	// File should be on disk and contain exactly the content passed.
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != validJSON {
		t.Errorf("file content mismatch:\n got  %q\n want %q", string(data), validJSON)
	}
}

// TestWriteTool_Handler_InvalidJSON verifies that writing invalid JSON to a .json file
// returns a structured error without writing the file.
func TestWriteTool_Handler_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	invalidJSON := `{"key": "value", "broken"`
	args, _ := json.Marshal(map[string]string{"path": "deploy/targets.json", "content": invalidJSON})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	// Must not be a hard error (the LLM should see the rejection).
	if err != nil {
		t.Fatalf("expected structured error result, got hard error: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	errObj, hasErr := m["error"]
	if !hasErr {
		t.Fatalf("expected 'error' key in result, got: %s", result)
	}
	errMap, ok := errObj.(map[string]any)
	if !ok {
		t.Fatalf("expected error to be an object, got: %T", errObj)
	}
	if errMap["code"] != "invalid_json" {
		t.Errorf("expected error code 'invalid_json', got: %v", errMap["code"])
	}
	// The file must NOT have been written.
	if _, statErr := os.Stat(filepath.Join(dir, "deploy/targets.json")); !os.IsNotExist(statErr) {
		t.Error("invalid JSON file should not be written to disk")
	}
}

// TestWriteTool_Handler_InvalidJSON_EscapedNewlines checks the specific failure mode
// from the issue: a model writing JSON with escaped newline sequences (\n as literal
// text) produces invalid JSON which must be rejected.
func TestWriteTool_Handler_InvalidJSON_EscapedNewlines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	// Simulate the corruption seen in Terminal Bench: the model emits literal
	// backslash-n sequences instead of actual newlines, which when embedded in a
	// JSON string value creates valid JSON — but the test here covers the case
	// where the model writes the JSON structure itself with literal \n outside
	// quotes (i.e., the surrounding JSON is malformed).
	malformedJSON := "{\n  \"targets\": [\n    \"prod\"\n  ]\n" // missing closing brace
	args, _ := json.Marshal(map[string]string{"path": "config.json", "content": malformedJSON})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("expected structured error, got hard error: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	if _, hasErr := m["error"]; !hasErr {
		t.Fatalf("expected error for malformed JSON file, got: %s", result)
	}
	// File must not be written.
	if _, statErr := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(statErr) {
		t.Error("malformed JSON file should not be written to disk")
	}
}

// TestWriteTool_Handler_NonJSONExtension verifies that non-JSON files bypass JSON validation.
func TestWriteTool_Handler_NonJSONExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	// This is invalid JSON, but the file is a .txt so no validation should occur.
	args, _ := json.Marshal(map[string]string{"path": "notes.txt", "content": `{broken`})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error for .txt file: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("unexpected error for .txt file: %s", result)
	}
}

// TestWriteTool_Handler_JSONArray verifies arrays are accepted as valid JSON content.
func TestWriteTool_Handler_JSONArray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	args, _ := json.Marshal(map[string]string{"path": "list.json", "content": `["a","b","c"]`})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error for JSON array: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("unexpected error for JSON array: %s", result)
	}
}

// TestWriteToolRejectsInvalidYAML verifies that writing invalid YAML to a .yaml file
// returns an error and does not create the file on disk.
func TestWriteToolRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	// Indentation error: mixed tabs and spaces, or a mapping key with wrong indent
	invalidYAML := "key: value\n  bad_indent: [unclosed"
	args, _ := json.Marshal(map[string]string{"path": "config.yaml", "content": invalidYAML})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("handler returned hard error (want soft error result): %v", err)
	}

	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	errMap, hasErr := m["error"].(map[string]any)
	if !hasErr {
		t.Fatalf("expected error in result for invalid YAML, got: %s", result)
	}
	if errMap["code"] != "invalid_yaml" {
		t.Errorf("expected error code 'invalid_yaml', got: %v", errMap["code"])
	}

	// File must not be written.
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(statErr) {
		t.Error("invalid YAML file should not be written to disk")
	}
}

// TestWriteToolRejectsInvalidYML verifies that .yml extension is also validated.
func TestWriteToolRejectsInvalidYML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	invalidYAML := ": bad_scalar\n  - not_a_list_item"
	args, _ := json.Marshal(map[string]string{"path": "deploy.yml", "content": invalidYAML})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("handler returned hard error (want soft error result): %v", err)
	}

	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	errMap, hasErr := m["error"].(map[string]any)
	if !hasErr {
		t.Fatalf("expected error in result for invalid YAML, got: %s", result)
	}
	if errMap["code"] != "invalid_yaml" {
		t.Errorf("expected error code 'invalid_yaml', got: %v", errMap["code"])
	}
	if _, statErr := os.Stat(filepath.Join(dir, "deploy.yml")); !os.IsNotExist(statErr) {
		t.Error("invalid YAML file should not be written to disk")
	}
}

// TestWriteToolAcceptsValidYAML verifies that valid YAML is written successfully.
func TestWriteToolAcceptsValidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	validYAML := "name: myapp\nversion: 1.2.3\nreplicas: 3\ntags:\n  - web\n  - api\n"
	args, _ := json.Marshal(map[string]string{"path": "values.yaml", "content": validYAML})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error for valid YAML: %v", err)
	}

	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("unexpected error for valid YAML: %s", result)
	}

	data, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if err != nil {
		t.Fatalf("file should exist after valid YAML write: %v", err)
	}
	if string(data) != validYAML {
		t.Errorf("file content mismatch:\n got  %q\n want %q", string(data), validYAML)
	}
}

// TestWriteToolNonStructuredPassesThrough verifies that non-structured files
// (e.g. .txt) bypass both JSON and YAML validation entirely.
func TestWriteToolNonStructuredPassesThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tool := WriteTool(tools.BuildOptions{WorkspaceRoot: dir})

	// This is invalid both as JSON and YAML, but the .txt extension must bypass validation.
	args, _ := json.Marshal(map[string]string{"path": "notes.txt", "content": "{broken: [unclosed"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error for .txt file: %v", err)
	}

	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(result), &m); jsonErr != nil {
		t.Fatalf("result is not JSON: %v", jsonErr)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("unexpected validation error for .txt file: %s", result)
	}
}

// TestEditTool_Definition verifies the edit tool constructor.
func TestEditTool_Definition(t *testing.T) {
	tool := EditTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "edit", tools.TierCore)
}

// TestEditTool_Handler_MissingPath verifies edit returns an error when path is empty.
func TestEditTool_Handler_MissingPath(t *testing.T) {
	tool := EditTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"old_text":"a","new_text":"b"}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestEditTool_Handler_Success verifies edit replaces text in a file.
func TestEditTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo bar"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := EditTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"f.txt","old_text":"foo","new_text":"baz"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "baz bar" {
		t.Errorf("expected 'baz bar', got %q", string(data))
	}
}

// TestGlobTool_Definition verifies the glob tool constructor.
func TestGlobTool_Definition(t *testing.T) {
	tool := GlobTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "glob", tools.TierCore)
}

// TestGlobTool_Handler_MissingPattern verifies glob returns an error when pattern is empty.
func TestGlobTool_Handler_MissingPattern(t *testing.T) {
	tool := GlobTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

// TestGlobTool_Handler_Success verifies glob finds files.
func TestGlobTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644)
	tool := GlobTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestGrepTool_Definition verifies the grep tool constructor.
func TestGrepTool_Definition(t *testing.T) {
	tool := GrepTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "grep", tools.TierCore)
}

// TestGrepTool_Handler_MissingQuery verifies grep returns an error when query is empty.
func TestGrepTool_Handler_MissingQuery(t *testing.T) {
	tool := GrepTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// TestGrepTool_Handler_Success verifies grep finds matching text.
func TestGrepTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.txt"), []byte("needle in haystack"), 0o644)
	tool := GrepTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"needle"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestLsTool_Definition verifies the ls tool constructor.
func TestLsTool_Definition(t *testing.T) {
	tool := LsTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "ls", tools.TierCore)
}

// TestLsTool_Handler_Success verifies ls lists directory contents.
func TestLsTool_Handler_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte(""), 0o644)
	tool := LsTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestGitStatusTool_Definition verifies the git_status tool constructor.
func TestGitStatusTool_Definition(t *testing.T) {
	tool := GitStatusTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_status", tools.TierCore)
}

// TestGitDiffTool_Definition verifies the git_diff tool constructor.
func TestGitDiffTool_Definition(t *testing.T) {
	tool := GitDiffTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_diff", tools.TierCore)
}

// TestApplyPatchTool_Definition verifies the apply_patch tool constructor.
func TestApplyPatchTool_Definition(t *testing.T) {
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "apply_patch", tools.TierCore)
}

// TestApplyPatchTool_Handler_MissingPath verifies apply_patch returns an error when path is empty.
func TestApplyPatchTool_Handler_MissingPath(t *testing.T) {
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"find":"x","replace":"y"}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestApplyPatchTool_Handler_FindReplace verifies apply_patch find/replace mode.
func TestApplyPatchTool_Handler_FindReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "patch.txt"), []byte("hello world"), 0o644)
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"patch.txt","find":"hello","replace":"goodbye"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "patch.txt"))
	if string(data) != "goodbye world" {
		t.Errorf("expected 'goodbye world', got %q", string(data))
	}
}

// TestApplyPatchTool_Handler_UnifiedPatch verifies apply_patch unified patch mode.
func TestApplyPatchTool_Handler_UnifiedPatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "u.txt"), []byte("line1\nline2\nline3\n"), 0o644)
	patch := `*** Begin Patch
*** Update File: u.txt
@@ context
 line1
-line2
+lineXX
 line3
*** End Patch`
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args, _ := json.Marshal(map[string]string{"patch": patch})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestApplyPatchTool_Handler_MultiEdit verifies apply_patch multi-edit mode.
func TestApplyPatchTool_Handler_MultiEdit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "me.txt"), []byte("aaa bbb ccc"), 0o644)
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args := `{"path":"me.txt","edits":[{"old_text":"aaa","new_text":"xxx"},{"old_text":"ccc","new_text":"zzz"}]}`
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "me.txt"))
	if string(data) != "xxx bbb zzz" {
		t.Errorf("expected 'xxx bbb zzz', got %q", string(data))
	}
}

// TestApplyPatchTool_Handler_StandardUnifiedDiff verifies apply_patch accepts standard
// unified diff format (--- a/file / +++ b/file) as produced by git diff and most models.
func TestApplyPatchTool_Handler_StandardUnifiedDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	patch := "--- a/file.txt\n+++ b/file.txt\n@@ -1,3 +1,3 @@\n line1\n-line2\n+lineXX\n line3\n"
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(data) != "line1\nlineXX\nline3\n" {
		t.Errorf("unexpected file content after patch: %q", string(data))
	}
}

// TestApplyPatchTool_Handler_StandardUnifiedDiff_MultipleHunks verifies apply_patch
// handles multiple hunks in a single standard unified diff.
func TestApplyPatchTool_Handler_StandardUnifiedDiff_MultipleHunks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	if err := os.WriteFile(filepath.Join(dir, "multi.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	// Two hunks: change "beta" and "delta"
	patch := "--- a/multi.txt\n+++ b/multi.txt\n" +
		"@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n" +
		"@@ -3,3 +3,3 @@\n gamma\n-delta\n+DELTA\n epsilon\n"
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, err := os.ReadFile(filepath.Join(dir, "multi.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want := "alpha\nBETA\ngamma\nDELTA\nepsilon\n"
	if string(data) != want {
		t.Errorf("unexpected file content:\ngot  %q\nwant %q", string(data), want)
	}
}

// TestApplyPatchTool_Handler_StandardUnifiedDiff_NewFile verifies apply_patch
// can create a new file via standard unified diff (--- /dev/null / +++ b/file).
func TestApplyPatchTool_Handler_StandardUnifiedDiff_NewFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	patch := "--- /dev/null\n+++ b/newfile.txt\n@@ -0,0 +1,2 @@\n+hello\n+world\n"
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, err := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(data) != "hello\nworld\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

// TestApplyPatchTool_Handler_StandardUnifiedDiff_MultipleFiles verifies apply_patch
// can patch multiple files in a single standard unified diff.
func TestApplyPatchTool_Handler_StandardUnifiedDiff_MultipleFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("foo\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bar\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-foo\n+FOO\n" +
		"--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-bar\n+BAR\n"
	tool := ApplyPatchTool(tools.BuildOptions{WorkspaceRoot: dir})
	args, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	dataA, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(dataA) != "FOO\n" {
		t.Errorf("a.txt: expected 'FOO\\n', got %q", string(dataA))
	}
	dataB, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(dataB) != "BAR\n" {
		t.Errorf("b.txt: expected 'BAR\\n', got %q", string(dataB))
	}
}

// TestParseStandardUnifiedDiff_BasicHunk verifies parseStandardUnifiedDiff handles a simple hunk.
func TestParseStandardUnifiedDiff_BasicHunk(t *testing.T) {
	t.Parallel()
	patch := "--- a/foo.txt\n+++ b/foo.txt\n@@ -1,3 +1,3 @@\n context\n-old\n+new\n context2\n"
	files, err := parseStandardUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "foo.txt" {
		t.Errorf("expected path 'foo.txt', got %q", files[0].Path)
	}
	if files[0].Kind != "update" {
		t.Errorf("expected kind 'update', got %q", files[0].Kind)
	}
}

// TestParseStandardUnifiedDiff_NewFile verifies parseStandardUnifiedDiff detects new files.
func TestParseStandardUnifiedDiff_NewFile(t *testing.T) {
	t.Parallel()
	patch := "--- /dev/null\n+++ b/newfile.txt\n@@ -0,0 +1 @@\n+content\n"
	files, err := parseStandardUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Kind != "add" {
		t.Errorf("expected kind 'add', got %q", files[0].Kind)
	}
	if files[0].Path != "newfile.txt" {
		t.Errorf("expected path 'newfile.txt', got %q", files[0].Path)
	}
}

// TestParseStandardUnifiedDiff_DeleteFile verifies parseStandardUnifiedDiff detects deletions.
func TestParseStandardUnifiedDiff_DeleteFile(t *testing.T) {
	t.Parallel()
	patch := "--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-line1\n-line2\n"
	files, err := parseStandardUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Kind != "delete" {
		t.Errorf("expected kind 'delete', got %q", files[0].Kind)
	}
}

// TestBashTool_Definition verifies the bash tool constructor.
func TestBashTool_Definition(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := BashTool(jm)
	assertToolDef(t, tool, "bash", tools.TierCore)
}

// TestBashTool_Handler_EmptyCommand verifies bash returns an error for empty command.
func TestBashTool_Handler_EmptyCommand(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := BashTool(jm)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"command":""}`))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// TestBashTool_Handler_Success verifies bash runs a simple command.
func TestBashTool_Handler_Success(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := BashTool(jm)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestBashTool_StreamingOutputDelta verifies that BashTool emits incremental streaming chunks
// via the OutputStreamer from context when a multi-line command runs.
// This is the integration test for issue #1: streaming bash output line-by-line.
func TestBashTool_StreamingOutputDelta(t *testing.T) {
	t.Parallel()

	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := BashTool(jm)

	var mu sync.Mutex
	type chunk struct {
		content string
		index   int
	}
	var chunks []chunk
	idx := 0
	streamer := func(c string) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk{content: c, index: idx})
		idx++
	}

	ctx := context.WithValue(context.Background(), tools.ContextKeyOutputStreamer, (func(string))(streamer))

	result, err := tool.Handler(ctx, json.RawMessage(`{"command":"printf 'line1\\nline2\\nline3\\n'","timeout_seconds":5}`))
	if err != nil {
		t.Fatalf("BashTool handler error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Verify full output is still present in the result.
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line2") || !strings.Contains(result, "line3") {
		t.Errorf("full output missing expected lines; result: %q", result)
	}

	// Verify streaming chunks were received.
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 streaming chunks for 3 lines, got %d: %v", len(chunks), chunks)
	}

	combined := ""
	for _, ch := range chunks {
		combined += ch.content
	}
	if !strings.Contains(combined, "line1") || !strings.Contains(combined, "line2") || !strings.Contains(combined, "line3") {
		t.Errorf("streaming chunks missing expected content; combined: %q", combined)
	}
}

// TestBashTool_StreamingNoStreamer verifies that BashTool works correctly when no
// OutputStreamer is in the context (backward compatibility).
func TestBashTool_StreamingNoStreamer(t *testing.T) {
	t.Parallel()

	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := BashTool(jm)

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"command":"echo nostream","timeout_seconds":5}`))
	if err != nil {
		t.Fatalf("BashTool handler error: %v", err)
	}
	if !strings.Contains(result, "nostream") {
		t.Errorf("expected result to contain 'nostream', got: %q", result)
	}
}

// TestJobOutputTool_Definition verifies the job_output tool constructor.
func TestJobOutputTool_Definition(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := JobOutputTool(jm)
	assertToolDef(t, tool, "job_output", tools.TierCore)
}

// TestJobOutputTool_Handler_MissingShellID verifies job_output returns an error when shell_id is empty.
func TestJobOutputTool_Handler_MissingShellID(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := JobOutputTool(jm)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing shell_id")
	}
}

// TestJobKillTool_Definition verifies the job_kill tool constructor.
func TestJobKillTool_Definition(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := JobKillTool(jm)
	assertToolDef(t, tool, "job_kill", tools.TierCore)
}

// TestJobKillTool_Handler_MissingShellID verifies job_kill returns an error when shell_id is empty.
func TestJobKillTool_Handler_MissingShellID(t *testing.T) {
	jm := tools.NewJobManager(t.TempDir(), nil)
	tool := JobKillTool(jm)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing shell_id")
	}
}

// TestAskUserQuestionTool_Definition verifies the ask_user_question tool constructor.
func TestAskUserQuestionTool_Definition(t *testing.T) {
	tool := AskUserQuestionTool(nil, 30*time.Second)
	assertToolDef(t, tool, tools.AskUserQuestionToolName, tools.TierCore)
}

// TestAskUserQuestionTool_Handler_NilBroker verifies ask_user_question returns an error when broker is nil.
func TestAskUserQuestionTool_Handler_NilBroker(t *testing.T) {
	tool := AskUserQuestionTool(nil, 30*time.Second)
	args := `{"questions":[{"question":"What?","header":"H","options":[{"label":"A","description":"a"},{"label":"B","description":"b"}],"multiSelect":false}]}`
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for nil broker")
	}
}

// TestObservationalMemoryTool_Definition verifies the observational_memory tool constructor.
func TestObservationalMemoryTool_Definition(t *testing.T) {
	tool := ObservationalMemoryTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "observational_memory", tools.TierCore)
}

// TestObservationalMemoryTool_Handler_MissingAction verifies observational_memory returns an error when action is empty.
func TestObservationalMemoryTool_Handler_MissingAction(t *testing.T) {
	tool := ObservationalMemoryTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

// ---------- observational_memory helper functions ----------

// TestConfigFromArgs_Nil verifies configFromArgs returns nil when input is nil.
func TestConfigFromArgs_Nil(t *testing.T) {
	result := configFromArgs(nil)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

// TestConfigFromArgs_NonNil verifies configFromArgs returns a populated config.
func TestConfigFromArgs_NonNil(t *testing.T) {
	input := &struct {
		ObserveMinTokens       int `json:"observe_min_tokens"`
		SnippetMaxTokens       int `json:"snippet_max_tokens"`
		ReflectThresholdTokens int `json:"reflect_threshold_tokens"`
	}{
		ObserveMinTokens:       100,
		SnippetMaxTokens:       500,
		ReflectThresholdTokens: 1000,
	}
	result := configFromArgs(input)
	if result == nil {
		t.Fatal("expected non-nil config")
	}
	if result.ObserveMinTokens != 100 {
		t.Errorf("expected ObserveMinTokens=100, got %d", result.ObserveMinTokens)
	}
	if result.SnippetMaxTokens != 500 {
		t.Errorf("expected SnippetMaxTokens=500, got %d", result.SnippetMaxTokens)
	}
	if result.ReflectThresholdTokens != 1000 {
		t.Errorf("expected ReflectThresholdTokens=1000, got %d", result.ReflectThresholdTokens)
	}
}

// TestMemoryScopeFromMetadata_Defaults verifies memoryScopeFromMetadata fills defaults.
func TestMemoryScopeFromMetadata_Defaults(t *testing.T) {
	scope := memoryScopeFromMetadata("run-123", tools.RunMetadata{})
	if scope.TenantID != "default" {
		t.Errorf("expected TenantID='default', got %q", scope.TenantID)
	}
	if scope.AgentID != "default" {
		t.Errorf("expected AgentID='default', got %q", scope.AgentID)
	}
	if scope.ConversationID != "run-123" {
		t.Errorf("expected ConversationID='run-123', got %q", scope.ConversationID)
	}
}

// TestMemoryScopeFromMetadata_Provided verifies memoryScopeFromMetadata uses provided values.
func TestMemoryScopeFromMetadata_Provided(t *testing.T) {
	md := tools.RunMetadata{
		TenantID:       "t1",
		ConversationID: "c1",
		AgentID:        "a1",
	}
	scope := memoryScopeFromMetadata("run-123", md)
	if scope.TenantID != "t1" {
		t.Errorf("expected TenantID='t1', got %q", scope.TenantID)
	}
	if scope.AgentID != "a1" {
		t.Errorf("expected AgentID='a1', got %q", scope.AgentID)
	}
	if scope.ConversationID != "c1" {
		t.Errorf("expected ConversationID='c1', got %q", scope.ConversationID)
	}
}

// TestSanitizePathPart verifies sanitizePathPart normalizes path parts.
func TestSanitizePathPart(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "default"},
		{"  ", "default"},
		{"hello", "hello"},
		{"foo/bar", "foo-bar"},
		{"a..b", "a-b"},
		{"a b c", "a-b-c"},
	}
	for _, tt := range tests {
		got := sanitizePathPart(tt.input)
		if got != tt.want {
			t.Errorf("sanitizePathPart(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ========== file_inspect tool tests ==========

// TestFileInspectTool_Definition verifies the file_inspect tool constructor returns a valid tool.
func TestFileInspectTool_Definition(t *testing.T) {
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "file_inspect", tools.TierCore)
}

// TestFileInspectTool_Handler_MissingPath verifies file_inspect returns an error when path is empty.
func TestFileInspectTool_Handler_MissingPath(t *testing.T) {
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestFileInspectTool_Handler_NonexistentFile verifies file_inspect returns an error for a file that doesn't exist.
func TestFileInspectTool_Handler_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"does_not_exist.txt"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// TestFileInspectTool_Handler_PathEscape verifies file_inspect returns an error when path escapes workspace.
func TestFileInspectTool_Handler_PathEscape(t *testing.T) {
	dir := t.TempDir()
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"../../etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error for path escape")
	}
}

// TestFileInspectTool_Handler_DirectoryPath verifies file_inspect returns an error when path points to a directory.
func TestFileInspectTool_Handler_DirectoryPath(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"subdir"}`))
	if err == nil {
		t.Fatal("expected error for directory path")
	}
}

// TestFileInspectTool_Handler_TextFile verifies file_inspect returns correct metadata for a text file.
func TestFileInspectTool_Handler_TextFile(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify path
	if p, ok := m["path"].(string); !ok || p != "hello.txt" {
		t.Errorf("expected path 'hello.txt', got %v", m["path"])
	}

	// Verify size_bytes matches content length
	if sz, ok := m["size_bytes"].(float64); !ok || int(sz) != len(content) {
		t.Errorf("expected size_bytes=%d, got %v", len(content), m["size_bytes"])
	}

	// Verify encoding is utf-8
	if enc, ok := m["encoding"].(string); !ok || enc != "utf-8" {
		t.Errorf("expected encoding 'utf-8', got %v", m["encoding"])
	}

	// Verify mime_type contains "text"
	if mime, ok := m["mime_type"].(string); !ok || !strings.Contains(mime, "text") {
		t.Errorf("expected mime_type containing 'text', got %v", m["mime_type"])
	}

	// Verify preview contains lines from the file
	if preview, ok := m["preview"].(string); !ok || !strings.Contains(preview, "line one") {
		t.Errorf("expected preview to contain 'line one', got %v", m["preview"])
	}

	// Verify total_lines is correct (3 non-empty lines + trailing empty = 4 total split lines, or 3 content lines)
	if totalLines, ok := m["total_lines"].(float64); !ok || int(totalLines) < 3 {
		t.Errorf("expected total_lines >= 3, got %v", m["total_lines"])
	}
}

// TestFileInspectTool_Handler_BinaryFile verifies file_inspect detects binary content correctly.
func TestFileInspectTool_Handler_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	// PNG magic bytes followed by binary data
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), binaryData, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"image.png"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify encoding is binary
	if enc, ok := m["encoding"].(string); !ok || enc != "binary" {
		t.Errorf("expected encoding 'binary', got %v", m["encoding"])
	}

	// Verify hex_preview is non-empty
	if hexPreview, ok := m["hex_preview"].(string); !ok || hexPreview == "" {
		t.Errorf("expected non-empty hex_preview, got %v", m["hex_preview"])
	}

	// Verify preview is null/empty for binary files
	if preview, ok := m["preview"].(string); ok && preview != "" {
		t.Errorf("expected empty or absent preview for binary file, got %q", preview)
	}
}

// TestFileInspectTool_Handler_EmptyFile verifies file_inspect handles 0-byte files.
func TestFileInspectTool_Handler_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"empty.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify size_bytes is 0
	if sz, ok := m["size_bytes"].(float64); !ok || int(sz) != 0 {
		t.Errorf("expected size_bytes=0, got %v", m["size_bytes"])
	}
}

// TestFileInspectTool_Handler_LargeFile verifies file_inspect truncates and warns for large files.
func TestFileInspectTool_Handler_LargeFile(t *testing.T) {
	dir := t.TempDir()
	// Create a file with many lines (more than a typical preview limit)
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("This is a line of text to create a large file for testing.\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"large.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify truncation_warning is set
	if tw, ok := m["truncation_warning"].(string); !ok || tw == "" {
		t.Errorf("expected non-empty truncation_warning for large file, got %v", m["truncation_warning"])
	}
}

// TestFileInspectTool_Handler_PreviewLinesParam verifies custom preview_lines parameter works.
func TestFileInspectTool_Handler_PreviewLinesParam(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "multi.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"multi.txt","preview_lines":3}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify preview has at most 3 lines
	if preview, ok := m["preview"].(string); ok {
		lines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
		if len(lines) > 3 {
			t.Errorf("expected at most 3 preview lines, got %d", len(lines))
		}
	} else {
		t.Error("expected preview string in result")
	}
}

// TestFileInspectTool_Handler_HexBytesParam verifies custom hex_bytes parameter works.
func TestFileInspectTool_Handler_HexBytesParam(t *testing.T) {
	dir := t.TempDir()
	// Binary file with known content
	binaryData := make([]byte, 64)
	for i := range binaryData {
		binaryData[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), binaryData, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"data.bin","hex_bytes":8}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify hex_preview is present and limited.
	// hex.Dump produces "00000000  00 01 02 03 04 05 06 07  |........|\n" format.
	// With hex_bytes=8, only one line should appear (16 bytes per line in hex.Dump).
	if hexPreview, ok := m["hex_preview"].(string); !ok || hexPreview == "" {
		t.Errorf("expected non-empty hex_preview, got %v", m["hex_preview"])
	} else {
		lines := strings.Split(strings.TrimSpace(hexPreview), "\n")
		if len(lines) > 1 {
			t.Errorf("hex_preview for hex_bytes=8 should be at most 1 line, got %d lines", len(lines))
		}
		// Verify the hex dump contains our expected bytes.
		if !strings.Contains(hexPreview, "00 01 02 03 04 05 06 07") {
			t.Errorf("hex_preview doesn't contain expected byte sequence: %q", hexPreview)
		}
	}
}

// TestFileInspectTool_Handler_NoExtension verifies file_inspect handles files without extensions.
func TestFileInspectTool_Handler_NoExtension(t *testing.T) {
	dir := t.TempDir()
	content := "#!/bin/bash\necho hello\n"
	if err := os.WriteFile(filepath.Join(dir, "myscript"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"myscript"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify mime_type is present (should detect as text even without extension)
	if mime, ok := m["mime_type"].(string); !ok || mime == "" {
		t.Errorf("expected non-empty mime_type for file without extension, got %v", m["mime_type"])
	}

	// Verify encoding is text-based (not binary)
	if enc, ok := m["encoding"].(string); !ok || enc != "utf-8" {
		t.Errorf("expected encoding 'utf-8' for text file without extension, got %v", m["encoding"])
	}
}

// TestFileInspectTool_Handler_Symlink verifies file_inspect follows symlinks.
func TestFileInspectTool_Handler_Symlink(t *testing.T) {
	dir := t.TempDir()
	content := "symlink target content\n"
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "target.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("symlinks not supported on this platform")
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"link.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Verify size_bytes matches the target file's content length
	if sz, ok := m["size_bytes"].(float64); !ok || int(sz) != len(content) {
		t.Errorf("expected size_bytes=%d, got %v", len(content), m["size_bytes"])
	}

	// Verify preview contains the target's content
	if preview, ok := m["preview"].(string); !ok || !strings.Contains(preview, "symlink target content") {
		t.Errorf("expected preview to contain 'symlink target content', got %v", m["preview"])
	}
}

// TestFileInspectTool_Handler_ConcurrentAccess verifies file_inspect is safe under concurrent access.
func TestFileInspectTool_Handler_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	// Create several test files
	for i := 0; i < 10; i++ {
		fname := filepath.Join(dir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(fname, []byte(fmt.Sprintf("content of file %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := FileInspectTool(tools.BuildOptions{WorkspaceRoot: dir})

	const goroutines = 20
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			fileNum := idx % 10
			arg := fmt.Sprintf(`{"path":"file%d.txt"}`, fileNum)
			result, err := tool.Handler(context.Background(), json.RawMessage(arg))
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", idx, err)
				return
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(result), &m); err != nil {
				errs <- fmt.Errorf("goroutine %d: parse error: %w", idx, err)
				return
			}
			errs <- nil
		}(g)
	}
	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// assertToolDef is a test helper that checks a tool has the expected name, tier, and a non-nil handler.
func assertToolDef(t *testing.T, tool tools.Tool, expectedName string, expectedTier tools.ToolTier) {
	t.Helper()
	if tool.Definition.Name != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, tool.Definition.Name)
	}
	if tool.Definition.Tier != expectedTier {
		t.Errorf("expected tier %q, got %q", expectedTier, tool.Definition.Tier)
	}
	if tool.Handler == nil {
		t.Error("handler is nil")
	}
	if tool.Definition.Parameters == nil {
		t.Error("parameters is nil")
	}
}

// ---------- mock ConversationReader for conversation tool tests ----------

type mockConversationReader struct {
	conversations []tools.ConversationSummary
	searchResults []tools.ConversationSearchResult
	searchErr     error
	listErr       error
}

func (m *mockConversationReader) ListConversations(_ context.Context, limit, offset int) ([]tools.ConversationSummary, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	start := offset
	if start >= len(m.conversations) {
		return []tools.ConversationSummary{}, nil
	}
	end := start + limit
	if end > len(m.conversations) {
		end = len(m.conversations)
	}
	return m.conversations[start:end], nil
}

func (m *mockConversationReader) SearchConversations(_ context.Context, query string, limit int) ([]tools.ConversationSearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	_ = query
	_ = limit
	return m.searchResults, nil
}

// ---------- list_conversations tests ----------

// TestListConversationsTool_Definition verifies the list_conversations tool constructor.
func TestListConversationsTool_Definition(t *testing.T) {
	mock := &mockConversationReader{}
	tool := ListConversationsTool(mock)
	assertToolDef(t, tool, "list_conversations", tools.TierCore)
}

// TestListConversationsTool_ParallelSafe verifies list_conversations is parallel-safe and read-only.
func TestListConversationsTool_ParallelSafe(t *testing.T) {
	mock := &mockConversationReader{}
	tool := ListConversationsTool(mock)
	if !tool.Definition.ParallelSafe {
		t.Error("expected list_conversations to be parallel-safe")
	}
	if tool.Definition.Mutating {
		t.Error("expected list_conversations to be non-mutating")
	}
}

// TestListConversationsTool_Handler_NilStore verifies list_conversations returns an error when store is nil.
func TestListConversationsTool_Handler_NilStore(t *testing.T) {
	tool := ListConversationsTool(nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when conversation store is nil")
	}
}

// TestListConversationsTool_Handler_Success verifies list_conversations returns conversation metadata.
func TestListConversationsTool_Handler_Success(t *testing.T) {
	mock := &mockConversationReader{
		conversations: []tools.ConversationSummary{
			{ID: "conv-1", Title: "First", CreatedAt: "2024-01-01T00:00:00Z", UpdatedAt: "2024-01-02T00:00:00Z", MsgCount: 5},
			{ID: "conv-2", Title: "Second", CreatedAt: "2024-01-03T00:00:00Z", UpdatedAt: "2024-01-04T00:00:00Z", MsgCount: 3},
		},
	}
	tool := ListConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "conv-1") {
		t.Errorf("expected conv-1 in result, got: %s", result)
	}
	if !strings.Contains(result, "conv-2") {
		t.Errorf("expected conv-2 in result, got: %s", result)
	}
}

// TestListConversationsTool_Handler_EmptyStore verifies list_conversations handles empty store.
func TestListConversationsTool_Handler_EmptyStore(t *testing.T) {
	mock := &mockConversationReader{
		conversations: []tools.ConversationSummary{},
	}
	tool := ListConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestListConversationsTool_Handler_CustomLimitOffset verifies list_conversations accepts limit/offset.
func TestListConversationsTool_Handler_CustomLimitOffset(t *testing.T) {
	var conversations []tools.ConversationSummary
	for i := 0; i < 10; i++ {
		conversations = append(conversations, tools.ConversationSummary{
			ID:       fmt.Sprintf("conv-%d", i),
			MsgCount: i,
		})
	}
	mock := &mockConversationReader{conversations: conversations}
	tool := ListConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"limit":3,"offset":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should contain conv-2, conv-3, conv-4 (offset 2, limit 3)
	if !strings.Contains(result, "conv-2") {
		t.Errorf("expected conv-2 in paginated result, got: %s", result)
	}
}

// TestListConversationsTool_Handler_StoreError verifies list_conversations propagates store errors.
func TestListConversationsTool_Handler_StoreError(t *testing.T) {
	mock := &mockConversationReader{
		listErr: fmt.Errorf("database unavailable"),
	}
	tool := ListConversationsTool(mock)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when store returns error")
	}
}

// ---------- search_conversations tests ----------

// TestSearchConversationsTool_Definition verifies the search_conversations tool constructor.
func TestSearchConversationsTool_Definition(t *testing.T) {
	mock := &mockConversationReader{}
	tool := SearchConversationsTool(mock)
	assertToolDef(t, tool, "search_conversations", tools.TierCore)
}

// TestSearchConversationsTool_ParallelSafe verifies search_conversations is parallel-safe and read-only.
func TestSearchConversationsTool_ParallelSafe(t *testing.T) {
	mock := &mockConversationReader{}
	tool := SearchConversationsTool(mock)
	if !tool.Definition.ParallelSafe {
		t.Error("expected search_conversations to be parallel-safe")
	}
	if tool.Definition.Mutating {
		t.Error("expected search_conversations to be non-mutating")
	}
}

// TestSearchConversationsTool_Handler_NilStore verifies search_conversations returns error when store is nil.
func TestSearchConversationsTool_Handler_NilStore(t *testing.T) {
	tool := SearchConversationsTool(nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"hello"}`))
	if err == nil {
		t.Fatal("expected error when conversation store is nil")
	}
}

// TestSearchConversationsTool_Handler_MissingQuery verifies search_conversations requires a query.
func TestSearchConversationsTool_Handler_MissingQuery(t *testing.T) {
	mock := &mockConversationReader{}
	tool := SearchConversationsTool(mock)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// TestSearchConversationsTool_Handler_Success verifies search_conversations returns snippets.
func TestSearchConversationsTool_Handler_Success(t *testing.T) {
	mock := &mockConversationReader{
		searchResults: []tools.ConversationSearchResult{
			{ConversationID: "conv-1", Role: "user", Snippet: "hello world needle here"},
			{ConversationID: "conv-2", Role: "assistant", Snippet: "responding to needle query"},
		},
	}
	tool := SearchConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"needle"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "conv-1") {
		t.Errorf("expected conv-1 in result, got: %s", result)
	}
	if !strings.Contains(result, "needle") {
		t.Errorf("expected snippet content in result, got: %s", result)
	}
}

// TestSearchConversationsTool_Handler_NoResults verifies search_conversations handles empty results.
func TestSearchConversationsTool_Handler_NoResults(t *testing.T) {
	mock := &mockConversationReader{
		searchResults: []tools.ConversationSearchResult{},
	}
	tool := SearchConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"nonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestSearchConversationsTool_Handler_StoreError verifies search_conversations propagates store errors.
func TestSearchConversationsTool_Handler_StoreError(t *testing.T) {
	mock := &mockConversationReader{
		searchErr: fmt.Errorf("fts index unavailable"),
	}
	tool := SearchConversationsTool(mock)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"anything"}`))
	if err == nil {
		t.Fatal("expected error when store returns error")
	}
}

// TestSearchConversationsTool_Handler_CustomLimit verifies search_conversations accepts a custom limit.
func TestSearchConversationsTool_Handler_CustomLimit(t *testing.T) {
	mock := &mockConversationReader{
		searchResults: []tools.ConversationSearchResult{
			{ConversationID: "conv-1", Role: "user", Snippet: "snippet one"},
		},
	}
	tool := SearchConversationsTool(mock)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"snippet","limit":5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}
