package server_test

// auth_scope_test.go — regression tests for API key scope enforcement (issue #319).
//
// Authorization matrix tested:
//   runs:read  → GET endpoints (list runs, get run, events, etc.)
//   runs:write → mutating endpoints (POST /v1/runs, POST /v1/runs/{id}/input, etc.)
//   admin      → provider key management (PUT /v1/providers/{name}/key)
//   admin      → superscope: satisfies runs:write and runs:read checks
//   runs:write → satisfies runs:read checks
//
// These tests FAIL before the scope enforcement is implemented.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// scopeTestServer builds a test server with authentication enabled and a
// provider registry wired so that PUT /v1/providers/{name}/key is exercisable.
func scopeTestServer(t *testing.T) (h http.Handler, tokens map[string]string) {
	t.Helper()

	ms := store.NewMemoryStore()
	tokens = make(map[string]string)

	for _, tc := range []struct {
		name   string
		scopes []string
	}{
		{"read_only", []string{store.ScopeRunsRead}},
		{"write", []string{store.ScopeRunsRead, store.ScopeRunsWrite}},
		{"admin", []string{store.ScopeAdmin}},
	} {
		raw, key := generateFastAPIKey(t, "tenant-scope-test", tc.name, tc.scopes)
		if err := ms.CreateAPIKey(context.Background(), key); err != nil {
			t.Fatalf("CreateAPIKey(%s): %v", tc.name, err)
		}
		tokens[tc.name] = raw
	}

	// Build a minimal catalog and provider registry for PUT /v1/providers tests.
	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "TEST_SCOPE_OPENAI_KEY",
				BaseURL:     "https://api.openai.com/v1",
				Models:      map[string]catalog.Model{},
			},
		},
	}
	reg := catalog.NewProviderRegistry(cat)

	// Wire a minimal runner so POST /v1/runs doesn't panic.
	runner := harness.NewRunner(
		&scopeStaticProvider{},
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "gpt-4.1-mini", MaxSteps: 1},
	)

	h = server.NewWithOptions(server.ServerOptions{
		Runner:           runner,
		Store:            ms,
		Catalog:          cat,
		ProviderRegistry: reg,
		// Stub reload callback so admin-scope tests on POST /v1/config/reload
		// can pass the scope gate and reach the handler.
		ConfigReload: func(_ context.Context) (config.ReloadReport, error) {
			return config.ReloadReport{}, nil
		},
	})
	return h, tokens
}

// scopeStaticProvider is a minimal provider that returns immediately.
// It is used only to prevent nil panics in scope tests.
type scopeStaticProvider struct{}

func (p *scopeStaticProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return harness.CompletionResult{Content: "scope-test-done"}, nil
}

// assertScopeResponse is a helper that makes a request and checks the HTTP status.
// When expectForbidden is true it also verifies the 403 body has the right shape.
func assertScopeResponse(t *testing.T, h http.Handler, method, path, token string, expectedStatus int) {
	t.Helper()

	var body *bytes.Reader
	if method == http.MethodPost || method == http.MethodPut {
		body = bytes.NewReader([]byte(`{"prompt":"test","key":"sk-test"}`))
	} else {
		body = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, body)
	if method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != expectedStatus {
		t.Errorf("%s %s with token scope: expected %d, got %d (body: %s)",
			method, path, expectedStatus, w.Code, w.Body.String())
		return
	}

	// When we expect 403, validate the structured error body.
	if expectedStatus == http.StatusForbidden {
		var errResp struct {
			Error    string `json:"error"`
			Required string `json:"required"`
		}
		if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
			t.Errorf("%s %s: 403 body is not valid JSON: %v; body=%q",
				method, path, err, w.Body.String())
			return
		}
		if errResp.Error != "insufficient_scope" {
			t.Errorf("%s %s: 403 error field = %q, want \"insufficient_scope\"",
				method, path, errResp.Error)
		}
		if errResp.Required == "" {
			t.Errorf("%s %s: 403 required field is empty", method, path)
		}
	}
}

// ============================================================
// POST /v1/runs — requires runs:write
// ============================================================

// TestScope_ReadOnly_CannotPostRuns verifies that a runs:read key returns 403
// when attempting POST /v1/runs.
func TestScope_ReadOnly_CannotPostRuns(t *testing.T) {
	h, tokens := scopeTestServer(t)
	assertScopeResponse(t, h, http.MethodPost, "/v1/runs", tokens["read_only"], http.StatusForbidden)
}

