package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type allowAllPolicy struct{}

func (a allowAllPolicy) Allow(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
	return PolicyDecision{Allow: true}, nil
}

type denyAllPolicy struct{}

func (d denyAllPolicy) Allow(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
	return PolicyDecision{Allow: false, Reason: "denied in test"}, nil
}

type fakeMCP struct {
	calledTool string
}

func (f *fakeMCP) ListResources(_ context.Context, server string) ([]MCPResource, error) {
	if server == "bad" {
		return nil, errors.New("bad server")
	}
	return []MCPResource{{URI: "mcp://" + server + "/r1", Name: "r1"}}, nil
}

func (f *fakeMCP) ReadResource(_ context.Context, server, uri string) (string, error) {
	if uri == "missing" {
		return "", errors.New("missing")
	}
	return server + ":" + uri, nil
}

func (f *fakeMCP) ListTools(_ context.Context) (map[string][]MCPToolDefinition, error) {
	return map[string][]MCPToolDefinition{
		"server-a": {{Name: "Do Thing", Description: "desc", Parameters: map[string]any{"type": "object"}}},
	}, nil
}

func (f *fakeMCP) CallTool(_ context.Context, server, tool string, _ json.RawMessage) (string, error) {
	f.calledTool = server + "/" + tool
	return `{"ok":true}`, nil
}

type fakeRunner struct{}

func (f *fakeRunner) RunPrompt(_ context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "fail") {
		return "", errors.New("runner failed")
	}
	return "ran: " + prompt, nil
}

type fakeWeb struct{}

func (f *fakeWeb) Search(_ context.Context, query string, maxResults int) ([]map[string]any, error) {
	if query == "fail" {
		return nil, errors.New("search failed")
	}
	return []map[string]any{{"title": "t", "query": query, "max": maxResults}}, nil
}

func (f *fakeWeb) Fetch(_ context.Context, url string) (string, error) {
	if strings.Contains(url, "fail") {
		return "", errors.New("fetch failed")
	}
	return "content:" + url, nil
}

func findToolByName(t *testing.T, list []Tool, name string) Tool {
	t.Helper()
	for _, tool := range list {
		if tool.Definition.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return Tool{}
}

func executeTool(t *testing.T, list []Tool, name string, ctx context.Context, args string) map[string]any {
	t.Helper()
	tool := findToolByName(t, list, name)
	out, err := tool.Handler(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %s output: %v", name, err)
	}
	return payload
}

func TestBuildCatalogDefaultNamesSorted(t *testing.T) {
	t.Parallel()

	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: t.TempDir(), EnableTodos: true, EnableLSP: true})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Definition.Name)
	}
	expected := []string{
		"AskUserQuestion", "apply_patch", "bash", "compact_history", "context_status", "download", "edit", "fetch", "git_diff", "git_status", "glob", "grep", "job_kill", "job_output", "ls", "lsp_diagnostics", "lsp_references", "lsp_restart", "observational_memory", "read", "todos", "write",
	}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("unexpected catalog names\n got: %v\nwant: %v", names, expected)
	}
}

