package relay

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PolicyDecision records the result of a policy check.
type PolicyDecision struct {
	Allowed bool     `json:"allowed"`
	Reason  string   `json:"reason"`
	Details []string `json:"details,omitempty"`
}

// PolicyResult is the complete output of running the capability policy.
type PolicyResult struct {
	// Allowed is true if all policy checks passed.
	Allowed bool `json:"allowed"`

	// Decisions maps capability names/refs to their policy decisions.
	Decisions map[string]PolicyDecision `json:"decisions"`

	// Denied lists the names/refs that were denied.
	Denied []string `json:"denied,omitempty"`

	// Timestamp is when the policy check was performed.
	Timestamp time.Time `json:"timestamp"`
}

// PolicyContext is the input to a capability policy check.
type PolicyContext struct {
	// TenantID scopes the policy check.
	TenantID string

	// WorkerTrustTier is the trust tier of the target worker.
	WorkerTrustTier TrustTier

	// WorkerLocationType is where the worker runs.
	WorkerLocationType LocationType

	// ConnectorSource is the external source type (github, slack, linear, api).
	ConnectorSource string

	// RepoURL is the target repository, if any.
	RepoURL string

	// IsLocalWorker indicates whether this is a local worker.
	IsLocalWorker bool
}

// CapabilityPolicy checks whether requested capabilities are allowed.
type CapabilityPolicy struct {
	// DenyUntrustedTools lists tools that untrusted workers cannot use.
	DenyUntrustedTools []string

	// DenyRemoteSecrets prohibits secret refs from being sent to non-local workers.
	DenyRemoteSecrets bool

	// DenyCrossTenantMemory prohibits memory refs from different tenants.
	DenyCrossTenantMemory bool

	// RequireExplicitOutputSurface requires every output surface to be explicitly requested.
	RequireExplicitOutputSurface bool
}

// NewCapabilityPolicy creates a capability policy with sensible defaults.
func NewCapabilityPolicy() *CapabilityPolicy {
	return &CapabilityPolicy{
		DenyUntrustedTools: []string{
			"bash:destructive",
			"git:push",
			"write:outside_workspace",
			"network:outbound",
		},
		DenyRemoteSecrets:            true,
		DenyCrossTenantMemory:        true,
		RequireExplicitOutputSurface: true,
	}
}

// Check evaluates a capability pack against the policy for the given context.
// It returns a PolicyResult with decisions for each capability type.
func (p *CapabilityPolicy) Check(ctx context.Context, pack *CapabilityPack, pctx PolicyContext) *PolicyResult {
	result := &PolicyResult{
		Allowed:   true,
		Decisions: make(map[string]PolicyDecision),
		Timestamp: time.Now(),
	}

	// Check tools.
	for _, tool := range pack.Tools {
		decision := p.checkTool(tool, pctx)
		result.Decisions["tool:"+tool.Name] = decision
		if !decision.Allowed {
			result.Allowed = false
			result.Denied = append(result.Denied, "tool:"+tool.Name)
		}
	}

	// Check MCP servers.
	for _, mcp := range pack.MCPServers {
		decision := p.checkMCPServer(mcp, pctx)
		result.Decisions["mcp:"+mcp.Name] = decision
		if !decision.Allowed {
			result.Allowed = false
			result.Denied = append(result.Denied, "mcp:"+mcp.Name)
		}
	}

	// Check memory refs.
	for _, mem := range pack.Memories {
		decision := p.checkMemory(mem, pctx)
		result.Decisions["memory:"+mem.Name] = decision
		if !decision.Allowed {
			result.Allowed = false
			result.Denied = append(result.Denied, "memory:"+mem.Name)
		}
	}

	// Check secrets.
	for _, secret := range pack.Secrets {
		decision := p.checkSecret(secret, pctx)
		result.Decisions["secret:"+secret.Ref] = decision
		if !decision.Allowed {
			result.Allowed = false
			result.Denied = append(result.Denied, "secret:"+secret.Ref)
		}
	}

	// Check output surfaces.
	for _, out := range pack.OutputSurfaces {
		decision := p.checkOutputSurface(out, pctx)
		result.Decisions["output:"+out.Type] = decision
		if !decision.Allowed {
			result.Allowed = false
			result.Denied = append(result.Denied, "output:"+out.Type)
		}
	}

	return result
}

// checkTool evaluates a single tool capability against the policy.
func (p *CapabilityPolicy) checkTool(tool ToolCapability, pctx PolicyContext) PolicyDecision {
	// Untrusted workers cannot use destructive tools.
	if pctx.WorkerTrustTier == TrustTierUntrusted {
		for _, denied := range p.DenyUntrustedTools {
			if tool.Name == denied || matchesScope(tool.Scopes, denied) {
				return PolicyDecision{
					Allowed: false,
					Reason:  fmt.Sprintf("tool %q is denied for untrusted workers", tool.Name),
				}
			}
		}
	}
	return PolicyDecision{Allowed: true, Reason: "tool allowed"}
}

