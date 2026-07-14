package anthropic

import (
	"context"
	"encoding/json"
	"errors"
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

// --- NewClient default HTTP client transport (BUG 1: whole-request timeout) ---

// TestNewClientDefaultHTTPClientHasNoWholeRequestTimeout verifies that the
// client constructed when Config.Client is nil does NOT set http.Client.Timeout,
// which bounds the entire exchange (including streaming body reads). A 90s
// whole-request timeout force-closes long-running SSE streams mid-generation.
// Instead, only per-phase timeouts (dial, TLS handshake, response headers,
// expect-continue) should be set via a custom Transport, leaving overall
// cancellation to the request's context.
func TestNewClientDefaultHTTPClientHasNoWholeRequestTimeout(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if c.client.Timeout != 0 {
		t.Fatalf("expected no whole-request Client.Timeout (bounds entire exchange incl. streaming body), got %v", c.client.Timeout)
	}

	transport, ok := c.client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected *http.Transport with per-phase timeouts, got %T", c.client.Transport)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("expected TLSHandshakeTimeout=10s, got %v", transport.TLSHandshakeTimeout)
	}
	// See the identical comment in the openai package's equivalent test:
	// ResponseHeaderTimeout for a non-streaming completion is, in practice,
	// a cap on total generation time and must be raised well above the 90s
	// whole-request timeout BUG1 removed (adversarial review caught the
	// original 60s value as a strict regression, compounded by BUG2a
	// raising Anthropic max_tokens up to 4-8x).
	if transport.ResponseHeaderTimeout != nonStreamingHeaderTimeout {
		t.Fatalf("expected ResponseHeaderTimeout=%v, got %v", nonStreamingHeaderTimeout, transport.ResponseHeaderTimeout)
	}
	if transport.ExpectContinueTimeout != 1*time.Second {
		t.Fatalf("expected ExpectContinueTimeout=1s, got %v", transport.ExpectContinueTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be set with a bounded dial timeout")
	}
}

// TestDefaultHTTPClientPreservesHTTP2AndConnectionPooling is a MUST-FIX3
// regression guard: building the Transport from zero values (rather than
// cloning http.DefaultTransport) silently disables HTTP/2 — a custom
// DialContext suppresses Go's automatic HTTP/2 upgrade unless
// ForceAttemptHTTP2 is explicitly set — and disables connection pooling
// (MaxIdleConns/IdleConnTimeout default to 0/unset on a zero-value
// Transport, and MaxIdleConnsPerHost then defaults to 2).
func TestDefaultHTTPClientPreservesHTTP2AndConnectionPooling(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	transport, ok := c.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.client.Transport)
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("expected ForceAttemptHTTP2=true (lost when building Transport from zero values instead of cloning http.DefaultTransport)")
	}
	defaultTransport := http.DefaultTransport.(*http.Transport)
	if transport.MaxIdleConns != defaultTransport.MaxIdleConns {
		t.Fatalf("expected MaxIdleConns=%d (from http.DefaultTransport), got %d", defaultTransport.MaxIdleConns, transport.MaxIdleConns)
	}
	if transport.IdleConnTimeout != defaultTransport.IdleConnTimeout {
		t.Fatalf("expected IdleConnTimeout=%v (from http.DefaultTransport), got %v", defaultTransport.IdleConnTimeout, transport.IdleConnTimeout)
	}
}

// TestDefaultHTTPClientNegotiatesHTTP2 proves end-to-end (not just field
// inspection) that the default client can still negotiate HTTP/2 against a
// server that supports it. See the identical test in the openai package for
// the full rationale (concurrent-agent connection pooling impact).
func TestDefaultHTTPClientNegotiatesHTTP2(t *testing.T) {
	t.Parallel()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	c, err := NewClient(Config{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	transport, ok := c.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.client.Transport)
	}
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig

	httpReq, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	res, err := c.client.Do(httpReq)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer res.Body.Close()

	if res.Proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2.0, got %q — Transport was likely built from zero values without ForceAttemptHTTP2", res.Proto)
	}
}

