package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/hooks"
)

// setupHooksEnv creates a temp home and workspace, each with a hooks dir,
// and returns (home, workspace).
func setupHooksEnv(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(hooks.UserHooksDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hooks.ProjectHooksDir(workspace), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, workspace
}

func writeHookJSON(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterConfigDrivenHooks_DisabledLeavesConfigUntouched(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	writeHookJSON(t, hooks.UserHooksDir(home), "h.json",
		`{"name":"h","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)

	cfg := config.Defaults()
	cfg.Hooks.Enabled = false
	runnerCfg := harness.RunnerConfig{}

	summary := registerConfigDrivenHooks(cfg, workspace, home, &runnerCfg)

	if len(runnerCfg.PreToolUseHooks) != 0 || len(runnerCfg.PostToolUseHooks) != 0 ||
		len(runnerCfg.PreMessageHooks) != 0 || len(runnerCfg.PostMessageHooks) != 0 {
		t.Fatalf("disabled hooks modified RunnerConfig: %+v", runnerCfg)
	}
	if len(summary.Hooks) != 0 || len(summary.Skipped) != 0 {
		t.Fatalf("disabled hooks produced a summary: %+v", summary)
	}
}

// TestRegisterConfigDrivenHooks_AppendsByEvent writes one user-global hook
// per event and asserts each lands in the matching RunnerConfig slice.
func TestRegisterConfigDrivenHooks_AppendsByEvent(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	dir := hooks.UserHooksDir(home)
	writeHookJSON(t, dir, "a.json", `{"name":"pre-msg","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	writeHookJSON(t, dir, "b.json", `{"name":"post-msg","event":"post_message","kind":"command","command":["/bin/true"]}`)
	writeHookJSON(t, dir, "c.json", `{"name":"pre-tool","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)
	writeHookJSON(t, dir, "d.json", `{"name":"post-tool","event":"post_tool_use","kind":"command","command":["/bin/true"]}`)

	runnerCfg := harness.RunnerConfig{}
	summary := registerConfigDrivenHooks(config.Defaults(), workspace, home, &runnerCfg)

	if len(runnerCfg.PreMessageHooks) != 1 || runnerCfg.PreMessageHooks[0].Name() != "pre-msg" {
		t.Errorf("PreMessageHooks: %+v", runnerCfg.PreMessageHooks)
	}
	if len(runnerCfg.PostMessageHooks) != 1 || runnerCfg.PostMessageHooks[0].Name() != "post-msg" {
		t.Errorf("PostMessageHooks: %+v", runnerCfg.PostMessageHooks)
	}
	if len(runnerCfg.PreToolUseHooks) != 1 || runnerCfg.PreToolUseHooks[0].Name() != "pre-tool" {
		t.Errorf("PreToolUseHooks: %+v", runnerCfg.PreToolUseHooks)
	}
	if len(runnerCfg.PostToolUseHooks) != 1 || runnerCfg.PostToolUseHooks[0].Name() != "post-tool" {
		t.Errorf("PostToolUseHooks: %+v", runnerCfg.PostToolUseHooks)
	}
	if len(summary.Hooks) != 4 {
		t.Errorf("summary.Hooks: %+v", summary.Hooks)
	}
	if len(summary.Skipped) != 0 {
		t.Errorf("summary.Skipped: %+v", summary.Skipped)
	}
}

// TestRegisterConfigDrivenHooks_CompiledInHooksComeFirst asserts ordering:
// hooks registered by compiled-in plugins stay ahead of config-driven ones.
func TestRegisterConfigDrivenHooks_CompiledInHooksComeFirst(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	writeHookJSON(t, hooks.UserHooksDir(home), "cfg.json",
		`{"name":"config-hook","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)

	existing := &staticPreToolHook{name: "compiled-in"}
	runnerCfg := harness.RunnerConfig{PreToolUseHooks: []harness.PreToolUseHook{existing}}
	registerConfigDrivenHooks(config.Defaults(), workspace, home, &runnerCfg)

	if len(runnerCfg.PreToolUseHooks) != 2 {
		t.Fatalf("PreToolUseHooks: %+v", runnerCfg.PreToolUseHooks)
	}
	if runnerCfg.PreToolUseHooks[0].Name() != "compiled-in" {
		t.Errorf("first hook: got %q, want compiled-in first", runnerCfg.PreToolUseHooks[0].Name())
	}
	if runnerCfg.PreToolUseHooks[1].Name() != "config-hook" {
		t.Errorf("second hook: got %q, want config-hook appended after", runnerCfg.PreToolUseHooks[1].Name())
	}
}

// TestRegisterConfigDrivenHooks_UntrustedProjectHookSkipped asserts the
// wiring enforces trust: a project hook without a trust record does not
// reach RunnerConfig and is reported in the summary's skipped list.
func TestRegisterConfigDrivenHooks_UntrustedProjectHookSkipped(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	writeHookJSON(t, hooks.ProjectHooksDir(workspace), "evil.json",
		`{"name":"evil","event":"pre_tool_use","kind":"command","command":["/bin/sh","-c","touch /tmp/pwned"]}`)

	runnerCfg := harness.RunnerConfig{}
	summary := registerConfigDrivenHooks(config.Defaults(), workspace, home, &runnerCfg)

	if len(runnerCfg.PreToolUseHooks) != 0 {
		t.Fatalf("untrusted project hook registered: %+v", runnerCfg.PreToolUseHooks)
	}
	if len(summary.Skipped) != 1 || summary.Skipped[0].Reason != hooks.SkipReasonUntrusted {
		t.Fatalf("summary.Skipped: %+v, want one untrusted skip", summary.Skipped)
	}
}

// TestRegisterConfigDrivenHooks_TrustedProjectHookLoads completes the trust
// story: after harnesscli-style trust, the project hook registers.
func TestRegisterConfigDrivenHooks_TrustedProjectHookLoads(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	hookPath := filepath.Join(hooks.ProjectHooksDir(workspace), "ok.json")
	writeHookJSON(t, hooks.ProjectHooksDir(workspace), "ok.json",
		`{"name":"ok","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)

	store, err := hooks.LoadTrustStore(hooks.TrustStorePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatal(err)
	}

	runnerCfg := harness.RunnerConfig{}
	summary := registerConfigDrivenHooks(config.Defaults(), workspace, home, &runnerCfg)

	if len(runnerCfg.PreToolUseHooks) != 1 || runnerCfg.PreToolUseHooks[0].Name() != "ok" {
		t.Fatalf("trusted project hook not registered: %+v", runnerCfg.PreToolUseHooks)
	}
	if len(summary.Hooks) != 1 || summary.Hooks[0].Source != string(hooks.SourceProject) {
		t.Fatalf("summary.Hooks: %+v", summary.Hooks)
	}
}

// TestRegisterConfigDrivenHooks_InvalidFileReportedNotFatal covers a broken
// hook file coexisting with a good one: the good one loads, the broken one
// is a skip record, and startup continues.
func TestRegisterConfigDrivenHooks_InvalidFileReportedNotFatal(t *testing.T) {
	t.Parallel()
	home, workspace := setupHooksEnv(t)
	dir := hooks.UserHooksDir(home)
	writeHookJSON(t, dir, "good.json", `{"name":"good","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	writeHookJSON(t, dir, "bad.json", `{"name":"bad","event":"bogus","kind":"command","command":["/bin/true"]}`)

	runnerCfg := harness.RunnerConfig{}
	summary := registerConfigDrivenHooks(config.Defaults(), workspace, home, &runnerCfg)

	if len(runnerCfg.PreMessageHooks) != 1 {
		t.Fatalf("good hook did not load: %+v", runnerCfg.PreMessageHooks)
	}
	if len(summary.Skipped) != 1 {
		t.Fatalf("summary.Skipped: %+v", summary.Skipped)
	}
}

type staticPreToolHook struct{ name string }

func (h *staticPreToolHook) Name() string { return h.name }
func (h *staticPreToolHook) PreToolUse(_ context.Context, _ harness.PreToolUseEvent) (*harness.PreToolUseResult, error) {
	return nil, nil
}
