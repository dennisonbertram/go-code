package main

// config_reload_test.go — configReloader unit tests (epic #815 slice 3).
//
// The reloader re-runs the startup config load sequence, diffs against the
// last-known-good config, reassembles the full RunnerConfig (hooks and
// conclusion watcher included), and applies it to the runner for subsequent
// runs. An invalid config is rejected and the last-known-good stays active.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
)

// reloadRecordingProvider records the model of every completion request.
type reloadRecordingProvider struct {
	mu     sync.Mutex
	models []string
}

func (p *reloadRecordingProvider) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = append(p.models, req.Model)
	return harness.CompletionResult{Content: "done"}, nil
}

func (p *reloadRecordingProvider) lastModel(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.models) == 0 {
		t.Fatal("provider saw no completion requests")
	}
	return p.models[len(p.models)-1]
}

// reloadRig builds a reloader against a temp config file with temp dirs for
// home/workspace/global state, plus a runner created from the initial config
// exactly the way run() does it (assembleRunnerConfig).
type reloadRig struct {
	provider *reloadRecordingProvider
	runner   *harness.Runner
	reloader *configReloader
	cfgPath  string
	home     string
}

func newReloadRig(t *testing.T, initialTOML string) *reloadRig {
	t.Helper()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(initialTOML), 0600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	workspace := t.TempDir()
	globalDir := t.TempDir()

	loadOpts := config.LoadOptions{
		UserConfigPath:    cfgPath,
		ProjectConfigPath: filepath.Join(workspace, ".harness", "config.toml"),
		ProfilesDir:       filepath.Join(home, ".harness", "profiles"),
	}
	getenv := func(string) string { return "" }

	initial, err := loadHarnessConfig(loadOpts, nil, getenv)
	if err != nil {
		t.Fatalf("initial loadHarnessConfig: %v", err)
	}

	deps := runnerConfigAssemblyDeps{
		opts:      runnerConfigOptions{},
		getenv:    getenv,
		workspace: workspace,
		home:      home,
		globalDir: globalDir,
	}

	provider := &reloadRecordingProvider{}
	startCfg, _ := assembleRunnerConfig(initial, deps)
	runner := harness.NewRunner(provider, harness.NewRegistry(), startCfg)

	reloader := newConfigReloader(func() (config.Config, error) {
		return loadHarnessConfig(loadOpts, nil, getenv)
	}, initial, deps)
	reloader.bindRunner(runner)

	return &reloadRig{provider: provider, runner: runner, reloader: reloader, cfgPath: cfgPath, home: home}
}

