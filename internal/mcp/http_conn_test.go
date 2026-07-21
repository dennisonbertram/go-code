package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

// --- Compile-time interface check ---

var _ mcp.Conn = (*mcp.HTTPConnForTest)(nil)

// --- Mock MCP HTTP server helper ---

// mockHTTPMCPServerOpts allows customizing the mock's behavior.
type mockHTTPMCPServerOpts struct {
	// tools are the tools the server advertises.
	tools []mcp.ToolDef
	// initDelay delays the initialize response.
	initDelay time.Duration
	// rejectVersion2025 causes initialize to fail with -32602 when the client
	// sends protocolVersion "2025-11-25", simulating version negotiation.
	rejectVersion2025 bool
	// callToolIsError makes tools/call return isError:true.
	callToolIsError bool
	// callToolJSONRPCError makes tools/call return a JSON-RPC error field.
	callToolJSONRPCError bool
	// non2xxStatus causes the server to return this HTTP status.
	non2xxStatus int
	// respondAsSSE causes the server to respond with text/event-stream.
	respondAsSSE bool
}

func newMockMCPHTTPServer(t *testing.T, opts mockHTTPMCPServerOpts) *httptest.Server {
	t.Helper()
	var requestCount atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)

		if opts.non2xxStatus != 0 {
			w.WriteHeader(opts.non2xxStatus)
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			ID      json.RawMessage `json:"id"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var resp map[string]any

		switch req.Method {
		case "initialize":
			if opts.initDelay > 0 {
				time.Sleep(opts.initDelay)
			}
			// Parse params to check protocolVersion.
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &params)

			if opts.rejectVersion2025 && params.ProtocolVersion == "2025-11-25" {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32602, "message": "unsupported protocol version"},
				}
			} else {
				negotiated := params.ProtocolVersion
				if negotiated == "" {
					negotiated = "2024-11-05"
				}
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocolVersion": negotiated,
						"capabilities":    map[string]any{"tools": map[string]any{}},
						"serverInfo":      map[string]any{"name": "test-http-server", "version": "1.0"},
					},
				}
			}

		case "tools/list":
			toolList := make([]map[string]any, 0, len(opts.tools))
			for _, t := range opts.tools {
				entry := map[string]any{
					"name":        t.Name,
					"description": t.Description,
				}
				if t.InputSchema != nil {
					entry["inputSchema"] = json.RawMessage(t.InputSchema)
				} else {
					entry["inputSchema"] = map[string]any{"type": "object", "properties": map[string]any{}}
				}
				toolList = append(toolList, entry)
			}
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"tools": toolList},
			}

		case "tools/call":
			var callParams struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &callParams)

			if opts.callToolJSONRPCError {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32000, "message": "tool execution failed"},
				}
			} else if opts.callToolIsError {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "something went wrong"},
						},
						"isError": true,
					},
				}
			} else {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": fmt.Sprintf(`{"tool":"%s","called":true}`, callParams.Name)},
						},
					},
				}
			}

		default:
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			}
		}

		data, _ := json.Marshal(resp)

		if opts.respondAsSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
}

// newMockMCPServer is a convenience wrapper matching the spec helper name.
func newMockMCPServer(t *testing.T, tools []mcp.ToolDef) *httptest.Server {
	t.Helper()
	return newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{tools: tools})
}

// --- Unit Tests ---

func TestHTTPConn_ImplementsConn(t *testing.T) {
	t.Parallel()
	// Compile-time check is done above with var _ mcp.Conn = (*mcp.HTTPConnForTest)(nil)
}

func TestHTTPConn_Initialize_Success(t *testing.T) {
	t.Parallel()

	var receivedVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedVersion = req.Params.ProtocolVersion

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
			},
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if receivedVersion != "2025-11-25" {
		t.Errorf("expected protocolVersion 2025-11-25, got %q", receivedVersion)
	}
	if v := conn.NegotiatedVersion(); v != "2025-11-25" {
		t.Errorf("negotiatedVersion = %q, want 2025-11-25", v)
	}
}

func TestHTTPConn_Initialize_VersionNegotiation_Downgrade(t *testing.T) {
	t.Parallel()

	var postCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := postCount.Add(1)
		var req struct {
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		var resp map[string]any
		if n == 1 && req.Params.ProtocolVersion == "2025-11-25" {
			// Reject the newer version.
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32602, "message": "unsupported protocol version"},
			}
		} else {
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
				},
			}
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if got := postCount.Load(); got != 2 {
		t.Errorf("expected 2 POSTs (initial + retry), got %d", got)
	}
	if v := conn.NegotiatedVersion(); v != "2024-11-05" {
		t.Errorf("negotiatedVersion = %q, want 2024-11-05", v)
	}
}

func TestHTTPConn_Initialize_VersionNegotiation_OlderVersionAccepted(t *testing.T) {
	t.Parallel()

	var postCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		postCount.Add(1)
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Accept with 2024-11-05 on first try (no error).
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "test", "version": "1.0"},
			},
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if got := postCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 POST, got %d", got)
	}
	if v := conn.NegotiatedVersion(); v != "2024-11-05" {
		t.Errorf("negotiatedVersion = %q, want 2024-11-05", v)
	}
}

func TestHTTPConn_ListTools(t *testing.T) {
	t.Parallel()

	tools := []mcp.ToolDef{
		{Name: "search", Description: "Search for things"},
		{Name: "create", Description: "Create a resource"},
	}
	srv := newMockMCPServer(t, tools)
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[0].Name != "search" || got[1].Name != "create" {
		t.Errorf("unexpected tools: %v", got)
	}
}

func TestHTTPConn_CallTool_Success(t *testing.T) {
	t.Parallel()

	srv := newMockMCPServer(t, []mcp.ToolDef{{Name: "greet", Description: "Say hi"}})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	result, err := conn.CallTool(ctx, "greet", json.RawMessage(`{"name":"world"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(result, "greet") {
		t.Errorf("expected result to contain 'greet', got %q", result)
	}
}

func TestHTTPConn_CallTool_IsError(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		tools:           []mcp.ToolDef{{Name: "fail", Description: "Fails"}},
		callToolIsError: true,
	})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := conn.CallTool(ctx, "fail", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from CallTool with isError:true, got nil")
	}
}

