package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tools "go-agent-harness/internal/harness/tools"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// testTranscriptReader implements tools.TranscriptReader for tests.
type testTranscriptReader struct {
	messages []tools.TranscriptMessage
}

func (r testTranscriptReader) Snapshot(limit int, includeTools bool) tools.TranscriptSnapshot {
	msgs := r.messages
	if !includeTools {
		var filtered []tools.TranscriptMessage
		for _, m := range msgs {
			if m.Role != "tool" {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return tools.TranscriptSnapshot{
		Messages:    msgs,
		GeneratedAt: time.Now(),
	}
}

// testSummarizer implements tools.MessageSummarizer for tests.
type testSummarizer struct {
	result string
	err    error
	mu     sync.Mutex
	calls  int
}

func (s *testSummarizer) SummarizeMessages(_ context.Context, msgs []map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	if s.result != "" {
		return s.result, nil
	}
	return fmt.Sprintf("Summary of %d messages", len(msgs)), nil
}

// testReplacer captures the replaced messages.
type testReplacer struct {
	mu       sync.Mutex
	called   bool
	messages []map[string]any
}

func (r *testReplacer) replace(msgs []map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.called = true
	r.messages = msgs
}

// makeCtx builds a context with the given transcript reader and replacer.
func makeCtx(reader tools.TranscriptReader, replacer *testReplacer) context.Context {
	ctx := context.Background()
	if reader != nil {
		ctx = context.WithValue(ctx, tools.ContextKeyTranscriptReader, reader)
	}
	if replacer != nil {
		ctx = context.WithValue(ctx, tools.ContextKeyMessageReplacer, func(msgs []map[string]any) {
			replacer.replace(msgs)
		})
	}
	return ctx
}

// parseResult unmarshals a JSON tool result string into a map.
func parseResult(t *testing.T, out string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse output %q: %v", out, err)
	}
	return result
}

// ===========================================================================
// context_status tests
// ===========================================================================

func TestContextStatusTool_Core_Definition(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	if tool.Definition.Name != "context_status" {
		t.Errorf("expected name context_status, got %s", tool.Definition.Name)
	}
	if tool.Definition.Mutating {
		t.Error("context_status should not be mutating")
	}
	if !tool.Definition.ParallelSafe {
		t.Error("context_status should be parallel safe")
	}
	if tool.Definition.Tier != tools.TierCore {
		t.Errorf("expected TierCore, got %s", tool.Definition.Tier)
	}
	if tool.Definition.Action != tools.ActionRead {
		t.Errorf("expected ActionRead, got %s", tool.Definition.Action)
	}
	if tool.Handler == nil {
		t.Error("handler is nil")
	}
	if tool.Definition.Parameters == nil {
		t.Error("parameters is nil")
	}
}

func TestContextStatusTool_Core_NoTranscriptReader(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	out, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	if _, ok := result["error"]; !ok {
		t.Error("expected error JSON field when no transcript reader")
	}
}

func TestContextStatusTool_Core_EmptyConversation(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	reader := testTranscriptReader{}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["message_count"].(float64) != 0 {
		t.Errorf("expected 0 messages, got %v", result["message_count"])
	}
	if result["estimated_context_tokens"].(float64) != 0 {
		t.Errorf("expected 0 tokens, got %v", result["estimated_context_tokens"])
	}
	if result["user_message_count"].(float64) != 0 {
		t.Errorf("expected 0 user messages, got %v", result["user_message_count"])
	}
	rec := result["recommendation"].(string)
	if !strings.HasPrefix(rec, "healthy") {
		t.Errorf("expected healthy recommendation, got: %s", rec)
	}
}

func TestContextStatusTool_Core_MixedMessages(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Index: 0, Role: "system", Content: "You are a helpful assistant."},
			{Index: 1, Role: "user", Content: "Hello"},
			{Index: 2, Role: "assistant", Content: "Hi there!"},
			{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "file contents here"},
			{Index: 4, Role: "assistant", Content: "I found the file."},
			{Index: 5, Role: "user", Content: "Thanks"},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	assertEqual := func(key string, expected float64) {
		t.Helper()
		got, ok := result[key].(float64)
		if !ok {
			t.Errorf("key %s not a number: %v", key, result[key])
			return
		}
		if got != expected {
			t.Errorf("%s: expected %v, got %v", key, expected, got)
		}
	}

	assertEqual("message_count", 6)
	assertEqual("user_message_count", 2)
	assertEqual("assistant_message_count", 2)
	assertEqual("tool_result_count", 1)
	assertEqual("tool_call_count", 1)
	assertEqual("system_message_count", 1)

	if result["has_compact_summary"].(bool) != false {
		t.Error("expected has_compact_summary to be false")
	}
}

func TestContextStatusTool_Core_CompactSummaryDetection(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Index: 0, Role: "system", Name: "compact_summary", Content: "Summary of prior conversation."},
			{Index: 1, Role: "user", Content: "Continue please"},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["has_compact_summary"].(bool) != true {
		t.Error("expected has_compact_summary to be true")
	}
}

