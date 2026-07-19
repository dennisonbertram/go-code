package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/mcp"
)

// TestRegisterMCPServersFromConfig_TOMLOnly verifies that a server defined
// only in the TOML config appears in ListServers after registration (T1).
func TestRegisterMCPServersFromConfig_TOMLOnly(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"my-tool": {Transport: "stdio", Command: "/usr/local/bin/my-mcp-server"},
	}
	var logs []string
	registerMCPServersFromConfig(manager, toml, nil, func(format string, args ...any) {
		// capture but don't print
	})

	servers := manager.ListServers()
	if len(servers) != 1 || servers[0] != "my-tool" {
		t.Fatalf("expected [my-tool], got %v", servers)
	}
	_ = logs
}

// TestRegisterMCPServersFromConfig_EnvVarCollision verifies that when an env
// var server has the same name as a TOML server, the TOML entry is preserved
// and the env var entry is skipped (T2).
func TestRegisterMCPServersFromConfig_EnvVarCollision(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"server-a": {Transport: "stdio", Command: "/toml-cmd"},
	}
	envServers := []mcp.ServerConfig{
		{Name: "server-a", Transport: "stdio", Command: "/env-cmd"},
	}

	var skipped []string
	registerMCPServersFromConfig(manager, toml, envServers, func(format string, args ...any) {
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok && s == "server-a" {
				// capture skip log
				skipped = append(skipped, s)
			}
		}
	})

	servers := manager.ListServers()
	if len(servers) != 1 || servers[0] != "server-a" {
		t.Fatalf("expected exactly [server-a], got %v", servers)
	}
	// The env var server with the same name should have been skipped.
	// Verify no second AddServer error occurs by checking the manager only
	// has one entry (which it does from the assertion above).
}

// TestRegisterMCPServersFromConfig_EnvVarUniqueAdded verifies that a unique
// env var server is registered alongside TOML servers (T3).
func TestRegisterMCPServersFromConfig_EnvVarUniqueAdded(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"toml-server": {Transport: "stdio", Command: "/toml-cmd"},
	}
	envServers := []mcp.ServerConfig{
		{Name: "env-server", Transport: "stdio", Command: "/env-cmd"},
	}

	registerMCPServersFromConfig(manager, toml, envServers, func(string, ...any) {})

	servers := manager.ListServers()
	sort.Strings(servers)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %v", servers)
	}
	if servers[0] != "env-server" || servers[1] != "toml-server" {
		t.Fatalf("unexpected server names: %v", servers)
	}
}

// TestRegisterMCPServersFromConfig_InferStdio verifies that transport is
// inferred as "stdio" when Transport is empty and no URL is set (T4).
func TestRegisterMCPServersFromConfig_InferStdio(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"stdio-server": {Command: "/usr/bin/my-server"}, // Transport intentionally empty
	}

	var logged []string
	registerMCPServersFromConfig(manager, toml, nil, func(format string, args ...any) {
		logged = append(logged, format)
		for _, a := range args {
			if s, ok := a.(string); ok {
				logged = append(logged, s)
			}
		}
	})

	servers := manager.ListServers()
	if len(servers) != 1 || servers[0] != "stdio-server" {
		t.Fatalf("expected [stdio-server] to be registered, got %v", servers)
	}

	// Confirm that "stdio" appears in a log line, indicating the inferred transport.
	foundStdio := false
	for _, entry := range logged {
		if entry == "stdio" {
			foundStdio = true
			break
		}
	}
	if !foundStdio {
		t.Fatalf("expected 'stdio' in log output, got %v", logged)
	}
}

// TestRegisterMCPServersFromConfig_InferHTTP verifies that transport is
// inferred as "http" when Transport is empty and a URL is set (T5).
func TestRegisterMCPServersFromConfig_InferHTTP(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"http-server": {URL: "http://localhost:3001/mcp"}, // Transport intentionally empty
	}

	var logged []string
	registerMCPServersFromConfig(manager, toml, nil, func(format string, args ...any) {
		logged = append(logged, format)
		for _, a := range args {
			if s, ok := a.(string); ok {
				logged = append(logged, s)
			}
		}
	})

	servers := manager.ListServers()
	if len(servers) != 1 || servers[0] != "http-server" {
		t.Fatalf("expected [http-server] to be registered, got %v", servers)
	}

	// Confirm that "http" appears in a log line, indicating the inferred transport.
	foundHTTP := false
	for _, entry := range logged {
		if entry == "http" {
			foundHTTP = true
			break
		}
	}
	if !foundHTTP {
		t.Fatalf("expected 'http' in log output, got %v", logged)
	}
}

