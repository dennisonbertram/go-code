package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
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
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("expected ResponseHeaderTimeout=60s, got %v", transport.ResponseHeaderTimeout)
	}
	if transport.ExpectContinueTimeout != 1*time.Second {
		t.Fatalf("expected ExpectContinueTimeout=1s, got %v", transport.ExpectContinueTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be set with a bounded dial timeout")
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

// TestNewClientStreamingSurvivesSlowBodyAfterFastHeaders is a BUG1
// regression guard distinct from the Timeout-field assertion above: it
// exercises actual traffic through the default client against a server that
// answers headers immediately but drip-feeds the SSE body with delays
// between chunks. If a future change reintroduces any whole-request bound
// (via Client.Timeout or a Transport field that inadvertently caps body
// reads, e.g. misusing ResponseHeaderTimeout to cover the whole response),
// this test's flush-delayed multi-chunk stream would fail to complete.
func TestNewClientStreamingSurvivesSlowBodyAfterFastHeaders(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		chunks := []string{"Hel", "lo", ", ", "world"}
		for _, c := range chunks {
			time.Sleep(30 * time.Millisecond)
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"`+c+`"}}]}`+"\n\n")
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer testServer.Close()

	// No Config.Client override — exercises the real default transport built
	// by defaultHTTPClient().
	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Content != "Hello, world" {
		t.Fatalf("unexpected content: %q", result.Content)
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

// TestClientCompleteStreamStallTimesOutWithoutHanging is the BUG1
// follow-up's primary red/green test: a server that sends headers and a
// couple of chunks, then goes completely silent (no more bytes, connection
// stays open) must fail with a typed error within the idle interval instead
// of hanging forever. This is the exact hole BUG1's fix opened: removing
// Client.Timeout means nothing else would ever abort a post-headers stall
// without this idle watchdog.
//
// NOT t.Parallel(): mutates the shared idleStreamTimeout package var.
func TestClientCompleteStreamStallTimesOutWithoutHanging(t *testing.T) {
	withShrunkIdleStreamTimeout(t, 60*time.Millisecond)

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hel"}}]}`+"\n\n")
		flusher.Flush()

		// Go silent forever (from the client's perspective) — but bound the
		// handler goroutine with a safety cap and release early if the
		// client aborts the connection, so the test server can always Close()
		// promptly instead of hanging the test process on failure.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	start := time.Now()
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
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

