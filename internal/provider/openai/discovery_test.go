package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/provider/catalog"
)

func TestNewModelDiscoveryListsOpenAIModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer authentication, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-4.1","object":"model","created":1740000000,"owned_by":"openai"}]}`)
	}))
	defer srv.Close()

	discovery := NewModelDiscovery(Config{APIKey: "test-key", BaseURL: srv.URL + "/v1", Client: srv.Client()})
	models, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error: %v", err)
	}
	want := []catalog.DiscoveredModel{{ID: "gpt-4.1", Name: "gpt-4.1"}}
	if fmt.Sprint(models) != fmt.Sprint(want) {
		t.Fatalf("models = %+v, want %+v", models, want)
	}
}

func TestNewDeepSeekModelDiscoveryListsOpenAICompatibleModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer deepseek-key" {
			t.Fatalf("unexpected DeepSeek discovery request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"deepseek-chat","object":"model","owned_by":"deepseek"}]}`)
	}))
	defer srv.Close()

	models, err := NewDeepSeekModelDiscovery(Config{APIKey: "deepseek-key", BaseURL: srv.URL + "/v1", Client: srv.Client(), ProviderName: "deepseek"}).Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "deepseek-chat" {
		t.Fatalf("expected DeepSeek's live model, got %+v", models)
	}
}
