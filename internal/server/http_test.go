package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/store"
)

type staticProvider struct {
	result harness.CompletionResult
}

func (s *staticProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return s.result, nil
}

type scriptedProvider struct {
	mu    sync.Mutex
	turns []harness.CompletionResult
	calls int
}

func (s *scriptedProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.turns) {
		return harness.CompletionResult{}, nil
	}
	out := s.turns[s.calls]
	s.calls++
	return out, nil
}

// waitingProvider blocks until its done channel is closed. Useful for testing
// SSE keep-alive pings and other scenarios that require an idle event stream.
type waitingProvider struct {
	done chan struct{}
}

func (w *waitingProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	select {
	case <-ctx.Done():
		return harness.CompletionResult{}, ctx.Err()
	case <-w.done:
		return harness.CompletionResult{Content: "done"}, nil
	}
}

func TestRunLifecycleEndpoints(t *testing.T) {
	t.Parallel()

	registry := harness.NewRegistry()
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "done"}}, registry, harness.RunnerConfig{
		DefaultModel:        "gpt-4.1-mini",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Hello"}`))
	if err != nil {
		t.Fatalf("create run request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, string(body))
	}

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.RunID == "" {
		t.Fatalf("expected run id")
	}

	eventsRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer eventsRes.Body.Close()

	if got := eventsRes.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected event stream content type, got %q", got)
	}

	eventBody, err := io.ReadAll(eventsRes.Body)
	if err != nil {
		t.Fatalf("read events body: %v", err)
	}
	bodyStr := string(eventBody)

	if !strings.Contains(bodyStr, "event: run.completed") {
		t.Fatalf("expected run.completed event in body: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "event: assistant.message") {
		t.Fatalf("expected assistant.message event in body: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "event: usage.delta") {
		t.Fatalf("expected usage.delta event in body: %s", bodyStr)
	}

	statusRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
	if err != nil {
		t.Fatalf("get run request: %v", err)
	}
	defer statusRes.Body.Close()

	if statusRes.StatusCode != http.StatusOK {
		t.Fatalf("unexpected run status code: %d", statusRes.StatusCode)
	}
	var runState struct {
		Status      string                  `json:"status"`
		Output      string                  `json:"output"`
		UsageTotals *harness.RunUsageTotals `json:"usage_totals"`
		CostTotals  *harness.RunCostTotals  `json:"cost_totals"`
	}
	if err := json.NewDecoder(statusRes.Body).Decode(&runState); err != nil {
		t.Fatalf("decode run state: %v", err)
	}
	if runState.Status != string(harness.RunStatusCompleted) {
		t.Fatalf("expected completed run, got %q", runState.Status)
	}
	if runState.Output != "done" {
		t.Fatalf("unexpected output %q", runState.Output)
	}
	if runState.UsageTotals == nil || runState.CostTotals == nil {
		t.Fatalf("expected usage/cost totals, got %+v", runState)
	}
	if runState.UsageTotals.TotalTokens != 0 {
		t.Fatalf("expected zero totals for provider-unreported usage, got %+v", runState.UsageTotals)
	}
	if runState.CostTotals.CostStatus != harness.CostStatusProviderUnreported {
		t.Fatalf("expected provider_unreported cost status, got %+v", runState.CostTotals)
	}
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("unexpected health status: %q", payload.Status)
	}
}

func TestRunsEndpointMethodNotAllowedAndInvalidJSON(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// GET /v1/runs without a store returns 501 (store not configured), not 405.
	// GET is now a valid method — it returns the run list when a store is wired in.
	getRes, err := http.Get(ts.URL + "/v1/runs")
	if err != nil {
		t.Fatalf("GET /v1/runs: %v", err)
	}
	defer getRes.Body.Close()
	if getRes.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501 (store not configured), got %d", getRes.StatusCode)
	}

	// DELETE /v1/runs is still not allowed.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/runs", nil)
	delRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/runs: %v", err)
	}
	defer delRes.Body.Close()
	if delRes.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for DELETE, got %d", delRes.StatusCode)
	}

	invalidRes, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString("{"))
	if err != nil {
		t.Fatalf("invalid json request: %v", err)
	}
	defer invalidRes.Body.Close()
	if invalidRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", invalidRes.StatusCode)
	}
	var payload map[string]map[string]string
	if err := json.NewDecoder(invalidRes.Body).Decode(&payload); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if payload["error"]["code"] != "invalid_json" {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}

func TestRunByIDEndpointsNotFoundAndMethodValidation(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	notFoundRes, err := http.Get(ts.URL + "/v1/runs/missing")
	if err != nil {
		t.Fatalf("GET missing run: %v", err)
	}
	defer notFoundRes.Body.Close()
	if notFoundRes.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", notFoundRes.StatusCode)
	}

	eventsNotFoundRes, err := http.Get(ts.URL + "/v1/runs/missing/events")
	if err != nil {
		t.Fatalf("GET missing events: %v", err)
	}
	defer eventsNotFoundRes.Body.Close()
	if eventsNotFoundRes.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", eventsNotFoundRes.StatusCode)
	}

	rootNotFoundRes, err := http.Get(ts.URL + "/v1/runs/")
	if err != nil {
		t.Fatalf("GET empty run id path: %v", err)
	}
	defer rootNotFoundRes.Body.Close()
	if rootNotFoundRes.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for /v1/runs/, got %d", rootNotFoundRes.StatusCode)
	}

	createRes, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"x"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer createRes.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	statusPostReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/runs/"+created.RunID, bytes.NewBufferString(`{}`))
	statusPostRes, err := http.DefaultClient.Do(statusPostReq)
	if err != nil {
		t.Fatalf("POST run status: %v", err)
	}
	defer statusPostRes.Body.Close()
	if statusPostRes.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST run status, got %d", statusPostRes.StatusCode)
	}

	eventsPostReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/runs/"+created.RunID+"/events", bytes.NewBufferString(`{}`))
	eventsPostRes, err := http.DefaultClient.Do(eventsPostReq)
	if err != nil {
		t.Fatalf("POST run events: %v", err)
	}
	defer eventsPostRes.Body.Close()
	if eventsPostRes.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST run events, got %d", eventsPostRes.StatusCode)
	}
}