// TestClientCompleteStreamSlowButContinuousSurvivesLongerThanIdleTimeout is
// the regression guard proving the idle timeout is gap-based, not a
// disguised total-duration cap: the server never goes silent for longer than
// idleStreamTimeout between chunks, but the overall stream runs well past
// idleStreamTimeout in total. If a future change replaced the per-chunk
// timer reset with anything resembling a fixed deadline from stream start
// (i.e. reintroducing BUG1 in a new disguise), this test would start
// failing even though no individual gap ever exceeded the idle interval.
//
// NOT t.Parallel(): mutates the shared idleStreamTimeout package var.
func TestClientCompleteStreamSlowButContinuousSurvivesLongerThanIdleTimeout(t *testing.T) {
	withShrunkIdleStreamTimeout(t, 50*time.Millisecond)

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// 6 chunks, 20ms apart (well under the 50ms idle timeout per gap),
		// for a total stream duration of ~120ms — more than double the idle
		// timeout, but the stream must still succeed because it never goes
		// idle for longer than 50ms at a stretch.
		chunks := []string{"o", "n", "e", " ", "t", "wo"}
		for _, c := range chunks {
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"`+c+`"}}]}`+"\n\n")
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("complete: %v (a continuously-producing stream must not be killed by the idle timeout just because its TOTAL duration exceeds the idle interval)", err)
	}
	if result.Content != "one two" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestClientCompleteParsesToolCalls(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"tool_choice":"auto"`) {
			t.Fatalf("expected tool_choice in request body: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[
				{
					"message":{
						"content":"",
						"tool_calls":[
							{"id":"call-1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}
						]
					}
				}
			],
			"usage":{
				"prompt_tokens":120,
				"completion_tokens":30,
				"total_tokens":150,
				"prompt_tokens_details":{"cached_tokens":20,"audio_tokens":0},
				"completion_tokens_details":{"reasoning_tokens":12,"audio_tokens":2}
			}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Model:   "gpt-4.1-mini",
		PricingResolver: pricing.NewResolverFromCatalog(&pricing.Catalog{
			PricingVersion: "vtest",
			Providers: map[string]pricing.ProviderCatalog{
				"openai": {
					Models: map[string]pricing.Rates{
						"gpt-4.1-mini": {
							InputPer1MTokensUSD:     1.00,
							OutputPer1MTokensUSD:    2.00,
							CacheReadPer1MTokensUSD: 0.50,
						},
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model: "gpt-4.1-mini",
		Messages: []harness.Message{
			{Role: "user", Content: "List files"},
		},
		Tools: []harness.ToolDefinition{{
			Name:        "list_files",
			Description: "List files",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "list_files" {
		t.Fatalf("unexpected tool call: %+v", result.ToolCalls[0])
	}
	if result.Usage == nil {
		t.Fatalf("expected usage")
	}
	if result.UsageStatus != harness.UsageStatusProviderReported {
		t.Fatalf("unexpected usage status: %q", result.UsageStatus)
	}
	if result.Usage.PromptTokens != 120 || result.Usage.CompletionTokens != 30 || result.Usage.TotalTokens != 150 {
		t.Fatalf("unexpected usage values: %+v", result.Usage)
	}
	if result.Usage.CachedPromptTokens == nil || *result.Usage.CachedPromptTokens != 20 {
		t.Fatalf("expected cached prompt tokens: %+v", result.Usage)
	}
	if result.Usage.ReasoningTokens == nil || *result.Usage.ReasoningTokens != 12 {
		t.Fatalf("expected reasoning tokens: %+v", result.Usage)
	}
	if result.CostStatus != harness.CostStatusAvailable {
		t.Fatalf("unexpected cost status: %q", result.CostStatus)
	}
	if result.Cost == nil || result.CostUSD == nil {
		t.Fatalf("expected cost values")
	}
	if result.Cost.PricingVersion != "vtest" {
		t.Fatalf("unexpected pricing version: %+v", result.Cost)
	}
	// expected: non-cached input (100)*1.0 + output (30)*2.0 + cache-read (20)*0.5 per 1M tokens.
	expected := (100.0/1_000_000.0)*1.0 + (30.0/1_000_000.0)*2.0 + (20.0/1_000_000.0)*0.5
	if math.Abs(*result.CostUSD-expected) > 1e-12 {
		t.Fatalf("unexpected total cost: got=%f want=%f", *result.CostUSD, expected)
	}
}

func TestClientCompleteFailsWithoutChoices(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientCompleteStreamsAssistantAndToolCallDeltas(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, `"stream":true`) {
			t.Fatalf("expected stream=true in request body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, `"include_usage":true`) {
			t.Fatalf("expected stream_options.include_usage=true in request body: %s", bodyStr)
		}

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
			``,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`,
			``,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"write","arguments":"{\"path\":\""}}]}}]}`,
			``,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"demo.txt\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if result.Content != "Hello" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "write" || result.ToolCalls[0].Arguments != `{"path":"demo.txt"}` {
		t.Fatalf("unexpected tool call: %+v", result.ToolCalls[0])
	}
	if result.Usage == nil || result.Usage.TotalTokens != 14 {
		t.Fatalf("expected streamed usage totals, got %+v", result.Usage)
	}

	var contentParts []string
	var toolArgParts []string
	for _, delta := range deltas {
		if delta.Content != "" {
			contentParts = append(contentParts, delta.Content)
		}
		if delta.ToolCall.Arguments != "" {
			toolArgParts = append(toolArgParts, delta.ToolCall.Arguments)
		}
	}
	if !slices.Equal(contentParts, []string{"Hel", "lo"}) {
		t.Fatalf("unexpected content deltas: %+v", contentParts)
	}
	if !slices.Equal(toolArgParts, []string{`{"path":"`, `demo.txt"}`}) {
		t.Fatalf("unexpected tool argument deltas: %+v", toolArgParts)
	}
}

// TestClientCompleteStreamMidStreamErrorReturnsTypedFailure is a BUG3 test:
// a mid-stream `{"error": {...}}` SSE payload was not recognized by the
// chunk decoder (completionChunk has no error field), so it was silently
// skipped — the stream then hit [DONE]-less EOF or ended cleanly and the
// caller received an EMPTY but SUCCESSFUL result instead of a failure. The
// fix must surface it as a typed *harness.ProviderHTTPError so provider
// fallback still triggers, matching the non-streaming error path.
func TestClientCompleteStreamMidStreamErrorReturnsTypedFailure(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
			``,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`,
			``,
			`data: {"error":{"message":"rate limit","type":"rate_limit_error","code":"429"}}`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err == nil {
		t.Fatalf("expected a non-nil error for a mid-stream error payload, got empty-success result: %+v", result)
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if phe.Provider != "openai" {
		t.Fatalf("expected provider %q, got %q", "openai", phe.Provider)
	}
	if !strings.Contains(phe.Body, "rate limit") {
		t.Fatalf("expected error body to mention the upstream error message, got %q", phe.Body)
	}
}

// TestClientCompleteStreamContentMentioningErrorIsNotMisdetected is a BUG3
// regression guard for false positives: the fix must only recognize a
// top-level JSON "error" object in a chunk, not merely the substring "error"
// appearing inside legitimate assistant content. A naive string-search based
// detector (instead of parsing chunk.Error as structured JSON) would
// misfire here and turn a normal, successful completion into a spurious
// failure.
func TestClientCompleteStreamContentMentioningErrorIsNotMisdetected(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"The plan has an "}}]}`,
			``,
			`data: {"choices":[{"delta":{"content":"\"error\" handling step."}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Content != `The plan has an "error" handling step.` {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestProcessStreamBlock_ReasoningContent(t *testing.T) {
	t.Parallel()

	raw := `data: {"choices":[{"delta":{"reasoning_content":"Let me think"}}]}`
	state := &streamedCompletionState{}
	var received []harness.CompletionDelta
	streamFn := func(delta harness.CompletionDelta) {
		received = append(received, delta)
	}

	done, err := processStreamBlock(raw, state, streamFn)
	if err != nil {
		t.Fatalf("processStreamBlock error: %v", err)
	}
	if done {
		t.Fatalf("expected done=false")
	}
	if state.reasoning.String() != "Let me think" {
		t.Fatalf("expected reasoning buffer = %q, got %q", "Let me think", state.reasoning.String())
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(received))
	}
	if received[0].Reasoning != "Let me think" {
		t.Fatalf("expected delta.Reasoning = %q, got %q", "Let me think", received[0].Reasoning)
	}
	if received[0].Content != "" {
		t.Fatalf("expected delta.Content to be empty, got %q", received[0].Content)
	}
}

func TestClientCompleteStreamsReasoningContent(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"reasoning_content":"Think"}}]}`,
			``,
			`data: {"choices":[{"delta":{"reasoning_content":"ing..."}}]}`,
			``,
			`data: {"choices":[{"delta":{"content":"Answer"}}]}`,
			``,
			`data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if result.Content != "Answer" {
		t.Fatalf("unexpected content: %q", result.Content)
	}

	var reasoningParts []string
	var contentParts []string
	for _, d := range deltas {
		if d.Reasoning != "" {
			reasoningParts = append(reasoningParts, d.Reasoning)
		}
		if d.Content != "" {
			contentParts = append(contentParts, d.Content)
		}
	}
	if !slices.Equal(reasoningParts, []string{"Think", "ing..."}) {
		t.Fatalf("unexpected reasoning deltas: %+v", reasoningParts)
	}
	if !slices.Equal(contentParts, []string{"Answer"}) {
		t.Fatalf("unexpected content deltas: %+v", contentParts)
	}
}

func TestClientCompleteMissingUsageReturnsProviderUnreported(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}]
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Usage == nil {
		t.Fatalf("expected usage object")
	}
	if result.Usage.PromptTokens != 0 || result.Usage.CompletionTokens != 0 || result.Usage.TotalTokens != 0 {
		t.Fatalf("expected zero usage, got %+v", result.Usage)
	}
	if result.UsageStatus != harness.UsageStatusProviderUnreported {
		t.Fatalf("unexpected usage status: %q", result.UsageStatus)
	}
	if result.CostStatus != harness.CostStatusProviderUnreported {
		t.Fatalf("unexpected cost status: %q", result.CostStatus)
	}
	if result.CostUSD == nil || *result.CostUSD != 0 {
		t.Fatalf("expected zero cost, got %+v", result.CostUSD)
	}
}

func TestClientCompleteUnpricedModelReturnsUnpricedStatus(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Model:   "gpt-unpriced",
		PricingResolver: pricing.NewResolverFromCatalog(&pricing.Catalog{
			PricingVersion: "v1",
			Providers: map[string]pricing.ProviderCatalog{
				"openai": {
					Models: map[string]pricing.Rates{
						"another-model": {
							InputPer1MTokensUSD:  1,
							OutputPer1MTokensUSD: 1,
						},
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.CostStatus != harness.CostStatusUnpricedModel {
		t.Fatalf("unexpected cost status: %q", result.CostStatus)
	}
	if result.CostUSD == nil || *result.CostUSD != 0 {
		t.Fatalf("expected zero cost, got %+v", result.CostUSD)
	}
}

func TestClientPassesReasoningEffortToProvider(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Messages:        []harness.Message{{Role: "user", Content: "Hello"}},
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if !strings.Contains(string(capturedBody), `"reasoning_effort":"high"`) {
		t.Fatalf("expected reasoning_effort in request body, got: %s", string(capturedBody))
	}
}

func TestClientOmitsReasoningEffortWhenEmpty(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: testServer.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if strings.Contains(string(capturedBody), `"reasoning_effort"`) {
		t.Fatalf("expected reasoning_effort to be absent from request body, got: %s", string(capturedBody))
	}
}

// ── Responses API tests ─────────────────────────────────────────────────────

// newResponsesClient creates a test client with ModelAPILookup configured to
// route "gpt-5.1-codex-mini" to the Responses API.
func newResponsesClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      baseURL,
		Model:        "gpt-5.1-codex-mini",
		ProviderName: "openai",
		ModelAPILookup: func(provider, model string) string {
			if provider == "openai" && model == "gpt-5.1-codex-mini" {
				return "responses"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// newResponsesClientWithRetry is like newResponsesClient but uses a fast
// bounded retry config, for tests that intentionally exercise the
// non-2xx/retry path against a responses-routed model.
func newResponsesClientWithRetry(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      baseURL,
		Model:        "gpt-5.1-codex-mini",
		ProviderName: "openai",
		ModelAPILookup: func(provider, model string) string {
			if provider == "openai" && model == "gpt-5.1-codex-mini" {
				return "responses"
			}
			return ""
		},
		Retry: testRetryConfig(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// TestResponsesAPIRoutingFlag verifies that usesResponsesAPI returns true/false correctly.
func TestResponsesAPIRoutingFlag(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      "https://api.openai.com",
		ProviderName: "openai",
		ModelAPILookup: func(provider, model string) string {
			if provider == "openai" && model == "gpt-5.1-codex-mini" {
				return "responses"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if !client.usesResponsesAPI("gpt-5.1-codex-mini") {
		t.Fatal("expected gpt-5.1-codex-mini to use Responses API")
	}
	if client.usesResponsesAPI("gpt-4.1-mini") {
		t.Fatal("expected gpt-4.1-mini to NOT use Responses API")
	}
	if client.usesResponsesAPI("") {
		t.Fatal("expected empty model to NOT use Responses API")
	}
}

// TestResponsesAPIRoutingFlagNilLookup verifies that usesResponsesAPI returns false when no lookup is configured.
func TestResponsesAPIRoutingFlagNilLookup(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: "https://api.openai.com",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if client.usesResponsesAPI("gpt-5.1-codex-mini") {
		t.Fatal("expected false when no ModelAPILookup configured")
	}
}

// TestResponsesAPIExistingModelsUnaffected verifies that gpt-4.1-mini hits /v1/chat/completions.
func TestResponsesAPIExistingModelsUnaffected(t *testing.T) {
	t.Parallel()

	var requestPath string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"hi","tool_calls":[]}}],
			"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Model:   "gpt-4.1-mini",
		ModelAPILookup: func(provider, model string) string {
			if provider == "openai" && model == "gpt-5.1-codex-mini" {
				return "responses"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if requestPath != "/v1/chat/completions" {
		t.Fatalf("expected request to /v1/chat/completions, got %q", requestPath)
	}
}

// TestResponsesAPIRequestFormat verifies that system/user/assistant/tool messages are mapped correctly.
func TestResponsesAPIRequestFormat(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("expected /v1/responses, got %q", r.URL.Path)
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"output":[{"type":"message","content":[{"type":"output_text","text":"Hello!"}]}],
			"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}
		}`))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model: "gpt-5.1-codex-mini",
		Messages: []harness.Message{
			{Role: "system", Content: "You are an assistant."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []harness.ToolDefinition{{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	// System message → instructions field (not in input).
	instructions, ok := req["instructions"].(string)
	if !ok || instructions != "You are an assistant." {
		t.Fatalf("expected instructions=%q, got %v", "You are an assistant.", req["instructions"])
	}

	// Input should contain only the user message (no system entry).
	input, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("expected input array, got %T: %v", req["input"], req["input"])
	}
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d: %v", len(input), input)
	}
	userMsg := input[0].(map[string]any)
	if userMsg["type"] != "message" || userMsg["role"] != "user" {
		t.Fatalf("unexpected user message item: %v", userMsg)
	}
	if _, hasArguments := userMsg["arguments"]; hasArguments {
		t.Fatalf("message input items must not include arguments: %v", userMsg)
	}

	// Tool spec should be flat (no nested "function" wrapper).
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected tools array, got: %v", req["tools"])
	}
	toolItem := tools[0].(map[string]any)
	if toolItem["type"] != "function" {
		t.Fatalf("expected tool type=function, got %v", toolItem["type"])
	}
	if toolItem["name"] != "get_weather" {
		t.Fatalf("expected tool name=get_weather, got %v", toolItem["name"])
	}
	// Flat spec: "name" at top level, not nested under "function".
	if _, hasFunction := toolItem["function"]; hasFunction {
		t.Fatal("Responses API tool spec should NOT have nested 'function' wrapper")
	}
	// strict: false — our tool schemas don't satisfy the strict mode requirement
	// (all properties must be in required, additionalProperties must be false everywhere).
	// strict: false still enables tool calling without schema enforcement overhead.
	if toolItem["strict"] != false {
		t.Fatalf("expected strict=false, got %v", toolItem["strict"])
	}
}

// TestResponsesAPIToolCallMapping verifies that assistant messages with tool calls
// produce separate function_call input items.
func TestResponsesAPIToolCallMapping(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],
			"usage":{"input_tokens":30,"output_tokens":2,"total_tokens":32}
		}`))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model: "gpt-5.1-codex-mini",
		Messages: []harness.Message{
			{Role: "user", Content: "What's the weather?"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []harness.ToolCall{
					{ID: "call_abc", Name: "get_weather", Arguments: `{"location":"London"}`},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	input, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("expected input array, got %T", req["input"])
	}

	// Expected: user message + function_call item (no assistant text message since content is empty).
	if len(input) != 2 {
		t.Fatalf("expected 2 input items (user + function_call), got %d: %v", len(input), input)
	}

	userItem := input[0].(map[string]any)
	if userItem["role"] != "user" {
		t.Fatalf("first item should be user message, got: %v", userItem)
	}

	funcCallItem := input[1].(map[string]any)
	if funcCallItem["type"] != "function_call" {
		t.Fatalf("expected function_call item, got: %v", funcCallItem)
	}
	if funcCallItem["call_id"] != "call_abc" {
		t.Fatalf("expected call_id=call_abc, got %v", funcCallItem["call_id"])
	}
	if funcCallItem["name"] != "get_weather" {
		t.Fatalf("expected name=get_weather, got %v", funcCallItem["name"])
	}
	if funcCallItem["arguments"] != `{"location":"London"}` {
		t.Fatalf("expected arguments={\"location\":\"London\"}, got %v", funcCallItem["arguments"])
	}
}

// TestResponsesAPIMultiTurnToolResult verifies that tool messages map to function_call_output items.
func TestResponsesAPIMultiTurnToolResult(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"output":[{"type":"message","content":[{"type":"output_text","text":"72F and sunny."}]}],
			"usage":{"input_tokens":40,"output_tokens":4,"total_tokens":44}
		}`))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model: "gpt-5.1-codex-mini",
		Messages: []harness.Message{
			{Role: "user", Content: "What's the weather?"},
			{
				Role: "assistant",
				ToolCalls: []harness.ToolCall{
					{ID: "call_abc", Name: "get_weather", Arguments: `{"location":"London"}`},
				},
			},
			{Role: "tool", ToolCallID: "call_abc", Content: "72F, sunny"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	input, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("expected input array, got %T", req["input"])
	}

	// Expected: user message + function_call item + function_call_output item.
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d: %v", len(input), input)
	}

	toolResultItem := input[2].(map[string]any)
	if toolResultItem["type"] != "function_call_output" {
		t.Fatalf("expected function_call_output, got: %v", toolResultItem)
	}
	if toolResultItem["call_id"] != "call_abc" {
		t.Fatalf("expected call_id=call_abc, got %v", toolResultItem["call_id"])
	}
	if toolResultItem["output"] != "72F, sunny" {
		t.Fatalf("expected output=72F sunny, got %v", toolResultItem["output"])
	}
}

// TestResponsesAPIResponseParsing verifies that output[] items are correctly parsed
// into CompletionResult content and tool calls.
func TestResponsesAPIResponseParsing(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_abc",
			"output":[
				{
					"type":"message",
					"content":[{"type":"output_text","text":"The weather is nice."}]
				},
				{
					"type":"function_call",
					"call_id":"call_xyz",
					"name":"get_weather",
					"arguments":"{\"location\":\"Paris\"}"
				}
			],
			"usage":{
				"input_tokens":100,
				"output_tokens":50,
				"total_tokens":150,
				"input_tokens_details":{"cached_tokens":20},
				"output_tokens_details":{"reasoning_tokens":10}
			}
		}`))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Weather?"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if result.Content != "The weather is nice." {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_xyz" {
		t.Fatalf("unexpected tool call ID: %q", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Fatalf("unexpected tool call name: %q", tc.Name)
	}
	if tc.Arguments != `{"location":"Paris"}` {
		t.Fatalf("unexpected tool call arguments: %q", tc.Arguments)
	}

	// Verify usage: input_tokens → PromptTokens, output_tokens → CompletionTokens.
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
	if result.Usage.PromptTokens != 100 {
		t.Fatalf("expected PromptTokens=100, got %d", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 50 {
		t.Fatalf("expected CompletionTokens=50, got %d", result.Usage.CompletionTokens)
	}
	if result.Usage.TotalTokens != 150 {
		t.Fatalf("expected TotalTokens=150, got %d", result.Usage.TotalTokens)
	}
	if result.Usage.CachedPromptTokens == nil || *result.Usage.CachedPromptTokens != 20 {
		t.Fatalf("expected CachedPromptTokens=20, got %v", result.Usage.CachedPromptTokens)
	}
	if result.Usage.ReasoningTokens == nil || *result.Usage.ReasoningTokens != 10 {
		t.Fatalf("expected ReasoningTokens=10, got %v", result.Usage.ReasoningTokens)
	}
	if result.UsageStatus != harness.UsageStatusProviderReported {
		t.Fatalf("unexpected usage status: %q", result.UsageStatus)
	}
}

// TestResponsesAPIStreamingTextAndToolCalls verifies that the streaming parser correctly
// handles typed SSE events for text deltas and tool call argument deltas.
func TestResponsesAPIStreamingTextAndToolCalls(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected stream=true in request, got: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello"}`,
			``,
			`event: response.output_text.delta`,
			`data: {"item_id":"msg_1","output_index":0,"content_index":0,"delta":" world"}`,
			``,
			`event: response.function_call_arguments.delta`,
			`data: {"item_id":"fc_1","output_index":1,"call_id":"call_abc","delta":"{\"loc"}`,
			``,
			`event: response.function_call_arguments.delta`,
			`data: {"item_id":"fc_1","output_index":1,"call_id":"call_abc","delta":"ation\":\"London\"}"}`,
			``,
			`event: response.function_call_arguments.done`,
			`data: {"item_id":"fc_1","output_index":1,"call_id":"call_abc","name":"get_weather","arguments":"{\"location\":\"London\"}"}`,
			``,
			`event: response.completed`,
			`data: {"response":{"id":"resp_abc","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if result.Content != "Hello world" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_abc" {
		t.Fatalf("unexpected tool call ID: %q", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool call name: %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Arguments != `{"location":"London"}` {
		t.Fatalf("unexpected tool call arguments: %q", result.ToolCalls[0].Arguments)
	}

	if result.Usage == nil || result.Usage.TotalTokens != 15 {
		t.Fatalf("expected total tokens=15, got %+v", result.Usage)
	}

	// Verify content deltas were streamed.
	var contentDeltas []string
	var toolArgDeltas []string
	for _, d := range deltas {
		if d.Content != "" {
			contentDeltas = append(contentDeltas, d.Content)
		}
		if d.ToolCall.Arguments != "" {
			toolArgDeltas = append(toolArgDeltas, d.ToolCall.Arguments)
		}
	}
	if !slices.Equal(contentDeltas, []string{"Hello", " world"}) {
		t.Fatalf("unexpected content deltas: %v", contentDeltas)
	}
	if !slices.Equal(toolArgDeltas, []string{`{"loc`, `ation":"London"}`}) {
		t.Fatalf("unexpected tool arg deltas: %v", toolArgDeltas)
	}
}

// TestResponsesAPINonStreamingErrorReturnsTypedFailure is a BUG4 test: the
// non-streaming Responses API error branch returned a plain fmt.Errorf
// instead of the typed *harness.ProviderHTTPError the Chat Completions path
// returns. Callers type-assert on ProviderHTTPError to decide whether to
// fall back to another provider, so for responses-routed models on a
// transient upstream failure (e.g. 429), fallback never triggered.
func TestResponsesAPINonStreamingErrorReturnsTypedFailure(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer testServer.Close()

	client := newResponsesClientWithRetry(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if phe.Provider != "openai" {
		t.Fatalf("expected provider %q, got %q", "openai", phe.Provider)
	}
	if phe.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", phe.StatusCode)
	}
	if !strings.Contains(phe.Body, "rate limit exceeded") {
		t.Fatalf("expected body to contain upstream error message, got %q", phe.Body)
	}
}

// TestResponsesAPIStreamingErrorReturnsTypedFailure is the streaming
// counterpart of TestResponsesAPINonStreamingErrorReturnsTypedFailure:
// the Responses API streaming branch's non-2xx handling also returned a
// plain fmt.Errorf instead of *harness.ProviderHTTPError.
func TestResponsesAPIStreamingErrorReturnsTypedFailure(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected stream=true in request, got: %s", body)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer testServer.Close()

	client := newResponsesClientWithRetry(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if phe.Provider != "openai" {
		t.Fatalf("expected provider %q, got %q", "openai", phe.Provider)
	}
	if phe.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", phe.StatusCode)
	}
	if !strings.Contains(phe.Body, "rate limit exceeded") {
		t.Fatalf("expected body to contain upstream error message, got %q", phe.Body)
	}
}

// TestResponsesAPINonStreamingClientErrorReturnsTypedFailureWithoutRetry is a
// BUG4 regression guard covering a status code the earlier 429 tests do not:
// a 400 (client error, not fallback/retry-eligible) must still come back as
// a *harness.ProviderHTTPError with the exact status code preserved — this
// ensures the fix is a general replacement of the error construction, not a
// special case wired only for 429/5xx. It also confirms 400 is not retried
// (DoWithRetry only retries 429/500/502/503/504), so the fake server should
// see exactly one request.
func TestResponsesAPINonStreamingClientErrorReturnsTypedFailureWithoutRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid request: unknown model"}}`))
	}))
	defer testServer.Close()

	client := newResponsesClientWithRetry(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var phe *harness.ProviderHTTPError
	if !errors.As(err, &phe) {
		t.Fatalf("expected *harness.ProviderHTTPError, got %T: %v", err, err)
	}
	if phe.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", phe.StatusCode)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt (400 is not retry-eligible), got %d", got)
	}
}

