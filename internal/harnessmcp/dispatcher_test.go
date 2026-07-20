package harnessmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestT1_Initialize verifies that initialize returns correct protocol version,
// serverInfo.name, and capabilities.tools.
func TestT1_Initialize(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`1`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}`),
	}

	resp, shouldRespond := d.Dispatch(context.Background(), req)
	if !shouldRespond {
		t.Fatal("expected shouldRespond=true for initialize")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("got protocolVersion %q, want %q", result.ProtocolVersion, "2025-11-25")
	}
	if result.ServerInfo.Name == "" {
		t.Error("serverInfo.name must not be empty")
	}
	if result.Capabilities.Tools == nil {
		t.Error("capabilities.tools must not be nil")
	}
}

// TestT2_ToolsList verifies that tools/list returns exactly 5 tools, each with
// a non-empty description and valid inputSchema.
func TestT2_ToolsList(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`2`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/list",
	}

	resp, shouldRespond := d.Dispatch(context.Background(), req)
	if !shouldRespond {
		t.Fatal("expected shouldRespond=true")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expectedTools := []string{"start_run", "get_run_status", "wait_for_run", "continue_run", "list_runs"}
	if len(result.Tools) != len(expectedTools) {
		t.Errorf("got %d tools, want %d", len(result.Tools), len(expectedTools))
	}

	toolsByName := make(map[string]Tool)
	for _, tool := range result.Tools {
		toolsByName[tool.Name] = tool
	}

	for _, name := range expectedTools {
		tool, ok := toolsByName[name]
		if !ok {
			t.Errorf("missing tool %q", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if tool.InputSchema.Type == "" {
			t.Errorf("tool %q has empty inputSchema.type", name)
		}
	}
}

// TestT3_ToolsCall_StartRun verifies start_run POSTs to /v1/runs and returns run_id.
func TestT3_ToolsCall_StartRun(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/runs" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "run-t3"})
		}
	}))
	defer srv.Close()

	client := NewHarnessClient(srv.URL)
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`3`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"start_run","arguments":{"prompt":"hello world"}}`),
	}

	resp, shouldRespond := d.Dispatch(context.Background(), req)
	if !shouldRespond {
		t.Fatal("expected shouldRespond=true")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}

	var toolResult ToolResult
	if err := json.Unmarshal(resp.Result, &toolResult); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if toolResult.IsError {
		t.Errorf("unexpected tool error: %v", toolResult.Content)
	}
	if len(toolResult.Content) == 0 {
		t.Fatal("expected at least one content block")
	}

	// Verify the run_id is in the content.
	var content map[string]string
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &content); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if content["run_id"] != "run-t3" {
		t.Errorf("got run_id %q, want %q", content["run_id"], "run-t3")
	}

	// Verify the POST body.
	if capturedBody["prompt"] != "hello world" {
		t.Errorf("got prompt %v, want %q", capturedBody["prompt"], "hello world")
	}
}

// TestT4_ToolsCall_GetRunStatus verifies get_run_status GETs /v1/runs/{id}.
func TestT4_ToolsCall_GetRunStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run-t4" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(RunStatus{
				RunID:  "run-t4",
				Status: "completed",
			})
		}
	}))
	defer srv.Close()

	client := NewHarnessClient(srv.URL)
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`4`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"get_run_status","arguments":{"run_id":"run-t4"}}`),
	}

	resp, _ := d.Dispatch(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}

	var toolResult ToolResult
	_ = json.Unmarshal(resp.Result, &toolResult)
	if toolResult.IsError {
		t.Errorf("unexpected tool error: %v", toolResult.Content)
	}
	if len(toolResult.Content) == 0 {
		t.Fatal("expected content")
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &status); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if status["status"] != "completed" {
		t.Errorf("got status %v, want %q", status["status"], "completed")
	}
}

// TestT5_WaitForRun_Polls tests that wait_for_run polls until terminal status.
// Mock returns "running" twice then "completed". Verifies 3 HTTP calls total.
func TestT5_WaitForRun_Polls(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run-t5" {
			callCount++
			status := "running"
			if callCount >= 3 {
				status = "completed"
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(RunStatus{
				RunID:  "run-t5",
				Status: status,
			})
		}
	}))
	defer srv.Close()

	client := NewHarnessClient(srv.URL)

	// Use a fast mock clock that fires immediately.
	clock := &mockClock{}
	d := NewDispatcher(client, clock)

	idRaw := json.RawMessage(`5`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"wait_for_run","arguments":{"run_id":"run-t5","timeout_seconds":30}}`),
	}

	resp, _ := d.Dispatch(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}

	var toolResult ToolResult
	_ = json.Unmarshal(resp.Result, &toolResult)
	if toolResult.IsError {
		t.Errorf("unexpected tool error: %v", toolResult.Content)
	}

	if callCount != 3 {
		t.Errorf("got %d HTTP calls, want 3", callCount)
	}
}

