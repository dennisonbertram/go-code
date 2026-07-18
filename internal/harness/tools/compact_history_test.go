package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// mockSummarizer implements MessageSummarizer for testing.
type mockSummarizer struct {
	result string
	err    error
	calls  int
}

func (m *mockSummarizer) SummarizeMessages(_ context.Context, msgs []map[string]any) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	if m.result != "" {
		return m.result, nil
	}
	return fmt.Sprintf("Summary of %d messages", len(msgs)), nil
}

// mockReplacer captures the replaced messages.
type mockReplacer struct {
	called   bool
	messages []map[string]any
}

func (m *mockReplacer) replace(msgs []map[string]any) {
	m.called = true
	m.messages = msgs
}

func TestCompactHistoryTool_Definition(t *testing.T) {
	t.Parallel()
	tool := compactHistoryTool(nil)
	if tool.Definition.Name != "compact_history" {
		t.Errorf("expected name compact_history, got %s", tool.Definition.Name)
	}
	if !tool.Definition.Mutating {
		t.Error("compact_history should be mutating")
	}
	if tool.Definition.ParallelSafe {
		t.Error("compact_history should not be parallel safe")
	}
	if tool.Definition.Tier != TierCore {
		t.Errorf("expected TierCore, got %s", tool.Definition.Tier)
	}
}

func TestCompactHistoryTool_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := compactHistoryTool(nil)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"mode":"invalid"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error for invalid mode")
	}
}

func TestCompactHistoryTool_NoTranscriptReader(t *testing.T) {
	t.Parallel()
	replacer := &mockReplacer{}
	ctx := context.WithValue(context.Background(), ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error when no transcript reader")
	}
}

func TestCompactHistoryTool_NoReplacer(t *testing.T) {
	t.Parallel()
	reader := contextStatusTestReader{
		runID:    "test",
		messages: []TranscriptMessage{{Role: "user", Content: "hello"}},
	}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))

	tool := compactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error when no replacer")
	}
}

func TestCompactHistoryTool_EmptyMessages(t *testing.T) {
	t.Parallel()
	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test"}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted for empty messages")
	}
}

func TestCompactHistoryTool_StripMode(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read file.txt"},
		{Index: 2, Role: "assistant", Content: "I'll read that file."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "file contents here that are long enough"},
		{Index: 4, Role: "user", Content: "Now read other.txt"},
		{Index: 5, Role: "assistant", Content: "Reading other.txt."},
		{Index: 6, Role: "tool", ToolCallID: "call_2", Content: "other file contents"},
		// These last 4 turns should be kept with keep_last=4
		{Index: 7, Role: "user", Content: "What did you find?"},
		{Index: 8, Role: "assistant", Content: "I found interesting data."},
		{Index: 9, Role: "user", Content: "Summarize it."},
		{Index: 10, Role: "assistant", Content: "Here is the summary."},
	}

	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":4}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if !replacer.called {
		t.Fatal("replacer was not called")
	}

	// Verify tool messages were stripped from compaction zone
	afterTokens := result["after_tokens"].(float64)
	beforeTokens := result["before_tokens"].(float64)
	if afterTokens >= beforeTokens {
		t.Errorf("expected after_tokens (%v) < before_tokens (%v)", afterTokens, beforeTokens)
	}

	// Verify the compact summary marker exists
	foundMarker := false
	for _, m := range replacer.messages {
		if m["role"] == "system" && m["name"] == "compact_summary" {
			foundMarker = true
			content := m["content"].(string)
			if !strings.Contains(content, "stripped") {
				t.Errorf("expected stripped marker, got: %s", content)
			}
		}
	}
	if !foundMarker {
		t.Error("expected compact summary marker in output")
	}
}

func TestCompactHistoryTool_SummarizeMode(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi there!"},
		{Index: 3, Role: "user", Content: "Read a file"},
		{Index: 4, Role: "assistant", Content: ""},
		{Index: 5, Role: "tool", ToolCallID: "call_1", Content: "file data"},
		// keep_last=2 window:
		{Index: 6, Role: "user", Content: "Thanks"},
		{Index: 7, Role: "assistant", Content: "You're welcome!"},
	}

	summarizer := &mockSummarizer{result: "The user greeted, then asked to read a file. The file contained data."}
	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if summarizer.calls != 1 {
		t.Errorf("expected 1 summarizer call, got %d", summarizer.calls)
	}
	if result["summary"] == nil {
		t.Error("expected summary in result")
	}

	// Verify compact summary message exists
	foundSummary := false
	for _, m := range replacer.messages {
		if m["name"] == "compact_summary" && m["role"] == "system" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("expected compact summary message")
	}
}

