package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryDecodeOpenRouterResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "moonshotai/kimi-k2.5",
					"name":           "Kimi K2.5",
					"context_length": 262144,
				},
				{
					"id":   "openai/gpt-4.1-mini",
					"name": "GPT-4.1 Mini",
				},
			},
		})
	}))
	defer srv.Close()

	discovery := NewDiscovery(DiscoveryOptions{
		Endpoint: srv.URL,
		Client:   srv.Client(),
		TTL:      time.Minute,
	})

	models, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "moonshotai/kimi-k2.5" {
		t.Fatalf("expected first id moonshotai/kimi-k2.5, got %q", models[0].ID)
	}
	if models[0].Name != "Kimi K2.5" {
		t.Fatalf("expected first name Kimi K2.5, got %q", models[0].Name)
	}
	if models[0].ContextWindow != 262144 {
		t.Fatalf("expected first context window 262144, got %d", models[0].ContextWindow)
	}
}

func TestDiscoveryTTLCache(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"id":"model-%d","name":"Model %d"}]}`, hits.Load(), hits.Load())
	}))
	defer srv.Close()

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	discovery := NewDiscovery(DiscoveryOptions{
		Endpoint: srv.URL,
		Client:   srv.Client(),
		TTL:      time.Minute,
		Now: func() time.Time {
			return now
		},
	})

	first, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("first Models() error: %v", err)
	}
	second, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("second Models() error: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 upstream hit before ttl expiry, got %d", hits.Load())
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("expected cached model id %q, got %q", first[0].ID, second[0].ID)
	}

	now = now.Add(2 * time.Minute)

	third, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("third Models() error: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected second upstream hit after ttl expiry, got %d", hits.Load())
	}
	if third[0].ID == second[0].ID {
		t.Fatalf("expected refreshed model after ttl expiry, still got %q", third[0].ID)
	}
}

func TestDiscoveryReturnsCachedDataWhenRefreshFails(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := calls.Add(1)
		if current > 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"moonshotai/kimi-k2.5","name":"Kimi K2.5"}]}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	discovery := NewDiscovery(DiscoveryOptions{
		Endpoint: srv.URL,
		Client:   srv.Client(),
		TTL:      time.Minute,
		Now: func() time.Time {
			return now
		},
	})

	first, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("first Models() error: %v", err)
	}

	now = now.Add(2 * time.Minute)

	second, err := discovery.Models(context.Background())
	if err != nil {
		t.Fatalf("expected cached data on refresh failure, got error: %v", err)
	}
	if len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("expected cached model %q, got %+v", first[0].ID, second)
	}
}

func TestDiscoveryNilReturnsError(t *testing.T) {
	t.Parallel()

	var discovery *Discovery
	if _, err := discovery.Models(context.Background()); err == nil {
		t.Fatal("expected nil generic discovery to return an error")
	}
}
