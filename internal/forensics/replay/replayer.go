// Package replay provides offline simulation replay and fork-from-step
// capabilities for JSONL rollout files. It reuses the shared rollout loader
// from internal/forensics/rollout/.
//
// Phase 2 (Replayer): loads a rollout, reconstructs the message history,
// and for each tool call returns the recorded output instead of executing
// live. Verifies that the event sequence matches the original.
//
// Phase 3 (Fork): loads a rollout up to step N and reconstructs the
// []harness.Message history, ready to hand off to a live runner.
package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
)

// maxMismatchStringBytes caps the length of attacker-controlled strings
// embedded in mismatch messages.
const maxMismatchStringBytes = 1024 // 1 KiB

// sanitizeMismatch strips control characters and bidi/format chars from
// attacker-controlled strings. Caps FIRST to bound the strings.Map allocation.
func sanitizeMismatch(s string) string {
	s = capString(s, maxMismatchStringBytes)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, s)
}

// errCapExceeded is returned by cappedWriter.Write when the byte cap is reached.
var errCapExceeded = errors.New("cap exceeded")

// maxDetailStringBytes caps individual string fields in ReplayEvent.Details
// and harness.Message.
const maxDetailStringBytes = 65536 // 64 KiB

// capString truncates s to at most limit bytes at a rune boundary.
func capString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...<truncated>"
}

// maxPayloadDepth limits recursion in deepCapStrings to prevent runaway
// traversal on deeply nested (non-JSON) structures.
const maxPayloadDepth = 20

// maxPayloadElements limits the total number of elements visited per
// deepCapStrings call, bounding CPU and memory usage for adversarial payloads.
const maxPayloadElements = 100000

// deepCapStrings recursively caps all string values in v at maxDetailStringBytes,
// with depth and element-count budgets to prevent runaway traversal.
// CRITICAL-2 fix: the earlier shallow cap left nested structures verbatim.
// HIGH-3 fix: adds depth and element budgets to bound CPU/memory usage.
func deepCapStrings(v any) any {
	count := 0
	return deepCapWithBudget(v, maxPayloadDepth, &count)
}

func deepCapWithBudget(v any, depth int, count *int) any {
	*count++
	if depth <= 0 || *count > maxPayloadElements {
		return "<payload:truncated>"
	}
	switch val := v.(type) {
	case string:
		return capString(val, maxDetailStringBytes)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, v2 := range val {
			// HIGH-6 fix: cap map keys to bound memory/log-viewer issues from
			// attacker-controlled large keys. Cap before using as map key.
			cappedKey := capString(k, maxDetailStringBytes)
			out[cappedKey] = deepCapWithBudget(v2, depth-1, count)
		}
		return out
	case map[string]string:
		// HIGH-6 fix: handle typed string maps (not produced by JSON decoder but
		// possible in in-process callers). Cap both keys and values.
		out := make(map[string]string, len(val))
		for k, s := range val {
			out[capString(k, maxDetailStringBytes)] = capString(s, maxDetailStringBytes)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = deepCapWithBudget(elem, depth-1, count)
		}
		return out
	case []string:
		// HIGH-6 fix: handle typed string slices.
		out := make([]string, len(val))
		for i, s := range val {
			out[i] = capString(s, maxDetailStringBytes)
		}
		return out
	default:
		return v
	}
}

// maxIDBytes is the hard limit on tool call ID length. IDs exceeding this
// limit are rejected as schema violations. Prefix-hashing oversized IDs
// allows collision attacks on announcement/lifecycle integrity checks.
const maxIDBytes = 256

// capID returns a map key for an ID validated to be within maxIDBytes.
// All callers MUST reject oversized IDs before calling this function.
// The "l:" prefix separates this literal key namespace from reserved prefixes.
func capID(id string) string {
	return "l:" + id
}

// maxTotalCallIDs caps how many distinct call IDs are tracked per pass.
const maxTotalCallIDs = 10000

// maxMismatches caps how many mismatch strings are stored in ReplayResult.
const maxMismatches = 1000

// mismatch sets result.Matched=false and (if re != nil) re.Matched=false,
// then appends msg to result.Mismatches up to maxMismatches with a sentinel.
func mismatch(result *ReplayResult, re *ReplayEvent, msg string) {
	result.Matched = false
	if re != nil {
		re.Matched = false
	}
	if len(result.Mismatches) < maxMismatches {
		result.Mismatches = append(result.Mismatches, msg)
	} else if len(result.Mismatches) == maxMismatches {
		result.Mismatches = append(result.Mismatches,
			"(further mismatches suppressed)")
	}
}

