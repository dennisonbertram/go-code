package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// mockAuthorizationServer is an in-process OAuth 2.1 authorization server for
// tests. It serves AS metadata, an authorization endpoint that 302-redirects
// with a code, a token endpoint that validates the PKCE verifier against the
// challenge recorded at authorization time, and optional RFC 7591 dynamic
// client registration.
type mockAuthorizationServer struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	authRequests  []authorizeRequest
	tokenRequests []url.Values
	registerHits  int
	codes         map[string]string // code -> code_challenge
	refreshTokens map[string]bool   // known refresh tokens
	codeCounter   int
	tokenCounter  int

	// Behavior knobs.
	enableRegistration bool   // advertise and serve /register
	nextClientID       string // client ID handed out by /register
	redirectError      string // if set, /authorize redirects with ?error=
	redirectState      string // if set, /authorize redirects with this state instead of the client's
	tokenError         string // if set, /token always fails with this error code
	noRotateRefresh    bool   // if set, refresh grants do not return a new refresh_token
	allowClientID      string // if set, /authorize and /token reject other client IDs
}

type authorizeRequest struct {
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	Resource            string
	CodeChallenge       string
	CodeChallengeMethod string
	ResponseType        string
}

func newMockAuthorizationServer(t *testing.T, configure func(*mockAuthorizationServer)) *mockAuthorizationServer {
	t.Helper()
	m := &mockAuthorizationServer{
		t:             t,
		codes:         make(map[string]string),
		refreshTokens: make(map[string]bool),
		nextClientID:  "dyn-client-1",
	}
	if configure != nil {
		configure(m)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", m.handleMetadata)
	mux.HandleFunc("/authorize", m.handleAuthorize)
	mux.HandleFunc("/token", m.handleToken)
	mux.HandleFunc("/register", m.handleRegister)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAuthorizationServer) url() string { return m.srv.URL }

func (m *mockAuthorizationServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
	meta := map[string]any{
		"issuer":                                m.srv.URL,
		"authorization_endpoint":                m.srv.URL + "/authorize",
		"token_endpoint":                        m.srv.URL + "/token",
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
	if m.enableRegistration {
		meta["registration_endpoint"] = m.srv.URL + "/register"
	}
	writeJSON(w, http.StatusOK, meta)
}

func (m *mockAuthorizationServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := authorizeRequest{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		State:               q.Get("state"),
		Scope:               q.Get("scope"),
		Resource:            q.Get("resource"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		ResponseType:        q.Get("response_type"),
	}

	m.mu.Lock()
	m.authRequests = append(m.authRequests, req)
	m.mu.Unlock()

	redirect, err := url.Parse(req.RedirectURI)
	if err != nil || redirect.Scheme == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}

	state := req.State
	if m.redirectState != "" {
		state = m.redirectState
	}
	out := redirect.Query()

	if m.redirectError != "" {
		out.Set("error", m.redirectError)
		out.Set("error_description", "mock AS: "+m.redirectError)
		out.Set("state", state)
		redirect.RawQuery = out.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	}

	if req.ResponseType != "code" || req.ClientID == "" || req.CodeChallenge == "" {
		out.Set("error", "invalid_request")
		out.Set("state", state)
		redirect.RawQuery = out.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	}
	if m.allowClientID != "" && req.ClientID != m.allowClientID {
		out.Set("error", "unauthorized_client")
		out.Set("state", state)
		redirect.RawQuery = out.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	}

	m.mu.Lock()
	m.codeCounter++
	code := fmt.Sprintf("code-%d", m.codeCounter)
	m.codes[code] = req.CodeChallenge
	m.mu.Unlock()

	out.Set("code", code)
	out.Set("state", state)
	redirect.RawQuery = out.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (m *mockAuthorizationServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	m.mu.Lock()
	clone := make(url.Values, len(r.Form))
	for k, vs := range r.Form {
		clone[k] = append([]string(nil), vs...)
	}
	m.tokenRequests = append(m.tokenRequests, clone)
	m.mu.Unlock()

	if m.tokenError != "" {
		writeOAuthError(w, http.StatusBadRequest, m.tokenError)
		return
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		m.handleCodeGrant(w, r)
	case "refresh_token":
		m.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func (m *mockAuthorizationServer) handleCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	clientID := r.Form.Get("client_id")

	m.mu.Lock()
	challenge, known := m.codes[code]
	m.mu.Unlock()

	if !known {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	if m.allowClientID != "" && clientID != m.allowClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	m.mu.Lock()
	m.tokenCounter++
	access := fmt.Sprintf("as-access-%d", m.tokenCounter)
	refresh := fmt.Sprintf("as-refresh-%d", m.tokenCounter)
	m.refreshTokens[refresh] = true
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (m *mockAuthorizationServer) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	refresh := r.Form.Get("refresh_token")

	m.mu.Lock()
	known := m.refreshTokens[refresh]
	m.mu.Unlock()
	if !known {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	m.mu.Lock()
	m.tokenCounter++
	access := fmt.Sprintf("as-access-%d", m.tokenCounter)
	resp := map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   3600,
	}
	if !m.noRotateRefresh {
		newRefresh := fmt.Sprintf("as-refresh-%d", m.tokenCounter)
		m.refreshTokens[newRefresh] = true
		delete(m.refreshTokens, refresh)
		resp["refresh_token"] = newRefresh
	}
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

func (m *mockAuthorizationServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.registerHits++
	m.mu.Unlock()
	if !m.enableRegistration {
		writeOAuthError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":   m.nextClientID,
		"client_name": "go-agent-harness",
	})
}

// --- accessors used by tests ---

func (m *mockAuthorizationServer) lastAuthorize() authorizeRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.authRequests) == 0 {
		return authorizeRequest{}
	}
	return m.authRequests[len(m.authRequests)-1]
}

func (m *mockAuthorizationServer) registerCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerHits
}

func (m *mockAuthorizationServer) tokenGrantTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.tokenRequests))
	for _, tr := range m.tokenRequests {
		out = append(out, tr.Get("grant_type"))
	}
	return out
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code, "error_description": "mock AS error: " + code})
}

