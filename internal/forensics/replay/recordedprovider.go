package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
)

// RecordedProvider is a harness.Provider that replays a rollout's recorded LLM
// turns instead of calling a live model. It is the "live variable isolation"
// half of the drift-detection contract (deliverable E): when the harness is
// re-run against a RecordedProvider (with tool execution short-circuited by the
// replay tool dispatch, below), every provider output and tool result is fixed
// to what the original run produced. The harness's own step/decision logic is
// then the ONLY live variable, so any divergence in the re-run is attributable
// to the harness rather than to model nondeterminism.
//
// Reconstruction contract (must match how the runner emits events):
//   - One scripted turn is produced per step that contains an llm.turn.completed
//     event. Turns are ordered by step.
//   - A turn's ToolCalls come from that step's tool.call.started events
//     (call_id → ID, tool → Name, arguments → Arguments). They are NOT read
//     from llm.turn.completed, which carries only a tool_calls COUNT in real
//     rollouts (runner_step_engine.go:492-498).
//   - A turn's Content comes from that step's assistant.message event, which the
//     runner emits only on the terminal no-tool turn
//     (runner_step_engine.go:637). Tool-calling turns therefore have empty
//     Content, matching the live runner where Content for those turns is
//     typically empty.
//   - A turn's Usage/Cost come from that step's usage.delta event
//     (recordAccounting: turn_usage / turn_cost_usd).
type RecordedProvider struct {
	mu    sync.Mutex
	turns []harness.CompletionResult
	next  int
}

// recordedTurn is the in-progress accumulation for a single step while scanning
// events in file order. It is finalized into a harness.CompletionResult once all
// of the step's events have been observed.
type recordedTurn struct {
	step      int
	hasTurn   bool // saw an llm.turn.completed for this step
	content   string
	toolCalls []harness.ToolCall
	usage     *harness.CompletionUsage
	costUSD   *float64
	hasCost   bool
}

// NewRecordedProvider ingests rollout events and builds the ordered list of
// scripted CompletionResults, one per LLM turn. The events must satisfy the
// rollout loader invariants (validateEvents); a malformed stream is rejected
// rather than silently producing a partial script, because a recorded provider
// built from a tampered rollout would invalidate the drift comparison.
func NewRecordedProvider(events []rollout.RolloutEvent) (*RecordedProvider, error) {
	if err := validateEvents(events); err != nil {
		return nil, fmt.Errorf("replay: cannot build recorded provider from invalid rollout: %w", err)
	}

	// Accumulate per-step turn state in file order. Using a map keyed by step
	// plus an explicit ordered slice of step numbers keeps the turn order stable
	// and independent of map iteration order.
	byStep := make(map[int]*recordedTurn)
	var stepOrder []int

	getStep := func(step int) *recordedTurn {
		rt, ok := byStep[step]
		if !ok {
			rt = &recordedTurn{step: step}
			byStep[step] = rt
			stepOrder = append(stepOrder, step)
		}
		return rt
	}

	for _, ev := range events {
		switch ev.Type {
		case "llm.turn.completed":
			getStep(ev.Step).hasTurn = true

		case "assistant.message":
			content, _ := payloadString(ev.Payload, "content")
			rt := getStep(ev.Step)
			// The runner emits at most one assistant.message per step (terminal
			// no-tool turn). If somehow present more than once, the last wins,
			// matching "last write" recording semantics.
			rt.content = capString(content, maxDetailStringBytes)

		case "tool.call.started":
			callID, _ := payloadString(ev.Payload, "call_id")
			toolName, _ := payloadString(ev.Payload, "tool")
			// Arguments may be a string (the common runner format) or, in a
			// JSON-decoded rollout, a map; payloadStringOrJSON normalizes both.
			args, _ := payloadStringOrJSON(ev.Payload, "arguments")
			rt := getStep(ev.Step)
			rt.toolCalls = append(rt.toolCalls, harness.ToolCall{
				ID:        capString(callID, maxDetailStringBytes),
				Name:      capString(toolName, maxDetailStringBytes),
				Arguments: capString(args, maxToolArgMarshalBytes),
			})

		case "usage.delta":
			rt := getStep(ev.Step)
			if u := extractTurnUsage(ev.Payload); u != nil {
				rt.usage = u
			}
			if cost, ok := extractTurnCost(ev.Payload); ok {
				rt.costUSD = &cost
				rt.hasCost = true
			}
		}
	}

	// Steps may be processed in file order, but ensure the scripted turn order is
	// strictly by step number (file order is already non-decreasing by step per
	// validateEvents, but sorting makes the intent explicit and robust).
	sort.Ints(stepOrder)

	var turns []harness.CompletionResult
	for _, step := range stepOrder {
		rt := byStep[step]
		if !rt.hasTurn {
			// A step with tool/usage events but no llm.turn.completed is not a
			// distinct LLM turn (should not happen for well-formed rollouts).
			continue
		}
		res := harness.CompletionResult{
			Content:   rt.content,
			ToolCalls: rt.toolCalls,
			Usage:     rt.usage,
		}
		if rt.hasCost {
			res.CostUSD = rt.costUSD
		}
		turns = append(turns, res)
	}

	return &RecordedProvider{turns: turns}, nil
}

