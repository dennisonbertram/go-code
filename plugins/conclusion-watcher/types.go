// Package conclusionwatcher detects and intervenes on conclusion-jump patterns
// in LLM agent responses. It is a standalone plugin that wires into the harness
// via the existing PreToolUseHooks, PostMessageHooks, and PostToolUseHooks slices
// in RunnerConfig. Zero changes to internal/harness/ are needed.
package conclusionwatcher

import "context"

// PatternType identifies which conclusion pattern was detected.
type PatternType string

const (
	PatternHedgeAssertion         PatternType = "hedge_assertion"
	PatternUnverifiedFileClaim    PatternType = "unverified_file_claim"
	PatternPrematureCompletion    PatternType = "premature_completion"
	PatternSkippedDiagnostic      PatternType = "skipped_diagnostic"
	PatternArchitectureAssumption PatternType = "architecture_assumption"
)

// InterventionMode controls what the watcher does when a pattern fires.
type InterventionMode string

const (
	// InterventionInjectPrompt appends a validation request to the LLM
	// response text so that the next LLM turn receives the injected message.
	// This is the default mode.
	InterventionInjectPrompt InterventionMode = "inject_prompt"
	// InterventionPauseForUser blocks the step and surfaces the detection
	// to the user via HookActionBlock.
	InterventionPauseForUser InterventionMode = "pause_for_user"
	// InterventionRequestCritique fires a secondary LLM call through
	// CritiqueProvider and injects the critique into the response.
	InterventionRequestCritique InterventionMode = "request_critique"
)

// DetectionResult describes a single fired pattern.
type DetectionResult struct {
	Pattern    PatternType `json:"pattern"`
	Confidence float64     `json:"confidence"` // 0.0–1.0
	Evidence   string      `json:"evidence"`   // excerpt that triggered the match
	Step       int         `json:"step"`
	RunID      string      `json:"run_id"`
}

// CritiqueProvider is satisfied by anything that can produce a critique
// for a given piece of content. The harness Provider interface is NOT
// used directly — callers wire in a thin adapter if needed.
type CritiqueProvider interface {
	Critique(ctx context.Context, content string) (string, error)
}

// WatcherConfig holds all watcher options. Zero values produce safe defaults.
type WatcherConfig struct {
	// Patterns lists which PatternTypes to arm. Empty means all 5 armed.
	Patterns []PatternType

	// Mode selects the intervention strategy. Defaults to InterventionInjectPrompt.
	Mode InterventionMode

	// CritiqueProvider is required when Mode == InterventionRequestCritique.
	CritiqueProvider CritiqueProvider

	// Evaluator, if non-nil, runs in parallel with phrase detectors on each
	// AfterMessage call. The LLM result wins on conflict: if the evaluator
	// returns HasUnjustifiedConclusion=false, phrase detections are suppressed;
	// if it returns true, its detections are used instead of (or in addition to)
	// phrase detections. If the evaluator errors, phrase detections are used as
	// fallback.
	Evaluator Evaluator

	// EventEmitter, if non-nil, is called whenever a detection fires or an
	// intervention executes. The plugin does NOT register its own event types
	// in the harness event bus; it emits through this callback instead.
	// eventType will be one of the package-level EventConclusionDetected /
	// EventConclusionIntervened constants.
	EventEmitter func(eventType string, runID string, payload map[string]any)

	// ValidationPrompt is the text appended in InterventionInjectPrompt mode.
	// Defaults to DefaultValidationPrompt when empty.
	ValidationPrompt string

	// MaxInterventionsPerRun caps total interventions to avoid runaway injection.
	// 0 means unlimited.
	MaxInterventionsPerRun int

	// MinConfidence suppresses interventions below this threshold. Default 0.0 = all fire.
	MinConfidence float64
}

// Plugin-scoped event type strings. These are plain string constants,
// intentionally NOT of type harness.EventType, so the harness events.go
// and AllEventTypes() are never touched.
const (
	EventConclusionDetected   = "conclusion.detected"
	EventConclusionIntervened = "conclusion.intervened"
)

// DefaultValidationPrompt is injected when ValidationPrompt is empty and
// Mode == InterventionInjectPrompt.
const DefaultValidationPrompt = "\n\n[WATCHER] PAUSE: The previous reasoning contains an assertion about code or state that has not been verified by prior tool use. Before proceeding with any write, edit, or modification: (1) Read the relevant file(s) using the read tool. (2) Verify your assumption is correct. (3) Only then proceed with the planned action."
