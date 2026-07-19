package server_test

// http_viz_test.go — tests for the embedded session visualizer shell served
// under /viz (epic #812, slice 1).
//
// Behavior under test:
//   - /viz and /viz/ require Bearer auth, like every other route except /healthz.
//   - /viz/ requires the runs:read scope (runs:write satisfies it via the
//     documented superscope rule in auth.go).
//   - With proper auth the embedded shell (index.html, app.js, style.css) is
//     served with correct content types.
//   - Path traversal attempts do not escape the embedded filesystem.
//   - /healthz remains unauthenticated (regression).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// vizTestServer builds an auth-enabled test server with three keys:
// a runs:read key, a runs:write-only key, and a key with no scopes.
func vizTestServer(t *testing.T) (h http.Handler, tokens map[string]string) {
	t.Helper()

	ms := store.NewMemoryStore()
	tokens = make(map[string]string)

	for _, tc := range []struct {
		name   string
		scopes []string
	}{
		{"read", []string{store.ScopeRunsRead}},
		{"write_only", []string{store.ScopeRunsWrite}},
		{"no_scopes", []string{}},
	} {
		raw, key := generateFastAPIKey(t, "tenant-viz-test", tc.name, tc.scopes)
		if err := ms.CreateAPIKey(context.Background(), key); err != nil {
			t.Fatalf("CreateAPIKey(%s): %v", tc.name, err)
		}
		tokens[tc.name] = raw
	}

	h = server.NewWithOptions(server.ServerOptions{Store: ms})
	return h, tokens
}

func vizGet(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestVizRequiresAuth(t *testing.T) {
	t.Parallel()
	h, _ := vizTestServer(t)

	for _, path := range []string{"/viz", "/viz/", "/viz/app.js", "/viz/style.css"} {
		rec := vizGet(t, h, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without token: status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestVizRequiresRunsReadScope(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	// A key with no scopes must be rejected with the structured 403 body.
	rec := vizGet(t, h, "/viz/", tokens["no_scopes"])
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /viz/ with scope-less key: status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var errResp struct {
		Error    string `json:"error"`
		Required string `json:"required"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("403 body is not valid JSON: %v; body=%q", err, rec.Body.String())
	}
	if errResp.Error != "insufficient_scope" {
		t.Errorf("403 error = %q, want %q", errResp.Error, "insufficient_scope")
	}
	if errResp.Required != store.ScopeRunsRead {
		t.Errorf("403 required = %q, want %q", errResp.Required, store.ScopeRunsRead)
	}

	// runs:write satisfies runs:read per the documented superscope rule
	// (internal/server/auth.go hasScope), consistent with every other read route.
	rec = vizGet(t, h, "/viz/", tokens["write_only"])
	if rec.Code != http.StatusOK {
		t.Errorf("GET /viz/ with runs:write-only key: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestVizServesShellWithReadScope(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	rec := vizGet(t, h, "/viz/", tokens["read"])
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /viz/: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET /viz/: Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"<html", "app.js", "style.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /viz/: body missing %q", want)
		}
	}
}

func TestVizServesStaticAssets(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	cases := []struct {
		path        string
		contentType string
	}{
		{"/viz/app.js", "javascript"},
		{"/viz/style.css", "text/css"},
	}
	for _, tc := range cases {
		rec := vizGet(t, h, tc.path, tokens["read"])
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", tc.path, rec.Code, http.StatusOK)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.contentType) {
			t.Errorf("GET %s: Content-Type = %q, want it to contain %q", tc.path, ct, tc.contentType)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s: empty body", tc.path)
		}
	}
}

func TestVizRootRedirectsToSlash(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	rec := vizGet(t, h, "/viz", tokens["read"])
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /viz: status = %d, want a redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/viz/" {
		t.Errorf("GET /viz: Location = %q, want %q", loc, "/viz/")
	}
}

func TestVizPathTraversalRejected(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	for _, path := range []string{"/viz/../v1/runs", "/viz/%2e%2e/v1/runs", "/viz/../../etc/passwd"} {
		rec := vizGet(t, h, path, tokens["read"])
		if rec.Code == http.StatusOK {
			body, _ := io.ReadAll(rec.Body)
			if strings.Contains(string(body), "<html") {
				t.Errorf("GET %s: traversal served embedded shell content", path)
			}
		}
	}
}

func TestVizMissingAssetReturns404(t *testing.T) {
	t.Parallel()
	h, tokens := vizTestServer(t)

	rec := vizGet(t, h, "/viz/does-not-exist.js", tokens["read"])
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /viz/does-not-exist.js: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestVizDoesNotAffectHealthz(t *testing.T) {
	t.Parallel()
	h, _ := vizTestServer(t)

	rec := vizGet(t, h, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz without token: status = %d, want %d (must stay unauthenticated)", rec.Code, http.StatusOK)
	}
}
