package server

import (
	"io"
	"net/http"
	"testing"

	"go-agent-harness/internal/harness"
)

// TestS5_GitHubWebhook_ReplayedDeliveryRejected is an ATTACK test: a captured,
// validly-signed GitHub webhook delivery replayed with its ORIGINAL
// X-GitHub-Delivery ID must be rejected the second time, even though the
// signature is (correctly) valid both times.
func TestS5_GitHubWebhook_ReplayedDeliveryRejected(t *testing.T) {
	t.Parallel()

	const secret = "test-webhook-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	ts, _ := newGitHubWebhookServer(t, provider, secret)

	body := issuesOpenedBody(1)
	sig := computeGitHubSig(secret, body)

	res1 := sendGitHubWebhook(t, ts, "issues", "delivery-replay-001", sig, body)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res1.Body)
		t.Fatalf("first delivery: expected 202, got %d: %s", res1.StatusCode, raw)
	}

	// Replay the exact same request (same delivery ID, same valid signature).
	res2 := sendGitHubWebhook(t, ts, "issues", "delivery-replay-001", sig, body)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(res2.Body)
		t.Fatalf("replayed delivery: expected 409, got %d: %s", res2.StatusCode, raw)
	}
}

// TestS5_GitHubWebhook_DistinctDeliveriesAccepted is a regression/sanity test
// ensuring dedup is scoped to the delivery ID, not something coarser (e.g.
// source alone) that would wrongly reject unrelated, legitimate deliveries.
func TestS5_GitHubWebhook_DistinctDeliveriesAccepted(t *testing.T) {
	t.Parallel()

	const secret = "test-webhook-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	ts, _ := newGitHubWebhookServer(t, provider, secret)

	body1 := issuesOpenedBody(1)
	sig1 := computeGitHubSig(secret, body1)
	res1 := sendGitHubWebhook(t, ts, "issues", "delivery-distinct-001", sig1, body1)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res1.Body)
		t.Fatalf("first delivery: expected 202, got %d: %s", res1.StatusCode, raw)
	}

	body2 := issuesOpenedBody(2)
	sig2 := computeGitHubSig(secret, body2)
	res2 := sendGitHubWebhook(t, ts, "issues", "delivery-distinct-002", sig2, body2)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res2.Body)
		t.Fatalf("second (distinct) delivery: expected 202, got %d: %s", res2.StatusCode, raw)
	}
}

// TestS5_ExternalTrigger_ReplayedSourceIDRejected is an ATTACK test for the
// generic POST /v1/external/trigger route: a replayed source_id under a
// validly-signed envelope must be rejected the second time.
func TestS5_ExternalTrigger_ReplayedSourceIDRejected(t *testing.T) {
	t.Parallel()

	const secret = "test-github-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := makeGitHubRegistry(secret)
	ts, _ := newTriggerServer(t, provider, reg)

	body, sig := buildTriggerRequest(t, "github", secret, "start", "build the feature", "PR#replay", map[string]string{
		"source_id": "delivery-generic-001",
	})

	res1 := sendTrigger(t, ts, body, sig)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res1.Body)
		t.Fatalf("first request: expected 202, got %d: %s", res1.StatusCode, raw)
	}

	res2 := sendTrigger(t, ts, body, sig)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(res2.Body)
		t.Fatalf("replayed request: expected 409, got %d: %s", res2.StatusCode, raw)
	}
}

// TestS5_ExternalTrigger_NoSourceIDNeverDeduped is a regression/sanity test:
// when the caller supplies no source_id at all (nothing to dedup on), repeat
// requests must NOT be rejected as replays — dedup must not regress the
// (pre-existing, still-valid) no-source_id usage pattern exercised throughout
// http_external_trigger_test.go.
func TestS5_ExternalTrigger_NoSourceIDNeverDeduped(t *testing.T) {
	t.Parallel()

	const secret = "test-github-secret"
	provider := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	reg := makeGitHubRegistry(secret)
	ts, _ := newTriggerServer(t, provider, reg)

	body, sig := buildTriggerRequest(t, "github", secret, "start", "build the feature", "PR#no-source-id", nil)

	res1 := sendTrigger(t, ts, body, sig)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res1.Body)
		t.Fatalf("first request: expected 202, got %d: %s", res1.StatusCode, raw)
	}

	body2, sig2 := buildTriggerRequest(t, "github", secret, "start", "build another feature", "PR#no-source-id-2", nil)
	res2 := sendTrigger(t, ts, body2, sig2)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res2.Body)
		t.Fatalf("second request with no source_id: expected 202, got %d: %s", res2.StatusCode, raw)
	}
}