func TestContextStatusTool_Core_LargeContext_Critical(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()

	// >100k estimated tokens triggers critical. Each ASCII char ~ 0.25 tokens.
	// 200k chars => ~50k tokens each => 3 x 50k = 150k tokens > 100k threshold.
	bigContent := strings.Repeat("a", 200000)
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Index: 0, Role: "user", Content: "Hello"},
			{Index: 1, Role: "tool", ToolCallID: "call_1", Content: bigContent},
			{Index: 2, Role: "tool", ToolCallID: "call_2", Content: bigContent},
			{Index: 3, Role: "tool", ToolCallID: "call_3", Content: bigContent},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	rec := result["recommendation"].(string)
	if !strings.HasPrefix(rec, "critical") {
		t.Errorf("expected critical recommendation, got: %s", rec)
	}
}

func TestContextStatusTool_Core_RecommendationThresholds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		tokens      int
		msgCount    int
		toolResults int
		wantPrefix  string
	}{
		{"healthy_low", 1000, 5, 2, "healthy"},
		{"elevated_tokens_30k", 35000, 10, 5, "elevated"},
		{"elevated_many_tool_results", 5000, 25, 25, "elevated"},
		{"warning_60k", 65000, 20, 10, "warning"},
		{"critical_100k", 110000, 30, 15, "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Build messages to hit the desired token/count targets.
			// We approximate: each char => 0.25 tokens, so chars = tokens*4.
			charCount := tt.tokens * 4
			var msgs []tools.TranscriptMessage
			// Add user messages
			for i := 0; i < tt.msgCount-tt.toolResults; i++ {
				msgs = append(msgs, tools.TranscriptMessage{
					Role:    "user",
					Content: "",
				})
			}
			// Add tool messages spreading the content
			if tt.toolResults > 0 {
				perTool := charCount / tt.toolResults
				for i := 0; i < tt.toolResults; i++ {
					msgs = append(msgs, tools.TranscriptMessage{
						Role:       "tool",
						ToolCallID: fmt.Sprintf("call_%d", i),
						Content:    strings.Repeat("x", perTool),
					})
				}
			} else if charCount > 0 {
				// Put all content in a single user message
				msgs[0].Content = strings.Repeat("x", charCount)
			}

			reader := testTranscriptReader{messages: msgs}
			ctx := makeCtx(reader, nil)
			tool := ContextStatusTool()
			out, err := tool.Handler(ctx, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result := parseResult(t, out)
			rec := result["recommendation"].(string)
			if !strings.HasPrefix(rec, tt.wantPrefix) {
				t.Errorf("expected prefix %q, got %q", tt.wantPrefix, rec)
			}
		})
	}
}

func TestContextStatusTool_Core_SystemMessageNotCompactSummary(t *testing.T) {
	t.Parallel()
	// System messages without name="compact_summary" should not set has_compact_summary.
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Index: 0, Role: "system", Name: "other_name", Content: "Not a compact summary."},
			{Index: 1, Role: "system", Content: "Plain system message."},
			{Index: 2, Role: "user", Content: "Hello"},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
	if result["has_compact_summary"].(bool) != false {
		t.Error("expected has_compact_summary=false for non-compact system messages")
	}
	if result["system_message_count"].(float64) != 2 {
		t.Errorf("expected 2 system messages, got %v", result["system_message_count"])
	}
}

// ===========================================================================
// compact_history tests
// ===========================================================================

func TestCompactHistoryTool_Core_Definition(t *testing.T) {
	t.Parallel()
	tool := CompactHistoryTool(nil)
	if tool.Definition.Name != "compact_history" {
		t.Errorf("expected name compact_history, got %s", tool.Definition.Name)
	}
	if !tool.Definition.Mutating {
		t.Error("compact_history should be mutating")
	}
	if tool.Definition.ParallelSafe {
		t.Error("compact_history should not be parallel safe")
	}
	if tool.Definition.Tier != tools.TierCore {
		t.Errorf("expected TierCore, got %s", tool.Definition.Tier)
	}
	if tool.Definition.Action != tools.ActionExecute {
		t.Errorf("expected ActionExecute, got %s", tool.Definition.Action)
	}
	if tool.Handler == nil {
		t.Error("handler is nil")
	}
	if tool.Definition.Parameters == nil {
		t.Error("parameters is nil")
	}
}

func TestCompactHistoryTool_Core_InvalidMode(t *testing.T) {
	t.Parallel()
	tool := CompactHistoryTool(nil)
	replacer := &testReplacer{}
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{{Role: "user", Content: "hi"}},
	}
	ctx := makeCtx(reader, replacer)

	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"invalid"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error field in result")
	}
	if !strings.Contains(errMsg, "mode must be one of") {
		t.Errorf("expected mode validation error, got: %s", errMsg)
	}
}

