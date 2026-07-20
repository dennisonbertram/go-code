package catalog

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProviderClient is the interface that provider clients must implement.
// This avoids an import cycle with the harness package.
type ProviderClient interface{}

// ClientFactory creates a provider client given API key, base URL, and provider name.
type ClientFactory func(apiKey, baseURL, providerName string) (ProviderClient, error)

// ProviderRegistry holds a Catalog and lazily creates provider client instances per provider.
type ProviderRegistry struct {
	catalog       *Catalog
	mu            sync.RWMutex
	clients       map[string]ProviderClient
	overrideKeys  map[string]string
	getenv        func(string) string
	clientFactory ClientFactory
	discoverers   map[string]ModelDiscoverer
}

// NewProviderRegistry creates a registry that uses os.Getenv for API key lookup.
func NewProviderRegistry(catalog *Catalog) *ProviderRegistry {
	return &ProviderRegistry{
		catalog:     catalog,
		clients:     make(map[string]ProviderClient),
		discoverers: make(map[string]ModelDiscoverer),
		getenv:      os.Getenv,
	}
}

// NewProviderRegistryWithEnv creates a registry with a custom getenv function (for testing).
func NewProviderRegistryWithEnv(catalog *Catalog, getenv func(string) string) *ProviderRegistry {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &ProviderRegistry{
		catalog:     catalog,
		clients:     make(map[string]ProviderClient),
		discoverers: make(map[string]ModelDiscoverer),
		getenv:      getenv,
	}
}

// SetClientFactory sets the factory function used to create provider clients.
// Must be called before GetClient/GetClientForModel if client creation is needed.
func (r *ProviderRegistry) SetClientFactory(factory ClientFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clientFactory = factory
}

// SetDiscovery sets the live model discoverer used for additive model
// resolution and merged model listing for providerName.
func (r *ProviderRegistry) SetDiscovery(providerName string, discoverer ModelDiscoverer) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if discoverer == nil {
		delete(r.discoverers, providerName)
		return
	}
	if r.discoverers == nil {
		r.discoverers = make(map[string]ModelDiscoverer)
	}
	r.discoverers[providerName] = discoverer
}

// SetAPIKey stores a runtime API key override for the named provider.
// When set, GetClient uses this key instead of the environment variable.
func (r *ProviderRegistry) SetAPIKey(provider, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.overrideKeys == nil {
		r.overrideKeys = make(map[string]string)
	}
	r.overrideKeys[provider] = key
	// Evict any cached client so the next GetClient uses the new key.
	delete(r.clients, provider)
}

// IsConfigured returns true if the named provider has an API key available,
// either via a runtime override, the environment variable, or an optional
// local-provider configuration that does not require a key.
func (r *ProviderRegistry) IsConfigured(providerName string) bool {
	r.mu.RLock()
	if k := r.overrideKeys[providerName]; k != "" {
		r.mu.RUnlock()
		return true
	}
	r.mu.RUnlock()
	entry, ok := r.catalog.Providers[providerName]
	if !ok {
		return false
	}
	if entry.APIKeyOptional {
		return true
	}
	return r.getenv(entry.APIKeyEnv) != ""
}

// GetClient returns (or lazily creates) a provider client for the named provider.
func (r *ProviderRegistry) GetClient(providerName string) (ProviderClient, error) {
	// Fast path: check if already created.
	r.mu.RLock()
	if client, ok := r.clients[providerName]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	// Slow path: create client under write lock.
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if client, ok := r.clients[providerName]; ok {
		return client, nil
	}

	entry, ok := r.catalog.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not found in catalog", providerName)
	}

	// Check runtime override before falling back to environment variable.
	apiKey := r.overrideKeys[providerName]
	if apiKey == "" {
		apiKey = r.getenv(entry.APIKeyEnv)
	}
	if apiKey == "" {
		if !entry.APIKeyOptional {
			return nil, fmt.Errorf("provider %q: API key env %q is not set", providerName, entry.APIKeyEnv)
		}
		apiKey = providerName
	}

	if r.clientFactory == nil {
		return nil, fmt.Errorf("provider %q: no client factory configured", providerName)
	}

	if entry.APIKeyOptional {
		if err := checkLocalServerReachable(providerName, entry); err != nil {
			return nil, err
		}
	}

	client, err := r.clientFactory(apiKey, entry.BaseURL, providerName)
	if err != nil {
		return nil, fmt.Errorf("create client for provider %q: %w", providerName, err)
	}

	r.clients[providerName] = client
	return client, nil
}

