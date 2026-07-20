package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DiscoveredModel is the minimal live metadata needed from a provider's model
// listing endpoint.
type DiscoveredModel struct {
	ID            string
	Name          string
	ContextWindow int
}

// ModelDiscoverer exposes cached live model lookup for a provider.
type ModelDiscoverer interface {
	Models(ctx context.Context) ([]DiscoveredModel, error)
}

// DiscoveryOptions configures a provider's live model discovery.
type DiscoveryOptions struct {
	Endpoint string
	Client   *http.Client
	TTL      time.Duration
	Now      func() time.Time
	Fetch    func(ctx context.Context, client *http.Client, endpoint string) ([]DiscoveredModel, error)
}

// Discovery fetches and caches live model data from a provider.
type Discovery struct {
	endpoint string
	client   *http.Client
	ttl      time.Duration
	now      func() time.Time

	mu        sync.Mutex
	cached    []DiscoveredModel
	expiresAt time.Time
	fetchFn   func(ctx context.Context, client *http.Client, endpoint string) ([]DiscoveredModel, error)
}

// NewDiscovery creates a provider discovery client with in-memory TTL caching.
// If Fetch is omitted, it decodes OpenRouter's public model-list response.
func NewDiscovery(opts DiscoveryOptions) *Discovery {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		endpoint = "https://openrouter.ai/api/v1/models"
	}
	fetchFn := opts.Fetch
	if fetchFn == nil {
		fetchFn = FetchOpenRouterModels
	}
	return &Discovery{
		endpoint: endpoint,
		client:   client,
		ttl:      ttl,
		now:      now,
		fetchFn:  fetchFn,
	}
}

// Models returns cached live provider models when fresh, refreshes stale
// cache entries, and returns stale cached data if a refresh attempt fails.
func (d *Discovery) Models(ctx context.Context) ([]DiscoveredModel, error) {
	if d == nil {
		return nil, fmt.Errorf("model discovery is nil")
	}

	d.mu.Lock()
	if len(d.cached) > 0 && d.now().Before(d.expiresAt) {
		models := cloneDiscoveredModels(d.cached)
		d.mu.Unlock()
		return models, nil
	}
	cached := cloneDiscoveredModels(d.cached)
	d.mu.Unlock()

	models, err := d.fetch(ctx)
	if err != nil {
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}

	d.mu.Lock()
	d.cached = cloneDiscoveredModels(models)
	d.expiresAt = d.now().Add(d.ttl)
	fresh := cloneDiscoveredModels(d.cached)
	d.mu.Unlock()
	return fresh, nil
}

func (d *Discovery) fetch(ctx context.Context) ([]DiscoveredModel, error) {
	return d.fetchFn(ctx, d.client, d.endpoint)
}

// FetchOpenRouterModels decodes OpenRouter's public model-list response.
func FetchOpenRouterModels(ctx context.Context, client *http.Client, endpoint string) ([]DiscoveredModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create openrouter request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch openrouter models: status %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode openrouter models: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		models = append(models, DiscoveredModel{
			ID:            id,
			Name:          strings.TrimSpace(entry.Name),
			ContextWindow: entry.ContextLength,
		})
	}
	return models, nil
}

func cloneDiscoveredModels(in []DiscoveredModel) []DiscoveredModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]DiscoveredModel, len(in))
	copy(out, in)
	return out
}
