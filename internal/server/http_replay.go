package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-agent-harness/internal/forensics/replay"
	"go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
)

// replayRequest is the JSON body for POST /v1/runs/replay.
type replayRequest struct {
	RolloutPath string `json:"rollout_path"`
	Mode        string `json:"mode"`      // "simulate" | "fork"
	ForkStep    int    `json:"fork_step"` // required when mode=fork
	// DetectDrift opts the simulate path into the second (drift) layer of replay.
	// When false or absent, simulate behaves exactly as before (integrity-only,
	// offline). When true, simulate additionally re-runs the harness against the
	// recorded-response provider and diffs the result. See handleReplaySimulate.
	DetectDrift bool `json:"detect_drift"`
}

// handleRunReplay handles POST /v1/runs/replay.
// mode=simulate: replays the rollout offline, returns a JSON summary.
// mode=fork: reconstructs conversation history up to fork_step, starts a new
// live run with that history, and returns the new run ID.
func (s *Server) handleRunReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req replayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if strings.TrimSpace(req.RolloutPath) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "rollout_path is required")
		return
	}
	if resolved, ok := s.resolveReplaySpecifier(req.RolloutPath); ok {
		req.RolloutPath = resolved
	}

	// SECURITY (PFIX-4): two-part gate, active only when auth is enabled AND a
	// rollout dir is configured (auth-disabled / no-dir deployments are unchanged).
	//
	//  1. Path containment (read-safety): the caller-supplied rollout_path must
	//     resolve — after symlink evaluation — to a location UNDER the configured
	//     rollout dir. This rejects path traversal (../), absolute-path escapes,
	//     and in-bounds symlinks whose target escapes the root, so the endpoint
	//     can never read arbitrary server-side files.
	//  2. Tenant ownership (enforced below, after the rollout is loaded): the
	//     recorded run.started event carries the owning tenant_id, which must
	//     match the caller's tenant. This is verified from rollout CONTENT rather
	//     than by confining to a per-tenant directory the recorder never writes.
	resolved, ok := s.resolveTenantRolloutPath(r, req.RolloutPath)
	if !ok {
		// 404 not_found: do not reveal whether the file exists or the on-disk
		// directory structure (matches the by-ID unknown-resource contract).
		writeError(w, http.StatusNotFound, "rollout_not_found", "rollout not found")
		return
	}
	req.RolloutPath = resolved

	switch req.Mode {
	case "simulate":
		s.handleReplaySimulate(w, r, req)
	case "fork":
		s.handleReplayFork(w, r, req)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("mode must be \"simulate\" or \"fork\", got %q", req.Mode))
	}
}

// resolveReplaySpecifier maps the personal CLI's replay shorthand
// ("run_abc123") to the dated rollout file that RunnerConfig.RolloutDir writes
// as <RolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl. Explicit file paths keep their
// existing behavior.
func (s *Server) resolveReplaySpecifier(spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if !isBareRunIDSpecifier(spec) || strings.TrimSpace(s.rolloutDir) == "" {
		return spec, false
	}
	root := strings.TrimSpace(s.rolloutDir)
	matches := make([]string, 0, 1)
	targetName := spec + ".jsonl"
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == targetName {
			matches = append(matches, path)
		}
		return nil
	}); err != nil {
		return spec, false
	}
	if len(matches) == 0 {
		return spec, false
	}
	sort.Strings(matches)
	return matches[len(matches)-1], true
}