// GetClientForModel searches all providers to find which one has the given model,
// returns the client and provider name.
func (r *ProviderRegistry) GetClientForModel(modelID string) (ProviderClient, string, error) {
	providerName, found := r.ResolveProvider(modelID)
	if !found {
		return nil, "", fmt.Errorf("model %q not found in any provider", modelID)
	}

	client, err := r.GetClient(providerName)
	if err != nil {
		return nil, "", err
	}
	return client, providerName, nil
}

// ResolveProvider searches all providers to find which one has the given model (including aliases).
func (r *ProviderRegistry) ResolveProvider(modelID string) (string, bool) {
	return r.ResolveProviderContext(context.Background(), modelID)
}

// ResolveProviderContext searches all providers to find which one has the given
// model, including live discovery when configured.
func (r *ProviderRegistry) ResolveProviderContext(ctx context.Context, modelID string) (string, bool) {
	if r.catalog == nil {
		return "", false
	}
	modelID = strings.TrimSpace(modelID)
	if providerName, found := r.resolveProviderFromCatalog(modelID); found {
		return providerName, true
	}
	if providerName, found := r.hasDiscoveredModel(ctx, modelID); found {
		return providerName, true
	}
	// OpenRouter exposes a very large dynamic slug space (for example
	// "moonshotai/kimi-k2.5"), so startup and run-time routing cannot depend on
	// every routed model being hardcoded in the local catalog.
	if modelID != "" && strings.Contains(modelID, "/") {
		if _, ok := r.catalog.Providers["openrouter"]; ok {
			return "openrouter", true
		}
	}
	return "", false
}

// ResolveProviderStatic resolves only against the loaded static catalog and
// does not trigger any live discovery requests.
func (r *ProviderRegistry) ResolveProviderStatic(modelID string) (string, bool) {
	if r == nil || r.catalog == nil {
		return "", false
	}
	return r.resolveProviderFromCatalog(strings.TrimSpace(modelID))
}

func (r *ProviderRegistry) resolveProviderFromCatalog(modelID string) (string, bool) {
	for name, entry := range r.catalog.Providers {
		// Check direct model match.
		if _, ok := entry.Models[modelID]; ok {
			return name, true
		}
		// Check alias match.
		if target, ok := entry.Aliases[modelID]; ok {
			if _, modelOK := entry.Models[target]; modelOK {
				return name, true
			}
		}
	}
	return "", false
}

// Catalog returns the underlying catalog (read-only access).
func (r *ProviderRegistry) Catalog() *Catalog {
	return r.catalog
}

// ListModelsContext returns a merged static/live model listing. Static catalog
// metadata remains authoritative when it overlaps with live OpenRouter entries.
func (r *ProviderRegistry) ListModelsContext(ctx context.Context) []ModelResult {
	if r == nil || r.catalog == nil {
		return []ModelResult{}
	}
	return r.effectiveCatalog(ctx).ListModels()
}

// MaxContextTokens returns the context window size for the given model from the
// catalog. Returns 0 and false when the model is not found or the registry has
// no catalog. The value comes from Model.ContextWindow which is validated > 0
// at load time, so a non-zero return is always a valid token count.
func (r *ProviderRegistry) MaxContextTokens(modelID string) (int, bool) {
	return r.MaxContextTokensContext(context.Background(), modelID)
}

// MaxContextTokensContext returns the merged-context window size for the given model.
func (r *ProviderRegistry) MaxContextTokensContext(ctx context.Context, modelID string) (int, bool) {
	if r == nil || r.catalog == nil {
		return 0, false
	}
	providerName, found := r.ResolveProviderContext(ctx, modelID)
	if !found {
		return 0, false
	}
	entry, ok := r.effectiveCatalog(ctx).Providers[providerName]
	if !ok {
		return 0, false
	}
	// Resolve alias if needed.
	resolved := modelID
	if target, ok := entry.Aliases[modelID]; ok {
		if _, modelOK := entry.Models[target]; modelOK {
			resolved = target
		}
	}
	m, ok := entry.Models[resolved]
	if !ok {
		return 0, false
	}
	return m.ContextWindow, true
}

