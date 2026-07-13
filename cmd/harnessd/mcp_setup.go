package main

import (
	"context"
	"encoding/json"
	"fmt"

	"go-agent-harness/internal/config"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/mcp"
)

// clientManagerRegistry adapts *mcp.ClientManager to the htools.MCPRegistry interface
// so it can be passed to DefaultRegistryOptions and RunnerConfig.
type clientManagerRegistry struct {
	cm *mcp.ClientManager
}

// ListTools returns all tools across all registered servers, keyed by server name.
func (r *clientManagerRegistry) ListTools(ctx context.Context) (map[string][]htools.MCPToolDefinition, error) {
	result := make(map[string][]htools.MCPToolDefinition)
	for _, serverName := range r.cm.ListServers() {
		defs, err := r.cm.DiscoverTools(ctx, serverName)
		if err != nil {
			return nil, fmt.Errorf("list tools from MCP server %q: %w", serverName, err)
		}
		toolDefs := make([]htools.MCPToolDefinition, 0, len(defs))
		for _, d := range defs {
			params := make(map[string]any)
			if d.InputSchema != nil {
				_ = json.Unmarshal(d.InputSchema, &params)
			}
			toolDefs = append(toolDefs, htools.MCPToolDefinition{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			})
		}
		result[serverName] = toolDefs
	}
	return result, nil
}

// CallTool invokes a tool on the named server.
func (r *clientManagerRegistry) CallTool(ctx context.Context, server, tool string, args json.RawMessage) (string, error) {
	return r.cm.ExecuteTool(ctx, server, tool, args)
}

// ListResources returns the resources advertised by the named MCP server.
func (r *clientManagerRegistry) ListResources(ctx context.Context, server string) ([]htools.MCPResource, error) {
	defs, err := r.cm.ListResources(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("list resources from MCP server %q: %w", server, err)
	}
	resources := make([]htools.MCPResource, 0, len(defs))
	for _, d := range defs {
		resources = append(resources, htools.MCPResource{
			URI:  d.URI,
			Name: d.Name,
		})
	}
	return resources, nil
}

// ReadResource reads the content of a resource by URI from the named MCP server.
func (r *clientManagerRegistry) ReadResource(ctx context.Context, server, uri string) (string, error) {
	content, err := r.cm.ReadResource(ctx, server, uri)
	if err != nil {
		return "", fmt.Errorf("read resource %q from MCP server %q: %w", uri, server, err)
	}
	return content, nil
}

// registerMCPServersFromConfig registers MCP servers from TOML config and env
// var sources into the given ClientManager.
//
// Registration order:
//  1. TOML servers are registered first (they take precedence).
//  2. Env var servers are registered next; if a name collision occurs, the env
//     var entry is skipped and a log message is emitted via logf.
//
// Transport inference: if a TOML server's Transport field is empty, it is
// inferred from the other fields — "http" when URL is non-empty, "stdio"
// otherwise.
func registerMCPServersFromConfig(
	manager *mcp.ClientManager,
	tomlServers map[string]config.MCPServerConfig,
	envServers []mcp.ServerConfig,
	logf func(string, ...any),
) {
	// Register TOML servers first.
	for name, srv := range tomlServers {
		transport := srv.Transport
		if transport == "" {
			if srv.URL != "" {
				transport = "http"
			} else {
				transport = "stdio"
			}
		}
		sc := mcp.ServerConfig{
			Name:      name,
			Transport: transport,
			Command:   srv.Command,
			Args:      srv.Args,
			URL:       srv.URL,
		}
		if addErr := manager.AddServer(sc); addErr != nil {
			logf("warning: failed to register TOML MCP server %q: %v", name, addErr)
		} else {
			logf("registered MCP server %q from config (transport=%s)", name, transport)
		}
	}

	// Register env var servers, skipping any names already registered from TOML.
	registered := make(map[string]struct{}, len(tomlServers))
	for name := range tomlServers {
		registered[name] = struct{}{}
	}
	for _, sc := range envServers {
		if _, exists := registered[sc.Name]; exists {
			logf("mcp: skipping env var server %q: already registered from TOML config", sc.Name)
			continue
		}
		if addErr := manager.AddServer(sc); addErr != nil {
			logf("warning: failed to register MCP server %q: %v", sc.Name, addErr)
		} else {
			logf("registered MCP server %q (transport=%s)", sc.Name, sc.Transport)
		}
	}
}
