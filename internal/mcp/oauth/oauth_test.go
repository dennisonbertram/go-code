package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

func newTestFlow(store *mcp.TokenStore, openURL func(string) error) *Flow {
	return &Flow{Store: store, OpenURL: openURL}
}

// TestLogin_FullPKCERoundTrip_HeaderDiscovery is the slice acceptance test: a
// full authorization-code + PKCE login against in-process mock servers,
// discovered via the WWW-Authenticate resource_metadata header, completing
// without a real network or browser.
func TestLogin_FullPKCERoundTrip_HeaderDiscovery(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.enableRegistration = false
	})
	// The protected-resource document lives at a NON-standard location that
	// only the WWW-Authenticate header points to; well-known probing would
	// fail, so success proves header-driven discovery.
	rs := newMockResourceServer(t, as.url(), func(m *mockResourceServer) {
		m.withMetadataHeader = true
		m.protectedResourcePath = "/custom/pr-meta"
	})

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "demo",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
		Scopes:      []string{"mcp"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if !strings.HasPrefix(tok.AccessToken, "as-access-") {
		t.Errorf("AccessToken = %q, want mock-issued token", tok.AccessToken)
	}
	if tok.RefreshToken == "" {
		t.Error("expected a refresh token")
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", tok.TokenType)
	}
	if tok.Issuer != as.url() {
		t.Errorf("Issuer = %q, want %q", tok.Issuer, as.url())
	}
	if tok.Expiry.IsZero() {
		t.Error("expected an expiry from expires_in")
	} else if d := time.Until(tok.Expiry); d < 55*time.Minute || d > 61*time.Minute {
		t.Errorf("Expiry is %v away, want ~1h", d)
	}
	if tok.ClientID != "preregistered-client" {
		t.Errorf("ClientID = %q, want recorded for later refresh", tok.ClientID)
	}

	// The mock AS only issues a token when S256(verifier) == the recorded
	// challenge, so reaching this point proves the PKCE round trip. Also
	// verify the authorization request carried the RFC 8707 resource param.
	authReq := as.lastAuthorize()
	if authReq.Resource != rs.mcpURL() {
		t.Errorf("authorize resource param = %q, want %q", authReq.Resource, rs.mcpURL())
	}
	if authReq.CodeChallengeMethod != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", authReq.CodeChallengeMethod)
	}
	if authReq.Scope != "mcp" {
		t.Errorf("scope = %q, want %q", authReq.Scope, "mcp")
	}

	// The token must be persisted in the store.
	stored, err := store.Get("demo")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.AccessToken != tok.AccessToken {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, tok.AccessToken)
	}
}

// TestLogin_DiscoveryFallback_WellKnown covers the fallback path: the 401 has
// no resource_metadata parameter, so discovery must probe the well-known
// protected-resource document.
func TestLogin_DiscoveryFallback_WellKnown(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	rs := newMockResourceServer(t, as.url(), func(m *mockResourceServer) {
		m.withMetadataHeader = false // 401 header without resource_metadata
	})

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "fallback",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, "as-access-") {
		t.Errorf("AccessToken = %q, want mock-issued token", tok.AccessToken)
	}
}

// TestLogin_DynamicClientRegistration verifies that when no client ID is
// configured and the AS advertises a registration endpoint, the flow
// registers a client per RFC 7591 and uses the issued client ID.
func TestLogin_DynamicClientRegistration(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.enableRegistration = true
		m.nextClientID = "dyn-client-42"
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "dyn",
		ResourceURL: rs.mcpURL(),
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if as.registerCallCount() != 1 {
		t.Errorf("register endpoint hit %d times, want 1", as.registerCallCount())
	}
	if got := as.lastAuthorize().ClientID; got != "dyn-client-42" {
		t.Errorf("authorize client_id = %q, want dynamically registered %q", got, "dyn-client-42")
	}
	if tok.ClientID != "dyn-client-42" {
		t.Errorf("stored ClientID = %q, want %q", tok.ClientID, "dyn-client-42")
	}
}