func TestResponsesAPIStreamingToolCallArgumentsFromOutputItemDone(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected stream=true in request, got: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.output_item.done`,
			`data: {"item":{"type":"function_call","call_id":"call_write","name":"write","arguments":"{\"path\":\"index.html\",\"content\":\"hi\"}"}}`,
			``,
			`event: response.completed`,
			`data: {"response":{"id":"resp_abc","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Write a file"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_write" {
		t.Fatalf("unexpected tool call ID: %q", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "write" {
		t.Fatalf("unexpected tool call name: %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Arguments != `{"path":"index.html","content":"hi"}` {
		t.Fatalf("unexpected tool call arguments: %q", result.ToolCalls[0].Arguments)
	}
}

// TestResponsesAPIStreamingMissingCompleted verifies that an error is returned
// if the stream ends without a response.completed event.
func TestResponsesAPIStreamingMissingCompleted(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"delta":"partial"}`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client := newResponsesClient(t, testServer.URL)

	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-5.1-codex-mini",
		Messages: []harness.Message{{Role: "user", Content: "Hi"}},
		Stream:   func(harness.CompletionDelta) {},
	})
	if err == nil {
		t.Fatal("expected error for stream without response.completed")
	}
	if !strings.Contains(err.Error(), "response.completed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResponsesAPIUsageNormalization verifies that input_tokens maps to PromptTokens
// and output_tokens maps to CompletionTokens.
func TestResponsesAPIUsageNormalization(t *testing.T) {
	t.Parallel()

	usage, status := normalizeResponsesUsage(&responsesUsage{
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		InputTokensDetails:  &responsesInputDetails{CachedTokens: 25},
		OutputTokensDetails: &responsesOutputDetails{ReasoningTokens: 8},
	})

	if status != harness.UsageStatusProviderReported {
		t.Fatalf("unexpected status: %q", status)
	}
	if usage.PromptTokens != 100 {
		t.Fatalf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Fatalf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 150 {
		t.Fatalf("expected TotalTokens=150, got %d", usage.TotalTokens)
	}
	if usage.CachedPromptTokens == nil || *usage.CachedPromptTokens != 25 {
		t.Fatalf("expected CachedPromptTokens=25, got %v", usage.CachedPromptTokens)
	}
	if usage.ReasoningTokens == nil || *usage.ReasoningTokens != 8 {
		t.Fatalf("expected ReasoningTokens=8, got %v", usage.ReasoningTokens)
	}
}

// TestResponsesAPIFunctionCallArgumentsNeverOmitted is a regression test for the bug where
// Arguments had json:"arguments,omitempty", causing the field to be absent from the JSON
// when a tool call had empty arguments. The Responses API rejects such requests with
// "Missing required parameter: 'input[N].arguments'".
func TestResponsesAPIFunctionCallArgumentsNeverOmitted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		arguments string
	}{
		{"empty string", ""},
		{"empty object", "{}"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedBody []byte
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"id":"resp_test",
					"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],
					"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}
				}`))
			}))
			defer testServer.Close()

			client := newResponsesClient(t, testServer.URL)

			_, err := client.Complete(context.Background(), harness.CompletionRequest{
				Model: "gpt-5.1-codex-mini",
				Messages: []harness.Message{
					{Role: "user", Content: "list files"},
					{
						Role: "assistant",
						ToolCalls: []harness.ToolCall{
							{ID: "call_bash", Name: "bash", Arguments: tc.arguments},
						},
					},
				},
			})
			if err != nil {
				t.Fatalf("complete: %v", err)
			}

			var req map[string]any
			if err := json.Unmarshal(capturedBody, &req); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}

			input := req["input"].([]any)
			// input[0] = user message, input[1] = function_call item
			userItem := input[0].(map[string]any)
			if _, ok := userItem["arguments"]; ok {
				t.Fatalf("message item must not include arguments: %v", userItem)
			}

			funcCallItem := input[1].(map[string]any)
			if funcCallItem["type"] != "function_call" {
				t.Fatalf("expected function_call item, got: %v", funcCallItem)
			}

			// CRITICAL: arguments must always be present, even when empty.
			if _, ok := funcCallItem["arguments"]; !ok {
				t.Fatal("regression: arguments field missing from function_call item (omitempty bug)")
			}
			if funcCallItem["arguments"] != tc.arguments {
				t.Fatalf("expected arguments=%q, got %v", tc.arguments, funcCallItem["arguments"])
			}
		})
	}
}