func TestRunInputEndpoints(t *testing.T) {
	t.Parallel()

	broker := harness.NewInMemoryAskUserQuestionBroker(time.Now)
	provider := &scriptedProvider{turns: []harness.CompletionResult{
		{
			ToolCalls: []harness.ToolCall{{
				ID:        "call_input",
				Name:      "AskUserQuestion",
				Arguments: `{"questions":[{"question":"Where next?","header":"Route","options":[{"label":"Docs","description":"Read docs"},{"label":"Code","description":"Read code"}],"multiSelect":false}]}`,
			}},
		},
		{Content: "done"},
	}}
	registry := harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{
		ApprovalMode:   harness.ToolApprovalModeFullAuto,
		AskUserBroker:  broker,
		AskUserTimeout: 3 * time.Second,
	})
	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel:   "gpt-5-nano",
		MaxSteps:       4,
		AskUserBroker:  broker,
		AskUserTimeout: 3 * time.Second,
	})

	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Need input"}`))
	if err != nil {
		t.Fatalf("create run request: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	var inputRes *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for {
		inputRes, err = http.Get(ts.URL + "/v1/runs/" + created.RunID + "/input")
		if err != nil {
			t.Fatalf("get input request: %v", err)
		}
		if inputRes.StatusCode == http.StatusOK {
			break
		}
		_ = inputRes.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pending input")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer inputRes.Body.Close()

	var pending map[string]any
	if err := json.NewDecoder(inputRes.Body).Decode(&pending); err != nil {
		t.Fatalf("decode pending input: %v", err)
	}
	if pending["tool"] != "AskUserQuestion" {
		t.Fatalf("unexpected pending payload: %+v", pending)
	}

	invalidRes, err := http.Post(ts.URL+"/v1/runs/"+created.RunID+"/input", "application/json", bytes.NewBufferString(`{"answers":{"Where next?":"Nope"}}`))
	if err != nil {
		t.Fatalf("post invalid input: %v", err)
	}
	defer invalidRes.Body.Close()
	if invalidRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid answers, got %d", invalidRes.StatusCode)
	}

	validRes, err := http.Post(ts.URL+"/v1/runs/"+created.RunID+"/input", "application/json", bytes.NewBufferString(`{"answers":{"Where next?":"Docs"}}`))
	if err != nil {
		t.Fatalf("post valid input: %v", err)
	}
	defer validRes.Body.Close()
	if validRes.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 for valid answers, got %d", validRes.StatusCode)
	}

	noPendingRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/input")
	if err != nil {
		t.Fatalf("get no pending input: %v", err)
	}
	defer noPendingRes.Body.Close()
	if noPendingRes.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for no pending input, got %d", noPendingRes.StatusCode)
	}
}

func TestRunInputEndpointsMissingRunAndInvalidJSON(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	getRes, err := http.Get(ts.URL + "/v1/runs/missing/input")
	if err != nil {
		t.Fatalf("GET missing input: %v", err)
	}
	defer getRes.Body.Close()
	if getRes.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getRes.StatusCode)
	}

	postMissingRes, err := http.Post(ts.URL+"/v1/runs/missing/input", "application/json", bytes.NewBufferString(`{"answers":{"x":"y"}}`))
	if err != nil {
		t.Fatalf("POST missing input: %v", err)
	}
	defer postMissingRes.Body.Close()
	if postMissingRes.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", postMissingRes.StatusCode)
	}

	createRes, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"x"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer createRes.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	invalidJSONRes, err := http.Post(ts.URL+"/v1/runs/"+created.RunID+"/input", "application/json", bytes.NewBufferString(`{`))
	if err != nil {
		t.Fatalf("POST invalid json: %v", err)
	}
	defer invalidJSONRes.Body.Close()
	if invalidJSONRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", invalidJSONRes.StatusCode)
	}
}

func TestConversationMessagesEndpoint(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "done"}}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// Create a run with a specific conversation ID
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Hello","conversation_id":"conv-http"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Wait for run to complete
	deadline := time.Now().Add(4 * time.Second)
	for {
		statusRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		var runState struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(statusRes.Body).Decode(&runState); err != nil {
			statusRes.Body.Close()
			t.Fatalf("decode run: %v", err)
		}
		statusRes.Body.Close()
		if runState.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run to complete, last status: %s", runState.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// GET conversation messages
	convRes, err := http.Get(ts.URL + "/v1/conversations/conv-http/messages")
	if err != nil {
		t.Fatalf("get conversation messages: %v", err)
	}
	defer convRes.Body.Close()

	if convRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(convRes.Body)
		t.Fatalf("expected 200, got %d: %s", convRes.StatusCode, string(body))
	}

	var payload struct {
		Messages []harness.Message `json:"messages"`
	}
	if err := json.NewDecoder(convRes.Body).Decode(&payload); err != nil {
		t.Fatalf("decode conversation messages: %v", err)
	}
	if len(payload.Messages) == 0 {
		t.Fatalf("expected non-empty messages array")
	}
}