func TestCompactHistoryTool_Core_InvalidJSON(t *testing.T) {
	t.Parallel()
	tool := CompactHistoryTool(nil)
	replacer := &testReplacer{}
	reader := testTranscriptReader{}
	ctx := makeCtx(reader, replacer)

	out, err := tool.Handler(ctx, json.RawMessage(`{not json`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	if _, ok := result["error"]; !ok {
		t.Error("expected error for malformed JSON args")
	}
}

func TestCompactHistoryTool_Core_MissingTranscriptReader(t *testing.T) {
	t.Parallel()
	replacer := &testReplacer{}
	// Only set replacer, no transcript reader.
	ctx := context.WithValue(context.Background(), tools.ContextKeyMessageReplacer, func(msgs []map[string]any) {
		replacer.replace(msgs)
	})

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error field")
	}
	if !strings.Contains(errMsg, "transcript reader not available") {
		t.Errorf("expected transcript reader error, got: %s", errMsg)
	}
}

func TestCompactHistoryTool_Core_MissingReplacer(t *testing.T) {
	t.Parallel()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{{Role: "user", Content: "hello"}},
	}
	// Only set reader, no replacer.
	ctx := context.WithValue(context.Background(), tools.ContextKeyTranscriptReader, tools.TranscriptReader(reader))

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error field")
	}
	if !strings.Contains(errMsg, "message replacer not available") {
		t.Errorf("expected replacer error, got: %s", errMsg)
	}
}

func TestCompactHistoryTool_Core_EmptyMessages(t *testing.T) {
	t.Parallel()
	replacer := &testReplacer{}
	reader := testTranscriptReader{} // no messages
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted for empty messages")
	}
	if result["before_tokens"].(float64) != 0 {
		t.Error("expected 0 before_tokens")
	}
	if result["after_tokens"].(float64) != 0 {
		t.Error("expected 0 after_tokens")
	}
	if replacer.called {
		t.Error("replacer should not be called for empty messages")
	}
}

func TestCompactHistoryTool_Core_NothingToCompact(t *testing.T) {
	t.Parallel()
	// Only 2 turns, keep_last defaults to 4 => nothing to compact.
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "Hello"},
		{Index: 1, Role: "assistant", Content: "Hi!"},
	}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted")
	}
	beforeTokens := result["before_tokens"].(float64)
	afterTokens := result["after_tokens"].(float64)
	if beforeTokens != afterTokens {
		t.Errorf("before (%v) should equal after (%v) when nothing compacted", beforeTokens, afterTokens)
	}
	if replacer.called {
		t.Error("replacer should not be called when nothing to compact")
	}
}

func TestCompactHistoryTool_Core_SingleMessage(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "Hello"},
	}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted for a single message")
	}
}

func TestCompactHistoryTool_Core_StripMode(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read file.txt"},
		{Index: 2, Role: "assistant", Content: "I'll read that file."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "file contents here that are long enough"},
		{Index: 4, Role: "user", Content: "Now read other.txt"},
		{Index: 5, Role: "assistant", Content: "Reading other.txt."},
		{Index: 6, Role: "tool", ToolCallID: "call_2", Content: "other file contents"},
		// keep_last=4 window:
		{Index: 7, Role: "user", Content: "What did you find?"},
		{Index: 8, Role: "assistant", Content: "I found interesting data."},
		{Index: 9, Role: "user", Content: "Summarize it."},
		{Index: 10, Role: "assistant", Content: "Here is the summary."},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":4}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if !replacer.called {
		t.Fatal("replacer was not called")
	}

	afterTokens := result["after_tokens"].(float64)
	beforeTokens := result["before_tokens"].(float64)
	if afterTokens >= beforeTokens {
		t.Errorf("expected after_tokens (%v) < before_tokens (%v)", afterTokens, beforeTokens)
	}

	// Verify the compact summary marker exists.
	foundMarker := false
	for _, m := range replacer.messages {
		if m["role"] == "system" && m["name"] == "compact_summary" {
			foundMarker = true
			content, _ := m["content"].(string)
			if !strings.Contains(content, "stripped") {
				t.Errorf("expected stripped marker, got: %s", content)
			}
		}
	}
	if !foundMarker {
		t.Error("expected compact summary marker in output")
	}

	// Tool messages from compaction zone should be gone.
	for _, m := range replacer.messages {
		if m["role"] == "tool" {
			// Tool messages should only appear in the keep_last window,
			// but keep_last window has no tool messages here.
			t.Error("unexpected tool message in stripped output compaction zone")
		}
	}

	// User/assistant text content from compaction zone should be preserved.
	foundUserRead := false
	for _, m := range replacer.messages {
		if m["role"] == "user" {
			c, _ := m["content"].(string)
			if c == "Read file.txt" {
				foundUserRead = true
			}
		}
	}
	if !foundUserRead {
		t.Error("expected user text to be preserved after strip")
	}
}

