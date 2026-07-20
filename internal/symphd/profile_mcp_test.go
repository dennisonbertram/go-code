package symphd

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"go-agent-harness/internal/config"
)

// TestMergeProfileMCPIntoTOML_EmptyExisting verifies that mergeProfileMCPIntoTOML
// with an empty existing TOML string adds profile MCP servers correctly.
func TestMergeProfileMCPIntoTOML_EmptyExisting(t *testing.T) {
	t.Parallel()

	profileServers := map[string]config.MCPServerConfig{
		"my-tool": {
			Transport: "stdio",
			Command:   "/usr/local/bin/my-tool",
			Args:      []string{"--verbose"},
		},
	}

	result, err := mergeProfileMCPIntoTOML("", profileServers)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML: %v", err)
	}

	// Parse the result and verify mcp_servers has the entry.
	var parsed struct {
		MCPServers map[string]config.MCPServerConfig `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(result, &parsed); err != nil {
		t.Fatalf("parse result TOML: %v (got: %s)", err, result)
	}

	srv, ok := parsed.MCPServers["my-tool"]
	if !ok {
		t.Fatalf("expected mcp_servers[\"my-tool\"] in result, got: %s", result)
	}
	if srv.Transport != "stdio" {
		t.Errorf("expected transport %q, got %q", "stdio", srv.Transport)
	}
	if srv.Command != "/usr/local/bin/my-tool" {
		t.Errorf("expected command %q, got %q", "/usr/local/bin/my-tool", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "--verbose" {
		t.Errorf("expected args [\"--verbose\"], got %v", srv.Args)
	}
}

// TestMergeProfileMCPIntoTOML_ExistingServerPreserved verifies that when
// the existing TOML already has an MCP server, merging profile servers with
// different names preserves the existing server.
func TestMergeProfileMCPIntoTOML_ExistingServerPreserved(t *testing.T) {
	t.Parallel()

	existingTOML := `
[mcp_servers]
[mcp_servers.existing-tool]
transport = "http"
url = "http://localhost:9000/mcp"
`

	profileServers := map[string]config.MCPServerConfig{
		"new-tool": {
			Transport: "stdio",
			Command:   "/usr/bin/new-tool",
		},
	}

	result, err := mergeProfileMCPIntoTOML(existingTOML, profileServers)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML: %v", err)
	}

	var parsed struct {
		MCPServers map[string]map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(result, &parsed); err != nil {
		t.Fatalf("parse result TOML: %v (got: %s)", err, result)
	}

	if _, ok := parsed.MCPServers["existing-tool"]; !ok {
		t.Errorf("expected existing-tool to be preserved in result: %s", result)
	}
	if _, ok := parsed.MCPServers["new-tool"]; !ok {
		t.Errorf("expected new-tool to be added in result: %s", result)
	}
}

// TestMergeProfileMCPIntoTOML_ProfileOverridesExisting verifies that a profile
// MCP server with the same name as an existing entry overrides it.
func TestMergeProfileMCPIntoTOML_ProfileOverridesExisting(t *testing.T) {
	t.Parallel()

	existingTOML := `
[mcp_servers]
[mcp_servers.my-tool]
transport = "http"
url = "http://old-server:9000/mcp"
`

	profileServers := map[string]config.MCPServerConfig{
		"my-tool": {
			Transport: "stdio",
			Command:   "/usr/bin/new-my-tool",
		},
	}

	result, err := mergeProfileMCPIntoTOML(existingTOML, profileServers)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML: %v", err)
	}

	var parsed struct {
		MCPServers map[string]config.MCPServerConfig `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(result, &parsed); err != nil {
		t.Fatalf("parse result TOML: %v (got: %s)", err, result)
	}

	srv, ok := parsed.MCPServers["my-tool"]
	if !ok {
		t.Fatalf("expected mcp_servers[\"my-tool\"] in result")
	}
	// Profile's stdio command should win.
	if srv.Transport != "stdio" {
		t.Errorf("expected profile to override transport to %q, got %q", "stdio", srv.Transport)
	}
	if srv.Command != "/usr/bin/new-my-tool" {
		t.Errorf("expected profile to override command to %q, got %q", "/usr/bin/new-my-tool", srv.Command)
	}
	// Old http URL should be gone (replaced by profile entry).
	if strings.Contains(result, "old-server") {
		t.Errorf("expected old server URL to be overridden, but found it in: %s", result)
	}
}