func TestRunSummaryEndpoint(t *testing.T) {
	t.Parallel()

	cached := 10
	provider := &scriptedProvider{turns: []harness.CompletionResult{
		{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`}},
			Usage:      &harness.CompletionUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CachedPromptTokens: &cached},
			CostUSD:    ptrFloat64(0.005),
			CostStatus: harness.CostStatusAvailable,
		},
		{Content: "done", Usage: &harness.CompletionUsage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230}, CostUSD: ptrFloat64(0.003), CostStatus: harness.CostStatusAvailable},
	}}

	registry := harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{
		ApprovalMode: harness.ToolApprovalModeFullAuto,
	})
	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
	})

	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// Create run
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"test summary"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	json.NewDecoder(res.Body).Decode(&created)

	// Wait for completion
	deadline := time.Now().Add(4 * time.Second)
	for {
		statusRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		var state struct {
			Status string `json:"status"`
		}
		json.NewDecoder(statusRes.Body).Decode(&state)
		statusRes.Body.Close()
		if state.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out, status=%s", state.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// GET summary
	summaryRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/summary")
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	defer summaryRes.Body.Close()
	if summaryRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(summaryRes.Body)
		t.Fatalf("expected 200, got %d: %s", summaryRes.StatusCode, body)
	}

	var summary harness.RunSummary
	if err := json.NewDecoder(summaryRes.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	if summary.Status != harness.RunStatusCompleted {
		t.Fatalf("expected completed, got %s", summary.Status)
	}
	if summary.StepsTaken != 2 {
		t.Fatalf("expected 2 steps, got %d", summary.StepsTaken)
	}
	if summary.TotalPromptTokens != 300 {
		t.Fatalf("expected 300 prompt tokens, got %d", summary.TotalPromptTokens)
	}
	if summary.TotalCompletionTokens != 80 {
		t.Fatalf("expected 80 completion tokens, got %d", summary.TotalCompletionTokens)
	}
	if summary.TotalCostUSD != 0.008 {
		t.Fatalf("expected 0.008 cost, got %f", summary.TotalCostUSD)
	}
	if len(summary.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(summary.ToolCalls))
	}
	if summary.ToolCalls[0].ToolName != "bash" {
		t.Fatalf("expected bash tool call, got %s", summary.ToolCalls[0].ToolName)
	}
	if summary.CacheHitRate <= 0 {
		t.Fatalf("expected positive cache hit rate, got %f", summary.CacheHitRate)
	}
}

func TestRunSummaryNotFound(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/runs/missing/summary")
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

func ptrFloat64(v float64) *float64 { return &v }

// mockConversationStore implements harness.ConversationStore for testing.
type mockConversationStore struct {
	conversations      []harness.Conversation
	messages           map[string][]harness.Message
	listErr            error
	deleteErr          error
	loadErr            error
	searchResults      []harness.MessageSearchResult
	searchErr          error
	deletedIDs         []string
	searchedQuery      string
	searchedTenant     string
	searchedLimit      int
	deleteOldCount     int       // number to return from DeleteOldConversations
	deleteOldErr       error     // error to return from DeleteOldConversations
	deleteOldThreshold time.Time // last threshold passed to DeleteOldConversations
	deleteOldCalled    bool      // whether DeleteOldConversations was called
}

func (m *mockConversationStore) Migrate(_ context.Context) error { return nil }
func (m *mockConversationStore) Close() error                    { return nil }
func (m *mockConversationStore) SaveConversation(_ context.Context, _ string, _ []harness.Message) error {
	return nil
}
func (m *mockConversationStore) SaveConversationWithCost(_ context.Context, _ string, _ []harness.Message, _ harness.ConversationTokenCost) error {
	return nil
}
func (m *mockConversationStore) LoadMessages(_ context.Context, convID string) ([]harness.Message, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.messages != nil {
		if msgs, ok := m.messages[convID]; ok {
			return msgs, nil
		}
	}
	return nil, nil
}
func (m *mockConversationStore) ListConversations(_ context.Context, _ harness.ConversationFilter, limit, offset int) ([]harness.Conversation, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if offset >= len(m.conversations) {
		return []harness.Conversation{}, nil
	}
	end := offset + limit
	if end > len(m.conversations) {
		end = len(m.conversations)
	}
	return m.conversations[offset:end], nil
}
func (m *mockConversationStore) DeleteConversation(_ context.Context, convID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedIDs = append(m.deletedIDs, convID)
	return nil
}
func (m *mockConversationStore) SearchMessages(_ context.Context, tenantID, query string, limit int) ([]harness.MessageSearchResult, error) {
	m.searchedQuery = query
	m.searchedTenant = tenantID
	m.searchedLimit = limit
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if m.searchResults != nil {
		return m.searchResults, nil
	}
	return []harness.MessageSearchResult{}, nil
}
func (m *mockConversationStore) DeleteOldConversations(_ context.Context, olderThan time.Time) (int, error) {
	m.deleteOldCalled = true
	m.deleteOldThreshold = olderThan
	if m.deleteOldErr != nil {
		return 0, m.deleteOldErr
	}
	return m.deleteOldCount, nil
}
func (m *mockConversationStore) PinConversation(_ context.Context, _ string, _ bool) error {
	return nil
}
func (m *mockConversationStore) CompactConversation(_ context.Context, _ string, _ int, _ harness.Message) error {
	return nil
}
func (m *mockConversationStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}
func (m *mockConversationStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *mockConversationStore) GetConversationOwner(_ context.Context, _ string) (*harness.Conversation, error) {
	return nil, nil
}
func (m *mockConversationStore) ForkConversation(_ context.Context, srcID, newID string) (*harness.Conversation, error) {
	if m.messages == nil {
		return nil, fmt.Errorf("fork: source conversation %q not found", srcID)
	}
	msgs, ok := m.messages[srcID]
	if !ok {
		return nil, fmt.Errorf("fork: source conversation %q not found", srcID)
	}
	if _, taken := m.messages[newID]; taken {
		return nil, fmt.Errorf("fork: target conversation %q already exists", newID)
	}
	cp := make([]harness.Message, len(msgs))
	copy(cp, msgs)
	m.messages[newID] = cp
	return &harness.Conversation{ID: newID, MsgCount: len(msgs)}, nil
}

func TestConversationMessagesEndpoint404(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/nonexistent/messages")
	if err != nil {
		t.Fatalf("get conversation messages: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

// capturingServerProvider captures all CompletionRequests for inspection.
type capturingServerProvider struct {
	mu     sync.Mutex
	result harness.CompletionResult
	calls  []harness.CompletionRequest
}

func (c *capturingServerProvider) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, req)
	return c.result, nil
}

func (c *capturingServerProvider) lastRequest() *harness.CompletionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	last := c.calls[len(c.calls)-1]
	return &last
}

func TestWriteSSE_IncludesIDAndRetry(t *testing.T) {
	rec := httptest.NewRecorder()
	event := harness.Event{
		ID:        "run_1:42",
		RunID:     "run_1",
		Type:      harness.EventRunStarted,
		Timestamp: time.Now(),
	}
	err := writeSSE(rec, event)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "id: run_1:42\n") {
		t.Errorf("missing id field, got:\n%s", body)
	}
	if !strings.Contains(body, "retry: 3000\n") {
		t.Errorf("missing retry field, got:\n%s", body)
	}
	if !strings.Contains(body, "event: run.started\n") {
		t.Errorf("missing event field, got:\n%s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("missing data field, got:\n%s", body)
	}
}

func TestWriteSSEPing(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeSSEPing(rec)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	// SSE comment lines start with ':' and are ignored by clients per the SSE spec.
	if body != ": ping\n\n" {
		t.Errorf("expected SSE comment line \": ping\\n\\n\", got: %q", body)
	}
}

// TestSSEKeepalivePingsInEventStream verifies that SSE comment pings appear
// in the event stream when the event channel is idle.
func TestSSEKeepalivePingsInEventStream(t *testing.T) {
	t.Setenv("HARNESS_SSE_KEEPALIVE_SECONDS", "1")

	wp := &waitingProvider{done: make(chan struct{})}
	runner := harness.NewRunner(
		wp,
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel: "gpt-4.1-mini",
			MaxSteps:     1,
		},
	)

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Create a run with a provider that blocks, so the event channel stays idle
	// long enough for a keep-alive ping to fire.
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Hello"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Read the SSE stream with a short deadline. We expect at least one ping
	// comment to appear within a few seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/runs/"+created.RunID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect to events stream: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Close the waiting provider to let the run finish cleanly.
	close(wp.done)

	if !strings.Contains(bodyStr, ": ping\n") {
		t.Errorf("expected SSE keep-alive ping comment in event stream, got:\n%s", bodyStr)
	}

	// Verify pings are SSE comments (no event: or data: prefix that would
	// be parsed by EventSource clients).
	if strings.Contains(bodyStr, "event: ping") || strings.Contains(bodyStr, "data: ping") {
		t.Errorf("keep-alive pings should be SSE comments (no event: or data: prefix)")
	}
}

func TestLastEventIDSkipsSeenEvents(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "done"}}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Create a run
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Hello"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Wait for run completion by reading all events
	fullRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatalf("full events: %v", err)
	}
	fullBody, _ := io.ReadAll(fullRes.Body)
	fullRes.Body.Close()
	fullStr := string(fullBody)

	// Count events in full response
	fullEventCount := strings.Count(fullStr, "\nevent: ")
	if fullEventCount == 0 {
		// Try without leading newline for first event
		fullEventCount = strings.Count(fullStr, "event: ")
	}
	if fullEventCount < 3 {
		t.Fatalf("expected at least 3 events, got %d in:\n%s", fullEventCount, fullStr)
	}

	// Reconnect with Last-Event-ID set to skip early events
	// Use seq=1 to skip events 0 and 1
	lastEventID := fmt.Sprintf("%s:1", created.RunID)
	req, _ := http.NewRequest("GET", ts.URL+"/v1/runs/"+created.RunID+"/events", nil)
	req.Header.Set("Last-Event-ID", lastEventID)
	reconnectRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	reconnectBody, _ := io.ReadAll(reconnectRes.Body)
	reconnectRes.Body.Close()
	reconnectStr := string(reconnectBody)

	// The reconnect response should have fewer events
	reconnectEventCount := strings.Count(reconnectStr, "event: ")
	if reconnectEventCount >= fullEventCount {
		t.Fatalf("reconnect should have fewer events than full stream: reconnect=%d full=%d\nfull:\n%s\nreconnect:\n%s",
			reconnectEventCount, fullEventCount, fullStr, reconnectStr)
	}

	// First event in reconnect should NOT be :0 or :1
	if strings.Contains(reconnectStr, fmt.Sprintf("id: %s:0\n", created.RunID)) {
		t.Fatalf("reconnect should not contain event :0")
	}
	if strings.Contains(reconnectStr, fmt.Sprintf("id: %s:1\n", created.RunID)) {
		t.Fatalf("reconnect should not contain event :1")
	}
}

// TestLastEventID_AdversarialValuesDoNotPanic is an ATTACK test (C1): a
// crafted Last-Event-ID header must never crash the run-events handler.
// handleRunEvents parses the sequence number out of Last-Event-ID and slices
// the in-memory history with it (history[seq+1:]); a huge, overflow-prone, or
// out-of-range sequence must fall back to a safe replay instead of panicking
// (remote DoS via a single header).
//
// The handler is invoked directly via ServeHTTP (no real network listener) so
// that a panic inside the handler surfaces as a Go panic in this test's own
// goroutine — recovered below into a clear test failure — rather than being
// silently absorbed by net/http's per-connection panic recovery, which would
// mask the bug behind an ordinary connection reset.
func TestLastEventID_AdversarialValuesDoNotPanic(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "done"}}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})
	handler := New(runner)

	// Create a run.
	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`))
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create run: expected 202, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Drain the full event stream once (blocks until the run reaches a
	// terminal event) so the in-memory history is populated and stable
	// before we probe it with adversarial Last-Event-ID values.
	drainRec := httptest.NewRecorder()
	drainReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID+"/events", nil)
	handler.ServeHTTP(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain events: expected 200, got %d: %s", drainRec.Code, drainRec.Body.String())
	}

	cases := []struct {
		name      string
		lastEvent string
	}{
		// int64-overflow boundary: seq+1 as uint64 no longer fits in int64.
		{"overflow_boundary_maxint64", created.RunID + ":9223372036854775807"},
		// Largest representable uint64 (parses fine via strconv.ParseUint).
		{"max_uint64", created.RunID + ":18446744073709551615"},
		// Valid but far beyond the actual history length.
		{"far_beyond_history_length", created.RunID + ":999999"},
		{"non_numeric", created.RunID + ":not-a-number"},
		{"missing_colon", created.RunID},
		{"empty_sequence", created.RunID + ":"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("handler panicked for Last-Event-ID %q: %v", tc.lastEvent, r)
				}
			}()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID+"/events", nil)
			req.Header.Set("Last-Event-ID", tc.lastEvent)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 (safe fallback), got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSpecialCharacterPromptsRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prompt string
	}{
		{"exclamation", "Hello! How are you?"},
		{"single_quotes", "It's a test"},
		{"double_quotes", `She said "hello"`},
		{"backslashes", `path\to\file`},
		{"newlines", "line1\nline2"},
		{"unicode_emoji", "Hello 🌍 world"},
		{"json_in_prompt", `Parse this: {"key": "value"}`},
		{"shell_metacharacters", `echo $HOME && rm -rf /; ls | grep foo`},
		{"backticks", "`code block`"},
		{"mixed", `It's "complex"! path\to\file 🎉 $var`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prov := &capturingServerProvider{result: harness.CompletionResult{Content: "ack"}}
			registry := harness.NewRegistry()
			runner := harness.NewRunner(prov, registry, harness.RunnerConfig{
				DefaultModel: "test-model",
				MaxSteps:     2,
			})
			ts := httptest.NewServer(New(runner))
			defer ts.Close()

			// Marshal prompt into JSON properly
			body, err := json.Marshal(map[string]string{"prompt": tc.prompt})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("create run: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusAccepted {
				respBody, _ := io.ReadAll(res.Body)
				t.Fatalf("expected 202, got %d: %s", res.StatusCode, respBody)
			}

			var created struct {
				RunID string `json:"run_id"`
			}
			json.NewDecoder(res.Body).Decode(&created)

			// Wait for completion
			deadline := time.Now().Add(4 * time.Second)
			for {
				statusRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
				if err != nil {
					t.Fatalf("get run: %v", err)
				}
				var state struct {
					Status string `json:"status"`
				}
				json.NewDecoder(statusRes.Body).Decode(&state)
				statusRes.Body.Close()
				if state.Status == "completed" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("timed out, last status: %s", state.Status)
				}
				time.Sleep(20 * time.Millisecond)
			}

			// Assert the provider received the exact prompt
			last := prov.lastRequest()
			if last == nil {
				t.Fatal("provider was never called")
			}
			// Find the user message in the messages slice
			found := false
			for _, msg := range last.Messages {
				if msg.Role == "user" && msg.Content == tc.prompt {
					found = true
					break
				}
			}
			if !found {
				// Show what was actually received for debugging
				var contents []string
				for _, msg := range last.Messages {
					contents = append(contents, fmt.Sprintf("role=%s content=%q", msg.Role, msg.Content))
				}
				t.Fatalf("prompt not found in messages.\nExpected: %q\nGot messages: %v", tc.prompt, contents)
			}
		})
	}
}