// TestScope_Write_CanPostRuns verifies that a runs:write key can POST /v1/runs.
func TestScope_Write_CanPostRuns(t *testing.T) {
	h, tokens := scopeTestServer(t)
	// 202 Accepted or 400 (bad request body) are both acceptable — what matters is NOT 403.
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader([]byte(`{"prompt":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["write"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:write key should be allowed POST /v1/runs, got 403: %s", w.Body.String())
	}
}

// TestScope_Admin_CanPostRuns verifies that an admin key (superscope) can POST /v1/runs.
func TestScope_Admin_CanPostRuns(t *testing.T) {
	h, tokens := scopeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader([]byte(`{"prompt":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["admin"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("admin key should be allowed POST /v1/runs (superscope), got 403: %s", w.Body.String())
	}
}

// ============================================================
// GET /v1/runs — requires runs:read
// ============================================================

// TestScope_ReadOnly_CanGetRuns verifies a runs:read key can GET /v1/runs.
func TestScope_ReadOnly_CanGetRuns(t *testing.T) {
	h, tokens := scopeTestServer(t)
	// 200 or 501 (no store) are both acceptable.
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["read_only"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:read key should be allowed GET /v1/runs, got 403: %s", w.Body.String())
	}
}

// TestScope_Write_CanGetRuns verifies that runs:write also satisfies runs:read for GET.
func TestScope_Write_CanGetRuns(t *testing.T) {
	h, tokens := scopeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["write"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:write key should satisfy runs:read for GET /v1/runs, got 403")
	}
}

// ============================================================
// GET /v1/runs/{id} — requires runs:read
// ============================================================

// TestScope_ReadOnly_CanGetRunByID verifies a runs:read key can GET /v1/runs/{id}.
func TestScope_ReadOnly_CanGetRunByID(t *testing.T) {
	h, tokens := scopeTestServer(t)
	// 404 is fine (no such run) — just not 403.
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent-run-id", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["read_only"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:read key should be allowed GET /v1/runs/{id}, got 403: %s", w.Body.String())
	}
}

// ============================================================
// PUT /v1/providers/{name}/key — requires admin
// ============================================================

// TestScope_Write_CannotPutProviderKey verifies that a runs:write key returns 403
// when attempting PUT /v1/providers/{name}/key (admin-only endpoint).
func TestScope_Write_CannotPutProviderKey(t *testing.T) {
	h, tokens := scopeTestServer(t)
	assertScopeResponse(t, h, http.MethodPut, "/v1/providers/openai/key", tokens["write"], http.StatusForbidden)
}

// TestScope_ReadOnly_CannotPutProviderKey verifies that a runs:read key returns 403.
func TestScope_ReadOnly_CannotPutProviderKey(t *testing.T) {
	h, tokens := scopeTestServer(t)
	assertScopeResponse(t, h, http.MethodPut, "/v1/providers/openai/key", tokens["read_only"], http.StatusForbidden)
}

// TestScope_Admin_CanPutProviderKey verifies that an admin key can PUT /v1/providers/{name}/key.
func TestScope_Admin_CanPutProviderKey(t *testing.T) {
	h, tokens := scopeTestServer(t)
	body := bytes.NewReader([]byte(`{"key":"sk-newkey-123"}`))
	req := httptest.NewRequest(http.MethodPut, "/v1/providers/openai/key", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["admin"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("admin key should be allowed PUT /v1/providers/{name}/key, got 403: %s", w.Body.String())
	}
}

// ============================================================
// POST /v1/config/reload — requires admin
// ============================================================

// TestScope_Write_CannotReloadConfig verifies that a runs:write key returns 403
// when attempting POST /v1/config/reload (admin-only endpoint).
func TestScope_Write_CannotReloadConfig(t *testing.T) {
	h, tokens := scopeTestServer(t)
	assertScopeResponse(t, h, http.MethodPost, "/v1/config/reload", tokens["write"], http.StatusForbidden)
}

// TestScope_ReadOnly_CannotReloadConfig verifies that a runs:read key returns 403.
func TestScope_ReadOnly_CannotReloadConfig(t *testing.T) {
	h, tokens := scopeTestServer(t)
	assertScopeResponse(t, h, http.MethodPost, "/v1/config/reload", tokens["read_only"], http.StatusForbidden)
}

// TestScope_Admin_CanReloadConfig verifies that an admin key can POST /v1/config/reload.
func TestScope_Admin_CanReloadConfig(t *testing.T) {
	h, tokens := scopeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/config/reload", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["admin"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("admin key should be allowed POST /v1/config/reload, got 403: %s", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Errorf("admin key POST /v1/config/reload: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// ============================================================
// Unauthenticated mode — scope checks must be skipped
// ============================================================

// TestScope_AuthDisabled_SkipsScopeCheck verifies that when auth is disabled,
// even "dangerous" endpoints (POST /v1/runs, PUT /v1/providers/{name}/key) are
// accessible without any token and without scope enforcement.
func TestScope_AuthDisabled_SkipsScopeCheck(t *testing.T) {
	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "TEST_SCOPE_OPENAI_KEY_DISABLED",
				BaseURL:     "https://api.openai.com/v1",
				Models:      map[string]catalog.Model{},
			},
		},
	}
	reg := catalog.NewProviderRegistry(cat)
	// Wire a runner so POST /v1/runs doesn't panic on nil receiver.
	runner := harness.NewRunner(
		&scopeStaticProvider{},
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "gpt-4.1-mini", MaxSteps: 1},
	)
	h := server.NewWithOptions(server.ServerOptions{
		Runner:           runner,
		Catalog:          cat,
		ProviderRegistry: reg,
		AuthDisabled:     true,
	})

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/runs"},
		{http.MethodPut, "/v1/providers/openai/key"},
	}

	for _, tc := range cases {
		t.Run(tc.method+"_"+tc.path, func(t *testing.T) {
			var body *bytes.Reader
			if tc.method == http.MethodPost {
				body = bytes.NewReader([]byte(`{"prompt":"test"}`))
			} else {
				body = bytes.NewReader([]byte(`{"key":"sk-test"}`))
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set("Content-Type", "application/json")
			// No Authorization header — auth is disabled.
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code == http.StatusForbidden {
				t.Errorf("auth disabled: %s %s should not be scope-checked, got 403", tc.method, tc.path)
			}
			if w.Code == http.StatusUnauthorized {
				t.Errorf("auth disabled: %s %s should not require auth, got 401", tc.method, tc.path)
			}
		})
	}
}

