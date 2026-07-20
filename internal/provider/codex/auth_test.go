package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRefreshPostsCodexOAuthRefreshGrant(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
			t.Fatalf("request = %s %s, want POST /oauth/token", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("client_id"); got != ClientID {
			t.Errorf("client_id = %q, want fixed Codex client id", got)
		}
		if got := r.Form.Get("refresh_token"); got != "test-refresh-credential" {
			t.Error("refresh credential was not sent to OAuth endpoint")
		}
		_, _ = w.Write([]byte(`{"access_token":"test-next-access","refresh_token":"test-next-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	refresh := NewRefreshFunc(server.Client(), server.URL+"/oauth/token", time.Now)
	token, nextRefresh, expiresAt, err := refresh(context.Background(), "test-refresh-credential")
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if token != "test-next-access" || nextRefresh != "test-next-refresh" {
		t.Fatal("refresh did not return replacement credentials")
	}
	if expiresAt.Before(time.Now().Add(59*time.Minute)) || expiresAt.After(time.Now().Add(61*time.Minute)) {
		t.Fatalf("expiry %s is not derived from expires_in", expiresAt)
	}
}

func TestRefreshSanitizesOAuthErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "credential test-refresh-credential rejected", http.StatusUnauthorized)
	}))
	defer server.Close()

	refresh := NewRefreshFunc(server.Client(), server.URL, time.Now)
	_, _, _, err := refresh(context.Background(), "test-refresh-credential")
	if err == nil {
		t.Fatal("Refresh() succeeded for OAuth failure")
	}
	if got := err.Error(); containsCredential(got) {
		t.Fatalf("refresh error exposes credential: %q", got)
	}
}

func TestRefreshRejectsIncompleteOAuthResponse(t *testing.T) {
	t.Parallel()

	refresh := NewRefreshFunc(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}, "http://example.invalid/oauth/token", time.Now)
	_, _, _, err := refresh(context.Background(), "test-refresh-credential")
	if err == nil || containsCredential(err.Error()) {
		t.Fatal("incomplete OAuth response must fail without exposing credentials")
	}
}

func containsCredential(value string) bool {
	return strings.Contains(value, "test-refresh-credential") || strings.Contains(value, "test-next-access")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
