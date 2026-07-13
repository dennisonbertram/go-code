package deferred

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tools "go-agent-harness/internal/harness/tools"
)

// TestNewTodoStore_ToolAndManagerShareStore is the regression test for the wiring
// fix: a write performed through the LLM-facing tool handler (run ID taken from
// context) must be visible through the TodoManager handed to the HTTP layer, and
// vice-versa. Before the fix, TodosTool() built an isolated store so the
// /v1/runs/{id}/todos route never saw tool writes.
func TestNewTodoStore_ToolAndManagerShareStore(t *testing.T) {
	t.Parallel()

	mgr, toolFn := NewTodoStore()
	tool := toolFn()
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunID, "run-shared")

	// Write via the tool handler; read via the manager (HTTP path).
	_, err := tool.Handler(ctx, json.RawMessage(`{"action":"set","todos":[{"id":"1","text":"from tool","status":"pending"}]}`))
	if err != nil {
		t.Fatalf("tool set: %v", err)
	}
	got := mgr.GetTodos("run-shared")
	if len(got) != 1 || got[0].Text != "from tool" {
		t.Fatalf("manager did not see tool write; got %+v", got)
	}

	// Write via the manager (HTTP path); read via the tool handler (get action).
	if err := mgr.SetTodos("run-shared", []TodoItem{{ID: "2", Text: "from http", Status: "completed"}}); err != nil {
		t.Fatalf("manager set: %v", err)
	}
	out, err := tool.Handler(ctx, json.RawMessage(`{"action":"get"}`))
	if err != nil {
		t.Fatalf("tool get: %v", err)
	}
	if want := "from http"; !strings.Contains(out, want) {
		t.Fatalf("tool did not see manager write; got %s", out)
	}

	// Runs are isolated by run ID.
	if other := mgr.GetTodos("run-other"); len(other) != 0 {
		t.Fatalf("expected run isolation, got %+v", other)
	}
}

func TestNewTodoStore_ReturnsManagerAndToolFactory(t *testing.T) {
	t.Parallel()

	mgr, toolFn := NewTodoStore()
	if mgr == nil {
		t.Fatal("NewTodoStore returned nil manager")
	}
	if toolFn == nil {
		t.Fatal("NewTodoStore returned nil tool factory")
	}
	tool := toolFn()
	if tool.Definition.Name != "todos" {
		t.Errorf("expected tool name %q, got %q", "todos", tool.Definition.Name)
	}
}

func TestTodoStore_GetTodos_EmptyForUnknownRunID(t *testing.T) {
	t.Parallel()

	mgr, _ := NewTodoStore()
	todos := mgr.GetTodos("unknown-run")
	if todos == nil {
		t.Fatal("GetTodos returned nil, expected empty slice")
	}
	if len(todos) != 0 {
		t.Errorf("expected 0 todos, got %d", len(todos))
	}
}

func TestTodoStore_SetAndGetTodos(t *testing.T) {
	t.Parallel()

	mgr, _ := NewTodoStore()
	items := []TodoItem{
		{ID: "1", Text: "Write tests", Status: "pending"},
		{ID: "2", Text: "Ship it", Status: "in_progress"},
	}
	if err := mgr.SetTodos("run-1", items); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}

	got := mgr.GetTodos("run-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(got))
	}
	if got[0].Text != "Write tests" {
		t.Errorf("todo[0].Text = %q, want %q", got[0].Text, "Write tests")
	}
	if got[1].Status != "in_progress" {
		t.Errorf("todo[1].Status = %q, want %q", got[1].Status, "in_progress")
	}
}

func TestTodoStore_SetTodos_NormalizesEmptyStatus(t *testing.T) {
	t.Parallel()

	mgr, _ := NewTodoStore()
	items := []TodoItem{{Text: "no status"}}
	if err := mgr.SetTodos("run-1", items); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}
	got := mgr.GetTodos("run-1")
	if got[0].Status != "pending" {
		t.Errorf("expected status %q, got %q", "pending", got[0].Status)
	}
}

func TestTodoStore_SetTodos_RejectsInvalidStatus(t *testing.T) {
	t.Parallel()

	mgr, _ := NewTodoStore()
	items := []TodoItem{{Text: "bad", Status: "bogus"}}
	if err := mgr.SetTodos("run-1", items); err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}

func TestTodoStore_GetTodos_ReturnsCopy(t *testing.T) {
	t.Parallel()

	mgr, _ := NewTodoStore()
	items := []TodoItem{{Text: "task", Status: "pending"}}
	if err := mgr.SetTodos("run-1", items); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}

	// Mutate the returned slice and verify the store is unaffected.
	got := mgr.GetTodos("run-1")
	got[0].Text = "mutated"

	got2 := mgr.GetTodos("run-1")
	if got2[0].Text == "mutated" {
		t.Error("GetTodos returned a reference to the internal slice, not a copy")
	}
}
