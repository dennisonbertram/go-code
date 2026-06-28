package relay_test

import (
	"context"
	"testing"

	"go-agent-harness/internal/relay"
)

func TestCapabilityPolicyUntrustedTools(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash:destructive"},
			{Name: "read"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierUntrusted,
	}

	result := p.Check(context.Background(), pack, pctx)

	if result.Allowed {
		t.Error("expected policy to deny untrusted destructive tools")
	}
	if len(result.Denied) != 1 || result.Denied[0] != "tool:bash:destructive" {
		t.Errorf("expected bash:destructive denied, got %v", result.Denied)
	}
	// read should be allowed.
	if d, ok := result.Decisions["tool:read"]; !ok || !d.Allowed {
		t.Error("read should be allowed even for untrusted workers")
	}
}

func TestCapabilityPolicyStandardWorkerAllTools(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash:destructive"},
			{Name: "git:push"},
			{Name: "write:outside_workspace"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierStandard,
	}

	result := p.Check(context.Background(), pack, pctx)
	if !result.Allowed {
		t.Errorf("standard worker should allow all tools, got denied: %v", result.Denied)
	}
}

func TestCapabilityPolicyRemoteSecrets(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Secrets: []relay.SecretCapability{
			{Name: "org-token", Ref: "refs/org", Scope: "org"},
			{Name: "repo-token", Ref: "refs/repo", Scope: "repo"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierStandard,
		IsLocalWorker:   false,
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected org-scoped secret to be denied for non-local non-privileged worker")
	}
}

func TestCapabilityPolicyLocalSecretsAllowed(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Secrets: []relay.SecretCapability{
			{Name: "org-token", Ref: "refs/org", Scope: "org"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierStandard,
		IsLocalWorker:   true,
	}

	result := p.Check(context.Background(), pack, pctx)
	if !result.Allowed {
		t.Errorf("local worker should allow org secrets: %v", result.Denied)
	}
}

func TestCapabilityPolicySecretValueLeakDetection(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Secrets: []relay.SecretCapability{
			{Name: "token", Ref: "this-is-way-too-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", Scope: "repo"},
		},
	}
	pctx := relay.PolicyContext{
		IsLocalWorker: true,
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected long secret ref to be denied (possible value leak)")
	}
}

func TestCapabilityPolicyOutputSurfaceMismatch(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "slack:reply"},
		},
	}
	pctx := relay.PolicyContext{
		ConnectorSource: "github",
		WorkerTrustTier: relay.TrustTierStandard,
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected slack output surface denied for github-sourced run")
	}
}

func TestCapabilityPolicyGitHubSurfaceMismatchForSlackSource(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "github:pr"},
		},
	}
	pctx := relay.PolicyContext{
		ConnectorSource: "slack",
		WorkerTrustTier: relay.TrustTierStandard,
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected github output surface denied for slack-sourced standard worker")
	}
}

func TestCapabilityPolicyCrossTenantMemoryDenied(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Memories: []relay.MemoryCapability{
			{Name: "other-tenant-memory", Scope: "tenant:t2:repo"},
		},
	}
	pctx := relay.PolicyContext{
		TenantID: "t1",
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected cross-tenant memory to be denied")
	}
}

func TestCapabilityPolicyPrivilegedCrossSurface(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "slack:reply"},
		},
	}
	pctx := relay.PolicyContext{
		ConnectorSource: "github",
		WorkerTrustTier: relay.TrustTierPrivileged,
	}

	result := p.Check(context.Background(), pack, pctx)
	if !result.Allowed {
		t.Errorf("privileged worker should allow cross-source output: %v", result.Denied)
	}
}

func TestCapabilityPolicyFilterPack(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash:destructive"},
			{Name: "read"},
			{Name: "write"},
		},
		Secrets: []relay.SecretCapability{
			{Name: "org-token", Ref: "refs/org", Scope: "org"},
			{Name: "repo-token", Ref: "refs/repo", Scope: "repo"},
		},
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "github:pr"},
			{Type: "slack:reply"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierUntrusted,
		ConnectorSource: "github",
		IsLocalWorker:   false,
	}

	filtered, result := p.FilterPack(context.Background(), pack, pctx)

	if result.Allowed {
		t.Fatal("expected policy to deny some capabilities")
	}

	// bash:destructive should be removed.
	if filtered.HasTool("bash:destructive") {
		t.Error("bash:destructive should be filtered out")
	}
	// read should survive.
	if !filtered.HasTool("read") {
		t.Error("read should survive filtering")
	}
	// org secret should be removed for non-local, non-privileged.
	if filtered.HasSecret("refs/org") {
		t.Error("org secret should be filtered out")
	}
	// repo secret should survive.
	if !filtered.HasSecret("refs/repo") {
		t.Error("repo secret should survive filtering")
	}
	// slack:reply should be removed (source mismatch).
	for _, o := range filtered.OutputSurfaces {
		if o.Type == "slack:reply" && pctx.WorkerTrustTier != relay.TrustTierPrivileged {
			t.Error("slack:reply should be filtered out for github source")
		}
	}
}

func TestCapabilityPolicyMCPServerSecret(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		MCPServers: []relay.MCPServerCapability{
			{Name: "context7", SecretRef: "refs/api-key"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierUntrusted,
		IsLocalWorker:   false,
	}

	result := p.Check(context.Background(), pack, pctx)
	if result.Allowed {
		t.Error("expected MCP server with secret to be denied for untrusted non-local worker")
	}
}

func TestCapabilityPolicyEmptyPack(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{RunID: "run-1"}
	pctx := relay.PolicyContext{}

	result := p.Check(context.Background(), pack, pctx)
	if !result.Allowed {
		t.Errorf("empty pack should be allowed, got denied: %v", result.Denied)
	}
}

func TestCapabilityPolicyAPIAllowsAnyOutput(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "slack:reply"},
			{Type: "github:pr"},
		},
	}
	pctx := relay.PolicyContext{
		ConnectorSource: "api",
		WorkerTrustTier: relay.TrustTierStandard,
	}

	result := p.Check(context.Background(), pack, pctx)
	if !result.Allowed {
		t.Errorf("API source should allow any output: %v", result.Denied)
	}
}

func TestCapabilityPolicyDeniedReasons(t *testing.T) {
	p := relay.NewCapabilityPolicy()
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash:destructive"},
		},
	}
	pctx := relay.PolicyContext{
		WorkerTrustTier: relay.TrustTierUntrusted,
	}

	result := p.Check(context.Background(), pack, pctx)
	decision := result.Decisions["tool:bash:destructive"]
	if decision.Allowed {
		t.Fatal("expected denial")
	}
	if decision.Reason == "" {
		t.Error("denied decision should have a reason")
	}
	if !result.Timestamp.IsZero() == false {
		// Timestamp should be non-zero.
	}
}
