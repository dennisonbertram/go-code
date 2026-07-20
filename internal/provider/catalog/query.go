package catalog

import "sort"

// ModelResult represents a model with its provider context.
type ModelResult struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_display_name"`
	ModelID      string `json:"model_id"`
	Model        Model  `json:"model"`
}

// FilterOptions for filtering models.
type FilterOptions struct {
	Provider    string // filter by provider key
	ToolCalling *bool  // filter by tool_calling support
	Streaming   *bool  // filter by streaming support
	SpeedTier   string // filter by speed_tier
	CostTier    string // filter by cost_tier
	Modality    string // filter by modality (e.g. "text", "vision")
	MinContext  int    // minimum context_window

	BestFor   string // filter by best_for tag
	Strength  string // filter by strength tag
	Reasoning *bool  // filter by reasoning_mode
}

// ProviderSummary is a brief summary of a provider.
type ProviderSummary struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	ModelCount  int    `json:"model_count"`
	BaseURL     string `json:"base_url"`
}

// ListModels returns all models in the catalog, sorted by provider then model ID.
func (c *Catalog) ListModels() []ModelResult {
	var results []ModelResult
	for provKey, prov := range c.Providers {
		for modelID, model := range prov.Models {
			results = append(results, ModelResult{
				Provider:     provKey,
				ProviderName: prov.DisplayName,
				ModelID:      modelID,
				Model:        model,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Provider != results[j].Provider {
			return results[i].Provider < results[j].Provider
		}
		return results[i].ModelID < results[j].ModelID
	})
	return results
}

// FilterModels returns models matching all the given filter options (AND logic).
func (c *Catalog) FilterModels(opts FilterOptions) []ModelResult {
	all := c.ListModels()
	var results []ModelResult
	for _, r := range all {
		if matchesFilter(r, opts) {
			results = append(results, r)
		}
	}
	return results
}

// ModelInfo returns detailed info for a specific model.
func (c *Catalog) ModelInfo(provider, modelID string) (*ModelResult, bool) {
	prov, ok := c.Providers[provider]
	if !ok {
		return nil, false
	}
	model, ok := prov.Models[modelID]
	if !ok {
		return nil, false
	}
	return &ModelResult{
		Provider:     provider,
		ProviderName: prov.DisplayName,
		ModelID:      modelID,
		Model:        model,
	}, true
}

// ListProviders returns a summary of each provider in the catalog.
func (c *Catalog) ListProviders() []ProviderSummary {
	var summaries []ProviderSummary
	for key, prov := range c.Providers {
		summaries = append(summaries, ProviderSummary{
			Key:         key,
			DisplayName: prov.DisplayName,
			ModelCount:  len(prov.Models),
			BaseURL:     prov.BaseURL,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Key < summaries[j].Key
	})
	return summaries
}

func matchesFilter(r ModelResult, opts FilterOptions) bool {
	if opts.Provider != "" && r.Provider != opts.Provider {
		return false
	}
	if opts.ToolCalling != nil && r.Model.ToolCalling != *opts.ToolCalling {
		return false
	}
	if opts.Streaming != nil && r.Model.Streaming != *opts.Streaming {
		return false
	}
	if opts.SpeedTier != "" && r.Model.SpeedTier != opts.SpeedTier {
		return false
	}
	if opts.CostTier != "" && r.Model.CostTier != opts.CostTier {
		return false
	}
	if opts.Modality != "" && !containsString(r.Model.Modalities, opts.Modality) {
		return false
	}
	if opts.BestFor != "" && !containsString(r.Model.BestFor, opts.BestFor) {
		return false
	}
	if opts.Strength != "" && !containsString(r.Model.Strengths, opts.Strength) {
		return false
	}
	if opts.MinContext > 0 && r.Model.ContextWindow < opts.MinContext {
		return false
	}
	if opts.Reasoning != nil && r.Model.ReasoningMode != *opts.Reasoning {
		return false
	}
	return true
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
