package replay

import (
	"encoding/json"
	"fmt"
	"sort"

	"go-agent-harness/internal/forensics/differ"
	"go-agent-harness/internal/forensics/rollout"
)

// DriftResult is the structured outcome of comparing an ORIGINAL recorded
// rollout against a REPLAYED one (a re-run of the harness against the recorded
// provider). It classifies the differ's step-level diff into the categories the
// drift contract cares about, separates tool-call divergence from generic
// content changes, and reports cost as a non-fatal delta.
//
// # Replay drift contract (deliverable E)
//
// Drift detection is the opt-in, second layer of replay (the first is integrity:
// offline causal-consistency checking of a single recorded stream, which never
// re-executes anything). For drift, the harness is re-run against the recorded
// provider (replay.RecordedProvider) with tool execution short-circuited to the
// recorded outputs (replay.NewReplayToolHandler). Every provider output and tool
// result is therefore fixed to the original recording, so the harness's own
// step/decision logic is the ONLY live variable. Any difference the diff finds
// is attributable to the harness — that is what "drift" means here.
//
// Both streams are canonicalized with rollout.DriftOptions before diffing. The
// contract partitions every field as DETERMINISTIC (a difference IS drift) or
// VARIABLE (stripped before diffing, so a difference is NOT drift):
//
//	DETERMINISTIC (drift if different):
//	  - per-step event TYPE sequence and step grouping
//	  - assistant content
//	  - the SET of tool calls (name + normalized arguments) and call_id→tool map
//	  - tool results fed back into the conversation
//	  - terminal outcome (completed vs failed)
//	  - total step / turn count
//
//	VARIABLE (stripped before diffing; never drift on its own):
//	  - ts / Timestamp; run_id; event id / seq
//	  - total_duration_ms / ttft_ms / latency_ms (wall-clock timings)
//	  - provider name; prompt_hash; model_version
//	  - within-step ORDERING of concurrent tool events (order-insensitive
//	    within a step — see normalizeWithinStep)
//
// COST is reported as CostDeltaUSD, never a hard mismatch: pricing or model
// drift is surfaced for inspection, not treated as a failed replay.
type DriftResult struct {
	// AddedSteps are step numbers present in the replay but not the original
	// (the replay took extra steps).
	AddedSteps []int `json:"added_steps"`
	// RemovedSteps are step numbers present in the original but not the replay
	// (the replay took fewer steps).
	RemovedSteps []int `json:"removed_steps"`
	// ChangedSteps are step numbers present in both whose deterministic content
	// diverged (after within-step reorder normalization).
	ChangedSteps []int `json:"changed_steps"`
	// DivergentToolCalls describes tool calls whose name or normalized arguments
	// differ between the runs, or that appear in only one run.
	DivergentToolCalls []ToolCallDivergence `json:"divergent_tool_calls"`
	// CostDeltaUSD is replayed total cost minus original total cost. Positive
	// means the replay cost more. Never causes Matched=false on its own.
	CostDeltaUSD float64 `json:"cost_delta_usd"`
	// OutcomeDiff is "identical", "completed-vs-failed", "failed-vs-completed",
	// or "diverged" (unknown terminal on one side).
	OutcomeDiff string `json:"outcome_diff"`
	// Score is the differ's regression score (winner + reasons), surfaced for
	// the caller's convenience. It does not affect Matched.
	Score differ.RegressionScore `json:"score"`
	// Matched is true only when there are NO added/removed/changed steps AND the
	// outcome is identical. Cost deltas and stripped variable fields do not
	// affect it.
	Matched bool `json:"matched"`
}

// ToolCallDivergence describes one tool call that differs between the original
// and replayed runs. Side is "original" or "replayed" for a call present in only
// one run; for a call present in both with different name/args, Side is "both".
type ToolCallDivergence struct {
	Step         int    `json:"step"`
	CallID       string `json:"call_id"`
	Side         string `json:"side"`
	OriginalTool string `json:"original_tool,omitempty"`
	ReplayedTool string `json:"replayed_tool,omitempty"`
	OriginalArgs string `json:"original_args,omitempty"`
	ReplayedArgs string `json:"replayed_args,omitempty"`
}

