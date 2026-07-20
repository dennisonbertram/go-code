package symphd

import (
	"context"
	"os"
	"sync"
	"time"

	"go-agent-harness/internal/workspace"
)

// Orchestrator coordinates agent dispatch across workspaces.
type Orchestrator struct {
	config      *Config
	startedAt   time.Time
	mu          sync.RWMutex
	agents      int
	tracker     Tracker
	dispatcher  *Dispatcher
	retryPolicy RetryPolicy
	deadLetters *DeadLetterQueue
	pool        *workspace.Pool // non-nil only when WorkspaceType is "pool"
}

// NewOrchestrator creates a new Orchestrator with the given config.
// If the config has GitHubOwner and GitHubRepo set, a GitHubTracker is
// initialised automatically. When WorkspaceType is set, a WorkspaceFactory is
// built and, if a tracker is also configured, a Dispatcher is auto-wired.
func NewOrchestrator(cfg *Config) *Orchestrator {
	o := &Orchestrator{
		config:    cfg,
		startedAt: time.Now(),
		retryPolicy: RetryPolicy{
			MaxAttempts: cfg.RetryMaxAttempts,
			BaseDelayMs: cfg.RetryBaseDelayMs,
			MaxDelayMs:  cfg.RetryMaxDelayMs,
		},
		deadLetters: NewDeadLetterQueue(),
	}
	if cfg.GitHubOwner != "" && cfg.GitHubRepo != "" {
		o.tracker = NewGitHubTracker(cfg.GitHubOwner, cfg.GitHubRepo, cfg.TrackLabel, cfg.GitHubToken)
	}

	// Build workspace factory and optional pool.
	wsFactory, pool := buildWorkspaceFactory(cfg)
	o.pool = pool

	// Auto-wire dispatcher when both workspace type and tracker are configured.
	if wsFactory != nil && o.tracker != nil {
		// Build env map for subagent workspaces: inject known API keys from
		// the parent process environment. Keys are never written to the TOML
		// config file — they go to workspace.Options.Env (container env vars).
		subagentEnv := buildSubagentEnv()

		dispatchCfg := DispatchConfig{
			MaxConcurrent: cfg.MaxConcurrentAgents,
			StallTimeout:  5 * time.Minute,
			PollInterval:  5 * time.Second,
			HarnessURL:    cfg.HarnessURL,
			BaseDir:       cfg.BaseDir,
			SubagentEnv:   subagentEnv,
		}
		o.dispatcher = NewDispatcherSimple(dispatchCfg, wsFactory, o.tracker)
	}

	return o
}

// SetTracker replaces the tracker (useful for testing).
func (o *Orchestrator) SetTracker(t Tracker) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tracker = t
}

// SetDispatcher replaces the dispatcher (useful for testing).
func (o *Orchestrator) SetDispatcher(d *Dispatcher) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dispatcher = d
}

// State returns a snapshot of the orchestrator's current state.
func (o *Orchestrator) State() map[string]any {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return map[string]any{
		"version":       "0.1.0",
		"running_since": o.startedAt.UTC().Format(time.RFC3339),
		"agent_count":   o.agents,
		"config": map[string]any{
			"workspace_type":        o.config.WorkspaceType,
			"max_concurrent_agents": o.config.MaxConcurrentAgents,
		},
	}
}

// Issues returns all tracked issues, or an empty slice if no tracker is set.
func (o *Orchestrator) Issues() []*TrackedIssue {
	o.mu.RLock()
	tr := o.tracker
	o.mu.RUnlock()

	if tr == nil {
		return []*TrackedIssue{}
	}
	return tr.Issues()
}

// Refresh polls the tracker for new issues. It is a no-op when no tracker is
// configured.
func (o *Orchestrator) Refresh(ctx context.Context) error {
	o.mu.RLock()
	tr := o.tracker
	o.mu.RUnlock()

	if tr == nil {
		return nil
	}
	return tr.Poll(ctx)
}

// DeadLetters returns the current dead letter queue items.
func (o *Orchestrator) DeadLetters() []*DeadLetter {
	return o.deadLetters.Items()
}

// RetryFailed checks a failed issue and either resets it for another attempt
// or moves it to the dead letter queue. Returns true if the issue was retried,
// false if it was dead-lettered.
func (o *Orchestrator) RetryFailed(issue *TrackedIssue, lastErr string) bool {
	o.mu.RLock()
	tr := o.tracker
	policy := o.retryPolicy
	dlq := o.deadLetters
	o.mu.RUnlock()

	if policy.ShouldRetry(issue.Attempts) {
		if tr != nil {
			_ = tr.Reset(issue.Number)
		}
		return true
	}
	dlq.Add(issue, lastErr)
	return false
}

