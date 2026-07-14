package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	githubadapter "go-agent-harness/internal/github"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/trigger"
)

// newTriggerServerWithTenants is like newTriggerServer but also wires
// WebhookTenantIDs, the per-source configured tenant used by S1/S2 to
// authoritatively scope webhook/trigger-initiated runs.
func newTriggerServerWithTenants(t *testing.T, provider harness.Provider, reg *trigger.ValidatorRegistry, webhookTenants map[string]string) (*httptest.Server, *store.MemoryStore) {
	t.Helper()
	ms := store.NewMemoryStore()
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
		Store:        ms,
	})
	handler := NewWithOptions(ServerOptions{
		Runner:           runner,
		Store:            ms,
		AuthDisabled:     true,
		Validators:       reg,
		WebhookTenantIDs: webhookTenants,
		GitHubAdapter:    githubadapter.NewGitHubAdapter("gh-webhook-secret"),
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, ms
}

// TestS1_ExternalTrigger_BodyTenantMismatchRejected is an ATTACK test: with a
// configured tenant for the "github" source, a caller who knows the shared
// trigger secret must NOT be able to inject a run into an arbitrary tenant by
// naming it in the request body's tenant_id field.
func TestS1_ExternalTrigger_BodyTenantMismatchRejected(t *testing.T) {
	t.Parallel()

	const secret = "test-github-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := makeGitHubRegistry(secret)
	ts, ms := newTriggerServerWithTenants(t, provider, reg, map[string]string{"github": "tenant-real-owner"})

	body, sig := buildTriggerRequest(t, "github", secret, "start", "inject into another tenant", "PR#1", map[string]string{
		"tenant_id": "tenant-attacker-chosen",
	})
	res := sendTrigger(t, ts, body, sig)
	defer res.Body.Close()

	if res.StatusCode != http.StatusForbidden {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 403 for body/configured tenant mismatch, got %d: %s", res.StatusCode, raw)
	}

	// No run should have been created under the attacker-chosen tenant.
	runs, err := ms.ListRuns(context.Background(), store.RunFilter{TenantID: "tenant-attacker-chosen"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs created under attacker-chosen tenant, got %d", len(runs))
	}
}

// TestS1_ExternalTrigger_NoConfiguredTenant_BodyTenantIgnored verifies the
// secure default: when no per-source tenant is configured (the common
// zero-config/local case), a caller-supplied tenant_id in the body is
// ignored entirely rather than trusted, and the run is always scoped to the
// "default" tenant. This closes the S1 hole even for deployments that never
// opt into WebhookTenantIDs.
func TestS1_ExternalTrigger_NoConfiguredTenant_BodyTenantIgnored(t *testing.T) {
	t.Parallel()

	const secret = "test-github-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := makeGitHubRegistry(secret)
	ts, ms := newTriggerServerWithTenants(t, provider, reg, nil)

	body, sig := buildTriggerRequest(t, "github", secret, "start", "try to name my own tenant", "PR#2", map[string]string{
		"tenant_id": "attacker-chosen-tenant",
	})
	res := sendTrigger(t, ts, body, sig)
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 202, got %d: %s", res.StatusCode, raw)
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	run, err := ms.GetRun(context.Background(), resp.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.TenantID != "default" {
		t.Fatalf("expected run tenant to be forced to 'default', got %q (body tenant_id must be ignored without a configured source tenant)", run.TenantID)
	}
}

// TestS1_ExternalTrigger_ConfiguredTenantMatchesBody_Succeeds verifies the
// legitimate opt-in path: when the body-supplied tenant_id matches the
// configured tenant for the source, the request proceeds normally.
func TestS1_ExternalTrigger_ConfiguredTenantMatchesBody_Succeeds(t *testing.T) {
	t.Parallel()

	const secret = "test-github-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := makeGitHubRegistry(secret)
	ts, ms := newTriggerServerWithTenants(t, provider, reg, map[string]string{"github": "tenant-real-owner"})

	body, sig := buildTriggerRequest(t, "github", secret, "start", "legit run", "PR#3", map[string]string{
		"tenant_id": "tenant-real-owner",
	})
	res := sendTrigger(t, ts, body, sig)
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 202, got %d: %s", res.StatusCode, raw)
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	run, err := ms.GetRun(context.Background(), resp.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.TenantID != "tenant-real-owner" {
		t.Fatalf("expected run tenant %q, got %q", "tenant-real-owner", run.TenantID)
	}
}

// TestS2_GitHubWebhook_UsesConfiguredTenant is an ATTACK/regression test for
// S2: GitHub/Slack/Linear webhook adapters never set TenantID on the
// envelope they produce (they have no body for the caller to set tenant_id
// on in the first place — it comes entirely from provider-specific JSON
// unrelated to tenancy), so every webhook-initiated run previously collapsed
// into the "default" tenant regardless of which integration was configured.
// With a configured source tenant, GitHub-webhook-initiated runs must be
// scoped to that tenant, consistent with S1's resolution mechanism.
func TestS2_GitHubWebhook_UsesConfiguredTenant(t *testing.T) {
	t.Parallel()

	const secret = "gh-webhook-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := trigger.NewValidatorRegistry()
	reg.Register("github", &trigger.GitHubValidator{Secret: secret})
	ts, ms := newTriggerServerWithTenants(t, provider, reg, map[string]string{"github": "acme-corp-tenant"})

	body := issuesOpenedBody(101)
	sig := computeGitHubSig(secret, body)
	res := sendGitHubWebhook(t, ts, "issues", "delivery-tenant-001", sig, body)
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 202, got %d: %s", res.StatusCode, raw)
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	run, err := ms.GetRun(context.Background(), resp.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.TenantID != "acme-corp-tenant" {
		t.Fatalf("expected GitHub-webhook run tenant %q, got %q", "acme-corp-tenant", run.TenantID)
	}
}
