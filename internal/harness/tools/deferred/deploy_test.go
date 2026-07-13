package deferred

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/deploy"
	tools "go-agent-harness/internal/harness/tools"
)

// --- mock Platform ---

type mockPlatform struct {
	name         string
	detectResult bool
	detectErr    error
	deployResult *deploy.DeployResult
	deployErr    error
	statusResult *deploy.DeployStatus
	statusErr    error
	logsOutput   string
	logsErr      error
	rollbackErr  error
	teardownErr  error
}

func (m *mockPlatform) Name() string { return m.name }

func (m *mockPlatform) Detect(_ context.Context, _ string) (bool, error) {
	return m.detectResult, m.detectErr
}

func (m *mockPlatform) Deploy(_ context.Context, _ string, opts deploy.DeployOpts) (*deploy.DeployResult, error) {
	if m.deployErr != nil {
		return nil, m.deployErr
	}
	if m.deployResult != nil {
		return m.deployResult, nil
	}
	return &deploy.DeployResult{
		Platform:  m.name,
		URL:       "https://example.com",
		Timestamp: time.Now(),
		Logs:      "deployed",
	}, nil
}

func (m *mockPlatform) Status(_ context.Context, _ string) (*deploy.DeployStatus, error) {
	if m.statusErr != nil {
		return &deploy.DeployStatus{State: "failed"}, m.statusErr
	}
	if m.statusResult != nil {
		return m.statusResult, nil
	}
	return &deploy.DeployStatus{State: "running", URL: "https://example.com"}, nil
}

func (m *mockPlatform) Logs(_ context.Context, _ string, _ bool) (io.Reader, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	return strings.NewReader(m.logsOutput), nil
}

func (m *mockPlatform) Rollback(_ context.Context, _ string, _ string) error {
	return m.rollbackErr
}

func (m *mockPlatform) Teardown(_ context.Context, _ string) error {
	return m.teardownErr
}

// --- helpers ---

// writeDeployFile creates a file with empty content at dir/name.
func writeDeployFile(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// --- tests ---

// TestDeployTool_Definition verifies tool metadata.
func TestDeployTool_Definition(t *testing.T) {
	reg := DefaultDeployPlatformRegistry()
	tool := DeployTool(reg, t.TempDir())
	assertToolDef(t, tool, "deploy", tools.TierDeferred)
	assertHasTags(t, tool, "deploy", "cloud")
}

// TestDeployTool_MissingAction verifies error when action is absent.
func TestDeployTool_MissingAction(t *testing.T) {
	reg := DefaultDeployPlatformRegistry()
	tool := DeployTool(reg, t.TempDir())
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

// TestDeployTool_InvalidJSON verifies error for unparseable JSON.
func TestDeployTool_InvalidJSON(t *testing.T) {
	reg := DefaultDeployPlatformRegistry()
	tool := DeployTool(reg, t.TempDir())
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestDeployTool_DetectAction verifies the detect action returns the platform name.
func TestDeployTool_DetectAction(t *testing.T) {
	dir := t.TempDir()
	writeDeployFile(t, dir, "fly.toml")
	reg := DefaultDeployPlatformRegistry()
	tool := DeployTool(reg, dir)
	args, _ := json.Marshal(map[string]string{"action": "detect"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "flyio") {
		t.Errorf("expected 'flyio' in result, got %q", result)
	}
}

// TestDeployTool_DetectAction_NoPlatform verifies detect returns error when no config found.
func TestDeployTool_DetectAction_NoPlatform(t *testing.T) {
	dir := t.TempDir()
	reg := DefaultDeployPlatformRegistry()
	tool := DeployTool(reg, dir)
	args, _ := json.Marshal(map[string]string{"action": "detect"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error when no platform detected")
	}
}

// TestDeployTool_DeployAction verifies deploy action returns structured result.
func TestDeployTool_DeployAction(t *testing.T) {
	mock := &mockPlatform{
		name: "railway",
		deployResult: &deploy.DeployResult{
			Platform: "railway",
			URL:      "https://myapp.railway.app",
		},
	}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"action":      "deploy",
		"platform":    "railway",
		"environment": "production",
	})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "railway.app") {
		t.Errorf("expected URL in result, got %q", result)
	}
}

// TestDeployTool_DeployDryRun verifies dry-run is passed through without error.
func TestDeployTool_DeployDryRun(t *testing.T) {
	mock := &mockPlatform{name: "railway"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"action":   "deploy",
		"platform": "railway",
		"dry_run":  true,
	})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDeployTool_DeployError propagates platform deploy errors.
func TestDeployTool_DeployError(t *testing.T) {
	mock := &mockPlatform{name: "railway", deployErr: errors.New("auth failed")}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "deploy", "platform": "railway"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error from platform")
	}
}

// TestDeployTool_StatusAction verifies status returns deployment state.
func TestDeployTool_StatusAction(t *testing.T) {
	mock := &mockPlatform{
		name: "flyio",
		statusResult: &deploy.DeployStatus{
			State: "running",
			URL:   "https://myapp.fly.dev",
		},
	}
	reg := DeployPlatformRegistry{"flyio": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "status", "platform": "flyio"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "running") {
		t.Errorf("expected 'running' in result, got %q", result)
	}
}

// TestDeployTool_StatusError propagates platform status errors.
func TestDeployTool_StatusError(t *testing.T) {
	mock := &mockPlatform{name: "flyio", statusErr: errors.New("not found")}
	reg := DeployPlatformRegistry{"flyio": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "status", "platform": "flyio"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error from platform")
	}
}

// TestDeployTool_LogsAction verifies logs are returned.
func TestDeployTool_LogsAction(t *testing.T) {
	mock := &mockPlatform{name: "railway", logsOutput: "2024-01-01 server started"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "logs", "platform": "railway"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "server started") {
		t.Errorf("expected log content in result, got %q", result)
	}
}

// TestDeployTool_LogsError propagates platform logs errors.
func TestDeployTool_LogsError(t *testing.T) {
	mock := &mockPlatform{name: "railway", logsErr: errors.New("no logs")}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "logs", "platform": "railway"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error from platform")
	}
}

