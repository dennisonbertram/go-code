package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"go-agent-harness/internal/relay"
)

// TestRelayControl_PolicyFilter hits POST /v1/relay/policy/filter and verifies
// that the handler returns a filtered capability pack plus the policy result.
func TestRelayControl_PolicyFilter(t *testing.T) {
	h, _ := newRelayControlServer(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/relay/policy/filter", map[string]any{
		"pack": map[string]any{
			"run_id": "run-filter",
			"tools": []map[string]any{
				{"name": "read"},
				{"name": "bash:destructive"},
			},
		},
		"policy_context": map[string]any{
			"TenantID":        "t1",
			"WorkerTrustTier": "standard",
			"IsLocalWorker":   true,
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		FilteredPack *relay.CapabilityPack `json:"filtered_pack"`
		Result       *relay.PolicyResult   `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode policy filter response: %v", err)
	}

	if resp.Result == nil {
		t.Fatal("expected a policy result")
	}
	if !resp.Result.Allowed {
		t.Errorf("expected policy to be allowed for a standard local worker, got denied: %v", resp.Result.Denied)
	}

	if resp.FilteredPack == nil {
		t.Fatal("expected a filtered pack")
	}
	if resp.FilteredPack.RunID != "run-filter" {
		t.Errorf("filtered pack run_id = %q, want %q", resp.FilteredPack.RunID, "run-filter")
	}
	if len(resp.FilteredPack.Tools) != 2 {
		t.Errorf("expected 2 tools in filtered pack, got %d: %+v", len(resp.FilteredPack.Tools), resp.FilteredPack.Tools)
	}
}