// TurnCount returns the number of scripted turns (one per recorded LLM turn).
func (p *RecordedProvider) TurnCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.turns)
}

// Complete implements harness.Provider by returning the next scripted turn in
// recorded order. If req.Stream is non-nil the recorded content is streamed as a
// single delta so streaming callers observe the same content they would from a
// live provider. When the script is exhausted it returns an error so an
// over-running re-run (the harness taking more turns than the original) is
// surfaced rather than masked by a silent empty turn.
func (p *RecordedProvider) Complete(ctx context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return harness.CompletionResult{}, err
	}

	p.mu.Lock()
	if p.next >= len(p.turns) {
		idx := p.next
		p.next++
		p.mu.Unlock()
		return harness.CompletionResult{}, fmt.Errorf("replay: recorded provider exhausted after %d turns (re-run requested turn %d)", len(p.turns), idx+1)
	}
	res := p.turns[p.next]
	p.next++
	p.mu.Unlock()

	// Return an independent copy so callers cannot mutate the script.
	out := harness.CompletionResult{
		Content: res.Content,
		Usage:   res.Usage,
		CostUSD: res.CostUSD,
	}
	if len(res.ToolCalls) > 0 {
		out.ToolCalls = append([]harness.ToolCall(nil), res.ToolCalls...)
	}
	// Set UsageStatus/CostStatus to match what the original recorded run reported.
	// Without these, recordAccounting re-derives them from the result fields and
	// may produce a non-zero CostDeltaUSD on an identical replay (spurious drift).
	if res.Usage != nil {
		out.UsageStatus = harness.UsageStatusProviderReported
		if res.CostUSD != nil {
			out.CostStatus = harness.CostStatusAvailable
		}
	}

	if req.Stream != nil && out.Content != "" {
		req.Stream(harness.CompletionDelta{Content: out.Content})
	}

	return out, nil
}

// extractTurnUsage pulls the per-turn usage map (turn_usage) from a usage.delta
// payload and converts it to a *harness.CompletionUsage. Numbers decoded from
// JSON arrive as float64; in-process maps may carry int. Returns nil when no
// usage information is present.
func extractTurnUsage(payload map[string]any) *harness.CompletionUsage {
	if payload == nil {
		return nil
	}
	raw, ok := payload["turn_usage"].(map[string]any)
	if !ok {
		return nil
	}
	u := harness.CompletionUsage{
		PromptTokens:     intField(raw, "prompt_tokens"),
		CompletionTokens: intField(raw, "completion_tokens"),
		TotalTokens:      intField(raw, "total_tokens"),
	}
	if c, ok := intFieldPtr(raw, "cached_prompt_tokens"); ok {
		u.CachedPromptTokens = c
	}
	if rt, ok := intFieldPtr(raw, "reasoning_tokens"); ok {
		u.ReasoningTokens = rt
	}
	return &u
}

// extractTurnCost pulls the per-turn cost (turn_cost_usd) from a usage.delta
// payload. The bool reports whether the field was present.
func extractTurnCost(payload map[string]any) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	switch v := payload["turn_cost_usd"].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// intField returns the integer value of key in m, accepting float64 (JSON), int,
// and int64. Missing or non-numeric values yield 0.
func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