// TestNewClientRespectsConfigClientOverride is a regression guard: callers
// that pass an explicit Config.Client must have it used verbatim, not
// replaced by the default transport-timeout client.
func TestNewClientRespectsConfigClientOverride(t *testing.T) {
	t.Parallel()

	custom := &http.Client{Timeout: 5 * time.Second}
	c, err := NewClient(Config{APIKey: "test-key", Client: custom})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.client != custom {
		t.Fatalf("expected Config.Client override to be used verbatim, got a different client")
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

// --- FinishReason normalization (BUG2b follow-up) ---

// TestCompleteNonStreamingSurfacesMaxTokensAsLengthFinishReason is a BUG2b
// follow-up test: a non-streaming response with stop_reason: "max_tokens"
// (Anthropic's truncation signal) must surface as the SAME normalized value
// OpenAI's finish_reason: "length" maps to (harness.FinishReasonLength),
// rather than leaking a provider-specific vocabulary.
func TestCompleteNonStreamingSurfacesMaxTokensAsLengthFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_trunc",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "partial answer that got cut off"}],
			"stop_reason": "max_tokens",
			"usage": {"input_tokens": 10, "output_tokens": 16384}
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
	if result.FinishReason != harness.FinishReasonLength {
		t.Fatalf("expected FinishReasonLength (normalized from anthropic's max_tokens), got %q", result.FinishReason)
	}
}

// TestCompleteNonStreamingSurfacesEndTurnAsStopFinishReason verifies a normal
// completion surfaces the normalized "stop" value rather than leaving
// FinishReason empty by accident.
func TestCompleteNonStreamingSurfacesEndTurnAsStopFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_ok",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "a complete answer"}],
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
	if result.FinishReason != harness.FinishReasonStop {
		t.Fatalf("expected FinishReasonStop, got %q", result.FinishReason)
	}
}

// TestCompleteStreamingSurfacesMaxTokensAsLengthFinishReason is the
// streaming counterpart: Anthropic reports stop_reason on the
// message_delta event.
func TestCompleteStreamingSurfacesMaxTokensAsLengthFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":16384}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.FinishReason != harness.FinishReasonLength {
		t.Fatalf("expected FinishReasonLength (normalized from anthropic's max_tokens), got %q", result.FinishReason)
	}
}

// TestCompleteStreamingSurfacesEndTurnAsStopFinishReason is the streaming
// counterpart of the normal-completion case.
func TestCompleteStreamingSurfacesEndTurnAsStopFinishReason(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
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
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.FinishReason != harness.FinishReasonStop {
		t.Fatalf("expected FinishReasonStop, got %q", result.FinishReason)
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

// withShrunkIdleStreamTimeout temporarily overrides the package-level
// idleStreamTimeout var for the duration of a test. Callers MUST NOT mark
// the test t.Parallel(): idleStreamTimeout is process-wide, shared, and
// mutated without synchronization here — this is safe only because
// non-parallel tests in this package run strictly before the batch of
// t.Parallel() tests is allowed to start (Go's testing package pauses every
// parallel test at its Parallel() call until all non-parallel tests in the
// same run have completed), so there is no concurrent access.
func withShrunkIdleStreamTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	original := idleStreamTimeout
	idleStreamTimeout = d
	t.Cleanup(func() { idleStreamTimeout = original })
}

// TestCompleteStreamStallTimesOutWithoutHanging is the BUG1 follow-up's
// primary red/green test for anthropic: a server that sends headers and one
// content delta, then goes completely silent (no more bytes, connection
// stays open) must fail with a typed error within the idle interval instead
// of hanging forever.
//
// NOT t.Parallel(): mutates the shared idleStreamTimeout package var.
func TestCompleteStreamStallTimesOutWithoutHanging(t *testing.T) {
	withShrunkIdleStreamTimeout(t, 60*time.Millisecond)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
			``,
		}, "\n")+"\n")
		flusher.Flush()

		// Go silent forever (from the client's perspective) — bounded by a
		// safety cap so the test server can always Close() promptly even if
		// the fix regresses.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	start := time.Now()
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a stall error, got success")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("expected the idle-stream watchdog to fail fast (~60ms), took %s — looks like it hung instead", elapsed)
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if !strings.Contains(phe.Body, "stall") {
		t.Fatalf("expected error body to mention the stall, got %q", phe.Body)
	}
}

