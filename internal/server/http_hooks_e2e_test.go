package server

// http_hooks_e2e_test.go — end-to-end proof that a config-driven hook file,
// loaded trust-aware and registered into RunnerConfig exactly the way
// harnessd startup does it, denies a tool call through the real HTTP run API.
// Key-free: scripted fakeprovider, no network, no LLM, no API key.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/hooks"
)

// hooksE2EEnv builds home/workspace dirs with a project hook file containing
// the given script, plus a user-global hooks dir.
func hooksE2EEnv(t *testing.T, scriptBody string) (home, workspace, hookPath string) {
	t.Helper()
	home = t.TempDir()
	workspace = t.TempDir()
	scriptPath := filepath.Join(home, "deny.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	hooksDir := hooks.ProjectHooksDir(workspace)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath = filepath.Join(hooksDir, "deny-all.json")
	def := map[string]any{
		"name": "deny-all", "event": "pre_tool_use", "kind": "command",
		"command": []string{"/bin/sh", scriptPath},
	}
	raw, _ := json.Marshal(def)
	if err := os.WriteFile(hookPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, workspace, hookPath
}

// loadHooksLikeHarnessd reproduces the harnessd startup path: trust-aware
// load over user-global + project dirs, Build adapters, append to slices.
func loadHooksLikeHarnessd(t *testing.T, home, workspace string) hooks.Adapters {
	t.Helper()
	store, err := hooks.LoadTrustStore(hooks.TrustStorePath(home))
	if err != nil {
		t.Fatal(err)
	}
	userDir := hooks.UserHooksDir(home)
	defs, _ := hooks.LoadWithOptions(hooks.LoadOptions{
		UserDir: userDir, TrustStore: store,
	}, userDir, hooks.ProjectHooksDir(workspace))
	return hooks.Build(defs, nil)
}

// driveDenyRun starts a server whose runner has the given adapters and a
// provider that calls echo_tool once, then completes. It POSTs a run and
// polls to terminal, returning (final status, provider).
func driveDenyRun(t *testing.T, adapters hooks.Adapters) (string, *fakeprovider.Provider) {
	t.Helper()
	prov := fakeprovider.New([]fakeprovider.Turn{
		{ToolCalls: []harness.ToolCall{{ID: "call_1", Name: "echo_tool", Arguments: `{"message":"hi"}`}}},
		{Content: "done"},
	})

	reg := harness.NewRegistry()
	_ = reg.Register(harness.ToolDefinition{
		Name:        "echo_tool",
		Description: "echoes input",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, raw json.RawMessage) (string, error) {
		return "echo-executed", nil
	})

	runner := harness.NewRunner(prov, reg, harness.RunnerConfig{
		DefaultModel:    "test-model",
		MaxSteps:        5,
		PreToolUseHooks: adapters.PreToolUse,
	})
	defer func() { _ = runner.Shutdown(context.Background()) }()

	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"go"}`))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("POST /v1/runs: got %d: %s", res.StatusCode, body)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		getRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err != nil {
			t.Fatalf("GET run: %v", err)
		}
		var body struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(getRes.Body).Decode(&body)
		getRes.Body.Close()
		if body.Status == "completed" || body.Status == "failed" || body.Status == "cancelled" {
			return body.Status, prov
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("run did not reach terminal status")
	return "", nil
}

// TestConfigDrivenHookDenyThroughServer: with a TRUSTED deny-all project
// hook, a run through the HTTP API blocks the tool and the deny reason
// reaches the LLM's next request.
func TestConfigDrivenHookDenyThroughServer(t *testing.T) {
	t.Parallel()
	home, workspace, hookPath := hooksE2EEnv(t, `echo '{"decision":"deny","reason":"blocked by config hook"}'`)

	// Trust the project hook (as `harnesscli hooks trust` would).
	store, err := hooks.LoadTrustStore(hooks.TrustStorePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatal(err)
	}

	adapters := loadHooksLikeHarnessd(t, home, workspace)
	if len(adapters.PreToolUse) != 1 {
		t.Fatalf("expected 1 pre-tool adapter, got %d", len(adapters.PreToolUse))
	}

	status, prov := driveDenyRun(t, adapters)
	if status != "completed" {
		t.Fatalf("status: got %q, want completed (deny is not a run failure)", status)
	}
	lastReq, ok := prov.LastRequest()
	if !ok {
		t.Fatal("provider saw no requests")
	}
	found := false
	for _, m := range lastReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "blocked by config hook") {
			found = true
		}
	}
	if !found {
		t.Fatal("deny reason did not reach the LLM as the tool result")
	}
}

// TestUntrustedConfigHookNeverExecutes: the same project hook WITHOUT trust
// is skipped by the startup-equivalent load, so the tool executes normally.
func TestUntrustedConfigHookNeverExecutes(t *testing.T) {
	t.Parallel()
	home, workspace, _ := hooksE2EEnv(t, `echo '{"decision":"deny","reason":"should never fire"}'`)

	adapters := loadHooksLikeHarnessd(t, home, workspace)
	if len(adapters.PreToolUse) != 0 {
		t.Fatalf("untrusted hook produced an adapter: %d", len(adapters.PreToolUse))
	}

	status, prov := driveDenyRun(t, adapters)
	if status != "completed" {
		t.Fatalf("status: got %q", status)
	}
	lastReq, ok := prov.LastRequest()
	if !ok {
		t.Fatal("provider saw no requests")
	}
	found := false
	for _, m := range lastReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "echo-executed") {
			found = true
		}
	}
	if !found {
		t.Fatal("tool did not execute — untrusted hook must not interfere")
	}
}