// intFieldPtr is like intField but distinguishes present (ok=true) from absent.
func intFieldPtr(m map[string]any, key string) (*int, bool) {
	raw, exists := m[key]
	if !exists {
		return nil, false
	}
	switch v := raw.(type) {
	case float64:
		n := int(v)
		return &n, true
	case int:
		n := v
		return &n, true
	case int64:
		n := int(v)
		return &n, true
	default:
		return nil, false
	}
}

// NewReplayToolHandler returns a harness.ToolHandler that short-circuits live
// tool execution: instead of running the tool, it returns the recorded
// tool.call.completed output for the call_id carried in the context (set by the
// runner via htools.ContextKeyToolCallID). The output is resolved with the F0
// result/output fallback so real captured rollouts — which emit under the
// "output" key — are not silently truncated.
//
// Registering this single handler for every tool name (or wiring it as the
// dispatch for a replay Registry) makes the re-run's tool results identical to
// the original recording, so the harness's tool-dispatch/decision logic is the
// only live variable for those calls.
//
// An unrecorded call_id is an error rather than an empty string: in a faithful
// replay the harness must request exactly the calls the original made, so a
// missing recording signals harness drift and must not be masked.
func NewReplayToolHandler(events []rollout.RolloutEvent) (harness.ToolHandler, error) {
	d, err := NewReplayToolDispatch(events)
	if err != nil {
		return nil, err
	}
	return d.Handler, nil
}

// ReplayToolDispatch holds an index from call_id to recorded tool output for
// replay tool execution. Use Handler as a harness.ToolHandler, or Output to look
// up a recorded result directly.
type ReplayToolDispatch struct {
	// outputs maps call_id → recorded tool output (already F0 result/output
	// resolved). Keys are the raw recorded call_ids.
	outputs map[string]string
}

// NewReplayToolDispatch indexes a rollout's tool.call.completed outputs by
// call_id. It validates the rollout first (rejecting tampered streams) and uses
// the shared completion index so the F0 result/output fallback is applied
// consistently with Replay/ReconstructMessages.
func NewReplayToolDispatch(events []rollout.RolloutEvent) (*ReplayToolDispatch, error) {
	if err := validateEvents(events); err != nil {
		return nil, fmt.Errorf("replay: cannot build replay tool dispatch from invalid rollout: %w", err)
	}
	// Walk events to build a literal-call_id → output map. We key off the
	// un-sanitized call_id so lookups match the id the runner places in the tool
	// context, and apply the same F0 result/output fallback indexToolCompletions
	// uses.
	outputs := make(map[string]string)
	for _, ev := range events {
		if ev.Type != "tool.call.completed" || ev.Payload == nil {
			continue
		}
		callID, ok := payloadString(ev.Payload, "call_id")
		if !ok || callID == "" || len(callID) > maxIDBytes {
			continue
		}
		if _, exists := outputs[callID]; exists {
			// Duplicate completion: keep the first (matches indexToolCompletions,
			// which treats later duplicates as integrity failures, not overrides).
			continue
		}
		// F0 fallback: prefer "result", fall back to "output".
		out, _ := payloadString(ev.Payload, "result")
		if out == "" {
			out, _ = payloadString(ev.Payload, "output")
		}
		outputs[callID] = out
	}
	return &ReplayToolDispatch{outputs: outputs}, nil
}

// Output returns the recorded output for a call_id and whether it was found.
func (d *ReplayToolDispatch) Output(callID string) (string, bool) {
	out, ok := d.outputs[callID]
	return out, ok
}

// Handler is a harness.ToolHandler that resolves the recorded output for the
// call_id in the context. The args are ignored: in a faithful replay the
// recorded output is authoritative regardless of the (already-recorded)
// arguments the harness reconstructs.
func (d *ReplayToolDispatch) Handler(ctx context.Context, _ json.RawMessage) (string, error) {
	callID := htools.ToolCallIDFromContext(ctx)
	if callID == "" {
		return "", fmt.Errorf("replay: no tool_call_id in context; cannot resolve recorded output")
	}
	out, ok := d.outputs[callID]
	if !ok {
		return "", fmt.Errorf("replay: no recorded tool.call.completed for call_id %q", sanitizeMismatch(callID))
	}
	return out, nil
}
