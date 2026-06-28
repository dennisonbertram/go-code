// Package benchresult provides a structured schema for benchmark run results.
// Every field is annotated to its real source: RunSummary, Run, DERIVED,
// RECONSTRUCTED, or EXTERNAL/opt-in. No fields are invented or inferred beyond
// the listed sources.
package benchresult

import (
	"fmt"
	"time"

	"go-agent-harness/internal/harness"
)

// BenchmarkResult is the serialisable record produced from a completed harness
// run. It is grounded 1:1 in harness.RunSummary and harness.Run — no invented
// fields. Fields marked DERIVED are computed deterministically from raw fields.
// Fields marked RECONSTRUCTED require caller-supplied context (rolloutDir).
// Fields marked EXTERNAL/opt-in are never populated by FromRun; callers fill
// them after the fact from external oracles (drift analysis, forensic logs).
type BenchmarkResult struct {
	// --- Identity (source: RunSummary.RunID / Run.ID — identical values) ---

	// RunID is the unique run identifier.
	// Source: RunSummary.RunID (= Run.ID).
	RunID string `json:"run_id"`

	// --- Status (source: RunSummary.Status) ---

	// Status is the terminal run status: "completed" or "failed".
	// Note: there is no task-level pass/fail here; that is an external oracle.
	// Source: RunSummary.Status (harness.RunStatus).
	Status string `json:"status"`

	// --- Step and token counters (source: RunSummary) ---

	// StepsTaken is the number of LLM turns completed before the run ended.
	// Source: RunSummary.StepsTaken.
	StepsTaken int `json:"steps_taken"`

	// TotalPromptTokens is the cumulative input-token count across all turns.
	// Source: RunSummary.TotalPromptTokens.
	TotalPromptTokens int `json:"total_prompt_tokens"`

	// TotalCompletionTokens is the cumulative output-token count across all turns.
	// Source: RunSummary.TotalCompletionTokens.
	TotalCompletionTokens int `json:"total_completion_tokens"`

	// --- Cost fields (source: RunSummary) ---

	// TotalCostUSD is the total monetary cost of the run in USD.
	// Source: RunSummary.TotalCostUSD.
	TotalCostUSD float64 `json:"total_cost_usd"`

	// CostStatus reports the pricing confidence level (e.g. "available",
	// "unpriced_model", "provider_unreported").
	// Source: RunSummary.CostStatus (harness.CostStatus).
	CostStatus string `json:"cost_status"`

	// --- Cache (source: RunSummary) ---

	// CacheHitRate is the fraction of prompt tokens served from cache ([0, 1]).
	// Source: RunSummary.CacheHitRate.
	CacheHitRate float64 `json:"cache_hit_rate"`

	// --- Error (source: RunSummary) ---

	// ErrorMessage is non-empty only when Status == "failed".
	// Source: RunSummary.Error.
	ErrorMessage string `json:"error_message,omitempty"`

	// --- Run identity fields (source: Run) ---

	// Model is the model name used for this run (e.g. "gpt-4.1-mini").
	// Source: Run.Model.
	Model string `json:"model"`

	// ProviderName is the resolved provider key (e.g. "openai", "anthropic").
	// Source: Run.ProviderName.
	ProviderName string `json:"provider_name,omitempty"`

	// Prompt is the task prompt submitted to the agent.
	// Source: Run.Prompt.
	Prompt string `json:"prompt"`

	// Output is the final agent response text.
	// Source: Run.Output.
	Output string `json:"output,omitempty"`

	// TenantID is the tenant scoping key, if set.
	// Source: Run.TenantID.
	TenantID string `json:"tenant_id,omitempty"`

	// ConversationID is the conversation continuity key, if set.
	// Source: Run.ConversationID.
	ConversationID string `json:"conversation_id,omitempty"`

	// AgentID is the agent identifier, if set.
	// Source: Run.AgentID.
	AgentID string `json:"agent_id,omitempty"`

	// --- Timestamps (source: Run) ---

	// CreatedAt is the UTC timestamp when the run was created.
	// Source: Run.CreatedAt.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the UTC timestamp of the last run state change.
	// Source: Run.UpdatedAt.
	UpdatedAt time.Time `json:"updated_at"`

	// --- DERIVED fields ---

	// DurationMs is the wall-clock run duration in milliseconds.
	// DERIVED: Run.UpdatedAt.Sub(Run.CreatedAt).Milliseconds().
	DurationMs int64 `json:"duration_ms"`

	// --- RECONSTRUCTED fields ---

	// RolloutPath is the expected JSONL rollout file path for this run.
	// RECONSTRUCTED: <rolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl, where the date
	// is derived from Run.CreatedAt in UTC. Empty when rolloutDir is unknown
	// (FromRun never populates this; call ReconstructRolloutPath separately).
	RolloutPath string `json:"rollout_path,omitempty"`

	// --- Tool call records (source: RunSummary.ToolCalls) ---

	// ToolCallRecords is the ordered list of tool invocations recorded during the run.
	// Source: RunSummary.ToolCalls ([]harness.ToolCallSummary).
	ToolCallRecords []ToolCallRecord `json:"tool_calls"`

	// --- EXTERNAL / opt-in fields ---

	// Drift is populated by an external drift-analysis oracle AFTER the run.
	// FromRun always leaves this nil; it is never derived from harness data.
	// EXTERNAL/opt-in.
	Drift *DriftSummary `json:"drift,omitempty"`

	// ForensicEvents is an optional slice of raw forensic event payloads
	// captured from the rollout JSONL by an external loader.
	// FromRun always leaves this nil; populate it from the rollout file if needed.
	// EXTERNAL/opt-in.
	ForensicEvents []map[string]any `json:"forensic_events,omitempty"`
}

