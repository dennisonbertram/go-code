package relay

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CapabilityType classifies what kind of capability is being advertised or granted.
type CapabilityType string

const (
	CapabilityTool          CapabilityType = "tool"
	CapabilityMCPServer     CapabilityType = "mcp_server"
	CapabilityMemory        CapabilityType = "memory"
	CapabilityRepo          CapabilityType = "repo"
	CapabilityWorkspaceMode CapabilityType = "workspace_mode"
	CapabilitySecret        CapabilityType = "secret"
	CapabilityOutputSurface CapabilityType = "output_surface"
	CapabilityBrowser       CapabilityType = "browser"
	CapabilityDocker        CapabilityType = "docker"
)

// ValidCapabilityTypes is the set of recognized capability types.
var ValidCapabilityTypes = map[CapabilityType]bool{
	CapabilityTool:          true,
	CapabilityMCPServer:     true,
	CapabilityMemory:        true,
	CapabilityRepo:          true,
	CapabilityWorkspaceMode: true,
	CapabilitySecret:        true,
	CapabilityOutputSurface: true,
	CapabilityBrowser:       true,
	CapabilityDocker:        true,
}

// ToolCapability describes a tool a worker can execute.
type ToolCapability struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes,omitempty"` // e.g. "read", "write", "destructive"
}

// MCPServerCapability describes an MCP server a worker can connect to.
type MCPServerCapability struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Transport   string `json:"transport,omitempty"` // "stdio", "sse", "http"
	// SecretRef points to a secret reference, never contains the actual secret value.
	SecretRef string `json:"secret_ref,omitempty"`
}

// MemoryCapability describes a memory source a worker can access.
type MemoryCapability struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Scope       string `json:"scope"` // "personal", "repo", "team", "connector_thread", "run_local"
	Description string `json:"description,omitempty"`
}

// RepoCapability describes repository access a worker has.
type RepoCapability struct {
	RepoURL  string `json:"repo_url"`
	RepoPath string `json:"repo_path,omitempty"` // Only safe for local workers; redacted for remote
	AuthType string `json:"auth_type,omitempty"` // "ssh", "token", "none"
	// SecretRef for repo authentication; never contains the actual secret value.
	SecretRef string `json:"secret_ref,omitempty"`
}

// WorkspaceModeCapability describes a workspace type a worker supports.
type WorkspaceModeCapability struct {
	Mode        string `json:"mode"` // "local", "worktree", "container", "vm", "sandbox"
	DisplayName string `json:"display_name,omitempty"`
	MaxSizeMB   int    `json:"max_size_mb,omitempty"`
}

// SecretCapability is a reference to a secret a worker can resolve.
// NEVER contains the actual secret value — only a reference identifier.
type SecretCapability struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Scope       string `json:"scope"`              // "repo", "org", "personal", "run"
	Provider    string `json:"provider,omitempty"` // "env", "vault", "aws-secrets", "gcp-secrets"
	// Ref is the reference identifier for the secret, never the value.
	Ref string `json:"ref"`
}

// OutputSurfaceCapability describes a connector output surface a worker can write to.
type OutputSurfaceCapability struct {
	Type        string `json:"type"` // "github:comment", "github:pr", "slack:reply", "linear:update"
	DisplayName string `json:"display_name,omitempty"`
}

// BrowserCapability describes browser/UI access a worker has.
type BrowserCapability struct {
	Available bool   `json:"available"`
	Driver    string `json:"driver,omitempty"` // "chromedp", "playwright", "none"
}

// DockerCapability describes Docker/test support a worker has.
type DockerCapability struct {
	Available   bool     `json:"available"`
	Runtimes    []string `json:"runtimes,omitempty"`
	MaxMemoryMB int      `json:"max_memory_mb,omitempty"`
}