// validateEvents checks that events satisfy the invariants rollout.LoadReader
// enforces. Callers (Fork, ReconstructMessages, etc.) that may receive
// non-loader-validated events should call this first and reject on error.
//
// CRITICAL-1 fix: without this check, sortEvents() inside ReconstructMessages
// can launder a non-monotonic input into an apparently-valid causal order, e.g.
// placing tool.call.completed (step=2) before tool.call.started (step=1) in the
// slice; sorting by step produces the correct order, bypassing file-order checks.
//
// HIGH-4 fix: deeper invariant checks catch additional fabricated structures:
// - Multiple run.started events (attacker can inject a new conversation start)
// - Events with step > terminal event's step (post-mortem event injection)
// messageProducingTypes is the set of event types that inject messages into
// the reconstructed conversation history. These types are forbidden at step 0
// because only run.started is valid at step 0; accepting them would let an
// attacker inject "prior context" into Fork(events, 0) history.
var messageProducingTypes = map[string]bool{
	"llm.turn.completed":     true,
	"tool.call.started":      true,
	"tool.call.completed":    true,
	"steering.received":      true,
	"conversation.continued": true,
}

func validateEvents(events []rollout.RolloutEvent) error {
	prev := -1
	terminalIndex := -1 // index of the first terminal event (run.completed/run.failed)
	runStartedCount := 0
	for i, ev := range events {
		// CRITICAL-1 (round 28): reject events after terminal by FILE INDEX, not by step.
		// Checking step > terminalStep allowed events at the SAME step as terminal
		// (e.g., run.completed at step 5 followed by llm.turn.completed at step 5),
		// which ReconstructMessages(events, 5) would include as injected content.
		if terminalIndex >= 0 {
			return fmt.Errorf("event[%d] (type=%q) appears after terminal event at index %d; no events are allowed after run.completed or run.failed",
				i, ev.Type, terminalIndex)
		}
		if ev.Step < 0 {
			return fmt.Errorf("event[%d] (type=%q) has negative step %d",
				i, ev.Type, ev.Step)
		}
		if ev.Step < prev {
			return fmt.Errorf("event[%d] (type=%q) step %d is less than previous step %d (non-monotonic; would be reordered by sortEvents, bypassing file-order integrity checks)",
				i, ev.Type, ev.Step, prev)
		}
		// CRITICAL-1 (round 28): enforce step-0 exclusivity. Only run.started is
		// valid at step 0. Message-producing types at step 0 allow attacker content
		// to appear in Fork(events, 0) history before the initial prompt.
		if ev.Step == 0 && messageProducingTypes[ev.Type] {
			return fmt.Errorf("event[%d] (type=%q) at step 0 is forbidden; only run.started may appear at step 0",
				i, ev.Type)
		}
		if ev.Type == "run.started" {
			runStartedCount++
			if runStartedCount > 1 {
				return fmt.Errorf("event[%d] is a duplicate run.started; only one run.started is allowed per rollout",
					i)
			}
			// run.started must be the first event at step 0.
			if i != 0 {
				return fmt.Errorf("event[%d] run.started must be the first event (index 0), not at index %d",
					i, i)
			}
			if ev.Step != 0 {
				return fmt.Errorf("event[%d] run.started must have step 0, got step %d",
					i, ev.Step)
			}
		}
		if ev.Type == "run.completed" || ev.Type == "run.failed" {
			terminalIndex = i
		}
		prev = ev.Step
	}
	// HIGH-4 fix (round 29): require run.started to be present. Without this
	// check, a crafted slice starting with llm.turn.completed at step 1 passes
	// validation and injects an assistant message into Fork() history with no
	// prior user/system context, violating message-ordering invariants.
	if len(events) > 0 && runStartedCount == 0 {
		return fmt.Errorf("no run.started event found; rollout must begin with run.started")
	}
	return nil
}

// ReplayEvent captures one step of an offline replay simulation.
type ReplayEvent struct {
	Step      int    `json:"step"`
	Type      string `json:"type"`
	EventType string `json:"event_type"`
	// Details holds event-specific information. Note: Type is always "replay"
	// while EventType holds the actual rollout event type.
	Details map[string]any `json:"details,omitempty"`
	// Matched is true when the replayed event matches the original.
	Matched bool `json:"matched"`
}