func TestParsePositiveInt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"zero", "0", 0, false},
		{"positive", "42", 42, false},
		{"large", "99999", 99999, false},
		{"negative_sign", "-1", 0, true},
		{"letters", "abc", 0, true},
		{"mixed", "12x", 0, true},
		{"empty", "", 0, false},
		{"float", "3.5", 0, true},
		{"spaces", "1 2", 0, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePositiveInt(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for input %q", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("parsePositiveInt(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestHandleListConversationsNoStore(t *testing.T) {
	t.Parallel()

	// Runner without ConversationStore — store is nil
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/")
	if err != nil {
		t.Fatalf("GET conversations: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501 when store is nil, got %d", res.StatusCode)
	}
}

func TestHandleListConversationsMethodNotAllowed(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/conversations/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE conversations: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.StatusCode)
	}
}

func TestHandleListConversationsSuccess(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		conversations: []harness.Conversation{
			{ID: "conv-1", MsgCount: 3},
			{ID: "conv-2", MsgCount: 5},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/")
	if err != nil {
		t.Fatalf("GET conversations: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	var payload struct {
		Conversations []harness.Conversation `json:"conversations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Conversations) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(payload.Conversations))
	}
}

func TestHandleListConversationsWithLimitOffset(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		conversations: []harness.Conversation{
			{ID: "conv-1"},
			{ID: "conv-2"},
			{ID: "conv-3"},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?limit=1&offset=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var payload struct {
		Conversations []harness.Conversation `json:"conversations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Conversations) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(payload.Conversations))
	}
	if payload.Conversations[0].ID != "conv-2" {
		t.Fatalf("expected conv-2, got %s", payload.Conversations[0].ID)
	}
}

func TestHandleListConversationsStoreError(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		listErr: fmt.Errorf("database locked"),
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.StatusCode)
	}
}

func TestHandleDeleteConversationNoStore(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/conversations/conv-1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
}

func TestHandleDeleteConversationSuccess(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/conversations/conv-del-test", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	var payload struct {
		Deleted bool `json:"deleted"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.Deleted {
		t.Fatalf("expected deleted=true")
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "conv-del-test" {
		t.Fatalf("expected store.deletedIDs=[conv-del-test], got %v", store.deletedIDs)
	}
}

func TestHandleDeleteConversationStoreError(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		deleteErr: fmt.Errorf("disk full"),
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/conversations/conv-1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Search endpoint tests (Issue #37)
// ---------------------------------------------------------------------------

func TestHandleSearchConversations_NoStore(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/search?q=hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
}

func TestHandleSearchConversations_MissingQuery(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/search")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

func TestHandleSearchConversations_ReturnsResults(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		searchResults: []harness.MessageSearchResult{
			{ConversationID: "conv-1", Role: "user", Snippet: "hello <b>world</b>"},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/search?q=world&limit=5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}
	var payload struct {
		Results []harness.MessageSearchResult `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(payload.Results))
	}
	if payload.Results[0].ConversationID != "conv-1" {
		t.Fatalf("unexpected conversation_id: %s", payload.Results[0].ConversationID)
	}
	if store.searchedQuery != "world" {
		t.Fatalf("expected query=world, got %q", store.searchedQuery)
	}
	if store.searchedLimit != 5 {
		t.Fatalf("expected limit=5, got %d", store.searchedLimit)
	}
}

