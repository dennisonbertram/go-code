package goals

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "goals.db")
	st, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSQLiteStore_CreateGetRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	completed := time.Date(2026, 7, 12, 10, 30, 0, 123456789, time.UTC)
	full := &Goal{
		ID:          "g-full",
		Name:        "Full goal",
		Description: "a description",
		Status:      StatusCompleted,
		Progress:    Progress{Total: 10, Completed: 4, Percent: 40},
		DependsOn:   []string{"a", "b"},
		Blocks:      []string{"c"},
		VerifyCriteria: "all tests pass",
		Metadata:    map[string]string{"key1": "val1", "key2": "val2"},
		Result:      "done",
		Error:       "",
		CreatedAt:   time.Date(2026, 7, 12, 9, 0, 0, 111, time.UTC),
		UpdatedAt:   time.Date(2026, 7, 12, 9, 30, 0, 222, time.UTC),
		CompletedAt: &completed,
	}
	if err := st.Create(ctx, full); err != nil {
		t.Fatalf("Create full: %v", err)
	}

	got, err := st.Get(ctx, "g-full")
	if err != nil {
		t.Fatalf("Get full: %v", err)
	}
	if got.ID != full.ID || got.Name != full.Name || got.Description != full.Description {
		t.Errorf("scalar fields mismatch: got %+v", got)
	}
	if got.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", got.Status, StatusCompleted)
	}
	if got.Progress != full.Progress {
		t.Errorf("Progress = %+v, want %+v", got.Progress, full.Progress)
	}
	if len(got.DependsOn) != 2 || got.DependsOn[0] != "a" || got.DependsOn[1] != "b" {
		t.Errorf("DependsOn = %v, want [a b]", got.DependsOn)
	}
	if len(got.Blocks) != 1 || got.Blocks[0] != "c" {
		t.Errorf("Blocks = %v, want [c]", got.Blocks)
	}
	if got.VerifyCriteria != full.VerifyCriteria {
		t.Errorf("VerifyCriteria = %q, want %q", got.VerifyCriteria, full.VerifyCriteria)
	}
	if len(got.Metadata) != 2 || got.Metadata["key1"] != "val1" || got.Metadata["key2"] != "val2" {
		t.Errorf("Metadata = %v, want map[key1:val1 key2:val2]", got.Metadata)
	}
	if got.Result != "done" {
		t.Errorf("Result = %q, want done", got.Result)
	}
	if !got.CreatedAt.Equal(full.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, full.CreatedAt)
	}
	if !got.UpdatedAt.Equal(full.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, full.UpdatedAt)
	}
	if got.CompletedAt == nil {
		t.Fatalf("CompletedAt = nil, want %v", completed)
	}
	if !got.CompletedAt.Equal(completed) {
		t.Errorf("CompletedAt = %v, want %v", got.CompletedAt, completed)
	}

	// Second goal with nil CompletedAt must round-trip as nil.
	nilCompleted := &Goal{
		ID:        "g-nil",
		Name:      "No completion",
		Status:    StatusPending,
		CreatedAt: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC),
	}
	if err := st.Create(ctx, nilCompleted); err != nil {
		t.Fatalf("Create nil-completed: %v", err)
	}
	got2, err := st.Get(ctx, "g-nil")
	if err != nil {
		t.Fatalf("Get nil-completed: %v", err)
	}
	if got2.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want nil", got2.CompletedAt)
	}
}

func TestSQLiteStore_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.Get(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Get missing err = %v, want contains 'not found'", err)
	}
	if err := st.Update(ctx, &Goal{ID: "missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Update missing err = %v, want contains 'not found'", err)
	}
	if err := st.Delete(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Delete missing err = %v, want contains 'not found'", err)
	}
}

