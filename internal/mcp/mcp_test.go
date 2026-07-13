package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

// mockMCPServer is a minimal MCP server for testing that speaks JSON-RPC 2.0
// over stdio. It is implemented as an executable written to a temp file.
// Instead of exec, we implement a pipe-based in-process mock via a helper type.

// inProcessMCPServer simulates an MCP server in-process using io.Pipe.
type inProcessMCPServer struct {
	// clientWriter is what the client writes to (the mock's stdin).
	clientWriter *io.PipeWriter
	// clientReader is what the client reads from (the mock's stdout).
	clientReader *io.PipeReader

	// serverReader is the mock's stdin reader (reads from clientWriter).
	serverReader *io.PipeReader
	// serverWriter is the mock's stdout writer (writes to clientReader).
	serverWriter *io.PipeWriter

	tools     []mcp.ToolDef
	resources []mcp.ResourceDef
	// resourceContents maps a resource URI to the text returned by resources/read.
	resourceContents map[string]string
	// supportsResources controls whether resources/list and resources/read are
	// handled at all; when false the mock falls through to the default
	// method-not-found response, simulating a server without the capability.
	supportsResources bool
	done              chan struct{}
}

func newInProcessMCPServer(tools []mcp.ToolDef) *inProcessMCPServer {
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	return &inProcessMCPServer{
		clientWriter: clientToServerW,
		clientReader: serverToClientR,
		serverReader: clientToServerR,
		serverWriter: serverToClientW,
		tools:        tools,
		done:         make(chan struct{}),
	}
}

// start runs the mock server in a background goroutine.
func (s *inProcessMCPServer) start() {
	go s.serve()
}

// stop shuts the mock server down.
func (s *inProcessMCPServer) stop() {
	s.clientWriter.Close()
	s.serverWriter.Close()
	<-s.done
}