// Start begins orchestration. It runs a polling loop that claims unclaimed
// issues from the tracker and dispatches them via the Dispatcher. If no
// dispatcher is configured, Start returns immediately.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.RLock()
	d := o.dispatcher
	tr := o.tracker
	o.mu.RUnlock()

	if d == nil || tr == nil {
		// No dispatcher or tracker configured; nothing to do.
		return nil
	}

	pollInterval := time.Duration(o.config.PollIntervalMs) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Drain results in a background goroutine so the semaphore is never blocked
	// by an unread results channel. Failed results are forwarded to
	// handleFailedResult which retries or dead-letters the issue.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case result, ok := <-d.Results():
				if !ok {
					return
				}
				if result.Error != nil {
					o.handleFailedResult(result)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.C:
			// Claim all unclaimed issues, then dispatch claimed candidates.
			for _, issue := range tr.Issues() {
				if issue.ClaimState == ClaimStateUnclaimed {
					_ = tr.Claim(issue.Number)
				}
			}
			for _, candidate := range tr.Candidates() {
				if err := d.Dispatch(ctx, candidate); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					// Log dispatch errors but keep looping.
					continue
				}
			}
		}
	}
}

// handleFailedResult processes a failed RunResult, either retrying the issue
// or moving it to the dead-letter queue.
func (o *Orchestrator) handleFailedResult(result RunResult) {
	o.mu.RLock()
	tr := o.tracker
	o.mu.RUnlock()

	if tr == nil {
		return
	}

	// Find the issue to check its attempt count.
	var failedIssue *TrackedIssue
	for _, issue := range tr.Issues() {
		if issue.Number == result.IssueNumber {
			failedIssue = issue
			break
		}
	}
	if failedIssue == nil {
		return
	}

	errMsg := result.Error.Error()
	if !o.RetryFailed(failedIssue, errMsg) {
		// Issue was dead-lettered (max attempts exceeded).
		// RetryFailed already called deadLetters.Add() for us.
		_ = errMsg // acknowledged
	}
	// If retried, RetryFailed called tracker.Reset() which sets state back to Unclaimed.
	// The next poll tick will re-claim and re-dispatch it.
}

// Shutdown gracefully stops the orchestrator and any in-flight dispatches.
// If a workspace pool is configured, it is also closed after dispatcher shutdown.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.RLock()
	d := o.dispatcher
	pool := o.pool
	o.mu.RUnlock()

	if d != nil {
		d.Shutdown(ctx)
	}
	if pool != nil {
		pool.Close()
	}
	return nil
}

// buildSubagentEnv collects API keys and other secrets from the parent process
// environment and returns them as a map suitable for workspace.Options.Env.
// These values are passed to container environments rather than written to disk.
// Only well-known API key variables are captured.
func buildSubagentEnv() map[string]string {
	knownKeys := []string{
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"HARNESS_MODEL",
	}
	env := make(map[string]string)
	for _, key := range knownKeys {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// buildWorkspaceFactory returns a WorkspaceFactory and optional Pool based on cfg.
// The Pool (if non-nil) must be closed by the caller on shutdown.
func buildWorkspaceFactory(cfg *Config) (WorkspaceFactory, *workspace.Pool) {
	switch cfg.WorkspaceType {
	case "local":
		return func() workspace.Workspace {
			return workspace.NewLocal(cfg.HarnessURL, cfg.BaseDir)
		}, nil

	case "worktree":
		return func() workspace.Workspace {
			return workspace.NewWorktree(cfg.HarnessURL, cfg.BaseDir)
		}, nil

	case "container":
		return func() workspace.Workspace {
			return workspace.NewContainer("")
		}, nil

	case "vm":
		apiKey := os.Getenv("HETZNER_API_KEY")
		return func() workspace.Workspace {
			return workspace.NewVM(workspace.NewHetznerProvider(apiKey))
		}, nil

	case "pool":
		inner := buildRawFactory(cfg.PoolWorkspaceType, cfg)
		if inner == nil {
			return nil, nil
		}
		pool := workspace.NewPool(inner, workspace.Options{BaseDir: cfg.BaseDir}, cfg.PoolSize)
		rawFactory := pool.Factory()
		return WorkspaceFactory(rawFactory), pool

	default:
		return nil, nil
	}
}

// buildRawFactory returns a raw workspace.Factory for a given workspace type name.
// Used as the inner factory for pool mode.
func buildRawFactory(wsType string, cfg *Config) workspace.Factory {
	switch wsType {
	case "local":
		return func() workspace.Workspace {
			return workspace.NewLocal(cfg.HarnessURL, cfg.BaseDir)
		}
	case "container":
		return func() workspace.Workspace {
			return workspace.NewContainer("")
		}
	case "vm":
		apiKey := os.Getenv("HETZNER_API_KEY")
		return func() workspace.Workspace {
			return workspace.NewVM(workspace.NewHetznerProvider(apiKey))
		}
	default:
		return nil
	}
}
