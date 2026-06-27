package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

func TestBuildMCPStdioRuntimeCreatesCatalogAndServer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	runtime, err := buildMCPStdioRuntime(workspace)
	if err != nil {
		t.Fatalf("buildMCPStdioRuntime: %v", err)
	}
	if runtime.workspace != workspace {
		t.Fatalf("workspace: got %q", runtime.workspace)
	}
	if len(runtime.catalog) == 0 {
		t.Fatal("expected tool catalog")
	}
	if runtime.server == nil {
		t.Fatal("expected stdio server")
	}
	if got, want := runtime.server.ToolCount(), len(runtime.catalog); got != want {
		t.Fatalf("ToolCount: got %d want %d", got, want)
	}
}

func TestBuildHTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	askUserBroker := harness.NewInMemoryAskUserQuestionBroker(time.Now)
	msgSummarizer := &lazySummarizer{}
	callbackStarter := &callbackRunStarter{}
	baseRegistryOptions := harness.DefaultRegistryOptions{
		ApprovalMode:      harness.ToolApprovalModeFullAuto,
		AskUserBroker:     askUserBroker,
		AskUserTimeout:    time.Minute,
		MessageSummarizer: msgSummarizer,
	}
	tools := harness.NewDefaultRegistryWithOptions(workspace, baseRegistryOptions)

	runtime, err := buildHTTPRuntime(httpRuntimeOptions{
		addr:                "127.0.0.1:0",
		workspace:           workspace,
		provider:            &noopProvider{},
		tools:               tools,
		runnerCfg:           harness.RunnerConfig{DefaultModel: "gpt-4.1-mini"},
		skillLister:         nil,
		baseRegistryOptions: baseRegistryOptions,
		cronClient:          nil,
		modelCatalog:        nil,
		providerRegistry:    nil,
		runStore:            nil,
		triggers:            buildTriggerRuntime(nil, nil),
		callbackStarter:     callbackStarter,
		msgSummarizer:       msgSummarizer,
		skillManager:        nil,
		subagentBaseRef:     "HEAD",
		subagentConfigTOML:  "",
	})
	if err != nil {
		t.Fatalf("buildHTTPRuntime: %v", err)
	}
	if runtime.runner == nil {
		t.Fatal("expected runner")
	}
	if runtime.subagentManager == nil {
		t.Fatal("expected subagent manager")
	}
	if runtime.handler == nil {
		t.Fatal("expected http handler")
	}
	if runtime.httpServer == nil {
		t.Fatal("expected http server")
	}
	if runtime.httpServer.Addr != "127.0.0.1:0" {
		t.Fatalf("httpServer.Addr: got %q", runtime.httpServer.Addr)
	}
	if runtime.httpServer.Handler == nil {
		t.Fatal("expected http server handler")
	}
	if runtime.httpServer.ReadTimeout != 60*time.Second {
		t.Fatalf("ReadTimeout: got %s want 60s", runtime.httpServer.ReadTimeout)
	}
	if runtime.httpServer.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout: got %s want 120s", runtime.httpServer.IdleTimeout)
	}
	if runtime.httpServer.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes: got %d want %d", runtime.httpServer.MaxHeaderBytes, 1<<20)
	}
	if runtime.mcpServer == nil {
		t.Fatal("expected mcp server to be initialized")
	}

	// Verify the /mcp endpoint is reachable via the top-level mux.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
	runtime.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp (initialize): expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the main API still works via the top-level mux.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	runtime.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /healthz: expected 200, got %d", rec.Code)
	}

	callbackStarter.mu.Lock()
	boundRunner := callbackStarter.runner
	callbackStarter.mu.Unlock()
	if boundRunner != runtime.runner {
		t.Fatal("expected callback starter to bind the built runner")
	}
	msgSummarizer.mu.Lock()
	innerSummarizer := msgSummarizer.summarizer
	msgSummarizer.mu.Unlock()
	if innerSummarizer == nil {
		t.Fatal("expected lazy summarizer to bind to the built runner")
	}

}
