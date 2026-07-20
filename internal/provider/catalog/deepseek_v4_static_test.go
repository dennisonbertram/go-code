package catalog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go-agent-harness/internal/provider/pricing"
)

// repoRoot returns the root of the repository by walking up from the test
// file location until we find catalog/models.json.
func repoRoot(t *testing.T) string {
	t.Helper()
	// Start from the directory of this source file and walk upward.
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(filename)
	for {
		candidate := filepath.Join(dir, "catalog", "models.json")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (catalog/models.json not found walking upward)")
		}
		dir = parent
	}
}

// TestDeepSeekV4StaticCatalogEntries verifies that the static catalog entries
// for DeepSeek V4 Pro and V4 Flash under the openrouter provider parse
// correctly without requiring a live network call.
func TestDeepSeekV4StaticCatalogEntries(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cat, err := LoadCatalog(filepath.Join(root, "catalog", "models.json"))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	or, ok := cat.Providers["openrouter"]
	if !ok {
		t.Fatal("openrouter provider not found in catalog")
	}

	tests := []struct {
		modelID           string
		wantContextWindow int
		wantInputPer1M    float64
		wantOutputPer1M   float64
		wantCacheRead1M   float64
		wantReasoning     bool
		wantToolCalling   bool
		wantStreaming     bool
		wantQuirk         string
	}{
		{
			modelID:           "deepseek/deepseek-v4-pro",
			wantContextWindow: 1048576,
			wantInputPer1M:    0.435,
			wantOutputPer1M:   0.87,
			wantCacheRead1M:   0.003625,
			wantReasoning:     true,
			wantToolCalling:   true,
			wantStreaming:     true,
			wantQuirk:         "reasoning_content_passback",
		},
		{
			modelID:           "deepseek/deepseek-v4-flash",
			wantContextWindow: 1048576,
			wantInputPer1M:    0.14,
			wantOutputPer1M:   0.28,
			wantCacheRead1M:   0.0028,
			wantReasoning:     true,
			wantToolCalling:   true,
			wantStreaming:     true,
			wantQuirk:         "reasoning_content_passback",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.modelID, func(t *testing.T) {
			t.Parallel()

			m, ok := or.Models[tc.modelID]
			if !ok {
				t.Fatalf("model %q not found in openrouter provider", tc.modelID)
			}

			if m.ContextWindow != tc.wantContextWindow {
				t.Errorf("ContextWindow = %d, want %d", m.ContextWindow, tc.wantContextWindow)
			}
			if !m.ToolCalling {
				t.Errorf("ToolCalling = false, want true")
			}
			if !m.Streaming {
				t.Errorf("Streaming = false, want true")
			}
			if !m.ReasoningMode {
				t.Errorf("ReasoningMode = false, want true")
			}

			// Verify the quirk is present.
			foundQuirk := false
			for _, q := range m.Quirks {
				if q == tc.wantQuirk {
					foundQuirk = true
					break
				}
			}
			if !foundQuirk {
				t.Errorf("Quirks = %v, want to contain %q", m.Quirks, tc.wantQuirk)
			}

			// Verify inline pricing.
			if m.Pricing == nil {
				t.Fatal("Pricing is nil")
			}
			if m.Pricing.InputPer1MTokensUSD != tc.wantInputPer1M {
				t.Errorf("InputPer1MTokensUSD = %g, want %g", m.Pricing.InputPer1MTokensUSD, tc.wantInputPer1M)
			}
			if m.Pricing.OutputPer1MTokensUSD != tc.wantOutputPer1M {
				t.Errorf("OutputPer1MTokensUSD = %g, want %g", m.Pricing.OutputPer1MTokensUSD, tc.wantOutputPer1M)
			}
			if m.Pricing.CacheReadPer1MTokensUSD != tc.wantCacheRead1M {
				t.Errorf("CacheReadPer1MTokensUSD = %g, want %g", m.Pricing.CacheReadPer1MTokensUSD, tc.wantCacheRead1M)
			}
		})
	}
}

// TestDeepSeekV4MaxContextTokensNoDiscovery verifies that MaxContextTokens
// returns the correct value from the static catalog without any live discovery.
func TestDeepSeekV4MaxContextTokensNoDiscovery(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cat, err := LoadCatalog(filepath.Join(root, "catalog", "models.json"))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	reg := NewProviderRegistryWithEnv(cat, func(string) string { return "" })

	for _, modelID := range []string{
		"deepseek/deepseek-v4-pro",
		"deepseek/deepseek-v4-flash",
	} {
		ctx, ok := reg.MaxContextTokens(modelID)
		if !ok {
			t.Errorf("MaxContextTokens(%q): not found in static catalog", modelID)
			continue
		}
		if ctx != 1048576 {
			t.Errorf("MaxContextTokens(%q) = %d, want 1048576", modelID, ctx)
		}
	}
}

// TestDeepSeekV4PricingCatalog verifies that the pricing catalog file ships
// entries for the DeepSeek V4 models under the openrouter provider.
func TestDeepSeekV4PricingCatalog(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pricingPath := filepath.Join(root, "catalog", "pricing.json")
	resolver, err := pricing.NewFileResolver(pricingPath)
	if err != nil {
		t.Fatalf("pricing.NewFileResolver: %v", err)
	}

	tests := []struct {
		modelID         string
		wantInputPer1M  float64
		wantOutputPer1M float64
	}{
		{
			modelID:         "deepseek/deepseek-v4-pro",
			wantInputPer1M:  0.435,
			wantOutputPer1M: 0.87,
		},
		{
			modelID:         "deepseek/deepseek-v4-flash",
			wantInputPer1M:  0.14,
			wantOutputPer1M: 0.28,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.modelID, func(t *testing.T) {
			t.Parallel()

			rates, ok := resolver.Resolve("openrouter", tc.modelID)
			if !ok {
				t.Fatalf("Resolve(openrouter, %q): not found", tc.modelID)
			}
			if rates.Rates.InputPer1MTokensUSD == 0 {
				t.Errorf("InputPer1MTokensUSD is zero")
			}
			if rates.Rates.InputPer1MTokensUSD != tc.wantInputPer1M {
				t.Errorf("InputPer1MTokensUSD = %g, want %g", rates.Rates.InputPer1MTokensUSD, tc.wantInputPer1M)
			}
			if rates.Rates.OutputPer1MTokensUSD != tc.wantOutputPer1M {
				t.Errorf("OutputPer1MTokensUSD = %g, want %g", rates.Rates.OutputPer1MTokensUSD, tc.wantOutputPer1M)
			}
		})
	}
}
