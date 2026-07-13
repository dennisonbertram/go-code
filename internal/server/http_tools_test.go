package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/goals"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

func TestToolsEndpoint_EnumeratesCatalog(t *testing.T) {
	// A default registry with a goals manager wired: exercises both an
	// always-registered tool (deploy) and a conditionally-registered one (goals).
	reg := harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{
		GoalManager: goals.NewManager(nil),
	})
	h := server.NewWithOptions(server.ServerOptions{
		AuthDisabled: true,
		Tools:        reg,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Count int `json:"count"`
		Tools []struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tier        string   `json:"tier"`
			Tags        []string `json:"tags"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count == 0 || resp.Count != len(resp.Tools) {
		t.Fatalf("count %d does not match tools len %d", resp.Count, len(resp.Tools))
	}

	byName := make(map[string]bool, len(resp.Tools))
	for _, tool := range resp.Tools {
		byName[tool.Name] = true
		if tool.Name == "" || tool.Tier == "" {
			t.Errorf("tool has empty name or tier: %+v", tool)
		}
	}
	for _, want := range []string{"deploy", "goals", "todos", "read", "bash"} {
		if !byName[want] {
			t.Errorf("expected tool %q in catalog; got %v", want, keysOf(byName))
		}
	}
}

func TestToolsEndpoint_NotConfigured501(t *testing.T) {
	h := server.NewWithOptions(server.ServerOptions{AuthDisabled: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
