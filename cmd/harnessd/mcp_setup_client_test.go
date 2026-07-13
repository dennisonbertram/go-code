package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/mcp"
)

// --- Test-local fake conn ---

// testFakeMCPConn implements mcp.Conn for in-process testing without needing
// a real subprocess or network connection. It is intentionally local to this
// file so the main package tests don't depend on harness-package internals.
type testFakeMCPConn struct {
	name      string
	tools     []mcp.ToolDef
	resources []mcp.ResourceDef
	callErr   error // if non-nil, CallTool returns this error
	listErr   error // if non-nil, ListTools returns this error
	id        int64
}

func newTestFakeMCPConn(name string, tools []mcp.ToolDef) *testFakeMCPConn {
	return &testFakeMCPConn{name: name, tools: tools}
}

func (f *testFakeMCPConn) Initialize(_ context.Context) error { return nil }

func (f *testFakeMCPConn) ListTools(_ context.Context) ([]mcp.ToolDef, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tools, nil
}

func (f *testFakeMCPConn) CallTool(_ context.Context, name string, args json.RawMessage) (string, error) {
	if f.callErr != nil {
		return "", f.callErr
	}
	return fmt.Sprintf(`{"content":[{"type":"text","text":"result:%s"}]}`, name), nil
}

func (f *testFakeMCPConn) NextID() int64 {
	f.id++
	return f.id
}

func (f *testFakeMCPConn) Close() error { return nil }

func (f *testFakeMCPConn) ListResources(_ context.Context) ([]mcp.ResourceDef, error) {
	return f.resources, nil
}

func (f *testFakeMCPConn) ReadResource(_ context.Context, uri string) (string, error) {
	return fmt.Sprintf("content:%s", uri), nil
}

var _ mcp.ResourceCapableConn = (*testFakeMCPConn)(nil)

// testFakeMCPConnNoResources implements mcp.Conn but not mcp.ResourceCapableConn,
// simulating a server connection with no resources support.
type testFakeMCPConnNoResources struct {
	id int64
}

func (f *testFakeMCPConnNoResources) Initialize(_ context.Context) error { return nil }

func (f *testFakeMCPConnNoResources) ListTools(_ context.Context) ([]mcp.ToolDef, error) {
	return nil, nil
}

func (f *testFakeMCPConnNoResources) CallTool(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}

func (f *testFakeMCPConnNoResources) NextID() int64 {
	f.id++
	return f.id
}

func (f *testFakeMCPConnNoResources) Close() error { return nil }

// --- Interface compliance check ---

// TestClientManagerRegistry_InterfaceCompliance is a compile-time check that
// *clientManagerRegistry satisfies htools.MCPRegistry.
func TestClientManagerRegistry_InterfaceCompliance(t *testing.T) {
	var _ htools.MCPRegistry = (*clientManagerRegistry)(nil)
}

// --- ListTools tests ---

// TestClientManagerRegistry_ListTools_Empty verifies that an empty ClientManager
// returns an empty map without panicking.
func TestClientManagerRegistry_ListTools_Empty(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	reg := &clientManagerRegistry{cm: cm}

	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools on empty manager: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected empty map, got %d entries", len(tools))
	}
}

// TestClientManagerRegistry_ListTools_WithServer verifies that a registered
// server's tools are returned with correct field mapping.
func TestClientManagerRegistry_ListTools_WithServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	err := cm.AddServerWithConn("my-server", func() (mcp.Conn, error) {
		return newTestFakeMCPConn("my-server", []mcp.ToolDef{
			{Name: "search", Description: "search tool", InputSchema: schema},
			{Name: "fetch", Description: "fetch tool", InputSchema: nil},
		}), nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	defs, ok := tools["my-server"]
	if !ok {
		t.Fatal("expected 'my-server' in tools map")
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(defs))
	}

	// Verify field mapping for the first tool (search).
	var searchDef htools.MCPToolDefinition
	for _, d := range defs {
		if d.Name == "search" {
			searchDef = d
			break
		}
	}
	if searchDef.Name != "search" {
		t.Errorf("expected Name='search', got %q", searchDef.Name)
	}
	if searchDef.Description != "search tool" {
		t.Errorf("expected Description='search tool', got %q", searchDef.Description)
	}
	// InputSchema should have been unmarshalled into Parameters map.
	if searchDef.Parameters == nil {
		t.Error("expected Parameters map to be non-nil for tool with InputSchema")
	}

	// Verify fetch tool with nil InputSchema does not cause an error and has
	// a non-nil (but empty) Parameters map.
	var fetchDef htools.MCPToolDefinition
	for _, d := range defs {
		if d.Name == "fetch" {
			fetchDef = d
			break
		}
	}
	if fetchDef.Name != "fetch" {
		t.Errorf("expected Name='fetch', got %q", fetchDef.Name)
	}
	if fetchDef.Parameters == nil {
		t.Error("expected Parameters to be non-nil even when InputSchema is nil")
	}
}

