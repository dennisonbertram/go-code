package main

// config_reload.go — daemon config reload path (epic #815 slice 3).
//
// POST /v1/config/reload (and SIGHUP in slice 4) funnel into configReloader,
// which re-runs the exact startup config resolution, diffs the result against
// the last-known-good config via config.ReloadDiff, reassembles the complete
// RunnerConfig — hooks, conclusion watcher, stores included — and applies it
// with Runner.ApplyConfig so only subsequently started runs observe it.
//
// loadHarnessConfig and assembleRunnerConfig are shared with startup: using
// one code path for both is what guarantees reload semantics cannot drift
// from startup semantics.

import (
	"context"
	"log"
	"path/filepath"
	"sync"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/plugins"
	"go-agent-harness/internal/profiles"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/store/s3backup"
	conclusionwatcher "go-agent-harness/plugins/conclusion-watcher"
)

// loadHarnessConfig runs the full startup config resolution: layered Load,
// Resolve, profile defaults, and the daemon-level MaxSteps default (0 → 8
// unless HARNESS_MAX_STEPS is explicitly set). Startup and reload both use it
// so a reload observes exactly what a restart would.
func loadHarnessConfig(opts config.LoadOptions, startupProfile *profiles.Profile, getenv func(string) string) (config.Config, error) {
	harnessCfg, err := config.Load(opts)
	if err != nil {
		return config.Config{}, err
	}
	harnessCfg = harnessCfg.Resolve()
	harnessCfg = applyProfileDefaults(harnessCfg, startupProfile, getenv)
	// When max_steps is 0 from config (unlimited), preserve the daemon
	// default of 8 unless the user has explicitly set HARNESS_MAX_STEPS.
	if harnessCfg.MaxSteps == 0 && getenv("HARNESS_MAX_STEPS") == "" {
		harnessCfg.MaxSteps = 8
	}
	return harnessCfg, nil
}

// runnerConfigAssemblyDeps holds the long-lived wiring dependencies needed to
// assemble a complete RunnerConfig from a loaded config.Config. Values are
// captured once at startup and reused for every reload.
type runnerConfigAssemblyDeps struct {
	opts            runnerConfigOptions
	profileRunStore store.ProfileRunStoreIface
	getenv          func(string) string
	workspace       string
	home            string
	globalDir       string
}

// assembleRunnerConfig builds the full RunnerConfig for a loaded config:
// buildRunnerConfig plus everything startup appends to it — profile run
// store, S3 uploader, conclusion watcher, config-driven hooks, and trusted
// plugin hooks. Sharing this between startup and reload is what keeps a
// reload from silently wiping hook/plugin registrations (ApplyConfig
// replaces the hook slices wholesale).
func assembleRunnerConfig(harnessCfg config.Config, deps runnerConfigAssemblyDeps) (harness.RunnerConfig, hooks.Summary) {
	runnerCfg := buildRunnerConfig(harnessCfg, deps.opts)

	// Persist per-run profile history so get_efficiency_report has data to
	// aggregate. Nil when the store failed to open (feature degrades gracefully).
	runnerCfg.ProfileRunStore = deps.profileRunStore

	// S3 backup: wire uploader when all required env vars are present.
	if s3cfg, ok := s3backup.ConfigFromEnv(deps.getenv); ok {
		runnerCfg.S3Uploader = s3backup.NewUploader(s3cfg)
		log.Printf("s3 backup enabled: bucket=%s prefix=%s region=%s", s3cfg.Bucket, s3cfg.KeyPrefix, s3cfg.Region)
	} else {
		runnerCfg.S3Uploader = s3backup.NewNoOpUploader()
	}

	// Conclusion watcher plugin.
	if harnessCfg.ConclusionWatcher.Enabled {
		wcfg := conclusionwatcher.WatcherConfig{
			Mode: conclusionwatcher.InterventionMode(harnessCfg.ConclusionWatcher.InterventionMode),
		}
		if harnessCfg.ConclusionWatcher.EvaluatorEnabled {
			cwAPIKey := harnessCfg.ConclusionWatcher.EvaluatorAPIKey
			if cwAPIKey == "" {
				cwAPIKey = deps.getenv("OPENAI_API_KEY")
			}
			eval := conclusionwatcher.NewOpenAIEvaluator(cwAPIKey)
			if harnessCfg.ConclusionWatcher.EvaluatorModel != "" {
				eval.Model = harnessCfg.ConclusionWatcher.EvaluatorModel
			}
			wcfg.Evaluator = eval
		}
		cw := conclusionwatcher.New(wcfg)
		cw.Register(&runnerCfg)
	}

	// Config-driven lifecycle hooks (epic #737): load hook files trust-aware
	// and append adapters to the existing hook slices. Runs AFTER compiled-in
	// plugins so plugin hooks keep their leading slice position.
	hooksSummary := registerConfigDrivenHooks(harnessCfg, deps.workspace, deps.home, &runnerCfg)
	pluginRoot := filepath.Join(deps.globalDir, "plugins")
	registerTrustedPluginHooks(pluginRoot, plugins.NewStateStore(filepath.Join(pluginRoot, "state.json")), &runnerCfg)

	return runnerCfg, hooksSummary
}

// configReloader is the daemon's config reload engine. It serializes reloads
// so concurrent triggers (HTTP, SIGHUP) cannot interleave the
// load → diff → apply → commit sequence.
type configReloader struct {
	// load re-runs the startup config resolution (loadHarnessConfig with the
	// startup LoadOptions and profile). It returns an error for invalid
	// config, leaving the last-known-good config active.
	load func() (config.Config, error)
	deps runnerConfigAssemblyDeps

	mu      sync.Mutex
	current config.Config
	runner  *harness.Runner
}

// newConfigReloader creates a reloader seeded with the startup config as the
// diff base. bindRunner must be called before reload is served.
func newConfigReloader(load func() (config.Config, error), current config.Config, deps runnerConfigAssemblyDeps) *configReloader {
	return &configReloader{load: load, deps: deps, current: current}
}

// bindRunner records the runner created by buildHTTPRuntime. It is called
// exactly once, before the server starts accepting requests (the
// subagentRunnerHandoff precedent), but the mutex keeps it safe regardless.
func (r *configReloader) bindRunner(runner *harness.Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runner = runner
}

// reload re-loads the config, applies the hot-swappable subset to the runner
// for subsequent runs, and reports the diff. On a load error the previous
// configuration remains active and the error is returned to the caller.
func (r *configReloader) reload(_ context.Context) (config.ReloadReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	next, err := r.load()
	if err != nil {
		return config.ReloadReport{}, err
	}
	report := config.ReloadDiff(r.current, next)
	runnerCfg, _ := assembleRunnerConfig(next, r.deps)
	r.runner.ApplyConfig(runnerCfg)
	r.current = next
	return report, nil
}
