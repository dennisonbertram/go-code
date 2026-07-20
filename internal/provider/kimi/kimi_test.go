package kimi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefreshUsesOAuthFormAndDoesNotExposeCredential(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/oauth/token" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "fake-refresh" || form.Get("client_id") != ClientID {
			t.Fatal("unexpected OAuth refresh form")
		}
		_, _ = w.Write([]byte(`{"access_token":"fake-next","refresh_token":"fake-next-refresh","expires_in":900}`))
	}))
	defer server.Close()

	token, next, expires, err := RefreshFunc(server.URL+"/api/oauth/token", server.Client())(context.Background(), "fake-refresh")
	if err != nil {
		t.Fatalf("RefreshFunc: %v", err)
	}
	until := time.Until(expires)
	if token != "fake-next" || next != "fake-next-refresh" || until < 14*time.Minute || until > 16*time.Minute {
		t.Fatal("unexpected refresh result")
	}

	_, _, _, err = RefreshFunc(server.URL, server.Client())(context.Background(), "fake-secret")
	if err == nil || strings.Contains(err.Error(), "fake-secret") {
		t.Fatalf("credential leaked in error: %v", err)
	}
}

func TestSafetyMarginIsRealisticForKimiShortTTL(t *testing.T) {
	if SafetyMargin != 30*time.Second {
		t.Fatalf("SafetyMargin = %s, want 30s", SafetyMargin)
	}
}

func TestImportCopiesVendorCredentialToSeparateRestrictiveStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	vendor := dir + "/vendor.json"
	store := dir + "/harness/kimi.json"
	if err := os.WriteFile(vendor, []byte(`{"access_token":"fake-access","refresh_token":"fake-refresh","expires_at":2000000000,"expires_in":900}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Import(vendor, store); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got, err := Load(store); err != nil || got.AccessToken != "fake-access" || got.RefreshToken != "fake-refresh" {
		t.Fatal("import did not preserve credential pair")
	}
	if info, err := os.Stat(store); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("store mode = %v, want 0600", info.Mode())
	}
	if data, err := os.ReadFile(vendor); err != nil || !strings.Contains(string(data), "fake-access") {
		t.Fatal("vendor credential changed")
	}
}
