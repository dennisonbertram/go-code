package training

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// truncationThreshold is the token count above which middle messages are dropped.
const truncationThreshold = 180000

// jsonlEntry is the on-disk format of a rollout JSONL record.
type jsonlEntry struct {
	Ts   time.Time      `json:"ts"`
	Seq  uint64         `json:"seq"`
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

// ExportFromJSONL reads a rollout JSONL file and produces a TraceBundle.
func ExportFromJSONL(path string) (*TraceBundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rollout file: %w", err)
	}
	defer f.Close()

	var entries []jsonlEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e jsonlEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan rollout file: %w", err)
	}

	// Sort by sequence number for deterministic processing.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Seq < entries[j].Seq
	})

	bundle := &TraceBundle{
		Outcome: "unknown",
	}

	// Track tool calls by call_id for matching calls to results
	type pendingCall struct {
		name    string
		args    map[string]any
		stepIdx int
	}
	pendingCalls := make(map[string]*pendingCall)

	// Track seen tool call signatures for retry detection
	type callSig struct {
		name string
		args string
	}
	seenCalls := make(map[callSig]int) // signature -> count

	for _, e := range entries {
		switch e.Type {
		case "run.started":
			// The recorder uses "conversation_id" as the run identifier, not "run_id".
			if v, ok := e.Data["conversation_id"].(string); ok {
				bundle.RunID = v
			}
			if v, ok := e.Data["prompt"].(string); ok {
				bundle.Messages = append(bundle.Messages, Message{
					Role:    "user",
					Content: v,
				})
			}
			if v, ok := e.Data["system_prompt"].(string); ok {
				bundle.SystemPrompt = v
			}
			if v, ok := e.Data["task_id"].(string); ok {
				bundle.TaskID = v
			}

		case "tool.call.started":
			// Real event: "tool.call.started" with fields "tool" (name), "call_id",
			// "arguments" (JSON string), "step".
			name, _ := e.Data["tool"].(string)
			callID, _ := e.Data["call_id"].(string)
			step := intFromData(e.Data, "step")
			// Arguments are a JSON-encoded string, not a pre-decoded map.
			var args map[string]any
			if argStr, ok := e.Data["arguments"].(string); ok && argStr != "" {
				_ = json.Unmarshal([]byte(argStr), &args)
			}
			pendingCalls[callID] = &pendingCall{name: name, args: args, stepIdx: step}

		case "tool.call.completed":
			// Real event: "tool.call.completed" with fields "call_id", "tool",
			// "output" (string), "error" (string, omitted on success), "step".
			callID, _ := e.Data["call_id"].(string)
			name, _ := e.Data["tool"].(string)
			output, _ := e.Data["output"].(string)
			// Success is indicated by the absence of a non-empty "error" field.
			errStr, hasError := e.Data["error"].(string)
			success := !hasError || errStr == ""
			step := intFromData(e.Data, "step")

			tc := ToolCallTrace{
				Name:    name,
				Output:  output,
				Success: success,
				StepIdx: step,
			}

			// Get args from pending call.
			if pc, ok := pendingCalls[callID]; ok {
				tc.Args = pc.args
				tc.StepIdx = pc.stepIdx
				delete(pendingCalls, callID)
			}

			// Check for retry.
			argsJSON, _ := json.Marshal(tc.Args)
			sig := callSig{name: tc.Name, args: string(argsJSON)}
			seenCalls[sig]++
			if seenCalls[sig] > 1 {
				tc.Retried = true
			}

			bundle.ToolCalls = append(bundle.ToolCalls, tc)

			// Add as message.
			bundle.Messages = append(bundle.Messages, Message{
				Role:       "tool",
				Content:    output,
				ToolName:   name,
				ToolCallID: callID,
			})

		case "usage.delta":
			// Real event: "usage.delta" carries per-turn and cumulative token/cost data.
			// Track cumulative values; the last usage.delta gives the final totals.
			if v, ok := e.Data["cumulative_cost_usd"].(float64); ok {
				bundle.CostUSD = v
			}
			if cumUsage, ok := e.Data["cumulative_usage"].(map[string]any); ok {
				if total, ok := cumUsage["total_tokens"].(float64); ok {
					bundle.TokenCount = int(total)
				}
			}

		case "assistant.message":
			// Fully assembled assistant message (non-delta).
			if content, ok := e.Data["content"].(string); ok && content != "" {
				bundle.Messages = append(bundle.Messages, Message{
					Role:    "assistant",
					Content: content,
				})
			}

		case "context.window.snapshot":
			snap := ContextSnapshot{
				StepIdx:     intFromData(e.Data, "step"),
				TotalTokens: intFromData(e.Data, "max_context_tokens"),
				UsedTokens:  intFromData(e.Data, "estimated_total_tokens"),
			}
			if r, ok := e.Data["usage_ratio"].(float64); ok {
				snap.Ratio = r
			}
			bundle.ContextSnapshots = append(bundle.ContextSnapshots, snap)
			if snap.Ratio > bundle.MaxContextRatio {
				bundle.MaxContextRatio = snap.Ratio
			}

		case "tool.antipattern":
			ap := AntiPatternAlert{
				StepIdx: intFromData(e.Data, "step"),
			}
			if v, ok := e.Data["type"].(string); ok {
				ap.Type = v
			}
			if v, ok := e.Data["tool"].(string); ok {
				ap.Message = fmt.Sprintf("%s: %s", ap.Type, v)
			}
			if v, ok := e.Data["evidence"].(string); ok {
				ap.Evidence = v
			}
			bundle.AntiPatterns = append(bundle.AntiPatterns, ap)

		case "run.completed":
			bundle.Outcome = "pass"
			// "step" in run.completed is the last step number = total steps taken.
			if v := intFromData(e.Data, "step"); v > 0 {
				bundle.Steps = v
			}
			// Override with authoritative totals from cost_totals / usage_totals.
			if costTotals, ok := e.Data["cost_totals"].(map[string]any); ok {
				if v, ok := costTotals["cost_usd_total"].(float64); ok && v > 0 {
					bundle.CostUSD = v
				}
			}
			if usageTotals, ok := e.Data["usage_totals"].(map[string]any); ok {
				if v, ok := usageTotals["total_tokens"].(float64); ok && int(v) > bundle.TokenCount {
					bundle.TokenCount = int(v)
				}
			}

		case "run.failed":
			bundle.Outcome = "fail"
			// "step" in run.failed is the last step number = total steps taken.
			if v := intFromData(e.Data, "step"); v > 0 {
				bundle.Steps = v
			}
			if costTotals, ok := e.Data["cost_totals"].(map[string]any); ok {
				if v, ok := costTotals["cost_usd_total"].(float64); ok && v > 0 {
					bundle.CostUSD = v
				}
			}
			if usageTotals, ok := e.Data["usage_totals"].(map[string]any); ok {
				if v, ok := usageTotals["total_tokens"].(float64); ok && int(v) > bundle.TokenCount {
					bundle.TokenCount = int(v)
				}
			}
		}
	}

	// Compute derived metrics.
	computeFirstTryRate(bundle)
	computeEfficiencyScore(bundle)
	applyTruncation(bundle)

	return bundle, nil
}

