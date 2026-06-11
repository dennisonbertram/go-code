package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadWriteEditTools(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	writeOut, err := registry.Execute(context.Background(), "write", []byte(`{"path":"notes.txt","content":"hello world"}`))
	if err != nil {
		t.Fatalf("write tool failed: %v", err)
	}
	var writeResult struct {
		BytesWritten int `json:"bytes_written"`
	}
	if err := json.Unmarshal([]byte(writeOut), &writeResult); err != nil {
		t.Fatalf("unmarshal write output: %v", err)
	}
	if writeResult.BytesWritten == 0 {
		t.Fatalf("expected bytes written")
	}

	readOut, err := registry.Execute(context.Background(), "read", []byte(`{"path":"notes.txt"}`))
	if err != nil {
		t.Fatalf("read tool failed: %v", err)
	}
	var readResult struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(readOut), &readResult); err != nil {
		t.Fatalf("unmarshal read output: %v", err)
	}
	if readResult.Content != "hello world" {
		t.Fatalf("unexpected read content: %q", readResult.Content)
	}

	editOut, err := registry.Execute(context.Background(), "edit", []byte(`{"path":"notes.txt","old_text":"world","new_text":"agent"}`))
	if err != nil {
		t.Fatalf("edit tool failed: %v", err)
	}
	var editResult struct {
		Replacements int `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(editOut), &editResult); err != nil {
		t.Fatalf("unmarshal edit output: %v", err)
	}
	if editResult.Replacements != 1 {
		t.Fatalf("expected 1 replacement, got %d", editResult.Replacements)
	}

	readEditedOut, err := registry.Execute(context.Background(), "read", []byte(`{"path":"notes.txt"}`))
	if err != nil {
		t.Fatalf("read edited file failed: %v", err)
	}
	if err := json.Unmarshal([]byte(readEditedOut), &readResult); err != nil {
		t.Fatalf("unmarshal read edited output: %v", err)
	}
	if readResult.Content != "hello agent" {
		t.Fatalf("unexpected edited content: %q", readResult.Content)
	}

	if _, err := registry.Execute(context.Background(), "read", []byte(`{"path":"../secret.txt"}`)); err == nil {
		t.Fatalf("expected workspace boundary error for read")
	}
	if _, err := registry.Execute(context.Background(), "write", []byte(`{"path":"../secret.txt","content":"x"}`)); err == nil {
		t.Fatalf("expected workspace boundary error for write")
	}
	if _, err := registry.Execute(context.Background(), "edit", []byte(`{"path":"../secret.txt","old_text":"x","new_text":"y"}`)); err == nil {
		t.Fatalf("expected workspace boundary error for edit")
	}
}

func TestEditToolFailsWhenTargetMissing(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	registry := NewDefaultRegistry(workspace)
	if _, err := registry.Execute(context.Background(), "edit", []byte(`{"path":"notes.txt","old_text":"beta","new_text":"gamma"}`)); err == nil {
		t.Fatalf("expected missing target error")
	}
}

func TestApplyPatchToolAcceptsUnifiedPatchPayload(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "retry.go"), []byte("package retry\n\nfunc schedule() string {\n\treturn \"old\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	registry := NewDefaultRegistry(workspace)
	patch := `{"patch":"*** Begin Patch\n*** Update File: retry.go\n@@\n-package retry\n-\n-func schedule() string {\n-\treturn \"old\"\n-}\n+package retry\n+\n+func schedule() string {\n+\treturn \"new\"\n+}\n*** End Patch"}`
	if _, err := registry.Execute(context.Background(), "apply_patch", []byte(patch)); err != nil {
		t.Fatalf("apply_patch unified diff failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "retry.go"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if !strings.Contains(string(content), `"new"`) {
		t.Fatalf("expected updated file content, got %q", string(content))
	}
}

func TestWriteToolAcceptsContentAliases(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	if _, err := registry.Execute(context.Background(), "write", []byte(`{"path":"notes.txt","new_text":"hello alias"}`)); err != nil {
		t.Fatalf("write tool with new_text failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello alias" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestBashTool(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	out, err := registry.Execute(context.Background(), "bash", []byte(`{"command":"printf 'ok'","timeout_seconds":10}`))
	if err != nil {
		t.Fatalf("bash tool failed: %v", err)
	}
	var bashResult struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal([]byte(out), &bashResult); err != nil {
		t.Fatalf("unmarshal bash output: %v", err)
	}
	if bashResult.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", bashResult.ExitCode)
	}
	if bashResult.Output != "ok" {
		t.Fatalf("unexpected output: %q", bashResult.Output)
	}

	out, err = registry.Execute(context.Background(), "bash", []byte(`{"command":"exit 7"}`))
	if err != nil {
		t.Fatalf("bash non-zero run failed unexpectedly: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &bashResult); err != nil {
		t.Fatalf("unmarshal bash non-zero output: %v", err)
	}
	if bashResult.ExitCode != 7 {
		t.Fatalf("expected exit 7, got %d", bashResult.ExitCode)
	}

	if _, err := registry.Execute(context.Background(), "bash", []byte(`{"command":"rm -rf /"}`)); err == nil {
		t.Fatalf("expected dangerous command rejection")
	}
}


// TestDefaultRegistryWriteRejectsUnrealWorkspaceRoot verifies that a default registry
// built against a workspace root that does not exist on the filesystem rejects write
// calls with a clear error rather than silently creating the missing root and landing
// files in the wrong place.
func TestDefaultRegistryWriteRejectsUnrealWorkspaceRoot(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	registry := NewDefaultRegistry(missing)

	_, err := registry.Execute(context.Background(), "write", []byte(`{"path":"test.txt","content":"hello"}`))
	if err == nil {
		t.Fatal("expected error when workspace root does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q should mention missing root", err.Error())
	}
}

func TestBashToolOutputUsesHeadTailBuffer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	cmd := `{"command":"i=0; while [ $i -lt 5000 ]; do printf 'line-%04d\\n' $i; i=$((i+1)); done","timeout_seconds":10}`
	out, err := registry.Execute(context.Background(), "bash", []byte(cmd))
	if err != nil {
		t.Fatalf("bash tool failed: %v", err)
	}

	var bashResult struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal([]byte(out), &bashResult); err != nil {
		t.Fatalf("unmarshal bash output: %v", err)
	}
	if bashResult.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", bashResult.ExitCode)
	}
	if !strings.Contains(bashResult.Output, "line-0000") {
		t.Fatalf("expected head content to be preserved")
	}
	if !strings.Contains(bashResult.Output, "line-4999") {
		t.Fatalf("expected tail content to be preserved")
	}
	if strings.Contains(bashResult.Output, "line-2500") {
		t.Fatalf("expected middle content to be omitted")
	}
	if !strings.Contains(bashResult.Output, "[truncated output]") {
		t.Fatalf("expected truncation marker")
	}
}

func TestApplyPatchTool(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "patch.txt")
	if err := os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write patch.txt: %v", err)
	}

	registry := NewDefaultRegistry(workspace)
	out, err := registry.Execute(context.Background(), "apply_patch", []byte(`{"path":"patch.txt","find":"two","replace":"TWO"}`))
	if err != nil {
		t.Fatalf("apply_patch failed: %v", err)
	}
	var result struct {
		Replacements int `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal apply_patch output: %v", err)
	}
	if result.Replacements != 1 {
		t.Fatalf("expected 1 replacement, got %d", result.Replacements)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if !strings.Contains(string(updated), "TWO") {
		t.Fatalf("expected updated content, got %q", string(updated))
	}

	if _, err := registry.Execute(context.Background(), "apply_patch", []byte(`{"path":"patch.txt","find":"missing","replace":"x"}`)); err == nil {
		t.Fatalf("expected missing target error")
	}
	if _, err := registry.Execute(context.Background(), "apply_patch", []byte(`{"path":"../patch.txt","find":"one","replace":"ONE"}`)); err == nil {
		t.Fatalf("expected boundary error")
	}
}