func isBareRunIDSpecifier(spec string) bool {
	if spec == "" || filepath.IsAbs(spec) || strings.ContainsAny(spec, `/\`) {
		return false
	}
	if strings.Contains(spec, ".") {
		return false
	}
	return strings.HasPrefix(spec, "run_")
}

// replayTenantGateActive reports whether the per-tenant replay gate is active:
// auth enabled AND a rollout dir configured. When inactive the legacy
// (single-tenant / offline) behavior is preserved with no path or tenant checks.
func (s *Server) replayTenantGateActive() bool {
	return !s.authDisabled && strings.TrimSpace(s.rolloutDir) != ""
}

// verifyRolloutTenantOwnership enforces, when the gate is active, that the
// loaded rollout's owning tenant (recorded in the run.started event's tenant_id)
// matches the caller's authenticated tenant. Returns false (caller should emit
// 404) on any mismatch. When the gate is inactive it always returns true.
//
// A rollout that predates the tenant_id field (no tenant_id in run.started) is
// treated as un-ownable and therefore NOT replayable by any tenant while the
// gate is active — fail closed rather than leak a possibly cross-tenant rollout.
func (s *Server) verifyRolloutTenantOwnership(r *http.Request, events []rollout.RolloutEvent) bool {
	if !s.replayTenantGateActive() {
		return true
	}
	caller := strings.TrimSpace(TenantIDFromContext(r.Context()))
	if caller == "" {
		return false
	}
	owner := rolloutOwningTenant(events)
	return owner != "" && owner == caller
}

// rolloutOwningTenant returns the tenant_id recorded on the rollout's
// run.started event, or "" if absent/malformed. The loader guarantees
// run.started is the first event when present, but we scan defensively.
func rolloutOwningTenant(events []rollout.RolloutEvent) string {
	for _, ev := range events {
		if ev.Type != string(harness.EventRunStarted) {
			continue
		}
		if ev.Payload == nil {
			return ""
		}
		tid, _ := ev.Payload["tenant_id"].(string)
		return strings.TrimSpace(tid)
	}
	return ""
}

// resolveTenantRolloutPath validates a caller-supplied rollout_path and returns
// the cleaned, symlink-evaluated absolute path to load, plus true when it is
// permitted.
//
// This is purely a READ-SAFETY containment check: the resolved path must stay
// UNDER the configured rollout dir (the same directory the recorder writes to,
// <RolloutDir>/<date>/<run_id>.jsonl) so that the endpoint can never read
// arbitrary server-side files. It rejects path traversal (../), absolute-path
// escapes, and in-bounds symlinks whose target escapes the root (via
// filepath.EvalSymlinks followed by a re-check of containment).
//
// Tenant ownership is NOT enforced here — it is verified separately from the
// loaded rollout's recorded tenant_id (see verifyRolloutTenantOwnership), which
// lets legitimate same-tenant replays of real recorded rollouts succeed.
//
// The gate is inactive — and the path returned unchanged — when auth is
// disabled or no rollout dir is configured, preserving legacy behavior for
// single-tenant/offline deployments.
func (s *Server) resolveTenantRolloutPath(r *http.Request, rolloutPath string) (string, bool) {
	if !s.replayTenantGateActive() {
		return rolloutPath, true
	}

	tenant := strings.TrimSpace(TenantIDFromContext(r.Context()))
	if tenant == "" {
		// Auth enabled with a rollout dir but no resolvable tenant: deny rather
		// than fall through to an unscoped read.
		return "", false
	}

	// textualRoot is the cleaned absolute rollout dir as written in config (no
	// symlink evaluation). It anchors the pre-filesystem containment check that
	// rejects ../ and absolute escapes.
	textualRoot, err := filepath.Abs(s.rolloutDir)
	if err != nil {
		return "", false
	}

	// Resolve the requested path. Relative paths are interpreted against the
	// rollout root so that callers may pass either a bare filename or a full path.
	candidate := rolloutPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(textualRoot, candidate)
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", false
	}

	// First containment check on the textual (cleaned) path against the textual
	// root: rejects ../ and absolute escapes before touching the filesystem.
	if !pathWithin(textualRoot, abs) {
		return "", false
	}

	// Second containment check after evaluating symlinks: rejects an in-bounds
	// symlink whose target escapes the root. Both sides are symlink-evaluated so
	// the comparison is between fully-resolved paths (e.g. on macOS t.TempDir
	// returns /var/... which is a symlink to /private/var/...). EvalSymlinks
	// requires the path to exist; a non-existent path is a normal "not found" —
	// return the in-bounds textual path so the loader produces a consistent
	// not-found error rather than leaking the distinction.
	evalRoot, err := filepath.EvalSymlinks(textualRoot)
	if err != nil {
		// A configured rollout dir that does not resolve is a server-side
		// problem; deny rather than fall through to an unbounded read.
		return "", false
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, true
		}
		return "", false
	}
	if !pathWithin(evalRoot, evaluated) {
		return "", false
	}
	return evaluated, true
}

// pathWithin reports whether target is the directory root itself or a path
// nested inside it. It uses filepath.Rel and rejects any result that escapes
// (starts with "..") or is absolute, which closes prefix-matching pitfalls such
// as "/a/b" being treated as inside "/a/bc".
func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// handleReplaySimulate runs an offline replay simulation.
//
// # Two-layer replay contract (deliverable E)
//
// Simulate has two layers, selected by the request's detect_drift flag:
//
//   - INTEGRITY (default; detect_drift false or absent): offline, no
//     re-execution. replay.Replay verifies the internal causal consistency of
//     the single recorded stream (matched=false means the stream is malformed or
//     tampered, NOT that the harness drifted). The response shape is UNCHANGED
//     from before drift detection existed: top-level mode/events_replayed/
//     step_count/matched/mismatches, with NO drift or integrity sub-blocks.
//     This default is regression-guarded by TestReplaySimulate_DefaultUnchanged.
//
//   - DRIFT (opt-in; detect_drift true): the integrity result is preserved under
//     an "integrity" sub-block AND the harness is re-run against the recorded
//     provider (replay.RecordedProvider) with tool execution short-circuited to
//     the recorded outputs (replay.NewReplayToolHandler). Because every provider
//     output and tool result is fixed to the original recording, the harness's
//     own step/decision logic is the only live variable, so any divergence the
//     diff finds is attributable to the harness. The response is
//     {mode, matched, integrity:{...}, drift:{added_steps, removed_steps,
//     changed_steps, divergent_tool_calls, cost_delta_usd, outcome_diff, score}}.
//     Top-level matched = integrity.matched AND drift.matched. Cost is reported
//     as a delta, never a hard mismatch (see replay.DetectDrift).
//
// The Issue-2 tenant/path gate stays in force for both layers: the path was
// already resolved+verified by handleRunReplay before this is reached, and the
// loaded rollout's tenant ownership is verified below before any replay work.
func (s *Server) handleReplaySimulate(w http.ResponseWriter, r *http.Request, req replayRequest) {
	events, err := loadRolloutFile(req.RolloutPath)
	if err != nil {
		writeRolloutError(w, err)
		return
	}

	// SECURITY (PFIX-4): verify the loaded rollout is owned by the caller's
	// tenant. 404 (not 403) to avoid revealing that the rollout exists.
	if !s.verifyRolloutTenantOwnership(r, events) {
		writeError(w, http.StatusNotFound, "rollout_not_found", "rollout not found")
		return
	}

	result := replay.Replay(events)

	// DEFAULT (integrity-only): response shape unchanged from before drift
	// detection. Do not add any sub-block.
	if !req.DetectDrift {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":            "simulate",
			"events_replayed": len(result.Events),
			"step_count":      result.StepCount,
			"matched":         result.Matched,
			"mismatches":      result.Mismatches,
		})
		return
	}

	// OPT-IN drift detection: keep the integrity result under "integrity" and
	// add the drift result from a recorded-provider re-run.
	release, ok := s.acquireReplayDriftSlot(r.Context())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "replay_busy", "drift detection is already at capacity")
		return
	}
	defer release()
	drift, err := s.runDriftDetection(r.Context(), events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "replay_error",
			fmt.Sprintf("drift detection failed: %s", err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":    "simulate",
		"matched": result.Matched && drift.Matched,
		"integrity": map[string]any{
			"events_replayed": len(result.Events),
			"step_count":      result.StepCount,
			"matched":         result.Matched,
			"mismatches":      result.Mismatches,
		},
		"drift": map[string]any{
			"added_steps":          drift.AddedSteps,
			"removed_steps":        drift.RemovedSteps,
			"changed_steps":        drift.ChangedSteps,
			"divergent_tool_calls": drift.DivergentToolCalls,
			"cost_delta_usd":       drift.CostDeltaUSD,
			"outcome_diff":         drift.OutcomeDiff,
			"score":                drift.Score,
			"matched":              drift.Matched,
		},
	})
}

func (s *Server) replayDriftSemaphore() chan struct{} {
	s.replayDriftOnce.Do(func() {
		if s.replayDriftSem != nil {
			return
		}
		slots := s.replayDriftSlots
		if slots <= 0 {
			slots = defaultReplayDriftSlots
		}
		s.replayDriftSem = make(chan struct{}, slots)
	})
	return s.replayDriftSem
}

func (s *Server) acquireReplayDriftSlot(ctx context.Context) (func(), bool) {
	sem := s.replayDriftSemaphore()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	case <-ctx.Done():
		return nil, false
	default:
		return nil, false
	}
}

// runDriftDetection executes the drift layer: it builds the recorded-response
// provider and replay tool dispatch from the loaded rollout, re-runs the harness
// against them (recording into a temp dir), loads the resulting "replayed"
// rollout, and diffs it against the original with replay.DetectDrift.
//
// The re-run uses a throwaway Runner — NOT s.runner — because s.runner is wired
// to the live provider; drift detection must isolate the harness's own logic by
// fixing every provider output and tool result to the original recording. The
// temp RolloutDir and Runner are torn down before returning.
func (s *Server) runDriftDetection(ctx context.Context, original []rollout.RolloutEvent) (replay.DriftResult, error) {
	provider, err := replay.NewRecordedProvider(original)
	if err != nil {
		return replay.DriftResult{}, err
	}

	toolDispatch, err := replay.NewReplayToolDispatch(original)
	if err != nil {
		return replay.DriftResult{}, err
	}

	// Register every distinct recorded tool name with the replay handler so the
	// re-run's tool calls resolve to the recorded outputs. Tools are marked
	// non-parallel-safe so they execute sequentially and deterministically; the
	// handler is keyed by call_id, so ordering does not affect the recorded result.
	registry := harness.NewRegistry()
	for _, name := range recordedToolNames(original) {
		def := harness.ToolDefinition{
			Name:         name,
			Description:  "replay (recorded output)",
			ParallelSafe: false,
		}
		if regErr := registry.Register(def, toolDispatch.Handler); regErr != nil {
			return replay.DriftResult{}, fmt.Errorf("register replay tool %q: %w", name, regErr)
		}
	}

	tempDir, err := os.MkdirTemp("", "replay-drift-*")
	if err != nil {
		return replay.DriftResult{}, fmt.Errorf("create temp rollout dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	factory := s.driftRunnerFactory
	if factory == nil {
		factory = harness.NewRunner
	}
	runner := factory(provider, registry, harness.RunnerConfig{
		DefaultModel:        recordedModel(original),
		DefaultSystemPrompt: recordedSystemPrompt(original),
		// Cap the re-run at one more step than the original took: a faithful
		// replay should reproduce the recorded step count, and the recorded
		// provider errors once its scripted turns are exhausted, so an
		// over-running harness fails fast rather than spinning.
		MaxSteps:   recordedStepCount(original) + 1,
		RolloutDir: tempDir,
	})
	defer runner.Shutdown(context.Background())

	run, err := runner.StartRun(harness.RunRequest{
		Prompt:       recordedPrompt(original),
		SystemPrompt: recordedSystemPrompt(original),
	})
	if err != nil {
		return replay.DriftResult{}, fmt.Errorf("start re-run: %w", err)
	}

	if err := waitForRunTerminal(ctx, runner, run.ID); err != nil {
		return replay.DriftResult{}, err
	}

	replayed, err := loadCompletedRollout(ctx, tempDir, run.ID)
	if err != nil {
		return replay.DriftResult{}, err
	}

	return replay.DetectDrift(original, replayed, rollout.DriftOptions), nil
}

// loadCompletedRollout locates and loads the recorder's output for runID, polling
// until the file exists, parses, and carries a terminal event. The rollout
// recorder flushes asynchronously: the terminal EVENT can be delivered to
// subscribers (so waitForRunTerminal returns) slightly before the recorder
// goroutine drains its channel and writes the terminal LINE to disk. Reading the
// file the instant the event fires therefore races and can observe a partial or
// empty rollout. Polling for a terminal-terminated, parseable file closes that
// race deterministically.
func loadCompletedRollout(ctx context.Context, dir, runID string) ([]rollout.RolloutEvent, error) {
	deadline := time.Now().Add(4 * time.Second)
	var lastErr error
	for {
		path, err := findRolloutFile(dir, runID)
		if err == nil {
			events, loadErr := loadRolloutFile(path)
			if loadErr == nil && rolloutHasTerminal(events) {
				return events, nil
			}
			if loadErr != nil {
				lastErr = loadErr
			} else {
				lastErr = fmt.Errorf("replayed rollout for run %s has no terminal event yet", runID)
			}
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("load replayed rollout: %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// rolloutHasTerminal reports whether the events contain a terminal run event,
// indicating the recorder has flushed the full run to disk.
func rolloutHasTerminal(events []rollout.RolloutEvent) bool {
	for _, ev := range events {
		if harness.IsTerminalEvent(harness.EventType(ev.Type)) {
			return true
		}
	}
	return false
}

// waitForRunTerminal subscribes to the run and blocks until it reaches a terminal
// event (completed/failed/cancelled) or ctx is done.
func waitForRunTerminal(ctx context.Context, runner *harness.Runner, runID string) error {
	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		return fmt.Errorf("subscribe to re-run: %w", err)
	}
	defer cancel()

	for _, ev := range history {
		if harness.IsTerminalEvent(ev.Type) {
			return nil
		}
	}
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return nil
			}
			if harness.IsTerminalEvent(ev.Type) {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// findRolloutFile locates the recorder's output for runID under dir
// (<dir>/<date>/<runID>.jsonl). The date partition is chosen at record time, so
// it is found by walking rather than assuming a date.
func findRolloutFile(dir, runID string) (string, error) {
	target := runID + ".jsonl"
	var found string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(p) == target {
			found = p
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("locate replayed rollout: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("replayed rollout file for run %s not found", runID)
	}
	return found, nil
}

// recordedToolNames returns the distinct tool names referenced by the rollout's
// tool.call.started events, in first-seen order.
func recordedToolNames(events []rollout.RolloutEvent) []string {
	seen := make(map[string]bool)
	var names []string
	for _, ev := range events {
		if ev.Type != string(harness.EventToolCallStarted) || ev.Payload == nil {
			continue
		}
		name, _ := ev.Payload["tool"].(string)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// recordedPrompt returns the prompt recorded on the rollout's run.started event,
// or a non-empty placeholder so StartRun (which requires a prompt) succeeds.
func recordedPrompt(events []rollout.RolloutEvent) string {
	for _, ev := range events {
		if ev.Type == string(harness.EventRunStarted) && ev.Payload != nil {
			if p, ok := ev.Payload["prompt"].(string); ok && p != "" {
				return p
			}
		}
	}
	return "replay"
}

// recordedSystemPrompt returns the system prompt recorded on run.started, if the
// recorder captured one (real recorders typically do not; this is best-effort).
func recordedSystemPrompt(events []rollout.RolloutEvent) string {
	for _, ev := range events {
		if ev.Type == string(harness.EventRunStarted) && ev.Payload != nil {
			if p, ok := ev.Payload["system_prompt"].(string); ok {
				return p
			}
		}
	}
	return ""
}

// recordedModel returns the model the original run used, so the re-run resolves
// the same model in its provider.resolved event. The real recorder does not put
// the model on run.started, but it does emit a provider.resolved event carrying
// "model"; prefer that, falling back to run.started for hand-authored rollouts.
func recordedModel(events []rollout.RolloutEvent) string {
	for _, ev := range events {
		if ev.Type == "provider.resolved" && ev.Payload != nil {
			if m, ok := ev.Payload["model"].(string); ok && m != "" {
				return m
			}
		}
	}
	for _, ev := range events {
		if ev.Type == string(harness.EventRunStarted) && ev.Payload != nil {
			if m, ok := ev.Payload["model"].(string); ok && m != "" {
				return m
			}
		}
	}
	return ""
}

// recordedStepCount returns the highest step number in the rollout.
func recordedStepCount(events []rollout.RolloutEvent) int {
	maxStep := 0
	for _, ev := range events {
		if ev.Step > maxStep {
			maxStep = ev.Step
		}
	}
	return maxStep
}

// handleReplayFork reconstructs conversation history and starts a new run.
func (s *Server) handleReplayFork(w http.ResponseWriter, r *http.Request, req replayRequest) {
	events, err := loadRolloutFile(req.RolloutPath)
	if err != nil {
		writeRolloutError(w, err)
		return
	}

	// SECURITY (PFIX-4): verify the loaded rollout is owned by the caller's
	// tenant before reconstructing/forking it. 404 to avoid revealing existence.
	if !s.verifyRolloutTenantOwnership(r, events) {
		writeError(w, http.StatusNotFound, "rollout_not_found", "rollout not found")
		return
	}

	forkResult, err := replay.Fork(events, req.ForkStep, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	if len(forkResult.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "no messages reconstructed from rollout")
		return
	}

	// Extract a prompt from the last user message for StartRun.
	prompt := extractLastUserPrompt(forkResult.Messages)

	// Populate TenantID and InitiatorAPIKeyPrefix from auth context so that
	// forked runs are always created under the authenticated tenant.
	run, err := s.runner.StartRun(harness.RunRequest{
		Prompt:                prompt,
		TenantID:              TenantIDFromContext(r.Context()),
		InitiatorAPIKeyPrefix: APIKeyPrefixFromContext(r.Context()),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "replay_error",
			fmt.Sprintf("failed to start forked run: %s", err.Error()))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"mode":                "fork",
		"run_id":              run.ID,
		"from_step":           forkResult.FromStep,
		"original_step_count": forkResult.OriginalStepCount,
		"original_outcome":    forkResult.OriginalOutcome,
		"messages_restored":   len(forkResult.Messages),
	})
}

// loadRolloutFile loads and returns rollout events, returning a descriptive
// error if the file cannot be found or parsed.
func loadRolloutFile(path string) ([]rollout.RolloutEvent, error) {
	events, err := rollout.LoadFile(path)
	if err != nil {
		return nil, err
	}
	return events, nil
}

// writeRolloutError writes an appropriate HTTP error based on the rollout
// loading error type.
func writeRolloutError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "rollout_not_found", err.Error())
		return
	}
	// Check for wrapped os.ErrNotExist from rollout.LoadFile.
	if pathErr := (*os.PathError)(nil); errors.As(err, &pathErr) {
		writeError(w, http.StatusNotFound, "rollout_not_found", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "replay_error", err.Error())
}

// extractLastUserPrompt finds the last user message content to use as the
// StartRun prompt.
func extractLastUserPrompt(msgs []harness.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return "forked run"
}
