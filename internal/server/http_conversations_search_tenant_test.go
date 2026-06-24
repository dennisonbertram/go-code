package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// searchTenantFixture wires a server with auth enabled (two tenants) AND a
// SQLite conversation store pre-populated with conversations owned by each
// tenant. Full-text search must never return one tenant's messages to another.
type searchTenantFixture struct {
	ts      *httptest.Server
	tokenA  string
	tenantA string
	tokenB  string
	tenantB string
}

func newSearchTenantFixture(t *testing.T) *searchTenantFixture {
	t.Helper()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tenantB := "tenant-bravo"
	tokenB, keyB := generateFastAPIKey(t, tenantB, "key B", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	// SQLite conversation store with one conversation per tenant.
	path := filepath.Join(t.TempDir(), "search-tenant.db")
	cs, err := harness.NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	ctx := context.Background()
	// Tenant A's conversation contains a unique secret token.
	if err := cs.SaveConversation(ctx, "conv-A", []harness.Message{
		{Role: "user", Content: "the magic word is zxqwoplmnbsecret"},
		{Role: "assistant", Content: "noted zxqwoplmnbsecret"},
	}); err != nil {
		t.Fatalf("SaveConversation A: %v", err)
	}
	if err := cs.UpdateConversationMeta(ctx, "conv-A", "", tenantA); err != nil {
		t.Fatalf("UpdateConversationMeta A: %v", err)
	}

	// Tenant B's conversation mentions a different word.
	if err := cs.SaveConversation(ctx, "conv-B", []harness.Message{
		{Role: "user", Content: "bravo only knows pineapple"},
	}); err != nil {
		t.Fatalf("SaveConversation B: %v", err)
	}
	if err := cs.UpdateConversationMeta(ctx, "conv-B", "", tenantB); err != nil {
		t.Fatalf("UpdateConversationMeta B: %v", err)
	}

	runner := harness.NewRunner(
		fakeprovider.New(nil),
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:      "test-model",
			Store:             ms,
			ConversationStore: cs,
		},
	)

	h := server.NewWithOptions(server.ServerOptions{
		Store:  ms,
		Runner: runner,
		// AuthDisabled NOT set -- auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &searchTenantFixture{
		ts:      ts,
		tokenA:  tokenA,
		tenantA: tenantA,
		tokenB:  tokenB,
		tenantB: tenantB,
	}
}

func (f *searchTenantFixture) search(t *testing.T, token, path string) []harness.MessageSearchResult {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", path, resp.StatusCode, body)
	}
	var out struct {
		Results []harness.MessageSearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode search response: %v (%s)", err, body)
	}
	return out.Results
}

// TestTenantIsolation_Search_CrossTenantDenied (fixup-search): tenant B's
// full-text search must never surface tenant A's messages, via either the
// dedicated /search route or the ?q= delegation on the list route.
func TestTenantIsolation_Search_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newSearchTenantFixture(t)

	// Sanity: tenant A finds their own secret.
	if got := f.search(t, f.tokenA, "/v1/conversations/search?q=zxqwoplmnbsecret"); len(got) == 0 {
		t.Fatal("tenant A should find their own message containing the secret")
	}

	// Tenant B searches for tenant A's secret -> must get ZERO results.
	for _, path := range []string{
		"/v1/conversations/search?q=zxqwoplmnbsecret",
		"/v1/conversations/?q=zxqwoplmnbsecret",
	} {
		got := f.search(t, f.tokenB, path)
		for _, r := range got {
			t.Errorf("cross-tenant search leak via %s: tenant B saw conversation %q (role=%q snippet=%q)",
				path, r.ConversationID, r.Role, r.Snippet)
		}
	}

	// Tenant B still finds their own content.
	if got := f.search(t, f.tokenB, "/v1/conversations/search?q=pineapple"); len(got) == 0 {
		t.Error("tenant B should find their own message containing pineapple")
	}
}