// DetectDrift canonicalizes both event streams with the drift options,
// normalizes within-step ordering of concurrent tool events to be
// order-insensitive, diffs them with the existing differ, and classifies the
// result into structured drift fields. See the DriftResult doc comment for the
// full replay drift contract.
//
// opts is normally rollout.DriftOptions; it is passed explicitly so callers can
// tune canonicalization (e.g. for tests). Whatever opts is given, the within-step
// reorder normalization is always applied so concurrent tool ordering is treated
// as VARIABLE.
func DetectDrift(original, replayed []rollout.RolloutEvent, opts rollout.CanonicalizationOptions) DriftResult {
	canonA := normalizeWithinStep(rollout.Canonicalize(original, opts))
	canonB := normalizeWithinStep(rollout.Canonicalize(replayed, opts))

	// Diff over the canonicalized streams WITH cost fields intact, so the differ
	// can compute the cost delta and the regression score (which weighs cost).
	d := differ.Diff(canonA, canonB)

	// Cost is reported as a delta, never as a hard step mismatch. Diff the STEP
	// structure over copies that have the cost-bearing fields stripped, so a
	// pure pricing/model cost change does not flag a step as "changed".
	costlessA := stripCostFields(canonA)
	costlessB := stripCostFields(canonB)
	stepDiff := differ.Diff(costlessA, costlessB)

	res := DriftResult{
		CostDeltaUSD: d.CostDelta,
		OutcomeDiff:  mapOutcome(d.OutcomeDiff),
		Score:        d.Score,
	}

	for _, sd := range stepDiff.StepDiffs {
		switch sd.Status {
		case "only_in_b":
			res.AddedSteps = append(res.AddedSteps, sd.Step)
		case "only_in_a":
			res.RemovedSteps = append(res.RemovedSteps, sd.Step)
		case "diverged":
			res.ChangedSteps = append(res.ChangedSteps, sd.Step)
		}
	}

	res.DivergentToolCalls = diffToolCalls(canonA, canonB)

	res.Matched = len(res.AddedSteps) == 0 &&
		len(res.RemovedSteps) == 0 &&
		len(res.ChangedSteps) == 0 &&
		res.OutcomeDiff == "identical"

	return res
}

// mapOutcome translates the differ's outcome vocabulary into the drift
// contract's vocabulary.
func mapOutcome(differOutcome string) string {
	switch differOutcome {
	case "identical":
		return "identical"
	case "b_failed":
		return "completed-vs-failed"
	case "a_failed":
		return "failed-vs-completed"
	default:
		return "diverged"
	}
}

// costFieldKeys are payload keys that carry run cost or its classification. They
// are stripped from a COPY of the streams before the structural step diff so that
// a pure cost difference (pricing or model drift) is surfaced as CostDeltaUSD
// rather than flagged as a changed step. The values are still read for the cost
// delta from the un-stripped streams.
//
// turn_cost_usd, cumulative_cost_usd and cost_totals are the cost amounts.
// cost_status / pricing_version are the cost CLASSIFICATION: a faithful replay
// against the recorded provider replays recorded cost AMOUNTS as explicit values
// (cost_status "available"), whereas the original may have derived cost from live
// pricing (e.g. "unpriced_model" for a model with no price). That classification
// flip carries no amount difference, so per the drift contract — COST is a delta,
// never a hard mismatch — it must not flag a step as changed.
var costFieldKeys = []string{
	"cumulative_cost_usd",
	"turn_cost_usd",
	"cost_totals",
	"cost_status",
	"pricing_version",
}

// stripCostFields returns a copy of events with cost-bearing payload keys
// removed. Payload values are shallow-copied at the top level (sufficient,
// because only top-level keys are deleted); the events themselves are not shared
// with the input so the caller's streams remain intact.
func stripCostFields(events []rollout.RolloutEvent) []rollout.RolloutEvent {
	out := make([]rollout.RolloutEvent, len(events))
	for i, ev := range events {
		out[i] = ev
		if ev.Payload == nil {
			continue
		}
		hasCost := false
		for _, k := range costFieldKeys {
			if _, ok := ev.Payload[k]; ok {
				hasCost = true
				break
			}
		}
		if !hasCost {
			continue
		}
		cp := make(map[string]any, len(ev.Payload))
		for k, v := range ev.Payload {
			cp[k] = v
		}
		for _, k := range costFieldKeys {
			delete(cp, k)
		}
		out[i].Payload = cp
	}
	return out
}

