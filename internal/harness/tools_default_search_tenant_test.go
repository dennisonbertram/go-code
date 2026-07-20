package harness

import (
	"context"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// spyConversationStore implements ConversationStore and records the tenantID
// passed to SearchMessages so tests can assert that the adapter threads the
// run's tenant into the search call.
type spyConversationStore struct {
	capturedTenantID string
	results          []MessageSearchResult
	searchErr        error
}

func (s *spyConversationStore) Migrate(_ context.Context) error { return nil }
func (s *spyConversationStore) Close() error                    { return nil }
func (s *spyConversationStore) SaveConversation(_ context.Context, _ string, _ []Message) error {
	return nil
}
func (s *spyConversationStore) SaveConversationWithCost(_ context.Context, _ string, _ []Message, _ ConversationTokenCost) error {
	return nil
}
func (s *spyConversationStore) LoadMessages(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}
func (s *spyConversationStore) ListConversations(_ context.Context, _ ConversationFilter, _, _ int) ([]Conversation, error) {
	return nil, nil
}
func (s *spyConversationStore) DeleteConversation(_ context.Context, _ string) error { return nil }
func (s *spyConversationStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return nil
}
func (s *spyConversationStore) GetConversationOwner(_ context.Context, _ string) (*Conversation, error) {
	return nil, nil
}
func (s *spyConversationStore) SearchMessages(_ context.Context, tenantID, _ string, _ int) ([]MessageSearchResult, error) {
	s.capturedTenantID = tenantID
	return s.results, s.searchErr
}
func (s *spyConversationStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (s *spyConversationStore) PinConversation(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *spyConversationStore) CompactConversation(_ context.Context, _ string, _ int, _ Message) error {
	return nil
}
func (s *spyConversationStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}
func (s *spyConversationStore) ForkConversation(_ context.Context, _, _ string) (*Conversation, error) {
	return nil, nil
}

// TestConversationStoreAdapter_SearchScoped verifies that SearchConversations
// threads the run's TenantID (from context RunMetadata) into SearchMessages,
// so agents cannot search across tenant boundaries via the in-agent tool.
// This is the TDD test for the PFIX-6 cross-tenant search fix.
func TestConversationStoreAdapter_SearchScoped(t *testing.T) {
	t.Parallel()

	spy := &spyConversationStore{
		results: []MessageSearchResult{
			{ConversationID: "conv-1", Role: "user", Snippet: "hello world"},
		},
	}
	adapter := &conversationStoreAdapter{store: spy}

	// Context carries RunMetadata for tenant "tenantA".
	ctx := context.WithValue(
		context.Background(),
		htools.ContextKeyRunMetadata,
		htools.RunMetadata{RunID: "run-1", TenantID: "tenantA"},
	)

	results, err := adapter.SearchConversations(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("SearchConversations: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// The tenant passed to SearchMessages MUST equal the run's TenantID.
	if spy.capturedTenantID != "tenantA" {
		t.Errorf("SearchMessages called with tenantID=%q; want %q (cross-tenant search leak)", spy.capturedTenantID, "tenantA")
	}
}

// TestConversationStoreAdapter_SearchDefaultTenantPassthrough verifies that
// the "default" synthetic tenant is passed through to SearchMessages (non-empty),
// so the filter remains active for single-tenant deployments that use the
// runner's canonical "default" tenant normalisation.
func TestConversationStoreAdapter_SearchDefaultTenantPassthrough(t *testing.T) {
	t.Parallel()

	spy := &spyConversationStore{}
	adapter := &conversationStoreAdapter{store: spy}

	ctx := context.WithValue(
		context.Background(),
		htools.ContextKeyRunMetadata,
		htools.RunMetadata{RunID: "run-2", TenantID: "default"},
	)

	_, _ = adapter.SearchConversations(ctx, "query", 5)

	// "default" is the runner's canonical form for the unnamed tenant.
	// Passing it to SearchMessages as non-empty keeps the filter active
	// and scopes results to conversations whose tenant_id = "default".
	// An empty tenantID would disable the filter entirely.
	if spy.capturedTenantID == "" {
		t.Errorf("SearchMessages called with empty tenantID for 'default' tenant; filter must remain active")
	}
}

// TestConversationStoreAdapter_SearchNoMetadataNoFilter verifies that when
// no RunMetadata is present in the context (e.g. auth-disabled local callers),
// SearchMessages is called with an empty tenantID (no filter), preserving the
// pre-fix behaviour for auth-disabled deployments.
func TestConversationStoreAdapter_SearchNoMetadataNoFilter(t *testing.T) {
	t.Parallel()

	spy := &spyConversationStore{}
	adapter := &conversationStoreAdapter{store: spy}

	// Plain background context — no RunMetadata.
	_, _ = adapter.SearchConversations(context.Background(), "query", 5)

	if spy.capturedTenantID != "" {
		t.Errorf("SearchMessages called with tenantID=%q for auth-disabled context; want empty (no filter)", spy.capturedTenantID)
	}
}