// TestClientManagerRegistry_ListTools_ServerError verifies that when
// DiscoverTools fails for a server, ListTools returns an error.
func TestClientManagerRegistry_ListTools_ServerError(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	listErr := fmt.Errorf("server exploded")
	err := cm.AddServerWithConn("bad-server", func() (mcp.Conn, error) {
		conn := newTestFakeMCPConn("bad-server", nil)
		conn.listErr = listErr
		return conn, nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	_, gotErr := reg.ListTools(context.Background())
	if gotErr == nil {
		t.Fatal("expected error from ListTools when DiscoverTools fails")
	}
}

// --- CallTool tests ---

// TestClientManagerRegistry_CallTool_Delegates verifies that CallTool routes
// the call to the underlying cm.ExecuteTool correctly.
func TestClientManagerRegistry_CallTool_Delegates(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("my-server", func() (mcp.Conn, error) {
		return newTestFakeMCPConn("my-server", nil), nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	result, err := reg.CallTool(context.Background(), "my-server", "mytool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result from CallTool")
	}
}

// TestClientManagerRegistry_CallTool_UnknownServer verifies that calling a
// tool on an unknown server returns an error.
func TestClientManagerRegistry_CallTool_UnknownServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	reg := &clientManagerRegistry{cm: cm}

	_, err := reg.CallTool(context.Background(), "nonexistent", "anytool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
}

// --- ListResources tests ---

// TestClientManagerRegistry_ListResources_UnknownServer verifies that
// ListResources returns an error for a server that was never registered.
func TestClientManagerRegistry_ListResources_UnknownServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	reg := &clientManagerRegistry{cm: cm}

	_, err := reg.ListResources(context.Background(), "any-server")
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
	if !strings.Contains(err.Error(), "any-server") {
		t.Errorf("expected error to mention server name, got: %v", err)
	}
}

// TestClientManagerRegistry_ListResources_WithServer verifies that resources
// from a resource-capable connection are delegated through and mapped onto
// htools.MCPResource correctly.
func TestClientManagerRegistry_ListResources_WithServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("res-server", func() (mcp.Conn, error) {
		return &testFakeMCPConn{
			name: "res-server",
			resources: []mcp.ResourceDef{
				{URI: "file:///a.txt", Name: "a", Description: "file a", MimeType: "text/plain"},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	resources, err := reg.ListResources(context.Background(), "res-server")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].URI != "file:///a.txt" || resources[0].Name != "a" {
		t.Errorf("unexpected resource: %+v", resources[0])
	}
}

// TestClientManagerRegistry_ListResources_NotSupported verifies that a server
// connection without resources support yields a clean error, not a panic.
func TestClientManagerRegistry_ListResources_NotSupported(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("no-res-server", func() (mcp.Conn, error) {
		return &testFakeMCPConnNoResources{}, nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	_, err = reg.ListResources(context.Background(), "no-res-server")
	if err == nil {
		t.Fatal("expected error for server without resources support, got nil")
	}
	if !strings.Contains(err.Error(), "does not support resources") {
		t.Errorf("expected 'does not support resources' in error, got: %v", err)
	}
}

// --- ReadResource tests ---

// TestClientManagerRegistry_ReadResource_UnknownServer verifies that
// ReadResource returns an error that mentions the server name for a server
// that was never registered.
func TestClientManagerRegistry_ReadResource_UnknownServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	reg := &clientManagerRegistry{cm: cm}

	serverName := "my-special-server"
	_, err := reg.ReadResource(context.Background(), serverName, "file:///some/resource")
	if err == nil {
		t.Fatal("expected error from ReadResource, got nil")
	}

	// The error message should reference the server name.
	if !strings.Contains(err.Error(), serverName) {
		t.Errorf("expected error to mention server name %q, got: %v", serverName, err)
	}
}

// TestClientManagerRegistry_ReadResource_WithServer verifies that ReadResource
// delegates to the underlying connection and returns its content.
func TestClientManagerRegistry_ReadResource_WithServer(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("res-server", func() (mcp.Conn, error) {
		return &testFakeMCPConn{name: "res-server"}, nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	content, err := reg.ReadResource(context.Background(), "res-server", "file:///a.txt")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if content != "content:file:///a.txt" {
		t.Errorf("unexpected content: %q", content)
	}
}

// TestClientManagerRegistry_ReadResource_NotSupported verifies that a server
// connection without resources support yields a clean error, not a panic.
func TestClientManagerRegistry_ReadResource_NotSupported(t *testing.T) {
	t.Parallel()

	cm := mcp.NewClientManager()
	err := cm.AddServerWithConn("no-res-server", func() (mcp.Conn, error) {
		return &testFakeMCPConnNoResources{}, nil
	})
	if err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}

	reg := &clientManagerRegistry{cm: cm}
	_, err = reg.ReadResource(context.Background(), "no-res-server", "file:///a.txt")
	if err == nil {
		t.Fatal("expected error for server without resources support, got nil")
	}
	if !strings.Contains(err.Error(), "does not support resources") {
		t.Errorf("expected 'does not support resources' in error, got: %v", err)
	}
}