func TestHandleSearchConversations_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		searchErr: fmt.Errorf("index corrupt"),
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/search?q=test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.StatusCode)
	}
}

func TestHandleSearchConversations_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/conversations/search?q=test", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Export endpoint tests (Issue #36)
// ---------------------------------------------------------------------------

func TestHandleExportConversation_NotFound(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/nonexistent/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

func TestHandleExportConversation_FromStore(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		messages: map[string][]harness.Message{
			"conv-export": {
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "world"},
			},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/conv-export/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("expected application/x-ndjson, got %q", ct)
	}

	body, _ := io.ReadAll(res.Body)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %s", len(lines), body)
	}
	var msg0 harness.Message
	if err := json.Unmarshal([]byte(lines[0]), &msg0); err != nil {
		t.Fatalf("parse line 0: %v", err)
	}
	if msg0.Role != "user" || msg0.Content != "hello" {
		t.Fatalf("unexpected line 0: %+v", msg0)
	}
}

func TestHandleExportConversation_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/conversations/conv-1/export", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.StatusCode)
	}
}

func TestHandleExportConversation_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		loadErr: fmt.Errorf("read failure"),
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/conv-1/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.StatusCode)
	}
}

func TestHandleExportConversation_StoreNotConfigured(t *testing.T) {
	t.Parallel()

	// No ConversationStore configured, conversation not in memory → 404
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/nonexistent/export")
	if err != nil {
		t.Fatalf("GET export: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when not in memory and no store, got %d", res.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Issue #35: workspace/tenant_id filter query param tests
// ---------------------------------------------------------------------------

// filterCapturingStore captures the filter passed to ListConversations.
type filterCapturingStore struct {
	mockConversationStore
	capturedFilter harness.ConversationFilter
}

func (f *filterCapturingStore) ListConversations(ctx context.Context, filter harness.ConversationFilter, limit, offset int) ([]harness.Conversation, error) {
	f.capturedFilter = filter
	return f.mockConversationStore.ListConversations(ctx, filter, limit, offset)
}

func TestHandleListConversationsFilterByTenantID(t *testing.T) {
	t.Parallel()

	fstore := &filterCapturingStore{
		mockConversationStore: mockConversationStore{
			conversations: []harness.Conversation{
				{ID: "conv-1", TenantID: "t-abc"},
				{ID: "conv-2", TenantID: "t-xyz"},
			},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: fstore,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?tenant_id=t-abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	if fstore.capturedFilter.TenantID != "t-abc" {
		t.Errorf("expected captured TenantID %q, got %q", "t-abc", fstore.capturedFilter.TenantID)
	}
	if fstore.capturedFilter.Workspace != "" {
		t.Errorf("expected empty Workspace, got %q", fstore.capturedFilter.Workspace)
	}
}

func TestHandleListConversationsFilterByWorkspace(t *testing.T) {
	t.Parallel()

	fstore := &filterCapturingStore{
		mockConversationStore: mockConversationStore{
			conversations: []harness.Conversation{
				{ID: "conv-1", Workspace: "ws-foo"},
			},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: fstore,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?workspace=ws-foo")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	if fstore.capturedFilter.Workspace != "ws-foo" {
		t.Errorf("expected captured Workspace %q, got %q", "ws-foo", fstore.capturedFilter.Workspace)
	}
	if fstore.capturedFilter.TenantID != "" {
		t.Errorf("expected empty TenantID, got %q", fstore.capturedFilter.TenantID)
	}
}

func TestHandleListConversationsFilterBothWorkspaceAndTenant(t *testing.T) {
	t.Parallel()

	fstore := &filterCapturingStore{
		mockConversationStore: mockConversationStore{
			conversations: []harness.Conversation{},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: fstore,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?workspace=ws-X&tenant_id=t-Y")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	if fstore.capturedFilter.Workspace != "ws-X" {
		t.Errorf("expected Workspace %q, got %q", "ws-X", fstore.capturedFilter.Workspace)
	}
	if fstore.capturedFilter.TenantID != "t-Y" {
		t.Errorf("expected TenantID %q, got %q", "t-Y", fstore.capturedFilter.TenantID)
	}
}

func TestHandleListConversationsNoFilter(t *testing.T) {
	t.Parallel()

	fstore := &filterCapturingStore{
		mockConversationStore: mockConversationStore{
			conversations: []harness.Conversation{},
		},
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: fstore,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	// No filter params — filter should be empty
	if fstore.capturedFilter.Workspace != "" || fstore.capturedFilter.TenantID != "" {
		t.Errorf("expected empty filter, got workspace=%q tenant_id=%q",
			fstore.capturedFilter.Workspace, fstore.capturedFilter.TenantID)
	}
}

// ---------------------------------------------------------------------------
// Fix 1: GET /v1/conversations/?q= delegates to search handler
// ---------------------------------------------------------------------------

func TestListConversations_QParam_DelegatesToSearch(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		searchResults: []harness.MessageSearchResult{
			{ConversationID: "conv-found", Role: "user", Snippet: "hello <b>world</b>"},
		},
	}
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?q=hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	var payload struct {
		Results []harness.MessageSearchResult `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(payload.Results))
	}
	if payload.Results[0].ConversationID != "conv-found" {
		t.Errorf("unexpected conversation_id: %s", payload.Results[0].ConversationID)
	}
	if store.searchedQuery != "hello" {
		t.Errorf("expected searchedQuery=hello, got %q", store.searchedQuery)
	}
}

func TestListConversations_QParam_NoStore(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/?q=hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()

	// Without a store, search returns 501 Not Implemented.
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", res.StatusCode)
	}
}

func TestListConversations_NoQParam_StillLists(t *testing.T) {
	t.Parallel()

	store := &mockConversationStore{
		conversations: []harness.Conversation{
			{ID: "conv-a"},
			{ID: "conv-b"},
		},
	}
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	var payload struct {
		Conversations []harness.Conversation `json:"conversations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Conversations) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(payload.Conversations))
	}
	// search should NOT have been called
	if store.searchedQuery != "" {
		t.Errorf("expected no search call, but got searchedQuery=%q", store.searchedQuery)
	}
}

// ---------------------------------------------------------------------------
// Fix 2: POST /v1/conversations/{id}/compact auto-generates summary
// ---------------------------------------------------------------------------

// summarizingProvider captures the messages it receives and returns a canned summary.
type summarizingProvider struct {
	mu              sync.Mutex
	capturedRequest harness.CompletionRequest
	summary         string
}

func (p *summarizingProvider) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.capturedRequest = req
	return harness.CompletionResult{Content: p.summary}, nil
}

func TestCompactConversation_AutoGenerateSummary(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	msgs := []harness.Message{
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "Paris."},
		{Role: "user", Content: "And Germany?"},
		{Role: "assistant", Content: "Berlin."},
	}
	if err := store.SaveConversation(ctx, "conv-auto-summary", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	prov := &summarizingProvider{summary: "Auto-generated: capitals discussed, Paris and Berlin."}
	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
		DefaultModel:      "test-model",
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// POST with no summary field — should auto-generate.
	body := bytes.NewBufferString(`{"keep_from_step":4}`)
	res, err := http.Post(ts.URL+"/v1/conversations/conv-auto-summary/compact", "application/json", body)
	if err != nil {
		t.Fatalf("POST compact: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, b)
	}

	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["compacted"] != true {
		t.Errorf("expected compacted=true, got %v", resp["compacted"])
	}

	// Verify the summary message in the store is the auto-generated one.
	loaded, err := store.LoadMessages(ctx, "conv-auto-summary")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("expected messages after compact")
	}
	if !loaded[0].IsCompactSummary {
		t.Error("first message should be marked as compact summary")
	}
	if loaded[0].Content != "Auto-generated: capitals discussed, Paris and Berlin." {
		t.Errorf("unexpected summary content: %q", loaded[0].Content)
	}

	// Verify the provider was called with the conversation messages plus a summarize prompt.
	prov.mu.Lock()
	captured := prov.capturedRequest
	prov.mu.Unlock()

	if captured.Model != "test-model" {
		t.Errorf("expected model=test-model, got %q", captured.Model)
	}
	// The last message in the request should be the summarize prompt.
	if len(captured.Messages) == 0 {
		t.Fatal("expected messages in captured request")
	}
	last := captured.Messages[len(captured.Messages)-1]
	if last.Role != "user" {
		t.Errorf("expected last message role=user, got %q", last.Role)
	}
	if !strings.Contains(last.Content, "summary") {
		t.Errorf("expected summarize prompt in last message, got %q", last.Content)
	}
}

func TestCompactConversation_AutoGenerateSummary_NoProvider(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	msgs := []harness.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	if err := store.SaveConversation(ctx, "conv-no-provider", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Use a provider that errors to simulate unavailability.
	errProv := &errorOnSummarizeProvider{}
	runner := harness.NewRunner(errProv, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: store,
	})
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	body := bytes.NewBufferString(`{"keep_from_step":2}`)
	res, err := http.Post(ts.URL+"/v1/conversations/conv-no-provider/compact", "application/json", body)
	if err != nil {
		t.Fatalf("POST compact: %v", err)
	}
	defer res.Body.Close()

	// When auto-summary fails, we expect a 500.
	if res.StatusCode != http.StatusInternalServerError {
		b, _ := io.ReadAll(res.Body)
		t.Errorf("expected 500 when provider errors, got %d: %s", res.StatusCode, b)
	}
}

// errorOnSummarizeProvider always returns an error from Complete.
type errorOnSummarizeProvider struct{}

func (e *errorOnSummarizeProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return harness.CompletionResult{}, fmt.Errorf("provider unavailable")
}

func TestCompactConversation_AutoGenerateSummary_ConvNotFound(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// No summary, conversation does not exist.
	body := bytes.NewBufferString(`{"keep_from_step":0}`)
	res, err := http.Post(ts.URL+"/v1/conversations/does-not-exist/compact", "application/json", body)
	if err != nil {
		t.Fatalf("POST compact: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(res.Body)
		t.Errorf("expected 404 for missing conversation, got %d: %s", res.StatusCode, b)
	}
}

// TestCleanupEndpoint tests POST /v1/conversations/cleanup.
func TestCleanupEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("no_store_returns_501", func(t *testing.T) {
		t.Parallel()
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
		ts := httptest.NewServer(New(runner))
		defer ts.Close()

		res, err := http.Post(ts.URL+"/v1/conversations/cleanup", "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Fatalf("expected 501, got %d", res.StatusCode)
		}
	})

	t.Run("returns_deleted_count", func(t *testing.T) {
		t.Parallel()
		store := &mockConversationStore{deleteOldCount: 5}
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
			ConversationStore: store,
		})
		ts := httptest.NewServer(New(runner))
		defer ts.Close()

		res, err := http.Post(ts.URL+"/v1/conversations/cleanup", "application/json", bytes.NewBufferString(`{"max_age_days":30}`))
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}

		var resp struct {
			Deleted int `json:"deleted"`
		}
		if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Deleted != 5 {
			t.Errorf("expected deleted=5, got %d", resp.Deleted)
		}
		if !store.deleteOldCalled {
			t.Error("expected DeleteOldConversations to be called")
		}
	})

	t.Run("default_max_age_30_days", func(t *testing.T) {
		t.Parallel()
		store := &mockConversationStore{deleteOldCount: 0}
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
			ConversationStore: store,
		})

		// Inject timeNow into the server struct (same package) to avoid a data race
		// with concurrent tests that also run HTTP requests.
		fakeNow := time.Date(2025, 1, 31, 12, 0, 0, 0, time.UTC)
		srv := &Server{runner: runner, timeNow: func() time.Time { return fakeNow }}
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", srv.handleHealth)
		mux.HandleFunc("/v1/runs", srv.handleRuns)
		mux.HandleFunc("/v1/runs/", srv.handleRunByID)
		mux.HandleFunc("/v1/conversations/", srv.handleConversations)
		mux.HandleFunc("/v1/models", srv.handleModels)
		mux.HandleFunc("/v1/agents", srv.handleAgents)
		ts := httptest.NewServer(mux)
		defer ts.Close()

		// POST without body — should default to 30 days.
		res, err := http.Post(ts.URL+"/v1/conversations/cleanup", "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}

		if !store.deleteOldCalled {
			t.Fatal("expected DeleteOldConversations to be called")
		}

		want := fakeNow.UTC().Add(-30 * 24 * time.Hour)
		if !store.deleteOldThreshold.Equal(want) {
			t.Errorf("threshold = %v, want %v", store.deleteOldThreshold, want)
		}
	})

	t.Run("zero_max_age_skips_deletion", func(t *testing.T) {
		t.Parallel()
		store := &mockConversationStore{deleteOldCount: 99}
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
			ConversationStore: store,
		})
		ts := httptest.NewServer(New(runner))
		defer ts.Close()

		res, err := http.Post(ts.URL+"/v1/conversations/cleanup", "application/json", bytes.NewBufferString(`{"max_age_days":0}`))
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}

		var resp struct {
			Deleted int `json:"deleted"`
		}
		if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Deleted != 0 {
			t.Errorf("expected deleted=0 for max_age_days=0, got %d", resp.Deleted)
		}
		if store.deleteOldCalled {
			t.Error("expected DeleteOldConversations NOT to be called when max_age_days=0")
		}
	})

	t.Run("method_not_allowed", func(t *testing.T) {
		t.Parallel()
		store := &mockConversationStore{}
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
			ConversationStore: store,
		})
		ts := httptest.NewServer(New(runner))
		defer ts.Close()

		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/conversations/cleanup", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET cleanup: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", res.StatusCode)
		}
	})

	t.Run("store_error_returns_500", func(t *testing.T) {
		t.Parallel()
		store := &mockConversationStore{deleteOldErr: errors.New("db failure")}
		runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
			ConversationStore: store,
		})
		ts := httptest.NewServer(New(runner))
		defer ts.Close()

		res, err := http.Post(ts.URL+"/v1/conversations/cleanup", "application/json", bytes.NewBufferString(`{"max_age_days":30}`))
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 500, got %d: %s", res.StatusCode, body)
		}
	})
}