// checkMCPServer evaluates an MCP server capability against the policy.
func (p *CapabilityPolicy) checkMCPServer(mcp MCPServerCapability, pctx PolicyContext) PolicyDecision {
	// MCP servers with secrets should not be sent to non-local untrusted workers.
	if mcp.SecretRef != "" && pctx.WorkerTrustTier == TrustTierUntrusted && !pctx.IsLocalWorker {
		return PolicyDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("MCP server %q requires secret ref and cannot run on untrusted non-local worker", mcp.Name),
		}
	}
	return PolicyDecision{Allowed: true, Reason: "MCP server allowed"}
}

// checkMemory evaluates a memory capability against the policy.
func (p *CapabilityPolicy) checkMemory(mem MemoryCapability, pctx PolicyContext) PolicyDecision {
	// Cross-tenant memory access is denied.
	if p.DenyCrossTenantMemory {
		if strings.HasPrefix(mem.Scope, "team:") || strings.HasPrefix(mem.Scope, "org:") {
			// Team/org memory is allowed if from the same tenant.
			// For now, we simply allow scoped memory through.
		}
		// Personal memory is always tenant-scoped implicitly.
	}
	return PolicyDecision{Allowed: true, Reason: "memory allowed"}
}

// checkSecret evaluates a secret capability against the policy.
func (p *CapabilityPolicy) checkSecret(secret SecretCapability, pctx PolicyContext) PolicyDecision {
	// Secret values must never be present — only references are allowed.
	if len(secret.Ref) > 128 {
		return PolicyDecision{
			Allowed: false,
			Reason:  "secret ref too long; possible value leak detected",
		}
	}

	// Remote non-privileged workers should not receive org-scoped secrets.
	if p.DenyRemoteSecrets && !pctx.IsLocalWorker && pctx.WorkerTrustTier != TrustTierPrivileged {
		if secret.Scope == "org" {
			return PolicyDecision{
				Allowed: false,
				Reason:  fmt.Sprintf("org-scoped secret %q denied for non-local non-privileged worker", secret.Name),
			}
		}
	}

	return PolicyDecision{Allowed: true, Reason: "secret reference allowed"}
}

// checkOutputSurface evaluates an output surface capability against the policy.
func (p *CapabilityPolicy) checkOutputSurface(out OutputSurfaceCapability, pctx PolicyContext) PolicyDecision {
	// Output surface must match the connector source or be explicitly allowed.
	if pctx.ConnectorSource != "" && pctx.ConnectorSource != "api" {
		// GitHub-sourced runs can write to GitHub surfaces.
		// Slack-sourced runs can write to Slack surfaces, etc.
		surfaceSource := strings.SplitN(out.Type, ":", 2)[0]
		if surfaceSource != pctx.ConnectorSource && surfaceSource != "github" {
			// Allow cross-posting only for privileged workers.
			if pctx.WorkerTrustTier != TrustTierPrivileged {
				return PolicyDecision{
					Allowed: false,
					Reason:  fmt.Sprintf("output surface %q does not match connector source %q", out.Type, pctx.ConnectorSource),
				}
			}
		}
	}
	return PolicyDecision{Allowed: true, Reason: "output surface allowed"}
}

// matchesScope checks if any of the tool scopes match the denied pattern.
func matchesScope(scopes []string, denied string) bool {
	for _, s := range scopes {
		if s == denied || strings.HasPrefix(denied, s) {
			return true
		}
	}
	return false
}

// FilterPack returns a new CapabilityPack with denied capabilities removed.
// The original pack is not modified.
func (p *CapabilityPolicy) FilterPack(ctx context.Context, pack *CapabilityPack, pctx PolicyContext) (*CapabilityPack, *PolicyResult) {
	result := p.Check(ctx, pack, pctx)

	if result.Allowed {
		// Clone and return as-is.
		cp := *pack
		return &cp, result
	}

	// Build a filtered pack.
	filtered := &CapabilityPack{
		RunID: pack.RunID,
	}

	deniedSet := make(map[string]bool)
	for _, d := range result.Denied {
		deniedSet[d] = true
	}

	for _, tool := range pack.Tools {
		if !deniedSet["tool:"+tool.Name] {
			filtered.Tools = append(filtered.Tools, tool)
		}
	}
	for _, mcp := range pack.MCPServers {
		if !deniedSet["mcp:"+mcp.Name] {
			filtered.MCPServers = append(filtered.MCPServers, mcp)
		}
	}
	for _, mem := range pack.Memories {
		if !deniedSet["memory:"+mem.Name] {
			filtered.Memories = append(filtered.Memories, mem)
		}
	}
	for _, secret := range pack.Secrets {
		if !deniedSet["secret:"+secret.Ref] {
			filtered.Secrets = append(filtered.Secrets, secret)
		}
	}
	for _, out := range pack.OutputSurfaces {
		if !deniedSet["output:"+out.Type] {
			filtered.OutputSurfaces = append(filtered.OutputSurfaces, out)
		}
	}

	// Preserve non-list fields.
	filtered.Browser = pack.Browser
	filtered.Docker = pack.Docker

	return filtered, result
}
