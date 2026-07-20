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

	"go-agent-harness/internal/harness"
	openai "go-agent-harness/internal/provider/openai"
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

func TestSubscriptionCompletionRefreshesInsideShortTTLWindow(t *testing.T) {
	var refreshes int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		_, _ = w.Write([]byte(`{"access_token":"fake-refreshed","refresh_token":"fake-rotated","expires_in":900}`))
	}))
	defer tokenServer.Close()
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fake-refreshed" {
			t.Fatal("completion did not use refreshed bearer credential")
		}
		for key, value := range ExtraHeaders() {
			if r.Header.Get(key) != value {
				t.Fatalf("missing %s", key)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer apiServer.Close()
	store := t.TempDir() + "/kimi.json"
	if err := save(store, Credentials{AccessToken: "fake-expiring", RefreshToken: "fake-refresh", ExpiresAt: time.Now().Add(20 * time.Second).Unix(), ExpiresIn: 900}); err != nil {
		t.Fatal(err)
	}
	source, err := NewTokenSource(store, tokenServer.URL, tokenServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	client, err := openai.NewClient(openai.Config{BaseURL: apiServer.URL, TokenSource: source, ExtraHeaders: ExtraHeaders(), ProviderName: "kimi-subscription"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), harness.CompletionRequest{Messages: []harness.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
	updated, err := Load(store)
	if err != nil || updated.AccessToken != "fake-refreshed" {
		t.Fatal("rotated pair was not persisted to harness store")
	}
}

func TestCredentialCodeHasNoLoggingCalls(t *testing.T) {
	data, err := os.ReadFile("kimi.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"log.", "slog.", "fmt.Print"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("credential code must not log: %s", forbidden)
		}
	}
}