func TestCompactHistoryTool_SummarizeMode_NoSummarizer(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi"},
		{Index: 3, Role: "user", Content: "Bye"},
		{Index: 4, Role: "assistant", Content: "Goodbye"},
		{Index: 5, Role: "user", Content: "Last"},
		{Index: 6, Role: "assistant", Content: "Final"},
	}

	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil) // no summarizer
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error when summarize mode used without summarizer")
	}
}

func TestCompactHistoryTool_HybridMode(t *testing.T) {
	t.Parallel()
	// Create a large tool result (>500 tokens ~ >2000 chars)
	largeContent := strings.Repeat("x", 3000)

	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read big file"},
		{Index: 2, Role: "assistant", Content: "Reading..."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: largeContent},
		{Index: 4, Role: "user", Content: "Read small file"},
		{Index: 5, Role: "assistant", Content: "Reading small..."},
		{Index: 6, Role: "tool", ToolCallID: "call_2", Content: "tiny"},
		// keep_last window:
		{Index: 7, Role: "user", Content: "Done?"},
		{Index: 8, Role: "assistant", Content: "Yes!"},
	}

	summarizer := &mockSummarizer{result: "Large file was read with data."}
	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	afterTokens := result["after_tokens"].(float64)
	beforeTokens := result["before_tokens"].(float64)
	if afterTokens >= beforeTokens {
		t.Errorf("expected after_tokens (%v) < before_tokens (%v)", afterTokens, beforeTokens)
	}

	// The small tool result should be preserved
	foundSmallTool := false
	for _, m := range replacer.messages {
		if m["role"] == "tool" {
			content := m["content"].(string)
			if content == "tiny" {
				foundSmallTool = true
			}
		}
	}
	if !foundSmallTool {
		t.Error("expected small tool result to be preserved in hybrid mode")
	}
}

func TestCompactHistoryTool_NothingToCompact(t *testing.T) {
	t.Parallel()
	// Only 2 turns, keep_last=4 means nothing to compact
	msgs := []TranscriptMessage{
		{Index: 0, Role: "user", Content: "Hello"},
		{Index: 1, Role: "assistant", Content: "Hi!"},
	}

	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted")
	}
	if replacer.called {
		t.Error("replacer should not be called when nothing to compact")
	}
}

func TestParseTurns(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "assistant", Content: "reading"},
		{Role: "tool", ToolCallID: "c1", Content: "data"},
		{Role: "user", Content: "bye"},
	}

	turns := parseTurns(msgs)
	if len(turns) != 5 {
		t.Fatalf("expected 5 turns, got %d", len(turns))
	}
	if turns[0].Kind != "system_prefix" {
		t.Errorf("turn 0: expected system_prefix, got %s", turns[0].Kind)
	}
	if turns[1].Kind != "user" {
		t.Errorf("turn 1: expected user, got %s", turns[1].Kind)
	}
	if turns[2].Kind != "assistant_text" {
		t.Errorf("turn 2: expected assistant_text, got %s", turns[2].Kind)
	}
	if turns[3].Kind != "assistant_tool" {
		t.Errorf("turn 3: expected assistant_tool, got %s", turns[3].Kind)
	}
	if turns[4].Kind != "user" {
		t.Errorf("turn 4: expected user, got %s", turns[4].Kind)
	}
}

func TestFindCompactionBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		turns      []turn
		keepLast   int
		wantPrefix int
		wantEnd    int
	}{
		{
			name: "basic",
			turns: []turn{
				{Kind: "system_prefix"},
				{Kind: "user"},
				{Kind: "assistant_tool"},
				{Kind: "user"},
				{Kind: "assistant_text"},
			},
			keepLast:   2,
			wantPrefix: 1,
			wantEnd:    3,
		},
		{
			name: "nothing to compact",
			turns: []turn{
				{Kind: "system_prefix"},
				{Kind: "user"},
				{Kind: "assistant_text"},
			},
			keepLast:   4,
			wantPrefix: 1,
			wantEnd:    1,
		},
		{
			name: "multiple system prefix",
			turns: []turn{
				{Kind: "system_prefix"},
				{Kind: "compact_summary"},
				{Kind: "user"},
				{Kind: "assistant_tool"},
				{Kind: "user"},
				{Kind: "assistant_text"},
			},
			keepLast:   2,
			wantPrefix: 2,
			wantEnd:    4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prefix, end := findCompactionBounds(tt.turns, tt.keepLast)
			if prefix != tt.wantPrefix {
				t.Errorf("prefixEnd = %d, want %d", prefix, tt.wantPrefix)
			}
			if end != tt.wantEnd {
				t.Errorf("compactEnd = %d, want %d", end, tt.wantEnd)
			}
		})
	}
}

func TestCompactHistoryTool_KeepLastDefault(t *testing.T) {
	t.Parallel()
	// When keep_last is 0 or < 2, it should default to 4
	msgs := []TranscriptMessage{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "t1"},
		{Role: "assistant", Content: "r1"},
		{Role: "user", Content: "t2"},
		{Role: "assistant", Content: "r2"},
		{Role: "user", Content: "t3"},
		{Role: "assistant", Content: "r3"},
		{Role: "user", Content: "t4"},
		{Role: "assistant", Content: "r4"},
		{Role: "user", Content: "t5"},
		{Role: "assistant", Content: "r5"},
	}

	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(nil)
	// keep_last=0 should default to 4
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":0}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// With 10 non-prefix turns and keep_last=4 (default), we compact 6 turns
	turnsCompacted := result["turns_compacted"].(float64)
	if turnsCompacted != 6 {
		t.Errorf("expected 6 turns compacted (default keep_last=4), got %v", turnsCompacted)
	}
}

func TestEstimateTextTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hi", 1},          // (2+3)/4 = 1
		{"hello", 2},       // (5+3)/4 = 2
		{"hello world", 3}, // (11+3)/4 = 3
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := estimateTextTokens(tt.input)
			if got != tt.want {
				t.Errorf("estimateTextTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestTranscriptMsgsToMaps(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCallID: "c1", Content: "data"},
		{Role: "system", Name: "compact_summary", Content: "summary"},
	}

	maps := transcriptMsgsToMaps(msgs)
	if len(maps) != 3 {
		t.Fatalf("expected 3 maps, got %d", len(maps))
	}
	if maps[0]["role"] != "user" {
		t.Error("expected user role")
	}
	if maps[1]["tool_call_id"] != "c1" {
		t.Error("expected tool_call_id")
	}
	if maps[2]["name"] != "compact_summary" {
		t.Error("expected name field")
	}
}

func TestCompactHistoryTool_SummarizerError(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi there!"},
		{Index: 3, Role: "user", Content: "Do something"},
		{Index: 4, Role: "assistant", Content: "Done"},
		{Index: 5, Role: "user", Content: "Thanks"},
		{Index: 6, Role: "assistant", Content: "Welcome"},
	}

	summarizer := &mockSummarizer{err: fmt.Errorf("LLM unavailable")}
	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error in result")
	}
	if !strings.Contains(errMsg, "summarization failed") {
		t.Errorf("expected summarization failed error, got: %s", errMsg)
	}
}