func TestHTTPConn_CallTool_JSONRPCError(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		tools:                []mcp.ToolDef{{Name: "broken", Description: "Broken"}},
		callToolJSONRPCError: true,
	})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := conn.CallTool(ctx, "broken", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from CallTool with JSON-RPC error, got nil")
	}
}

func TestHTTPConn_ContextCancellation(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		tools:     []mcp.ToolDef{{Name: "slow", Description: "Slow"}},
		initDelay: 500 * time.Millisecond,
	})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := conn.Initialize(ctx)
	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context-related error, got %q", err.Error())
	}
}

func TestHTTPConn_ServerNon2xx(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		non2xxStatus: 503,
	})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := conn.Initialize(ctx)
	if err == nil {
		t.Fatal("expected error from 503 response, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected '503' in error, got %q", err.Error())
	}
}

func TestHTTPConn_Close_IdempotentAndConcurrentlySafe(t *testing.T) {
	t.Parallel()

	srv := newMockMCPServer(t, nil)
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = conn.Close()
		}()
	}
	wg.Wait()
	// No panic = pass.
}

func TestHTTPConn_SSEResponse(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		tools:        []mcp.ToolDef{{Name: "sse-tool", Description: "SSE tool"}},
		respondAsSSE: true,
	})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize over SSE: %v", err)
	}

	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools over SSE: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "sse-tool" {
		t.Errorf("unexpected tools from SSE: %v", tools)
	}

	result, err := conn.CallTool(ctx, "sse-tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool over SSE: %v", err)
	}
	if !strings.Contains(result, "sse-tool") {
		t.Errorf("expected 'sse-tool' in result, got %q", result)
	}
}

// --- Integration Tests ---

func TestClientManager_AddServer_HTTP_DiscoverTools(t *testing.T) {
	t.Parallel()

	tools := []mcp.ToolDef{
		{Name: "alpha", Description: "Alpha tool"},
		{Name: "beta", Description: "Beta tool"},
	}
	srv := newMockMCPServer(t, tools)
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "http-test",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := cm.DiscoverTools(ctx, "http-test")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
}

func TestClientManager_AddServer_HTTP_ExecuteTool(t *testing.T) {
	t.Parallel()

	srv := newMockMCPServer(t, []mcp.ToolDef{{Name: "ping", Description: "Ping"}})
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "http-exec",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := cm.ExecuteTool(ctx, "http-exec", "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !strings.Contains(result, "ping") {
		t.Errorf("expected 'ping' in result, got %q", result)
	}
}