// CapabilityInventory represents everything a worker advertises it can provide.
type CapabilityInventory struct {
	// WorkerID links this inventory to its worker.
	WorkerID string `json:"worker_id"`

	// Tools lists the tools this worker can execute.
	Tools []ToolCapability `json:"tools,omitempty"`

	// MCPServers lists MCP servers this worker can connect to.
	MCPServers []MCPServerCapability `json:"mcp_servers,omitempty"`

	// Memories lists memory sources this worker can access.
	Memories []MemoryCapability `json:"memories,omitempty"`

	// Repos lists repositories this worker has checked out or can access.
	Repos []RepoCapability `json:"repos,omitempty"`

	// WorkspaceModes lists workspace types this worker can provision.
	WorkspaceModes []WorkspaceModeCapability `json:"workspace_modes,omitempty"`

	// Secrets lists secret references this worker can resolve.
	Secrets []SecretCapability `json:"secrets,omitempty"`

	// OutputSurfaces lists connector output surfaces this worker can write to.
	OutputSurfaces []OutputSurfaceCapability `json:"output_surfaces,omitempty"`

	// Browser describes browser/UI access.
	Browser *BrowserCapability `json:"browser,omitempty"`

	// Docker describes Docker/test support.
	Docker *DockerCapability `json:"docker,omitempty"`

	// UpdatedAt is when this inventory was last modified.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// CapabilityPack is the bounded subset of capabilities actually granted to a
// specific run contract. It separates advertised from granted capabilities
// and ensures every granted capability was explicitly approved.
type CapabilityPack struct {
	// RunID links this pack to a specific run contract.
	RunID string `json:"run_id"`

	// Tools granted to this run.
	Tools []ToolCapability `json:"tools,omitempty"`

	// MCPServers granted to this run.
	MCPServers []MCPServerCapability `json:"mcp_servers,omitempty"`

	// Memories granted to this run.
	Memories []MemoryCapability `json:"memories,omitempty"`

	// Repos granted to this run.
	Repos []RepoCapability `json:"repos,omitempty"`

	// WorkspaceModes granted to this run.
	WorkspaceModes []WorkspaceModeCapability `json:"workspace_modes,omitempty"`

	// Secrets granted to this run.
	Secrets []SecretCapability `json:"secrets,omitempty"`

	// OutputSurfaces granted to this run.
	OutputSurfaces []OutputSurfaceCapability `json:"output_surfaces,omitempty"`

	// Browser access granted to this run, if any.
	Browser *BrowserCapability `json:"browser,omitempty"`

	// Docker access granted to this run, if any.
	Docker *DockerCapability `json:"docker,omitempty"`
}

// HasTool returns true if the pack includes a tool with the given name.
func (cp *CapabilityPack) HasTool(name string) bool {
	for _, t := range cp.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// HasSecret returns true if the pack includes a secret with the given ref.
func (cp *CapabilityPack) HasSecret(ref string) bool {
	for _, s := range cp.Secrets {
		if s.Ref == ref {
			return true
		}
	}
	return false
}

// HasMCPServer returns true if the pack includes an MCP server with the given name.
func (cp *CapabilityPack) HasMCPServer(name string) bool {
	for _, m := range cp.MCPServers {
		if m.Name == name {
			return true
		}
	}
	return false
}

// ToJSON marshals the capability pack to JSON.
func (cp *CapabilityPack) ToJSON() ([]byte, error) {
	return json.Marshal(cp)
}

// CapabilityPackFromJSON unmarshals a capability pack from JSON.
func CapabilityPackFromJSON(data []byte) (*CapabilityPack, error) {
	cp := &CapabilityPack{}
	if err := json.Unmarshal(data, cp); err != nil {
		return nil, fmt.Errorf("relay: unmarshal capability pack: %w", err)
	}
	return cp, nil
}

// Sentinel errors for capability operations.
var (
	ErrCapabilityNotFound    = errors.New("relay: capability not found")
	ErrInvalidCapabilityType = errors.New("relay: invalid capability type")
	ErrSecretValueNotAllowed = errors.New("relay: secret values must not be stored in capability records")
)

// ValidateCapabilityType checks that a capability type is recognized.
func ValidateCapabilityType(ct CapabilityType) error {
	if !ValidCapabilityTypes[ct] {
		return ErrInvalidCapabilityType
	}
	return nil
}

// SanitizeInventoryForDisplay returns a copy of the inventory safe for operator display.
// It redacts repo paths for non-local workers and ensures no secret values are present.
func SanitizeInventoryForDisplay(inv *CapabilityInventory, locationType LocationType) *CapabilityInventory {
	if inv == nil {
		return nil
	}
	cp := *inv

	// Redact repo paths for non-local workers.
	if locationType != LocationLocal {
		for i := range cp.Repos {
			cp.Repos[i].RepoPath = "[redacted: non-local worker]"
			cp.Repos[i].SecretRef = "[redacted]"
		}
	}

	// Ensure secret refs don't contain values.
	for i := range cp.Secrets {
		// Secret refs should always be references, but double-check they don't
		// look like actual values (e.g. containing ':' or being very long).
		if len(cp.Secrets[i].Ref) > 128 {
			cp.Secrets[i].Ref = "[invalid: ref too long]"
		}
	}

	return &cp
}

// SanitizePackForDisplay returns a copy of the capability pack safe for operator display.
func SanitizePackForDisplay(pack *CapabilityPack, locationType LocationType) *CapabilityPack {
	if pack == nil {
		return nil
	}
	cp := *pack

	// Redact repo paths for non-local workers.
	if locationType != LocationLocal {
		for i := range cp.Repos {
			cp.Repos[i].RepoPath = "[redacted: non-local worker]"
			cp.Repos[i].SecretRef = "[redacted]"
		}
	}

	// Ensure secret refs don't contain values.
	for i := range cp.Secrets {
		if len(cp.Secrets[i].Ref) > 128 {
			cp.Secrets[i].Ref = "[invalid: ref too long]"
		}
	}

	return &cp
}
