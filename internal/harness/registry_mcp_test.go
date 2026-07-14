package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// mockMCPReg is a minimal MCPRegistry implementation for testing RegisterMCPTools.
type mockMCPReg struct{}

func (m *mockMCPReg) ListResources(_ context.Context, server string) ([]htools.MCPResource, error) {
	return nil, nil
}
func (m *mockMCPReg) ReadResource(_ context.Context, server, uri string) (string, error) {
	return "", nil
}
func (m *mockMCPReg) ListTools(_ context.Context) (map[string][]htools.MCPToolDefinition, error) {
	return nil, nil
}
func (m *mockMCPReg) CallTool(_ context.Context, server, tool string, args json.RawMessage) (string, error) {
	return `{"result":"ok"}`, nil
}

// TestRegistry_RegisterMCPTools_Success verifies RegisterMCPTools registers tools correctly.
func TestRegistry_RegisterMCPTools_Success(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "search", Description: "Search", Parameters: map[string]any{}},
		{Name: "fetch", Description: "Fetch", Parameters: map[string]any{}},
	}

	registered, err := r.RegisterMCPTools("my-server", toolDefs, caller)
	if err != nil {
		t.Fatalf("RegisterMCPTools failed: %v", err)
	}
	if len(registered) != 2 {
		t.Fatalf("expected 2 registered tools, got %d", len(registered))
	}

	// Verify tools appear in registry at deferred tier.
	defs := r.DeferredDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 deferred definitions, got %d", len(defs))
	}

	// Verify tool names follow the mcp_<server>_<tool> convention.
	for _, def := range defs {
		if def.Name != "mcp_my_server_search" && def.Name != "mcp_my_server_fetch" {
			t.Errorf("unexpected tool name %q", def.Name)
		}
	}
}

// TestRegistry_RegisterMCPTools_DuplicateServer verifies RegisterMCPTools rejects duplicate server names.
func TestRegistry_RegisterMCPTools_DuplicateServer(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "tool1", Description: "t1", Parameters: map[string]any{}},
	}

	if _, err := r.RegisterMCPTools("dup-server", toolDefs, caller); err != nil {
		t.Fatalf("first RegisterMCPTools failed: %v", err)
	}

	_, err := r.RegisterMCPTools("dup-server", toolDefs, caller)
	if err == nil {
		t.Fatal("expected error for duplicate server name")
	}
}

// TestRegistry_RegisterMCPTools_EmptyServerName verifies RegisterMCPTools rejects empty server name.
func TestRegistry_RegisterMCPTools_EmptyServerName(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	_, err := r.RegisterMCPTools("", []htools.MCPToolDefinition{}, caller)
	if err == nil {
		t.Fatal("expected error for empty server name")
	}
}

// TestRegistry_RegisterMCPTools_NilCaller verifies RegisterMCPTools rejects nil caller.
func TestRegistry_RegisterMCPTools_NilCaller(t *testing.T) {
	r := NewRegistry()

	_, err := r.RegisterMCPTools("my-server", []htools.MCPToolDefinition{}, nil)
	if err == nil {
		t.Fatal("expected error for nil caller")
	}
}

// TestRegistry_RegisterMCPTools_ExecuteTool verifies registered MCP tools are callable.
func TestRegistry_RegisterMCPTools_ExecuteTool(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "search", Description: "Search", Parameters: map[string]any{}},
	}

	if _, err := r.RegisterMCPTools("exec-server", toolDefs, caller); err != nil {
		t.Fatalf("RegisterMCPTools failed: %v", err)
	}

	result, err := r.Execute(context.Background(), "mcp_exec_server_search", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result from MCP tool")
	}
}

// TestRegistry_RegisterMCPTools_NameSanitization verifies tool names are properly sanitized.
func TestRegistry_RegisterMCPTools_NameSanitization(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "my-fancy.tool", Description: "Fancy", Parameters: map[string]any{}},
	}

	registered, err := r.RegisterMCPTools("my-server.v2", toolDefs, caller)
	if err != nil {
		t.Fatalf("RegisterMCPTools failed: %v", err)
	}
	if len(registered) != 1 {
		t.Fatalf("expected 1 registered tool, got %d", len(registered))
	}
	if registered[0] != "mcp_my_server_v2_my_fancy_tool" {
		t.Errorf("unexpected tool name %q", registered[0])
	}
}

// TestRegistry_RegisterMCPTools_EmptyToolList verifies RegisterMCPTools handles empty tool lists.
func TestRegistry_RegisterMCPTools_EmptyToolList(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	registered, err := r.RegisterMCPTools("empty-server", []htools.MCPToolDefinition{}, caller)
	if err != nil {
		t.Fatalf("RegisterMCPTools failed: %v", err)
	}
	if len(registered) != 0 {
		t.Errorf("expected 0 registered tools, got %d", len(registered))
	}
}