// mockResourceServer is an in-process MCP resource server that requires
// OAuth. Unauthenticated requests get a 401; when withMetadataHeader is set,
// the 401 carries a WWW-Authenticate header with a resource_metadata pointer
// (RFC 9728). The protected-resource document is served at
// protectedResourcePath — tests move it off the well-known location to prove
// header-driven discovery, or drop the header parameter to force the
// well-known fallback.
type mockResourceServer struct {
	srv *httptest.Server

	asURL                 string
	withMetadataHeader    bool
	protectedResourcePath string
}

func newMockResourceServer(t *testing.T, asURL string, configure func(*mockResourceServer)) *mockResourceServer {
	t.Helper()
	m := &mockResourceServer{
		asURL:                 asURL,
		withMetadataHeader:    true,
		protectedResourcePath: "/.well-known/oauth-protected-resource",
	}
	if configure != nil {
		configure(m)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", m.handleMCP)
	mux.HandleFunc(m.protectedResourcePath, m.handleProtectedResource)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockResourceServer) mcpURL() string { return m.srv.URL + "/mcp" }

func (m *mockResourceServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if m.withMetadataHeader {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, m.srv.URL+m.protectedResourcePath))
	} else {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
	}
	w.WriteHeader(http.StatusUnauthorized)
}

func (m *mockResourceServer) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              m.mcpURL(),
		"authorization_servers": []string{m.asURL},
	})
}

// browserViaHTTP returns an OpenURL stub that performs the user-agent step
// in-process: it GETs the authorization URL and follows the redirect, which
// lands on the flow's loopback callback server.
func browserViaHTTP(t *testing.T) func(string) error {
	t.Helper()
	return func(rawurl string) error {
		if !strings.HasPrefix(rawurl, "http://") && !strings.HasPrefix(rawurl, "https://") {
			return fmt.Errorf("unexpected browser URL: %q", rawurl)
		}
		resp, err := http.Get(rawurl) //nolint: no real network — mock servers are loopback
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}
