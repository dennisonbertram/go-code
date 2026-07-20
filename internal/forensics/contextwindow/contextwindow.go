// Package contextwindow provides types and helpers for capturing context window
// state snapshots during LLM agent runs.
//
// Token estimation uses utf8.RuneCountInString(s)/4 as a rough approximation.
// This is deliberately simple and documented as an estimate — the only accurate
// token count comes from the provider usage response. All estimated values are
// labeled with Estimated:true in the snapshot payload.
package contextwindow

import (
	"unicode/utf8"
)

// EstimateTokens estimates the number of tokens in a string using a simple
// rune-count heuristic: (rune_count + 3) / 4 (rounding up to nearest integer).
//
// This is a rough approximation. Different tokenisers (BPE, SentencePiece, etc.)
// and different languages (e.g. CJK characters) will produce very different
// results. The value is always labeled as an estimate and should never be used
// for billing or strict limit enforcement.
func EstimateTokens(s string) int {
	runes := utf8.RuneCountInString(s)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

// ContextBreakdown is a best-effort decomposition of context window usage
// for a single LLM turn. All token counts except ProviderReportedTotal are
// estimated using the rune-count heuristic and are labeled Estimated:true.
type ContextBreakdown struct {
	// SystemPromptTokens is the estimated token count of the system prompt
	// text (static system prompt + runtime context, combined).
	// This is an estimate; Estimated is always true.
	SystemPromptTokens int `json:"system_prompt_tokens"`

	// ConversationTokens is the estimated token count of the conversation
	// history messages (user + assistant + tool messages).
	// This is an estimate; Estimated is always true.
	ConversationTokens int `json:"conversation_tokens"`

	// ToolResultTokens is the estimated token count of tool result messages
	// in the current conversation.
	// This is an estimate; Estimated is always true.
	ToolResultTokens int `json:"tool_result_tokens"`

	// Estimated indicates whether any of the above counts are estimates
	// (i.e. not derived from the provider usage response). Always true for
	// all fields in this struct.
	Estimated bool `json:"estimated"`
}

// WindowSnapshot captures the context window state after an LLM turn.
type WindowSnapshot struct {
	// Step is the 1-based step number within the run.
	Step int `json:"step"`

	// ProviderReportedTokens is the total token count reported by the provider
	// in the usage response. Zero when the provider did not report usage.
	ProviderReportedTokens int `json:"provider_reported_tokens"`

	// ProviderReported is true when ProviderReportedTokens came from the
	// provider usage response and is not an estimate.
	ProviderReported bool `json:"provider_reported"`

	// EstimatedTotalTokens is a best-effort estimate of the total context
	// window usage (system prompt + conversation + tool results) based on
	// the rune-count heuristic. Always labeled estimated.
	EstimatedTotalTokens int `json:"estimated_total_tokens"`

	// MaxContextTokens is the model's maximum context window size in tokens.
	// Sourced from the provider catalog when available. Zero when unknown.
	MaxContextTokens int `json:"max_context_tokens"`

	// UsageRatio is the fraction of the context window currently in use.
	// When ProviderReported is true, this uses ProviderReportedTokens as the
	// numerator; otherwise EstimatedTotalTokens is used.
	// Zero when MaxContextTokens is zero (unknown window size).
	UsageRatio float64 `json:"usage_ratio"`

	// HeadroomTokens is the estimated number of tokens remaining before the
	// context window is full. Negative values are possible when an estimate
	// exceeds MaxContextTokens. Zero when MaxContextTokens is zero.
	HeadroomTokens int `json:"headroom_tokens"`

	// Breakdown provides a labelled decomposition of context usage.
	// All sub-fields are estimates (Estimated:true).
	Breakdown ContextBreakdown `json:"breakdown"`
}

// CompactionTokenCounts holds before/after token counts for a compaction event.
type CompactionTokenCounts struct {
	// BeforeTokens is the estimated token count before compaction.
	BeforeTokens int `json:"before_tokens"`
	// AfterTokens is the estimated token count after compaction.
	AfterTokens int `json:"after_tokens"`
	// Estimated is always true — counts come from the rune heuristic.
	Estimated bool `json:"estimated"`
}

// BuildSnapshot constructs a WindowSnapshot for the given inputs.
//
//   - step: 1-based step number.
//   - systemPromptText: concatenated system prompt text (static + runtime context).
//   - conversationMessages: slice of (role, content) pairs for the conversation.
//   - providerPromptTokens: token count from provider usage response; 0 if not reported.
//   - providerReported: true when providerPromptTokens came from the provider.
//   - maxContextTokens: model context window size from catalog; 0 if unknown.
func BuildSnapshot(
	step int,
	systemPromptText string,
	messages []MessageForEstimate,
	providerPromptTokens int,
	providerReported bool,
	maxContextTokens int,
) WindowSnapshot {
	sysTokens := EstimateTokens(systemPromptText)

	var convTokens, toolTokens int
	for _, m := range messages {
		est := EstimateTokens(m.Content)
		if m.Role == "tool" {
			toolTokens += est
		} else {
			convTokens += est
		}
	}

	estimatedTotal := sysTokens + convTokens + toolTokens

	// Choose the best numerator for the ratio.
	numerator := estimatedTotal
	if providerReported && providerPromptTokens > 0 {
		numerator = providerPromptTokens
	}

	var ratio float64
	headroom := 0
	if maxContextTokens > 0 {
		ratio = float64(numerator) / float64(maxContextTokens)
		headroom = maxContextTokens - numerator
	}

	return WindowSnapshot{
		Step:                   step,
		ProviderReportedTokens: providerPromptTokens,
		ProviderReported:       providerReported,
		EstimatedTotalTokens:   estimatedTotal,
		MaxContextTokens:       maxContextTokens,
		UsageRatio:             ratio,
		HeadroomTokens:         headroom,
		Breakdown: ContextBreakdown{
			SystemPromptTokens: sysTokens,
			ConversationTokens: convTokens,
			ToolResultTokens:   toolTokens,
			Estimated:          true,
		},
	}
}

// MessageForEstimate is a minimal message representation used for token estimation.
type MessageForEstimate struct {
	Role    string
	Content string
}

// SnapshotToPayload converts a WindowSnapshot to a generic payload map suitable
// for inclusion in SSE event payloads.
func SnapshotToPayload(s WindowSnapshot) map[string]any {
	return map[string]any{
		"step":                     s.Step,
		"provider_reported_tokens": s.ProviderReportedTokens,
		"provider_reported":        s.ProviderReported,
		"estimated_total_tokens":   s.EstimatedTotalTokens,
		"max_context_tokens":       s.MaxContextTokens,
		"usage_ratio":              s.UsageRatio,
		"headroom_tokens":          s.HeadroomTokens,
		"breakdown": map[string]any{
			"system_prompt_tokens": s.Breakdown.SystemPromptTokens,
			"conversation_tokens":  s.Breakdown.ConversationTokens,
			"tool_result_tokens":   s.Breakdown.ToolResultTokens,
			"estimated":            s.Breakdown.Estimated,
		},
	}
}