func (r *ProviderRegistry) hasDiscoveredModel(ctx context.Context, modelID string) (string, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", false
	}
	if r == nil || r.catalog == nil {
		return "", false
	}
	for providerName, discoverer := range r.discoverySnapshot() {
		if _, ok := r.catalog.Providers[providerName]; !ok {
			continue
		}
		models, err := discoverer.Models(ctx)
		if err != nil {
			continue
		}
		for _, model := range models {
			if strings.TrimSpace(model.ID) == modelID {
				return providerName, true
			}
		}
	}
	return "", false
}

func (r *ProviderRegistry) effectiveCatalog(ctx context.Context) *Catalog {
	clone := cloneCatalog(r.catalog)
	if clone == nil {
		return nil
	}
	if r == nil {
		return clone
	}
	for providerName, discoverer := range r.discoverySnapshot() {
		entry, ok := clone.Providers[providerName]
		if !ok {
			continue
		}
		models, err := discoverer.Models(ctx)
		if err != nil {
			continue
		}
		clone.Providers[providerName] = mergeDiscoveredProvider(entry, models)
	}
	return clone
}

func (r *ProviderRegistry) discoverySnapshot() map[string]ModelDiscoverer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	discoverers := make(map[string]ModelDiscoverer, len(r.discoverers))
	for providerName, discoverer := range r.discoverers {
		discoverers[providerName] = discoverer
	}
	return discoverers
}

func mergeDiscoveredProvider(static ProviderEntry, discovered []DiscoveredModel) ProviderEntry {
	merged := cloneProviderEntry(static)
	if merged.Models == nil {
		merged.Models = make(map[string]Model, len(discovered))
	}
	for _, model := range discovered {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		existing, ok := merged.Models[id]
		if ok {
			if strings.TrimSpace(existing.DisplayName) == "" {
				existing.DisplayName = firstNonEmpty(model.Name, id)
			}
			if existing.ContextWindow <= 0 {
				existing.ContextWindow = model.ContextWindow
			}
			merged.Models[id] = existing
			continue
		}
		merged.Models[id] = Model{
			DisplayName:   firstNonEmpty(model.Name, id),
			ContextWindow: model.ContextWindow,
		}
	}
	return merged
}

func cloneCatalog(cat *Catalog) *Catalog {
	if cat == nil {
		return nil
	}
	providers := make(map[string]ProviderEntry, len(cat.Providers))
	for name, entry := range cat.Providers {
		providers[name] = cloneProviderEntry(entry)
	}
	return &Catalog{
		CatalogVersion: cat.CatalogVersion,
		Providers:      providers,
	}
}

func cloneProviderEntry(entry ProviderEntry) ProviderEntry {
	cloned := entry
	cloned.Quirks = append([]string(nil), entry.Quirks...)
	cloned.Models = make(map[string]Model, len(entry.Models))
	for id, model := range entry.Models {
		cloned.Models[id] = cloneModel(model)
	}
	if len(entry.Aliases) > 0 {
		cloned.Aliases = make(map[string]string, len(entry.Aliases))
		for alias, target := range entry.Aliases {
			cloned.Aliases[alias] = target
		}
	}
	return cloned
}

