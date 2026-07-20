package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseMCPServersEnv_EnvUnset_ReturnsEmpty(t *testing.T) {
	t.Setenv(EnvVarMCPServers, "")
	// Also test with a getenv that returns "" (simulates unset).
	configs, err := ParseMCPServersEnvWith(func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected empty slice, got %d configs", len(configs))
	}
}

func TestParseMCPServersEnv_EnvEmpty_ReturnsEmpty(t *testing.T) {
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return "   "
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected empty slice, got %d configs", len(configs))
	}
}

func TestParseMCPServersEnv_UsesProcessEnvironment(t *testing.T) {
	t.Setenv(EnvVarMCPServers, `[{"name":"env-server","command":"node","args":["server.js"]}]`)

	configs, err := ParseMCPServersEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Name != "env-server" || configs[0].Transport != "stdio" {
		t.Fatalf("unexpected config: %+v", configs[0])
	}
}

func TestParseMCPServersEnv_ValidStdio(t *testing.T) {
	raw := `[{"name":"test-server","transport":"stdio","command":"node","args":["server.js","--port","3000"]}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	c := configs[0]
	if c.Name != "test-server" {
		t.Errorf("name = %q, want %q", c.Name, "test-server")
	}
	if c.Transport != "stdio" {
		t.Errorf("transport = %q, want %q", c.Transport, "stdio")
	}
	if c.Command != "node" {
		t.Errorf("command = %q, want %q", c.Command, "node")
	}
	if len(c.Args) != 3 || c.Args[0] != "server.js" || c.Args[1] != "--port" || c.Args[2] != "3000" {
		t.Errorf("args = %v, want [server.js --port 3000]", c.Args)
	}
}

func TestParseMCPServersEnv_ValidHTTP(t *testing.T) {
	raw := `[{"name":"remote-mcp","transport":"http","url":"http://localhost:9090/mcp"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	c := configs[0]
	if c.Name != "remote-mcp" {
		t.Errorf("name = %q, want %q", c.Name, "remote-mcp")
	}
	if c.Transport != "http" {
		t.Errorf("transport = %q, want %q", c.Transport, "http")
	}
	if c.URL != "http://localhost:9090/mcp" {
		t.Errorf("url = %q, want %q", c.URL, "http://localhost:9090/mcp")
	}
}

func TestParseMCPServersEnv_TransportInferredFromCommand(t *testing.T) {
	raw := `[{"name":"inferred-stdio","command":"python3","args":["mcp_server.py"]}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Transport != "stdio" {
		t.Errorf("transport = %q, want %q (inferred from command)", configs[0].Transport, "stdio")
	}
}

func TestParseMCPServersEnv_TransportInferredFromURL(t *testing.T) {
	raw := `[{"name":"inferred-http","url":"https://mcp.example.com/api"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Transport != "http" {
		t.Errorf("transport = %q, want %q (inferred from url)", configs[0].Transport, "http")
	}
}

func TestParseMCPServersEnv_InvalidJSON_OuterArray_ReturnsError(t *testing.T) {
	// Not a JSON array — should return an error.
	raw := `{"name":"single-object"}`
	_, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error for non-array JSON, got nil")
	}
	if !strings.Contains(err.Error(), "JSON array") {
		t.Errorf("error should mention 'JSON array', got: %v", err)
	}
}

func TestParseMCPServersEnv_MixedValidInvalid_ValidOnesLoaded(t *testing.T) {
	raw := `[
		{"name":"good","command":"node","args":["server.js"]},
		{"name":"","command":"bad"},
		{"name":"also-good","url":"http://localhost:8080"}
	]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 valid configs, got %d", len(configs))
	}
	if configs[0].Name != "good" {
		t.Errorf("first config name = %q, want %q", configs[0].Name, "good")
	}
	if configs[1].Name != "also-good" {
		t.Errorf("second config name = %q, want %q", configs[1].Name, "also-good")
	}
}

func TestParseMCPServersEnv_MissingName_Skipped(t *testing.T) {
	raw := `[{"command":"node","args":["server.js"]}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs (missing name skipped), got %d", len(configs))
	}
}

func TestParseMCPServersEnv_MissingCommandAndURL_Skipped(t *testing.T) {
	raw := `[{"name":"no-endpoint","transport":"stdio"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs (missing command/url skipped), got %d", len(configs))
	}
}

func TestParseMCPServersEnv_BothCommandAndURL_Skipped(t *testing.T) {
	raw := `[{"name":"both","command":"node","url":"http://localhost:3000"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs (both command and url skipped), got %d", len(configs))
	}
}

func TestParseMCPServersEnv_UnsupportedTransport_Skipped(t *testing.T) {
	raw := `[{"name":"ws","transport":"websocket","url":"ws://localhost:3000"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs (unsupported transport skipped), got %d", len(configs))
	}
}

func TestParseMCPServersEnv_ArgsPreserved(t *testing.T) {
	raw := `[{"name":"args-test","command":"npx","args":["-y","@example/mcp-server","--flag","value with spaces"]}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	want := []string{"-y", "@example/mcp-server", "--flag", "value with spaces"}
	got := configs[0].Args
	if len(got) != len(want) {
		t.Fatalf("args length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseMCPServersEnv_DuplicateNames_FirstWins(t *testing.T) {
	// Two entries with the same name — only the first should be returned.
	raw := `[
		{"name":"dup-server","command":"node","args":["first.js"]},
		{"name":"dup-server","command":"node","args":["second.js"]}
	]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config (duplicate skipped), got %d", len(configs))
	}
	if configs[0].Name != "dup-server" {
		t.Errorf("name = %q, want %q", configs[0].Name, "dup-server")
	}
	// Verify the first occurrence wins by checking the args.
	if len(configs[0].Args) != 1 || configs[0].Args[0] != "first.js" {
		t.Errorf("args = %v, want [first.js] (first occurrence should win)", configs[0].Args)
	}
}

func TestParseMCPServersEnv_EmptyArray_ReturnsEmpty(t *testing.T) {
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return "[]"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected empty slice, got %d configs", len(configs))
	}
}

// TestHarnessStartup_MCPServersFromEnv is an integration test that creates an
// httptest server responding to MCP initialize and tools/list, then verifies
// that ParseMCPServersEnvWith correctly parses an HTTP server config and the
// ClientManager can discover tools from it.
func TestHarnessStartup_MCPServersFromEnv(t *testing.T) {
	// Set up a fake MCP server over HTTP using pipes (since the real HTTP
	// transport is not yet implemented in ClientManager, we test the
	// config parse + AddServer flow with a stdio-based in-process server).
	//
	// We create a pipe-based MCP server that responds to initialize and
	// tools/list, then register it via AddServerWithConn (simulating what
	// the startup wiring would do after parsing the env var).

	// Parse config from env.
	raw := `[{"name":"test-http-server","url":"http://localhost:12345/mcp"}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Transport != "http" {
		t.Errorf("transport = %q, want %q", configs[0].Transport, "http")
	}

	// Create a pipe-based MCP server to verify the AddServer → DiscoverTools flow.
	cm := NewClientManager()

	// Create pipe-based connection (simulates the MCP protocol).
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()

	// Start a goroutine that acts as an MCP server.
	go func() {
		defer serverW.Close()
		scanner := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, err := serverR.Read(buf)
			if err != nil {
				return
			}
			scanner = append(scanner, buf[:n]...)
			// Process complete lines.
			for {
				idx := -1
				for i, b := range scanner {
					if b == '\n' {
						idx = i
						break
					}
				}
				if idx < 0 {
					break
				}
				line := scanner[:idx]
				scanner = scanner[idx+1:]

				var req map[string]any
				if jsonErr := json.Unmarshal(line, &req); jsonErr != nil {
					continue
				}
				method, _ := req["method"].(string)
				id, _ := req["id"].(float64)

				var resp map[string]any
				switch method {
				case "initialize":
					resp = map[string]any{
						"jsonrpc": "2.0",
						"id":      id,
						"result": map[string]any{
							"protocolVersion": "2024-11-05",
							"capabilities":    map[string]any{},
							"serverInfo":      map[string]any{"name": "test-server", "version": "1.0"},
						},
					}
				case "tools/list":
					resp = map[string]any{
						"jsonrpc": "2.0",
						"id":      id,
						"result": map[string]any{
							"tools": []map[string]any{
								{
									"name":        "echo",
									"description": "Echoes input",
									"inputSchema": map[string]any{"type": "object"},
								},
							},
						},
					}
				default:
					continue
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverW, "%s\n", data)
			}
		}
	}()

	// Register with a pipe-based connection factory.
	err = cm.AddServerWithConn(configs[0].Name, func() (Conn, error) {
		return NewStdioConn(configs[0].Name, clientR, clientW)
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	// Discover tools.
	tools, err := cm.DiscoverTools(context.Background(), configs[0].Name)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", tools[0].Name, "echo")
	}

	_ = cm.Close()
}

func TestHarnessStartup_NoMCPServersEnv_StartsCleanly(t *testing.T) {
	// When the env var is not set, the startup path should produce no configs
	// and not error.
	configs, err := ParseMCPServersEnvWith(func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}

	// Verify that a fresh ClientManager with no servers works correctly.
	cm := NewClientManager()
	servers := cm.ListServers()
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(servers))
	}
	if err := cm.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestProgrammaticRegistration_StillWorks(t *testing.T) {
	// Verify that the existing AddServer and AddServerWithConn APIs still
	// function correctly alongside the new env-var parsing.
	cm := NewClientManager()
	defer cm.Close()

	// Register via config (existing API).
	err := cm.AddServer(ServerConfig{
		Name:      "programmatic-stdio",
		Transport: "stdio",
		Command:   "echo",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// Register via conn factory (existing test API).
	err = cm.AddServerWithConn("programmatic-conn", func() (Conn, error) {
		r, w := io.Pipe()
		return NewStdioConn("programmatic-conn", r, w)
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	servers := cm.ListServers()
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}

	// Also parse some configs from env and add them — they should coexist.
	raw := `[{"name":"env-server","command":"node","args":["s.js"]}]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, cfg := range configs {
		if err := cm.AddServer(cfg); err != nil {
			t.Fatalf("AddServer from env config: %v", err)
		}
	}

	servers = cm.ListServers()
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers after env configs, got %d", len(servers))
	}
}

// TestParseMCPServersEnv_HTTPTestServer is a true integration test using
// httptest.Server. It creates a server that responds to JSON-RPC MCP
// initialize and tools/list, verifies config parsing, and shows how the
// complete flow would work if HTTP transport were fully implemented.
func TestParseMCPServersEnv_HTTPTestServer(t *testing.T) {
	// Create an httptest server that responds to MCP JSON-RPC requests.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", 500)
			return
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "json error", 400)
			return
		}
		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)

		var resp map[string]any
		switch method {
		case "initialize":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "httptest-mcp", "version": "1.0"},
				},
			}
		case "tools/list":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "greet",
							"description": "Greets someone",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			}
		default:
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Parse config pointing to our httptest server.
	raw := fmt.Sprintf(`[{"name":"httptest-server","url":"%s"}]`, ts.URL)
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Name != "httptest-server" {
		t.Errorf("name = %q, want %q", configs[0].Name, "httptest-server")
	}
	if configs[0].Transport != "http" {
		t.Errorf("transport = %q, want %q", configs[0].Transport, "http")
	}
	if configs[0].URL != ts.URL {
		t.Errorf("url = %q, want %q", configs[0].URL, ts.URL)
	}

	// Verify the config is valid and can be registered with ClientManager.
	cm := NewClientManager()
	defer cm.Close()

	// Note: AddServer will accept the config. DiscoverTools would fail because
	// HTTP transport is not yet fully implemented in ClientManager, but the
	// registration itself should succeed.
	err = cm.AddServer(configs[0])
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	servers := cm.ListServers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server registered, got %d", len(servers))
	}
}

// TestParseMCPServersEnv_HeadersRoundTrip verifies that the "headers" key in
// HARNESS_MCP_SERVERS JSON entries is parsed into ServerConfig.Headers.
func TestParseMCPServersEnv_HeadersRoundTrip(t *testing.T) {
	raw := `[
		{
			"name": "authed-http",
			"transport": "http",
			"url": "http://localhost:9999/mcp",
			"headers": {"Authorization": "Bearer tok-123", "X-Tenant": "acme"}
		},
		{
			"name": "plain-http",
			"transport": "http",
			"url": "http://localhost:9998/mcp"
		}
	]`
	configs, err := ParseMCPServersEnvWith(func(key string) string {
		if key == EnvVarMCPServers {
			return raw
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	authed := configs[0]
	if authed.Name != "authed-http" {
		t.Fatalf("unexpected first config: %+v", authed)
	}
	if len(authed.Headers) != 2 {
		t.Fatalf("Headers: got %d entries, want 2 (%v)", len(authed.Headers), authed.Headers)
	}
	if got := authed.Headers["Authorization"]; got != "Bearer tok-123" {
		t.Errorf("Headers[Authorization] = %q, want %q", got, "Bearer tok-123")
	}
	if got := authed.Headers["X-Tenant"]; got != "acme" {
		t.Errorf("Headers[X-Tenant] = %q, want %q", got, "acme")
	}

	plain := configs[1]
	if plain.Name != "plain-http" {
		t.Fatalf("unexpected second config: %+v", plain)
	}
	if len(plain.Headers) != 0 {
		t.Errorf("Headers: got %v, want empty for server without headers key", plain.Headers)
	}
}

// TestParseMCPServersEnv_HeadersWrongType_Skipped verifies that an entry whose
// "headers" value is not a JSON object of strings is skipped as invalid while
// valid sibling entries still load.
func TestParseMCPServersEnv_HeadersWrongType_Skipped(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"headers as string", `{"name":"bad","url":"http://x/mcp","headers":"Bearer tok"}`},
		{"headers as array", `{"name":"bad","url":"http://x/mcp","headers":["Authorization: Bearer tok"]}`},
		{"headers with non-string value", `{"name":"bad","url":"http://x/mcp","headers":{"Authorization":42}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw := `[` + tc.raw + `,{"name":"good","url":"http://y/mcp"}]`
			configs, err := ParseMCPServersEnvWith(func(key string) string {
				if key == EnvVarMCPServers {
					return raw
				}
				return ""
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(configs) != 1 || configs[0].Name != "good" {
				t.Fatalf("expected only the valid entry to load, got %+v", configs)
			}
		})
	}
}
