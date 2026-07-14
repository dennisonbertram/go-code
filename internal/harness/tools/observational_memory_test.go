package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	om "go-agent-harness/internal/observationalmemory"
)

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

func TestObservationalMemoryToolStatusWithoutManager(t *testing.T) {
	tool := observationalMemoryTool(t.TempDir(), nil, nil, SandboxScopeUnrestricted)
	out, err := tool.Handler(context.WithValue(context.Background(), ContextKeyRunID, "run_1"), json.RawMessage(`{"action":"status"}`))
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
	tool := observationalMemoryTool(workspace, stub, nil, SandboxScopeUnrestricted)
	ctx := context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{RunID: "run_1", TenantID: "default", ConversationID: "conv", AgentID: "agent"})
	ctx = context.WithValue(ctx, ContextKeyRunID, "run_1")
	out, err := tool.Handler(ctx, json.RawMessage(`{"action":"export","export":{"format":"json","path":"exports/memory.json"}}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	export, ok := payload["export"].(map[string]any)
	if !ok {
		t.Fatalf("missing export payload: %#v", payload)
	}
	rel := export["path"].(string)
	if rel != "exports/memory.json" {
		t.Fatalf("unexpected export path: %q", rel)
	}
	content, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(content), "export-content") {
		t.Fatalf("unexpected export content: %q", string(content))
	}
}

func TestObservationalMemoryToolEnableUsesConfig(t *testing.T) {
	workspace := t.TempDir()
	stub := &memoryStub{status: om.Status{Mode: om.ModeLocalCoordinator, MemoryID: "default|conv|agent", Scope: om.ScopeKey{TenantID: "default", ConversationID: "conv", AgentID: "agent"}, Enabled: false, UpdatedAt: time.Now().UTC()}}
	tool := observationalMemoryTool(workspace, stub, nil, SandboxScopeUnrestricted)
	ctx := context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{RunID: "run_1", TenantID: "default", ConversationID: "conv", AgentID: "agent"})
	ctx = context.WithValue(ctx, ContextKeyRunID, "run_1")
	out, err := tool.Handler(ctx, json.RawMessage(`{"action":"enable","config":{"observe_min_tokens":11,"snippet_max_tokens":22,"reflect_threshold_tokens":33}}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if stub.lastConfig == nil {
		t.Fatalf("expected config to be forwarded")
	}
	if stub.lastConfig.ObserveMinTokens != 11 || stub.lastConfig.SnippetMaxTokens != 22 || stub.lastConfig.ReflectThresholdTokens != 33 {
		t.Fatalf("unexpected forwarded config: %+v", stub.lastConfig)
	}
	if !strings.Contains(out, `"enabled":true`) {
		t.Fatalf("expected enabled status in output: %s", out)
	}
}

func TestObservationalMemoryToolReviewCallsRunner(t *testing.T) {
	workspace := t.TempDir()
	stub := &memoryStub{
		status:   om.Status{Mode: om.ModeLocalCoordinator, MemoryID: "default|conv|agent", Scope: om.ScopeKey{TenantID: "default", ConversationID: "conv", AgentID: "agent"}, Enabled: true, UpdatedAt: time.Now().UTC()},
		exported: om.ExportResult{Format: "markdown", Content: "# memory\n- note", Bytes: 14},
	}
	runner := &reviewRunnerStub{out: "analysis output"}
	tool := observationalMemoryTool(workspace, stub, runner, SandboxScopeUnrestricted)
	ctx := context.WithValue(context.Background(), ContextKeyRunMetadata, RunMetadata{RunID: "run_1", TenantID: "default", ConversationID: "conv", AgentID: "agent"})
	ctx = context.WithValue(ctx, ContextKeyRunID, "run_1")

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
	tool := observationalMemoryTool(t.TempDir(), &memoryStub{}, nil, SandboxScopeUnrestricted)
	ctx := context.WithValue(context.Background(), ContextKeyRunID, "run_1")
	ctx = context.WithValue(ctx, ContextKeyRunMetadata, RunMetadata{RunID: "run_1", ConversationID: "conv"})
	if _, err := tool.Handler(ctx, json.RawMessage(`{"action":"unknown"}`)); err == nil {
		t.Fatalf("expected unsupported action error")
	}
}

func TestObservationalMemoryToolRequiresRunContext(t *testing.T) {
	tool := observationalMemoryTool(t.TempDir(), &memoryStub{}, nil, SandboxScopeUnrestricted)
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
	}{
		ObserveMinTokens:       7,
		SnippetMaxTokens:       11,
		ReflectThresholdTokens: 13,
	}
	cfg := configFromArgs(in)
	if cfg == nil || cfg.ObserveMinTokens != 7 || cfg.SnippetMaxTokens != 11 || cfg.ReflectThresholdTokens != 13 {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if got := sanitizePathPart(""); got != "default" {
		t.Fatalf("expected default for empty path part, got %q", got)
	}
	got := sanitizePathPart(" tenant/../id value ")
	if strings.Contains(got, "/") || strings.Contains(got, "..") || strings.Contains(got, " ") {
		t.Fatalf("expected sanitized path part, got %q", got)
	}
}
