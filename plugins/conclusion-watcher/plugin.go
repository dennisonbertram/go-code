package conclusionwatcher

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"go-agent-harness/internal/harness"
)

// ConclusionWatcher is the root plugin object. Create one per run (or share
// across runs by calling ledger.Reset() between them — but per-run is safer).
type ConclusionWatcher struct {
	cfg               WatcherConfig
	ledger            *ObservationLedger
	interventionCount int64      // atomic counter
	currentStep       int64      // atomic; incremented at start of each AfterMessage
	mu                sync.Mutex // guards detections slice
	detections        []DetectionResult
}

// New creates a ConclusionWatcher with the given config.
// WatcherConfig zero values are safe: all patterns armed, inject mode, no emitter.
func New(cfg WatcherConfig) *ConclusionWatcher {
	if cfg.Mode == "" {
		cfg.Mode = InterventionInjectPrompt
	}
	return &ConclusionWatcher{
		cfg:    cfg,
		ledger: NewObservationLedger(),
	}
}

// Ledger returns the watcher's ObservationLedger. Exposed for testing.
func (w *ConclusionWatcher) Ledger() *ObservationLedger {
	return w.ledger
}

// Register wires the watcher into a RunnerConfig's hook slices.
// Call before creating the runner. Register appends (never replaces) hooks,
// so it is safe to call alongside other plugins.
//
// Hooks appended:
//   - cfg.PostMessageHooks  <- postMessageHook (runs HedgeAssertion, UnverifiedFileClaim,
//     PrematureCompletion, ArchitectureAssumption)
//   - cfg.PreToolUseHooks   <- preToolUseHook (runs SkippedDiagnostic; also updates
//     the ledger's tool history and file observations)
//   - cfg.PostToolUseHooks  <- postToolUseHook (updates ledger file observations
//     from tool output)
//
// Warning: calling Register twice on the same cfg will append hooks twice.
func (w *ConclusionWatcher) Register(cfg *harness.RunnerConfig) {
	cfg.PostMessageHooks = append(cfg.PostMessageHooks, &postMessageHook{w: w})
	cfg.PreToolUseHooks = append(cfg.PreToolUseHooks, &preToolUseHook{w: w})
	cfg.PostToolUseHooks = append(cfg.PostToolUseHooks, &postToolUseHook{w: w})
}

// Detections returns a copy of all DetectionResults recorded so far.
// Safe for concurrent use.
func (w *ConclusionWatcher) Detections() []DetectionResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]DetectionResult, len(w.detections))
	copy(out, w.detections)
	return out
}

// InterventionCount returns the number of interventions executed.
func (w *ConclusionWatcher) InterventionCount() int64 {
	return atomic.LoadInt64(&w.interventionCount)
}

// isPatternArmed reports whether a given PatternType should run.
// If cfg.Patterns is empty, all patterns are armed.
func (w *ConclusionWatcher) isPatternArmed(p PatternType) bool {
	if len(w.cfg.Patterns) == 0 {
		return true
	}
	for _, ap := range w.cfg.Patterns {
		if ap == p {
			return true
		}
	}
	return false
}

// recordDetection appends a detection to the internal slice.
func (w *ConclusionWatcher) recordDetection(d DetectionResult) {
	w.mu.Lock()
	w.detections = append(w.detections, d)
	w.mu.Unlock()
}

// emitDetected calls the EventEmitter with EventConclusionDetected payload.
func (w *ConclusionWatcher) emitDetected(runID string, d DetectionResult) {
	if w.cfg.EventEmitter == nil {
		return
	}
	w.cfg.EventEmitter(EventConclusionDetected, runID, map[string]any{
		"pattern":    string(d.Pattern),
		"confidence": d.Confidence,
		"evidence":   d.Evidence,
		"step":       d.Step,
	})
}

// emitIntervened calls the EventEmitter with EventConclusionIntervened payload.
func (w *ConclusionWatcher) emitIntervened(runID string, d DetectionResult, mode InterventionMode) {
	if w.cfg.EventEmitter == nil {
		return
	}
	w.cfg.EventEmitter(EventConclusionIntervened, runID, map[string]any{
		"pattern": string(d.Pattern),
		"mode":    string(mode),
		"step":    d.Step,
	})
}

// checkMaxInterventions returns true if the intervention cap has been reached.
func (w *ConclusionWatcher) checkMaxInterventions() bool {
	if w.cfg.MaxInterventionsPerRun <= 0 {
		return false
	}
	return atomic.LoadInt64(&w.interventionCount) >= int64(w.cfg.MaxInterventionsPerRun)
}

