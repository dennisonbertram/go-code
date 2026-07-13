package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider"
	"go-agent-harness/internal/provider/pricing"
)

// --- Helper ---

func newTestClient(t *testing.T, srv *httptest.Server, extra ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-6",
	}
	for _, fn := range extra {
		fn(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// --- NewClient validation ---

func TestNewClientRequiresAPIKey(t *testing.T) {
	t.Parallel()
	_, err := NewClient(Config{})
	if err == nil {
		t.Fatal("expected error for missing api key")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestNewClientDefaultsModel(t *testing.T) {
	t.Parallel()
	c, err := NewClient(Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.model != "claude-sonnet-4-6" {
		t.Fatalf("expected default model, got %q", c.model)
	}
}

func TestNewClientDefaultsBaseURL(t *testing.T) {
	t.Parallel()
	c, err := NewClient(Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.baseURL != defaultBaseURL {
		t.Fatalf("expected default base URL, got %q", c.baseURL)
	}
}

func TestNewClientDefaultsProviderName(t *testing.T) {
	t.Parallel()
	c, err := NewClient(Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.providerName != "anthropic" {
		t.Fatalf("expected provider name 'anthropic', got %q", c.providerName)
	}
}

// --- TestCompleteTextResponse ---

func TestCompleteTextResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Hello, world!"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Content != "Hello, world!" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
	if result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.UsageStatus != harness.UsageStatusProviderReported {
		t.Fatalf("unexpected usage status: %q", result.UsageStatus)
	}
}

// --- TestCompleteToolCallResponse ---

func TestCompleteToolCallResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_02",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "tool_use", "id": "toolu_01", "name": "list_files", "input": {"path": "."}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 20, "output_tokens": 10}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "List files"}},
		Tools: []harness.ToolDefinition{{
			Name:        "list_files",
			Description: "List files in directory",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "toolu_01" {
		t.Fatalf("unexpected tool call ID: %q", tc.ID)
	}
	if tc.Name != "list_files" {
		t.Fatalf("unexpected tool call name: %q", tc.Name)
	}
	// Arguments should be JSON with path field
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["path"] != "." {
		t.Fatalf("unexpected path arg: %v", args["path"])
	}
}

// --- TestCompleteMessageConversion ---

func TestCompleteMessageConversion(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_03",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there", ToolCalls: []harness.ToolCall{
				{ID: "tc1", Name: "mytool", Arguments: `{"x": 1}`},
			}},
			{Role: "tool", ToolCallID: "tc1", Content: "result"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var req messageRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}

	// System should be extracted to top-level field
	if req.System != "You are helpful" {
		t.Fatalf("unexpected system: %q", req.System)
	}

	// Should have 3 messages: user, assistant, user (tool result merged)
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(req.Messages), req.Messages)
	}

	// First message: user
	if req.Messages[0].Role != "user" {
		t.Fatalf("expected user role, got %q", req.Messages[0].Role)
	}

	// Second message: assistant with text + tool_use
	if req.Messages[1].Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", req.Messages[1].Role)
	}

	// Third message: user with tool_result
	if req.Messages[2].Role != "user" {
		t.Fatalf("expected user role for tool result, got %q", req.Messages[2].Role)
	}

	// Verify third message contains tool_result block
	var blocks []contentBlock
	if err := json.Unmarshal(req.Messages[2].Content, &blocks); err != nil {
		t.Fatalf("unmarshal tool result content: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != "tool_result" {
		t.Fatalf("expected tool_result block, got %+v", blocks)
	}
	if blocks[0].ToolUseID != "tc1" {
		t.Fatalf("unexpected tool_use_id: %q", blocks[0].ToolUseID)
	}
	if blocks[0].Content != "result" {
		t.Fatalf("unexpected content: %q", blocks[0].Content)
	}
}

// --- TestCompleteToolDefinitionConversion ---

func TestCompleteToolDefinitionConversion(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_04",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Tools: []harness.ToolDefinition{
			{
				Name:        "search",
				Description: "Search for things",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var req messageRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	tool := req.Tools[0]
	if tool.Name != "search" {
		t.Fatalf("unexpected tool name: %q", tool.Name)
	}
	if tool.Description != "Search for things" {
		t.Fatalf("unexpected tool description: %q", tool.Description)
	}
	if tool.InputSchema == nil {
		t.Fatal("expected input_schema")
	}
	if tool.InputSchema["type"] != "object" {
		t.Fatalf("unexpected input_schema type: %v", tool.InputSchema["type"])
	}
}

// --- TestCompleteErrorResponse ---

func TestCompleteErrorResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid api key"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got: %v", err)
	}
}

// --- TestCompleteStreaming ---

func TestCompleteStreaming(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify stream request header
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("expected stream=true in request")
		}

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_05","type":"message","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(d harness.CompletionDelta) {
			deltas = append(deltas, d)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if result.Content != "Hello" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
	if result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}

	var contentParts []string
	for _, d := range deltas {
		if d.Content != "" {
			contentParts = append(contentParts, d.Content)
		}
	}
	if !slices.Equal(contentParts, []string{"Hel", "lo"}) {
		t.Fatalf("unexpected content deltas: %+v", contentParts)
	}
}

// --- TestCompleteStreamingToolCall ---

func TestCompleteStreamingToolCall(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_06","type":"message","role":"assistant","content":[],"usage":{"input_tokens":15,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"list_files","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"/\"}"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "List files"}},
		Stream: func(d harness.CompletionDelta) {
			deltas = append(deltas, d)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "toolu_01" {
		t.Fatalf("unexpected tool ID: %q", tc.ID)
	}
	if tc.Name != "list_files" {
		t.Fatalf("unexpected tool name: %q", tc.Name)
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["path"] != "/" {
		t.Fatalf("unexpected path: %v", args["path"])
	}

	// Verify tool call deltas were emitted
	var argParts []string
	for _, d := range deltas {
		if d.ToolCall.Arguments != "" {
			argParts = append(argParts, d.ToolCall.Arguments)
		}
	}
	if !slices.Equal(argParts, []string{`{"path":`, `"/"}` }) {
		t.Fatalf("unexpected tool arg deltas: %+v", argParts)
	}
}

// --- TestCompleteHTTPHeaders ---

func TestCompleteHTTPHeaders(t *testing.T) {
	t.Parallel()

	var (
		gotAPIKey  string
		gotVersion string
		gotCT      string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_07",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotAPIKey != "test-key" {
		t.Fatalf("unexpected x-api-key: %q", gotAPIKey)
	}
	if gotVersion != anthropicVersion {
		t.Fatalf("unexpected anthropic-version: %q", gotVersion)
	}
	if gotCT != "application/json" {
		t.Fatalf("unexpected content-type: %q", gotCT)
	}
}

// --- TestCompleteNoTools ---

func TestCompleteNoTools(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_08",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// When no tools, "tools" should be omitted from request
	if strings.Contains(string(capturedBody), `"tools"`) {
		t.Fatalf("expected tools to be omitted from request, got: %s", capturedBody)
	}
}

// --- TestCompleteMissingUsage ---

func TestCompleteMissingUsage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_09",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn"
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.UsageStatus != harness.UsageStatusProviderUnreported {
		t.Fatalf("unexpected usage status: %q", result.UsageStatus)
	}
	if result.CostStatus != harness.CostStatusProviderUnreported {
		t.Fatalf("unexpected cost status: %q", result.CostStatus)
	}
}

