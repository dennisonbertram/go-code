package relay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ComposeRequest is the input to the contract composer.
type ComposeRequest struct {
	// Prompt is the task description from the user or connector.
	Prompt string

	// Source is the trigger source information.
	Source TriggerSource

	// TenantID scopes the run to a tenant.
	TenantID string

	// RequestedTools are the tools the caller wants to use.
	RequestedTools []string

	// RequestedMCPServers are MCP servers the caller wants to use.
	RequestedMCPServers []string

	// RequestedMemoryRefs are memory sources the caller wants to access.
	RequestedMemoryRefs []string

	// RequestedOutputSurfaces are output targets the caller wants to write to.
	RequestedOutputSurfaces []string

	// RepoURL is the target repository.
	RepoURL string

	// WorkspaceMode is the requested workspace type.
	WorkspaceMode string

	// PreferCleanWorkspace requests a clean (non-local) workspace.
	PreferCleanWorkspace bool

	// ContextHint is additional context from the trigger source.
	ContextHint string

	// MaxTurns limits the number of LLM turns.
	MaxTurns int

	// BudgetTokens limits token usage.
	BudgetTokens int

	// TimeoutSeconds limits execution time.
	TimeoutSeconds int

	// CreatedBy identifies who/what created this request.
	CreatedBy string

	// Tags are arbitrary labels for categorization.
	Tags []string

	// LocalOnly restricts execution to local workers.
	LocalOnly bool
}

// Composer turns user/connector intent into a validated RunContract.
type Composer struct {
	// MaxContextHintBytes is the maximum size of context hint to include.
	MaxContextHintBytes int
}

// NewComposer creates a new contract composer with sensible defaults.
func NewComposer() *Composer {
	return &Composer{
		MaxContextHintBytes: 64 * 1024, // 64 KiB
	}
}

// Compose produces a RunContract from a ComposeRequest.
// The caller is responsible for routing/placement after composition.
func (c *Composer) Compose(req ComposeRequest) (*RunContract, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("compose: prompt is required")
	}

	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}

	contractID := generateContractID()
	now := time.Now().UTC()

	// Build the capability pack from requested items.
	capPack := c.buildCapabilityPack(req)

	// Build output expectations.
	outputs := c.buildOutputs(req)

	// Build workspace target.
	workspace := WorkspaceTarget{
		Mode:  req.WorkspaceMode,
		Clean: req.PreferCleanWorkspace,
	}
	if req.RepoURL != "" {
		workspace.RepoURL = req.RepoURL
	}
	if workspace.Mode == "" {
		workspace.Mode = "local"
	}

	// Truncate context hint.
	contextHint := c.truncateContextHint(req.ContextHint)

	contract := &RunContract{
		ID:           contractID,
		Prompt:       req.Prompt,
		Source:       req.Source,
		Workspace:    workspace,
		Capabilities: capPack,
		Permissions: PermissionSet{
			ApprovalRequired: c.defaultApprovalRequired(req),
		},
		Limits: RunLimits{
			MaxTurns:       req.MaxTurns,
			BudgetTokens:   req.BudgetTokens,
			TimeoutSeconds: req.TimeoutSeconds,
		},
		Outputs: outputs,
		Mobility: MobilityResumable,
		Metadata: RunMetadata{
			TenantID:  tenantID,
			CreatedBy: req.CreatedBy,
			CreatedAt: now,
			Tags:      req.Tags,
		},
		ContextHint: contextHint,
	}

	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("compose: validation: %w", err)
	}

	return contract, nil
}

// buildCapabilityPack converts request items into a CapabilityPack.
func (c *Composer) buildCapabilityPack(req ComposeRequest) CapabilityPack {
	pack := CapabilityPack{
		RunID: "", // Will be set after contract ID is generated
	}

	for _, t := range req.RequestedTools {
		pack.Tools = append(pack.Tools, ToolCapability{Name: t})
	}
	for _, m := range req.RequestedMCPServers {
		pack.MCPServers = append(pack.MCPServers, MCPServerCapability{Name: m})
	}
	for _, m := range req.RequestedMemoryRefs {
		pack.Memories = append(pack.Memories, MemoryCapability{Name: m})
	}
	for _, s := range req.RequestedOutputSurfaces {
		pack.OutputSurfaces = append(pack.OutputSurfaces, OutputSurfaceCapability{Type: s})
	}

	return pack
}

// buildOutputs creates output expectations from the request.
func (c *Composer) buildOutputs(req ComposeRequest) []OutputExpectation {
	var outputs []OutputExpectation

	for _, surface := range req.RequestedOutputSurfaces {
		outputs = append(outputs, OutputExpectation{
			Type:   surface,
			Target: surface,
		})
	}

	// Default output: summary for direct API calls without explicit outputs.
	if len(outputs) == 0 && req.Source.Type == "api" {
		outputs = append(outputs, OutputExpectation{
			Type:   "summary",
			Format: "markdown",
		})
	}

	return outputs
}

// defaultApprovalRequired returns the default set of actions requiring approval.
func (c *Composer) defaultApprovalRequired(req ComposeRequest) []string {
	defaults := []string{"bash:destructive", "git:push", "write:outside_workspace"}
	if req.LocalOnly {
		// Local-only runs are more trusted; fewer approval requirements.
		return nil
	}
	return defaults
}

// truncateContextHint truncates the context hint to the maximum allowed size.
func (c *Composer) truncateContextHint(hint string) string {
	if len(hint) <= c.MaxContextHintBytes {
		return hint
	}
	marker := "\n\n[... context truncated: exceeded max hint size ...]"
	cutAt := c.MaxContextHintBytes - len(marker)
	if cutAt < 0 {
		cutAt = 0
	}
	return hint[:cutAt] + marker
}

// generateContractID produces a unique contract ID.
func generateContractID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "rc-" + hex.EncodeToString(b)
}
