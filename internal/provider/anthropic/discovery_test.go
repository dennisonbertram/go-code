package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/provider/catalog"
)

func TestNewModelDiscoveryListsAnthropicModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("expected x-api-key authentication, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Fatalf("expected anthropic version %q, got %q", anthropicVersion, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-sonnet-4-6","display_name":"Claude Sonnet 4.6","created_at":"2026-02-17T00:00:00Z"}],"has_more":false,"first_id":"claude-sonnet-4-6","last_id":"claude-sonnet-4-6"}`)
	}))
	defer srv.Close()

	discovery := NewModelDiscovery(Config{APIKey: "test-key", BaseURL: srv.URL + "/v1", Client: srv.Client()})
	models, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error: %v", err)
	}
	want := []catalog.DiscoveredModel{{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"}}
	if fmt.Sprint(models) != fmt.Sprint(want) {
		t.Fatalf("models = %+v, want %+v", models, want)
	}
}
