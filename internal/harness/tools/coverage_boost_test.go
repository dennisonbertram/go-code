package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type errPolicy struct{}

func (e errPolicy) Allow(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
	return PolicyDecision{}, errors.New("policy boom")
}

func TestPolicyBranchesAndValidationErrors(t *testing.T) {
	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModePermissions, Policy: nil})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	write := findToolByName(t, list, "write")
	out, err := write.Handler(context.Background(), json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("expected structured deny output, got err: %v", err)
	}
	if !strings.Contains(out, "permission_denied") {
		t.Fatalf("expected permission_denied output, got %s", out)
	}

	list2, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModePermissions, Policy: errPolicy{}})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	write = findToolByName(t, list2, "write")
	out, err = write.Handler(context.Background(), json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("expected structured policy_error output, got err: %v", err)
	}
	if !strings.Contains(out, "permission_error") {
		t.Fatalf("expected permission_error output, got %s", out)
	}

	fullAuto, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModeFullAuto})
	if err != nil {
		t.Fatalf("BuildCatalog full auto: %v", err)
	}
	fetch := findToolByName(t, fullAuto, "fetch")
	if _, err := fetch.Handler(context.Background(), json.RawMessage(`{"url":"ftp://x"}`)); err == nil {
		t.Fatalf("expected invalid fetch scheme error")
	}
	download := findToolByName(t, fullAuto, "download")
	if _, err := download.Handler(context.Background(), json.RawMessage(`{"url":"http://x"}`)); err == nil {
		t.Fatalf("expected missing file_path error")
	}
}

func TestApplyPatchFindReplaceBranches(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "p.txt"), []byte("a\na\n"), 0o644); err != nil {
		t.Fatalf("write p.txt: %v", err)
	}
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	patch := findToolByName(t, list, "apply_patch")

	if _, err := patch.Handler(context.Background(), json.RawMessage(`{"path":"p.txt"}`)); err == nil {
		t.Fatalf("expected missing find error")
	}
	if _, err := patch.Handler(context.Background(), json.RawMessage(`{"path":"p.txt","find":"missing","replace":"x"}`)); err == nil {
		t.Fatalf("expected not present error")
	}

	out, err := patch.Handler(context.Background(), json.RawMessage(`{"path":"p.txt","find":"a","replace":"A","replace_all":true}`))
	if err != nil {
		t.Fatalf("replace_all patch failed: %v", err)
	}
	if !strings.Contains(out, `"replacements":2`) {
		t.Fatalf("expected 2 replacements, got %s", out)
	}
}

func TestWriteMissingExpectedVersionBranch(t *testing.T) {
	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	write := findToolByName(t, list, "write")
	out, err := write.Handler(context.Background(), json.RawMessage(`{"path":"missing.txt","content":"x","expected_version":"abc"}`))
	if err != nil {
		t.Fatalf("expected stale_write output, got err: %v", err)
	}
	if !strings.Contains(out, `"stale_write"`) {
		t.Fatalf("expected stale_write output, got %s", out)
	}
}

// TestWriteJSONValidation verifies that the write tool rejects invalid JSON content
// when the target file has a .json extension.
func TestWriteJSONValidation(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	write := findToolByName(t, list, "write")

	// Invalid JSON should return a structured error and not write the file.
	out, err := write.Handler(context.Background(), json.RawMessage(`{"path":"deploy/targets.json","content":"{\"broken\""}`))
	if err != nil {
		t.Fatalf("expected structured error result, got hard error: %v", err)
	}
	if !strings.Contains(out, `"invalid_json"`) {
		t.Fatalf("expected invalid_json code in output, got: %s", out)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "deploy/targets.json")); !os.IsNotExist(statErr) {
		t.Error("invalid JSON file should not have been written to disk")
	}

	// Valid JSON should be written without an error.
	validPayload, _ := json.Marshal(map[string]any{
		"path":    "config.json",
		"content": `{"key":"value"}`,
	})
	out, err = write.Handler(context.Background(), json.RawMessage(validPayload))
	if err != nil {
		t.Fatalf("unexpected error for valid JSON: %v", err)
	}
	if strings.Contains(out, `"error"`) && strings.Contains(out, `"invalid_json"`) {
		t.Fatalf("unexpected invalid_json error for valid JSON: %s", out)
	}
}