// --- TestCompleteCostComputation ---

func TestCompleteCostComputation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_10",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1000000, "output_tokens": 1000000}
		}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "claude-sonnet-4-6",
		PricingResolver: pricing.NewResolverFromCatalog(&pricing.Catalog{
			PricingVersion: "vtest",
			Providers: map[string]pricing.ProviderCatalog{
				"anthropic": {
					Models: map[string]pricing.Rates{
						"claude-sonnet-4-6": {
							InputPer1MTokensUSD:  3.00,
							OutputPer1MTokensUSD: 15.00,
						},
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if result.CostStatus != harness.CostStatusAvailable {
		t.Fatalf("unexpected cost status: %q", result.CostStatus)
	}
	// 1M input at $3 + 1M output at $15 = $18
	if result.CostUSD == nil || *result.CostUSD != 18.0 {
		t.Fatalf("unexpected cost: %v", result.CostUSD)
	}
	if result.Cost == nil || result.Cost.PricingVersion != "vtest" {
		t.Fatalf("unexpected cost object: %+v", result.Cost)
	}
}

// --- TestCompleteConsecutiveToolResults ---

func TestCompleteConsecutiveToolResults(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_11",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "done"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{
			{Role: "user", Content: "Run tools"},
			{
				Role: "assistant",
				ToolCalls: []harness.ToolCall{
					{ID: "tc1", Name: "tool1", Arguments: `{}`},
					{ID: "tc2", Name: "tool2", Arguments: `{}`},
				},
			},
			{Role: "tool", ToolCallID: "tc1", Content: "result1"},
			{Role: "tool", ToolCallID: "tc2", Content: "result2"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var req messageRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should be: user, assistant, user (merged tool results)
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages (consecutive tool results merged), got %d", len(req.Messages))
	}
	// Last message should have 2 tool_result blocks
	var blocks []contentBlock
	if err := json.Unmarshal(req.Messages[2].Content, &blocks); err != nil {
		t.Fatalf("unmarshal last message: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 tool_result blocks merged, got %d", len(blocks))
	}
	if blocks[0].ToolUseID != "tc1" || blocks[1].ToolUseID != "tc2" {
		t.Fatalf("unexpected tool_use_ids: %q, %q", blocks[0].ToolUseID, blocks[1].ToolUseID)
	}
}

// --- TestCompleteStreamingError ---

func TestCompleteStreamingError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(_ harness.CompletionDelta) {},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 in error, got: %v", err)
	}
}

// --- TestCompleteMaxTokensInRequest ---

func TestCompleteMaxTokensInRequest(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_12",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var req messageRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.MaxTokens != defaultMaxTokens {
		t.Fatalf("expected max_tokens=%d, got %d", defaultMaxTokens, req.MaxTokens)
	}
}

// --- TestExtractSystem ---

func TestExtractSystem(t *testing.T) {
	t.Parallel()

	msgs := []harness.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
	}
	system, remaining := extractSystem(msgs)
	if system != "You are helpful" {
		t.Fatalf("unexpected system: %q", system)
	}
	if len(remaining) != 1 || remaining[0].Role != "user" {
		t.Fatalf("unexpected remaining: %+v", remaining)
	}
}