// ReplayResult is the outcome of an offline simulation replay.
type ReplayResult struct {
	Events     []ReplayEvent `json:"events"`
	StepCount  int           `json:"step_count"`
	Matched    bool          `json:"matched"`
	Mismatches []string      `json:"mismatches,omitempty"`
}

// announcedCallEntry stores the announced tool name and arguments for a
// call_id seen in llm.turn.completed.tool_calls. Used for cross-checking
// against tool.call.started (HIGH-1 name check, HIGH-2 arg check).
type announcedCallEntry struct {
	name string // tool name; "<empty>" sentinel if originally absent
	args string // arguments (capped); empty if not present
}

// Replay performs an offline simulation of a rollout. For each tool call
// event it returns the recorded output (no live execution). It verifies
// the event sequence by checking that tool call starts have corresponding
// completions with matching call IDs, tool names, and arguments.
func Replay(events []rollout.RolloutEvent) ReplayResult {
	var result ReplayResult
	result.Matched = true

	// HIGH-9 fix: validate event ordering before processing. Without this,
	// a pre-sorted or out-of-file-order slice bypasses the comp.fileIndex <= i
	// causal ordering checks (which assume events are in their original file order).
	if err := validateEvents(events); err != nil {
		mismatch(&result, nil, fmt.Sprintf("event sequence validation failed: %v", err))
		return result
	}

	idx := indexToolCompletions(events)
	for _, dup := range idx.duplicates {
		mismatch(&result, nil,
			fmt.Sprintf("duplicate tool.call.completed for call_id %q", sanitizeMismatch(dup)))
	}

	// announcedCallInfo maps capID(callID) → announced tool name and args (in
	// file order). Enforces causal ordering and cross-checks (HIGH-1, HIGH-2).
	announcedCallInfo := make(map[string]announcedCallEntry)
	startedCallIDs := make(map[string]bool)
	var callIDCapReached bool // sentinel emitted once when maxTotalCallIDs is reached

	maxStep := 0
	for i, ev := range events {
		if ev.Step > maxStep {
			maxStep = ev.Step
		}

		re := ReplayEvent{
			Step:      ev.Step,
			Type:      "replay",
			EventType: ev.Type,
			Matched:   true,
		}

		switch ev.Type {
		case "tool.call.started":
			callID, callIDOK := payloadString(ev.Payload, "call_id")
			toolName, _ := payloadString(ev.Payload, "tool")
			// HIGH-3 fix: use payloadStringOrJSON so that object-typed arguments
			// (map[string]any from a JSON-decoded payload) are marshaled to a
			// string for comparison rather than silently producing args=="",
			// which would bypass the announced-vs-started arg cross-check.
			args, _ := payloadStringOrJSON(ev.Payload, "arguments")

			re.Details = map[string]any{
				"tool":      capString(toolName, maxDetailStringBytes),
				"call_id":   capString(callID, maxDetailStringBytes),
				"arguments": capString(args, maxDetailStringBytes),
			}

			safeCallID := sanitizeMismatch(callID)
			safeToolName := sanitizeMismatch(toolName)

			if !callIDOK || callID == "" {
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call (%q) has missing or non-string call_id",
					ev.Step, safeToolName))
			} else if len(callID) > maxIDBytes {
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call (%q) call_id exceeds maximum length %d",
					ev.Step, safeToolName, maxIDBytes))
			} else if toolName == "" {
				// CRITICAL-1 fix: empty tool name bypasses both the announced-name
				// cross-check and the started-vs-completed name-consistency check.
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call %q has missing or empty tool name in tool.call.started",
					ev.Step, safeCallID))
			} else if announced, ok := announcedCallInfo[capID(callID)]; !ok {
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call %q (%q) was never announced in llm.turn.completed.tool_calls",
					ev.Step, safeCallID, safeToolName))
			} else if announced.name != "" && announced.name != toolName {
				// HIGH-1/HIGH-2 fix: cross-check announced vs started tool name.
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call %q: announced tool %q does not match started tool %q",
					ev.Step, safeCallID, sanitizeMismatch(announced.name), safeToolName))
			} else if announced.args != "" &&
				capString(args, maxToolArgMarshalBytes) != announced.args {
				// HIGH-2 fix: cross-check announced vs started arguments. This catches
				// argument splicing: announcing {"path":"safe"} while actually executing
				// {"path":"/etc/shadow"}. Note: if args were stored as a JSON map at
				// announcement time, key ordering may differ from the started string —
				// this check is effective for string-valued args (the common format).
				//
				// HIGH-5 fix (round 33): removed the `args != ""` guard. The previous
				// condition `announced.args != "" && args != ""` allowed an attacker to
				// announce non-empty arguments then provide an empty args field in
				// tool.call.started to bypass the cross-check entirely. Now: if
				// announced.args is non-empty, the started args must match exactly
				// (an empty started-args is a mismatch against non-empty announced-args).
				// If announced.args is empty, the check is still skipped (nothing was
				// announced to compare against).
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool call %q (%q): announced arguments differ from started arguments",
					ev.Step, safeCallID, safeToolName))
			} else if startedCallIDs[capID(callID)] {
				// MEDIUM-7 fix: detect duplicate starts.
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: duplicate tool.call.started for call_id %q",
					ev.Step, safeCallID))
			} else {
				startedCallIDs[capID(callID)] = true
				if comp, ok := idx.entries[capID(callID)]; ok {
					if comp.fileIndex <= i {
						mismatch(&result, &re, fmt.Sprintf(
							"step %d: tool call %q (%q) completion appears before started event in file order",
							ev.Step, safeCallID, safeToolName))
					} else if toolName != "" && comp.toolName != toolName {
						mismatch(&result, &re, fmt.Sprintf(
							"step %d: tool call %q: name mismatch between started (%q) and completed (%q)",
							ev.Step, safeCallID, safeToolName, sanitizeMismatch(comp.toolName)))
					} else {
						re.Details["result"] = comp.result
					}
				} else {
					mismatch(&result, &re, fmt.Sprintf(
						"step %d: tool call %q (%q) has no recorded completion",
						ev.Step, safeCallID, safeToolName))
				}
			}

		case "tool.call.completed":
			callID, callIDOK := payloadString(ev.Payload, "call_id")
			// The runner emits tool outputs under "output" (runner_step_engine.go:847,874,1072,1091).
			// Fall back to "output" when "result" is absent or empty so that real captured
			// rollouts are not silently truncated. Existing rollouts that use "result" are unaffected.
			toolResult, _ := payloadString(ev.Payload, "result")
			if toolResult == "" {
				toolResult, _ = payloadString(ev.Payload, "output")
			}
			toolName, _ := payloadString(ev.Payload, "tool")
			re.Details = map[string]any{
				"call_id": capString(callID, maxDetailStringBytes),
				"result":  capString(toolResult, maxDetailStringBytes),
				"tool":    capString(toolName, maxDetailStringBytes),
			}
			if !callIDOK || callID == "" {
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool.call.completed has missing or non-string call_id", ev.Step))
			} else if len(callID) > maxIDBytes {
				// HIGH-1 fix: flag oversized completion call_ids.
				mismatch(&result, &re, fmt.Sprintf(
					"step %d: tool.call.completed call_id exceeds maximum length %d", ev.Step, maxIDBytes))
			}

		case "llm.turn.completed":
			content, _ := payloadString(ev.Payload, "content")
			re.Details = map[string]any{"content": capString(content, maxDetailStringBytes)}
			for _, tc := range extractToolCalls(ev.Payload) {
				if tc.ID == "" || len(tc.ID) > maxIDBytes {
					continue
				}
				key := capID(tc.ID)
				if _, already := announcedCallInfo[key]; already {
					// MEDIUM-2 fix: re-announcement of the same call_id overwrites the
					// first announcement (e.g., with an empty name to weaken checks).
					mismatch(&result, &re, fmt.Sprintf(
						"step %d: duplicate announcement of call_id %q in llm.turn.completed",
						ev.Step, sanitizeMismatch(tc.ID)))
					continue // keep first announcement, flag as integrity failure
				}
				if len(announcedCallInfo) < maxTotalCallIDs {
					name := tc.Name
					if name == "" {
						// HIGH-1 fix: empty announced tool name bypasses the announced-
						// vs-started cross-check (which guards `announcedName != ""`).
						// Using a sentinel instead of empty triggers the mismatch when
						// the started event declares any non-empty tool name.
						name = "<empty>"
					}
					announcedCallInfo[key] = announcedCallEntry{name: name, args: tc.Arguments}
				} else if !callIDCapReached {
					// HIGH-4 fix: emit one sentinel when the cap is first reached.
					callIDCapReached = true
					mismatch(&result, nil, fmt.Sprintf(
						"call_id tracking limit (%d) reached — subsequent tool call announcements will not be validated; analysis may be incomplete",
						maxTotalCallIDs))
				}
			}

		case "run.started", "run.completed", "run.failed":
			re.Details = copyPayloadCapped(ev.Payload)

		default:
			re.Details = copyPayloadCapped(ev.Payload)
		}

		result.Events = append(result.Events, re)
	}

	// Post-loop: flag completions without a corresponding started.
	for key, entry := range idx.entries {
		if !startedCallIDs[key] {
			mismatch(&result, nil, fmt.Sprintf(
				"tool.call.completed for call_id %q has no corresponding tool.call.started",
				entry.rawID))
		}
	}

	result.StepCount = maxStep
	return result
}