// TestAllCatalogToolsHaveNonEmptyDescriptions ensures every tool registered
// in the catalog has a non-empty Description field. This is the end-to-end
// invariant for Issue #41: all tool descriptions must be loaded from embedded
// .md files via descriptions.Load(), not left empty or as zero-values.
func TestAllCatalogToolsHaveNonEmptyDescriptions(t *testing.T) {
	t.Parallel()

	mcp := &fakeMCP{}
	runner := &fakeRunner{}
	web := &fakeWeb{}
	list, err := BuildCatalog(BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableTodos:   true,
		EnableLSP:     true,
		EnableMCP:     true,
		MCPRegistry:   mcp,
		EnableAgent:   true,
		AgentRunner:   runner,
		EnableWebOps:  true,
		WebFetcher:    web,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	if len(list) == 0 {
		t.Fatalf("catalog is empty — cannot validate descriptions")
	}
	for _, tool := range list {
		name := tool.Definition.Name
		if strings.TrimSpace(tool.Definition.Description) == "" {
			t.Errorf("tool %q has empty Description — add descriptions.Load(%q) call and create descriptions/%s.md", name, name, name)
		}
	}
}

func TestCommonPathsAndHelpers(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := validateWorkspaceRelativePattern("../bad"); err == nil {
		t.Fatalf("expected pattern escape error")
	}
	if err := validateWorkspaceRelativePattern("*.go"); err != nil {
		t.Fatalf("expected valid pattern: %v", err)
	}
	if err := ValidateWorkspaceRelativePattern("ok/*.txt"); err != nil {
		t.Fatalf("expected exported helper to pass: %v", err)
	}

	abs, err := ResolveWorkspacePath(workspace, "a/b.txt")
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	if got := NormalizeRelPath(workspace, abs); got != "a/b.txt" {
		t.Fatalf("unexpected normalized path %q", got)
	}

	if _, err := BuildLineMatcher("(", true, false); err == nil {
		t.Fatalf("expected regex compile error")
	}
	matcher, err := BuildLineMatcher("Needle", false, false)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	if !matcher("contains needle") {
		t.Fatalf("expected case-insensitive match")
	}

	if _, _, timedOut, err := RunCommand(context.Background(), 20*time.Millisecond, "bash", "-lc", "sleep 0.2"); err != nil || !timedOut {
		t.Fatalf("expected timeout branch")
	}
	output, exitCode, timedOut, err := RunCommand(context.Background(), 2*time.Second, "bash", "-lc", "echo hi; exit 3")
	if err != nil {
		t.Fatalf("run command non-zero: %v", err)
	}
	if exitCode != 3 || timedOut || !strings.Contains(output, "hi") {
		t.Fatalf("unexpected command branch output=%q code=%d timeout=%v", output, exitCode, timedOut)
	}
	if !IsDangerousCommand("rm -rf /") {
		t.Fatalf("expected dangerous command detection")
	}
}

func TestCoreFileSearchAndGitTools(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "main.go"), []byte("package main\n// Needle\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	runGit("add", ".")
	runGit("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write modified README: %v", err)
	}

	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	ls := executeTool(t, list, "ls", context.Background(), `{"path":".","recursive":true,"depth":2}`)
	entries := ls["entries"].([]any)
	if len(entries) == 0 {
		t.Fatalf("expected ls entries: %#v", ls)
	}

	glob := executeTool(t, list, "glob", context.Background(), `{"pattern":"src/*.go"}`)
	matches := glob["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("expected one glob match: %#v", glob)
	}

	grep := executeTool(t, list, "grep", context.Background(), `{"query":"needle","path":"src","case_sensitive":false}`)
	gmatches := grep["matches"].([]any)
	if len(gmatches) == 0 {
		t.Fatalf("expected grep matches: %#v", grep)
	}

	status := executeTool(t, list, "git_status", context.Background(), `{}`)
	if _, ok := status["clean"]; !ok {
		t.Fatalf("expected git_status clean field: %#v", status)
	}

	diff := executeTool(t, list, "git_diff", context.Background(), `{"path":"README.md","max_bytes":200}`)
	if _, ok := diff["diff"]; !ok {
		t.Fatalf("expected git_diff output: %#v", diff)
	}
}

func TestReadWriteEditAndPatchWithVersioning(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, EnableTodos: true, EnableLSP: false})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	writeResult := executeTool(t, list, "write", context.Background(), `{"path":"notes.txt","content":"a\nb\nc\n"}`)
	version := writeResult["version"].(string)

	readResult := executeTool(t, list, "read", context.Background(), `{"file_path":"notes.txt","offset":1,"limit":1}`)
	if readResult["content"].(string) != "b" {
		t.Fatalf("unexpected paged read content: %#v", readResult)
	}
	if readResult["version"].(string) == "" {
		t.Fatalf("expected read version")
	}

	stale := executeTool(t, list, "write", context.Background(), `{"path":"notes.txt","content":"x","expected_version":"stale"}`)
	if stale["error"] == nil {
		t.Fatalf("expected stale_write output")
	}

	editResult := executeTool(t, list, "edit", context.Background(), `{"path":"notes.txt","old_text":"b","new_text":"B","expected_version":"`+version+`"}`)
	if editResult["replacements"].(float64) != 1 {
		t.Fatalf("expected 1 replacement: %#v", editResult)
	}

	patchResult := executeTool(t, list, "apply_patch", context.Background(), `{"path":"notes.txt","edits":[{"old_string":"a","new_string":"A"},{"old_string":"missing","new_string":"M"}]}`)
	if patchResult["partial"] != true {
		t.Fatalf("expected partial edit result: %#v", patchResult)
	}
}