func TestCompactHistoryTool_Core_SummarizeMode(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
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

	summarizer := &testSummarizer{result: "OK"}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	summarizer.mu.Lock()
	calls := summarizer.calls
	summarizer.mu.Unlock()
	if calls != 1 {
		t.Errorf("expected 1 summarizer call, got %d", calls)
	}
	if result["summary"] == nil {
		t.Error("expected summary in result")
	}

	// The compaction zone had 5 messages replaced by a single short summary,
	// so the after token count should be less.
	afterTokens := result["after_tokens"].(float64)
	beforeTokens := result["before_tokens"].(float64)
	if afterTokens >= beforeTokens {
		t.Errorf("expected after_tokens (%v) < before_tokens (%v)", afterTokens, beforeTokens)
	}

	// Verify compact summary message exists in replacement.
	foundSummary := false
	for _, m := range replacer.messages {
		if m["name"] == "compact_summary" && m["role"] == "system" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("expected compact summary message in replacer output")
	}
}

func TestCompactHistoryTool_Core_SummarizeModeWithoutSummarizer(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi"},
		{Index: 3, Role: "user", Content: "Bye"},
		{Index: 4, Role: "assistant", Content: "Goodbye"},
		{Index: 5, Role: "user", Content: "Last"},
		{Index: 6, Role: "assistant", Content: "Final"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil) // no summarizer
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error when summarize mode used without summarizer")
	}
	if !strings.Contains(errMsg, "summarize mode requires a message summarizer") {
		t.Errorf("unexpected error message: %s", errMsg)
	}
}

func TestCompactHistoryTool_Core_SummarizeModeError(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi there!"},
		{Index: 3, Role: "user", Content: "Do something"},
		{Index: 4, Role: "assistant", Content: "Done"},
		{Index: 5, Role: "user", Content: "Thanks"},
		{Index: 6, Role: "assistant", Content: "Welcome"},
	}

	summarizer := &testSummarizer{err: fmt.Errorf("LLM unavailable")}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error in result")
	}
	if !strings.Contains(errMsg, "summarization failed") {
		t.Errorf("expected summarization failed error, got: %s", errMsg)
	}
}