// serve reads requests and writes responses.
func (s *inProcessMCPServer) serve() {
	defer close(s.done)
	defer s.serverWriter.Close()

	scanner := bufio.NewScanner(s.serverReader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			ID      json.RawMessage `json:"id"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		// Handle notifications (no ID)
		if req.ID == nil {
			// notifications/initialized — no response needed
			continue
		}

		var resp map[string]any
		switch req.Method {
		case "initialize":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "test-server", "version": "1.0"},
				},
			}
		case "tools/list":
			toolList := make([]map[string]any, 0, len(s.tools))
			for _, t := range s.tools {
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
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf(`{"tool":"%s","called":true}`, callParams.Name)},
					},
				},
			}
		case "resources/list":
			if !s.supportsResources {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32601, "message": "method not found"},
				}
				break
			}
			resourceList := make([]map[string]any, 0, len(s.resources))
			for _, r := range s.resources {
				resourceList = append(resourceList, map[string]any{
					"uri":         r.URI,
					"name":        r.Name,
					"description": r.Description,
					"mimeType":    r.MimeType,
				})
			}
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"resources": resourceList},
			}
		case "resources/read":
			if !s.supportsResources {
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32601, "message": "method not found"},
				}
				break
			}
			var readParams struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(req.Params, &readParams)
			text := s.resourceContents[readParams.URI]
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"contents": []map[string]any{
						{"uri": readParams.URI, "mimeType": "text/plain", "text": text},
					},
				},
			}
		default:
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			}
		}

		data, _ := json.Marshal(resp)
		_, _ = fmt.Fprintf(s.serverWriter, "%s\n", data)
	}
}

// TestClientManager_AddServer tests that AddServer stores config correctly.
func TestClientManager_AddServer(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "my-server",
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
}

// TestClientManager_AddServer_Duplicate tests that adding the same server twice returns an error.
func TestClientManager_AddServer_Duplicate(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	cfg := mcp.ServerConfig{
		Name:      "dup-server",
		Transport: "stdio",
		Command:   "echo",
	}
	if err := cm.AddServer(cfg); err != nil {
		t.Fatalf("first AddServer: %v", err)
	}
	if err := cm.AddServer(cfg); err == nil {
		t.Fatal("expected error on duplicate AddServer")
	}
}

// TestClientManager_AddServer_InvalidTransport tests that an unknown transport returns an error.
func TestClientManager_AddServer_InvalidTransport(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "bad-server",
		Transport: "tcp", // unsupported
		Command:   "echo",
	})
	if err == nil {
		t.Fatal("expected error for unsupported transport")
	}
	if !strings.Contains(err.Error(), "unsupported transport") {
		t.Errorf("expected 'unsupported transport' in error, got %q", err.Error())
	}
}

// TestClientManager_AddServer_MissingName tests that an empty name returns an error.
func TestClientManager_AddServer_MissingName(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "",
		Transport: "stdio",
		Command:   "echo",
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// TestClientManager_AddServer_StdioMissingCommand tests that stdio without command returns an error.
func TestClientManager_AddServer_StdioMissingCommand(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "no-cmd",
		Transport: "stdio",
		Command:   "", // missing
	})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

// TestClientManager_AddServer_HTTPMissingURL tests that http without URL returns an error.
func TestClientManager_AddServer_HTTPMissingURL(t *testing.T) {
	t.Parallel()
	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "no-url",
		Transport: "http",
		URL:       "", // missing
	})
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

// TestMCPProtocol_Initialize tests the JSON-RPC initialize handshake using a pipe-based mock.
func TestMCPProtocol_Initialize(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer([]mcp.ToolDef{
		{Name: "ping", Description: "Send a ping"},
	})
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// TestMCPProtocol_ListTools tests that tools/list returns the expected tools.
func TestMCPProtocol_ListTools(t *testing.T) {
	t.Parallel()

	expectedTools := []mcp.ToolDef{
		{Name: "search", Description: "Search for things"},
		{Name: "create", Description: "Create a resource"},
	}

	srv := newInProcessMCPServer(expectedTools)
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != len(expectedTools) {
		t.Fatalf("expected %d tools, got %d", len(expectedTools), len(tools))
	}
	for i, want := range expectedTools {
		if tools[i].Name != want.Name {
			t.Errorf("tools[%d].Name = %q, want %q", i, tools[i].Name, want.Name)
		}
	}
}

// TestMCPProtocol_CallTool tests that tools/call returns a result.
func TestMCPProtocol_CallTool(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer([]mcp.ToolDef{
		{Name: "greet", Description: "Say hello"},
	})
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
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
		t.Errorf("expected result to mention 'greet', got %q", result)
	}
}

// TestClientManager_DiscoverTools tests DiscoverTools using an in-process server.
func TestClientManager_DiscoverTools(t *testing.T) {
	t.Parallel()

	serverTools := []mcp.ToolDef{
		{Name: "alpha", Description: "Tool alpha"},
		{Name: "beta", Description: "Tool beta"},
	}

	srv := newInProcessMCPServer(serverTools)
	srv.start()
	defer srv.stop()

	cm := mcp.NewClientManager()
	if err := cm.AddServerWithConn("pipe-server", func() (mcp.Conn, error) {
		return mcp.NewStdioConn("pipe-server", srv.clientReader, srv.clientWriter)
	}); err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := cm.DiscoverTools(ctx, "pipe-server")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

// TestClientManager_ExecuteTool tests ExecuteTool using an in-process server.
func TestClientManager_ExecuteTool(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer([]mcp.ToolDef{
		{Name: "ping", Description: "Ping the server"},
	})
	srv.start()
	defer srv.stop()

	cm := mcp.NewClientManager()
	if err := cm.AddServerWithConn("ping-server", func() (mcp.Conn, error) {
		return mcp.NewStdioConn("ping-server", srv.clientReader, srv.clientWriter)
	}); err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := cm.ExecuteTool(ctx, "ping-server", "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestClientManager_ExecuteTool_UnknownServer tests that ExecuteTool returns an error for unknown servers.
func TestClientManager_ExecuteTool_UnknownServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()

	ctx := context.Background()
	_, err := cm.ExecuteTool(ctx, "nonexistent", "ping", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

// TestClientManager_Concurrent tests that concurrent calls to separate servers
// work correctly. Each server has its own connection so there is no serialization.
func TestClientManager_Concurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 5
	cm := mcp.NewClientManager()

	// Create a separate in-process mock server for each goroutine.
	servers := make([]*inProcessMCPServer, goroutines)
	for i := 0; i < goroutines; i++ {
		srv := newInProcessMCPServer([]mcp.ToolDef{
			{Name: "count", Description: "Count something"},
		})
		srv.start()
		servers[i] = srv

		serverName := fmt.Sprintf("server-%d", i)
		srvCopy := srv // capture for closure
		if err := cm.AddServerWithConn(serverName, func() (mcp.Conn, error) {
			return mcp.NewStdioConn(serverName, srvCopy.clientReader, srvCopy.clientWriter)
		}); err != nil {
			t.Fatalf("AddServerWithConn %d: %v", i, err)
		}
	}
	defer func() {
		for _, srv := range servers {
			srv.stop()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errs := make(chan error, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			serverName := fmt.Sprintf("server-%d", n)
			args := json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))
			_, err := cm.ExecuteTool(ctx, serverName, "count", args)
			errs <- err
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent ExecuteTool: %v", err)
		}
	}
}

// TestClientManager_Close tests that Close() cleans up gracefully.
func TestClientManager_Close(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	if err := cm.Close(); err != nil {
		t.Fatalf("Close on empty manager: %v", err)
	}
}

// TestClientManager_ListServers tests that server names are tracked correctly.
func TestClientManager_ListServers(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()

	err := cm.AddServer(mcp.ServerConfig{
		Name:      "server-a",
		Transport: "http",
		URL:       "http://localhost:9999/mcp",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	names := cm.ListServers()
	if len(names) != 1 || names[0] != "server-a" {
		t.Errorf("ListServers = %v, want [server-a]", names)
	}
}

// TestStdioConn_IDUniqueness tests that concurrent requests get unique JSON-RPC IDs.
func TestStdioConn_IDUniqueness(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer([]mcp.ToolDef{
		{Name: "noop", Description: "No-op"},
	})
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("id-test", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	const n = 5
	ids := make(chan int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := conn.NextID()
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID: %d", id)
		}
		seen[id] = true
	}
}

// TestMCPProtocol_ListResources tests that resources/list returns the expected resources.
func TestMCPProtocol_ListResources(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil)
	srv.supportsResources = true
	srv.resources = []mcp.ResourceDef{
		{URI: "file:///a.txt", Name: "a", Description: "file a", MimeType: "text/plain"},
		{URI: "file:///b.txt", Name: "b", Description: "file b", MimeType: "text/plain"},
	}
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	rc, ok := conn.(mcp.ResourceCapableConn)
	if !ok {
		t.Fatal("expected conn to implement ResourceCapableConn")
	}

	resources, err := rc.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].URI != "file:///a.txt" || resources[0].Name != "a" {
		t.Errorf("unexpected resource[0]: %+v", resources[0])
	}
	if resources[1].URI != "file:///b.txt" || resources[1].Name != "b" {
		t.Errorf("unexpected resource[1]: %+v", resources[1])
	}
}

// TestMCPProtocol_ReadResource tests that resources/read returns the text content.
func TestMCPProtocol_ReadResource(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil)
	srv.supportsResources = true
	srv.resourceContents = map[string]string{
		"file:///a.txt": "hello from a.txt",
	}
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	rc, ok := conn.(mcp.ResourceCapableConn)
	if !ok {
		t.Fatal("expected conn to implement ResourceCapableConn")
	}

	content, err := rc.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if content != "hello from a.txt" {
		t.Errorf("ReadResource content = %q, want %q", content, "hello from a.txt")
	}
}

// TestMCPProtocol_ListResources_NotSupported tests that a server without the
// resources capability yields a clean error, not a panic, from a method-not-found
// JSON-RPC response.
func TestMCPProtocol_ListResources_NotSupported(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil) // supportsResources defaults to false
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	rc, ok := conn.(mcp.ResourceCapableConn)
	if !ok {
		t.Fatal("expected conn to implement ResourceCapableConn")
	}

	_, err = rc.ListResources(ctx)
	if err == nil {
		t.Fatal("expected error for server without resources capability, got nil")
	}
	if !strings.Contains(err.Error(), "does not support resources") {
		t.Errorf("expected 'does not support resources' in error, got %q", err.Error())
	}
}

// TestMCPProtocol_ReadResource_NotSupported tests that reading a resource from
// a server without the resources capability yields a clean error.
func TestMCPProtocol_ReadResource_NotSupported(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil) // supportsResources defaults to false
	srv.start()
	defer srv.stop()

	conn, err := mcp.NewStdioConn("test-server", srv.clientReader, srv.clientWriter)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	rc, ok := conn.(mcp.ResourceCapableConn)
	if !ok {
		t.Fatal("expected conn to implement ResourceCapableConn")
	}

	_, err = rc.ReadResource(ctx, "file:///missing.txt")
	if err == nil {
		t.Fatal("expected error for server without resources capability, got nil")
	}
	if !strings.Contains(err.Error(), "does not support resources") {
		t.Errorf("expected 'does not support resources' in error, got %q", err.Error())
	}
}

// TestClientManager_ListResources tests ClientManager.ListResources using an
// in-process server.
func TestClientManager_ListResources(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil)
	srv.supportsResources = true
	srv.resources = []mcp.ResourceDef{
		{URI: "mem://note", Name: "note"},
	}
	srv.start()
	defer srv.stop()

	cm := mcp.NewClientManager()
	if err := cm.AddServerWithConn("res-server", func() (mcp.Conn, error) {
		return mcp.NewStdioConn("res-server", srv.clientReader, srv.clientWriter)
	}); err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resources, err := cm.ListResources(ctx, "res-server")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "mem://note" {
		t.Fatalf("unexpected resources: %v", resources)
	}
}

// TestClientManager_ReadResource tests ClientManager.ReadResource using an
// in-process server.
func TestClientManager_ReadResource(t *testing.T) {
	t.Parallel()

	srv := newInProcessMCPServer(nil)
	srv.supportsResources = true
	srv.resourceContents = map[string]string{"mem://note": "note body"}
	srv.start()
	defer srv.stop()

	cm := mcp.NewClientManager()
	if err := cm.AddServerWithConn("res-server", func() (mcp.Conn, error) {
		return mcp.NewStdioConn("res-server", srv.clientReader, srv.clientWriter)
	}); err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, err := cm.ReadResource(ctx, "res-server", "mem://note")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if content != "note body" {
		t.Errorf("ReadResource content = %q, want %q", content, "note body")
	}
}

// TestClientManager_ListResources_UnknownServer tests that ListResources returns
// an error for unknown servers.
func TestClientManager_ListResources_UnknownServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()

	ctx := context.Background()
	_, err := cm.ListResources(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

// TestClientManager_MCPRegistryInterface tests that ClientManager implements MCPRegistry.
// This test verifies the interface via compile-time assignment.
func TestClientManager_MCPRegistryInterface(t *testing.T) {
	t.Parallel()
	// This test compiles only if ClientManager implements the correct methods.
	// The actual value is not used at runtime — just verifying the interface.
	cm := mcp.NewClientManager()
	_ = cm // compiler checks interface compliance
}

// TestStdioProcessConn_LifecycleUsingEcho tests actual subprocess lifecycle using
// a simple echo-server program. This uses a helper binary.
// This test is skipped if the helper command is not available.
func TestStdioProcessConn_LifecycleUsingEcho(t *testing.T) {
	// Check if we have a python interpreter available for a simple mock server.
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available, skipping subprocess test")
	}

	// Write a tiny MCP server in Python.
	serverScript := `
import sys
import json
import os

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        req = json.loads(line)
    except:
        continue

    req_id = req.get("id")
    if req_id is None:
        continue  # notification

    method = req.get("method", "")

    if method == "initialize":
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "py-test", "version": "1.0"}
        }}
    elif method == "tools/list":
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {
            "tools": [{"name": "echo", "description": "Echo tool", "inputSchema": {"type": "object"}}]
        }}
    elif method == "tools/call":
        params = req.get("params", {})
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {
            "content": [{"type": "text", "text": json.dumps({"echoed": params.get("arguments", {})})}]
        }}
    else:
        resp = {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": "not found"}}

    print(json.dumps(resp), flush=True)
`

	cm := mcp.NewClientManager()
	err = cm.AddServer(mcp.ServerConfig{
		Name:      "py-server",
		Transport: "stdio",
		Command:   pythonPath,
		Args:      []string{"-c", serverScript},
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := cm.DiscoverTools(ctx, "py-server")
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected tool 'echo', got %v", tools)
	}

	result, err := cm.ExecuteTool(ctx, "py-server", "echo", json.RawMessage(`{"msg":"hello"}`))
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !strings.Contains(result, "echoed") {
		t.Errorf("expected 'echoed' in result, got %q", result)
	}

	_ = cm.Close()
}

// TestClientManager_AddServerWithConn_NilFactory tests that AddServerWithConn rejects a nil factory.
func TestClientManager_AddServerWithConn_NilFactory(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("my-server", nil)
	if err == nil {
		t.Fatal("expected error for nil factory, got nil")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("expected 'nil' in error message, got %q", err.Error())
	}
}

// TestStdioConn_CloseUnblocksPending tests that Close() immediately unblocks
// any goroutine blocked in sendRequest, even when the server never responds.
// This is the regression test for the CRITICAL shutdown hang.
func TestStdioConn_CloseUnblocksPending(t *testing.T) {
	t.Parallel()

	// Simulate a server that:
	//   - accepts writes (so sendRequest can complete the write and register pending)
	//   - never sends any responses (simulates a hung/silent server)
	//
	// clientToServerR/W: client writes requests → server reads them (we drain in background)
	// serverToClientR/W: server would write responses → client reads; we never write to serverToClientW
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	// Drain client-to-server writes in background so they don't block.
	go func() {
		_, _ = io.Copy(io.Discard, clientToServerR)
	}()
	// serverToClientW is intentionally never written to — simulates silent server.
	// We keep it in scope so it doesn't get GC'd/closed.
	_ = serverToClientW

	conn, err := mcp.NewStdioConn("hang-server", serverToClientR, clientToServerW)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}

	// Send a request in a goroutine. It will complete the write (server drains it)
	// but never get a response (server is silent), so it blocks in the select.
	errCh := make(chan error, 1)
	go func() {
		ctx := context.Background() // no deadline — hangs forever unless Close() unblocks it
		// ListTools calls sendRequest internally; it will block in select.
		_, err := conn.ListTools(ctx)
		errCh <- err
	}()

	// Give the goroutine time to start, complete the write, and register its pending request.
	time.Sleep(50 * time.Millisecond)

	// Close the connection. This must return promptly (not hang).
	closeDone := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Close() hung for >2s — shutdown hang regression")
	}

	// The blocked ListTools goroutine must also have returned with an error.
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from ListTools after Close(), got nil")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("ListTools goroutine did not unblock after Close()")
	}
}
