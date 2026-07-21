package oauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

// loginForProviderTest completes a full login against the mocks so the store
// holds a token known to the mock AS.
func loginForProviderTest(t *testing.T, as *mockAuthorizationServer, store *mcp.TokenStore, serverName string) mcp.Token {
	t.Helper()
	rs := newMockResourceServer(t, as.url(), nil)
	flow := &Flow{Store: store, OpenURL: browserViaHTTP(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := flow.Login(ctx, LoginOptions{
		ServerName:  serverName,
		ResourceURL: rs.mcpURL(),
		ClientID:    "preregistered-client",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return tok
}

// TestTokenProvider_ValidToken verifies the provider returns the stored
// access token without hitting the token endpoint again.
func TestTokenProvider_ValidToken(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	store := mcp.NewTokenStore(t.TempDir())
	tok := loginForProviderTest(t, as, store, "demo")

	flow := &Flow{Store: store}
	provider := flow.TokenProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	access, err := provider(ctx, "demo")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if access != tok.AccessToken {
		t.Errorf("provider returned %q, want stored %q", access, tok.AccessToken)
	}
	if grants := as.tokenGrantTypes(); len(grants) != 1 || grants[0] != "authorization_code" {
		t.Errorf("token endpoint grants = %v, want only the login exchange (no refresh)", grants)
	}
}

// TestTokenProvider_ExpiredToken_Refreshes verifies the silent-refresh path:
// an expired stored token is renewed and the fresh access token returned.
func TestTokenProvider_ExpiredToken_Refreshes(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	store := mcp.NewTokenStore(t.TempDir())
	tok := loginForProviderTest(t, as, store, "demo")

	// Force the stored token to be expired.
	expired := tok
	expired.Expiry = time.Now().Add(-time.Minute)
	if err := store.Put("demo", expired); err != nil {
		t.Fatalf("Put: %v", err)
	}

	flow := &Flow{Store: store}
	provider := flow.TokenProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	access, err := provider(ctx, "demo")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if access == tok.AccessToken {
		t.Error("expected a refreshed access token, got the expired one")
	}

	// The refreshed token must be persisted with a future expiry.
	stored, err := store.Get("demo")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.AccessToken != access {
		t.Errorf("stored AccessToken = %q, want refreshed %q", stored.AccessToken, access)
	}
}

// TestTokenProvider_NoToken verifies that a missing token means "no
// credentials" — the transport sends the request unauthenticated and the
// server's own 401 guides the user.
func TestTokenProvider_NoToken(t *testing.T) {
	store := mcp.NewTokenStore(t.TempDir())
	flow := &Flow{Store: store}
	provider := flow.TokenProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	access, err := provider(ctx, "ghost")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if access != "" {
		t.Errorf("provider returned %q, want empty for missing token", access)
	}
}

// TestTokenProvider_RefreshRejected verifies that an invalid_grant during
// refresh surfaces as ErrReauthRequired so the user is sent back to login.
func TestTokenProvider_RefreshRejected(t *testing.T) {
	as := newMockAuthorizationServer(t, nil)
	store := mcp.NewTokenStore(t.TempDir())
	tok := loginForProviderTest(t, as, store, "demo")

	// Expire the token and break its refresh token.
	broken := tok
	broken.Expiry = time.Now().Add(-time.Minute)
	broken.RefreshToken = "as-refresh-999-unknown"
	if err := store.Put("demo", broken); err != nil {
		t.Fatalf("Put: %v", err)
	}

	flow := &Flow{Store: store}
	provider := flow.TokenProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := provider(ctx, "demo")
	if err == nil {
		t.Fatal("expected an error for a rejected refresh, got nil")
	}
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("error = %v, want errors.Is ErrReauthRequired", err)
	}
}