func TestCompactHistoryTool_Core_HybridMode(t *testing.T) {
	t.Parallel()
	// Create a large tool result (>500 tokens ~ >2000 chars).
	largeContent := strings.Repeat("x", 3000)

	msgs := []tools.TranscriptMessage{
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

	summarizer := &testSummarizer{result: "Large file was read with data."}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	afterTokens := result["after_tokens"].(float64)
	beforeTokens := result["before_tokens"].(float64)
	if afterTokens >= beforeTokens {
		t.Errorf("expected after_tokens (%v) < before_tokens (%v)", afterTokens, beforeTokens)
	}

	// The small tool result should be preserved.
	foundSmallTool := false
	for _, m := range replacer.messages {
		if m["role"] == "tool" {
			content, _ := m["content"].(string)
			if content == "tiny" {
				foundSmallTool = true
			}
		}
	}
	if !foundSmallTool {
		t.Error("expected small tool result to be preserved in hybrid mode")
	}

	// The large tool result should be removed (its content should not appear).
	for _, m := range replacer.messages {
		if m["role"] == "tool" {
			content, _ := m["content"].(string)
			if content == largeContent {
				t.Error("expected large tool result to be removed in hybrid mode")
			}
		}
	}

	// Summary marker should exist.
	foundMarker := false
	for _, m := range replacer.messages {
		if m["name"] == "compact_summary" {
			foundMarker = true
			content, _ := m["content"].(string)
			if !strings.Contains(content, "summarized") {
				t.Errorf("expected summarized marker, got: %s", content)
			}
		}
	}
	if !foundMarker {
		t.Error("expected compact summary marker in hybrid output")
	}

	// Summarizer should have been called.
	summarizer.mu.Lock()
	calls := summarizer.calls
	summarizer.mu.Unlock()
	if calls != 1 {
		t.Errorf("expected 1 summarizer call, got %d", calls)
	}
}

func TestCompactHistoryTool_Core_HybridModeNoSummarizer(t *testing.T) {
	t.Parallel()
	// Hybrid mode without a summarizer should still strip large tool results.
	// It falls back to a "removed" marker instead of "summarized".
	largeContent := strings.Repeat("x", 3000)

	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read big file"},
		{Index: 2, Role: "assistant", Content: "Reading..."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: largeContent},
		// keep_last window:
		{Index: 4, Role: "user", Content: "Done?"},
		{Index: 5, Role: "assistant", Content: "Yes!"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil) // no summarizer
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	// Should not be an error; hybrid gracefully degrades.
	if _, hasErr := result["error"]; hasErr {
		t.Errorf("hybrid mode without summarizer should not error, got: %v", result["error"])
	}

	// The marker should say "removed" rather than "summarized".
	foundMarker := false
	for _, m := range replacer.messages {
		if m["name"] == "compact_summary" {
			foundMarker = true
			content, _ := m["content"].(string)
			if !strings.Contains(content, "removed") {
				t.Errorf("expected 'removed' marker, got: %s", content)
			}
		}
	}
	if !foundMarker {
		t.Error("expected compact summary marker in hybrid output without summarizer")
	}
}

func TestCompactHistoryTool_Core_HybridModeAllSmall(t *testing.T) {
	t.Parallel()
	// When all tool results are small, hybrid keeps them all and does not add a summary marker.
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read a file"},
		{Index: 2, Role: "assistant", Content: "Reading..."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "small result"},
		{Index: 4, Role: "user", Content: "Read another"},
		{Index: 5, Role: "assistant", Content: "Reading..."},
		{Index: 6, Role: "tool", ToolCallID: "call_2", Content: "also small"},
		// keep_last window:
		{Index: 7, Role: "user", Content: "Done?"},
		{Index: 8, Role: "assistant", Content: "Yes!"},
	}

	summarizer := &testSummarizer{}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if _, hasErr := result["error"]; hasErr {
		t.Errorf("unexpected error: %v", result["error"])
	}

	// Summarizer should NOT have been called since no large tool outputs.
	summarizer.mu.Lock()
	calls := summarizer.calls
	summarizer.mu.Unlock()
	if calls != 0 {
		t.Errorf("expected 0 summarizer calls when all tool results are small, got %d", calls)
	}

	// Both small tool results should be preserved.
	toolCount := 0
	for _, m := range replacer.messages {
		if m["role"] == "tool" {
			toolCount++
		}
	}
	if toolCount != 2 {
		t.Errorf("expected 2 tool messages preserved, got %d", toolCount)
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

// TestCompactHistoryTool_Core_HybridModePreservesToolCallPairing guards #787:
// when hybrid compaction drops a large tool result but keeps a small one from
// the same assistant_tool turn, the rebuilt assistant message must keep
// exactly the tool_calls whose results survived. Rebuilding it with no
// tool_calls at all produces an orphan tool message that providers reject
// with a 400.
func TestCompactHistoryTool_Core_HybridModePreservesToolCallPairing(t *testing.T) {
	t.Parallel()
	largeContent := strings.Repeat("x", 3000) // >500 estimated tokens

	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "Read both files"},
		{Index: 2, Role: "assistant", Content: "Reading both.", ToolCalls: []tools.ToolCall{
			{ID: "call_big", Name: "read", Arguments: `{"path":"big.txt"}`},
			{ID: "call_small", Name: "read", Arguments: `{"path":"small.txt"}`},
		}},
		{Index: 3, Role: "tool", ToolCallID: "call_big", Content: largeContent},
		{Index: 4, Role: "tool", ToolCallID: "call_small", Content: "tiny"},
		// keep_last=2 window:
		{Index: 5, Role: "user", Content: "Done?"},
		{Index: 6, Role: "assistant", Content: "Yes!"},
	}

	summarizer := &testSummarizer{result: "Big file contents."}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
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

func TestCompactHistoryTool_Core_KeepLastDefaults(t *testing.T) {
	t.Parallel()
	// When keep_last=0 or keep_last=1 (< 2), it should default to 4.
	msgs := []tools.TranscriptMessage{
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

	for _, keepLast := range []int{0, 1} {
		keepLast := keepLast
		t.Run(fmt.Sprintf("keep_last=%d", keepLast), func(t *testing.T) {
			t.Parallel()
			replacer := &testReplacer{}
			reader := testTranscriptReader{messages: msgs}
			ctx := makeCtx(reader, replacer)

			tool := CompactHistoryTool(nil)
			argJSON := fmt.Sprintf(`{"mode":"strip","keep_last":%d}`, keepLast)
			out, err := tool.Handler(ctx, json.RawMessage(argJSON))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result := parseResult(t, out)

			// With 10 non-prefix turns and keep_last=4 (default), we compact 6.
			turnsCompacted := result["turns_compacted"].(float64)
			if turnsCompacted != 6 {
				t.Errorf("expected 6 turns compacted (default keep_last=4), got %v", turnsCompacted)
			}
		})
	}
}

func TestCompactHistoryTool_Core_KeepLastExplicit(t *testing.T) {
	t.Parallel()
	// With keep_last=2, more turns should be compacted.
	msgs := []tools.TranscriptMessage{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "t1"},
		{Role: "assistant", Content: "r1"},
		{Role: "user", Content: "t2"},
		{Role: "assistant", Content: "r2"},
		{Role: "user", Content: "t3"},
		{Role: "assistant", Content: "r3"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	// 6 non-prefix turns, keep 2 => compact 4.
	turnsCompacted := result["turns_compacted"].(float64)
	if turnsCompacted != 4 {
		t.Errorf("expected 4 turns compacted, got %v", turnsCompacted)
	}
}

func TestCompactHistoryTool_Core_ReplacerCallback(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi"},
		{Index: 3, Role: "user", Content: "Read file"},
		{Index: 4, Role: "assistant", Content: "Reading..."},
		{Index: 5, Role: "tool", ToolCallID: "call_1", Content: "file data here"},
		// keep_last window:
		{Index: 6, Role: "user", Content: "Thanks"},
		{Index: 7, Role: "assistant", Content: "Welcome"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	_, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	replacer.mu.Lock()
	defer replacer.mu.Unlock()

	if !replacer.called {
		t.Fatal("replacer was not called")
	}
	if len(replacer.messages) == 0 {
		t.Fatal("replacer received empty message slice")
	}

	// Verify the replacer receives maps with expected keys.
	for i, m := range replacer.messages {
		if _, ok := m["role"]; !ok {
			t.Errorf("message %d missing 'role' key", i)
		}
		if _, ok := m["content"]; !ok {
			t.Errorf("message %d missing 'content' key", i)
		}
	}

	// The system prompt should be preserved as the first message.
	if replacer.messages[0]["role"] != "system" {
		t.Errorf("first message should be system, got %v", replacer.messages[0]["role"])
	}

	// The last messages should be from the keep_last window.
	last := replacer.messages[len(replacer.messages)-1]
	if last["content"] != "Welcome" {
		t.Errorf("last message content should be 'Welcome', got %v", last["content"])
	}
}

func TestCompactHistoryTool_Core_StripModePreservesAssistantText(t *testing.T) {
	t.Parallel()
	// When an assistant_tool turn has text content, strip mode should keep
	// the assistant's text (but remove tool results).
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System"},
		{Index: 1, Role: "user", Content: "Do something"},
		{Index: 2, Role: "assistant", Content: "I'll call a tool for this."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "tool output"},
		// keep_last:
		{Index: 4, Role: "user", Content: "Thanks"},
		{Index: 5, Role: "assistant", Content: "Welcome"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	_, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The assistant's text "I'll call a tool for this." should be preserved.
	foundAssistantText := false
	for _, m := range replacer.messages {
		if m["role"] == "assistant" {
			c, _ := m["content"].(string)
			if c == "I'll call a tool for this." {
				foundAssistantText = true
			}
		}
	}
	if !foundAssistantText {
		t.Error("strip mode should preserve assistant text from assistant_tool turns")
	}
}

func TestCompactHistoryTool_Core_StripModeEmptyAssistantText(t *testing.T) {
	t.Parallel()
	// When the assistant message in an assistant_tool turn has empty content,
	// it should NOT be emitted.
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System"},
		{Index: 1, Role: "user", Content: "Do something"},
		{Index: 2, Role: "assistant", Content: ""},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "tool output"},
		// keep_last:
		{Index: 4, Role: "user", Content: "Thanks"},
		{Index: 5, Role: "assistant", Content: "Welcome"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	_, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count assistant messages in compaction zone (before the keep_last window).
	assistantCount := 0
	for _, m := range replacer.messages {
		if m["role"] == "assistant" {
			c, _ := m["content"].(string)
			if c == "" {
				assistantCount++
			}
		}
	}
	if assistantCount > 0 {
		t.Error("strip mode should not emit empty-content assistant messages")
	}
}

func TestCompactHistoryTool_Core_MultipleSystemPrefixes(t *testing.T) {
	t.Parallel()
	// Multiple system messages and a compact_summary at the start should all be preserved.
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "system", Name: "compact_summary", Content: "Previous summary"},
		{Index: 2, Role: "user", Content: "Hello"},
		{Index: 3, Role: "assistant", Content: "Hi"},
		{Index: 4, Role: "user", Content: "Read file"},
		{Index: 5, Role: "assistant", Content: "Reading..."},
		{Index: 6, Role: "tool", ToolCallID: "call_1", Content: "data"},
		// keep_last:
		{Index: 7, Role: "user", Content: "Thanks"},
		{Index: 8, Role: "assistant", Content: "Welcome"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	_, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System prefix messages should be preserved.
	systemCount := 0
	for _, m := range replacer.messages {
		if m["role"] == "system" {
			systemCount++
		}
	}
	// Original system + compact_summary prefix + new compact marker = 3
	if systemCount < 2 {
		t.Errorf("expected at least 2 system messages (prefixes preserved), got %d", systemCount)
	}
}

// ===========================================================================
// Concurrency tests (for -race flag)
// ===========================================================================

func TestContextStatusTool_Core_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!"},
			{Role: "tool", ToolCallID: "call_1", Content: "data"},
			{Role: "user", Content: "Thanks"},
		},
	}
	ctx := makeCtx(reader, nil)

	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := tool.Handler(ctx, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			result := parseResult(t, out)
			if result["message_count"].(float64) != 5 {
				t.Errorf("expected 5 messages, got %v", result["message_count"])
			}
		}()
	}
	wg.Wait()
}

func TestCompactHistoryTool_Core_ConcurrentStripCalls(t *testing.T) {
	t.Parallel()
	// Verify no data races when multiple goroutines call compact_history concurrently.
	// Each goroutine gets its own replacer to avoid races on the shared mock.
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi"},
		{Index: 3, Role: "user", Content: "Read file"},
		{Index: 4, Role: "assistant", Content: "Reading..."},
		{Index: 5, Role: "tool", ToolCallID: "call_1", Content: "file data"},
		{Index: 6, Role: "user", Content: "Thanks"},
		{Index: 7, Role: "assistant", Content: "Welcome"},
	}

	tool := CompactHistoryTool(nil)
	reader := testTranscriptReader{messages: msgs}

	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localReplacer := &testReplacer{}
			ctx := makeCtx(reader, localReplacer)
			out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			result := parseResult(t, out)
			if _, hasErr := result["error"]; hasErr {
				t.Errorf("unexpected error in result: %v", result["error"])
			}
		}()
	}
	wg.Wait()
}

