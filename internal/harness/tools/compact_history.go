package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func compactHistoryTool(summarizer MessageSummarizer) Tool {
	return Tool{
		Definition: Definition{
			Name:        "compact_history",
			Description: descriptions.Load("compact_history"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"type":        "string",
						"enum":        []string{"strip", "summarize", "hybrid"},
						"description": "Compaction strategy: strip (remove tool messages), summarize (LLM summary), or hybrid (strip metadata + summarize large outputs)",
					},
					"keep_last": map[string]any{
						"type":        "integer",
						"description": "Number of recent turns to preserve intact (default 4, minimum 1)",
					},
				},
				"required":             []string{"mode"},
				"additionalProperties": false,
			},
			Action:       ActionExecute,
			Mutating:     true,
			ParallelSafe: false,
			Tags:         []string{"context", "compact", "memory", "optimization"},
			Tier:         TierCore,
		},
		Handler: handleCompactHistory(summarizer),
	}
}

type compactHistoryArgs struct {
	Mode     string `json:"mode"`
	KeepLast int    `json:"keep_last"`
}

func handleCompactHistory(summarizer MessageSummarizer) Handler {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a compactHistoryArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return MarshalToolResult(map[string]any{"error": "invalid arguments: " + err.Error()})
		}

		if a.Mode != "strip" && a.Mode != "summarize" && a.Mode != "hybrid" {
			return MarshalToolResult(map[string]any{"error": "mode must be one of: strip, summarize, hybrid"})
		}
		if a.KeepLast <= 0 {
			a.KeepLast = 4
		}

		// Read current messages
		reader, ok := TranscriptReaderFromContext(ctx)
		if !ok {
			return MarshalToolResult(map[string]any{"error": "transcript reader not available"})
		}
		replacer, ok := MessageReplacerFromContext(ctx)
		if !ok {
			return MarshalToolResult(map[string]any{"error": "message replacer not available (compact_history requires runner support)"})
		}

		snap := reader.Snapshot(0, true)
		if len(snap.Messages) == 0 {
			return MarshalToolResult(map[string]any{
				"before_tokens":   0,
				"after_tokens":    0,
				"turns_compacted": 0,
			})
		}

		beforeTokens := estimateTranscriptTokens(snap.Messages)

		// Parse into turns
		turns := parseTurns(snap.Messages)

		// Find prefix (system/compact_summary turns) and determine compaction zone
		prefixEnd, compactEnd := findCompactionBounds(turns, a.KeepLast)

		if compactEnd <= prefixEnd {
			// Nothing to compact
			return MarshalToolResult(map[string]any{
				"before_tokens":   beforeTokens,
				"after_tokens":    beforeTokens,
				"turns_compacted": 0,
			})
		}

		var (
			compactedMsgs []TranscriptMessage
			summary       string
			err           error
		)

		switch a.Mode {
		case "strip":
			compactedMsgs = compactStrip(turns, prefixEnd, compactEnd)
		case "summarize":
			if summarizer == nil {
				return MarshalToolResult(map[string]any{"error": "summarize mode requires a message summarizer (not configured)"})
			}
			compactedMsgs, summary, err = compactSummarize(ctx, turns, prefixEnd, compactEnd, summarizer)
			if err != nil {
				return MarshalToolResult(map[string]any{"error": "summarization failed: " + err.Error()})
			}
		case "hybrid":
			compactedMsgs, summary, err = compactHybrid(ctx, turns, prefixEnd, compactEnd, summarizer)
			if err != nil {
				return MarshalToolResult(map[string]any{"error": "hybrid compaction failed: " + err.Error()})
			}
		}

		afterTokens := estimateTranscriptTokens(compactedMsgs)
		turnsCompacted := compactEnd - prefixEnd

		// Convert to maps and call replacer
		msgMaps := transcriptMsgsToMaps(compactedMsgs)
		replacer(msgMaps)

		result := map[string]any{
			"before_tokens":   beforeTokens,
			"after_tokens":    afterTokens,
			"turns_compacted": turnsCompacted,
		}
		if summary != "" {
			result["summary"] = summary
		}

		return MarshalToolResult(result)
	}
}

// turn represents a logical turn in the conversation.
type turn struct {
	Messages []TranscriptMessage
	Kind     string // "system_prefix", "user", "assistant_text", "assistant_tool", "compact_summary"
}

