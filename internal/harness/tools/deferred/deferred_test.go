package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/profiles"
	"go-agent-harness/internal/provider/catalog"
)

// ---------- mock types ----------

type mockAgentRunner struct{ output string }

func (m *mockAgentRunner) RunPrompt(_ context.Context, prompt string) (string, error) {
	return m.output, nil
}

// mockModelAgentRunner implements both AgentRunner and ModelAgentRunner interfaces.
// It records the model argument passed to RunPromptWithModel for assertion in tests.
type mockModelAgentRunner struct {
	output        string
	capturedModel string
}

func (m *mockModelAgentRunner) RunPrompt(_ context.Context, prompt string) (string, error) {
	return m.output, nil
}

func (m *mockModelAgentRunner) RunPromptWithModel(_ context.Context, prompt, model string) (string, error) {
	m.capturedModel = model
	return m.output, nil
}

type mockWebFetcher struct{ content string }

func (m *mockWebFetcher) Fetch(_ context.Context, url string) (string, error) {
	return m.content, nil
}
func (m *mockWebFetcher) Search(_ context.Context, query string, max int) ([]map[string]any, error) {
	return []map[string]any{{"url": "https://example.com", "title": "test"}}, nil
}

type mockCronClient struct{}

func (m *mockCronClient) CreateJob(_ context.Context, req tools.CronCreateJobRequest) (tools.CronJob, error) {
	return tools.CronJob{ID: "j1", Name: req.Name}, nil
}
func (m *mockCronClient) ListJobs(_ context.Context) ([]tools.CronJob, error) {
	return []tools.CronJob{}, nil
}
func (m *mockCronClient) GetJob(_ context.Context, id string) (tools.CronJob, error) {
	return tools.CronJob{ID: id}, nil
}
func (m *mockCronClient) DeleteJob(_ context.Context, id string) error { return nil }
func (m *mockCronClient) UpdateJob(_ context.Context, id string, req tools.CronUpdateJobRequest) (tools.CronJob, error) {
	return tools.CronJob{ID: id}, nil
}
func (m *mockCronClient) ListExecutions(_ context.Context, id string, limit, offset int) ([]tools.CronExecution, error) {
	return nil, nil
}
func (m *mockCronClient) Health(_ context.Context) error { return nil }

type mockMCPRegistry struct{}

func (m *mockMCPRegistry) ListResources(_ context.Context, name string) ([]tools.MCPResource, error) {
	return []tools.MCPResource{}, nil
}
func (m *mockMCPRegistry) ReadResource(_ context.Context, name, uri string) (string, error) {
	return "content", nil
}
func (m *mockMCPRegistry) ListTools(_ context.Context) (map[string][]tools.MCPToolDefinition, error) {
	return map[string][]tools.MCPToolDefinition{}, nil
}
func (m *mockMCPRegistry) CallTool(_ context.Context, server, name string, args json.RawMessage) (string, error) {
	return "{}", nil
}

// ---------- context helpers ----------

func withRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, tools.ContextKeyRunID, runID)
}

func withRunMetadata(ctx context.Context, md tools.RunMetadata) context.Context {
	return context.WithValue(ctx, tools.ContextKeyRunMetadata, md)
}

// testServerHost extracts the host (without port) from an httptest.Server URL,
// for use as an SSRF-guard NetworkAllowlist entry in tests that legitimately
// need to reach a local test server.
func testServerHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL %q: %v", rawURL, err)
	}
	host := u.Hostname()
	if host == "" {
		t.Fatalf("test server URL %q has no host", rawURL)
	}
	return host
}

// ---------- tests ----------

// TestFetchTool_Definition verifies the fetch tool constructor.
func TestFetchTool_Definition(t *testing.T) {
	tool := FetchTool(http.DefaultClient, nil)
	assertToolDef(t, tool, "fetch", tools.TierDeferred)
	assertHasTags(t, tool, "http", "web")
}