// TestRegistry_UnregisterMCPServer_RemovesToolsAndAllowsReregistration verifies
// that UnregisterMCPServer removes all mcp_<server>_* tools from the registry
// and clears the server-name guard so the same server can be registered again.
// This is the core fix for the "already connected" bug that prevented MCP tools
// from appearing in runs after the first run.
func TestRegistry_UnregisterMCPServer_RemovesToolsAndAllowsReregistration(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "search_users", Description: "Search", Parameters: map[string]any{}},
		{Name: "get_profile", Description: "Profile", Parameters: map[string]any{}},
	}

	// First registration succeeds.
	registered, err := r.RegisterMCPTools("social", toolDefs, caller)
	if err != nil {
		t.Fatalf("first RegisterMCPTools failed: %v", err)
	}
	if len(registered) != 2 {
		t.Fatalf("expected 2 registered, got %d", len(registered))
	}

	// Tools are present.
	defs := r.DeferredDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 deferred defs before unregister, got %d", len(defs))
	}

	// Unregister.
	r.UnregisterMCPServer("social")

	// Tools are gone.
	defs = r.DeferredDefinitions()
	if len(defs) != 0 {
		t.Fatalf("expected 0 deferred defs after unregister, got %d: %v", len(defs), defs)
	}

	// Second registration with the same server name now succeeds (no "already connected").
	registered2, err := r.RegisterMCPTools("social", toolDefs, caller)
	if err != nil {
		t.Fatalf("second RegisterMCPTools after unregister failed: %v", err)
	}
	if len(registered2) != 2 {
		t.Fatalf("expected 2 registered on second attempt, got %d", len(registered2))
	}

	// Tools are present again.
	defs = r.DeferredDefinitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 deferred defs after re-register, got %d", len(defs))
	}
}

// TestRegistry_UnregisterMCPServer_NoOpForUnknown verifies that calling
// UnregisterMCPServer with a name that was never registered is a no-op.
func TestRegistry_UnregisterMCPServer_NoOpForUnknown(t *testing.T) {
	r := NewRegistry()
	// Should not panic.
	r.UnregisterMCPServer("never-registered")
	r.UnregisterMCPServer("")
}

// TestRegistry_UnregisterMCPServer_DoesNotRemoveOtherServers verifies that
// unregistering one server does not remove tools from a different server.
func TestRegistry_UnregisterMCPServer_DoesNotRemoveOtherServers(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	defs1 := []htools.MCPToolDefinition{{Name: "tool_a", Description: "a", Parameters: map[string]any{}}}
	defs2 := []htools.MCPToolDefinition{{Name: "tool_b", Description: "b", Parameters: map[string]any{}}}

	if _, err := r.RegisterMCPTools("server-one", defs1, caller); err != nil {
		t.Fatalf("register server-one: %v", err)
	}
	if _, err := r.RegisterMCPTools("server-two", defs2, caller); err != nil {
		t.Fatalf("register server-two: %v", err)
	}

	// Unregister only server-one.
	r.UnregisterMCPServer("server-one")

	defs := r.DeferredDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 deferred def remaining, got %d", len(defs))
	}
	if defs[0].Name != "mcp_server_two_tool_b" {
		t.Errorf("expected mcp_server_two_tool_b to remain, got %q", defs[0].Name)
	}
}

// TestRegistry_UnregisterMCPServer_ToolNamesUsePrefix verifies that the prefix
// matching logic uses the sanitized server name. For example, a server named
// "social" removes "mcp_social_*" tools but not "mcp_social_extra_*" that
// belongs to a different server named "social-extra".
func TestRegistry_UnregisterMCPServer_ToolNamesUsePrefix(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	defsA := []htools.MCPToolDefinition{{Name: "find", Description: "f", Parameters: map[string]any{}}}
	defsB := []htools.MCPToolDefinition{{Name: "find", Description: "f", Parameters: map[string]any{}}}

	if _, err := r.RegisterMCPTools("social", defsA, caller); err != nil {
		t.Fatalf("register social: %v", err)
	}
	if _, err := r.RegisterMCPTools("social-extra", defsB, caller); err != nil {
		t.Fatalf("register social-extra: %v", err)
	}

	// Unregister "social" — should only remove mcp_social_find, not mcp_social_extra_find.
	r.UnregisterMCPServer("social")

	defs := r.DeferredDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool remaining, got %d: %v", len(defs), defs)
	}
	if !strings.HasPrefix(defs[0].Name, "mcp_social_extra_") {
		t.Errorf("expected mcp_social_extra_find to remain, got %q", defs[0].Name)
	}
}