// parseTurns groups a flat message array into logical turns.
func parseTurns(msgs []TranscriptMessage) []turn {
	if len(msgs) == 0 {
		return nil
	}

	var turns []turn
	i := 0

	// Collect leading system messages as prefix
	for i < len(msgs) && msgs[i].Role == "system" {
		kind := "system_prefix"
		if msgs[i].Name == "compact_summary" {
			kind = "compact_summary"
		}
		turns = append(turns, turn{
			Messages: []TranscriptMessage{msgs[i]},
			Kind:     kind,
		})
		i++
	}

	for i < len(msgs) {
		msg := msgs[i]

		switch msg.Role {
		case "user":
			turns = append(turns, turn{
				Messages: []TranscriptMessage{msg},
				Kind:     "user",
			})
			i++

		case "assistant":
			t := turn{
				Messages: []TranscriptMessage{msg},
				Kind:     "assistant_text",
			}
			i++

			// Collect following tool results and system meta-messages
			hasToolResults := false
			for i < len(msgs) && (msgs[i].Role == "tool" || (msgs[i].Role == "system" && i > 0)) {
				if msgs[i].Role == "tool" {
					hasToolResults = true
					t.Messages = append(t.Messages, msgs[i])
					i++
				} else if msgs[i].Role == "system" {
					// System message after tool results (meta-message)
					t.Messages = append(t.Messages, msgs[i])
					i++
				} else {
					break
				}
			}
			if hasToolResults {
				t.Kind = "assistant_tool"
			}
			turns = append(turns, t)

		case "system":
			// System message in the middle of conversation
			kind := "system_prefix"
			if msg.Name == "compact_summary" {
				kind = "compact_summary"
			}
			turns = append(turns, turn{
				Messages: []TranscriptMessage{msg},
				Kind:     kind,
			})
			i++

		case "tool":
			// Orphan tool result (shouldn't happen but handle gracefully)
			turns = append(turns, turn{
				Messages: []TranscriptMessage{msg},
				Kind:     "assistant_tool",
			})
			i++

		default:
			// Unknown role, treat as its own turn
			turns = append(turns, turn{
				Messages: []TranscriptMessage{msg},
				Kind:     "user",
			})
			i++
		}
	}

	return turns
}

// findCompactionBounds returns the indices [prefixEnd, compactEnd) of turns to compact.
// Prefix turns (system_prefix, compact_summary) at the start are preserved.
// The last keepLast non-prefix turns are preserved.
func findCompactionBounds(turns []turn, keepLast int) (prefixEnd, compactEnd int) {
	// Find prefix end
	prefixEnd = 0
	for prefixEnd < len(turns) {
		if turns[prefixEnd].Kind != "system_prefix" && turns[prefixEnd].Kind != "compact_summary" {
			break
		}
		prefixEnd++
	}

	// Non-prefix turns
	nonPrefixCount := len(turns) - prefixEnd
	if nonPrefixCount <= keepLast {
		// Nothing to compact
		return prefixEnd, prefixEnd
	}

	compactEnd = len(turns) - keepLast
	return prefixEnd, compactEnd
}

// compactStrip removes tool messages from the compaction zone.
func compactStrip(turns []turn, prefixEnd, compactEnd int) []TranscriptMessage {
	var result []TranscriptMessage

	// Keep prefix
	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	// Process compaction zone
	stripped := 0
	for i := prefixEnd; i < compactEnd; i++ {
		t := turns[i]
		switch t.Kind {
		case "assistant_tool":
			// Keep the assistant message only if it has text content
			if len(t.Messages) > 0 && strings.TrimSpace(t.Messages[0].Content) != "" {
				result = append(result, TranscriptMessage{
					Index:   t.Messages[0].Index,
					Role:    "assistant",
					Content: t.Messages[0].Content,
				})
			}
			// Count stripped tool results
			for _, m := range t.Messages {
				if m.Role == "tool" {
					stripped++
				}
			}
		case "user", "assistant_text":
			result = append(result, t.Messages...)
		default:
			// Keep system messages, compact summaries, etc.
			result = append(result, t.Messages...)
		}
	}

	// Insert synthetic marker if anything was stripped
	if stripped > 0 {
		result = append(result, TranscriptMessage{
			Role:    "system",
			Name:    "compact_summary",
			Content: fmt.Sprintf("[context compacted: %d tool interactions stripped]", stripped),
		})
	}

	// Keep last turns
	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result
}