// ============================================================
// GET /v1/models — requires runs:read (sanity check)
// ============================================================

// TestScope_ReadOnly_CanGetModels verifies runs:read can access GET /v1/models.
func TestScope_ReadOnly_CanGetModels(t *testing.T) {
	h, tokens := scopeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["read_only"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:read key should be allowed GET /v1/models, got 403")
	}
}

// ============================================================
// POST /v1/runs/{id}/input — requires runs:write
// ============================================================

// TestScope_ReadOnly_CannotPostRunInput verifies a runs:read key cannot POST input.
func TestScope_ReadOnly_CannotPostRunInput(t *testing.T) {
	h, tokens := scopeTestServer(t)
	body := bytes.NewReader([]byte(`{"answers":{"q":"answer"}}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/some-run-id/input", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["read_only"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("runs:read key should NOT be allowed POST /v1/runs/{id}/input, got %d", w.Code)
	}
}

// TestScope_Write_CanPostRunInput verifies runs:write can POST /v1/runs/{id}/input.
func TestScope_Write_CanPostRunInput(t *testing.T) {
	h, tokens := scopeTestServer(t)
	body := bytes.NewReader([]byte(`{"answers":{"q":"answer"}}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/some-run-id/input", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["write"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// 404 (run not found) or 409 (no pending input) are acceptable — just not 403.
	if w.Code == http.StatusForbidden {
		t.Errorf("runs:write key should be allowed POST /v1/runs/{id}/input, got 403")
	}
}

// ============================================================
// POST /v1/runs/{id}/steer — requires runs:write
// ============================================================

// TestScope_ReadOnly_CannotSteer verifies a runs:read key cannot POST steer.
func TestScope_ReadOnly_CannotSteer(t *testing.T) {
	h, tokens := scopeTestServer(t)
	body := bytes.NewReader([]byte(`{"message":"redirect this"}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/some-run-id/steer", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens["read_only"])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("runs:read key should NOT be allowed POST /v1/runs/{id}/steer, got %d", w.Code)
	}
}
