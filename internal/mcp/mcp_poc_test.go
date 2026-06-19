package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/mcp"
)

// =============================================================================
// POC 1: Full MCP lifecycle — stdio transport with echo server
// =============================================================================
// Uses the shell 'cat' as a simple stdio MCP server that echoes JSON-RPC.

func TestMCP_POC1_StdioTransportLifecycle(t *testing.T) {
	mgr := mcp.NewClientManager()

	// Use a persistent shell script as mock MCP server.
	// The script loops, reading JSON-RPC requests and writing responses.
	err := mgr.AddServer(mcp.ServerConfig{
		Name:      "echo-server",
		Transport: "stdio",
		Command:   "sh",
		Args: []string{"-c", `
			# Persistent MCP server — handle multiple requests
			while IFS= read -r line; do
				case "$line" in
					*initialize*)
						echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"echo","version":"1.0"},"capabilities":{"tools":{}}}}'
						;;
					*"tools/list"*)
						echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"Echo back the input","inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}]}}'
						;;
					*"tools/call"*)
						echo '{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ECHO: hello from MCP"}]}}'
						;;
					*)
						echo '{"jsonrpc":"2.0","id":0,"error":{"code":-32601,"message":"Unknown"}}'
						;;
				esac
			done
		`},
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Discover tools
	tools, err := mgr.DiscoverTools(ctx, "echo-server")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
	assert.Contains(t, tools[0].Description, "Echo")

	// Execute tool
	result, err := mgr.ExecuteTool(ctx, "echo-server", "echo", json.RawMessage(`{"message":"hello from MCP"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "ECHO")

	// List servers
	servers := mgr.ListServers()
	assert.Contains(t, servers, "echo-server")
}

// =============================================================================
// POC 2: HTTP transport with real HTTP server
// =============================================================================

func TestMCP_POC2_HTTPTransportLifecycle(t *testing.T) {
	// Create a real HTTP server that speaks MCP JSON-RPC
	requestCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		var resp map[string]any
		switch {
		case req.Method == "initialize":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]any{"name": "test-http-server", "version": "1.0"},
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			}
		case req.Method == "tools/list":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "http_tool",
							"description": "A tool served over HTTP",
							"inputSchema": map[string]any{
								"type":       "object",
								"properties": map[string]any{"input": map[string]any{"type": "string"}},
							},
						},
					},
				},
			}
		case req.Method == "tools/call":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("HTTP response #%d: tool executed successfully", count)},
					},
				},
			}
		default:
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32601, "message": "Method not found"},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mgr := mcp.NewClientManager()
	err := mgr.AddServer(mcp.ServerConfig{
		Name:      "http-server",
		Transport: "http",
		URL:       srv.URL,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Discover tools over HTTP
	tools, err := mgr.DiscoverTools(ctx, "http-server")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "http_tool", tools[0].Name)

	// Execute tool over HTTP
	result, err := mgr.ExecuteTool(ctx, "http-server", "http_tool", json.RawMessage(`{"input":"test"}`))
	require.NoError(t, err)
	assert.Contains(t, result, "tool executed successfully")

	// Multiple calls work
	result2, err := mgr.ExecuteTool(ctx, "http-server", "http_tool", json.RawMessage(`{"input":"test2"}`))
	require.NoError(t, err)
	assert.Contains(t, result2, "tool executed successfully")

	assert.Equal(t, 4, requestCount, "should have made 4 requests (init, list, call, call)")
}

// =============================================================================
// POC 3: Multi-server concurrent access
// =============================================================================

func TestMCP_POC3_MultiServerConcurrent(t *testing.T) {
	mgr := mcp.NewClientManager()

	// Create 3 real HTTP MCP servers for concurrent testing
	var servers []*httptest.Server
	for i := 1; i <= 3; i++ {
		idx := i
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				JSONRPC string `json:"jsonrpc"`
				ID      int    `json:"id"`
				Method  string `json:"method"`
			}
			json.NewDecoder(r.Body).Decode(&req)

			var resp map[string]any
			switch req.Method {
			case "initialize":
				resp = map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"serverInfo":      map[string]any{"name": fmt.Sprintf("s%d", idx), "version": "1.0"},
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				}
			case "tools/list":
				resp = map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"tools": []map[string]any{
							{"name": fmt.Sprintf("tool_%d", idx), "description": fmt.Sprintf("Tool from server %d", idx), "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
						},
					},
				}
			case "tools/call":
				resp = map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("result_from_s%d", idx)}},
					},
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()
		servers = append(servers, srv)

		err := mgr.AddServer(mcp.ServerConfig{
			Name:      fmt.Sprintf("server-%d", idx),
			Transport: "http",
			URL:       srv.URL,
		})
		require.NoError(t, err)
	}

	ctx := context.Background()

	// Concurrently discover tools from all servers
	var wg sync.WaitGroup
	results := make([]struct {
		server string
		tools  []mcp.ToolDef
		err    error
	}, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("server-%d", idx+1)
			tools, err := mgr.DiscoverTools(ctx, name)
			results[idx] = struct {
				server string
				tools  []mcp.ToolDef
				err    error
			}{name, tools, err}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		assert.NoError(t, r.err, "server %d discovery should succeed", i+1)
		assert.Len(t, r.tools, 1, "server %d should have 1 tool", i+1)
		assert.Equal(t, fmt.Sprintf("tool_%d", i+1), r.tools[0].Name)
	}

	// Execute tools from all servers concurrently
	var wg2 sync.WaitGroup
	execResults := make([]struct {
		server string
		result string
		err    error
	}, 3)

	for i := 0; i < 3; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			name := fmt.Sprintf("server-%d", idx+1)
			toolName := fmt.Sprintf("tool_%d", idx+1)
			result, err := mgr.ExecuteTool(ctx, name, toolName, json.RawMessage(`{}`))
			execResults[idx] = struct {
				server string
				result string
				err    error
			}{name, result, err}
		}(i)
	}
	wg2.Wait()

	for i, r := range execResults {
		assert.NoError(t, r.err, "server %d execution should succeed", i+1)
		assert.Contains(t, r.result, fmt.Sprintf("result_from_s%d", i+1))
	}

	// Verify all servers listed
	serversList := mgr.ListServers()
	assert.Len(t, serversList, 3)
}

// =============================================================================
// POC 4: Error handling — invalid configs, timeouts, disconnections
// =============================================================================

func TestMCP_POC4_ErrorHandling(t *testing.T) {
	mgr := mcp.NewClientManager()

	// Test 1: Invalid transport
	err := mgr.AddServer(mcp.ServerConfig{
		Name:      "bad-transport",
		Transport: "invalid",
		Command:   "echo",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported transport")

	// Test 2: Missing command for stdio
	err = mgr.AddServer(mcp.ServerConfig{
		Name:      "no-command",
		Transport: "stdio",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a command")

	// Test 3: Missing URL for HTTP
	err = mgr.AddServer(mcp.ServerConfig{
		Name:      "no-url",
		Transport: "http",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a URL")

	// Test 4: Empty name
	err = mgr.AddServer(mcp.ServerConfig{
		Name:      "",
		Transport: "stdio",
		Command:   "echo",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not be empty")

	// Test 5: Duplicate server name
	err = mgr.AddServer(mcp.ServerConfig{
		Name:      "unique",
		Transport: "stdio",
		Command:   "cat",
	})
	require.NoError(t, err)

	err = mgr.AddServer(mcp.ServerConfig{
		Name:      "unique",
		Transport: "stdio",
		Command:   "cat",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")

	// Test 6: Unknown server for discovery
	ctx := context.Background()
	_, err = mgr.DiscoverTools(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test 7: Unknown server for execution
	_, err = mgr.ExecuteTool(ctx, "nonexistent", "tool", json.RawMessage(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test 8: Close with no connections (should not panic)
	err = mgr.Close()
	assert.NoError(t, err)
}

// =============================================================================
// Verify existing MCP tests still pass
// =============================================================================

func TestMCP_ExistingFunctionalityPreserved(t *testing.T) {
	mgr := mcp.NewClientManager()

	assert.NotNil(t, mgr)
	assert.Empty(t, mgr.ListServers())

	// Add a server with factory
	err := mgr.AddServerWithConn("factory-test", func() (mcp.Conn, error) {
		return nil, fmt.Errorf("not implemented in this test")
	})
	require.NoError(t, err)

	servers := mgr.ListServers()
	assert.Contains(t, servers, "factory-test")

	// Verify that executing on an unconnected factory server returns an error
	_, err = mgr.ExecuteTool(context.Background(), "factory-test", "test", json.RawMessage(`{}`))
	assert.Error(t, err)
}

// =============================================================================
// Performance: Measure MCP tool discovery and execution latency

// =============================================================================
// Ensure the MCP types serialize correctly
// =============================================================================

func TestMCP_TypeSerialization(t *testing.T) {
	// ToolDef serialization
	toolDef := mcp.ToolDef{
		Name:        "test-tool",
		Description: "A test tool for serialization",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
	}

	data, err := json.Marshal(toolDef)
	require.NoError(t, err)

	var restored mcp.ToolDef
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	assert.Equal(t, "test-tool", restored.Name)
	assert.True(t, strings.Contains(string(restored.InputSchema), "properties"))

	// ServerConfig serialization
	cfg := mcp.ServerConfig{
		Name:      "serialize-test",
		Transport: "http",
		URL:       "https://example.com/mcp",
	}
	data, err = json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(data), "serialize-test")
	assert.Contains(t, string(data), "https://example.com/mcp")
}