func TestCompactHistoryTool_Core_ConcurrentSummarizeCalls(t *testing.T) {
	t.Parallel()
	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System prompt"},
		{Index: 1, Role: "user", Content: "Hello"},
		{Index: 2, Role: "assistant", Content: "Hi"},
		{Index: 3, Role: "user", Content: "Read file"},
		{Index: 4, Role: "assistant", Content: "Reading..."},
		{Index: 5, Role: "tool", ToolCallID: "call_1", Content: "file data"},
		{Index: 6, Role: "user", Content: "Thanks"},
		{Index: 7, Role: "assistant", Content: "Welcome"},
	}

	summarizer := &testSummarizer{result: "Concurrent summary."}
	tool := CompactHistoryTool(summarizer)
	reader := testTranscriptReader{messages: msgs}

	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localReplacer := &testReplacer{}
			ctx := makeCtx(reader, localReplacer)
			out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"summarize","keep_last":2}`))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			result := parseResult(t, out)
			if _, hasErr := result["error"]; hasErr {
				t.Errorf("unexpected error in result: %v", result["error"])
			}
		}()
	}
	wg.Wait()

	summarizer.mu.Lock()
	calls := summarizer.calls
	summarizer.mu.Unlock()
	if calls != goroutines {
		t.Errorf("expected %d summarizer calls, got %d", goroutines, calls)
	}
}

// ===========================================================================
// Additional edge case and boundary tests
// ===========================================================================

func TestCompactHistoryTool_Core_AllModesAccepted(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"strip", "summarize", "hybrid"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			msgs := []tools.TranscriptMessage{
				{Role: "system", Content: "System"},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
				{Role: "user", Content: "Read"},
				{Role: "assistant", Content: ""},
				{Role: "tool", ToolCallID: "c1", Content: "data"},
				{Role: "user", Content: "Thanks"},
				{Role: "assistant", Content: "Welcome"},
			}

			var summarizer tools.MessageSummarizer
			if mode == "summarize" || mode == "hybrid" {
				summarizer = &testSummarizer{result: "Summary."}
			}

			replacer := &testReplacer{}
			reader := testTranscriptReader{messages: msgs}
			ctx := makeCtx(reader, replacer)

			tool := CompactHistoryTool(summarizer)
			argJSON := fmt.Sprintf(`{"mode":"%s","keep_last":2}`, mode)
			out, err := tool.Handler(ctx, json.RawMessage(argJSON))
			if err != nil {
				t.Fatalf("unexpected error for mode %s: %v", mode, err)
			}
			result := parseResult(t, out)
			if _, hasErr := result["error"]; hasErr {
				t.Errorf("unexpected error for mode %s: %v", mode, result["error"])
			}
		})
	}
}

func TestCompactHistoryTool_Core_OnlySystemMessages(t *testing.T) {
	t.Parallel()
	// If transcript is only system messages, nothing should be compacted.
	msgs := []tools.TranscriptMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "system", Name: "compact_summary", Content: "Previous summary"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted for only system messages")
	}
}

func TestCompactHistoryTool_Core_LargeKeepLast(t *testing.T) {
	t.Parallel()
	// keep_last larger than total non-prefix turns => nothing to compact.
	msgs := []tools.TranscriptMessage{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":100}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
	if result["turns_compacted"].(float64) != 0 {
		t.Error("expected 0 turns compacted when keep_last exceeds total turns")
	}
}

func TestContextStatusTool_Core_OnlyToolMessages(t *testing.T) {
	t.Parallel()
	// Edge case: only tool messages (orphaned).
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Role: "tool", ToolCallID: "c1", Content: "data1"},
			{Role: "tool", ToolCallID: "c2", Content: "data2"},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["tool_result_count"].(float64) != 2 {
		t.Errorf("expected 2 tool results, got %v", result["tool_result_count"])
	}
	if result["tool_call_count"].(float64) != 2 {
		t.Errorf("expected 2 tool calls, got %v", result["tool_call_count"])
	}
	if result["user_message_count"].(float64) != 0 {
		t.Errorf("expected 0 user messages, got %v", result["user_message_count"])
	}
}

func TestContextStatusTool_Core_EmptyContent(t *testing.T) {
	t.Parallel()
	// Messages with empty content should contribute 0 tokens.
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Role: "user", Content: ""},
			{Role: "assistant", Content: ""},
		},
	}
	ctx := makeCtx(reader, nil)

	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)

	if result["estimated_context_tokens"].(float64) != 0 {
		t.Errorf("expected 0 tokens for empty content, got %v", result["estimated_context_tokens"])
	}
	if result["message_count"].(float64) != 2 {
		t.Errorf("expected 2 messages, got %v", result["message_count"])
	}
}

func TestCompactHistoryTool_Core_HybridModeSummarizerError(t *testing.T) {
	t.Parallel()
	// In hybrid mode, if the summarizer errors, the marker should say "removed"
	// (graceful degradation) rather than returning a top-level error.
	largeContent := strings.Repeat("x", 3000)

	msgs := []tools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "System"},
		{Index: 1, Role: "user", Content: "Read big file"},
		{Index: 2, Role: "assistant", Content: "Reading..."},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: largeContent},
		// keep_last:
		{Index: 4, Role: "user", Content: "Done?"},
		{Index: 5, Role: "assistant", Content: "Yes!"},
	}

	summarizer := &testSummarizer{err: fmt.Errorf("LLM down")}
	replacer := &testReplacer{}
	reader := testTranscriptReader{messages: msgs}
	ctx := makeCtx(reader, replacer)

	tool := CompactHistoryTool(summarizer)
	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":"hybrid","keep_last":2}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)

	// Hybrid gracefully degrades on summarizer error.
	if _, hasErr := result["error"]; hasErr {
		t.Errorf("hybrid should not propagate summarizer error, got: %v", result["error"])
	}

	// Should have "removed" marker.
	for _, m := range replacer.messages {
		if m["name"] == "compact_summary" {
			content, _ := m["content"].(string)
			if !strings.Contains(content, "removed") {
				t.Errorf("expected 'removed' marker on summarizer error, got: %s", content)
			}
		}
	}
}

func TestCompactHistoryTool_Core_ModeEmptyString(t *testing.T) {
	t.Parallel()
	// Empty mode string should be rejected.
	tool := CompactHistoryTool(nil)
	replacer := &testReplacer{}
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{{Role: "user", Content: "hi"}},
	}
	ctx := makeCtx(reader, replacer)

	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":""}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	if _, ok := result["error"]; !ok {
		t.Error("expected error for empty mode string")
	}
}

func TestCompactHistoryTool_Core_ModeNotString(t *testing.T) {
	t.Parallel()
	// Numeric mode should fail at JSON unmarshal or mode validation.
	tool := CompactHistoryTool(nil)
	replacer := &testReplacer{}
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{{Role: "user", Content: "hi"}},
	}
	ctx := makeCtx(reader, replacer)

	out, err := tool.Handler(ctx, json.RawMessage(`{"mode":123}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	if _, ok := result["error"]; !ok {
		t.Error("expected error for numeric mode")
	}
}