// TestListRunsEndpoint verifies GET /v1/runs with a store configured.
func TestListRunsEndpoint(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	runs := []*store.Run{
		{ID: "r1", ConversationID: "conv-A", Status: store.RunStatusCompleted, Prompt: "p1", CreatedAt: now.Add(-2 * time.Second), UpdatedAt: now.Add(-1 * time.Second)},
		{ID: "r2", ConversationID: "conv-A", Status: store.RunStatusRunning, Prompt: "p2", CreatedAt: now, UpdatedAt: now},
		{ID: "r3", ConversationID: "conv-B", Status: store.RunStatusCompleted, Prompt: "p3", CreatedAt: now.Add(-5 * time.Second), UpdatedAt: now.Add(-4 * time.Second)},
	}
	for _, r := range runs {
		if err := ms.CreateRun(ctx, r); err != nil {
			t.Fatalf("seed run %s: %v", r.ID, err)
		}
	}

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	handler := NewWithOptions(ServerOptions{Runner: runner, Store: ms, AuthDisabled: true})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	t.Run("list_all", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/v1/runs")
		if err != nil {
			t.Fatalf("GET /v1/runs: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}
		var payload struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(payload.Runs) != 3 {
			t.Fatalf("expected 3 runs, got %d", len(payload.Runs))
		}
	})

	t.Run("filter_by_conversation", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/v1/runs?conversation_id=conv-A")
		if err != nil {
			t.Fatalf("GET /v1/runs?conversation_id=conv-A: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}
		var payload struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(payload.Runs) != 2 {
			t.Fatalf("expected 2 runs for conv-A, got %d", len(payload.Runs))
		}
	})

	t.Run("filter_by_status", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/v1/runs?status=completed")
		if err != nil {
			t.Fatalf("GET /v1/runs?status=completed: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}
		var payload struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(payload.Runs) != 2 {
			t.Fatalf("expected 2 completed runs, got %d", len(payload.Runs))
		}
	})

	t.Run("no_store_returns_501", func(t *testing.T) {
		runner2 := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
		ts2 := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner2, AuthDisabled: true}))
		defer ts2.Close()

		res, err := http.Get(ts2.URL + "/v1/runs")
		if err != nil {
			t.Fatalf("GET /v1/runs (no store): %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Fatalf("expected 501, got %d", res.StatusCode)
		}
	})
}

// TestConversationRunsEndpoint verifies GET /v1/conversations/{id}/runs.
func TestConversationRunsEndpoint(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	runs := []*store.Run{
		{ID: "cr1", ConversationID: "conv-X", Status: store.RunStatusCompleted, Prompt: "first", CreatedAt: now.Add(-3 * time.Second), UpdatedAt: now.Add(-2 * time.Second)},
		{ID: "cr2", ConversationID: "conv-X", Status: store.RunStatusCompleted, Prompt: "second", CreatedAt: now, UpdatedAt: now},
		{ID: "cr3", ConversationID: "conv-Y", Status: store.RunStatusCompleted, Prompt: "other", CreatedAt: now.Add(-1 * time.Second), UpdatedAt: now},
	}
	for _, r := range runs {
		if err := ms.CreateRun(ctx, r); err != nil {
			t.Fatalf("seed run %s: %v", r.ID, err)
		}
	}

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
	handler := NewWithOptions(ServerOptions{Runner: runner, Store: ms, AuthDisabled: true})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	t.Run("returns_runs_for_conversation", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/v1/conversations/conv-X/runs")
		if err != nil {
			t.Fatalf("GET /v1/conversations/conv-X/runs: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
		}
		var payload struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(payload.Runs) != 2 {
			t.Fatalf("expected 2 runs for conv-X, got %d", len(payload.Runs))
		}
		// Verify conversation_id field is present
		for _, r := range payload.Runs {
			if r["conversation_id"] != "conv-X" {
				t.Errorf("expected conversation_id=conv-X, got %v", r["conversation_id"])
			}
		}
	})

	t.Run("empty_result_for_unknown_conversation", func(t *testing.T) {
		res, err := http.Get(ts.URL + "/v1/conversations/unknown-conv/runs")
		if err != nil {
			t.Fatalf("GET /v1/conversations/unknown-conv/runs: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("expected 200 (empty list), got %d: %s", res.StatusCode, body)
		}
		var payload struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(payload.Runs) != 0 {
			t.Fatalf("expected 0 runs for unknown-conv, got %d", len(payload.Runs))
		}
	})

	t.Run("no_store_returns_501", func(t *testing.T) {
		runner2 := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{})
		ts2 := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner2, AuthDisabled: true}))
		defer ts2.Close()

		res, err := http.Get(ts2.URL + "/v1/conversations/conv-X/runs")
		if err != nil {
			t.Fatalf("GET /v1/conversations/conv-X/runs (no store): %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Fatalf("expected 501, got %d", res.StatusCode)
		}
	})

	t.Run("post_method_not_allowed", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/conversations/conv-X/runs", bytes.NewBufferString(`{}`))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/conversations/conv-X/runs: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for POST, got %d", res.StatusCode)
		}
	})
}