func TestJobManagerCleanupAndResolveDirBranches(t *testing.T) {
	workspace := t.TempDir()
	mgr := NewJobManager(workspace, time.Now)
	mgr.maxJobs = 0
	if _, err := mgr.runBackground(context.Background(), "echo hi", 1, "."); err == nil {
		t.Fatalf("expected max job limit error")
	}

	mgr2 := NewJobManager(workspace, func() time.Time { return time.Unix(1000, 0) })
	mgr2.ttl = 0
	mgr2.maxJobs = 2
	_, err := mgr2.runBackground(context.Background(), "echo hi", 1, ".")
	if err != nil {
		t.Fatalf("runBackground: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mgr2.cleanupExpired()
	if _, err := resolveWorkingDir(workspace, "nested"); err != nil {
		// nested may not exist, but path resolution should still be inside workspace.
		if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "absolute") {
			t.Fatalf("unexpected resolveWorkingDir error: %v", err)
		}
	}
}

func TestLSPSuccessAndErrorBranchesWithFakeGopls(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	binDir := t.TempDir()
	script := filepath.Join(binDir, "gopls")
	scriptContent := "#!/bin/bash\nif [ \"$1\" = \"workspace_symbol\" ]; then echo refs; exit 0; fi\nif [ \"$1\" = \"check\" ]; then echo diagnostics; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write fake gopls: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)

	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, EnableLSP: true})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	diag := findToolByName(t, list, "lsp_diagnostics")
	out, err := diag.Handler(context.Background(), json.RawMessage(`{"file_path":"a.go"}`))
	if err != nil {
		t.Fatalf("lsp_diagnostics success branch failed: %v", err)
	}
	if !strings.Contains(out, "diagnostics") {
		t.Fatalf("expected diagnostics output, got %s", out)
	}

	refs := findToolByName(t, list, "lsp_references")
	out, err = refs.Handler(context.Background(), json.RawMessage(`{"symbol":"Main","path":"a.go"}`))
	if err != nil {
		t.Fatalf("lsp_references success branch failed: %v", err)
	}
	if !strings.Contains(out, "refs") {
		t.Fatalf("expected refs output, got %s", out)
	}

	// Force command failure path still returning JSON output.
	if err := os.WriteFile(script, []byte("#!/bin/bash\nexit 2\n"), 0o755); err != nil {
		t.Fatalf("overwrite fake gopls: %v", err)
	}
	out, err = refs.Handler(context.Background(), json.RawMessage(`{"symbol":"Main"}`))
	if err != nil {
		t.Fatalf("expected lsp_references failure branch as JSON output: %v", err)
	}
	if !strings.Contains(out, "\"exit_code\":1") {
		t.Fatalf("expected exit_code 1 output, got %s", out)
	}
}