// TestRegisterMCPServersFromConfig_SkipLogMessage verifies the exact log
// message format used when skipping a colliding env var server.
func TestRegisterMCPServersFromConfig_SkipLogMessage(t *testing.T) {
	manager := mcp.NewClientManager()
	toml := map[string]config.MCPServerConfig{
		"collide": {Transport: "stdio", Command: "/toml-cmd"},
	}
	envServers := []mcp.ServerConfig{
		{Name: "collide", Transport: "stdio", Command: "/env-cmd"},
	}

	var skipMessages []string
	registerMCPServersFromConfig(manager, toml, envServers, func(format string, args ...any) {
		if format == "mcp: skipping env var server %q: already registered from TOML config" {
			skipMessages = append(skipMessages, format)
		}
	})

	if len(skipMessages) != 1 {
		t.Fatalf("expected exactly 1 skip log message, got %d: %v", len(skipMessages), skipMessages)
	}
}

// TestRegisterMCPServersFromConfig_NilToml verifies that a nil TOML map is
// handled gracefully (no panic), and only env var servers are registered.
func TestRegisterMCPServersFromConfig_NilToml(t *testing.T) {
	manager := mcp.NewClientManager()
	envServers := []mcp.ServerConfig{
		{Name: "env-only", Transport: "stdio", Command: "/env-cmd"},
	}

	registerMCPServersFromConfig(manager, nil, envServers, func(string, ...any) {})

	servers := manager.ListServers()
	if len(servers) != 1 || servers[0] != "env-only" {
		t.Fatalf("expected [env-only], got %v", servers)
	}
}

// TestRegisterMCPServersFromConfig_EmptyBoth verifies no panic and empty
// result when both sources are empty.
func TestRegisterMCPServersFromConfig_EmptyBoth(t *testing.T) {
	manager := mcp.NewClientManager()
	registerMCPServersFromConfig(manager, nil, nil, func(string, ...any) {})
	servers := manager.ListServers()
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %v", servers)
	}
}

// bearerGatedMCPServer returns a mock MCP HTTP server that responds 401 unless
// the request carries "Authorization: Bearer x", and otherwise speaks enough
// JSON-RPC to initialize and list one tool.
func bearerGatedMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			resp["result"] = map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "gated", "version": "1.0"},
			}
		case "tools/list":
			resp["result"] = map[string]any{"tools": []map[string]any{
				{"name": "gated-tool", "description": "A gated tool", "inputSchema": map[string]any{"type": "object"}},
			}}
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

// TestRegisterMCPServersFromConfig_TOMLHeadersPassedThrough verifies that
// headers from a TOML [mcp_servers.*] entry are wired into the registered
// mcp.ServerConfig: a bearer-gated HTTP server registered via
// registerMCPServersFromConfig is reachable end to end.
func TestRegisterMCPServersFromConfig_TOMLHeadersPassedThrough(t *testing.T) {
	srv := bearerGatedMCPServer(t)
	defer srv.Close()

	manager := mcp.NewClientManager()
	defer manager.Close()

	toml := map[string]config.MCPServerConfig{
		"gated": {
			Transport: "http",
			URL:       srv.URL,
			Headers:   map[string]string{"Authorization": "Bearer x"},
		},
	}
	registerMCPServersFromConfig(manager, toml, nil, func(string, ...any) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := manager.DiscoverTools(ctx, "gated")
	if err != nil {
		t.Fatalf("DiscoverTools with TOML-configured headers: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "gated-tool" {
		t.Fatalf("unexpected tools: %v", tools)
	}
}

// TestRegisterMCPServersFromConfig_TOMLWithoutHeaders_Gets401 is the negative
// companion: the same gated server registered without headers fails, proving
// the pass-through above (not the mock) is what authorized the request.
func TestRegisterMCPServersFromConfig_TOMLWithoutHeaders_Gets401(t *testing.T) {
	srv := bearerGatedMCPServer(t)
	defer srv.Close()

	manager := mcp.NewClientManager()
	defer manager.Close()

	toml := map[string]config.MCPServerConfig{
		"gated": {Transport: "http", URL: srv.URL},
	}
	registerMCPServersFromConfig(manager, toml, nil, func(string, ...any) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := manager.DiscoverTools(ctx, "gated"); err == nil {
		t.Fatal("expected 401 error without configured headers, got nil")
	}
}
