package catalog

// Catalog is the top-level structure for the multi-provider model catalog.
type Catalog struct {
	CatalogVersion string                   `json:"catalog_version"`
	Providers      map[string]ProviderEntry `json:"providers"`
}

// ProviderEntry describes one LLM provider and its available models.
type ProviderEntry struct {
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url"`
	APIKeyEnv   string `json:"api_key_env"`
	// APIKeyOptional marks providers (e.g. local Ollama/LM Studio servers) that
	// do not require an API key to resolve or create a client.
	APIKeyOptional bool `json:"api_key_optional,omitempty"`
	// TokenSourceRequired marks a remote provider whose optional static API key
	// must be replaced by a runtime TokenSource (for subscription auth). It is
	// deliberately distinct from anonymous local-server providers.
	TokenSourceRequired bool     `json:"token_source_required,omitempty"`
	Protocol            string   `json:"protocol"`
	Quirks              []string `json:"quirks,omitempty"`
	// ModelsFrom derives this provider's model metadata from another provider
	// at load time, keeping mirrored billing routes in lockstep.
	ModelsFrom string            `json:"models_from,omitempty"`
	Models     map[string]Model  `json:"models"`
	Aliases    map[string]string `json:"aliases,omitempty"`
}

// Model describes a single LLM model's capabilities and metadata.
type Model struct {
	DisplayName       string        `json:"display_name"`
	Description       string        `json:"description"`
	ContextWindow     int           `json:"context_window"`
	MaxOutputTokens   int           `json:"max_output_tokens,omitempty"`
	Modalities        []string      `json:"modalities"`
	ToolCalling       bool          `json:"tool_calling"`
	ParallelToolCalls bool          `json:"parallel_tool_calls,omitempty"`
	Streaming         bool          `json:"streaming"`
	ReasoningMode     bool          `json:"reasoning_mode,omitempty"`
	Quirks            []string      `json:"quirks,omitempty"`
	Strengths         []string      `json:"strengths,omitempty"`
	Weaknesses        []string      `json:"weaknesses,omitempty"`
	BestFor           []string      `json:"best_for,omitempty"`
	SpeedTier         string        `json:"speed_tier,omitempty"`
	CostTier          string        `json:"cost_tier,omitempty"`
	Pricing           *ModelPricing `json:"pricing,omitempty"`
	// API specifies the wire protocol endpoint for this model.
	// "responses" means POST /v1/responses (OpenAI Responses API).
	// Empty string (default) means POST /v1/chat/completions.
	API string `json:"api,omitempty"`
}

// ModelPricing holds per-token cost information for a model.
type ModelPricing struct {
	InputPer1MTokensUSD      float64 `json:"input_per_1m_tokens_usd"`
	OutputPer1MTokensUSD     float64 `json:"output_per_1m_tokens_usd"`
	CacheReadPer1MTokensUSD  float64 `json:"cache_read_per_1m_tokens_usd,omitempty"`
	CacheWritePer1MTokensUSD float64 `json:"cache_write_per_1m_tokens_usd,omitempty"`
}