// TestDeployTool_UnknownAction verifies error for invalid action.
func TestDeployTool_UnknownAction(t *testing.T) {
	mock := &mockPlatform{name: "railway"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "bogus", "platform": "railway"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// TestDeployTool_UnknownPlatform verifies error for unregistered platform.
func TestDeployTool_UnknownPlatform(t *testing.T) {
	reg := DeployPlatformRegistry{}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "deploy", "platform": "unknown"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for unknown platform")
	}
}

// TestDeployTool_AutoDetect verifies platform is auto-detected when omitted.
func TestDeployTool_AutoDetect(t *testing.T) {
	dir := t.TempDir()
	writeDeployFile(t, dir, "fly.toml")
	mock := &mockPlatform{name: "flyio"}
	reg := DeployPlatformRegistry{"flyio": mock}
	tool := DeployTool(reg, dir)
	args, _ := json.Marshal(map[string]any{"action": "status"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDeployTool_AutoDetectFails verifies error when auto-detection fails.
func TestDeployTool_AutoDetectFails(t *testing.T) {
	dir := t.TempDir() // empty workspace, no config files
	mock := &mockPlatform{name: "flyio"}
	reg := DeployPlatformRegistry{"flyio": mock}
	tool := DeployTool(reg, dir)
	args, _ := json.Marshal(map[string]any{"action": "deploy"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error when auto-detection fails")
	}
}

// TestDeployTool_DefaultEnvironment verifies production is used when environment is omitted.
func TestDeployTool_DefaultEnvironment(t *testing.T) {
	mock := &mockPlatform{name: "railway"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "deploy", "platform": "railway"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDeployTool_WorkspaceOverride verifies custom workspace path is used.
func TestDeployTool_WorkspaceOverride(t *testing.T) {
	customDir := t.TempDir()
	writeDeployFile(t, customDir, "railway.json")
	mock := &mockPlatform{name: "railway"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir()) // default workspace has no config
	args, _ := json.Marshal(map[string]any{
		"action":    "detect",
		"workspace": customDir,
	})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "railway") {
		t.Errorf("expected 'railway' in result, got %q", result)
	}
}

// TestDefaultDeployPlatformRegistry verifies the built-in registry has expected adapters.
func TestDefaultDeployPlatformRegistry(t *testing.T) {
	reg := DefaultDeployPlatformRegistry()
	if _, ok := reg["railway"]; !ok {
		t.Error("expected 'railway' in default registry")
	}
	if _, ok := reg["flyio"]; !ok {
		t.Error("expected 'flyio' in default registry")
	}
}

// TestDeployTool_EmptyRegistry verifies error when registry is empty and platform given.
func TestDeployTool_EmptyRegistry(t *testing.T) {
	reg := DeployPlatformRegistry{}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{"action": "status", "platform": "railway"})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for empty registry")
	}
}

// TestDeployTool_ForceFlag verifies force=true passes through without error.
func TestDeployTool_ForceFlag(t *testing.T) {
	mock := &mockPlatform{name: "railway"}
	reg := DeployPlatformRegistry{"railway": mock}
	tool := DeployTool(reg, t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"action":   "deploy",
		"platform": "railway",
		"force":    true,
	})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestDeployTool_DoesNotAdvertiseUnbackedPlatforms guards the honesty fix: the
// tool must not tag itself with platforms it has no deploy adapter for.
func TestDeployTool_DoesNotAdvertiseUnbackedPlatforms(t *testing.T) {
	tool := DeployTool(DefaultDeployPlatformRegistry(), t.TempDir())
	for _, tag := range tool.Definition.Tags {
		if tag == "vercel" || tag == "cloudflare" {
			t.Errorf("deploy tool advertises %q but has no adapter for it; tags=%v", tag, tool.Definition.Tags)
		}
	}
}

// TestDeployTool_DetectReportsDeployableSubset verifies the detect action
// separates what it can DETECT from what it can DEPLOY: a vercel project is
// detected (in "all") but excluded from "deployable" since there is no adapter.
func TestDeployTool_DetectReportsDeployableSubset(t *testing.T) {
	dir := t.TempDir()
	writeDeployFile(t, dir, "fly.toml")    // deployable
	writeDeployFile(t, dir, "vercel.json") // detected only
	tool := DeployTool(DefaultDeployPlatformRegistry(), dir)
	args, _ := json.Marshal(map[string]string{"action": "detect"})
	result, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	var parsed struct {
		All        []string `json:"all"`
		Deployable []string `json:"deployable"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("parse result %q: %v", result, err)
	}
	if !containsStr(parsed.All, "vercel") {
		t.Errorf("expected vercel in detected 'all', got %v", parsed.All)
	}
	if containsStr(parsed.Deployable, "vercel") {
		t.Errorf("vercel must not appear in 'deployable' (no adapter), got %v", parsed.Deployable)
	}
	if !containsStr(parsed.Deployable, "flyio") {
		t.Errorf("expected flyio in 'deployable', got %v", parsed.Deployable)
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
