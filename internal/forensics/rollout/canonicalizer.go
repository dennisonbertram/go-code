package rollout

import (
	"sort"
	"time"
)

// deepCopyPayloadMap returns a recursive deep copy of a map[string]any,
// preventing aliasing between the canonicalized output and the original event.
func deepCopyPayloadMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyPayloadValue(v)
	}
	return out
}

func deepCopyPayloadValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyPayloadMap(val)
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = deepCopyPayloadValue(elem)
		}
		return out
	default:
		return v
	}
}

// CanonicalizationOptions controls which fields are stripped when
// canonicalizing rollout events for comparison.
type CanonicalizationOptions struct {
	StripTimestamps bool
	StripRunIDs     bool
	StripEventIDs   bool
	// StripPerRunConversationID strips "conversation_id" from event payloads.
	// conversation_id CAN be a stable cross-run identifier (multi-turn
	// conversations reuse one conversation_id), so DefaultOptions leaves it
	// intact. DriftOptions sets this to true because a drift re-run always
	// gets a fresh run/conversation ID, so stripping it is required for the
	// re-run to match the original.
	StripPerRunConversationID bool
	// StripVolatileLLMMeta removes per-call LLM metadata that varies run-to-run
	// even when the harness behaves identically — wall-clock timings
	// (total_duration_ms, ttft_ms, latency_ms), the provider name, the
	// prompt_hash, and the model_version. These are part of the VARIABLE side of
	// the replay drift contract (see replay.DriftOptions / replay.DetectDrift):
	// they are stripped before diffing so that pricing/model/provider/timing
	// drift is not mistaken for harness drift. Deterministic content (assistant
	// content, tool name/arguments, tool results, outcome) is never stripped.
	StripVolatileLLMMeta bool
}

// volatileLLMMetaKeys are the per-call metadata keys removed when
// StripVolatileLLMMeta is set. They vary between otherwise-identical runs.
//
// The first group is per-LLM-call metadata (provider/model/prompt identity and
// the LLM-call timings). The wall-clock group (*_ms) covers EVERY wall-clock
// timing the runner emits, not just LLM-call timings: step_start_ms is an
// absolute epoch timestamp and duration_ms is an elapsed wall-clock measurement,
// both of which differ run-to-run even when the harness behaves identically.
// They are the same VARIABLE class as total_duration_ms/ttft_ms/latency_ms in
// the drift contract and must be stripped so timing noise is not mistaken for
// harness drift.
var volatileLLMMetaKeys = []string{
	"total_duration_ms",
	"ttft_ms",
	"latency_ms",
	"step_start_ms",
	"duration_ms",
	"provider",
	"prompt_hash",
	"model_version",
}

// DefaultOptions strips timestamps, run IDs, and event IDs for comparison.
var DefaultOptions = CanonicalizationOptions{
	StripTimestamps: true,
	StripRunIDs:     true,
	StripEventIDs:   true,
}

// DriftOptions is DefaultOptions plus StripPerRunConversationID and
// StripVolatileLLMMeta: it strips every VARIABLE field of the replay drift
// contract so that two canonicalized streams differ only when the harness's own
// deterministic step/decision logic diverged. Used by replay.DetectDrift.
var DriftOptions = CanonicalizationOptions{
	StripTimestamps:           true,
	StripRunIDs:               true,
	StripEventIDs:             true,
	StripPerRunConversationID: true,
	StripVolatileLLMMeta:      true,
}

// Canonicalize returns a copy of events with non-deterministic fields stripped
// according to opts, and sorted by step then original sequence order.
func Canonicalize(events []RolloutEvent, opts CanonicalizationOptions) []RolloutEvent {
	result := make([]RolloutEvent, len(events))
	for i, ev := range events {
		result[i] = canonicalizeEvent(ev, opts)
	}

	// Stable sort by step, preserving original order within a step.
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Step < result[j].Step
	})

	return result
}

func canonicalizeEvent(ev RolloutEvent, opts CanonicalizationOptions) RolloutEvent {
	out := RolloutEvent{
		ID:        ev.ID,
		Type:      ev.Type,
		Step:      ev.Step,
		Timestamp: ev.Timestamp,
	}

	if opts.StripTimestamps {
		out.Timestamp = time.Time{}
	}
	if opts.StripEventIDs {
		out.ID = ""
	}

	// HIGH-2 fix (round 29): deep copy the payload before stripping fields.
	// The previous shallow copy shared nested map[string]any and []any values
	// with the original event. Any downstream mutation of the canonicalized
	// copy's nested structures silently corrupts the original event payload,
	// causing aliasing bugs in multi-stage replay/redaction pipelines.
	if ev.Payload != nil {
		cleaned := deepCopyPayloadMap(ev.Payload)
		if opts.StripRunIDs {
			delete(cleaned, "run_id")
		}
		if opts.StripPerRunConversationID {
			// conversation_id defaults to the run id (runner.StartRun assigns
			// run.ConversationID = run.ID when none is supplied), so it is a
			// per-run identifier for re-run scenarios. It is only stripped when
			// explicitly requested (DriftOptions) because conversation_id can
			// also be a stable cross-run identifier in multi-turn conversations.
			delete(cleaned, "conversation_id")
		}
		if opts.StripTimestamps {
			delete(cleaned, "timestamp")
			delete(cleaned, "ts")
		}
		if opts.StripEventIDs {
			delete(cleaned, "id")
			delete(cleaned, "event_id")
		}
		if opts.StripVolatileLLMMeta {
			for _, k := range volatileLLMMetaKeys {
				delete(cleaned, k)
			}
		}
		out.Payload = cleaned
	}

	return out
}
