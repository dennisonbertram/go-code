package catalog

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSubscriptionProviderEntriesRequireTokenSource verifies that every
// catalog provider whose name ends in "-subscription" is marked
// TokenSourceRequired, so GET /v1/providers reports auth_type "subscription"
// (not "api_key") and the TUI's "i" import keybinding and subscription-only
// gating actually fire for it. Found live: kimi-subscription shipped without
// this flag, so it was misreported as an api_key provider and its /keys "i"
// import binding silently no-op'd.
func TestSubscriptionProviderEntriesRequireTokenSource(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cat, err := LoadCatalog(filepath.Join(root, "catalog", "models.json"))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	found := 0
	for name, entry := range cat.Providers {
		if !strings.HasSuffix(name, "-subscription") {
			continue
		}
		found++
		if !entry.TokenSourceRequired {
			t.Errorf("provider %q: TokenSourceRequired = false, want true (auth_type would report \"api_key\" instead of \"subscription\")", name)
		}
		if !entry.APIKeyOptional {
			t.Errorf("provider %q: APIKeyOptional = false, want true (subscription providers have no static key)", name)
		}
	}
	if found == 0 {
		t.Fatal("no *-subscription provider entries found in catalog/models.json; update this test if the naming convention changed")
	}
}