func TestApplyPatchReplaceAll(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	content := strings.Repeat("line\n", 20)
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	registry := NewDefaultRegistry(workspace)

	patchOut, err := registry.Execute(context.Background(), "apply_patch", []byte(`{"path":"a.txt","find":"line","replace":"LINE","replace_all":true}`))
	if err != nil {
		t.Fatalf("apply_patch replace_all failed: %v", err)
	}
	var patchResult struct {
		Replacements int `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(patchOut), &patchResult); err != nil {
		t.Fatalf("unmarshal patch output: %v", err)
	}
	if patchResult.Replacements < 2 {
		t.Fatalf("expected multiple replacements, got %d", patchResult.Replacements)
	}
}

func TestInternalHelpersAndRunCommandBranches(t *testing.T) {
	t.Parallel()

	if err := validateWorkspaceRelativePattern("../bad"); err == nil {
		t.Fatalf("expected pattern escape error")
	}
	if err := validateWorkspaceRelativePattern("*.go"); err != nil {
		t.Fatalf("expected valid pattern: %v", err)
	}

	if _, err := buildLineMatcher("(", true, false); err == nil {
		t.Fatalf("expected regex compile error")
	}
	matcher, err := buildLineMatcher("Needle", false, false)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	if !matcher("contains needle") {
		t.Fatalf("expected case-insensitive match")
	}

	if _, _, timedOut, err := runCommand(context.Background(), 20*time.Millisecond, "bash", "-lc", "sleep 0.2"); err == nil || !timedOut {
		t.Fatalf("expected timeout error branch")
	}
	output, exitCode, timedOut, err := runCommand(context.Background(), 2*time.Second, "bash", "-lc", "echo hi; exit 3")
	if err != nil {
		t.Fatalf("expected non-zero exit to be handled without error: %v", err)
	}
	if exitCode != 3 {
		t.Fatalf("expected exit code 3, got %d", exitCode)
	}
	if timedOut {
		t.Fatalf("did not expect timeout")
	}
	if !strings.Contains(output, "hi") {
		t.Fatalf("expected command output, got %q", output)
	}
}