// normalizeWithinStep returns a copy of canonicalized events with events sorted
// into a stable order WITHIN each step, so that the order of concurrent tool
// events (which is non-deterministic between runs) does not register as drift.
// Step grouping and the relative order of distinct steps are preserved — only
// the order of events that share a step number is canonicalized.
//
// The input is assumed already sorted by step (rollout.Canonicalize guarantees
// this with a stable sort). Within a step, events are ordered by a stable
// signature: (type, canonical-payload-JSON). reflect-equal events thus land in
// a deterministic position regardless of their recorded order.
func normalizeWithinStep(events []rollout.RolloutEvent) []rollout.RolloutEvent {
	out := make([]rollout.RolloutEvent, len(events))
	copy(out, events)

	// Walk contiguous runs of equal Step and sort each run in place.
	i := 0
	for i < len(out) {
		j := i + 1
		for j < len(out) && out[j].Step == out[i].Step {
			j++
		}
		run := out[i:j]
		sort.SliceStable(run, func(a, b int) bool {
			return eventSignature(run[a]) < eventSignature(run[b])
		})
		i = j
	}
	return out
}

// eventSignature returns a stable, content-derived key for ordering events
// within a step. It combines the event type with a canonical JSON encoding of
// the payload (Go's encoding/json sorts map keys, so the encoding is stable for
// a given payload value).
func eventSignature(ev rollout.RolloutEvent) string {
	payloadSig := ""
	if ev.Payload != nil {
		if b, err := json.Marshal(ev.Payload); err == nil {
			payloadSig = string(b)
		}
	}
	return ev.Type + "\x00" + payloadSig
}

// recordedToolCall is the deterministic identity of a tool call: its name and
// normalized arguments, keyed by call_id within a step.
type recordedToolCall struct {
	step int
	tool string
	args string
}

// diffToolCalls compares the set of tool.call.started events between the two
// canonicalized streams, keyed by (step, call_id). A call present in only one
// stream, or present in both with a different tool name or arguments, is a
// divergence. Because lookups are keyed by call_id (not position), within-step
// reordering of concurrent calls does NOT register as divergence.
func diffToolCalls(a, b []rollout.RolloutEvent) []ToolCallDivergence {
	callsA := indexStartedCalls(a)
	callsB := indexStartedCalls(b)

	// Collect the union of keys in a stable (step, call_id) order.
	keys := make([]string, 0, len(callsA)+len(callsB))
	seen := make(map[string]bool, len(callsA)+len(callsB))
	for k := range callsA {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range callsB {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	stepOf := func(k string) int {
		if c, ok := callsA[k]; ok {
			return c.step
		}
		return callsB[k].step
	}
	sort.Slice(keys, func(i, j int) bool {
		si, sj := stepOf(keys[i]), stepOf(keys[j])
		if si != sj {
			return si < sj
		}
		return keys[i] < keys[j]
	})

	var out []ToolCallDivergence
	for _, k := range keys {
		ca, inA := callsA[k]
		cb, inB := callsB[k]
		callID := callIDFromKey(k)
		switch {
		case inA && !inB:
			out = append(out, ToolCallDivergence{
				Step: ca.step, CallID: callID, Side: "original",
				OriginalTool: ca.tool, OriginalArgs: ca.args,
			})
		case !inA && inB:
			out = append(out, ToolCallDivergence{
				Step: cb.step, CallID: callID, Side: "replayed",
				ReplayedTool: cb.tool, ReplayedArgs: cb.args,
			})
		default:
			if ca.tool != cb.tool || ca.args != cb.args {
				out = append(out, ToolCallDivergence{
					Step: ca.step, CallID: callID, Side: "both",
					OriginalTool: ca.tool, OriginalArgs: ca.args,
					ReplayedTool: cb.tool, ReplayedArgs: cb.args,
				})
			}
		}
	}
	return out
}

// indexStartedCalls builds a map keyed by "step\x00call_id" → recordedToolCall
// from the tool.call.started events. Calls without a call_id are keyed by a
// synthetic id so they are still surfaced if they diverge.
func indexStartedCalls(events []rollout.RolloutEvent) map[string]recordedToolCall {
	m := make(map[string]recordedToolCall)
	synthetic := 0
	for _, ev := range events {
		if ev.Type != "tool.call.started" {
			continue
		}
		callID, _ := payloadString(ev.Payload, "call_id")
		tool, _ := payloadString(ev.Payload, "tool")
		args, _ := payloadStringOrJSON(ev.Payload, "arguments")
		if callID == "" {
			callID = fmt.Sprintf("\x01synthetic-%d", synthetic)
			synthetic++
		}
		key := fmt.Sprintf("%d\x00%s", ev.Step, callID)
		m[key] = recordedToolCall{step: ev.Step, tool: tool, args: args}
	}
	return m
}

// callIDFromKey extracts the call_id portion of a "step\x00call_id" key.
func callIDFromKey(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[i+1:]
		}
	}
	return key
}
