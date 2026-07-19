package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// EnvVarMCPServers is the environment variable holding a JSON array of MCP
// server configurations.
const EnvVarMCPServers = "HARNESS_MCP_SERVERS"

// ParseMCPServersEnv reads HARNESS_MCP_SERVERS from the environment and
// returns the valid server configurations. Invalid entries are logged and
// skipped. If the env var is unset or empty, an empty slice is returned.
func ParseMCPServersEnv() ([]ServerConfig, error) {
	return ParseMCPServersEnvWith(os.Getenv)
}

// ParseMCPServersEnvWith is like ParseMCPServersEnv but uses the provided
// getenv function, making it testable without modifying the real environment.
func ParseMCPServersEnvWith(getenv func(string) string) ([]ServerConfig, error) {
	raw := getenv(EnvVarMCPServers)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	return parseMCPServersJSON(raw)
}

// ParseServerConfigsJSON validates a JSON array using the same parser and
// duplicate-name handling as HARNESS_MCP_SERVERS.
func ParseServerConfigsJSON(raw string) ([]ServerConfig, error) { return parseMCPServersJSON(raw) }

// parseMCPServersJSON parses a JSON array of server config objects. Each
// element is validated independently; invalid entries are logged and skipped.
func parseMCPServersJSON(raw string) ([]ServerConfig, error) {
	// Must be a JSON array at the top level.
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("mcp: %s must be a JSON array, got %q", EnvVarMCPServers, trimmed[:min(len(trimmed), 20)])
	}

	var items []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, fmt.Errorf("mcp: invalid JSON in %s: %w", EnvVarMCPServers, err)
	}

	seen := make(map[string]bool)
	var configs []ServerConfig
	for i, item := range items {
		cfg, err := parseSingleServerConfig(item)
		if err != nil {
			log.Printf("mcp: skipping server entry %d: %v", i, err)
			continue
		}
		if err := validateServerConfig(cfg); err != nil {
			log.Printf("mcp: skipping server %q (entry %d): %v", cfg.Name, i, err)
			continue
		}
		if seen[cfg.Name] {
			log.Printf("mcp: skipping HARNESS_MCP_SERVERS[%d]: duplicate server name %q (first occurrence wins)", i, cfg.Name)
			continue
		}
		seen[cfg.Name] = true
		configs = append(configs, cfg)
	}
	return configs, nil
}

// parseSingleServerConfig unmarshals a single JSON object into a ServerConfig,
// inferring the Transport field when it is not explicitly set.
func parseSingleServerConfig(raw json.RawMessage) (ServerConfig, error) {
	var obj struct {
		Name      string   `json:"name"`
		Transport string   `json:"transport"`
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		URL       string   `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ServerConfig{}, fmt.Errorf("invalid JSON object: %w", err)
	}

	cfg := ServerConfig{
		Name:      strings.TrimSpace(obj.Name),
		Transport: strings.TrimSpace(strings.ToLower(obj.Transport)),
		Command:   strings.TrimSpace(obj.Command),
		Args:      obj.Args,
		URL:       strings.TrimSpace(obj.URL),
	}

	// Infer transport when not explicitly set.
	if cfg.Transport == "" {
		switch {
		case cfg.Command != "" && cfg.URL == "":
			cfg.Transport = "stdio"
		case cfg.URL != "" && cfg.Command == "":
			cfg.Transport = "http"
		}
	}

	return cfg, nil
}

// validateServerConfig checks that a ServerConfig has the required fields and
// does not have conflicting settings.
func validateServerConfig(cfg ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("missing required field \"name\"")
	}
	if cfg.Command == "" && cfg.URL == "" {
		return fmt.Errorf("must specify either \"command\" (stdio) or \"url\" (http)")
	}
	if cfg.Command != "" && cfg.URL != "" {
		return fmt.Errorf("cannot specify both \"command\" and \"url\"")
	}
	switch cfg.Transport {
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("stdio transport requires \"command\"")
		}
	case "http":
		if cfg.URL == "" {
			return fmt.Errorf("http transport requires \"url\"")
		}
	default:
		return fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
	return nil
}