// TestResponsesAPIUsageNormalizationNil verifies that nil usage returns ProviderUnreported.
func TestResponsesAPIUsageNormalizationNil(t *testing.T) {
	t.Parallel()

	usage, status := normalizeResponsesUsage(nil)
	if status != harness.UsageStatusProviderUnreported {
		t.Fatalf("expected ProviderUnreported, got %q", status)
	}
	if usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
		t.Fatalf("expected zero usage, got %+v", usage)
	}
}

// TestForceNonStreamingIgnoresStreamCallback verifies that when ForceNonStreaming is true,
// the HTTP request body has stream=false even when req.Stream is non-nil, and that the
// content is emitted via the stream callback as a single delta.
func TestForceNonStreamingIgnoresStreamCallback(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"hello from gemini","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:            "test-key",
		BaseURL:           testServer.URL,
		Model:             "gemini-2.0-flash",
		ForceNonStreaming: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gemini-2.0-flash",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// The HTTP request must NOT have stream=true.
	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if streamVal, ok := body["stream"]; ok && streamVal == true {
		t.Fatalf("expected stream to be false or absent in request body, got: %v", streamVal)
	}

	// stream_options must NOT be present (only valid for streaming requests).
	if _, ok := body["stream_options"]; ok {
		t.Fatalf("expected stream_options to be absent from non-streaming request body, got: %s", string(capturedBody))
	}

	// Result content must be correct.
	if result.Content != "hello from gemini" {
		t.Fatalf("unexpected content: %q", result.Content)
	}

	// Stream callback must have been called once with the full content.
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta from stream callback, got %d", len(deltas))
	}
	if deltas[0].Content != "hello from gemini" {
		t.Fatalf("unexpected delta content: %q", deltas[0].Content)
	}
}