func TestRegistry_ReplaceByTagRebuildsMCPServerTools(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	if _, err := r.RegisterMCPTools("social", []htools.MCPToolDefinition{
		{Name: "search", Description: "Search", Parameters: map[string]any{}},
	}, caller); err != nil {
		t.Fatalf("RegisterMCPTools: %v", err)
	}

	err := r.ReplaceByTag("dynamic", []htools.Tool{{
		Definition: htools.Definition{
			Name:        "mcp_social_fetch",
			Description: "replacement",
			Tags:        []string{"mcp", "mcp_server:social"},
			Tier:        htools.TierDeferred,
		},
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return "replacement", nil
		},
	}})
	if err != nil {
		t.Fatalf("ReplaceByTag: %v", err)
	}

	r.mu.RLock()
	tracked := append([]string(nil), r.mcpServerTools["social"]...)
	r.mu.RUnlock()
	if len(tracked) != 1 || tracked[0] != "mcp_social_fetch" {
		t.Fatalf("expected social to track only replacement MCP tool, got %#v", tracked)
	}

	r.UnregisterMCPServer("social")
	if _, err := r.Execute(context.Background(), "mcp_social_fetch", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected replacement MCP tool to be removed by UnregisterMCPServer")
	}
}

func TestRegistry_ReplaceByTagWaitsForInFlightExecution(t *testing.T) {
	r := NewRegistry()

	started := make(chan struct{})
	release := make(chan struct{})
	if err := r.RegisterWithOptions(ToolDefinition{Name: "slow"}, func(context.Context, json.RawMessage) (string, error) {
		close(started)
		<-release
		return "old", nil
	}, RegisterOptions{Tags: []string{"hot"}}); err != nil {
		t.Fatalf("RegisterWithOptions: %v", err)
	}

	execDone := make(chan struct{})
	go func() {
		defer close(execDone)
		if _, err := r.Execute(context.Background(), "slow", json.RawMessage(`{}`)); err != nil {
			t.Errorf("Execute slow: %v", err)
		}
	}()
	<-started

	replaceDone := make(chan struct{})
	go func() {
		defer close(replaceDone)
		err := r.ReplaceByTag("hot", []htools.Tool{{
			Definition: htools.Definition{Name: "slow", Tags: []string{"hot"}},
			Handler: func(context.Context, json.RawMessage) (string, error) {
				return "new", nil
			},
		}})
		if err != nil {
			t.Errorf("ReplaceByTag: %v", err)
		}
	}()

	select {
	case <-replaceDone:
		t.Fatal("ReplaceByTag returned before the in-flight handler completed")
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case <-execDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old execution")
	}
	select {
	case <-replaceDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReplaceByTag")
	}

	got, err := r.Execute(context.Background(), "slow", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute replacement: %v", err)
	}
	if got != "new" {
		t.Fatalf("expected replacement handler, got %q", got)
	}
}

// TestRegistry_RegisterMCPTools_IsMutating verifies that every tool registered
// via RegisterMCPTools is treated as mutating by default. External MCP servers
// cannot prove a tool is read-only, so under ApprovalPolicyDestructive they
// must require approval.
func TestRegistry_RegisterMCPTools_IsMutating(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	toolDefs := []htools.MCPToolDefinition{
		{Name: "safe_read", Description: "Read-only-ish tool", Parameters: map[string]any{}},
	}

	registered, err := r.RegisterMCPTools("safe_server", toolDefs, caller)
	if err != nil {
		t.Fatalf("RegisterMCPTools failed: %v", err)
	}
	if len(registered) != 1 {
		t.Fatalf("expected 1 registered tool, got %d", len(registered))
	}

	toolName := registered[0]
	if !r.IsMutating(toolName) {
		t.Fatalf("expected MCP tool %q to be mutating", toolName)
	}
}

// TestRegistry_RegisterMCPTools_Concurrency verifies RegisterMCPTools is safe under concurrent access.
func TestRegistry_RegisterMCPTools_Concurrency(t *testing.T) {
	r := NewRegistry()
	caller := &mockMCPReg{}

	const nGoroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, nGoroutines)

	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			serverName := fmt.Sprintf("concurrent-server-%d", i)
			toolDefs := []htools.MCPToolDefinition{
				{Name: "tool", Description: "t", Parameters: map[string]any{}},
			}
			if _, err := r.RegisterMCPTools(serverName, toolDefs, caller); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent RegisterMCPTools failed: %v", err)
	}

	// Verify all servers were registered.
	defs := r.DeferredDefinitions()
	if len(defs) != nGoroutines {
		t.Errorf("expected %d deferred tools, got %d", nGoroutines, len(defs))
	}
}