// computeFirstTryRate calculates the fraction of non-retried tool calls.
func computeFirstTryRate(b *TraceBundle) {
	if len(b.ToolCalls) == 0 {
		b.FirstTryRate = 0
		return
	}
	nonRetried := 0
	for _, tc := range b.ToolCalls {
		if !tc.Retried {
			nonRetried++
		}
	}
	b.FirstTryRate = float64(nonRetried) / float64(len(b.ToolCalls))
}

// computeEfficiencyScore = 1.0 / (steps * cost) normalized to [0,1].
func computeEfficiencyScore(b *TraceBundle) {
	steps := b.Steps
	if steps <= 0 {
		steps = 1
	}
	cost := b.CostUSD
	if cost <= 0 {
		cost = 0.001
	}
	raw := 1.0 / (float64(steps) * cost)
	// Normalize: cap at 1.0
	if raw > 1.0 {
		raw = 1.0
	}
	b.EfficiencyScore = raw
}

// applyTruncation drops middle messages if token count exceeds threshold.
func applyTruncation(b *TraceBundle) {
	if b.TokenCount <= truncationThreshold {
		return
	}
	b.Truncated = true
	b.TruncationStrategy = "middle_drop"

	msgCount := len(b.Messages)
	if msgCount <= 5 {
		return // too few messages to truncate
	}

	// Keep first 20% and last 30%
	keepFirst := int(float64(msgCount) * 0.20)
	keepLast := int(float64(msgCount) * 0.30)
	if keepFirst < 1 {
		keepFirst = 1
	}
	if keepLast < 1 {
		keepLast = 1
	}
	if keepFirst+keepLast >= msgCount {
		return
	}

	truncated := make([]Message, 0, keepFirst+keepLast+1)
	truncated = append(truncated, b.Messages[:keepFirst]...)
	truncated = append(truncated, Message{
		Role:    "system",
		Content: fmt.Sprintf("[%d messages truncated]", msgCount-keepFirst-keepLast),
	})
	truncated = append(truncated, b.Messages[msgCount-keepLast:]...)

	b.TruncatedTokens = b.TokenCount // original count before truncation
	b.Messages = truncated
}

// intFromData extracts an int from a map[string]any, handling float64 JSON decoding.
func intFromData(data map[string]any, key string) int {
	v, ok := data[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