// TestCompleteNonStreamingStallTimesOutWithoutHanging is MUST-FIX1's most
// important test for this package: adversarial review proved the idle-stream
// watchdog only covered the streaming decode path, leaving
// io.ReadAll(httpRes.Body) on the NON-STREAMING path completely unbounded
// once BUG1 removed the whole-request Client.Timeout. A server that sends
// 200 + headers + a partial body then stalls must still fail within the
// idle interval when req.Stream == nil, not hang. This matters more than
// the streaming case because the one production non-streaming caller
// (auto-compaction summarizer) reaches the provider via
// context.Background(), so an unbounded non-streaming hang would not even
// be cancellable.
//
// NOT t.Parallel(): mutates the shared idleStreamTimeout package var.
func TestCompleteNonStreamingStallTimesOutWithoutHanging(t *testing.T) {
	withShrunkIdleStreamTimeout(t, 60*time.Millisecond)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Partial, deliberately-incomplete JSON body — proves the server did
		// respond and started sending a body, then goes silent forever.
		_, _ = io.WriteString(w, `{"id":"msg_stall","type":"message","role":"assistant","content":[{"type":"text","text":"partial`)
		flusher.Flush()

		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	start := time.Now()
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		// Stream is deliberately nil: exercises the NON-STREAMING path.
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a stall error, got success")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("expected the idle-stream watchdog to bound the non-streaming read (~60ms), took %s — non-streaming reads are unbounded without MUST-FIX1", elapsed)
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if !strings.Contains(phe.Body, "stall") {
		t.Fatalf("expected error body to mention the stall, got %q", phe.Body)
	}
}

// TestCompleteStreamSlowButContinuousSurvivesLongerThanIdleTimeout is the
// regression guard proving the idle timeout is gap-based, not a disguised
// total-duration cap: the server never goes silent for longer than
// idleStreamTimeout between chunks, but the overall stream runs well past
// idleStreamTimeout in total.
//
// NOT t.Parallel(): mutates the shared idleStreamTimeout package var.
func TestCompleteStreamSlowButContinuousSurvivesLongerThanIdleTimeout(t *testing.T) {
	// Idle timeout is widened to 500ms against a 20ms send interval (25x
	// slack) rather than the original 50ms/20ms (2.5x slack). See the
	// identical comment in the openai package's equivalent test.
	withShrunkIdleStreamTimeout(t, 500*time.Millisecond)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		write := func(lines ...string) {
			_, _ = io.WriteString(w, strings.Join(lines, "\n")+"\n\n")
			flusher.Flush()
		}
		write(`event: content_block_start`, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		// 3 content deltas, 20ms apart (well under the 500ms idle timeout
		// per gap), for a total stream duration of ~80ms — the stream must
		// still succeed.
		chunks := []string{"o", "n", "e"}
		for _, c := range chunks {
			time.Sleep(20 * time.Millisecond)
			write(`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+c+`"}}`)
		}
		time.Sleep(20 * time.Millisecond)
		write(`event: message_delta`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)
		write(`event: message_stop`, `data: {"type":"message_stop"}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v (a continuously-producing stream must not be killed by the idle timeout just because its TOTAL duration exceeds the idle interval)", err)
	}
	if result.Content != "one" {
		t.Fatalf("unexpected content: %q", result.Content)
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