// cappedWriter is an io.Writer that writes at most cap bytes.
type cappedWriter struct {
	buf []byte
	cap int
}

func (cw *cappedWriter) Write(p []byte) (int, error) {
	remaining := cw.cap - len(cw.buf)
	if remaining <= 0 {
		return 0, errCapExceeded
	}
	if len(p) > remaining {
		cw.buf = append(cw.buf, p[:remaining]...)
		return remaining, errCapExceeded
	}
	cw.buf = append(cw.buf, p...)
	return len(p), nil
}

// cappedMarshal encodes v to JSON allocating at most capSize bytes.
func cappedMarshal(v any, capSize int) []byte {
	cw := &cappedWriter{cap: capSize}
	enc := json.NewEncoder(cw)
	if err := enc.Encode(v); err != nil && !errors.Is(err, errCapExceeded) {
		return nil
	}
	b := cw.buf
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

// completionEntry holds result, file-order index, tool name, and sanitized
// original call ID of a tool.call.completed event.
type completionEntry struct {
	result    string
	fileIndex int
	toolName  string
	rawID     string // sanitized original call_id for human-readable reporting
}

// completionIndex is the result of indexing tool completions from a rollout.
type completionIndex struct {
	entries    map[string]completionEntry
	duplicates []string
}

// indexToolCompletions builds a map from capID(call_id) to completionEntry.
func indexToolCompletions(events []rollout.RolloutEvent) completionIndex {
	m := make(map[string]completionEntry)
	seen := make(map[string]bool)
	var duplicates []string

	for i, ev := range events {
		if ev.Type != "tool.call.completed" || ev.Payload == nil {
			continue
		}
		callID, callIDOK := payloadString(ev.Payload, "call_id")
		if !callIDOK || callID == "" || len(callID) > maxIDBytes {
			continue
		}
		key := capID(callID)
		if seen[key] {
			duplicates = append(duplicates, callID)
			continue
		}
		seen[key] = true
		const maxResultMarshalBytes = 65536
		// Read "result" first; fall back to "output" when absent or empty.
		// The runner emits tool outputs under "output" (runner_step_engine.go:847,874,1072,1091)
		// so real captured rollouts carry "output", not "result". Existing rollouts with "result"
		// continue to work unchanged.
		result, ok := payloadString(ev.Payload, "result")
		if ok {
			result = capString(result, maxDetailStringBytes)
		} else {
			if raw, exists := ev.Payload["result"]; exists {
				if b := cappedMarshal(raw, maxResultMarshalBytes); b != nil {
					if len(b) >= maxResultMarshalBytes {
						b = append(b, []byte("...<truncated>")...)
					}
					result = string(b)
				}
			}
		}
		if result == "" {
			// Fallback: try "output" key (runner emit key).
			if out, outOK := payloadString(ev.Payload, "output"); outOK && out != "" {
				result = capString(out, maxDetailStringBytes)
			} else if raw, exists := ev.Payload["output"]; exists && result == "" {
				if b := cappedMarshal(raw, maxResultMarshalBytes); b != nil {
					if len(b) >= maxResultMarshalBytes {
						b = append(b, []byte("...<truncated>")...)
					}
					result = string(b)
				}
			}
		}
		compToolName, _ := payloadString(ev.Payload, "tool")
		m[key] = completionEntry{
			result:    result,
			fileIndex: i,
			toolName:  compToolName,
			rawID:     sanitizeMismatch(callID),
		}
	}
	return completionIndex{entries: m, duplicates: duplicates}
}

// sortEvents returns a copy of events sorted by (Step, file-order index).
// File order is used as the tie-breaker to preserve causal ordering within
// a step independent of attacker-controlled seq values.
//
// NOTE: callers that receive events from untrusted sources (not rollout.LoadReader)
// MUST call validateEvents first. sortEvents can launder a non-monotonic input
// into an apparently-valid causal order (a non-monotonic slice becomes monotonic
// after sorting), bypassing file-order integrity checks.
func sortEvents(events []rollout.RolloutEvent) []rollout.RolloutEvent {
	type indexed struct {
		ev  rollout.RolloutEvent
		idx int
	}
	tmp := make([]indexed, len(events))
	for i, ev := range events {
		tmp[i] = indexed{ev: ev, idx: i}
	}
	sort.SliceStable(tmp, func(i, j int) bool {
		if tmp[i].ev.Step != tmp[j].ev.Step {
			return tmp[i].ev.Step < tmp[j].ev.Step
		}
		return tmp[i].idx < tmp[j].idx
	})
	sorted := make([]rollout.RolloutEvent, len(events))
	for i, ie := range tmp {
		sorted[i] = ie.ev
	}
	return sorted
}

// ReconstructMessages rebuilds the []harness.Message conversation history
// from rollout events up to and including the given step.
// Events are sorted by (step, file-order index) before reconstruction.
//
// Causal validation: only tool.call.completed events whose call_id was
// previously announced in an llm.turn.completed.tool_calls list AND whose
// call_id was seen in a tool.call.started event are included.
//
// HIGH-5 fix: validateEvents is called before sortEvents to prevent sort
// laundering — non-monotonic input to sortEvents produces an apparently-valid
// causal order that bypasses file-order integrity checks. Returns nil on
// validation failure.
func ReconstructMessages(events []rollout.RolloutEvent, upToStep int) []harness.Message {
	if err := validateEvents(events); err != nil {
		return nil
	}

	var messages []harness.Message
	announcedCalls := make(map[string]bool)
	startedCalls := make(map[string]bool)

	for _, ev := range sortEvents(events) {
		// Defensive guard: skip negative-step events to prevent backdating
		// injection if this is called with non-loader-validated events.
		if ev.Step < 0 || ev.Step > upToStep {
			continue
		}

		switch ev.Type {
		case "run.started":
			prompt, _ := payloadString(ev.Payload, "prompt")
			systemPrompt, _ := payloadString(ev.Payload, "system_prompt")
			if systemPrompt != "" {
				messages = append(messages, harness.Message{
					Role:    "system",
					Content: capString(systemPrompt, maxDetailStringBytes),
				})
			}
			if prompt != "" {
				messages = append(messages, harness.Message{
					Role:    "user",
					Content: capString(prompt, maxDetailStringBytes),
				})
			}

		case "llm.turn.completed":
			content, _ := payloadString(ev.Payload, "content")
			msg := harness.Message{
				Role:    "assistant",
				Content: capString(content, maxDetailStringBytes),
			}

			if tcs := extractToolCalls(ev.Payload); len(tcs) > 0 {
				msg.ToolCalls = tcs
				for _, tc := range tcs {
					if tc.ID != "" && len(tc.ID) <= maxIDBytes {
						if len(announcedCalls) < maxTotalCallIDs {
							announcedCalls[capID(tc.ID)] = true
						}
					}
				}
			}

			messages = append(messages, msg)

		case "tool.call.started":
			if callID, ok := payloadString(ev.Payload, "call_id"); ok && callID != "" {
				if len(callID) <= maxIDBytes && announcedCalls[capID(callID)] {
					if len(startedCalls) < maxTotalCallIDs {
						startedCalls[capID(callID)] = true
					}
				}
			}

		case "tool.call.completed":
			callID, _ := payloadString(ev.Payload, "call_id")
			// The runner emits tool outputs under "output" (runner_step_engine.go:847,874,1072,1091).
			// Fall back to "output" when "result" is absent or empty.
			toolResult, _ := payloadString(ev.Payload, "result")
			if toolResult == "" {
				toolResult, _ = payloadString(ev.Payload, "output")
			}
			toolName, _ := payloadString(ev.Payload, "tool")
			if callID != "" && len(callID) <= maxIDBytes &&
				announcedCalls[capID(callID)] && startedCalls[capID(callID)] {
				delete(announcedCalls, capID(callID))
				// HIGH-2 fix: clear startedCalls on completion to prevent reuse.
				delete(startedCalls, capID(callID))
				messages = append(messages, harness.Message{
					Role:       "tool",
					Content:    capString(toolResult, maxDetailStringBytes),
					ToolCallID: capString(callID, maxDetailStringBytes),
					Name:       capString(toolName, maxDetailStringBytes),
				})
			}

		case "steering.received":
			content, _ := payloadString(ev.Payload, "content")
			if content != "" {
				messages = append(messages, harness.Message{
					Role:    "user",
					Content: capString(content, maxDetailStringBytes),
				})
			}

		case "conversation.continued":
			message, _ := payloadString(ev.Payload, "message")
			if message != "" {
				messages = append(messages, harness.Message{
					Role:    "user",
					Content: capString(message, maxDetailStringBytes),
				})
			}
		}
	}

	return messages
}

// maxToolCallsPerTurn caps how many tool calls are extracted per turn.
const maxToolCallsPerTurn = 100

// maxToolArgMarshalBytes caps marshaled tool argument size.
const maxToolArgMarshalBytes = 65536 // 64 KiB

// extractToolCalls extracts tool call objects from an llm.turn.completed payload.
func extractToolCalls(payload map[string]any) []harness.ToolCall {
	raw, ok := payload["tool_calls"]
	if !ok {
		return nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	var calls []harness.ToolCall
	for _, item := range arr {
		if len(calls) >= maxToolCallsPerTurn {
			break
		}
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tc := harness.ToolCall{}
		if id, ok := obj["id"].(string); ok {
			tc.ID = capString(id, maxDetailStringBytes)
		}
		if name, ok := obj["name"].(string); ok {
			tc.Name = capString(name, maxDetailStringBytes)
		}
		if args, ok := obj["arguments"].(string); ok {
			tc.Arguments = capString(args, maxToolArgMarshalBytes)
		} else if args, ok := obj["arguments"].(map[string]any); ok {
			if b := cappedMarshal(args, maxToolArgMarshalBytes); b != nil {
				if len(b) >= maxToolArgMarshalBytes {
					b = append(b, []byte("...<truncated>")...)
				}
				tc.Arguments = string(b)
			}
		}
		calls = append(calls, tc)
	}
	return calls
}

// payloadString extracts a string value from a payload map.
func payloadString(payload map[string]any, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	v, ok := payload[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// payloadStringOrJSON extracts key from payload as a string. If the value is
// not a string (e.g., a JSON-decoded map from a parsed rollout), it is marshaled
// to compact JSON so the result can be compared against string-typed fields.
// This prevents the HIGH-3 argument-type confusion bypass: an attacker can pass
// arguments as an object in tool.call.started to make payloadString return "",
// which causes the arg cross-check to be silently skipped.
func payloadStringOrJSON(payload map[string]any, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	v, ok := payload[key]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	// Non-string value (e.g. map[string]any from a decoded JSON object):
	// marshal to compact JSON for comparison. Go's json.Marshal sorts map keys
	// lexicographically, making the output deterministic for the same content.
	//
	// HIGH-7 fix (round 30): use cappedMarshal instead of json.Marshal to
	// bound the allocation. A 100 MB arguments map causes json.Marshal to
	// allocate 100 MB before cappedMarshal (or any downstream capString) could
	// truncate it. cappedMarshal stops writing at maxToolArgMarshalBytes.
	b := cappedMarshal(v, maxToolArgMarshalBytes)
	if b == nil {
		return "", false
	}
	return string(b), true
}

// copyPayload makes a shallow copy of a payload map.
func copyPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	return out
}

// copyPayloadCapped deep-caps all string values in a payload map at
// maxDetailStringBytes, with depth and element-count budgets.
// CRITICAL-2 fix: earlier shallow cap left nested structures verbatim.
// HIGH-3 fix: depth and element budgets bound CPU/memory for large payloads.
func copyPayloadCapped(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = deepCapStrings(v)
	}
	return out
}
