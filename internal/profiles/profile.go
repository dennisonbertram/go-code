// Package profiles implements the named agent profile system for the harness.
//
// A profile is a reusable subagent configuration: tool allowlist, model,
// max_steps, system prompt, and cost ceiling. Profiles are stored as TOML
// files with a defined [meta], [runner], [tools], and [mcp_servers] section.
//
// Resolution order (highest priority first):
//  1. Project-level: .harness/profiles/<name>.toml
//  2. User-global:   ~/.harness/profiles/<name>.toml
//  3. Built-in:      embedded in the binary
package profiles

import (
	"time"

	"go-agent-harness/internal/config"
)

// Profile holds the full configuration for a named agent profile.
type Profile struct {
	// Extends declares an optional parent profile name for inheritance.
	// The child profile inherits unresolved fields from the base profile.
	Extends string `toml:"extends" json:"extends,omitempty"`

	Meta       ProfileMeta                       `toml:"meta"`
	Runner     ProfileRunner                     `toml:"runner"`
	Tools      ProfileTools                      `toml:"tools"`
	MCPServers map[string]config.MCPServerConfig `toml:"mcp_servers,omitempty"`

	// Permissions encodes the sandbox/approval policy for this profile.
	// Zero value means "inherit from runner/request defaults" (no override).
	Permissions ProfilePermissions `toml:"permissions" json:"permissions,omitempty"`

	// IsolationMode selects the workspace isolation backend.
	// Valid values: "none", "worktree", "container", "vm".
	// Empty string means "inherit from defaults".
	IsolationMode string `toml:"isolation_mode" json:"isolation_mode,omitempty"`

	// CleanupPolicy controls workspace lifecycle after the run completes.
	// Valid values: "keep", "delete", "delete_on_success".
	// Empty string means "inherit from defaults".
	CleanupPolicy string `toml:"cleanup_policy" json:"cleanup_policy,omitempty"`

	// BaseRef is the git ref to use as base for worktree-backed runs (e.g. "main").
	// Empty string means "inherit from runner defaults".
	BaseRef string `toml:"base_ref" json:"base_ref,omitempty"`

	// ResultMode controls how child/subagent output is formatted.
	// Valid values: "summary", "full", "structured".
	// Empty string means "inherit from defaults".
	ResultMode string `toml:"result_mode" json:"result_mode,omitempty"`
}

// ProfileMeta holds profile metadata.
type ProfileMeta struct {
	Name            string  `toml:"name"`
	Description     string  `toml:"description"`
	Version         int     `toml:"version"`
	CreatedAt       string  `toml:"created_at"`
	CreatedBy       string  `toml:"created_by"` // "built-in" | "agent" | "user"
	EfficiencyScore float64 `toml:"efficiency_score"`
	ReviewCount     int     `toml:"review_count"`
	ReviewEligible  bool    `toml:"review_eligible"` // false for built-ins

	reviewEligibleSet bool `toml:"-"`
}

// ProfileRunner holds runner configuration for the profile.
type ProfileRunner struct {
	Model        string  `toml:"model"`
	MaxSteps     int     `toml:"max_steps"`
	MaxTurns     int     `toml:"max_turns"`
	MaxCostUSD   float64 `toml:"max_cost_usd"`
	SystemPrompt string  `toml:"system_prompt"`
	// ReasoningEffort is the reasoning effort hint forwarded to the provider.
	// Valid values: "low", "medium", "high". Empty means provider default.
	ReasoningEffort string `toml:"reasoning_effort" json:"reasoning_effort,omitempty"`
}

