package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
)

const (
	ContextKeyRunID       = "run_id"
	ContextKeyRunMetadata = "run_metadata"
)

type RunMetadata = tools.RunMetadata

type ContextKey = string

type memoryStub struct {
	status      om.Status
	exported    om.ExportResult
	enabledCall bool
	lastConfig  *om.Config
}

func (m *memoryStub) Close() error  { return nil }
func (m *memoryStub) Mode() om.Mode { return om.ModeLocalCoordinator }
func (m *memoryStub) Status(context.Context, om.ScopeKey) (om.Status, error) {
	if m.status.MemoryID == "" {
		m.status = om.Status{Mode: om.ModeLocalCoordinator, MemoryID: "t|c|a", Scope: om.ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}, Enabled: true, UpdatedAt: time.Now().UTC()}
	}
	return m.status, nil
}
func (m *memoryStub) SetEnabled(_ context.Context, _ om.ScopeKey, enabled bool, cfg *om.Config, _ string, _ string) (om.Status, error) {
	m.enabledCall = enabled
	m.status.Enabled = enabled
	if cfg != nil {
		cfgCopy := *cfg
		m.lastConfig = &cfgCopy
	}
	return m.status, nil
}
func (m *memoryStub) Observe(context.Context, om.ObserveRequest) (om.ObserveResult, error) {
	return om.ObserveResult{Status: m.status}, nil
}
func (m *memoryStub) Snippet(context.Context, om.ScopeKey) (string, om.Status, error) {
	return "", m.status, nil
}
func (m *memoryStub) ReflectNow(context.Context, om.ScopeKey, string, string) (om.Status, error) {
	return m.status, nil
}
func (m *memoryStub) Export(_ context.Context, _ om.ScopeKey, format string) (om.ExportResult, error) {
	if m.exported.Format == "" {
		m.exported = om.ExportResult{Format: format, Content: "export-content", Bytes: len("export-content"), Status: m.status}
	}
	if m.exported.Format == "" {
		m.exported.Format = format
	}
	return m.exported, nil
}

type reviewRunnerStub struct {
	prompt string
	out    string
	err    error
}

func (r *reviewRunnerStub) RunPrompt(_ context.Context, prompt string) (string, error) {
	r.prompt = prompt
	if r.err != nil {
		return "", r.err
	}
	return r.out, nil
}

func observationalMemoryTool(workspaceRoot string, manager om.Manager, runner *reviewRunnerStub) tools.Tool {
	return observationalMemoryToolWithOptions(tools.BuildOptions{WorkspaceRoot: workspaceRoot, MemoryManager: manager, AgentRunner: runner})
}

func observationalMemoryToolWithOptions(opts tools.BuildOptions) tools.Tool {
	return ObservationalMemoryTool(opts)
}

func TestObservationalMemoryToolStatusWithoutManager(t *testing.T) {
	tool := observationalMemoryTool(t.TempDir(), nil, nil)
	out, err := tool.Handler(context.WithValue(context.Background(), tools.ContextKeyRunID, "run_1"), json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	warnings, ok := payload["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected warnings: %#v", payload)
	}
}

func TestObservationalMemoryToolExportWritesFile(t *testing.T) {
	workspace := t.TempDir()
	stub := &memoryStub{status: om.Status{Mode: om.ModeLocalCoordinator, MemoryID: "default|conv|agent", Scope: om.ScopeKey{TenantID: "default", ConversationID: "conv", AgentID: "agent"}, Enabled: true, UpdatedAt: time.Now().UTC()}}
	tool := observationalMemoryTool(workspace, stub, nil)
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run_1", TenantID: "default", ConversationID: "conv", AgentID: "agent"})
	ctx = context.WithValue(ctx, tools.ContextKeyRunID, "run_1")
	out, err := tool.Handler(ctx, json.RawMessage(`{"action":"export","export":{"format":"json","path":"exports/memory.json"}}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	path, ok := payload["export"].(map[string]any)["path"].(string)
	if !ok {
		t.Fatalf("expected export path in payload")
	}
	if path == "" {
		t.Fatalf("expected non-empty export path")
	}
	absPath := filepath.Join(workspace, path)
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("expected export file to exist: %v", err)
	}
}

func TestObservationalMemoryToolReviewCallsRunner(t *testing.T) {
	workspace := t.TempDir()
	stub := &memoryStub{
		status:   om.Status{Mode: om.ModeLocalCoordinator, MemoryID: "default|conv|agent", Scope: om.ScopeKey{TenantID: "default", ConversationID: "conv", AgentID: "agent"}, Enabled: true, UpdatedAt: time.Now().UTC()},
		exported: om.ExportResult{Format: "markdown", Content: "# memory\n- note", Bytes: 14},
	}
	runner := &reviewRunnerStub{out: "analysis output"}
	tool := observationalMemoryTool(workspace, stub, runner)
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run_1", TenantID: "default", ConversationID: "conv", AgentID: "agent"})
	ctx = context.WithValue(ctx, tools.ContextKeyRunID, "run_1")

	out, err := tool.Handler(ctx, json.RawMessage(`{"action":"review","review":{"prompt":"Focus on contradictions"}}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !strings.Contains(runner.prompt, "Focus on contradictions") {
		t.Fatalf("expected custom review prompt, got %q", runner.prompt)
	}
	if !strings.Contains(runner.prompt, "# memory") {
		t.Fatalf("expected exported memory in prompt, got %q", runner.prompt)
	}
	if !strings.Contains(out, "analysis output") {
		t.Fatalf("expected review output in payload: %s", out)
	}
}

func TestObservationalMemoryToolRejectsUnsupportedAction(t *testing.T) {
	tool := observationalMemoryTool(t.TempDir(), &memoryStub{}, nil)
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunID, "run_1")
	ctx = context.WithValue(ctx, tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run_1", ConversationID: "conv"})
	if _, err := tool.Handler(ctx, json.RawMessage(`{"action":"unknown"}`)); err == nil {
		t.Fatalf("expected unsupported action error")
	}
}

func TestObservationalMemoryToolRequiresRunContext(t *testing.T) {
	tool := observationalMemoryTool(t.TempDir(), &memoryStub{}, nil)
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"status"}`)); err == nil {
		t.Fatalf("expected run context error")
	}
}

func TestObservationalMemoryHelpers(t *testing.T) {
	if cfg := configFromArgs(nil); cfg != nil {
		t.Fatalf("expected nil config for nil input")
	}
	in := &struct {
		ObserveMinTokens       int `json:"observe_min_tokens"`
		SnippetMaxTokens       int `json:"snippet_max_tokens"`
		ReflectThresholdTokens int `json:"reflect_threshold_tokens"`
	}{ObserveMinTokens: 1, SnippetMaxTokens: 2, ReflectThresholdTokens: 3}
	cfg := configFromArgs(in)
	if cfg.ObserveMinTokens != 1 {
		t.Fatalf("expected observe_min_tokens 1, got %d", cfg.ObserveMinTokens)
	}
	if cfg.SnippetMaxTokens != 2 {
		t.Fatalf("expected snippet_max_tokens 2, got %d", cfg.SnippetMaxTokens)
	}
	if cfg.ReflectThresholdTokens != 3 {
		t.Fatalf("expected reflect_threshold_tokens 3, got %d", cfg.ReflectThresholdTokens)
	}
}