// TestForceNonStreamingNoCallbackWhenEmptyContent verifies that when ForceNonStreaming is true
// and the response content is empty, the stream callback is NOT called.
func TestForceNonStreamingNoCallbackWhenEmptyContent(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"bash","arguments":"{}"}}]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:            "test-key",
		BaseURL:           testServer.URL,
		Model:             "gemini-2.0-flash",
		ForceNonStreaming: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var callbackCount int
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gemini-2.0-flash",
		Messages: []harness.Message{{Role: "user", Content: "run bash"}},
		Stream: func(delta harness.CompletionDelta) {
			callbackCount++
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// When content is empty (tool call only response), the stream callback must NOT be invoked.
	if callbackCount != 0 {
		t.Fatalf("expected stream callback to not be called for empty content, called %d times", callbackCount)
	}

	// Tool calls must still be populated.
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "bash" {
		t.Fatalf("unexpected tool call name: %q", result.ToolCalls[0].Name)
	}
}

// TestNoParallelToolsIncludedInRequest verifies that when NoParallelTools is true,
// the request body includes "parallel_tool_calls": false.
func TestNoParallelToolsIncludedInRequest(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:          "test-key",
		BaseURL:         testServer.URL,
		Model:           "gemini-2.0-flash",
		NoParallelTools: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gemini-2.0-flash",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
		Tools: []harness.ToolDefinition{{
			Name:        "some_tool",
			Description: "A tool",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Parse the captured request body to verify parallel_tool_calls is present and false.
	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	ptc, ok := body["parallel_tool_calls"]
	if !ok {
		t.Fatalf("expected parallel_tool_calls in request body, got: %s", string(capturedBody))
	}
	if ptc != false {
		t.Fatalf("expected parallel_tool_calls to be false, got: %v", ptc)
	}
}

// TestNoParallelToolsOmittedByDefault verifies that when NoParallelTools is false (default),
// parallel_tool_calls is NOT included in the request body.
func TestNoParallelToolsOmittedByDefault(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"ok","tool_calls":[]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer testServer.Close()

	// NoParallelTools is false (default zero value)
	client, err := NewClient(Config{
		APIKey:   "test-key",
		BaseURL:  testServer.URL,
		Model:    "gpt-4.1-mini",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1-mini",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
		Tools: []harness.ToolDefinition{{
			Name:        "some_tool",
			Description: "A tool",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// parallel_tool_calls must NOT appear in the request when NoParallelTools is false.
	if strings.Contains(string(capturedBody), "parallel_tool_calls") {
		t.Fatalf("expected parallel_tool_calls to be absent from request body, got: %s", string(capturedBody))
	}
}

// minimalJSONResponse returns a JSON response body with a single choice and usage info,
// suitable for most header-capture tests that don't care about the response content.
func minimalJSONResponse() []byte {
	return []byte(`{
		"choices":[{"message":{"content":"ok","tool_calls":[]}}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
}

// TestOpenRouterHeadersPresent verifies that HTTP-Referer and X-Title headers are sent
// when providerName is "openrouter" and both config fields are non-empty.
func TestOpenRouterHeadersPresent(t *testing.T) {
	t.Parallel()

	var capturedReferer, capturedTitle string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:            "test-key",
		BaseURL:           testServer.URL,
		Model:             "openai/gpt-4.1-mini",
		ProviderName:      "openrouter",
		OpenRouterReferer: "https://github.com/dennisonbertram/go-agent-harness",
		OpenRouterTitle:   "go-agent-harness",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "openai/gpt-4.1-mini",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if capturedReferer != "https://github.com/dennisonbertram/go-agent-harness" {
		t.Errorf("HTTP-Referer = %q, want %q", capturedReferer, "https://github.com/dennisonbertram/go-agent-harness")
	}
	if capturedTitle != "go-agent-harness" {
		t.Errorf("X-Title = %q, want %q", capturedTitle, "go-agent-harness")
	}
}

// TestOpenRouterHeadersAbsentForOpenAI verifies that HTTP-Referer and X-Title headers
// are NOT sent when providerName is "openai".
func TestOpenRouterHeadersAbsentForOpenAI(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Errorf("unexpected HTTP-Referer header on openai request: %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "" {
			t.Errorf("unexpected X-Title header on openai request: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      testServer.URL,
		Model:        "gpt-4.1-mini",
		ProviderName: "openai",
		// These should be ignored because providerName != "openrouter".
		OpenRouterReferer: "https://github.com/dennisonbertram/go-agent-harness",
		OpenRouterTitle:   "go-agent-harness",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1-mini",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// TestOpenRouterHeadersAbsentForAnthropic verifies that HTTP-Referer and X-Title headers
// are NOT sent when providerName is "anthropic" (uses the openai compat client path).
func TestOpenRouterHeadersAbsentForAnthropic(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Errorf("unexpected HTTP-Referer header on anthropic request: %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "" {
			t.Errorf("unexpected X-Title header on anthropic request: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      testServer.URL,
		Model:        "claude-3-5-sonnet",
		ProviderName: "anthropic",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "claude-3-5-sonnet",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// TestOpenRouterHeadersEmptyWhenConfigBlank verifies that HTTP-Referer and X-Title
// are not set when providerName is "openrouter" but the config fields are empty.
func TestOpenRouterHeadersEmptyWhenConfigBlank(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Errorf("unexpected HTTP-Referer header when config fields are blank: %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "" {
			t.Errorf("unexpected X-Title header when config fields are blank: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:       "test-key",
		BaseURL:      testServer.URL,
		Model:        "openai/gpt-4.1-mini",
		ProviderName: "openrouter",
		// OpenRouterReferer and OpenRouterTitle intentionally left blank.
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "openai/gpt-4.1-mini",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func testRetryConfig() *provider.RetryConfig {
	return &provider.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		MaxTotal:    100 * time.Millisecond,
		Jitter:      false,
	}
}

func TestClientRetriesOn429(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Retry:   testRetryConfig(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestClientRetriesOn503(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unavailable"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalJSONResponse())
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Retry:   testRetryConfig(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestClientDoesNotRetryOn400(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Retry:   testRetryConfig(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(context.Background(), harness.CompletionRequest{
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

func TestClientStreamingRetriesInitialError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"ok"}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Retry:   testRetryConfig(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var deltas []harness.CompletionDelta
	result, err := client.Complete(context.Background(), harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
		Stream: func(delta harness.CompletionDelta) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if len(deltas) != 1 || deltas[0].Content != "ok" {
		t.Fatalf("unexpected deltas: %+v", deltas)
	}
}

func TestClientContextCancellationAbortsRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			cancel()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer testServer.Close()

	client, err := NewClient(Config{
		APIKey:  "test-key",
		BaseURL: testServer.URL,
		Retry: &provider.RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   10 * time.Second,
			MaxDelay:    10 * time.Second,
			MaxTotal:    10 * time.Second,
			Jitter:      false,
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Complete(ctx, harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}