func TestSourcegraphAndMCPAndAgentErrorBranches(t *testing.T) {
	workspace := t.TempDir()

	tool := sourcegraphTool(http.DefaultClient, SourcegraphConfig{})
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"x"}`)); err == nil {
		t.Fatalf("expected missing endpoint error")
	}

	mcp := &fakeMCP{}
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, EnableMCP: true, MCPRegistry: mcp, EnableAgent: true, AgentRunner: &fakeRunner{}, EnableWebOps: true, WebFetcher: &fakeWeb{}})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	mcpList := findToolByName(t, list, "list_mcp_resources")
	if _, err := mcpList.Handler(context.Background(), json.RawMessage(`{"mcp_name":"bad"}`)); err == nil {
		t.Fatalf("expected mcp list error branch")
	}

	mcpRead := findToolByName(t, list, "read_mcp_resource")
	if _, err := mcpRead.Handler(context.Background(), json.RawMessage(`{"mcp_name":"x","uri":"missing"}`)); err == nil {
		t.Fatalf("expected mcp read error branch")
	}

	agent := findToolByName(t, list, "agent")
	if _, err := agent.Handler(context.Background(), json.RawMessage(`{"prompt":"please fail"}`)); err == nil {
		t.Fatalf("expected agent runner error branch")
	}

	agentic := findToolByName(t, list, "agentic_fetch")
	if _, err := agentic.Handler(context.Background(), json.RawMessage(`{"prompt":"ok","url":"https://fail.example"}`)); err == nil {
		t.Fatalf("expected agentic fetch web error branch")
	}

	search := findToolByName(t, list, "web_search")
	if _, err := search.Handler(context.Background(), json.RawMessage(`{"query":"fail"}`)); err == nil {
		t.Fatalf("expected web search error branch")
	}

	fetch := findToolByName(t, list, "web_fetch")
	if _, err := fetch.Handler(context.Background(), json.RawMessage(`{"url":"https://fail.example"}`)); err == nil {
		t.Fatalf("expected web fetch error branch")
	}
}

// ---------------------------------------------------------------------------
// Unified patch parsing and application
// ---------------------------------------------------------------------------

func TestParseUnifiedPatchBasic(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** Add File: new.txt\n+hello\n+world\n*** End Patch"
	files, err := parseUnifiedPatch(patch)
	if err != nil {
		t.Fatalf("parseUnifiedPatch: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "new.txt" {
		t.Fatalf("Path: got %q", files[0].Path)
	}
	if files[0].Kind != "add" {
		t.Fatalf("Kind: got %q", files[0].Kind)
	}
}

func TestParseUnifiedPatchMissingBegin(t *testing.T) {
	t.Parallel()

	_, err := parseUnifiedPatch("not a patch")
	if err == nil || !strings.Contains(err.Error(), "Begin Patch") {
		t.Fatalf("expected Begin Patch error, got: %v", err)
	}
}

func TestParseUnifiedPatchMissingEnd(t *testing.T) {
	t.Parallel()

	_, err := parseUnifiedPatch("*** Begin Patch\n*** Add File: new.txt\n+hello\n")
	if err == nil || !strings.Contains(err.Error(), "missing terminator") {
		t.Fatalf("expected missing terminator error, got: %v", err)
	}
}

func TestParseUnifiedPatchDeleteFile(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** Delete File: old.txt\n*** End Patch"
	files, err := parseUnifiedPatch(patch)
	if err != nil {
		t.Fatalf("parseUnifiedPatch: %v", err)
	}
	if len(files) != 1 || files[0].Kind != "delete" {
		t.Fatalf("expected delete, got: %+v", files)
	}
}

func TestParseUnifiedPatchUpdateFile(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** Update File: src.txt\n@@ section\n-old line\n+new line\n*** End Patch"
	files, err := parseUnifiedPatch(patch)
	if err != nil {
		t.Fatalf("parseUnifiedPatch: %v", err)
	}
	if len(files) != 1 || files[0].Kind != "update" {
		t.Fatalf("expected update, got: %+v", files)
	}
	if len(files[0].Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(files[0].Hunks))
	}
	if !strings.Contains(files[0].Hunks[0].OldText, "old line") {
		t.Fatalf("OldText: got %q", files[0].Hunks[0].OldText)
	}
	if !strings.Contains(files[0].Hunks[0].NewText, "new line") {
		t.Fatalf("NewText: got %q", files[0].Hunks[0].NewText)
	}
}

func TestParseUnifiedPatchUnsupportedLine(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\nbogus line\n*** End Patch"
	_, err := parseUnifiedPatch(patch)
	if err == nil || !strings.Contains(err.Error(), "unsupported patch line") {
		t.Fatalf("expected unsupported patch line error, got: %v", err)
	}
}

func TestApplyUnifiedPatchAddFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: newfile.txt\n+hello world\n*** End Patch"

	result, err := applyUnifiedPatch(context.Background(), workspace, SandboxScopeUnrestricted, patch)
	if err != nil {
		t.Fatalf("applyUnifiedPatch: %v", err)
	}
	if !strings.Contains(result, "newfile.txt") {
		t.Fatalf("expected newfile.txt in result: %s", result)
	}
	if !strings.Contains(result, `"add"`) {
		t.Fatalf("expected add action in result: %s", result)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "newfile.txt"))
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if !strings.Contains(string(content), "hello world") {
		t.Fatalf("file content: got %q", string(content))
	}
}

func TestApplyUnifiedPatchDeleteFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "todelete.txt"), []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Delete File: todelete.txt\n*** End Patch"
	result, err := applyUnifiedPatch(context.Background(), workspace, SandboxScopeUnrestricted, patch)
	if err != nil {
		t.Fatalf("applyUnifiedPatch: %v", err)
	}
	if !strings.Contains(result, `"delete"`) {
		t.Fatalf("expected delete action in result: %s", result)
	}

	if _, err := os.Stat(filepath.Join(workspace, "todelete.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted")
	}
}

func TestApplyUnifiedPatchUpdateFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: src.txt\n@@ section\n-old line\n+new line\n*** End Patch"
	result, err := applyUnifiedPatch(context.Background(), workspace, SandboxScopeUnrestricted, patch)
	if err != nil {
		t.Fatalf("applyUnifiedPatch: %v", err)
	}
	if !strings.Contains(result, `"update"`) {
		t.Fatalf("expected update action in result: %s", result)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "src.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "new line") {
		t.Fatalf("expected 'new line' in content, got: %q", string(content))
	}
}

func TestApplyUnifiedPatchUpdateMissingOldText(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("something\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: src.txt\n@@ section\n+only new\n*** End Patch"
	_, err := applyUnifiedPatch(context.Background(), workspace, SandboxScopeUnrestricted, patch)
	if err == nil || !strings.Contains(err.Error(), "missing old text") {
		t.Fatalf("expected missing old text error, got: %v", err)
	}
}

func TestApplyUnifiedPatchUpdateHunkNotPresent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "src.txt"), []byte("actual content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: src.txt\n@@ section\n-nonexistent content\n+replacement\n*** End Patch"
	_, err := applyUnifiedPatch(context.Background(), workspace, SandboxScopeUnrestricted, patch)
	if err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("expected hunk not present error, got: %v", err)
	}
}

func TestParseUnifiedPatchHunkContextLines(t *testing.T) {
	t.Parallel()

	// Test context lines (space prefix), removed lines, and added lines.
	lines := []string{
		" context before",
		"-removed",
		"+added",
		" context after",
		"*** End Patch",
	}
	hunk, next, err := parseUnifiedPatchHunk(lines, 0)
	if err != nil {
		t.Fatalf("parseUnifiedPatchHunk: %v", err)
	}
	if next != 4 {
		t.Fatalf("next: got %d, want 4", next)
	}
	if !strings.Contains(hunk.OldText, "context before") {
		t.Fatalf("OldText missing context: %q", hunk.OldText)
	}
	if !strings.Contains(hunk.OldText, "removed") {
		t.Fatalf("OldText missing removed line: %q", hunk.OldText)
	}
	if !strings.Contains(hunk.NewText, "added") {
		t.Fatalf("NewText missing added line: %q", hunk.NewText)
	}
	if !strings.Contains(hunk.NewText, "context after") {
		t.Fatalf("NewText missing context: %q", hunk.NewText)
	}
}

func TestParseUnifiedPatchHunkBadLine(t *testing.T) {
	t.Parallel()

	lines := []string{"xbad line"}
	_, _, err := parseUnifiedPatchHunk(lines, 0)
	if err == nil || !strings.Contains(err.Error(), "unexpected hunk line") {
		t.Fatalf("expected unexpected hunk line error, got: %v", err)
	}
}
