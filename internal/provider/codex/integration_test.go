package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	openai "go-agent-harness/internal/provider/openai"
)

func TestSubscriptionCompletionRefreshesExpiredCredentialAgainstFakeHTTPS(t *testing.T) {
	t.Parallel()

	var refreshCalls, completionCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalls++
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != ClientID {
				t.Fatalf("invalid refresh request: %v", err)
			}
			_, _ = w.Write([]byte(`{"access_token":"test-refreshed-access","refresh_token":"test-refreshed-refresh","expires_in":3600}`))
		case "/backend-api/codex/responses":
			completionCalls++
			if completionCalls == 1 && r.Header.Get("Authorization") != "Bearer test-initial-access" {
				t.Fatal("first completion did not use initial access credential")
			}
			if completionCalls == 2 && r.Header.Get("Authorization") != "Bearer test-refreshed-access" {
				t.Fatal("second completion did not use refreshed access credential")
			}
			if r.Header.Get("chatgpt-account-id") != "acct-integration" {
				t.Fatal("completion is missing ChatGPT account header")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := NewStore(filepath.Join(t.TempDir(), "codex.json"))
	if err := store.Save(Credential{AccessToken: "test-initial-access", RefreshToken: "test-old-refresh", AccountID: "acct-integration", ExpiresAt: time.Now().Add(50 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	source, err := NewTokenSourceWithSafetyMargin(store, NewRefreshFunc(server.Client(), server.URL+"/oauth/token", time.Now), 0)
	if err != nil {
		t.Fatal(err)
	}
	client, err := openai.NewClient(openai.Config{
		BaseURL:      server.URL + "/backend-api/codex",
		SkipV1Path:   true,
		TokenSource:  source,
		ExtraHeaders: map[string]string{"chatgpt-account-id": source.AccountID()},
		ProviderName: "codex-subscription",
		ModelAPILookup: func(string, string) string {
			return "responses"
		},
		Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := harness.CompletionRequest{Model: "gpt-test", Messages: []harness.Message{{Role: "user", Content: "hello"}}}
	result, err := client.Complete(context.Background(), request)
	if err != nil || result.Content != "ok" {
		t.Fatalf("Complete() = %#v, %v", result, err)
	}
	time.Sleep(75 * time.Millisecond)
	result, err = client.Complete(context.Background(), request)
	if err != nil || result.Content != "ok" {
		t.Fatalf("second Complete() = %#v, %v", result, err)
	}
	if refreshCalls != 1 || completionCalls != 2 {
		t.Fatalf("refresh/completion calls = %d/%d, want 1/2", refreshCalls, completionCalls)
	}
	persisted, err := store.Load()
	if err != nil || persisted.RefreshToken != "test-refreshed-refresh" {
		t.Fatalf("refreshed credential was not persisted: %v", err)
	}
}