func TestClientManager_HTTP_ProtocolVersionNegotiation_Integration(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{
		tools:             []mcp.ToolDef{{Name: "v1tool", Description: "Old tool"}},
		rejectVersion2025: true,
	})
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "http-negotiation",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := cm.DiscoverTools(ctx, "http-negotiation")
	if err != nil {
		t.Fatalf("DiscoverTools with negotiation: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "v1tool" {
		t.Errorf("unexpected tools: %v", tools)
	}
}

// --- Race Tests ---

func TestHTTPConn_ConcurrentCallTool_Race(t *testing.T) {
	t.Parallel()

	srv := newMockMCPServer(t, []mcp.ToolDef{{Name: "concurrent", Description: "Concurrent tool"}})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			args := json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))
			_, err := conn.CallTool(ctx, "concurrent", args)
			errs <- err
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent CallTool error: %v", err)
		}
	}
}

// TestDialHTTP_InvalidScheme_Rejected verifies that dialHTTP rejects URLs with
// non-http/https schemes to prevent SSRF via file://, gopher://, etc.
func TestDialHTTP_InvalidScheme_Rejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://evil.com"},
		{"ftp scheme", "ftp://files.example.com"},
		{"javascript scheme", "javascript:alert(1)"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := mcp.DialHTTPForTest(mcp.ServerConfig{
				Name:      "test-server",
				Transport: "http",
				URL:       tc.url,
			})
			if err == nil {
				t.Fatalf("dialHTTP(%q): expected error for non-http/https scheme, got nil", tc.url)
			}
			if !strings.Contains(err.Error(), "http or https") {
				t.Errorf("dialHTTP(%q): error %q does not mention \"http or https\"", tc.url, err.Error())
			}
		})
	}
}

// TestDialHTTP_ValidSchemes_Accepted verifies that dialHTTP accepts http and https URLs.
func TestDialHTTP_ValidSchemes_Accepted(t *testing.T) {
	t.Parallel()

	cases := []string{
		"http://localhost:8080",
		"https://example.com/mcp",
	}

	for _, u := range cases {
		u := u
		t.Run(u, func(t *testing.T) {
			t.Parallel()
			conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
				Name:      "test-server",
				Transport: "http",
				URL:       u,
			})
			if err != nil {
				t.Fatalf("dialHTTP(%q): unexpected error: %v", u, err)
			}
			_ = conn.Close()
		})
	}
}

func TestClientManager_HTTP_ConcurrentDiscoverAndExecute_Race(t *testing.T) {
	t.Parallel()

	srv1 := newMockMCPServer(t, []mcp.ToolDef{{Name: "t1", Description: "Tool 1"}})
	defer srv1.Close()
	srv2 := newMockMCPServer(t, []mcp.ToolDef{{Name: "t2", Description: "Tool 2"}})
	defer srv2.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	if err := cm.AddServer(mcp.ServerConfig{
		Name: "srv1", Transport: "http", URL: srv1.URL,
	}); err != nil {
		t.Fatalf("AddServer srv1: %v", err)
	}
	if err := cm.AddServer(mcp.ServerConfig{
		Name: "srv2", Transport: "http", URL: srv2.URL,
	}); err != nil {
		t.Fatalf("AddServer srv2: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 40)

	// 10 goroutines discovering + 10 goroutines executing on each server.
	for i := 0; i < 10; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_, err := cm.DiscoverTools(ctx, "srv1")
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := cm.DiscoverTools(ctx, "srv2")
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := cm.ExecuteTool(ctx, "srv1", "t1", json.RawMessage(`{}`))
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := cm.ExecuteTool(ctx, "srv2", "t2", json.RawMessage(`{}`))
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent operation error: %v", err)
		}
	}
}

// --- Slice 1 (epic #809): static headers and typed auth errors ---

// headerRecordingServer is a mock MCP HTTP server that records the headers of
// every request, keyed by JSON-RPC method, and optionally requires a specific
// Authorization header value (responding 401 otherwise).
type headerRecordingServer struct {
	t *testing.T

	mu          sync.Mutex
	gotHeaders  map[string]http.Header // method -> headers of last request
	requireAuth string                 // if non-empty, required Authorization header value
}

func newHeaderRecordingServer(t *testing.T, requireAuth string) (*httptest.Server, *headerRecordingServer) {
	t.Helper()
	rec := &headerRecordingServer{t: t, gotHeaders: make(map[string]http.Header), requireAuth: requireAuth}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	return srv, rec
}