func (r *reloadRig) writeConfig(t *testing.T, contents string) {
	t.Helper()
	if err := os.WriteFile(r.cfgPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func (r *reloadRig) runOnce(t *testing.T) {
	t.Helper()
	run, err := r.runner.StartRun(harness.RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := r.runner.GetRun(run.ID)
		if ok && (st.Status == harness.RunStatusCompleted || st.Status == harness.RunStatusFailed) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not terminate", run.ID)
}

// TestReloader_AppliesNewConfig verifies a model edit is reported as applied
// and subsequent runs observe it.
func TestReloader_AppliesNewConfig(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\n")
	rig.runOnce(t)
	if got := rig.provider.lastModel(t); got != "model-a" {
		t.Fatalf("pre-reload model: got %q, want model-a", got)
	}

	rig.writeConfig(t, "model = \"model-b\"\n")
	report, err := rig.reloader.reload(context.Background())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "model" {
		t.Errorf("applied: got %v, want [model]", report.Applied)
	}
	if report.NeedsRestart() {
		t.Errorf("restart_required: got %v, want empty", report.RestartRequired)
	}

	rig.runOnce(t)
	if got := rig.provider.lastModel(t); got != "model-b" {
		t.Errorf("post-reload model: got %q, want model-b", got)
	}
}

// TestReloader_InvalidConfigKeepsLastKnownGood verifies a broken config file
// fails the reload with the parse error and leaves the old config active.
func TestReloader_InvalidConfigKeepsLastKnownGood(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\n")
	rig.writeConfig(t, "model = \n not toml = [\n")

	if _, err := rig.reloader.reload(context.Background()); err == nil {
		t.Fatal("reload with invalid TOML: got nil error, want parse error")
	}

	rig.runOnce(t)
	if got := rig.provider.lastModel(t); got != "model-a" {
		t.Errorf("after rejected reload, model: got %q, want model-a", got)
	}

	// The reloader recovers once the file is fixed.
	rig.writeConfig(t, "model = \"model-c\"\n")
	report, err := rig.reloader.reload(context.Background())
	if err != nil {
		t.Fatalf("reload after fix: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "model" {
		t.Errorf("applied after fix: got %v, want [model]", report.Applied)
	}
	rig.runOnce(t)
	if got := rig.provider.lastModel(t); got != "model-c" {
		t.Errorf("post-fix model: got %q, want model-c", got)
	}
}

// TestReloader_RestartOnlyReportedNotApplied verifies an addr change shows up
// as restart-only, and that the diff base advances so a second identical
// reload reports nothing.
func TestReloader_RestartOnlyReportedNotApplied(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\naddr = \":8080\"\n")
	rig.writeConfig(t, "model = \"model-a\"\naddr = \":9999\"\n")

	report, err := rig.reloader.reload(context.Background())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(report.RestartRequired) != 1 || report.RestartRequired[0] != "addr" {
		t.Errorf("restart_required: got %v, want [addr]", report.RestartRequired)
	}
	if len(report.Applied) != 0 {
		t.Errorf("applied: got %v, want empty", report.Applied)
	}

	// Diff base advanced: reloading the same file again reports no changes.
	report, err = rig.reloader.reload(context.Background())
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if report.Changed() {
		t.Errorf("second identical reload: got applied=%v restart=%v, want no changes",
			report.Applied, report.RestartRequired)
	}
}

// TestReloader_PreservesHooksAcrossReload is the assembly-fidelity contract:
// config-driven hooks registered at startup (from ~/.harness/hooks) must
// still be present on the runner config after a reload — a reload that
// rebuilt the runner config with bare buildRunnerConfig would wipe them.
func TestReloader_PreservesHooksAcrossReload(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\n[hooks]\nenabled = true\n")

	hooksDir := filepath.Join(rig.home, ".harness", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	hookJSON := `{
		"name": "deny-rm",
		"event": "pre_tool_use",
		"kind": "command",
		"command": ["/bin/sh", "-c", "echo allow"],
		"matcher": "bash",
		"timeout_seconds": 5
	}`
	if err := os.WriteFile(filepath.Join(hooksDir, "deny-rm.json"), []byte(hookJSON), 0600); err != nil {
		t.Fatal(err)
	}

	// Reload picks up the hook file and registers its adapter.
	rig.writeConfig(t, "model = \"model-b\"\n[hooks]\nenabled = true\n")
	if _, err := rig.reloader.reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	cfg := rig.runner.Config()
	if len(cfg.PreToolUseHooks) == 0 {
		t.Fatal("after reload: PreToolUseHooks is empty; config-driven hook registration was lost")
	}

	// A second reload (hook file unchanged) must keep the hook registered
	// rather than wiping or duplicating it.
	if _, err := rig.reloader.reload(context.Background()); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	cfg = rig.runner.Config()
	if len(cfg.PreToolUseHooks) != 1 {
		t.Errorf("after second reload: PreToolUseHooks len = %d, want exactly 1 (no wipe, no duplicate)", len(cfg.PreToolUseHooks))
	}

	// Disabling hooks in the file removes them on the next reload — proof
	// the reload reflects the new config rather than freezing the old one.
	rig.writeConfig(t, "model = \"model-b\"\n[hooks]\nenabled = false\n")
	if _, err := rig.reloader.reload(context.Background()); err != nil {
		t.Fatalf("third reload: %v", err)
	}
	cfg = rig.runner.Config()
	if len(cfg.PreToolUseHooks) != 0 {
		t.Errorf("after disabling hooks: PreToolUseHooks len = %d, want 0", len(cfg.PreToolUseHooks))
	}
	if !strings.Contains("model-b", rig.runner.Config().DefaultModel) {
		t.Errorf("model drifted across hook reloads: got %q", rig.runner.Config().DefaultModel)
	}
}

// TestReloader_ConcurrentReloadsSerialize verifies concurrent reload calls
// are serialized and leave the reloader on the final file contents.
func TestReloader_ConcurrentReloadsSerialize(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\n")
	rig.writeConfig(t, "model = \"model-final\"\n")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = rig.reloader.reload(context.Background())
		}()
	}
	wg.Wait()

	if got := rig.runner.Config().DefaultModel; got != "model-final" {
		t.Errorf("after concurrent reloads, model: got %q, want model-final", got)
	}
	rig.runOnce(t)
	if got := rig.provider.lastModel(t); got != "model-final" {
		t.Errorf("run after concurrent reloads, model: got %q, want model-final", got)
	}
}

// TestConfigReloadFunc_Wiring verifies the bootstrap plumbing: a wired
// reloader produces a non-nil server callback (endpoint enabled), and no
// reloader produces nil (endpoint returns 501).
func TestConfigReloadFunc_Wiring(t *testing.T) {
	rig := newReloadRig(t, "model = \"model-a\"\n")

	opts := buildServerOptions(serverBootstrapOptions{configReloader: rig.reloader})
	if opts.ConfigReload == nil {
		t.Fatal("buildServerOptions with reloader: ConfigReload is nil, want wired callback")
	}
	if _, err := opts.ConfigReload(context.Background()); err != nil {
		t.Errorf("wired callback invocation: %v", err)
	}

	opts = buildServerOptions(serverBootstrapOptions{})
	if opts.ConfigReload != nil {
		t.Error("buildServerOptions without reloader: ConfigReload is non-nil, want nil (501 path)")
	}
}
