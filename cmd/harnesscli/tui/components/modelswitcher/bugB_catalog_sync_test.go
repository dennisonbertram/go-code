package modelswitcher_test

// Regression coverage for user-reported BUG B (P2): the client-side model
// switcher shows a stale model set.
//
// DefaultModels is the hardcoded client fallback shown by New() and whenever
// the live GET /v1/models fetch has not yet completed or has failed. It had
// drifted from catalog/models.json — e.g. only listing gpt-4.1 and
// gpt-4.1-mini for OpenAI while the canonical catalog already contains
// gpt-5.1-codex, gpt-5.1-codex-mini, gpt-5.1-codex-max, gpt-5.2-codex,
// gpt-5.3-codex, and computer-use-preview.
//
// TestBugB_DefaultModelsMatchesCatalog fails whenever DefaultModels drifts
// from catalog/models.json again for the "built-in" providers (the ones
// DefaultModels hardcodes as an offline fallback). This is the safety net
// requested when full build-time generation from the catalog would be an
// invasive build-tooling change (out of scope for this fix).

import (
	"encoding/json"
	"os"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// catalogFile mirrors just the fields of catalog/models.json needed to check
// DefaultModels for drift.
type catalogFile struct {
	Providers map[string]struct {
		DisplayName string `json:"display_name"`
		Models      map[string]struct {
			DisplayName string `json:"display_name"`
		} `json:"models"`
	} `json:"providers"`
}

// builtInSyncProviders are the provider keys DefaultModels hardcodes as an
// offline/client-side fallback. OpenRouter, Together, Ollama, and LM Studio
// entries are always fetched live (or are local-only) and are intentionally
// excluded from this drift check.
var builtInSyncProviders = map[string]bool{
	"openai":    true,
	"anthropic": true,
	"gemini":    true,
	"deepseek":  true,
	"xai":       true,
	"groq":      true,
	"qwen":      true,
	"kimi":      true,
}

// catalogPath is relative to this package directory
// (cmd/harnesscli/tui/components/modelswitcher) up to the repo root.
const catalogPath = "../../../../../catalog/models.json"

func loadCatalog(t *testing.T) catalogFile {
	t.Helper()
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("reading %s: %v", catalogPath, err)
	}
	var cat catalogFile
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatalf("parsing %s: %v", catalogPath, err)
	}
	return cat
}

// TestBugB_DefaultModelsMatchesCatalog fails whenever the hardcoded
// client-side DefaultModels fallback drifts from catalog/models.json for the
// built-in providers, in either direction (catalog has a model DefaultModels
// is missing, or DefaultModels has a stale model no longer in the catalog).
func TestBugB_DefaultModelsMatchesCatalog(t *testing.T) {
	cat := loadCatalog(t)

	fallback := make(map[string]map[string]string) // provider -> id -> displayName
	for _, dm := range modelswitcher.DefaultModels {
		if fallback[dm.Provider] == nil {
			fallback[dm.Provider] = make(map[string]string)
		}
		fallback[dm.Provider][dm.ID] = dm.DisplayName
	}

	// Catalog -> DefaultModels: every built-in-provider catalog model must be
	// present in DefaultModels with a matching display name.
	for provider, pdata := range cat.Providers {
		if !builtInSyncProviders[provider] {
			continue
		}
		for id, cm := range pdata.Models {
			got, ok := fallback[provider][id]
			if !ok {
				t.Errorf("DefaultModels is missing catalog model %q for provider %q (display_name %q) — client fallback is stale",
					id, provider, cm.DisplayName)
				continue
			}
			if got != cm.DisplayName {
				t.Errorf("DefaultModels[%q].DisplayName = %q, catalog/models.json says %q", id, got, cm.DisplayName)
			}
		}
	}

	// DefaultModels -> catalog: every built-in-provider DefaultModels entry
	// must still exist in the catalog (no dead models lingering client-side).
	for provider := range builtInSyncProviders {
		catModels := cat.Providers[provider].Models
		for id := range fallback[provider] {
			if _, ok := catModels[id]; !ok {
				t.Errorf("DefaultModels has model %q for provider %q that no longer exists in catalog/models.json", id, provider)
			}
		}
	}
}

// TestBugB_OpenAIFallbackIncludesCodexModels is a narrower, human-readable
// reproduction of the originally reported symptom: the OpenAI section of the
// client fallback listed only gpt-4.1 / gpt-4.1-mini, hiding every 5.x codex
// model already present in the canonical catalog.
func TestBugB_OpenAIFallbackIncludesCodexModels(t *testing.T) {
	want := []string{
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.1-codex-max",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"computer-use-preview",
	}
	got := make(map[string]bool)
	for _, dm := range modelswitcher.DefaultModels {
		if dm.Provider == "openai" {
			got[dm.ID] = true
		}
	}
	for _, id := range want {
		if !got[id] {
			t.Errorf("DefaultModels (OpenAI) is missing %q — offline fallback is stale relative to catalog/models.json", id)
		}
	}
}
