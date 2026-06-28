package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

type staticPolicy struct {
	decision ToolPolicyDecision
	err      error
}

func (s staticPolicy) Allow(_ context.Context, _ ToolPolicyInput) (ToolPolicyDecision, error) {
	return s.decision, s.err
}

func TestToolPolicyAdapterAndDangerousWrapper(t *testing.T) {
	t.Parallel()

	adapter := toolPolicyAdapter{policy: staticPolicy{decision: ToolPolicyDecision{Allow: true, Reason: "ok"}}}
	decision, err := adapter.Allow(context.Background(), htools.PolicyInput{ToolName: "bash", Action: htools.ActionExecute})
	if err != nil {
		t.Fatalf("allow adapter returned error: %v", err)
	}
	if !decision.Allow || decision.Reason != "ok" {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	errAdapter := toolPolicyAdapter{policy: staticPolicy{err: errors.New("boom")}}
	if _, err := errAdapter.Allow(context.Background(), htools.PolicyInput{}); err == nil {
		t.Fatalf("expected adapter error")
	}

	nilAdapter := toolPolicyAdapter{}
	decision, err = nilAdapter.Allow(context.Background(), htools.PolicyInput{})
	if err != nil {
		t.Fatalf("nil adapter should not error: %v", err)
	}
	if decision.Allow {
		t.Fatalf("expected zero decision for nil policy")
	}

	if !isDangerousCommand("rm -rf /") {
		t.Fatalf("expected dangerous wrapper detection")
	}
}

func TestNewDefaultRegistryWithPolicyIncludesAskUserQuestion(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistryWithPolicy(t.TempDir(), ToolApprovalModeFullAuto, nil)
	defs := registry.Definitions()
	foundAskUser := false
	foundObsMemory := false
	for _, def := range defs {
		if def.Name == "AskUserQuestion" {
			foundAskUser = true
		}
		if def.Name == "observational_memory" {
			foundObsMemory = true
		}
	}
	if !foundAskUser {
		t.Fatalf("expected AskUserQuestion in default registry")
	}
	if !foundObsMemory {
		t.Fatalf("expected observational_memory in default registry")
	}
}

func TestDefaultRegistry_RecipesDir_RegistersRunRecipe(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	recipeYAML := `
name: greet
description: "Say hello"
steps:
  - name: s1
    tool: bash
    args:
      command: "echo {{name}}"
    capture: out
`
	if err := os.WriteFile(filepath.Join(dir, "greet.yaml"), []byte(recipeYAML), 0644); err != nil {
		t.Fatal(err)
	}

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		RecipesDir: dir,
	})
	defs := registry.DeferredDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "run_recipe" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected run_recipe to be registered when RecipesDir is set with recipes")
	}
}

func TestDefaultRegistry_RecipesDir_Empty_NoRunRecipe(t *testing.T) {
	t.Parallel()

	dir := t.TempDir() // empty — no recipe files

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		RecipesDir: dir,
	})
	defs := registry.DeferredDefinitions()
	for _, def := range defs {
		if def.Name == "run_recipe" {
			t.Error("expected run_recipe NOT to be registered for empty recipes dir")
			return
		}
	}
}

func TestDefaultRegistry_RecipesDir_Missing_NoRunRecipe(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		RecipesDir: "/tmp/nonexistent-recipes-for-test-xyz",
	})
	defs := registry.DeferredDefinitions()
	for _, def := range defs {
		if def.Name == "run_recipe" {
			t.Error("expected run_recipe NOT to be registered for missing recipes dir")
			return
		}
	}
}

func TestDefaultRegistry_RecipesDir_Empty_NoRegistry(t *testing.T) {
	t.Parallel()

	// No RecipesDir set — run_recipe should not appear
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{})
	defs := registry.DeferredDefinitions()
	for _, def := range defs {
		if def.Name == "run_recipe" {
			t.Error("expected run_recipe NOT to be registered when RecipesDir is empty")
			return
		}
	}
}

// mockConvStore is a minimal ConversationStore for adapter tests.
type mockConvStore struct {
	convs []Conversation
	msgs  []MessageSearchResult
	err   error
}

func (m *mockConvStore) Migrate(_ context.Context) error { return nil }
func (m *mockConvStore) Close() error                    { return nil }
func (m *mockConvStore) SaveConversation(_ context.Context, _ string, _ []Message) error {
	return nil
}
func (m *mockConvStore) SaveConversationWithCost(_ context.Context, _ string, _ []Message, _ ConversationTokenCost) error {
	return nil
}
func (m *mockConvStore) LoadMessages(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}
func (m *mockConvStore) ListConversations(_ context.Context, _ ConversationFilter, _, _ int) ([]Conversation, error) {
	return m.convs, m.err
}
func (m *mockConvStore) DeleteConversation(_ context.Context, _ string) error { return nil }
func (m *mockConvStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *mockConvStore) SearchMessages(_ context.Context, _ string, _ string, _ int) ([]MessageSearchResult, error) {
	return m.msgs, m.err
}
func (m *mockConvStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (m *mockConvStore) PinConversation(_ context.Context, _ string, _ bool) error { return nil }
func (m *mockConvStore) CompactConversation(_ context.Context, _ string, _ int, _ Message) error {
	return nil
}
func (m *mockConvStore) GetConversationOwner(_ context.Context, _ string) (*Conversation, error) {
	return nil, nil
}

func TestConversationStoreAdapterListConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	convs := []Conversation{
		{ID: "c1", Title: "First", MsgCount: 3},
		{ID: "c2", Title: "Second", MsgCount: 7},
	}
	store := &mockConvStore{convs: convs}
	adapter := &conversationStoreAdapter{store: store}

	results, err := adapter.ListConversations(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "c1" || results[1].ID != "c2" {
		t.Errorf("unexpected results: %+v", results)
	}
	if results[0].Title != "First" {
		t.Errorf("Title = %q, want %q", results[0].Title, "First")
	}
	if results[0].MsgCount != 3 {
		t.Errorf("MsgCount = %d, want 3", results[0].MsgCount)
	}
}

func TestConversationStoreAdapterListConversations_Error(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &mockConvStore{err: errors.New("list failed")}
	adapter := &conversationStoreAdapter{store: store}

	_, err := adapter.ListConversations(ctx, 10, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestConversationStoreAdapterSearchConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	msgs := []MessageSearchResult{
		{ConversationID: "c1", Role: "user", Snippet: "hello world"},
	}
	store := &mockConvStore{msgs: msgs}
	adapter := &conversationStoreAdapter{store: store}

	results, err := adapter.SearchConversations(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("SearchConversations: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ConversationID != "c1" {
		t.Errorf("ConversationID = %q, want c1", results[0].ConversationID)
	}
	if results[0].Snippet != "hello world" {
		t.Errorf("Snippet = %q, want 'hello world'", results[0].Snippet)
	}
}

func TestConversationStoreAdapterSearchConversations_Error(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := &mockConvStore{err: errors.New("search failed")}
	adapter := &conversationStoreAdapter{store: store}

	_, err := adapter.SearchConversations(ctx, "query", 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