// TestLogin_PreRegisteredClientID_SkipsRegistration verifies that a
// configured client ID is used directly and dynamic registration is not
// attempted even when the AS supports it.
func TestLogin_PreRegisteredClientID_SkipsRegistration(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.enableRegistration = true
		m.allowClientID = "my-preregistered-id"
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := flow.Login(ctx, LoginOptions{
		ServerName:  "pre",
		ResourceURL: rs.mcpURL(),
		ClientID:    "my-preregistered-id",
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if as.registerCallCount() != 0 {
		t.Errorf("register endpoint hit %d times, want 0 for pre-registered client", as.registerCallCount())
	}
}

// TestLogin_NoClientID_NoRegistrationEndpoint verifies the actionable error
// when the flow has no client ID and the AS cannot register one.
func TestLogin_NoClientID_NoRegistrationEndpoint(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.enableRegistration = false
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := flow.Login(ctx, LoginOptions{ServerName: "noc", ResourceURL: rs.mcpURL()})
	if err == nil {
		t.Fatal("expected error without client ID or registration endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "client") {
		t.Errorf("error %q should mention the missing client ID", err.Error())
	}
	if _, getErr := store.Get("noc"); !errors.Is(getErr, mcp.ErrTokenNotFound) {
		t.Errorf("no token must be stored on failure, Get error = %v", getErr)
	}
}

// TestLogin_StateMismatch verifies the flow rejects a callback whose state
// does not match the one it generated (CSRF protection).
func TestLogin_StateMismatch(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.redirectState = "tampered-state"
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := flow.Login(ctx, LoginOptions{
		ServerName:  "csrf",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err == nil {
		t.Fatal("expected state mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "state") {
		t.Errorf("error %q should mention state", err.Error())
	}
	if _, getErr := store.Get("csrf"); !errors.Is(getErr, mcp.ErrTokenNotFound) {
		t.Errorf("no token must be stored on state mismatch, Get error = %v", getErr)
	}
}

// TestLogin_AuthorizationError verifies that an error redirect from the AS
// (e.g. the user denied access) fails the login without storing a token.
func TestLogin_AuthorizationError(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.redirectError = "access_denied"
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := flow.Login(ctx, LoginOptions{
		ServerName:  "denied",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err == nil {
		t.Fatal("expected authorization error, got nil")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error %q should carry the AS error code", err.Error())
	}
}

// TestLogin_UserAborts verifies that cancelling the login context (the user
// aborts instead of completing the browser flow) unblocks the flow with the
// context error.
func TestLogin_UserAborts(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The "browser" never completes the flow; the user aborts.
	flow := newTestFlow(store, func(string) error {
		cancel()
		return nil
	})

	_, err := flow.Login(ctx, LoginOptions{
		ServerName:  "abort",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err == nil {
		t.Fatal("expected error on user abort, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want errors.Is context.Canceled", err)
	}
}

// TestLogin_TokenEndpointError verifies that an OAuth error response from the
// token endpoint during code exchange fails the login and surfaces the AS
// error code.
func TestLogin_TokenEndpointError(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.tokenError = "invalid_grant"
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := flow.Login(ctx, LoginOptions{
		ServerName:  "tokfail",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err == nil {
		t.Fatal("expected token exchange error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error %q should carry the AS error code", err.Error())
	}
	if _, getErr := store.Get("tokfail"); !errors.Is(getErr, mcp.ErrTokenNotFound) {
		t.Errorf("no token must be stored on exchange failure, Get error = %v", getErr)
	}
}

// TestRefresh_Success verifies a refresh round trip: the stored token is
// replaced by the refreshed one, including a rotated refresh token, and the
// issuer/client ID are preserved.
func TestRefresh_Success(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)

	store := mcp.NewTokenStore(t.TempDir())

	// Seed the store with a completed login so the refresh token is known to
	// the mock AS.
	rs := newMockResourceServer(t, as.url(), nil)
	flow := newTestFlow(store, browserViaHTTP(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	loginTok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "demo",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	refreshed, err := flow.Refresh(ctx, "demo")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.AccessToken == loginTok.AccessToken {
		t.Error("expected a new access token after refresh")
	}
	if refreshed.RefreshToken == loginTok.RefreshToken {
		t.Error("expected a rotated refresh token")
	}
	if refreshed.Issuer != loginTok.Issuer {
		t.Errorf("Issuer = %q, want preserved %q", refreshed.Issuer, loginTok.Issuer)
	}
	if refreshed.ClientID != loginTok.ClientID {
		t.Errorf("ClientID = %q, want preserved %q", refreshed.ClientID, loginTok.ClientID)
	}

	// The store must hold the refreshed token.
	stored, err := store.Get("demo")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.AccessToken != refreshed.AccessToken {
		t.Errorf("stored AccessToken = %q, want refreshed %q", stored.AccessToken, refreshed.AccessToken)
	}

	// The grant must have used the previous refresh token.
	grants := as.tokenGrantTypes()
	if len(grants) != 2 || grants[0] != "authorization_code" || grants[1] != "refresh_token" {
		t.Errorf("token grant sequence = %v, want [authorization_code refresh_token]", grants)
	}
}

// TestRefresh_UnrotatedRefreshTokenPreserved verifies that when the AS does
// not return a new refresh token, the previous one is kept.
func TestRefresh_UnrotatedRefreshTokenPreserved(t *testing.T) {
	as := newMockAuthorizationServer(t, func(m *mockAuthorizationServer) {
		m.noRotateRefresh = true
	})
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	loginTok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "demo",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	refreshed, err := flow.Refresh(ctx, "demo")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.RefreshToken != loginTok.RefreshToken {
		t.Errorf("RefreshToken = %q, want original %q preserved", refreshed.RefreshToken, loginTok.RefreshToken)
	}
	if refreshed.AccessToken == loginTok.AccessToken {
		t.Error("expected a new access token")
	}
}

// TestRefresh_InvalidGrant verifies that an invalid_grant response maps to a
// distinct re-authentication error and leaves the stored token untouched.
func TestRefresh_InvalidGrant(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	rs := newMockResourceServer(t, as.url(), nil)

	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	loginTok, err := flow.Login(ctx, LoginOptions{
		ServerName:  "demo",
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Overwrite the stored refresh token with one the AS does not know.
	bad := loginTok
	bad.RefreshToken = "as-refresh-999-unknown"
	if err := store.Put("demo", bad); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	_, err = flow.Refresh(ctx, "demo")
	if err == nil {
		t.Fatal("expected refresh error for unknown refresh token, got nil")
	}
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("error = %v, want errors.Is ErrReauthRequired", err)
	}

	// The stored token must remain (the CLI can point the user at login).
	stored, getErr := store.Get("demo")
	if getErr != nil {
		t.Fatalf("store.Get after failed refresh: %v", getErr)
	}
	if stored.AccessToken != bad.AccessToken {
		t.Errorf("stored token changed after failed refresh: %q, want %q", stored.AccessToken, bad.AccessToken)
	}
}

// TestRefresh_NoStoredToken verifies the error when no token exists.
func TestRefresh_NoStoredToken(t *testing.T) {
	store := mcp.NewTokenStore(t.TempDir())
	flow := newTestFlow(store, browserViaHTTP(t))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := flow.Refresh(ctx, "ghost")
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
	if !errors.Is(err, mcp.ErrTokenNotFound) {
		t.Errorf("error = %v, want errors.Is mcp.ErrTokenNotFound", err)
	}
}