// toolCallIDsOf extracts the tool_call ids from a replacer message map.
func toolCallIDsOf(m map[string]any) []string {
	var ids []string
	if tcs, ok := m["tool_calls"].([]map[string]any); ok {
		for _, tc := range tcs {
			id, _ := tc["id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}

// assertToolCallPairing enforces the provider-side two-way invariant: every
// assistant tool_calls id must have a following tool result with that id, and
// every tool result's tool_call_id must appear in some preceding assistant
// message's tool_calls. OpenAI/Anthropic reject transcripts that violate it.
func assertToolCallPairing(t *testing.T, msgs []map[string]any) {
	t.Helper()
	seenCallIDs := map[string]bool{}
	answeredCallIDs := map[string]bool{}
	for _, m := range msgs {
		switch m["role"] {
		case "assistant":
			for _, id := range toolCallIDsOf(m) {
				seenCallIDs[id] = true
			}
		case "tool":
			id, _ := m["tool_call_id"].(string)
			if !seenCallIDs[id] {
				t.Errorf("orphan tool result: tool_call_id %q has no preceding assistant tool_calls entry", id)
			}
			answeredCallIDs[id] = true
		}
	}
	for id := range seenCallIDs {
		if !answeredCallIDs[id] {
			t.Errorf("unanswered tool call: id %q has no following tool result", id)
		}
	}
}

// TestCompactHistoryTool_HybridModePreservesToolCallPairing guards #787: when
// hybrid compaction drops a large tool result but keeps a small one from the
// same assistant_tool turn, the rebuilt assistant message must keep exactly
// the tool_calls whose results survived. Rebuilding it with no tool_calls at
// all produces an orphan tool message that providers reject with a 400.
func TestCompactHistoryTool_HybridModePreservesToolCallPairing(t *testing.T) {
	t.Parallel()
	largeContent := strings.Repeat("x", 3000) // >500 estimated tokens

	msgs := []TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read both files"},
		{Index: 2, Role: "assistant", Content: "Reading both.", ToolCalls: []ToolCall{
			{ID: "call_big", Name: "read", Arguments: `{"path":"big.txt"}`},
			{ID: "call_small", Name: "read", Arguments: `{"path":"small.txt"}`},
		}},
		{Index: 3, Role: "tool", ToolCallID: "call_big", Content: largeContent},
		{Index: 4, Role: "tool", ToolCallID: "call_small", Content: "tiny"},
		// keep_last=2 window:
		{Index: 5, Role: "user", Content: "Done?"},
		{Index: 6, Role: "assistant", Content: "Yes!"},
	}

	summarizer := &mockSummarizer{result: "Big file contents."}
	replacer := &mockReplacer{}
	reader := contextStatusTestReader{runID: "test", messages: msgs}
	ctx := context.WithValue(context.Background(), ContextKeyTranscriptReader, TranscriptReader(reader))
	ctx = context.WithValue(ctx, ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := compactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, hasErr := result["error"]; hasErr {
		t.Fatalf("unexpected tool error: %v", result["error"])
	}
	if !replacer.called {
		t.Fatal("replacer was not called")
	}

	// Exactly one tool result survives: the small one (call_big was >500 tokens).
	var toolIdx []int
	for i, m := range replacer.messages {
		if m["role"] == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) != 1 {
		t.Fatalf("expected exactly 1 surviving tool message, got %d", len(toolIdx))
	}
	survivor := replacer.messages[toolIdx[0]]
	if survivor["tool_call_id"] != "call_small" {
		t.Errorf("expected surviving tool message tool_call_id=call_small, got %v", survivor["tool_call_id"])
	}

	// The nearest preceding assistant message must carry exactly the surviving
	// tool_call id — not both, and not none.
	var parent map[string]any
	for i := toolIdx[0] - 1; i >= 0; i-- {
		if replacer.messages[i]["role"] == "assistant" {
			parent = replacer.messages[i]
			break
		}
	}
	if parent == nil {
		t.Fatal("no assistant message precedes the surviving tool result")
	}
	ids := toolCallIDsOf(parent)
	if len(ids) != 1 || ids[0] != "call_small" {
		t.Errorf("expected parent assistant tool_calls ids exactly [call_small], got %v", ids)
	}

	// Whole-transcript two-way pairing invariant.
	assertToolCallPairing(t, replacer.messages)
}

func TestParseTurns_OrphanToolResult(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Role: "tool", ToolCallID: "orphan", Content: "orphan data"},
		{Role: "user", Content: "hello"},
	}

	turns := parseTurns(msgs)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Kind != "assistant_tool" {
		t.Errorf("turn 0: expected assistant_tool for orphan, got %s", turns[0].Kind)
	}
	if turns[1].Kind != "user" {
		t.Errorf("turn 1: expected user, got %s", turns[1].Kind)
	}
}

func TestParseTurns_Empty(t *testing.T) {
	t.Parallel()
	turns := parseTurns(nil)
	if turns != nil {
		t.Errorf("expected nil turns for empty input, got %v", turns)
	}
}

func TestEstimateTranscriptTokens(t *testing.T) {
	t.Parallel()
	msgs := []TranscriptMessage{
		{Content: "hello"}, // 2 tokens
		{Content: ""},      // 0 tokens
		{Content: "hi"},    // 1 token
	}
	got := estimateTranscriptTokens(msgs)
	if got != 3 {
		t.Errorf("estimateTranscriptTokens = %d, want 3", got)
	}
}