// TestT6_WaitForRun_Timeout tests that wait_for_run times out when the run
// never completes and timeout_seconds is short.
func TestT6_WaitForRun_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(RunStatus{
				RunID:  "run-t6",
				Status: "running",
			})
		}
	}))
	defer srv.Close()

	client := NewHarnessClient(srv.URL)

	// Use a mock clock that immediately fires the timeout but slow polls.
	clock := &mockClockWithTimeout{}
	d := NewDispatcher(client, clock)

	idRaw := json.RawMessage(`6`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"wait_for_run","arguments":{"run_id":"run-t6","timeout_seconds":1}}`),
	}

	resp, _ := d.Dispatch(context.Background(), req)
	var toolResult ToolResult
	_ = json.Unmarshal(resp.Result, &toolResult)

	if !toolResult.IsError {
		t.Error("expected isError=true for timeout")
	}
	if len(toolResult.Content) == 0 {
		t.Fatal("expected content in error result")
	}
	if toolResult.Content[0].Text != "timed out waiting for run run-t6" {
		t.Errorf("got %q, want %q", toolResult.Content[0].Text, "timed out waiting for run run-t6")
	}
}

// TestT7_UnknownTool verifies that calling an unknown tool returns isError=true.
func TestT7_UnknownTool(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`7`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"no_such_tool","arguments":{}}`),
	}

	resp, shouldRespond := d.Dispatch(context.Background(), req)
	if !shouldRespond {
		t.Fatal("expected shouldRespond=true")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}

	var toolResult ToolResult
	if err := json.Unmarshal(resp.Result, &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !toolResult.IsError {
		t.Error("expected isError=true for unknown tool")
	}
}

// TestT10_ContinueRun verifies that continue_run fetches the run for
// conversation_id, then starts a new run with that conversation_id.
func TestT10_ContinueRun(t *testing.T) {
	getCallCount := 0
	postCallCount := 0
	var capturedStartReq StartRunRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run-prev":
			getCallCount++
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(RunStatus{
				RunID:          "run-prev",
				Status:         "completed",
				ConversationID: "conv-abc",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			postCallCount++
			_ = json.NewDecoder(r.Body).Decode(&capturedStartReq)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "run-new"})
		}
	}))
	defer srv.Close()

	client := NewHarnessClient(srv.URL)
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`10`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"continue_run","arguments":{"run_id":"run-prev","prompt":"follow-up"}}`),
	}

	resp, _ := d.Dispatch(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}

	var toolResult ToolResult
	_ = json.Unmarshal(resp.Result, &toolResult)
	if toolResult.IsError {
		t.Errorf("unexpected tool error: %v", toolResult.Content)
	}

	if getCallCount != 1 {
		t.Errorf("expected 1 GET call, got %d", getCallCount)
	}
	if postCallCount != 1 {
		t.Errorf("expected 1 POST call, got %d", postCallCount)
	}
	if capturedStartReq.ConversationID != "conv-abc" {
		t.Errorf("got conversation_id %q, want %q", capturedStartReq.ConversationID, "conv-abc")
	}
	if capturedStartReq.Prompt != "follow-up" {
		t.Errorf("got prompt %q, want %q", capturedStartReq.Prompt, "follow-up")
	}
}

// TestMethodNotFound verifies unknown methods return -32601.
func TestMethodNotFound(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	idRaw := json.RawMessage(`99`)
	req := Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "unknown/method",
	}

	resp, shouldRespond := d.Dispatch(context.Background(), req)
	if !shouldRespond {
		t.Fatal("expected shouldRespond=true for unknown method")
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("got error code %d, want -32601", resp.Error.Code)
	}
}

// TestNotification_Initialized verifies that initialized notification returns shouldRespond=false.
func TestNotification_Initialized(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	req := Request{
		JSONRPC: "2.0",
		Method:  "initialized",
	}

	_, shouldRespond := d.Dispatch(context.Background(), req)
	if shouldRespond {
		t.Error("expected shouldRespond=false for initialized notification")
	}
}

// TestNotification_CancelRequest verifies $/cancelRequest doesn't respond.
func TestNotification_CancelRequest(t *testing.T) {
	client := NewHarnessClient("http://localhost:9999")
	d := NewDispatcher(client, RealClock{})

	req := Request{
		JSONRPC: "2.0",
		Method:  "$/cancelRequest",
		Params:  json.RawMessage(`{"id":1}`),
	}

	_, shouldRespond := d.Dispatch(context.Background(), req)
	if shouldRespond {
		t.Error("expected shouldRespond=false for $/cancelRequest")
	}
}

// mockClock fires poll delays immediately but never fires the timeout channel.
// wait_for_run creates the timeout channel first (call 1), then poll channels
// inside the loop (calls 2+). By never firing the timeout, we let polling proceed
// until a terminal state is reached.
type mockClock struct {
	mu        sync.Mutex
	callCount int
}

func (m *mockClock) Now() time.Time { return time.Now() }
func (m *mockClock) After(d time.Duration) <-chan time.Time {
	m.mu.Lock()
	m.callCount++
	n := m.callCount
	m.mu.Unlock()

	ch := make(chan time.Time, 1)
	if n == 1 {
		// First call: this is the timeout channel — never fire it so tests can complete.
		// Leave ch empty.
	} else {
		// Subsequent calls: poll delay — fire immediately.
		ch <- time.Now()
	}
	return ch
}

// mockClockWithTimeout simulates a timeout scenario.
// The first After() call (the timeout channel) fires immediately.
// Subsequent calls (poll delays) never fire, ensuring the timeout wins the select.
type mockClockWithTimeout struct {
	mu        sync.Mutex
	callCount int
}

func (m *mockClockWithTimeout) Now() time.Time { return time.Now() }
func (m *mockClockWithTimeout) After(d time.Duration) <-chan time.Time {
	m.mu.Lock()
	m.callCount++
	n := m.callCount
	m.mu.Unlock()

	ch := make(chan time.Time, 1)
	if n == 1 {
		// First call: the timeout channel — fire immediately to simulate instant timeout.
		ch <- time.Now()
	}
	// Subsequent calls (poll delays): never fire, so select always picks timeout.
	return ch
}