// TestStoreRunFallback tests the GET /v1/runs/{id} fallback path that uses storeRunToHarness.
// It seeds a run directly into the MemoryStore (bypassing the runner) and verifies
// that the server falls back to the store when the runner has no record for the ID.
func TestStoreRunFallback(t *testing.T) {
	t.Parallel()

	memStore := store.NewMemoryStore()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		registry,
		harness.RunnerConfig{},
	)
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, Store: memStore, AuthDisabled: true}))
	defer ts.Close()

	// Seed a run directly in the store (not via the runner — simulates a historical run).
	ctx := context.Background()
	seededRun := &store.Run{
		ID:             "historical-run-1",
		ConversationID: "hist-conv",
		TenantID:       "",
		AgentID:        "agent-test",
		Model:          "gpt-4",
		ProviderName:   "openai",
		Prompt:         "list primes",
		Status:         store.RunStatusCompleted,
		Output:         "2 3 5 7",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := memStore.CreateRun(ctx, seededRun); err != nil {
		t.Fatalf("seed run in store: %v", err)
	}

	// The runner has no knowledge of this run, so the server must fall back to the store.
	res, err := http.Get(ts.URL + "/v1/runs/historical-run-1")
	if err != nil {
		t.Fatalf("GET /v1/runs/historical-run-1: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := payload["id"].(string); !ok || got != "historical-run-1" {
		t.Errorf("expected id=historical-run-1, got %v", payload["id"])
	}
	if got, ok := payload["output"].(string); !ok || got != "2 3 5 7" {
		t.Errorf("expected output=2 3 5 7, got %v", payload["output"])
	}
}

// TestHarnessRunToStore tests that POST /v1/runs persists the run to the store
// when the runner owns run-record persistence and shares that store with the server.
func TestHarnessRunToStore(t *testing.T) {
	t.Parallel()

	memStore := store.NewMemoryStore()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "You are helpful.",
			MaxSteps:            1,
			Store:               memStore,
		},
	)
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, Store: memStore, AuthDisabled: true}))
	defer ts.Close()

	// Start a run via the server — the runner should persist it to the shared store.
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"Hello"}`))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 202, got %d: %s", res.StatusCode, body)
	}

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("expected non-empty run_id")
	}

	// The store should have a record for this run via runner-owned persistence.
	ctx := context.Background()
	// Poll briefly since the run is async.
	var storeRun *store.Run
	for i := 0; i < 50; i++ {
		r, err := memStore.GetRun(ctx, created.RunID)
		if err == nil {
			storeRun = r
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if storeRun == nil {
		t.Fatalf("run %q was not persisted to store after POST /v1/runs", created.RunID)
	}
	if storeRun.Prompt != "Hello" {
		t.Errorf("store run prompt: got %q, want Hello", storeRun.Prompt)
	}
}

func providerTestCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		CatalogVersion: "v1-test",
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				BaseURL:     "https://api.openai.com",
				APIKeyEnv:   "OPENAI_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"gpt-4.1-mini": {
						DisplayName:   "GPT-4.1 Mini",
						ContextWindow: 128000,
					},
				},
			},
			"groq": {
				DisplayName: "Groq",
				BaseURL:     "https://api.groq.com",
				APIKeyEnv:   "GROQ_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"llama-3": {
						DisplayName:   "Llama 3",
						ContextWindow: 8192,
					},
				},
			},
		},
	}
}

func TestHandleSetProviderKey_204(t *testing.T) {
	t.Parallel()
	cat := providerTestCatalog()
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	handler := NewWithOptions(ServerOptions{
		Catalog:          cat,
		ProviderRegistry: reg,
		AuthDisabled:     true,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body := bytes.NewBufferString(`{"key":"sk-test-key-123"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/providers/groq/key", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 204, got %d: %s", res.StatusCode, string(respBody))
	}
}