// ProfilePermissions encodes the sandbox/approval safety policy for a profile.
// Zero values mean "no override — inherit from request/runner defaults".
type ProfilePermissions struct {
	// AllowBash controls whether bash/shell tool calls are permitted.
	AllowBash bool `toml:"allow_bash" json:"allow_bash,omitempty"`
	// AllowFileWrite controls whether file-write tool calls are permitted.
	AllowFileWrite bool `toml:"allow_file_write" json:"allow_file_write,omitempty"`
	// AllowNetAccess controls whether network access is permitted.
	AllowNetAccess bool `toml:"allow_net_access" json:"allow_net_access,omitempty"`
	// AllowedCommands is an optional allowlist of shell command names.
	// Nil or empty means no command-level restriction beyond AllowBash.
	AllowedCommands []string `toml:"allowed_commands" json:"allowed_commands,omitempty"`

	allowBashSet      bool `toml:"-"`
	allowFileWriteSet bool `toml:"-"`
	allowNetAccessSet bool `toml:"-"`
}

// ProfileTools holds tool configuration for the profile.
type ProfileTools struct {
	// Allow is the list of tool names permitted for this profile.
	// An empty or nil slice means all tools are allowed.
	Allow []string `toml:"allow"`
}

// ApplyToRunRequest merges profile fields into a RunRequest-compatible struct.
// It returns the values that should be applied, in priority order.
// Fields already set in the destination (non-zero) are NOT overridden by the
// profile — the caller is responsible for applying these only as defaults.
func (p *Profile) ApplyValues() ProfileValues {
	return ProfileValues{
		Model:           p.Runner.Model,
		MaxSteps:        p.Runner.MaxSteps,
		MaxTurns:        p.Runner.MaxTurns,
		MaxCostUSD:      p.Runner.MaxCostUSD,
		SystemPrompt:    p.Runner.SystemPrompt,
		AllowedTools:    append([]string(nil), p.Tools.Allow...),
		ReasoningEffort: p.Runner.ReasoningEffort,
		Permissions:     p.Permissions,
		IsolationMode:   p.IsolationMode,
		CleanupPolicy:   p.CleanupPolicy,
		BaseRef:         p.BaseRef,
		ResultMode:      p.ResultMode,
	}
}

// ProfileValues holds the resolved field values from a profile,
// ready to be applied to a run request.
type ProfileValues struct {
	Model        string
	MaxSteps     int
	MaxTurns     int
	MaxCostUSD   float64
	SystemPrompt string
	AllowedTools []string

	// New runtime and safety policy fields (all backward-compatible: zero = no-op).

	// ReasoningEffort is the reasoning effort hint forwarded to the provider.
	// Empty means provider default.
	ReasoningEffort string
	// Permissions encodes the sandbox/approval policy for this profile.
	Permissions ProfilePermissions
	// IsolationMode selects the workspace isolation backend.
	// Empty means inherit from runner defaults.
	IsolationMode string
	// CleanupPolicy controls workspace lifecycle after the run completes.
	// Empty means inherit from runner defaults.
	CleanupPolicy string
	// BaseRef is the git ref to use as base for worktree-backed runs.
	// Empty means inherit from runner defaults.
	BaseRef string
	// ResultMode controls how child/subagent output is formatted.
	// Empty means inherit from defaults.
	ResultMode string
}

// EfficiencyReport holds the result of a post-run efficiency analysis.
type EfficiencyReport struct {
	RunID                string             `json:"run_id"`
	ProfileName          string             `json:"profile_name"`
	EfficiencyScore      float64            `json:"efficiency_score"`
	ToolRedundancy       []string           `json:"tool_redundancy"`
	UnusedTools          []string           `json:"unused_tools"`
	MissingTools         []string           `json:"missing_tools"`
	SuggestedRefinements ProfileRefinements `json:"suggested_refinements"`
	ReviewerRunID        string             `json:"reviewer_run_id,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
}

// ProfileRefinements holds suggested changes to a profile based on efficiency analysis.
type ProfileRefinements struct {
	RemoveTools          []string `json:"remove_tools,omitempty"`
	AddTools             []string `json:"add_tools,omitempty"`
	SystemPromptAddition string   `json:"system_prompt_addition,omitempty"`
	MaxStepsSuggestion   int      `json:"max_steps_suggestion,omitempty"`
}