func TestExtractSystemMultiple(t *testing.T) {
	t.Parallel()

	msgs := []harness.Message{
		{Role: "system", Content: "Part 1"},
		{Role: "system", Content: "Part 2"},
		{Role: "user", Content: "Hello"},
	}
	system, remaining := extractSystem(msgs)
	if system != "Part 1\nPart 2" {
		t.Fatalf("unexpected system: %q", system)
	}
	if len(remaining) != 1 {
		t.Fatalf("unexpected remaining: %+v", remaining)
	}
}

// --- TestMapMessages ---

func TestMapMessagesUserOnly(t *testing.T) {
	t.Parallel()

	msgs := []harness.Message{
		{Role: "user", Content: "Hello"},
	}
	out, err := mapMessages(msgs)
	if err != nil {
		t.Fatalf("mapMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].Role != "user" {
		t.Fatalf("unexpected role: %q", out[0].Role)
	}
}

func TestMapMessagesAssistantWithToolCall(t *testing.T) {
	t.Parallel()

	msgs := []harness.Message{
		{
			Role: "assistant",
			ToolCalls: []harness.ToolCall{
				{ID: "tc1", Name: "mytool", Arguments: `{"x": 1}`},
			},
		},
	}
	out, err := mapMessages(msgs)
	if err != nil {
		t.Fatalf("mapMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}

	var blocks []contentBlock
	if err := json.Unmarshal(out[0].Content, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != "tool_use" {
		t.Fatalf("expected tool_use block, got %+v", blocks)
	}
	if blocks[0].ID != "tc1" || blocks[0].Name != "mytool" {
		t.Fatalf("unexpected block: %+v", blocks[0])
	}
}

// --- Retry ---

func testRetryConfig() *provider.RetryConfig {
	return &provider.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		MaxTotal:    100 * time.Millisecond,
		Jitter:      false,
	}
}

func minimalMessageResponse() []byte {
	return []byte(`{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "ok"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)
}

func TestClientRetriesOn429(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalMessageResponse())
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(c *Config) {
		c.Retry = testRetryConfig()
	})

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestClientRetriesOn503(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"unavailable"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalMessageResponse())
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(c *Config) {
		c.Retry = testRetryConfig()
	})

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestClientDoesNotRetryOn400(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(c *Config) {
		c.Retry = testRetryConfig()
	})

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 in error, got: %v", err)
	}
}

func TestClientContextCancellationAbortsRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			cancel()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(c *Config) {
		c.Retry = &provider.RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   10 * time.Second,
			MaxDelay:    10 * time.Second,
			MaxTotal:    10 * time.Second,
			Jitter:      false,
		}
	})

	_, err := client.Complete(ctx, harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}
