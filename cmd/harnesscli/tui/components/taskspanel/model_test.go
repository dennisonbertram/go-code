package taskspanel_test

import (
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/taskspanel"
)

func sampleTasks() []taskspanel.TaskEntry {
	return []taskspanel.TaskEntry{
		{ID: "jm1:job_1", Type: "bash_job", Status: "running", Label: "sleep 30", AgeSeconds: 5, Actions: []string{"cancel"}},
		{ID: "sub-1", Type: "subagent", Status: "running", Label: "workspace-sub-1", AgeSeconds: 125, Actions: []string{"cancel"}},
		{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", AgeSeconds: 3780, Actions: []string{"pause", "delete"}},
		{ID: "cb-1", Type: "callback", Status: "pending", Label: "check the deploy", AgeSeconds: 42, Actions: []string{"cancel"}},
	}
}

func TestNewIsInactive(t *testing.T) {
	t.Parallel()

	m := taskspanel.New()
	if m.IsActive() {
		t.Error("New() should start inactive")
	}
	if m.Loading() {
		t.Error("New() should not be loading")
	}
	if m.TaskCount() != 0 {
		t.Errorf("New() TaskCount = %d, want 0", m.TaskCount())
	}
}

func TestOpenResetsState(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks()).MoveDown(2)
	if m.CursorIndex() != 2 {
		t.Fatalf("precondition: cursor = %d, want 2", m.CursorIndex())
	}

	reopened := m.Open()
	if !reopened.IsActive() {
		t.Error("Open() should activate the panel")
	}
	if !reopened.Loading() {
		t.Error("Open() should enter the loading state (fetch in flight)")
	}
	if reopened.CursorIndex() != 0 {
		t.Errorf("Open() should reset cursor to 0, got %d", reopened.CursorIndex())
	}
	if reopened.TaskCount() != 0 {
		t.Errorf("Open() should clear stale tasks, got %d", reopened.TaskCount())
	}
	if reopened.Err() != "" {
		t.Errorf("Open() should clear errors, got %q", reopened.Err())
	}
}

func TestCloseDeactivates(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().Close()
	if m.IsActive() {
		t.Error("Close() should deactivate the panel")
	}
}

func TestSetTasksClearsLoadingAndClampsCursor(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open()
	if !m.Loading() {
		t.Fatal("precondition: Open() must be loading")
	}

	m = m.SetTasks(sampleTasks())
	if m.Loading() {
		t.Error("SetTasks should clear the loading state")
	}
	if m.Err() != "" {
		t.Errorf("SetTasks should clear errors, got %q", m.Err())
	}
	if m.TaskCount() != len(sampleTasks()) {
		t.Fatalf("TaskCount = %d, want %d", m.TaskCount(), len(sampleTasks()))
	}

	// Cursor clamps to the last row when moving past the end.
	m = m.MoveDown(100)
	if m.CursorIndex() != len(sampleTasks())-1 {
		t.Errorf("cursor after MoveDown(100) = %d, want %d", m.CursorIndex(), len(sampleTasks())-1)
	}

	// Shrinking the list re-clamps the cursor.
	m = m.SetTasks(sampleTasks()[:1])
	if m.CursorIndex() != 0 {
		t.Errorf("cursor after shrinking SetTasks = %d, want 0", m.CursorIndex())
	}
}

func TestSetError(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetError("connection refused")
	if m.Loading() {
		t.Error("SetError should clear the loading state")
	}
	if m.Err() != "connection refused" {
		t.Errorf("Err() = %q, want %q", m.Err(), "connection refused")
	}
	if m.TaskCount() != 0 {
		t.Errorf("error state should have no tasks, got %d", m.TaskCount())
	}
}

func TestMoveUpClampsAtZero(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	m = m.MoveUp(5)
	if m.CursorIndex() != 0 {
		t.Errorf("cursor after MoveUp at top = %d, want 0", m.CursorIndex())
	}
}

func TestSelected(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open()
	if _, ok := m.Selected(); ok {
		t.Error("Selected on empty panel should be false")
	}

	m = m.SetTasks(sampleTasks())
	sel, ok := m.Selected()
	if !ok {
		t.Fatal("Selected with tasks should be true")
	}
	if sel.ID != "jm1:job_1" {
		t.Errorf("Selected ID = %q, want jm1:job_1", sel.ID)
	}

	m = m.MoveDown(1)
	sel, ok = m.Selected()
	if !ok {
		t.Fatal("Selected after MoveDown should be true")
	}
	if sel.ID != "sub-1" {
		t.Errorf("Selected ID after MoveDown = %q, want sub-1", sel.ID)
	}
}
