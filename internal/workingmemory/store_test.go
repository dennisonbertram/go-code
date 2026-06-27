package workingmemory

import (
	"context"
	"path/filepath"
	"testing"

	om "go-agent-harness/internal/observationalmemory"
)

func TestMemoryStoreCRUDAndScopeIsolation(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	scopeA := om.ScopeKey{TenantID: "t1", ConversationID: "c1", AgentID: "a1"}
	scopeB := om.ScopeKey{TenantID: "t1", ConversationID: "c1", AgentID: "a2"}

	if err := store.Set(context.Background(), scopeA, "plan", map[string]any{"step": "collect"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(context.Background(), scopeA, "constraint", "stay in repo"); err != nil {
		t.Fatalf("Set constraint: %v", err)
	}

	got, ok, err := store.Get(context.Background(), scopeA, "plan")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected stored value")
	}
	if got == "" {
		t.Fatal("expected stored json")
	}

	if _, ok, err := store.Get(context.Background(), scopeB, "plan"); err != nil {
		t.Fatalf("Get scopeB: %v", err)
	} else if ok {
		t.Fatal("expected scope isolation")
	}

	snippet, err := store.Snippet(context.Background(), scopeA)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	if snippet == "" {
		t.Fatal("expected snippet")
	}

	if err := store.Delete(context.Background(), scopeA, "constraint"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, err := store.List(context.Background(), scopeA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
}

func TestSQLiteStoreDeleteRemovesScopedEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "working-memory.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	scope := om.ScopeKey{TenantID: "tenant", ConversationID: "conversation", AgentID: "agent"}
	if err := store.Set(ctx, scope, "next", map[string]any{"prompt": "continue"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok, err := store.Get(ctx, scope, "next"); err != nil {
		t.Fatalf("Get before delete: %v", err)
	} else if !ok {
		t.Fatal("expected entry before delete")
	}
	if err := store.Delete(ctx, scope, "next"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, ok, err := store.Get(ctx, scope, "next"); err != nil {
		t.Fatalf("Get after delete: %v", err)
	} else if ok || got != "" {
		t.Fatalf("after delete = (%q, %v), want empty false", got, ok)
	}
}