func TestBashLifecycleAndJobTools(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModeFullAuto})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	fg := executeTool(t, list, "bash", context.Background(), `{"description":"say hi","command":"printf hi","working_dir":".","timeout_seconds":5}`)
	if fg["exit_code"].(float64) != 0 || fg["output"].(string) != "hi" {
		t.Fatalf("unexpected foreground bash output: %#v", fg)
	}

	bg := executeTool(t, list, "bash", context.Background(), `{"command":"sleep 0.2; echo done","run_in_background":true}`)
	shellID, ok := bg["shell_id"].(string)
	if !ok || shellID == "" {
		t.Fatalf("expected shell_id in background result: %#v", bg)
	}

	out := executeTool(t, list, "job_output", context.Background(), `{"shell_id":"`+shellID+`","wait":true}`)
	if _, ok := out["running"]; !ok {
		t.Fatalf("expected running field: %#v", out)
	}

	kill := executeTool(t, list, "job_kill", context.Background(), `{"shell_id":"`+shellID+`"}`)
	if kill["killed"] != true {
		t.Fatalf("expected killed=true: %#v", kill)
	}

	tool := findToolByName(t, list, "bash")
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`)); err == nil {
		t.Fatalf("expected dangerous command rejection")
	}
}

func TestFetchAndDownloadTools(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "abcdef")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello world")
	}))
	defer server.Close()

	workspace := t.TempDir()
	client := server.Client()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, HTTPClient: client})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	fetch := executeTool(t, list, "fetch", context.Background(), `{"url":"`+server.URL+`","max_bytes":5}`)
	if fetch["truncated"] != true {
		t.Fatalf("expected fetch truncation: %#v", fetch)
	}

	download := executeTool(t, list, "download", context.Background(), `{"url":"`+server.URL+`/bin","file_path":"d.bin"}`)
	if download["bytes_written"].(float64) <= 0 {
		t.Fatalf("expected bytes_written: %#v", download)
	}
	if _, err := os.Stat(filepath.Join(workspace, "d.bin")); err != nil {
		t.Fatalf("expected downloaded file: %v", err)
	}
}

func TestPermissionsModePolicy(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModePermissions, Policy: denyAllPolicy{}})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	readTool := findToolByName(t, list, "read")
	if _, err := readTool.Handler(context.Background(), json.RawMessage(`{"path":"missing"}`)); err == nil {
		// read tool itself should run (and then fail because file missing), not be permission blocked.
	} else if strings.Contains(err.Error(), "permission") {
		t.Fatalf("read should not be permission blocked: %v", err)
	}

	blocked := executeTool(t, list, "write", context.Background(), `{"path":"x.txt","content":"x"}`)
	errField, ok := blocked["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured permission error: %#v", blocked)
	}
	if errField["code"] != "permission_denied" {
		t.Fatalf("unexpected permission code: %#v", blocked)
	}

	allowed, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, ApprovalMode: ApprovalModePermissions, Policy: allowAllPolicy{}})
	if err != nil {
		t.Fatalf("BuildCatalog allow: %v", err)
	}
	out := executeTool(t, allowed, "write", context.Background(), `{"path":"ok.txt","content":"ok"}`)
	if out["bytes_written"].(float64) != 2 {
		t.Fatalf("expected successful write in allow policy: %#v", out)
	}
}

func TestTodosRunScope(t *testing.T) {
	t.Parallel()

	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: t.TempDir(), EnableTodos: true})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	ctx := context.WithValue(context.Background(), ContextKeyRunID, "run_123")
	res := executeTool(t, list, "todos", ctx, `{"todos":[{"id":"1","text":"a","status":"pending"}]}`)
	if res["run_id"].(string) != "run_123" {
		t.Fatalf("unexpected run_id in todos: %#v", res)
	}
}

func TestLSPToolsAndSourcegraph(t *testing.T) {
	t.Setenv("PATH", "")
	workspace := t.TempDir()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, EnableLSP: true})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	lspDiag := findToolByName(t, list, "lsp_diagnostics")
	if _, err := lspDiag.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatalf("expected missing gopls error")
	}

	lspRef := findToolByName(t, list, "lsp_references")
	if _, err := lspRef.Handler(context.Background(), json.RawMessage(`{"symbol":"S"}`)); err == nil {
		t.Fatalf("expected missing gopls error")
	}

	lspRestart := executeTool(t, list, "lsp_restart", context.Background(), `{}`)
	if lspRestart["restarted"] != true {
		t.Fatalf("expected lsp_restart success")
	}

	sg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer sg.Close()

	list2, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace, HTTPClient: sg.Client(), Sourcegraph: SourcegraphConfig{Endpoint: sg.URL}})
	if err != nil {
		t.Fatalf("BuildCatalog sg: %v", err)
	}
	res := executeTool(t, list2, "sourcegraph", context.Background(), `{"query":"repo:foo","count":1}`)
	if res["status_code"].(float64) != 200 {
		t.Fatalf("unexpected sourcegraph status: %#v", res)
	}
}

func TestMCPDynamicAndAgentTools(t *testing.T) {
	t.Parallel()

	mcp := &fakeMCP{}
	runner := &fakeRunner{}
	web := &fakeWeb{}
	list, err := BuildCatalog(BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableMCP:     true,
		MCPRegistry:   mcp,
		EnableAgent:   true,
		AgentRunner:   runner,
		EnableWebOps:  true,
		WebFetcher:    web,
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}

	mcpList := executeTool(t, list, "list_mcp_resources", context.Background(), `{"mcp_name":"alpha"}`)
	resources := mcpList["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected mcp resources: %#v", mcpList)
	}

	mcpRead := executeTool(t, list, "read_mcp_resource", context.Background(), `{"mcp_name":"alpha","uri":"u1"}`)
	if !strings.Contains(mcpRead["content"].(string), "alpha:u1") {
		t.Fatalf("unexpected mcp read: %#v", mcpRead)
	}

	dynamic := executeTool(t, list, "mcp_server_a_do_thing", context.Background(), `{}`)
	if dynamic["ok"] != true {
		t.Fatalf("unexpected dynamic mcp output: %#v", dynamic)
	}
	if mcp.calledTool != "server-a/Do Thing" {
		t.Fatalf("unexpected dynamic mcp call target: %s", mcp.calledTool)
	}

	agent := executeTool(t, list, "agent", context.Background(), `{"prompt":"hello"}`)
	if !strings.Contains(agent["output"].(string), "ran: hello") {
		t.Fatalf("unexpected agent output: %#v", agent)
	}

	agentic := executeTool(t, list, "agentic_fetch", context.Background(), `{"prompt":"analyze","url":"https://example.com"}`)
	if !strings.Contains(agentic["analysis"].(string), "ran: analyze") {
		t.Fatalf("unexpected agentic analysis: %#v", agentic)
	}

	search := executeTool(t, list, "web_search", context.Background(), `{"query":"golang","max_results":2}`)
	if _, ok := search["results"].([]any); !ok {
		t.Fatalf("expected web search results: %#v", search)
	}

	fetch := executeTool(t, list, "web_fetch", context.Background(), `{"url":"https://example.com"}`)
	if !strings.Contains(fetch["content"].(string), "content:") {
		t.Fatalf("unexpected web fetch output: %#v", fetch)
	}
}
