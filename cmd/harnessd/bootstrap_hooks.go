package main

import (
	"log"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/hooks"
)

// registerConfigDrivenHooks loads config-driven lifecycle hooks (epic #737)
// and appends their adapters to runnerCfg's existing hook slices — the same
// registration point compiled-in plugins use (see conclusion-watcher), so
// compiled-in hooks always run first.
//
// Discovery dirs: user-global ~/.harness/hooks/, project
// <workspace>/.harness/hooks/, plus any extra [hooks] dirs. Project-level
// files load only when trusted (harnesscli hooks trust); the trust store
// lives under the user's home, never the project tree.
//
// The returned Summary is computed once here — the /v1/hooks surface serves
// it verbatim so the listing always matches what the runner registered.
// When hooks are disabled the function is a no-op returning an empty summary.
func registerConfigDrivenHooks(harnessCfg config.Config, workspace, home string, runnerCfg *harness.RunnerConfig) hooks.Summary {
	if !harnessCfg.Hooks.Enabled {
		return hooks.NewSummary(nil, nil)
	}

	userDir := hooks.UserHooksDir(home)
	dirs := append([]string{userDir, hooks.ProjectHooksDir(workspace)}, harnessCfg.Hooks.Dirs...)

	store, err := hooks.LoadTrustStore(hooks.TrustStorePath(home))
	if err != nil {
		// A store that exists but cannot be READ (permissions) fails closed:
		// trust cannot be verified, so nothing project-level may load.
		log.Printf("config-driven hooks: trust store unreadable, project hooks disabled: %v", err)
		store = hooks.EmptyTrustStore()
	}

	defs, skips := hooks.LoadWithOptions(hooks.LoadOptions{
		UserDir:    userDir,
		TrustStore: store,
	}, dirs...)

	for _, skip := range skips {
		log.Printf("config-driven hooks: skipped hook_file=%s skip_reason=%s", skip.File, skip.Reason)
	}

	adapters := hooks.Build(defs, &stdLogger{})
	runnerCfg.PreMessageHooks = append(runnerCfg.PreMessageHooks, adapters.PreMessage...)
	runnerCfg.PostMessageHooks = append(runnerCfg.PostMessageHooks, adapters.PostMessage...)
	runnerCfg.PreToolUseHooks = append(runnerCfg.PreToolUseHooks, adapters.PreToolUse...)
	runnerCfg.PostToolUseHooks = append(runnerCfg.PostToolUseHooks, adapters.PostToolUse...)

	summary := hooks.NewSummary(defs, skips)
	for _, h := range summary.Hooks {
		log.Printf("config-driven hooks: loaded hook_name=%s event=%s kind=%s source=%s file=%s",
			h.Name, h.Event, h.Kind, h.Source, h.File)
	}
	return summary
}