// compactSummarize replaces the compaction zone with an LLM-generated summary.
func compactSummarize(ctx context.Context, turns []turn, prefixEnd, compactEnd int, summarizer MessageSummarizer) ([]TranscriptMessage, string, error) {
	var result []TranscriptMessage

	// Keep prefix
	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	// Collect compaction zone messages for summarization
	var zoneMsgs []map[string]any
	for i := prefixEnd; i < compactEnd; i++ {
		for _, m := range turns[i].Messages {
			zoneMsgs = append(zoneMsgs, map[string]any{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}

	summary, err := summarizer.SummarizeMessages(ctx, zoneMsgs)
	if err != nil {
		return nil, "", err
	}

	// Insert summary as compact summary message
	result = append(result, TranscriptMessage{
		Role:    "system",
		Name:    "compact_summary",
		Content: summary,
	})

	// Keep last turns
	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result, summary, nil
}

// compactHybrid strips tool call metadata and summarizes large tool results.
func compactHybrid(ctx context.Context, turns []turn, prefixEnd, compactEnd int, summarizer MessageSummarizer) ([]TranscriptMessage, string, error) {
	var result []TranscriptMessage

	// Keep prefix
	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	// Process compaction zone
	const largeTokenThreshold = 500
	var removedContent []string
	stripped := 0

	for i := prefixEnd; i < compactEnd; i++ {
		t := turns[i]
		switch t.Kind {
		case "assistant_tool":
			// Keep assistant text if present
			if len(t.Messages) > 0 && strings.TrimSpace(t.Messages[0].Content) != "" {
				result = append(result, TranscriptMessage{
					Index:   t.Messages[0].Index,
					Role:    "assistant",
					Content: t.Messages[0].Content,
				})
			}
			// Process tool results
			for _, m := range t.Messages {
				if m.Role != "tool" {
					continue
				}
				tokens := estimateTextTokens(m.Content)
				if tokens > largeTokenThreshold {
					removedContent = append(removedContent, m.Content)
					stripped++
				} else {
					// Keep small tool results
					result = append(result, m)
				}
			}
		case "user", "assistant_text":
			result = append(result, t.Messages...)
		default:
			result = append(result, t.Messages...)
		}
	}

	// Generate summary of removed content if we have a summarizer and removed content
	var summary string
	if len(removedContent) > 0 {
		if summarizer != nil {
			var summaryMsgs []map[string]any
			for _, content := range removedContent {
				summaryMsgs = append(summaryMsgs, map[string]any{
					"role":    "tool",
					"content": content,
				})
			}
			var err error
			summary, err = summarizer.SummarizeMessages(ctx, summaryMsgs)
			if err != nil {
				// Fall back to simple marker if summarization fails
				summary = ""
			}
		}

		marker := fmt.Sprintf("[context compacted: %d large tool outputs removed]", stripped)
		if summary != "" {
			marker = fmt.Sprintf("[context compacted: %d large tool outputs summarized]\n%s", stripped, summary)
		}
		result = append(result, TranscriptMessage{
			Role:    "system",
			Name:    "compact_summary",
			Content: marker,
		})
	}

	// Keep last turns
	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result, summary, nil
}

// estimateTextTokens estimates tokens for a single string using (runes+3)/4.
func estimateTextTokens(s string) int {
	runes := utf8.RuneCountInString(s)
	if runes <= 0 {
		return 0
	}
	return (runes + 3) / 4
}

// estimateTranscriptTokens estimates total tokens for transcript messages.
func estimateTranscriptTokens(msgs []TranscriptMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateTextTokens(m.Content)
	}
	return total
}

// transcriptMsgsToMaps converts TranscriptMessages to generic maps for the replacer callback.
func transcriptMsgsToMaps(msgs []TranscriptMessage) []map[string]any {
	result := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		entry := map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
		if m.Name != "" {
			entry["name"] = m.Name
		}
		if m.ToolCallID != "" {
			entry["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			toolCalls := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				toolCalls[j] = map[string]any{
					"id":        tc.ID,
					"name":      tc.Name,
					"arguments": tc.Arguments,
				}
			}
			entry["tool_calls"] = toolCalls
		}
		result[i] = entry
	}
	return result
}
