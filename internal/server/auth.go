package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go-agent-harness/internal/store"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const (
	// contextKeyTenantID is the context key for the authenticated tenant ID.
	contextKeyTenantID contextKey = iota
	// contextKeyAPIKeyPrefix is the context key for the first 8 characters of
	// the authenticated API key. Used by the audit trail for provenance.
	contextKeyAPIKeyPrefix
	// contextKeyKeyScopes is the context key for the validated API key scopes.
	// Injected by authMiddleware; used by requireScope middleware.
	contextKeyKeyScopes
)

// TenantIDFromContext returns the tenant ID injected by authMiddleware.
// Returns "" if no tenant ID is in the context.
func TenantIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyTenantID).(string)
	return v
}

// APIKeyPrefixFromContext returns the first 8 characters of the authenticated
// API key injected by authMiddleware. Returns "" if not present.
func APIKeyPrefixFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyAPIKeyPrefix).(string)
	return v
}

// KeyScopesFromContext returns the validated API key scopes injected by
// authMiddleware. Returns nil if no scopes are present (e.g. auth is disabled).
func KeyScopesFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(contextKeyKeyScopes).([]string)
	return v
}

// apiKeyPrefix returns the first 8 characters of a key for identification.
// Never stores or logs more than this prefix.
func apiKeyPrefix(key string) string {
	const prefixLen = 8
	if len(key) <= prefixLen {
		return key
	}
	return key[:prefixLen]
}

// authMiddleware enforces Bearer token authentication for all requests.
//
// Token extraction: Authorization: Bearer <token> header ONLY.
//
// A ?token= query-parameter fallback previously existed for SSE EventSource
// clients that cannot set custom headers in all browsers. It was removed
// (security hardening) because secrets in query strings leak into access
// logs, intermediate proxies, and browser history. No consumer in this
// repository requires it: the first-party CLI (cmd/harnesscli/tui/api.go)
// already authenticates via the Authorization header, and this codebase has
// no browser-based EventSource client that would need a header-free
// fallback (verified: no EventSource usage exists in this repo).
//
// Auth can be disabled at startup via:
//   - ServerOptions.AuthDisabled = true
//   - HARNESS_AUTH_DISABLED=true environment variable
//
// When disabled, all requests are allowed through and tenant ID is set to "".
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth when explicitly disabled.
		if s.authDisabled {
			next.ServeHTTP(w, r)
			return
		}
		// If no auth store is configured, auth is implicitly disabled.
		if s.runStore == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Extract the raw token.
		rawToken := extractToken(r)
		if rawToken == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authorization required")
			return
		}

		// Validate against the key store.
		key, err := s.runStore.ValidateAPIKey(r.Context(), rawToken)
		if err != nil {
			if err == store.ErrKeyExpired {
				writeError(w, http.StatusUnauthorized, "unauthorized", "api key expired")
				return
			}
			if store.IsKeyNotFound(err) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
				return
			}
			// Unexpected store error — log-safe message without leaking details.
			writeError(w, http.StatusInternalServerError, "internal_error", "auth check failed")
			return
		}

		// Inject tenant_id, API key prefix, and scopes into context.
		ctx := context.WithValue(r.Context(), contextKeyTenantID, key.TenantID)
		ctx = context.WithValue(ctx, contextKeyAPIKeyPrefix, apiKeyPrefix(rawToken))
		scopesCopy := make([]string, len(key.Scopes))
		copy(scopesCopy, key.Scopes)
		ctx = context.WithValue(ctx, contextKeyKeyScopes, scopesCopy)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// hasScope reports whether the request context carries the required scope.
//
// Scope hierarchy (superscope rules):
//   - store.ScopeAdmin   satisfies any scope check (runs:read, runs:write, admin)
//   - store.ScopeRunsWrite satisfies store.ScopeRunsRead
//
// When no scopes are stored in the context (auth disabled or no store configured),
// hasScope always returns true so that unauthenticated mode is unaffected.
func hasScope(ctx context.Context, required string) bool {
	scopes := KeyScopesFromContext(ctx)
	// No scopes in context means auth was skipped — allow everything.
	if scopes == nil {
		return true
	}
	for _, s := range scopes {
		if s == store.ScopeAdmin {
			// Admin is a superscope: satisfies any permission check.
			return true
		}
		if s == required {
			return true
		}
		// runs:write satisfies runs:read.
		if required == store.ScopeRunsRead && s == store.ScopeRunsWrite {
			return true
		}
	}
	return false
}

// requireScope returns middleware that enforces the required scope on every
// request. When the request context carries sufficient privileges, the wrapped
// handler is called. Otherwise the middleware writes a structured 403 response:
//
//	{"error": "insufficient_scope", "required": "<scope>"}
//
// When auth is disabled (no scopes in context), scope checks are skipped so
// that development / testing workflows are unaffected.
func (s *Server) requireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Auth disabled or no store — allow through without scope check.
			if s.authDisabled || s.runStore == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !hasScope(r.Context(), scope) {
				writeScopeError(w, scope)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeScopeError writes a structured 403 response for insufficient scope.
func writeScopeError(w http.ResponseWriter, required string) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":    "insufficient_scope",
		"required": required,
	})
}

// extractToken pulls the Bearer token from the Authorization header.
//
// A ?token= query-parameter fallback was intentionally removed: secrets in
// URLs leak into access logs, proxy logs, and browser history. See the
// authMiddleware doc comment for the rationale and what was verified before
// removing it.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// effectiveTenantID resolves the tenant ID that should be used for a request.
//
// When auth is enabled (store configured and not explicitly disabled):
//   - The effective tenant always comes from the authenticated API key in context.
//   - If requestTenantID is empty, the auth tenant is used silently.
//   - If requestTenantID matches the auth tenant, the auth tenant is returned.
//   - If requestTenantID differs from the auth tenant, an error is returned.
//     Callers should reject the request with 400 Bad Request.
//
// When auth is disabled (authDisabled=true or no store configured):
//   - requestTenantID is returned as-is (existing no-auth behavior preserved).
func (s *Server) effectiveTenantID(r *http.Request, requestTenantID string) (string, error) {
	// Auth is disabled or no store — pass through the request value unchanged.
	if s.authDisabled || s.runStore == nil {
		return requestTenantID, nil
	}

	authTenantID := TenantIDFromContext(r.Context())

	// Empty request value: silently fill from auth context.
	if requestTenantID == "" {
		return authTenantID, nil
	}

	// Matching value: allowed.
	if requestTenantID == authTenantID {
		return authTenantID, nil
	}

	// Mismatch: reject without leaking which tenant the auth key belongs to.
	return "", fmt.Errorf("tenant_id in request does not match authenticated tenant")
}

// authDisabledFromEnv returns true when HARNESS_AUTH_DISABLED=true is set.
func authDisabledFromEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HARNESS_AUTH_DISABLED")), "true")
}