func (rec *headerRecordingServer) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	rec.mu.Lock()
	rec.gotHeaders[req.Method] = r.Header.Clone()
	rec.mu.Unlock()

	if rec.requireAuth != "" && r.Header.Get("Authorization") != rec.requireAuth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
	}
	switch req.Method {
	case "initialize":
		resp["result"] = map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "rec", "version": "1.0"},
		}
	case "tools/list":
		resp["result"] = map[string]any{"tools": []map[string]any{
			{"name": "ping", "description": "Ping", "inputSchema": map[string]any{"type": "object"}},
		}}
	case "tools/call":
		resp["result"] = map[string]any{
			"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}},
		}
	default:
		resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}

	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (rec *headerRecordingServer) headerForMethod(method, key string) string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.gotHeaders[method].Get(key)
}

// TestHTTPConn_HeadersAttachedToEveryRequest verifies that every configured
// static header is sent on initialize, tools/list, and tools/call requests.
func TestHTTPConn_HeadersAttachedToEveryRequest(t *testing.T) {
	t.Parallel()

	srv, rec := newHeaderRecordingServer(t, "")
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "hdr-test",
		Transport: "http",
		URL:       srv.URL,
		Headers: map[string]string{
			"Authorization": "Bearer static-token",
			"X-Tenant":      "acme",
		},
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := conn.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := conn.CallTool(ctx, "ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	for _, method := range []string{"initialize", "tools/list", "tools/call"} {
		if got := rec.headerForMethod(method, "Authorization"); got != "Bearer static-token" {
			t.Errorf("%s: Authorization header = %q, want %q", method, got, "Bearer static-token")
		}
		if got := rec.headerForMethod(method, "X-Tenant"); got != "acme" {
			t.Errorf("%s: X-Tenant header = %q, want %q", method, got, "acme")
		}
		// Protocol headers must remain intact when custom headers are set.
		if got := rec.headerForMethod(method, "Content-Type"); got != "application/json" {
			t.Errorf("%s: Content-Type header = %q, want %q", method, got, "application/json")
		}
	}
}

// TestHTTPConn_NoHeadersConfigured_SendsNoAuthorization verifies the zero
// value behavior: no Authorization header is sent when none is configured.
func TestHTTPConn_NoHeadersConfigured_SendsNoAuthorization(t *testing.T) {
	t.Parallel()

	srv, rec := newHeaderRecordingServer(t, "")
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "no-hdr",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := rec.headerForMethod("initialize", "Authorization"); got != "" {
		t.Errorf("Authorization header = %q, want empty", got)
	}
}

// TestHTTPConn_TypedAuthErrors verifies that 401 and 403 responses map to the
// exported sentinel errors (errors.Is-compatible) while other non-2xx
// statuses keep the existing generic error behavior.
func TestHTTPConn_TypedAuthErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		status       int
		sentinel     error // nil means no auth sentinel expected
		wantContains string
	}{
		{"401 maps to ErrUnauthorized", http.StatusUnauthorized, mcp.ErrUnauthorized, "401"},
		{"403 maps to ErrForbidden", http.StatusForbidden, mcp.ErrForbidden, "403"},
		{"400 stays generic", http.StatusBadRequest, nil, "400"},
		{"500 stays generic", http.StatusInternalServerError, nil, "500"},
		{"503 stays generic", http.StatusServiceUnavailable, nil, "503"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{non2xxStatus: tc.status})
			defer srv.Close()

			conn := mcp.NewHTTPConnForTest("typed-err", srv.URL)
			defer conn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := conn.Initialize(ctx)
			if err == nil {
				t.Fatalf("expected error from %d response, got nil", tc.status)
			}

			if tc.sentinel != nil {
				if !errors.Is(err, tc.sentinel) {
					t.Errorf("errors.Is(err, %v) = false for error %q", tc.sentinel, err.Error())
				}
			} else {
				if errors.Is(err, mcp.ErrUnauthorized) {
					t.Errorf("errors.Is(err, ErrUnauthorized) = true for %d, want false", tc.status)
				}
				if errors.Is(err, mcp.ErrForbidden) {
					t.Errorf("errors.Is(err, ErrForbidden) = true for %d, want false", tc.status)
				}
			}

			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Errorf("error %q does not contain status %q", err.Error(), tc.wantContains)
			}
		})
	}
}

// TestHTTPConn_CallTool_TypedAuthError verifies the typed auth error also
// surfaces from tools/call, not just initialize.
func TestHTTPConn_CallTool_TypedAuthError(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{non2xxStatus: http.StatusUnauthorized})
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("call-typed", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := conn.CallTool(ctx, "anything", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from 401 response, got nil")
	}
	if !errors.Is(err, mcp.ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false for error %q", err.Error())
	}
}