func TestSQLiteStore_UpdatePersists(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	g := &Goal{
		ID:        "g1",
		Name:      "original",
		Status:    StatusPending,
		CreatedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
	}
	if err := st.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	g.Name = "changed"
	g.Status = StatusRunning
	g.Result = "partial"
	g.Progress = Progress{Total: 4, Completed: 2, Percent: 50}
	if err := st.Update(ctx, g); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := st.Get(ctx, "g1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "changed" || got.Status != StatusRunning || got.Result != "partial" {
		t.Errorf("update not persisted: got %+v", got)
	}
	if got.Progress != (Progress{Total: 4, Completed: 2, Percent: 50}) {
		t.Errorf("Progress = %+v, want {4 2 50}", got.Progress)
	}
}

func TestSQLiteStore_List(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	goals := []*Goal{
		{ID: "1", Name: "charlie", Status: StatusPending, CreatedAt: base, UpdatedAt: base},
		{ID: "2", Name: "alpha", Status: StatusRunning, CreatedAt: base.Add(time.Minute), UpdatedAt: base.Add(time.Minute)},
		{ID: "3", Name: "bravo", Status: StatusPending, CreatedAt: base.Add(2 * time.Minute), UpdatedAt: base.Add(2 * time.Minute)},
	}
	for _, g := range goals {
		if err := st.Create(ctx, g); err != nil {
			t.Fatalf("Create %s: %v", g.ID, err)
		}
	}

	// Status filter returns only matching.
	pending, err := st.List(ctx, GoalFilter{Status: StatusPending})
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending count = %d, want 2", len(pending))
	}
	for _, g := range pending {
		if g.Status != StatusPending {
			t.Errorf("got status %q in pending filter", g.Status)
		}
	}

	// Empty filter returns all.
	all, err := st.List(ctx, GoalFilter{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all count = %d, want 3", len(all))
	}

	// No match returns empty (not error).
	failed, err := st.List(ctx, GoalFilter{Status: StatusFailed})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("failed count = %d, want 0", len(failed))
	}

	// Sort by name descending: charlie, bravo, alpha.
	byNameDesc, err := st.List(ctx, GoalFilter{SortBy: "name", SortDesc: true})
	if err != nil {
		t.Fatalf("List name desc: %v", err)
	}
	wantNames := []string{"charlie", "bravo", "alpha"}
	if len(byNameDesc) != 3 {
		t.Fatalf("byNameDesc count = %d, want 3", len(byNameDesc))
	}
	for i, want := range wantNames {
		if byNameDesc[i].Name != want {
			t.Errorf("byNameDesc[%d].Name = %q, want %q", i, byNameDesc[i].Name, want)
		}
	}

	// Sort by name ascending with Limit=1, Offset=1: skip alpha, take bravo.
	page, err := st.List(ctx, GoalFilter{SortBy: "name", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("List page: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page count = %d, want 1", len(page))
	}
	if page[0].Name != "bravo" {
		t.Errorf("page[0].Name = %q, want bravo", page[0].Name)
	}

	// Default sort is created_at ascending: charlie, alpha, bravo.
	byCreated, err := st.List(ctx, GoalFilter{})
	if err != nil {
		t.Fatalf("List default: %v", err)
	}
	wantOrder := []string{"charlie", "alpha", "bravo"}
	for i, want := range wantOrder {
		if byCreated[i].Name != want {
			t.Errorf("byCreated[%d].Name = %q, want %q", i, byCreated[i].Name, want)
		}
	}
}

func TestSQLiteStore_PersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "goals.db")

	st, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	g := &Goal{
		ID:        "persist",
		Name:      "survives restart",
		Status:    StatusRunning,
		Metadata:  map[string]string{"a": "1"},
		CreatedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
	}
	if err := st.Create(ctx, g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, err := reopened.Get(ctx, "persist")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "survives restart" || got.Status != StatusRunning {
		t.Errorf("reopened goal mismatch: %+v", got)
	}
	if got.Metadata["a"] != "1" {
		t.Errorf("Metadata not persisted: %v", got.Metadata)
	}
}