// ToolCallRecord records a single tool invocation within a run.
// Source: harness.ToolCallSummary.
type ToolCallRecord struct {
	// ToolName is the registered name of the tool that was called.
	// Source: harness.ToolCallSummary.ToolName.
	ToolName string `json:"tool_name"`

	// Step is the LLM turn number during which the tool was called (1-indexed).
	// Source: harness.ToolCallSummary.Step.
	Step int `json:"step"`
}

// DriftSummary holds drift-analysis results produced by an external oracle
// (e.g. a comparison between this run's output and a reference baseline).
// All fields here are EXTERNAL — they must be filled by the caller from
// outside the harness API; none are available from RunSummary or Run.
type DriftSummary struct {
	// BaselineRunID is the run ID of the reference run being compared against.
	// EXTERNAL: must be provided by the caller.
	BaselineRunID string `json:"baseline_run_id,omitempty"`

	// OutputSimilarity is a [0, 1] similarity score between this run's output
	// and the baseline output, as computed by the external oracle.
	// EXTERNAL: must be provided by the caller.
	OutputSimilarity float64 `json:"output_similarity"`

	// DriftFlags is a list of free-text flags describing detected differences
	// (e.g. "token_count_increased_20pct", "missing_tool_call:bash").
	// EXTERNAL: must be provided by the caller.
	DriftFlags []string `json:"drift_flags,omitempty"`
}

// FromRun constructs a BenchmarkResult from a completed run's summary and
// run record. Every populated field maps 1:1 from summary or run (or is
// explicitly DERIVED). RECONSTRUCTED fields (RolloutPath) and EXTERNAL/opt-in
// fields (Drift, ForensicEvents) are left at their zero values.
//
// To populate RolloutPath, call ReconstructRolloutPath(rolloutDir, run) and
// assign the result:
//
//	result := benchresult.FromRun(summary, run)
//	result.RolloutPath = benchresult.ReconstructRolloutPath(cfg.RolloutDir, run)
func FromRun(summary harness.RunSummary, run harness.Run) BenchmarkResult {
	toolRecords := make([]ToolCallRecord, len(summary.ToolCalls))
	for i, tc := range summary.ToolCalls {
		toolRecords[i] = ToolCallRecord{
			ToolName: tc.ToolName,
			Step:     tc.Step,
		}
	}

	return BenchmarkResult{
		// Identity
		RunID: summary.RunID,

		// Status
		Status: string(summary.Status),

		// Step / token counters (source: RunSummary)
		StepsTaken:            summary.StepsTaken,
		TotalPromptTokens:     summary.TotalPromptTokens,
		TotalCompletionTokens: summary.TotalCompletionTokens,

		// Cost (source: RunSummary)
		TotalCostUSD: summary.TotalCostUSD,
		CostStatus:   string(summary.CostStatus),

		// Cache (source: RunSummary)
		CacheHitRate: summary.CacheHitRate,

		// Error (source: RunSummary)
		ErrorMessage: summary.Error,

		// Run identity (source: Run)
		Model:          run.Model,
		ProviderName:   run.ProviderName,
		Prompt:         run.Prompt,
		Output:         run.Output,
		TenantID:       run.TenantID,
		ConversationID: run.ConversationID,
		AgentID:        run.AgentID,

		// Timestamps (source: Run)
		CreatedAt: run.CreatedAt,
		UpdatedAt: run.UpdatedAt,

		// DERIVED: wall-clock duration
		DurationMs: run.UpdatedAt.Sub(run.CreatedAt).Milliseconds(),

		// RECONSTRUCTED: RolloutPath — left empty; caller uses ReconstructRolloutPath
		RolloutPath: "",

		// Tool calls (source: RunSummary.ToolCalls)
		ToolCallRecords: toolRecords,

		// EXTERNAL/opt-in — always nil/empty from FromRun
		Drift:          nil,
		ForensicEvents: nil,
	}
}

// ReconstructRolloutPath returns the expected JSONL rollout file path for run,
// using the convention: <rolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl, where the
// date is derived from run.CreatedAt in UTC.
//
// Returns an empty string when rolloutDir is empty — never guesses the directory.
// This matches the path written by RunnerConfig.RolloutDir in the harness.
func ReconstructRolloutPath(rolloutDir string, run harness.Run) string {
	if rolloutDir == "" {
		return ""
	}
	date := run.CreatedAt.UTC().Format("2006-01-02")
	return fmt.Sprintf("%s/%s/%s.jsonl", rolloutDir, date, run.ID)
}