// TestClientManager_HTTP_TypedAuthErrorSurfaced verifies that the typed 401
// error survives the ClientManager's error wrapping so callers of
// DiscoverTools/ExecuteTool can distinguish auth failures with errors.Is.
func TestClientManager_HTTP_TypedAuthErrorSurfaced(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{non2xxStatus: http.StatusUnauthorized})
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	if err := cm.AddServer(mcp.ServerConfig{
		Name:      "authed",
		Transport: "http",
		URL:       srv.URL,
	}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cm.DiscoverTools(ctx, "authed")
	if err == nil {
		t.Fatal("expected error from 401 server, got nil")
	}
	if !errors.Is(err, mcp.ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false through ClientManager for error %q", err.Error())
	}
}

// TestClientManager_HTTP_BearerGatedServer_ReachableViaConfig is the slice
// acceptance test: a mock HTTP MCP server requiring "Authorization: Bearer x"
// is reachable purely via ServerConfig headers — both DiscoverTools and
// ExecuteTool succeed.
func TestClientManager_HTTP_BearerGatedServer_ReachableViaConfig(t *testing.T) {
	t.Parallel()

	srv, _ := newHeaderRecordingServer(t, "Bearer x")
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	if err := cm.AddServer(mcp.ServerConfig{
		Name:      "gated",
		Transport: "http",
		URL:       srv.URL,
		Headers:   map[string]string{"Authorization": "Bearer x"},
	}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := cm.DiscoverTools(ctx, "gated")
	if err != nil {
		t.Fatalf("DiscoverTools against bearer-gated server: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected tools from gated server: %v", tools)
	}

	result, err := cm.ExecuteTool(ctx, "gated", "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool against bearer-gated server: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("expected 'ok' in tool result, got %q", result)
	}
}

// TestClientManager_HTTP_BearerGatedServer_WithoutHeaders_Unauthorized is the
// negative companion: the same gated server without configured headers fails
// with the typed ErrUnauthorized.
func TestClientManager_HTTP_BearerGatedServer_WithoutHeaders_Unauthorized(t *testing.T) {
	t.Parallel()

	srv, _ := newHeaderRecordingServer(t, "Bearer x")
	defer srv.Close()

	cm := mcp.NewClientManager()
	defer cm.Close()

	if err := cm.AddServer(mcp.ServerConfig{
		Name:      "gated-no-auth",
		Transport: "http",
		URL:       srv.URL,
	}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cm.DiscoverTools(ctx, "gated-no-auth")
	if err == nil {
		t.Fatal("expected error against bearer-gated server without headers, got nil")
	}
	if !errors.Is(err, mcp.ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false for error %q", err.Error())
	}
}

// --- Slice 4 (epic #809): token provider and 401 re-auth guidance ---

// TestHTTPConn_UnauthorizedError_ContainsLoginHint verifies that a 401
// response produces an error that names the server and the exact remediation
// command, while still wrapping ErrUnauthorized.
func TestHTTPConn_UnauthorizedError_ContainsLoginHint(t *testing.T) {
	t.Parallel()

	srv := newMockMCPHTTPServer(t, mockHTTPMCPServerOpts{non2xxStatus: http.StatusUnauthorized})
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "hinted",
		Transport: "http",
		URL:       srv.URL,
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = conn.Initialize(ctx)
	if err == nil {
		t.Fatal("expected error from 401 response, got nil")
	}
	if !errors.Is(err, mcp.ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false for %q", err.Error())
	}
	if !strings.Contains(err.Error(), "harnesscli mcp login hinted") {
		t.Errorf("error %q does not contain the re-auth hint %q", err.Error(), "harnesscli mcp login hinted")
	}
}

// TestHTTPConn_TokenProvider_AttachesBearer verifies that a configured token
// provider supplies the Authorization header end to end: a bearer-gated
// server becomes reachable.
func TestHTTPConn_TokenProvider_AttachesBearer(t *testing.T) {
	t.Parallel()

	srv, rec := newHeaderRecordingServer(t, "Bearer provided-tok")
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "provided",
		Transport: "http",
		URL:       srv.URL,
		TokenProvider: func(context.Context, string) (string, error) {
			return "provided-tok", nil
		},
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize with token provider: %v", err)
	}
	if _, err := conn.ListTools(ctx); err != nil {
		t.Fatalf("ListTools with token provider: %v", err)
	}
	for _, method := range []string{"initialize", "tools/list"} {
		if got := rec.headerForMethod(method, "Authorization"); got != "Bearer provided-tok" {
			t.Errorf("%s: Authorization = %q, want %q", method, got, "Bearer provided-tok")
		}
	}
}

// TestHTTPConn_TokenProvider_EmptyMeansNoHeader verifies that a provider
// returning ("", nil) leaves the request unauthenticated.
func TestHTTPConn_TokenProvider_EmptyMeansNoHeader(t *testing.T) {
	t.Parallel()

	srv, rec := newHeaderRecordingServer(t, "")
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "no-token",
		Transport: "http",
		URL:       srv.URL,
		TokenProvider: func(context.Context, string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := rec.headerForMethod("initialize", "Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

// TestHTTPConn_TokenProvider_StaticHeaderWins verifies that a statically
// configured Authorization header takes precedence over the token provider.
func TestHTTPConn_TokenProvider_StaticHeaderWins(t *testing.T) {
	t.Parallel()

	srv, rec := newHeaderRecordingServer(t, "Bearer static-tok")
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "static-wins",
		Transport: "http",
		URL:       srv.URL,
		Headers:   map[string]string{"Authorization": "Bearer static-tok"},
		TokenProvider: func(context.Context, string) (string, error) {
			return "provided-tok", nil
		},
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := rec.headerForMethod("initialize", "Authorization"); got != "Bearer static-tok" {
		t.Errorf("Authorization = %q, want static header %q", got, "Bearer static-tok")
	}
}

// TestHTTPConn_TokenProvider_ErrorSurfaces verifies that a provider failure
// fails the request with the provider's error.
func TestHTTPConn_TokenProvider_ErrorSurfaces(t *testing.T) {
	t.Parallel()

	srv := newMockMCPServer(t, nil)
	defer srv.Close()

	conn, err := mcp.DialHTTPForTest(mcp.ServerConfig{
		Name:      "provider-fails",
		Transport: "http",
		URL:       srv.URL,
		TokenProvider: func(context.Context, string) (string, error) {
			return "", errors.New("token store exploded")
		},
	})
	if err != nil {
		t.Fatalf("DialHTTPForTest: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = conn.Initialize(ctx)
	if err == nil {
		t.Fatal("expected provider error, got nil")
	}
	if !strings.Contains(err.Error(), "token store exploded") {
		t.Errorf("error %q does not contain the provider failure", err.Error())
	}
}

// TestClientManager_SetTokenProvider verifies the manager-level default
// provider is used by servers registered without an explicit one, and that an
// explicit per-server provider takes precedence.
func TestClientManager_SetTokenProvider(t *testing.T) {
	t.Parallel()

	t.Run("default used when server has none", func(t *testing.T) {
		t.Parallel()

		srv, _ := newHeaderRecordingServer(t, "Bearer x")
		defer srv.Close()

		cm := mcp.NewClientManager()
		defer cm.Close()
		cm.SetTokenProvider(func(context.Context, string) (string, error) { return "x", nil })

		if err := cm.AddServer(mcp.ServerConfig{Name: "gated", Transport: "http", URL: srv.URL}); err != nil {
			t.Fatalf("AddServer: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := cm.DiscoverTools(ctx, "gated"); err != nil {
			t.Fatalf("DiscoverTools with manager default provider: %v", err)
		}
	})

	t.Run("explicit provider overrides default", func(t *testing.T) {
		t.Parallel()

		srv, _ := newHeaderRecordingServer(t, "Bearer x")
		defer srv.Close()

		cm := mcp.NewClientManager()
		defer cm.Close()
		cm.SetTokenProvider(func(context.Context, string) (string, error) { return "x", nil })

		if err := cm.AddServer(mcp.ServerConfig{
			Name:      "gated-explicit",
			Transport: "http",
			URL:       srv.URL,
			TokenProvider: func(context.Context, string) (string, error) {
				return "wrong-token", nil
			},
		}); err != nil {
			t.Fatalf("AddServer: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// The explicit (wrong) provider is used, not the manager default that
		// would succeed — so the call must fail with 401.
		_, err := cm.DiscoverTools(ctx, "gated-explicit")
		if err == nil {
			t.Fatal("expected 401 with the explicit wrong-token provider, got nil")
		}
		if !errors.Is(err, mcp.ErrUnauthorized) {
			t.Errorf("errors.Is(err, ErrUnauthorized) = false for %q", err.Error())
		}
	})
}