// applyIntervention applies the configured intervention for a detection in a
// PostMessageHook context. Returns the (possibly mutated) result.
// If the intervention cap is exceeded, returns the result unchanged but still
// records the detection.
func (w *ConclusionWatcher) applyIntervention(
	ctx context.Context,
	currentResult harness.PostMessageHookResult,
	response *harness.CompletionResult,
	detection DetectionResult,
	alreadyBlocked bool,
) (harness.PostMessageHookResult, bool) {
	// Below minimum confidence threshold: skip intervention.
	if w.cfg.MinConfidence > 0 && detection.Confidence < w.cfg.MinConfidence {
		return currentResult, alreadyBlocked
	}

	// Check cap.
	if w.checkMaxInterventions() {
		return currentResult, alreadyBlocked
	}

	// If already blocked by a previous detection in this step, don't double-block.
	if alreadyBlocked {
		atomic.AddInt64(&w.interventionCount, 1)
		w.emitIntervened(detection.RunID, detection, w.cfg.Mode)
		return currentResult, true
	}

	atomic.AddInt64(&w.interventionCount, 1)
	w.emitIntervened(detection.RunID, detection, w.cfg.Mode)

	switch w.cfg.Mode {
	case InterventionPauseForUser:
		return PauseForUser(detection), true

	case InterventionRequestCritique:
		if w.cfg.CritiqueProvider != nil {
			result, err := RequestCritique(ctx, currentResult, response, detection, w.cfg.CritiqueProvider)
			if err == nil {
				return result, false
			}
			// Fall back to inject on error.
		}
		prompt := w.cfg.ValidationPrompt
		if prompt == "" {
			prompt = DefaultValidationPrompt
		}
		return InjectValidationPrompt(currentResult, response, prompt, detection), false

	default: // InterventionInjectPrompt
		prompt := w.cfg.ValidationPrompt
		if prompt == "" {
			prompt = DefaultValidationPrompt
		}
		return InjectValidationPrompt(currentResult, response, prompt, detection), false
	}
}

// --- internal hook implementations ---

// postMessageHook implements harness.PostMessageHook.
type postMessageHook struct{ w *ConclusionWatcher }

func (h *postMessageHook) Name() string { return "conclusion-watcher-post-message" }

func (h *postMessageHook) AfterMessage(
	ctx context.Context,
	in harness.PostMessageHookInput,
) (harness.PostMessageHookResult, error) {
	// Increment currentStep at the start of each AfterMessage.
	atomic.AddInt64(&h.w.currentStep, 1)

	result := harness.PostMessageHookResult{Action: harness.HookActionContinue}
	response := in.Response // local copy

	// Run all 4 post-message detectors (phrase-based).
	type detectorFn func() *DetectionResult
	detectors := []detectorFn{
		func() *DetectionResult {
			if !h.w.isPatternArmed(PatternHedgeAssertion) {
				return nil
			}
			return DetectHedgeAssertion(in.RunID, in.Step, in.Response.Content)
		},
		func() *DetectionResult {
			if !h.w.isPatternArmed(PatternUnverifiedFileClaim) {
				return nil
			}
			return DetectUnverifiedFileClaim(in.RunID, in.Step, in.Response.Content, h.w.ledger)
		},
		func() *DetectionResult {
			if !h.w.isPatternArmed(PatternPrematureCompletion) {
				return nil
			}
			return DetectPrematureCompletion(in.RunID, in.Step, in.Response.Content, h.w.ledger)
		},
		func() *DetectionResult {
			if !h.w.isPatternArmed(PatternArchitectureAssumption) {
				return nil
			}
			return DetectArchitectureAssumption(in.RunID, in.Step, in.Response.Content, h.w.ledger)
		},
	}

	// Collect phrase detections.
	var phraseDetections []*DetectionResult
	for _, fn := range detectors {
		d := fn()
		phraseDetections = append(phraseDetections, d) // may be nil
	}

	// If an evaluator is configured, run it concurrently with phrase detectors.
	// We already collected phrase detections above; now await evaluator result.
	var evalResult *EvaluatorResult
	if h.w.cfg.Evaluator != nil {
		type evalOutcome struct {
			result *EvaluatorResult
			err    error
		}
		ch := make(chan evalOutcome, 1)
		// Build tool history for the evaluator from the ledger.
		toolHistoryStrs := h.w.ledger.RecentTools(10)
		// Build proposed tool names from ToolCalls in the hook input.
		var proposedTools []string
		for _, tc := range in.ToolCalls {
			proposedTools = append(proposedTools, tc.Name)
		}

		go func() {
			r, err := h.w.cfg.Evaluator.Evaluate(ctx, in.Response.Content, toolHistoryStrs, proposedTools)
			ch <- evalOutcome{r, err}
		}()

		// Wait for evaluator (ctx cancellation propagates via the goroutine's ctx arg).
		select {
		case outcome := <-ch:
			if outcome.err == nil {
				evalResult = outcome.result
			}
			// On error: evalResult stays nil → fall back to phrase detections below.
		case <-ctx.Done():
			// Context cancelled: fall back to phrase detections.
		}
	}

	// Merge: determine which detections to use.
	var finalDetections []*DetectionResult
	if evalResult != nil {
		if evalResult.HasUnjustifiedConclusion {
			// LLM says there IS a jump: use LLM detections.
			for _, pt := range evalResult.Patterns {
				d := &DetectionResult{
					Pattern:    pt,
					Confidence: 1.0,
					Evidence:   evalResult.Evidence,
					Step:       in.Step,
					RunID:      in.RunID,
				}
				finalDetections = append(finalDetections, d)
			}
			// If LLM returned patterns but empty patterns slice, still flag it.
			if len(evalResult.Patterns) == 0 {
				finalDetections = append(finalDetections, &DetectionResult{
					Pattern:    PatternHedgeAssertion, // default fallback pattern
					Confidence: 1.0,
					Evidence:   evalResult.Evidence,
					Step:       in.Step,
					RunID:      in.RunID,
				})
			}
		}
		// If HasUnjustifiedConclusion=false: suppress phrase detections (finalDetections stays empty).
	} else {
		// No evaluator or evaluator errored: use phrase detections.
		for _, d := range phraseDetections {
			if d != nil {
				finalDetections = append(finalDetections, d)
			}
		}
	}

	// Apply detections.
	alreadyBlocked := false
	for _, d := range finalDetections {
		h.w.recordDetection(*d)
		h.w.emitDetected(in.RunID, *d)

		result, alreadyBlocked = h.w.applyIntervention(ctx, result, &response, *d, alreadyBlocked)

		if result.MutatedResponse != nil {
			response = *result.MutatedResponse
		}
	}

	return result, nil
}