func TestCompactHistoryTool_Core_NilContext(t *testing.T) {
	t.Parallel()
	// Passing a bare context without any values should produce errors, not panics.
	tool := CompactHistoryTool(nil)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"mode":"strip"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	result := parseResult(t, out)
	if _, ok := result["error"]; !ok {
		t.Error("expected error when context has no reader or replacer")
	}
}

func TestContextStatusTool_Core_NilArgs(t *testing.T) {
	t.Parallel()
	// context_status ignores args, so nil should be fine with a valid reader.
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Role: "user", Content: "Hello"},
		},
	}
	ctx := makeCtx(reader, nil)
	out, err := tool.Handler(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
	if result["message_count"].(float64) != 1 {
		t.Errorf("expected 1 message, got %v", result["message_count"])
	}
}

func TestContextStatusTool_Core_EmptyJSONArgs(t *testing.T) {
	t.Parallel()
	// Passing empty JSON object args should also work.
	tool := ContextStatusTool()
	reader := testTranscriptReader{
		messages: []tools.TranscriptMessage{
			{Role: "user", Content: "Hello"},
		},
	}
	ctx := makeCtx(reader, nil)
	out, err := tool.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := parseResult(t, out)
	if result["message_count"].(float64) != 1 {
		t.Errorf("expected 1 message, got %v", result["message_count"])
	}
}
