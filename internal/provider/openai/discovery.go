package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/provider/catalog"
)

// NewModelDiscovery returns a cached discoverer for an OpenAI-compatible
// provider's GET /v1/models endpoint. It uses the configured provider key.
func NewModelDiscovery(config Config) catalog.ModelDiscoverer {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
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
		return nil, fmt.Errorf("create openai model-list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openai models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch openai models: status %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode openai models: %w", err)
	}
	models := make([]catalog.DiscoveredModel, 0, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id != "" {
			models = append(models, catalog.DiscoveredModel{ID: id, Name: id})
		}
	}
	return models, nil
}