// preToolUseHook implements harness.PreToolUseHook.
type preToolUseHook struct{ w *ConclusionWatcher }

func (h *preToolUseHook) Name() string { return "conclusion-watcher-pre-tool-use" }

func (h *preToolUseHook) PreToolUse(
	ctx context.Context,
	ev harness.PreToolUseEvent,
) (*harness.PreToolUseResult, error) {
	step := int(atomic.LoadInt64(&h.w.currentStep))

	// Record tool in ledger.
	h.w.ledger.RecordTool(step, ev.ToolName, string(ev.Args))

	// If this is a read-type tool, extract file paths from args and record them.
	if ExplorationTools[ev.ToolName] {
		extractAndRecordPaths(h.w.ledger, ev.Args)
	}

	// Run SkippedDiagnostic detector.
	if !h.w.isPatternArmed(PatternSkippedDiagnostic) {
		return nil, nil
	}

	d := DetectSkippedDiagnostic(ev.RunID, step, ev.ToolName, ev.Args, h.w.ledger)
	if d == nil {
		return nil, nil
	}

	// Record detection.
	h.w.recordDetection(*d)
	h.w.emitDetected(ev.RunID, *d)

	// Check cap.
	if h.w.checkMaxInterventions() {
		return nil, nil
	}

	atomic.AddInt64(&h.w.interventionCount, 1)
	h.w.emitIntervened(ev.RunID, *d, h.w.cfg.Mode)

	// In pre-tool hook, both pause and inject modes deny the tool call.
	// For inject mode, the denial reason acts as the injected message for the next LLM turn.
	prompt := h.w.cfg.ValidationPrompt
	if prompt == "" {
		prompt = DefaultValidationPrompt
	}

	return &harness.PreToolUseResult{
		Decision: harness.ToolHookDeny,
		Reason:   d.Evidence + prompt,
	}, nil
}

// postToolUseHook implements harness.PostToolUseHook.
type postToolUseHook struct{ w *ConclusionWatcher }

func (h *postToolUseHook) Name() string { return "conclusion-watcher-post-tool-use" }

func (h *postToolUseHook) PostToolUse(
	ctx context.Context,
	ev harness.PostToolUseEvent,
) (*harness.PostToolUseResult, error) {
	// Record file paths from args (more reliable than parsing output).
	if ev.ToolName == "read_file" || ExplorationTools[ev.ToolName] {
		extractAndRecordPaths(h.w.ledger, ev.Args)
	}

	// Also scan the tool output for file path patterns (fallback for unknown tools).
	if ev.Result != "" {
		scanOutputForPaths(h.w.ledger, ev.Result)
	}

	return nil, nil
}

// extractAndRecordPaths extracts the "path" field from JSON args and records it.
func extractAndRecordPaths(ledger *ObservationLedger, args json.RawMessage) {
	if len(args) == 0 {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return
	}
	if path, ok := m["path"].(string); ok && path != "" {
		ledger.RecordFileSeen(path)
	}
	if paths, ok := m["paths"].([]any); ok {
		for _, p := range paths {
			if s, ok := p.(string); ok && s != "" {
				ledger.RecordFileSeen(s)
			}
		}
	}
}

// outputFilePathRe matches file paths in tool output lines.
var outputFilePathRe = regexp.MustCompile(
	`(?i)(?:^|[\s\t])([\w./\-]+\.(?:go|py|ts|js|jsx|tsx|yaml|yml|json|toml|md|sh|txt|env|cfg|conf))\b`,
)

// scanOutputForPaths scans tool result text for file paths and records them.
func scanOutputForPaths(ledger *ObservationLedger, output string) {
	for _, line := range strings.Split(output, "\n") {
		matches := outputFilePathRe.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) >= 2 && m[1] != "" {
				ledger.RecordFileSeen(m[1])
			}
		}
	}
}
