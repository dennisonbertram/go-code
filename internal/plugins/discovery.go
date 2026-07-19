package plugins

import (
	"fmt"
	"os"
	"path/filepath"

	"go-agent-harness/internal/mcp"
)

// EnabledBundles resolves validated bundles for enabled installed plugins.
func EnabledBundles(root string, store *StateStore) ([]*Bundle, error) {
	items, err := store.List()
	if err != nil {
		return nil, err
	}
	var bundles []*Bundle
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		bundle, err := LoadBundle(filepath.Join(root, item.Name, item.Version))
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

// TrustedBundles returns only enabled bundles whose executable surfaces are
// explicitly trusted. Skills and commands intentionally use EnabledBundles.
func TrustedBundles(root string, store *StateStore) ([]*Bundle, error) {
	items, err := store.List()
	if err != nil {
		return nil, err
	}
	var bundles []*Bundle
	for _, item := range items {
		if !item.Enabled || !item.Trusted {
			continue
		}
		bundle, err := LoadBundle(filepath.Join(root, item.Name, item.Version))
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

// MCPServers parses all trusted bundle MCP files with internal/mcp's existing
// validation path. Calling this never starts a process; the caller decides
// when to register the resulting configs.
func MCPServers(bundles []*Bundle) ([]mcp.ServerConfig, error) {
	var servers []mcp.ServerConfig
	for _, bundle := range bundles {
		if bundle.MCPPath == "" {
			continue
		}
		data, err := os.ReadFile(bundle.MCPPath)
		if err != nil {
			return nil, fmt.Errorf("read plugin MCP file: %w", err)
		}
		parsed, err := mcp.ParseServerConfigsJSON(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse plugin MCP file %s: %w", bundle.MCPPath, err)
		}
		servers = append(servers, parsed...)
	}
	return servers, nil
}