func TestHandleSetProviderKey_400(t *testing.T) {
	t.Parallel()
	cat := providerTestCatalog()
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	handler := NewWithOptions(ServerOptions{
		Catalog:          cat,
		ProviderRegistry: reg,
		AuthDisabled:     true,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Empty key should return 400.
	body := bytes.NewBufferString(`{"key":""}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/providers/groq/key", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}

	// Invalid JSON should also return 400.
	body2 := bytes.NewBufferString(`not json`)
	req2, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/providers/groq/key", body2)
	req2.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer res2.Body.Close()

	if res2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", res2.StatusCode)
	}
}

func TestGetProviders_ReflectsOverride(t *testing.T) {
	t.Parallel()
	cat := providerTestCatalog()
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	handler := NewWithOptions(ServerOptions{
		Catalog:          cat,
		ProviderRegistry: reg,
		AuthDisabled:     true,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Before override, groq should not be configured.
	res, err := http.Get(ts.URL + "/v1/providers")
	if err != nil {
		t.Fatalf("GET providers: %v", err)
	}
	defer res.Body.Close()

	var resp struct {
		Providers []ProviderResponse `json:"providers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	for _, p := range resp.Providers {
		if p.Name == "groq" && p.Configured {
			t.Fatal("expected groq not configured before override")
		}
	}

	// Set API key via PUT.
	body := bytes.NewBufferString(`{"key":"sk-test"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/providers/groq/key", body)
	req.Header.Set("Content-Type", "application/json")
	putRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	putRes.Body.Close()

	// After override, groq should be configured.
	res2, err := http.Get(ts.URL + "/v1/providers")
	if err != nil {
		t.Fatalf("GET providers after override: %v", err)
	}
	defer res2.Body.Close()

	var resp2 struct {
		Providers []ProviderResponse `json:"providers"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	found := false
	for _, p := range resp2.Providers {
		if p.Name == "groq" {
			found = true
			if !p.Configured {
				t.Fatal("expected groq configured after override")
			}
		}
	}
	if !found {
		t.Fatal("groq not found in providers response")
	}
}