func cloneModel(model Model) Model {
	cloned := model
	cloned.Modalities = append([]string(nil), model.Modalities...)
	cloned.Strengths = append([]string(nil), model.Strengths...)
	cloned.Weaknesses = append([]string(nil), model.Weaknesses...)
	cloned.BestFor = append([]string(nil), model.BestFor...)
	if model.Pricing != nil {
		pricing := *model.Pricing
		cloned.Pricing = &pricing
	}
	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// checkLocalServerReachable probes the local provider's OpenAI-compatible
// /v1/models endpoint. If the server is not reachable, it returns a clear,
// actionable error naming the provider, its base URL, and how to start it.
func checkLocalServerReachable(providerName string, entry ProviderEntry) error {
	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := strings.TrimRight(entry.BaseURL, "/")
	url := baseURL + "/models"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("no %s server reachable at %s — is %s running?: %w", entry.DisplayName, serverBaseURL(entry.BaseURL), localServerHint(providerName), err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("no %s server reachable at %s — is %s running?", entry.DisplayName, serverBaseURL(entry.BaseURL), localServerHint(providerName))
	}
	defer resp.Body.Close()
	return nil
}

func serverBaseURL(baseURL string) string {
	u := strings.TrimRight(baseURL, "/")
	u = strings.TrimSuffix(u, "/v1")
	if u == "" {
		return baseURL
	}
	return u
}

func localServerHint(providerName string) string {
	switch providerName {
	case "ollama":
		return "`ollama serve`"
	case "lmstudio":
		return "LM Studio"
	default:
		return "the server"
	}
}

// ModelAliasesContext returns aliases keyed by canonical model id for the given provider.
func (r *ProviderRegistry) ModelAliasesContext(ctx context.Context, providerName string) map[string][]string {
	if r == nil || r.catalog == nil {
		return map[string][]string{}
	}
	entry, ok := r.effectiveCatalog(ctx).Providers[providerName]
	if !ok {
		return map[string][]string{}
	}
	aliases := make(map[string][]string)
	for alias, target := range entry.Aliases {
		aliases[target] = append(aliases[target], alias)
	}
	for target := range aliases {
		sort.Strings(aliases[target])
	}
	return aliases
}

// CanonicalModelForProvider resolves a model slug to its canonical form when
// routing to a specific (non-OpenRouter) provider. When the slug contains a
// "/" separator and the prefix corresponds to the target provider, the prefix
// is stripped to yield the native provider model ID (e.g.
// "deepseek/deepseek-v4-flash" becomes "deepseek-v4-flash" for provider
// "deepseek").
//
// Aliases in the provider catalog are also resolved against the given provider.
//
// The raw modelID is returned unchanged when:
//   - The provider is OpenRouter.
//   - The modelID does not contain a "/" separator.
//   - The prefix does not correspond to the target provider.
func (r *ProviderRegistry) CanonicalModelForProvider(modelID, providerName string) string {
	modelID = strings.TrimSpace(modelID)
	providerName = strings.TrimSpace(providerName)

	// OpenRouter keeps its qualified slugs; no stripping needed.
	if strings.EqualFold(providerName, "openrouter") {
		return modelID
	}
	if modelID == "" || providerName == "" {
		return modelID
	}

	// No "/" means it is already a bare native ID.
	if !strings.Contains(modelID, "/") {
		// Resolve aliases within the provider's catalog entry.
		if r != nil && r.catalog != nil {
			if entry, ok := r.catalog.Providers[providerName]; ok {
				if resolved := resolveAlias(entry.Aliases, modelID); resolved != "" {
					return resolved
				}
			}
		}
		return modelID
	}

	// Strip a known OpenRouter provider prefix. The slug "deepseek/deepseek-v4-flash"
	// should become "deepseek-v4-flash" when the target provider is "deepseek".
	idx := strings.Index(modelID, "/")
	prefix := modelID[:idx]
	suffix := modelID[idx+1:]

	// Check whether the prefix corresponds to the target provider.
	if matchesProviderPrefix(prefix, providerName) {
		return suffix
	}

	// For unknown prefixes that are not the target provider, return the slug as-is
	// (this lets callers fall through to OpenRouter resolution).
	return modelID
}

// matchesProviderPrefix checks whether an OpenRouter-style provider prefix
// (e.g. "deepseek", "openai", "anthropic") corresponds to the target provider
// key. The mapping handles known OpenRouter provider prefix aliases.
func matchesProviderPrefix(prefix, provider string) bool {
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	provider = strings.TrimSpace(strings.ToLower(provider))
	if prefix == "" || provider == "" {
		return false
	}

	// Direct match.
	if prefix == provider {
		return true
	}

	// Known OpenRouter prefix aliases for common providers.
	knownAliases := map[string]string{
		"x-ai":       "xai",
		"meta-llama": "groq", // meta-llama models are often routed via groq
		"moonshotai": "kimi",
		"google":     "gemini",
	}
	if mapped, ok := knownAliases[prefix]; ok {
		return mapped == provider
	}

	return false
}
