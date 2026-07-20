package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/provider/catalog"
)

// NewModelDiscovery returns a cached discoverer for Anthropic's GET /v1/models
// endpoint. It uses the configured Anthropic API key and version header.
func NewModelDiscovery(config Config) catalog.ModelDiscoverer {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return catalog.NewDiscovery(catalog.DiscoveryOptions{
		Endpoint: baseURL + "/models",
		Client:   config.Client,
		TTL:      5 * time.Minute,
		Fetch: func(ctx context.Context, client *http.Client, endpoint string) ([]catalog.DiscoveredModel, error) {
			return fetchModels(ctx, client, endpoint, config.APIKey)
		},
	})
}

func fetchModels(ctx context.Context, client *http.Client, endpoint, apiKey string) ([]catalog.DiscoveredModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create anthropic model-list request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch anthropic models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch anthropic models: status %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode anthropic models: %w", err)
	}
	models := make([]catalog.DiscoveredModel, 0, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id != "" {
			models = append(models, catalog.DiscoveredModel{ID: id, Name: firstNonEmpty(strings.TrimSpace(entry.DisplayName), id)})
		}
	}
	return models, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
