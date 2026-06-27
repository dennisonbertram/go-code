package main

import (
	"os"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/harness"
	openai "go-agent-harness/internal/provider/openai"
)

// TestBootstrapPricingResolverNonNilByDefault verifies that when
// HARNESS_PRICING_CATALOG_PATH is unset but a model catalog is present,
// the pricing resolver is wired from the catalog (not nil).
func TestBootstrapPricingResolverNonNilByDefault(t *testing.T) {
	t.Parallel()

	// Point workspace at the real catalog so the resolver can be built.
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}

	bootstrap, err := buildCatalogBootstrap(catalogBootstrapOptions{
		workspace: repoRoot,
		getenv: func(key string) string {
			// Deliberately omit HARNESS_PRICING_CATALOG_PATH.
			return ""
		},
		newProvider: func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildCatalogBootstrap: %v", err)
	}
	if bootstrap.modelCatalog == nil {
		t.Fatal("expected model catalog to be loaded from repo workspace")
	}
	if bootstrap.pricingResolver == nil {
		t.Fatal("pricingResolver is nil: the catalog fallback wiring is missing (PRIMARY BUG #665)")
	}
}

// TestBootstrapPricingResolverResolvesAnthropicHaiku verifies that the
// catalog-backed resolver returns non-zero rates for claude-haiku-4-5-20251001,
// which has a pricing block in catalog/models.json.
func TestBootstrapPricingResolverResolvesAnthropicHaiku(t *testing.T) {
	t.Parallel()

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}

	bootstrap, err := buildCatalogBootstrap(catalogBootstrapOptions{
		workspace: repoRoot,
		getenv:    func(string) string { return "" },
		newProvider: func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildCatalogBootstrap: %v", err)
	}
	if bootstrap.pricingResolver == nil {
		t.Fatal("pricingResolver is nil; skip inner assertion (see TestBootstrapPricingResolverNonNilByDefault)")
	}

	rates, ok := bootstrap.pricingResolver.Resolve("anthropic", "claude-haiku-4-5-20251001")
	if !ok {
		t.Fatal("Resolve(anthropic, claude-haiku-4-5-20251001): got ok=false, want true")
	}
	if rates.Rates.InputPer1MTokensUSD <= 0 {
		t.Errorf("InputPer1MTokensUSD = %v, want > 0", rates.Rates.InputPer1MTokensUSD)
	}
	if rates.Rates.OutputPer1MTokensUSD <= 0 {
		t.Errorf("OutputPer1MTokensUSD = %v, want > 0", rates.Rates.OutputPer1MTokensUSD)
	}
}

// TestBootstrapPricingResolverExplicitPathStillWorks verifies that an explicitly
// provided HARNESS_PRICING_CATALOG_PATH overrides the catalog fallback.
func TestBootstrapPricingResolverExplicitPathStillWorks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pricingFile := filepath.Join(dir, "pricing.json")
	if err := os.WriteFile(pricingFile, []byte(`{
  "pricing_version": "test",
  "providers": {
    "openai": {
      "models": {
        "gpt-4.1-mini": {
          "input_per_1m_tokens_usd": 0.4,
          "output_per_1m_tokens_usd": 1.6
        }
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write pricing file: %v", err)
	}

	bootstrap, err := buildCatalogBootstrap(catalogBootstrapOptions{
		workspace: dir,
		getenv: func(key string) string {
			if key == "HARNESS_PRICING_CATALOG_PATH" {
				return pricingFile
			}
			return ""
		},
		newProvider: func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildCatalogBootstrap: %v", err)
	}
	// Resolver must be non-nil regardless of whether catalog was also loaded.
	if bootstrap.pricingResolver == nil {
		t.Fatal("pricingResolver is nil even with explicit pricing file path")
	}
	rates, ok := bootstrap.pricingResolver.Resolve("openai", "gpt-4.1-mini")
	if !ok {
		t.Fatal("Resolve(openai, gpt-4.1-mini): ok=false with explicit pricing file")
	}
	if rates.Rates.InputPer1MTokensUSD != 0.4 {
		t.Errorf("InputPer1MTokensUSD = %v, want 0.4", rates.Rates.InputPer1MTokensUSD)
	}
}

// findRepoRoot walks up from the test binary's working directory to find the
// repo root (the directory containing catalog/models.json).
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "catalog", "models.json")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