// TestMergeProfileMCPIntoTOML_NilProfileServers verifies that nil/empty
// profile servers return the existing TOML unchanged.
func TestMergeProfileMCPIntoTOML_NilProfileServers(t *testing.T) {
	t.Parallel()

	existingTOML := `model = "gpt-4.1-mini"
`

	result, err := mergeProfileMCPIntoTOML(existingTOML, nil)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML with nil servers: %v", err)
	}
	if result != existingTOML {
		t.Errorf("expected existing TOML unchanged, got %q", result)
	}

	result2, err := mergeProfileMCPIntoTOML(existingTOML, map[string]config.MCPServerConfig{})
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML with empty servers: %v", err)
	}
	if result2 != existingTOML {
		t.Errorf("expected existing TOML unchanged with empty map, got %q", result2)
	}
}

// TestMergeProfileMCPIntoTOML_InvalidExistingTOML verifies that an invalid
// existing TOML string returns an error.
func TestMergeProfileMCPIntoTOML_InvalidExistingTOML(t *testing.T) {
	t.Parallel()

	profileServers := map[string]config.MCPServerConfig{
		"tool": {Transport: "stdio", Command: "/bin/tool"},
	}

	_, err := mergeProfileMCPIntoTOML("not valid toml = [[[", profileServers)
	if err == nil {
		t.Error("expected error for invalid existing TOML, got nil")
	}
}

// TestMergeProfileMCPIntoTOML_HTTPServer verifies that HTTP transport servers
// are merged correctly (only URL field, no command/args).
func TestMergeProfileMCPIntoTOML_HTTPServer(t *testing.T) {
	t.Parallel()

	profileServers := map[string]config.MCPServerConfig{
		"remote-tool": {
			Transport: "http",
			URL:       "http://remote-server:3001/mcp",
		},
	}

	result, err := mergeProfileMCPIntoTOML("", profileServers)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML: %v", err)
	}

	var parsed struct {
		MCPServers map[string]config.MCPServerConfig `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(result, &parsed); err != nil {
		t.Fatalf("parse result TOML: %v (got: %s)", err, result)
	}

	srv, ok := parsed.MCPServers["remote-tool"]
	if !ok {
		t.Fatalf("expected mcp_servers[\"remote-tool\"] in result")
	}
	if srv.Transport != "http" {
		t.Errorf("expected transport %q, got %q", "http", srv.Transport)
	}
	if srv.URL != "http://remote-server:3001/mcp" {
		t.Errorf("expected URL %q, got %q", "http://remote-server:3001/mcp", srv.URL)
	}
}

// TestMergeProfileMCPIntoTOML_PreservesNonMCPFields verifies that non-mcp_servers
// fields in the existing TOML are preserved after the merge.
func TestMergeProfileMCPIntoTOML_PreservesNonMCPFields(t *testing.T) {
	t.Parallel()

	existingTOML := `model = "gpt-4.1-mini"
max_steps = 20
`

	profileServers := map[string]config.MCPServerConfig{
		"my-tool": {Transport: "stdio", Command: "/bin/tool"},
	}

	result, err := mergeProfileMCPIntoTOML(existingTOML, profileServers)
	if err != nil {
		t.Fatalf("mergeProfileMCPIntoTOML: %v", err)
	}

	var parsed struct {
		Model      string                            `toml:"model"`
		MaxSteps   int                               `toml:"max_steps"`
		MCPServers map[string]config.MCPServerConfig `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(result, &parsed); err != nil {
		t.Fatalf("parse result TOML: %v (got: %s)", err, result)
	}

	if parsed.Model != "gpt-4.1-mini" {
		t.Errorf("expected model %q preserved, got %q", "gpt-4.1-mini", parsed.Model)
	}
	if parsed.MaxSteps != 20 {
		t.Errorf("expected max_steps 20 preserved, got %d", parsed.MaxSteps)
	}
	if _, ok := parsed.MCPServers["my-tool"]; !ok {
		t.Errorf("expected mcp_servers[\"my-tool\"] added, not found in: %s", result)
	}
}