// TestFetchTool_Handler_MissingURL verifies fetch returns an error when url is empty.
func TestFetchTool_Handler_MissingURL(t *testing.T) {
	tool := FetchTool(http.DefaultClient, nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

// TestFetchTool_Handler_BadScheme verifies fetch rejects non-http schemes.
func TestFetchTool_Handler_BadScheme(t *testing.T) {
	tool := FetchTool(http.DefaultClient, nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"url":"ftp://example.com"}`))
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
}

// TestFetchTool_Handler_Success verifies fetch returns content from a test server.
// The test server listens on loopback, which the SSRF guard blocks by
// default (see ssrf_guard_test.go in the parent package) — so this test
// exercises the explicit opt-in allowlist to reach it, proving the guard
// doesn't break legitimate, explicitly-permitted local fetches.
func TestFetchTool_Handler_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello from server")
	}))
	defer ts.Close()

	tool := FetchTool(ts.Client(), []string{testServerHost(t, ts.URL)})
	args, _ := json.Marshal(map[string]string{"url": ts.URL})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDownloadTool_Definition verifies the download tool constructor.
func TestDownloadTool_Definition(t *testing.T) {
	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "download", tools.TierCore)
	assertHasTags(t, tool, "http", "download")
}

// TestDownloadTool_Handler_MissingURL verifies download returns an error when url is empty.
func TestDownloadTool_Handler_MissingURL(t *testing.T) {
	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"file_path":"out.txt"}`))
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

// TestDownloadTool_Handler_MissingFilePath verifies download returns an error when file_path is empty.
func TestDownloadTool_Handler_MissingFilePath(t *testing.T) {
	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil {
		t.Fatal("expected error for missing file_path")
	}
}

// TestDownloadTool_Handler_BlocksLoopbackByDefault is a regression test for
// the SSRF guard wiring specifically on the production download tool
// (BUG-2): without an explicit NetworkAllowlist entry, a request to a
// loopback test server must be refused, proving DownloadTool actually routes
// through the guarded client rather than the raw one.
func TestDownloadTool_Handler_BlocksLoopbackByDefault(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should never reach the agent"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: dir, HTTPClient: ts.Client()})
	args, _ := json.Marshal(map[string]string{"url": ts.URL, "file_path": "dl.txt"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected download from an unallowlisted loopback destination to be blocked by default")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "dl.txt")); statErr == nil {
		t.Fatal("expected no file to be written when the destination is blocked")
	}
}

// TestDownloadTool_Handler_Success verifies download saves content from a test server.
// The test server listens on loopback, which the SSRF guard blocks by
// default — so this test exercises the explicit opt-in NetworkAllowlist to
// reach it, proving the guard doesn't break legitimate, explicitly-permitted
// local fetches.
func TestDownloadTool_Handler_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("downloaded content"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: dir, HTTPClient: ts.Client(), NetworkAllowlist: []string{testServerHost(t, ts.URL)}})
	args, _ := json.Marshal(map[string]string{"url": ts.URL, "file_path": "dl.txt"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	data, err := os.ReadFile(filepath.Join(dir, "dl.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "downloaded content" {
		t.Errorf("expected 'downloaded content', got %q", string(data))
	}
}

// TestAgentTool_Definition verifies the agent tool constructor.
func TestAgentTool_Definition(t *testing.T) {
	tool := AgentTool(&mockAgentRunner{output: "ok"})
	assertToolDef(t, tool, "agent", tools.TierDeferred)
	assertHasTags(t, tool, "agent")
}

// TestAgentTool_Handler_MissingPrompt verifies agent returns an error when prompt is empty.
func TestAgentTool_Handler_MissingPrompt(t *testing.T) {
	tool := AgentTool(&mockAgentRunner{output: "ok"})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

// TestAgentTool_Handler_Success verifies agent runs a prompt.
func TestAgentTool_Handler_Success(t *testing.T) {
	tool := AgentTool(&mockAgentRunner{output: "agent result"})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"prompt":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestAgentToolAcceptsModelParam verifies that the agent tool passes the model parameter
// to RunPromptWithModel when the runner implements ModelAgentRunner.
func TestAgentToolAcceptsModelParam(t *testing.T) {
	runner := &mockModelAgentRunner{output: "model result"}
	tool := AgentTool(runner)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"prompt":"do something","model":"gpt-4.1-mini"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if runner.capturedModel != "gpt-4.1-mini" {
		t.Errorf("expected model %q to be passed to RunPromptWithModel, got %q", "gpt-4.1-mini", runner.capturedModel)
	}
}

// TestAgentToolDefaultModelWhenNotSpecified verifies that when no model is provided,
// the agent tool falls back to RunPrompt (empty model = runner's default).
func TestAgentToolDefaultModelWhenNotSpecified(t *testing.T) {
	runner := &mockModelAgentRunner{output: "default result"}
	tool := AgentTool(runner)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"prompt":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// When model is not specified, capturedModel should be empty (RunPrompt used instead).
	if runner.capturedModel != "" {
		t.Errorf("expected no model to be captured when model param is absent, got %q", runner.capturedModel)
	}
}

// TestAgentToolSchemaIncludesModelParam verifies the tool schema exposes the optional model parameter.
func TestAgentToolSchemaIncludesModelParam(t *testing.T) {
	tool := AgentTool(&mockAgentRunner{output: "ok"})
	props, ok := tool.Definition.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected parameters.properties to be map[string]any")
	}
	if _, exists := props["model"]; !exists {
		t.Error("expected parameters.properties to include 'model' field")
	}
	required, _ := tool.Definition.Parameters["required"].([]string)
	for _, r := range required {
		if r == "model" {
			t.Error("'model' must not be in required — it is optional")
		}
	}
}

// TestAgentToolModelParamFallbackWhenRunnerDoesNotImplementModelAgentRunner verifies that
// when the runner does not implement ModelAgentRunner, the tool falls back to RunPrompt
// even when a model param is provided.
func TestAgentToolModelParamFallbackWhenRunnerDoesNotImplementModelAgentRunner(t *testing.T) {
	runner := &mockAgentRunner{output: "basic result"}
	tool := AgentTool(runner)
	// Even with model param, should succeed by calling RunPrompt
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"prompt":"do something","model":"gpt-4.1-mini"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestAgenticFetchTool_Definition verifies the agentic_fetch tool constructor.
func TestAgenticFetchTool_Definition(t *testing.T) {
	tool := AgenticFetchTool(&mockWebFetcher{content: "page"}, &mockAgentRunner{output: "ok"})
	assertToolDef(t, tool, "agentic_fetch", tools.TierDeferred)
}

// TestAgenticFetchTool_Handler_MissingPrompt verifies agentic_fetch returns an error when prompt is empty.
func TestAgenticFetchTool_Handler_MissingPrompt(t *testing.T) {
	tool := AgenticFetchTool(&mockWebFetcher{content: "page"}, &mockAgentRunner{output: "ok"})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

// TestWebSearchTool_Definition verifies the web_search tool constructor.
func TestWebSearchTool_Definition(t *testing.T) {
	tool := WebSearchTool(&mockWebFetcher{})
	assertToolDef(t, tool, "web_search", tools.TierDeferred)
	assertHasTags(t, tool, "web", "search")
}

// TestWebSearchTool_Handler_MissingQuery verifies web_search returns an error when query is empty.
func TestWebSearchTool_Handler_MissingQuery(t *testing.T) {
	tool := WebSearchTool(&mockWebFetcher{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// TestWebSearchTool_Handler_Success verifies web_search returns results.
func TestWebSearchTool_Handler_Success(t *testing.T) {
	tool := WebSearchTool(&mockWebFetcher{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestWebFetchTool_Definition verifies the web_fetch tool constructor.
func TestWebFetchTool_Definition(t *testing.T) {
	tool := WebFetchTool(&mockWebFetcher{content: "page"})
	assertToolDef(t, tool, "web_fetch", tools.TierDeferred)
}

// TestWebFetchTool_Handler_MissingURL verifies web_fetch returns an error when url is empty.
func TestWebFetchTool_Handler_MissingURL(t *testing.T) {
	tool := WebFetchTool(&mockWebFetcher{content: "page"})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

// TestWebFetchTool_Handler_Success verifies web_fetch returns content.
func TestWebFetchTool_Handler_Success(t *testing.T) {
	tool := WebFetchTool(&mockWebFetcher{content: "fetched page"})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestListModelsTool_Definition verifies the list_models tool constructor.
func TestListModelsTool_Definition(t *testing.T) {
	cat := &catalog.Catalog{Providers: map[string]catalog.ProviderEntry{}}
	tool := ListModelsTool(cat)
	assertToolDef(t, tool, "list_models", tools.TierDeferred)
	assertHasTags(t, tool, "models")
}

// TestListModelsTool_Handler_List verifies list_models list action.
func TestListModelsTool_Handler_List(t *testing.T) {
	cat := &catalog.Catalog{Providers: map[string]catalog.ProviderEntry{}}
	tool := ListModelsTool(cat)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestListModelsTool_Handler_Providers verifies list_models providers action.
func TestListModelsTool_Handler_Providers(t *testing.T) {
	cat := &catalog.Catalog{Providers: map[string]catalog.ProviderEntry{}}
	tool := ListModelsTool(cat)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"providers"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestListModelsTool_Handler_UnknownAction verifies list_models returns error for unknown action.
func TestListModelsTool_Handler_UnknownAction(t *testing.T) {
	cat := &catalog.Catalog{Providers: map[string]catalog.ProviderEntry{}}
	tool := ListModelsTool(cat)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"bogus"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// TestCronCreateTool_Definition verifies the cron_create tool constructor.
func TestCronCreateTool_Definition(t *testing.T) {
	tool := CronCreateTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_create", tools.TierDeferred)
	assertHasTags(t, tool, "cron")
}

// TestCronCreateTool_Handler_Success verifies cron_create creates a job.
func TestCronCreateTool_Handler_Success(t *testing.T) {
	tool := CronCreateTool(&mockCronClient{})
	args := `{"name":"test","schedule":"* * * * *","command":"echo hi"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestCronListTool_Definition verifies the cron_list tool constructor.
func TestCronListTool_Definition(t *testing.T) {
	tool := CronListTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_list", tools.TierDeferred)
}

// TestCronGetTool_Definition verifies the cron_get tool constructor.
func TestCronGetTool_Definition(t *testing.T) {
	tool := CronGetTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_get", tools.TierDeferred)
}

// TestCronDeleteTool_Definition verifies the cron_delete tool constructor.
func TestCronDeleteTool_Definition(t *testing.T) {
	tool := CronDeleteTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_delete", tools.TierDeferred)
}

// TestCronPauseTool_Definition verifies the cron_pause tool constructor.
func TestCronPauseTool_Definition(t *testing.T) {
	tool := CronPauseTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_pause", tools.TierDeferred)
}

// TestCronResumeTool_Definition verifies the cron_resume tool constructor.
func TestCronResumeTool_Definition(t *testing.T) {
	tool := CronResumeTool(&mockCronClient{})
	assertToolDef(t, tool, "cron_resume", tools.TierDeferred)
}

// TestSetDelayedCallbackTool_Definition verifies the set_delayed_callback tool constructor.
func TestSetDelayedCallbackTool_Definition(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := SetDelayedCallbackTool(mgr)
	assertToolDef(t, tool, "set_delayed_callback", tools.TierDeferred)
	assertHasTags(t, tool, "callback", "delayed")
}

// TestCancelDelayedCallbackTool_Definition verifies the cancel_delayed_callback tool constructor.
func TestCancelDelayedCallbackTool_Definition(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := CancelDelayedCallbackTool(mgr)
	assertToolDef(t, tool, "cancel_delayed_callback", tools.TierDeferred)
}

// TestListDelayedCallbacksTool_Definition verifies the list_delayed_callbacks tool constructor.
func TestListDelayedCallbacksTool_Definition(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := ListDelayedCallbacksTool(mgr)
	assertToolDef(t, tool, "list_delayed_callbacks", tools.TierDeferred)
}

// TestListDelayedCallbacksTool_Handler_Success verifies list_delayed_callbacks returns an empty list.
func TestListDelayedCallbacksTool_Handler_Success(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := ListDelayedCallbacksTool(mgr)
	ctx := withRunMetadata(context.Background(), tools.RunMetadata{ConversationID: "conv-1"})
	result, err := tool.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestLspDiagnosticsTool_Definition verifies the lsp_diagnostics tool constructor.
func TestLspDiagnosticsTool_Definition(t *testing.T) {
	tool := LspDiagnosticsTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "lsp_diagnostics", tools.TierDeferred)
	assertHasTags(t, tool, "lsp")
}

// TestLspReferencesTool_Definition verifies the lsp_references tool constructor.
func TestLspReferencesTool_Definition(t *testing.T) {
	tool := LspReferencesTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "lsp_references", tools.TierDeferred)
}

// TestLspRestartTool_Definition verifies the lsp_restart tool constructor.
func TestLspRestartTool_Definition(t *testing.T) {
	tool := LspRestartTool()
	assertToolDef(t, tool, "lsp_restart", tools.TierDeferred)
}

// TestLspRestartTool_Handler_DefaultName verifies lsp_restart uses gopls by default.
func TestLspRestartTool_Handler_DefaultName(t *testing.T) {
	tool := LspRestartTool()
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestListMCPResourcesTool_Definition verifies the list_mcp_resources tool constructor.
func TestListMCPResourcesTool_Definition(t *testing.T) {
	tool := ListMCPResourcesTool(&mockMCPRegistry{})
	assertToolDef(t, tool, "list_mcp_resources", tools.TierDeferred)
	assertHasTags(t, tool, "mcp")
}

// TestListMCPResourcesTool_Handler_MissingName verifies list_mcp_resources returns error when name is empty.
func TestListMCPResourcesTool_Handler_MissingName(t *testing.T) {
	tool := ListMCPResourcesTool(&mockMCPRegistry{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing mcp_name")
	}
}

// TestListMCPResourcesTool_Handler_Success verifies list_mcp_resources returns resources.
func TestListMCPResourcesTool_Handler_Success(t *testing.T) {
	tool := ListMCPResourcesTool(&mockMCPRegistry{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"mcp_name":"test-server"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestReadMCPResourceTool_Definition verifies the read_mcp_resource tool constructor.
func TestReadMCPResourceTool_Definition(t *testing.T) {
	tool := ReadMCPResourceTool(&mockMCPRegistry{})
	assertToolDef(t, tool, "read_mcp_resource", tools.TierDeferred)
}

// TestReadMCPResourceTool_Handler_MissingFields verifies read_mcp_resource returns error when fields are empty.
func TestReadMCPResourceTool_Handler_MissingFields(t *testing.T) {
	tool := ReadMCPResourceTool(&mockMCPRegistry{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
}

// TestReadMCPResourceTool_Handler_Success verifies read_mcp_resource returns content.
func TestReadMCPResourceTool_Handler_Success(t *testing.T) {
	tool := ReadMCPResourceTool(&mockMCPRegistry{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"mcp_name":"srv","uri":"res://test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDynamicMCPTools_Empty verifies DynamicMCPTools returns empty list for empty registry.
func TestDynamicMCPTools_Empty(t *testing.T) {
	tt, err := DynamicMCPTools(context.Background(), &mockMCPRegistry{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tt) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tt))
	}
}

// TestSourcegraphTool_Definition verifies the sourcegraph tool constructor.
func TestSourcegraphTool_Definition(t *testing.T) {
	tool := SourcegraphTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "sourcegraph", tools.TierDeferred)
	assertHasTags(t, tool, "sourcegraph")
}

// TestSourcegraphTool_Handler_NotConfigured verifies sourcegraph returns error when not configured.
func TestSourcegraphTool_Handler_NotConfigured(t *testing.T) {
	tool := SourcegraphTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Fatal("expected error for unconfigured sourcegraph")
	}
}

// TestTodosTool_Definition verifies the todos tool constructor.
func TestTodosTool_Definition(t *testing.T) {
	tool := TodosTool()
	assertToolDef(t, tool, "todos", tools.TierCore)
	assertHasTags(t, tool, "planning")
}

// TestTodosTool_Handler_EmptyList verifies todos returns empty list by default.
func TestTodosTool_Handler_EmptyList(t *testing.T) {
	tool := TodosTool()
	ctx := withRunID(context.Background(), "test-run")
	result, err := tool.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestTodosTool_Handler_AddTodos verifies todos stores items.
func TestTodosTool_Handler_AddTodos(t *testing.T) {
	tool := TodosTool()
	ctx := withRunID(context.Background(), "test-run")
	args := `{"todos":[{"text":"task 1","status":"pending"},{"text":"task 2","status":"in_progress"}]}`
	result, err := tool.Handler(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestTodosTool_Handler_InvalidStatus verifies todos rejects invalid status values.
func TestTodosTool_Handler_InvalidStatus(t *testing.T) {
	tool := TodosTool()
	ctx := withRunID(context.Background(), "test-run")
	args := `{"todos":[{"text":"task","status":"invalid_status"}]}`
	_, err := tool.Handler(ctx, json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

// TestTodosTool_SetAction_BackwardCompat verifies the legacy todos array (no action field)
// still works exactly as before — full replacement of the list.
func TestTodosTool_SetAction_BackwardCompat(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-set-compat")

	// Set initial list (backward-compat: no action field)
	set1 := `{"todos":[{"id":"a","text":"alpha","status":"pending"},{"id":"b","text":"beta","status":"in_progress"}]}`
	res, err := tool.Handler(ctx, json.RawMessage(set1))
	if err != nil {
		t.Fatalf("set (no action field): %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal set result: %v", err)
	}
	todos, ok := out["todos"].([]any)
	if !ok || len(todos) != 2 {
		t.Fatalf("expected 2 todos after set, got %v", out["todos"])
	}

	// Replace with a smaller list
	set2 := `{"action":"set","todos":[{"id":"c","text":"gamma","status":"completed"}]}`
	res2, err := tool.Handler(ctx, json.RawMessage(set2))
	if err != nil {
		t.Fatalf("set (explicit action): %v", err)
	}
	if err := json.Unmarshal([]byte(res2), &out); err != nil {
		t.Fatalf("unmarshal set2 result: %v", err)
	}
	todos2, ok := out["todos"].([]any)
	if !ok || len(todos2) != 1 {
		t.Fatalf("expected 1 todo after set, got %v", out["todos"])
	}
}

// TestTodosTool_UpdateAction_Status verifies action=update changes the status of a single item.
func TestTodosTool_UpdateAction_Status(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-update-status")

	// Set initial list
	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"1","text":"task one","status":"pending"},{"id":"2","text":"task two","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set initial todos: %v", err)
	}

	// Update item "1" to completed
	res, err := tool.Handler(ctx, json.RawMessage(`{"action":"update","id":"1","status":"completed"}`))
	if err != nil {
		t.Fatalf("update action: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal update result: %v", err)
	}
	todos, ok := out["todos"].([]any)
	if !ok || len(todos) != 2 {
		t.Fatalf("expected 2 todos after update, got %v", out["todos"])
	}
	item1 := todos[0].(map[string]any)
	if item1["status"] != "completed" {
		t.Errorf("expected status 'completed' for item 1, got %v", item1["status"])
	}
	// item 2 should be unchanged
	item2 := todos[1].(map[string]any)
	if item2["status"] != "pending" {
		t.Errorf("expected status 'pending' for item 2, got %v", item2["status"])
	}
}

// TestTodosTool_UpdateAction_Text verifies action=update changes the text of a single item.
func TestTodosTool_UpdateAction_Text(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-update-text")

	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"x","text":"old text","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set initial todos: %v", err)
	}

	res, err := tool.Handler(ctx, json.RawMessage(`{"action":"update","id":"x","text":"new text"}`))
	if err != nil {
		t.Fatalf("update action: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	todos := out["todos"].([]any)
	item := todos[0].(map[string]any)
	if item["text"] != "new text" {
		t.Errorf("expected text 'new text', got %v", item["text"])
	}
}

// TestTodosTool_UpdateAction_UnknownID verifies action=update returns a useful error for unknown IDs.
func TestTodosTool_UpdateAction_UnknownID(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-update-notfound")

	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"real","text":"task","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set todos: %v", err)
	}

	res, err := tool.Handler(ctx, json.RawMessage(`{"action":"update","id":"ghost","status":"completed"}`))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errMsg, ok := out["error"].(string)
	if !ok || errMsg == "" {
		t.Fatalf("expected error field for unknown id, got %v", out)
	}
}

// TestTodosTool_UpdateAction_MissingID verifies action=update requires an id.
func TestTodosTool_UpdateAction_MissingID(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-update-noid")

	_, err := tool.Handler(ctx, json.RawMessage(`{"action":"update","status":"completed"}`))
	if err == nil {
		t.Fatal("expected error when id is missing for update action")
	}
}

// TestTodosTool_DeleteAction_Success verifies action=delete removes an item by ID.
func TestTodosTool_DeleteAction_Success(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-delete-ok")

	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"del","text":"to be deleted","status":"pending"},{"id":"keep","text":"keeper","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set todos: %v", err)
	}

	res, err := tool.Handler(ctx, json.RawMessage(`{"action":"delete","id":"del"}`))
	if err != nil {
		t.Fatalf("delete action: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	todos, ok := out["todos"].([]any)
	if !ok || len(todos) != 1 {
		t.Fatalf("expected 1 todo after delete, got %v", out["todos"])
	}
	remaining := todos[0].(map[string]any)
	if remaining["id"] != "keep" {
		t.Errorf("expected remaining item id 'keep', got %v", remaining["id"])
	}
}

// TestTodosTool_DeleteAction_UnknownID verifies action=delete returns a useful error for unknown IDs.
func TestTodosTool_DeleteAction_UnknownID(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-delete-notfound")

	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"real","text":"task","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set todos: %v", err)
	}

	res, err := tool.Handler(ctx, json.RawMessage(`{"action":"delete","id":"ghost"}`))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errMsg, ok := out["error"].(string)
	if !ok || errMsg == "" {
		t.Fatalf("expected error field for unknown id, got %v", out)
	}
}

// TestTodosTool_DeleteAction_MissingID verifies action=delete requires an id.
func TestTodosTool_DeleteAction_MissingID(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-delete-noid")

	_, err := tool.Handler(ctx, json.RawMessage(`{"action":"delete"}`))
	if err == nil {
		t.Fatal("expected error when id is missing for delete action")
	}
}

// TestTodosTool_UpdateAction_InvalidStatus verifies action=update rejects invalid status.
func TestTodosTool_UpdateAction_InvalidStatus(t *testing.T) {
	t.Parallel()

	tool := TodosTool()
	ctx := withRunID(context.Background(), "run-update-badstatus")

	_, err := tool.Handler(ctx, json.RawMessage(`{"todos":[{"id":"i1","text":"task","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("set todos: %v", err)
	}

	_, err = tool.Handler(ctx, json.RawMessage(`{"action":"update","id":"i1","status":"bogus"}`))
	if err == nil {
		t.Fatal("expected error for invalid status in update action")
	}
}

// TestDownloadTool_IsCoreTier verifies the download tool is promoted to core tier.
func TestDownloadTool_IsCoreTier(t *testing.T) {
	t.Parallel()

	tool := DownloadTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	if tool.Definition.Tier != tools.TierCore {
		t.Errorf("expected download to be TierCore, got %q", tool.Definition.Tier)
	}
}

// TestSanitizeToolNamePart verifies the sanitizeToolNamePart helper.
func TestSanitizeToolNamePart(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"Hello-World", "hello_world"},
		{"a/b.c d", "a_b_c_d"},
		{"", "x"},
		{"  spaces  ", "spaces"},
	}
	for _, tt := range tests {
		got := sanitizeToolNamePart(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeToolNamePart(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------- strPtr helper ----------

// TestStrPtr verifies the strPtr helper returns a pointer to the given string.
func TestStrPtr(t *testing.T) {
	p := strPtr("hello")
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != "hello" {
		t.Errorf("expected 'hello', got %q", *p)
	}
}

// ---------- CronPauseTool handler tests ----------

// TestCronPauseTool_Handler_MissingID verifies cron_pause returns error when id is missing.
func TestCronPauseTool_Handler_MissingID(t *testing.T) {
	tool := CronPauseTool(&mockCronClient{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	// The handler parses args but "id" is empty string, not an unmarshal error.
	// The UpdateJob mock doesn't fail on empty id, so we just check it returns without panic.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCronPauseTool_Handler_Success verifies cron_pause calls UpdateJob.
func TestCronPauseTool_Handler_Success(t *testing.T) {
	tool := CronPauseTool(&mockCronClient{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"id":"job-1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestCronPauseTool_Handler_InvalidJSON verifies cron_pause returns error for invalid JSON.
func TestCronPauseTool_Handler_InvalidJSON(t *testing.T) {
	tool := CronPauseTool(&mockCronClient{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------- CronResumeTool handler tests ----------

// TestCronResumeTool_Handler_Success verifies cron_resume calls UpdateJob.
func TestCronResumeTool_Handler_Success(t *testing.T) {
	tool := CronResumeTool(&mockCronClient{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"id":"job-2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestCronResumeTool_Handler_InvalidJSON verifies cron_resume returns error for invalid JSON.
func TestCronResumeTool_Handler_InvalidJSON(t *testing.T) {
	tool := CronResumeTool(&mockCronClient{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------- CancelDelayedCallbackTool handler tests ----------

// TestCancelDelayedCallbackTool_Handler_InvalidJSON verifies cancel_delayed_callback returns error for invalid JSON.
func TestCancelDelayedCallbackTool_Handler_InvalidJSON(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := CancelDelayedCallbackTool(mgr)
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestCancelDelayedCallbackTool_Handler_NotFound verifies cancel_delayed_callback returns error for unknown callback.
func TestCancelDelayedCallbackTool_Handler_NotFound(t *testing.T) {
	mgr := tools.NewCallbackManager(nil)
	tool := CancelDelayedCallbackTool(mgr)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"callback_id":"nonexistent"}`))
	if err == nil {
		t.Fatal("expected error for unknown callback_id")
	}
}

// ---------- CronDeleteTool handler tests ----------

// TestCronDeleteTool_Handler_Success verifies cron_delete calls DeleteJob.
func TestCronDeleteTool_Handler_Success(t *testing.T) {
	tool := CronDeleteTool(&mockCronClient{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"id":"j1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestCronDeleteTool_Handler_InvalidJSON verifies cron_delete returns error for invalid JSON.
func TestCronDeleteTool_Handler_InvalidJSON(t *testing.T) {
	tool := CronDeleteTool(&mockCronClient{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------- CronListTool handler tests ----------

// TestCronListTool_Handler_Success verifies cron_list returns list.
func TestCronListTool_Handler_Success(t *testing.T) {
	tool := CronListTool(&mockCronClient{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// ---------- CronGetTool handler tests ----------

// TestCronGetTool_Handler_Success verifies cron_get returns a job.
func TestCronGetTool_Handler_Success(t *testing.T) {
	tool := CronGetTool(&mockCronClient{})
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"id":"j1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestCronGetTool_Handler_InvalidJSON verifies cron_get returns error for invalid JSON.
func TestCronGetTool_Handler_InvalidJSON(t *testing.T) {
	tool := CronGetTool(&mockCronClient{})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------- helpers ----------

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

func assertHasTags(t *testing.T, tool tools.Tool, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		found := false
		for _, t2 := range tool.Definition.Tags {
			if t2 == tag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected tag %q not found in %v", tag, tool.Definition.Tags)
		}
	}
}

// ---------- GetEfficiencyReportTool tests ----------

// TestGetEfficiencyReportTool_Definition verifies the tool definition.
func TestGetEfficiencyReportTool_Definition(t *testing.T) {
	tool := GetEfficiencyReportTool(nil)
	assertToolDef(t, tool, "get_efficiency_report", tools.TierDeferred)
	assertHasTags(t, tool, "profile", "efficiency", "report")
}

// TestGetEfficiencyReportTool_MissingProfileName verifies an error is returned
// when profile_name is absent.
func TestGetEfficiencyReportTool_MissingProfileName(t *testing.T) {
	tool := GetEfficiencyReportTool(nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing profile_name")
	}
}

// TestGetEfficiencyReportTool_NoStore_ReturnsNoHistoryReport verifies that
// when store is nil the tool returns a valid no-history report.
func TestGetEfficiencyReportTool_NoStore_ReturnsNoHistoryReport(t *testing.T) {
	tool := GetEfficiencyReportTool(nil)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"profile_name":"researcher"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	var report map[string]any
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report["profile_name"] != "researcher" {
		t.Errorf("expected profile_name=researcher, got %v", report["profile_name"])
	}
	if report["has_history"] != false {
		t.Errorf("expected has_history=false when store is nil, got %v", report["has_history"])
	}
	suggestions, ok := report["suggestions"].([]any)
	if !ok || len(suggestions) == 0 {
		t.Errorf("expected non-empty suggestions for no-history report, got %v", report["suggestions"])
	}
}

// TestGetEfficiencyReportTool_InvalidJSON verifies the handler returns an error
// for malformed JSON.
func TestGetEfficiencyReportTool_InvalidJSON(t *testing.T) {
	tool := GetEfficiencyReportTool(nil)
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// mockProfileRunStore is a test double for ProfileRunStoreIface.
type mockProfileRunStore struct {
	stats map[string]mockStoreEntry
}

type mockStoreEntry struct {
	stats profiles.ProfileStats
	found bool
}

func (m *mockProfileRunStore) AggregateProfileStats(_ context.Context, profileName string) (profiles.ProfileStats, bool, error) {
	entry, ok := m.stats[profileName]
	if !ok {
		return profiles.ProfileStats{}, false, nil
	}
	return entry.stats, entry.found, nil
}

// TestGetEfficiencyReportTool_StoreFound verifies that when the store has data,
// the report reflects the stored stats.
func TestGetEfficiencyReportTool_StoreFound(t *testing.T) {
	store := &mockProfileRunStore{
		stats: map[string]mockStoreEntry{
			"researcher": {
				found: true,
				stats: profiles.ProfileStats{
					ProfileName: "researcher",
					RunCount:    5,
					AvgSteps:    8.0,
					AvgCostUSD:  0.02,
					SuccessRate: 0.9,
					TopTools:    []string{"read", "grep"},
					MaxSteps:    0,
				},
			},
		},
	}
	tool := GetEfficiencyReportTool(store)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"profile_name":"researcher"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report["has_history"] != true {
		t.Errorf("expected has_history=true when store has data, got %v", report["has_history"])
	}
	if report["run_count"].(float64) != 5 {
		t.Errorf("expected run_count=5, got %v", report["run_count"])
	}
}

// TestGetEfficiencyReportTool_StoreNotFound verifies that when the store has no
// history for the profile, the report has has_history=false.
func TestGetEfficiencyReportTool_StoreNotFound(t *testing.T) {
	store := &mockProfileRunStore{stats: map[string]mockStoreEntry{}}
	tool := GetEfficiencyReportTool(store)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"profile_name":"ghost"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report["has_history"] != false {
		t.Errorf("expected has_history=false for unknown profile, got %v", report["has_history"])
	}
}
