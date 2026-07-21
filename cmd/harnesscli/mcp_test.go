package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

// --- combined in-process OAuth authorization server + MCP resource server ---

// oauthMCPStack is one httptest server playing both roles: an OAuth 2.1
// authorization server (metadata, authorize, token) and an MCP resource
// server gated on the bearer tokens it issues.
type oauthMCPStack struct {
	srv *httptest.Server

	mu             sync.Mutex
	challenges     map[string]string
	accessTokens   map[string]bool
	refreshTokens  map[string]bool
	codeN          int
	tokenN         int
	lastAuthClient string
}

func newOAuthMCPStack(t *testing.T) *oauthMCPStack {
	t.Helper()
	s := &oauthMCPStack{
		challenges:    make(map[string]string),
		accessTokens:  make(map[string]bool),
		refreshTokens: make(map[string]bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleASMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.handleProtectedResource)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/mcp", s.handleMCP)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *oauthMCPStack) baseURL() string { return s.srv.URL }
func (s *oauthMCPStack) mcpURL() string  { return s.srv.URL + "/mcp" }

func (s *oauthMCPStack) handleASMetadata(w http.ResponseWriter, _ *http.Request) {
	stackWriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                           s.srv.URL,
		"authorization_endpoint":           s.srv.URL + "/authorize",
		"token_endpoint":                   s.srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
}

func (s *oauthMCPStack) handleProtectedResource(w http.ResponseWriter, _ *http.Request) {
	stackWriteJSON(w, http.StatusOK, map[string]any{
		"resource":              s.mcpURL(),
		"authorization_servers": []string{s.srv.URL},
	})
}

func (s *oauthMCPStack) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.lastAuthClient = q.Get("client_id")
	s.codeN++
	code := fmt.Sprintf("stack-code-%d", s.codeN)
	s.challenges[code] = q.Get("code_challenge")
	s.mu.Unlock()

	out := redirect.Query()
	out.Set("code", code)
	out.Set("state", q.Get("state"))
	redirect.RawQuery = out.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *oauthMCPStack) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		stackWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		challenge, ok := s.challenges[r.Form.Get("code")]
		if !ok {
			stackWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			stackWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
	case "refresh_token":
		if !s.refreshTokens[r.Form.Get("refresh_token")] {
			stackWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
	default:
		stackWriteJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
		return
	}

	s.tokenN++
	access := fmt.Sprintf("stack-access-%d", s.tokenN)
	refresh := fmt.Sprintf("stack-refresh-%d", s.tokenN)
	s.accessTokens[access] = true
	s.refreshTokens[refresh] = true
	stackWriteJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (s *oauthMCPStack) handleMCP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	s.mu.Lock()
	allowed := strings.HasPrefix(auth, "Bearer ") && s.accessTokens[strings.TrimPrefix(auth, "Bearer ")]
	s.mu.Unlock()
	if !allowed {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, s.srv.URL+"/.well-known/oauth-protected-resource"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch req.Method {
	case "initialize":
		resp["result"] = map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "stack", "version": "1.0"},
		}
	case "tools/list":
		resp["result"] = map[string]any{"tools": []map[string]any{
			{"name": "stack-tool", "description": "A gated tool", "inputSchema": map[string]any{"type": "object"}},
		}}
	default:
		resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}
	stackWriteJSON(w, http.StatusOK, resp)
}

func (s *oauthMCPStack) lastClientID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuthClient
}

func stackWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- test helpers ---

func captureCLI(t *testing.T) (out, errOut *bytes.Buffer) {
	t.Helper()
	origOut, origErr := stdout, stderr
	out = &bytes.Buffer{}
	errOut = &bytes.Buffer{}
	stdout, stderr = out, errOut
	t.Cleanup(func() { stdout, stderr = origOut, origErr })
	return out, errOut
}

// isolateMCPHome points HOME at a temp dir and clears HARNESS_MCP_SERVERS.
func isolateMCPHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(mcp.EnvVarMCPServers, "")
	return home
}

func writeUserConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".harness")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stackOpenURL(t *testing.T) func(string) error {
	t.Helper()
	return func(rawurl string) error {
		resp, err := http.Get(rawurl) // follows the redirect into the loopback listener
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}

func stubMCPOpenURL(t *testing.T, fn func(string) error) {
	t.Helper()
	orig := mcpOpenURL
	mcpOpenURL = fn
	t.Cleanup(func() { mcpOpenURL = orig })
}

// --- dispatch tests ---

func TestRunMCP_NoSubcommand(t *testing.T) {
	_, errOut := captureCLI(t)
	if code := runMCP(nil); code != 1 {
		t.Fatalf("runMCP(nil) = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "subcommand") {
		t.Errorf("stderr %q should print usage", errOut.String())
	}
}

func TestRunMCP_UnknownSubcommand(t *testing.T) {
	_, errOut := captureCLI(t)
	if code := runMCP([]string{"bogus"}); code != 1 {
		t.Fatalf("runMCP(bogus) = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unknown") {
		t.Errorf("stderr %q should report the unknown subcommand", errOut.String())
	}
}

func TestDispatch_MCPCase(t *testing.T) {
	captureCLI(t)
	if code := dispatch([]string{"mcp"}); code != 1 {
		t.Fatalf("dispatch(mcp) = %d, want 1 (usage)", code)
	}
	if code := dispatch([]string{"mcp", "bogus"}); code != 1 {
		t.Fatalf("dispatch(mcp bogus) = %d, want 1", code)
	}
}

// --- login/logout/status behavior tests ---

// TestMCPLogin_Logout_Status_EndToEnd is the slice acceptance test: against
// an in-process OAuth+MCP stack, `mcp login` stores a token, the transport
// then reaches the gated server with the bearer attached, `mcp status`
// reports the token state, and `mcp logout` removes it.
func TestMCPLogin_Logout_Status_EndToEnd(t *testing.T) {
	home := isolateMCPHome(t)
	stack := newOAuthMCPStack(t)
	writeUserConfig(t, home, fmt.Sprintf(`
[mcp_servers.demo]
transport = "http"
url = %q
`, stack.mcpURL()))
	stubMCPOpenURL(t, stackOpenURL(t))

	// login
	out, errOut := captureCLI(t)
	if code := runMCPLogin([]string{"--client-id", "e2e-client", "demo"}); code != 0 {
		t.Fatalf("runMCPLogin = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "demo") {
		t.Errorf("login output %q should name the server", out.String())
	}
	if got := stack.lastClientID(); got != "e2e-client" {
		t.Errorf("authorize client_id = %q, want the --client-id flag value", got)
	}

	// token stored under the isolated home with restrictive permissions
	tokenDir := filepath.Join(home, ".harness", "mcp")
	entries, err := os.ReadDir(tokenDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 token file in %s: entries=%v err=%v", tokenDir, entries, err)
	}
	fi, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("token file perms = %o, want 600", got)
	}

	// the transport uses the stored token automatically
	cm := mcp.NewClientManager()
	defer cm.Close()
	flow := newMCPOAuthFlow()
	cm.SetTokenProvider(flow.TokenProvider())
	if err := cm.AddServer(mcp.ServerConfig{Name: "demo", Transport: "http", URL: stack.mcpURL()}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	tools, err := cm.DiscoverTools(t.Context(), "demo")
	if err != nil {
		t.Fatalf("DiscoverTools with stored token: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "stack-tool" {
		t.Fatalf("unexpected tools: %v", tools)
	}

	// status reports the valid token
	out, _ = captureCLI(t)
	if code := runMCPStatus(nil); code != 0 {
		t.Fatalf("runMCPStatus = %d", code)
	}
	line := out.String()
	if !strings.Contains(line, "demo") || !strings.Contains(line, "valid") {
		t.Errorf("status output %q should show demo with a valid token", line)
	}

	// logout deletes the token
	out, errOut = captureCLI(t)
	if code := runMCPLogout([]string{"demo"}); code != 0 {
		t.Fatalf("runMCPLogout = %d, stderr: %s", code, errOut.String())
	}
	if _, err := mcp.NewTokenStore(tokenDir).Get("demo"); err == nil {
		t.Error("token still present after logout")
	}

	// status now reports no token
	out, _ = captureCLI(t)
	if code := runMCPStatus(nil); code != 0 {
		t.Fatalf("runMCPStatus = %d", code)
	}
	if !strings.Contains(out.String(), "no token") {
		t.Errorf("status output %q should show no token after logout", out.String())
	}
}

func TestRunMCPLogin_ServerNotConfigured(t *testing.T) {
	isolateMCPHome(t)
	_, errOut := captureCLI(t)
	if code := runMCPLogin([]string{"ghost"}); code != 1 {
		t.Fatalf("runMCPLogin = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "ghost") {
		t.Errorf("stderr %q should name the unknown server", errOut.String())
	}
}

func TestRunMCPLogin_StdioServerRejected(t *testing.T) {
	home := isolateMCPHome(t)
	writeUserConfig(t, home, `
[mcp_servers.local-tool]
transport = "stdio"
command = "/usr/local/bin/tool"
`)
	_, errOut := captureCLI(t)
	if code := runMCPLogin([]string{"local-tool"}); code != 1 {
		t.Fatalf("runMCPLogin = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "stdio") {
		t.Errorf("stderr %q should explain OAuth login does not apply to stdio", errOut.String())
	}
}

func TestRunMCPLogin_RequiresServerName(t *testing.T) {
	isolateMCPHome(t)
	captureCLI(t)
	if code := runMCPLogin(nil); code != 1 {
		t.Fatalf("runMCPLogin(nil) = %d, want 1", code)
	}
}

func TestRunMCPLogout_Idempotent(t *testing.T) {
	isolateMCPHome(t)
	captureCLI(t)
	if code := runMCPLogout([]string{"never-logged-in"}); code != 0 {
		t.Fatalf("runMCPLogout for unknown token = %d, want 0 (idempotent)", code)
	}
}

func TestRunMCPLogout_RequiresServerName(t *testing.T) {
	captureCLI(t)
	if code := runMCPLogout(nil); code != 1 {
		t.Fatalf("runMCPLogout(nil) = %d, want 1", code)
	}
}

// TestRunMCPStatus_States covers the per-server auth-state matrix.
func TestRunMCPStatus_States(t *testing.T) {
	home := isolateMCPHome(t)
	writeUserConfig(t, home, `
[mcp_servers.with-static]
transport = "http"
url = "https://static.example.com/mcp"

[mcp_servers.with-static.headers]
Authorization = "Bearer configured-token"

[mcp_servers.valid-tok]
transport = "http"
url = "https://valid.example.com/mcp"

[mcp_servers.expired-tok]
transport = "http"
url = "https://expired.example.com/mcp"

[mcp_servers.plain]
transport = "http"
url = "https://plain.example.com/mcp"

[mcp_servers.local-tool]
transport = "stdio"
command = "/usr/local/bin/tool"
`)

	store := mcp.NewTokenStore(filepath.Join(home, ".harness", "mcp"))
	if err := store.Put("valid-tok", mcp.Token{
		Issuer: "https://as.example.com", AccessToken: "a", RefreshToken: "r",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("expired-tok", mcp.Token{
		Issuer: "https://as.example.com", AccessToken: "a", RefreshToken: "r",
		Expiry: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	out, errOut := captureCLI(t)
	if code := runMCPStatus(nil); code != 0 {
		t.Fatalf("runMCPStatus = %d, stderr: %s", code, errOut.String())
	}
	got := out.String()

	assertLine := func(server, want string) {
		t.Helper()
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, server+" ") || strings.HasPrefix(line, server+"\t") {
				if !strings.Contains(line, want) {
					t.Errorf("status line for %q = %q, want it to contain %q\nfull output:\n%s", server, line, want, got)
				}
				return
			}
		}
		t.Errorf("no status line for %q\nfull output:\n%s", server, got)
	}
	assertLine("with-static", "static")
	assertLine("valid-tok", "valid")
	assertLine("expired-tok", "expired")
	assertLine("plain", "no token")
	assertLine("local-tool", "stdio")
}

// TestRunMCPStatus_EnvServer verifies servers from HARNESS_MCP_SERVERS appear
// in status output.
func TestRunMCPStatus_EnvServer(t *testing.T) {
	home := isolateMCPHome(t)
	t.Setenv(mcp.EnvVarMCPServers, `[{"name":"env-srv","url":"https://env.example.com/mcp","headers":{"Authorization":"Bearer env-token"}}]`)
	_ = home

	out, errOut := captureCLI(t)
	if code := runMCPStatus(nil); code != 0 {
		t.Fatalf("runMCPStatus = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "env-srv") {
		t.Errorf("status output %q should list the env-configured server", out.String())
	}
}

func TestRunMCPStatus_NoServers(t *testing.T) {
	isolateMCPHome(t)
	out, _ := captureCLI(t)
	if code := runMCPStatus(nil); code != 0 {
		t.Fatalf("runMCPStatus = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "No MCP servers") {
		t.Errorf("status output %q should say no servers are configured", out.String())
	}
}
