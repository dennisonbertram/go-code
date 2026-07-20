package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

// compactInstructionCaptureProvider records every CompletionRequest it
// receives and returns scripted results, gating the 4th call (idx 3) so the
// test can compact the run mid-flight.
type compactInstructionCaptureProvider struct {
	mu         sync.Mutex
	calls      int
	requests   []harness.CompletionRequest
	results    []harness.CompletionResult
	beforeCall func(idx int)
}

func (p *compactInstructionCaptureProvider) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	captured := harness.CompletionRequest{
		Model:    req.Model,
		Messages: append([]harness.Message(nil), req.Messages...),
	}
	p.requests = append(p.requests, captured)
	var result harness.CompletionResult
	if idx < len(p.results) {
		result = p.results[idx]
	}
	beforeCall := p.beforeCall
	p.mu.Unlock()

	if beforeCall != nil {
		beforeCall(idx)
	}
	return result, nil
}

func (p *compactInstructionCaptureProvider) snapshot() []harness.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]harness.CompletionRequest(nil), p.requests...)
}

func (p *compactInstructionCaptureProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestCompactEndpointInstructionReachesSummarizer verifies the run compact
// endpoint accepts an optional instruction field and threads it into the
// summarization prompt sent to the provider (epic #817 slice 1).
func TestCompactEndpointInstructionReachesSummarizer(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &compactInstructionCaptureProvider{
		results: []harness.CompletionResult{
			{ToolCalls: []harness.ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{"message":"s2"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-3", Name: "echo_json", Arguments: `{"message":"s3"}`}}},
			{Content: "done"},
			{Content: "a compact summary"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"echo":"ok"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     6,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("expected run_id in response")
	}

	// Wait for the run to be blocked in the 4th LLM call: 4 turns in state.
	<-blockCh

	compactRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/compact",
		"application/json",
		bytes.NewBufferString(`{"mode":"summarize","keep_last":1,"instruction":"keep the SQL schema"}`),
	)
	if err != nil {
		t.Fatalf("compact request: %v", err)
	}
	defer compactRes.Body.Close()

	if compactRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(compactRes.Body)
		t.Fatalf("expected 200, got %d: %s", compactRes.StatusCode, string(body))
	}

	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(compactRes.Body).Decode(&result); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}
	if !result.OK {
		t.Error("expected ok=true in compact response")
	}

	// The summarization call must carry the preserve-instruction in its final
	// (prompt) message.
	foundInstruction := false
	summarizationCalls := 0
	for _, req := range provider.snapshot() {
		if len(req.Messages) == 0 {
			continue
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.HasPrefix(last.Content, "Please provide a concise summary") {
			summarizationCalls++
			if strings.Contains(last.Content, "keep the SQL schema") {
				foundInstruction = true
			}
		}
	}
	if summarizationCalls != 1 {
		t.Fatalf("expected exactly 1 summarization call, got %d", summarizationCalls)
	}
	if !foundInstruction {
		t.Error("summarization prompt does not contain the instruction from the compact request")
	}

	close(releaseCh)

	// Let the run finish cleanly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		checkRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err == nil {
			var runState struct {
				Status string `json:"status"`
			}
			json.NewDecoder(checkRes.Body).Decode(&runState)
			checkRes.Body.Close()
			if runState.Status == "completed" || runState.Status == "failed" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCompactEndpointWithoutInstructionUnchanged verifies the endpoint still
// accepts requests without an instruction field and that the summarization
// prompt remains the fixed, instruction-free prompt (backward compatible).
func TestCompactEndpointWithoutInstructionUnchanged(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &compactInstructionCaptureProvider{
		results: []harness.CompletionResult{
			{ToolCalls: []harness.ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{"message":"s2"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-3", Name: "echo_json", Arguments: `{"message":"s3"}`}}},
			{Content: "done"},
			{Content: "a compact summary"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"echo":"ok"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     6,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	<-blockCh

	// No instruction field at all — the pre-feature request shape.
	compactRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/compact",
		"application/json",
		bytes.NewBufferString(`{"mode":"summarize","keep_last":1}`),
	)
	if err != nil {
		t.Fatalf("compact request: %v", err)
	}
	defer compactRes.Body.Close()

	if compactRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(compactRes.Body)
		t.Fatalf("expected 200, got %d: %s", compactRes.StatusCode, string(body))
	}

	const fixedPrompt = "Please provide a concise summary of this conversation so far, suitable for use as context in a continuation. Include key facts, decisions, and outputs. Be concise."
	summarizationCalls := 0
	for _, req := range provider.snapshot() {
		if len(req.Messages) == 0 {
			continue
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.HasPrefix(last.Content, "Please provide a concise summary") {
			summarizationCalls++
			if last.Content != fixedPrompt {
				t.Errorf("instruction-free request changed the summarization prompt:\n got: %q\nwant: %q", last.Content, fixedPrompt)
			}
		}
	}
	if summarizationCalls != 1 {
		t.Fatalf("expected exactly 1 summarization call, got %d", summarizationCalls)
	}

	close(releaseCh)
}

// TestCompactEndpointReturnsSummaryForSummarizeMode verifies the compact
// endpoint response carries mode and the produced summary for summarize mode
// (epic #817 slice 2): {"ok":true,"messages_removed":N,"mode":"summarize","summary":"..."}.
func TestCompactEndpointReturnsSummaryForSummarizeMode(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &compactInstructionCaptureProvider{
		results: []harness.CompletionResult{
			{ToolCalls: []harness.ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{"message":"s2"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-3", Name: "echo_json", Arguments: `{"message":"s3"}`}}},
			{Content: "done"},
			{Content: "a compact summary"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"echo":"ok"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     6,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	<-blockCh

	compactRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/compact",
		"application/json",
		bytes.NewBufferString(`{"mode":"summarize","keep_last":1}`),
	)
	if err != nil {
		t.Fatalf("compact request: %v", err)
	}
	defer compactRes.Body.Close()

	if compactRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(compactRes.Body)
		t.Fatalf("expected 200, got %d: %s", compactRes.StatusCode, string(body))
	}

	var resp map[string]any
	if err := json.NewDecoder(compactRes.Body).Decode(&resp); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}

	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	removed, ok := resp["messages_removed"].(float64)
	if !ok || removed <= 0 {
		t.Errorf("expected messages_removed > 0, got %v", resp["messages_removed"])
	}
	if resp["mode"] != "summarize" {
		t.Errorf("expected mode=%q, got %v", "summarize", resp["mode"])
	}
	if resp["summary"] != "a compact summary" {
		t.Errorf("expected summary=%q, got %v", "a compact summary", resp["summary"])
	}

	close(releaseCh)
}

// TestCompactEndpointStripModeReturnsEmptySummary verifies strip mode returns
// mode="strip" and an empty summary while messages_removed stays populated,
// and that the response remains additive-only (ok/messages_removed intact).
func TestCompactEndpointStripModeReturnsEmptySummary(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &compactInstructionCaptureProvider{
		results: []harness.CompletionResult{
			{ToolCalls: []harness.ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{"message":"s2"}`}}},
			{ToolCalls: []harness.ToolCall{{ID: "call-3", Name: "echo_json", Arguments: `{"message":"s3"}`}}},
			{Content: "done"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"echo":"ok"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     6,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	<-blockCh

	compactRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/compact",
		"application/json",
		bytes.NewBufferString(`{"mode":"strip","keep_last":1}`),
	)
	if err != nil {
		t.Fatalf("compact request: %v", err)
	}
	defer compactRes.Body.Close()

	if compactRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(compactRes.Body)
		t.Fatalf("expected 200, got %d: %s", compactRes.StatusCode, string(body))
	}

	var resp map[string]any
	if err := json.NewDecoder(compactRes.Body).Decode(&resp); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}

	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	removed, ok := resp["messages_removed"].(float64)
	if !ok || removed <= 0 {
		t.Errorf("expected messages_removed > 0, got %v", resp["messages_removed"])
	}
	if resp["mode"] != "strip" {
		t.Errorf("expected mode=%q, got %v", "strip", resp["mode"])
	}
	summaryVal, present := resp["summary"]
	if !present {
		t.Error("expected summary key to be present (additive field), got missing")
	} else if summaryVal != "" {
		t.Errorf("strip mode must return an empty summary, got %v", summaryVal)
	}

	close(releaseCh)
}
