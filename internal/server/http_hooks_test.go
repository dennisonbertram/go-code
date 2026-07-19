package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/server"
)

// TestHooksEndpoint_PopulatedSummary asserts the documented JSON shape:
// loaded hooks with name/event/kind/source/matcher, skipped files with
// reasons (including trust reasons from #755).
func TestHooksEndpoint_PopulatedSummary(t *testing.T) {
	t.Parallel()
	summary := hooks.NewSummary(
		[]hooks.HookDef{
			{Name: "deny-rm", Event: hooks.EventPreToolUse, Kind: hooks.KindCommand,
				Matcher: "bash", Source: hooks.SourceProject, FilePath: "/w/.harness/hooks/deny.json"},
			{Name: "audit", Event: hooks.EventPostToolUse, Kind: hooks.KindHTTP,
				Source: hooks.SourceUser, FilePath: "/home/.harness/hooks/audit.json"},
		},
		[]hooks.SkipRecord{
			{File: "/w/.harness/hooks/evil.json", Reason: hooks.SkipReasonUntrusted},
			{File: "/w/.harness/hooks/changed.json", Reason: hooks.SkipReasonModifiedSinceTrusted},
		},
	)
	h := server.NewWithOptions(server.ServerOptions{
		AuthDisabled: true,
		HooksSummary: summary,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/hooks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Hooks []struct {
			Name    string `json:"name"`
			Event   string `json:"event"`
			Kind    string `json:"kind"`
			Source  string `json:"source"`
			Matcher string `json:"matcher"`
			File    string `json:"file"`
		} `json:"hooks"`
		Skipped []struct {
			File   string `json:"file"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Hooks) != 2 {
		t.Fatalf("hooks: %+v", resp.Hooks)
	}
	first := resp.Hooks[0]
	if first.Name != "deny-rm" || first.Event != "pre_tool_use" || first.Kind != "command" ||
		first.Source != "project" || first.Matcher != "bash" {
		t.Errorf("first hook fields: %+v", first)
	}
	if len(resp.Skipped) != 2 {
		t.Fatalf("skipped: %+v", resp.Skipped)
	}
	reasons := map[string]bool{}
	for _, s := range resp.Skipped {
		reasons[s.Reason] = true
	}
	if !reasons["untrusted"] || !reasons["modified_since_trusted"] {
		t.Errorf("trust skip reasons must surface: %+v", resp.Skipped)
	}
}

// TestHooksEndpoint_EmptySummaryServesEmptyArrays: a server built without
// hooks wiring (hooks disabled) still serves the route — with empty arrays,
// not null, and no nil panic.
func TestHooksEndpoint_EmptySummaryServesEmptyArrays(t *testing.T) {
	t.Parallel()
	h := server.NewWithOptions(server.ServerOptions{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodGet, "/v1/hooks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Raw shape check: the fields must be [] not null.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"hooks", "skipped"} {
		v, ok := raw[field]
		if !ok {
			t.Fatalf("response missing %q: %s", field, rec.Body.String())
		}
		arr, ok := v.([]any)
		if !ok {
			t.Fatalf("%q is not an array (null?): %s", field, rec.Body.String())
		}
		if len(arr) != 0 {
			t.Fatalf("%q not empty: %s", field, rec.Body.String())
		}
	}
}

// TestHooksEndpoint_RejectsNonGet covers method handling.
func TestHooksEndpoint_RejectsNonGet(t *testing.T) {
	t.Parallel()
	h := server.NewWithOptions(server.ServerOptions{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodPost, "/v1/hooks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("POST /v1/hooks returned 200; want method rejection")
	}
}
