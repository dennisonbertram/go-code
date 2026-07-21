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

// --- slice 4: confirm + detail modes, notice ---

func TestAskConfirmAndResolve(t *testing.T) {
	t.Parallel()

	cronTask := taskspanel.TaskEntry{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync"}
	m := taskspanel.New().Open().SetTasks([]taskspanel.TaskEntry{cronTask})

	m = m.AskConfirm(cronTask)
	if !m.InConfirm() {
		t.Fatal("AskConfirm should enter confirm mode")
	}
	pending, ok := m.PendingConfirm()
	if !ok {
		t.Fatal("PendingConfirm should return the task")
	}
	if pending.ID != "job-1" {
		t.Errorf("PendingConfirm ID = %q, want job-1", pending.ID)
	}

	m = m.ResolveConfirm()
	if m.InConfirm() {
		t.Error("ResolveConfirm should leave confirm mode")
	}
	if _, ok := m.PendingConfirm(); ok {
		t.Error("PendingConfirm should be empty after ResolveConfirm")
	}
	// The task list survives the confirm round-trip.
	if m.TaskCount() != 1 {
		t.Errorf("TaskCount after ResolveConfirm = %d, want 1", m.TaskCount())
	}
}

func TestShowOutputAndCloseDetail(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	m = m.ShowOutput("sleep 30", "line one\nline two")

	if !m.InDetail() {
		t.Fatal("ShowOutput should enter detail mode")
	}
	m = m.CloseDetail()
	if m.InDetail() {
		t.Error("CloseDetail should leave detail mode")
	}
	// List state is preserved.
	if m.TaskCount() != len(sampleTasks()) {
		t.Errorf("TaskCount after CloseDetail = %d, want %d", m.TaskCount(), len(sampleTasks()))
	}
	if m.CursorIndex() != 0 {
		t.Errorf("cursor should be preserved across detail mode, got %d", m.CursorIndex())
	}
}

func TestDetailScrollClamps(t *testing.T) {
	t.Parallel()

	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "row"
	}
	output := ""
	for i, l := range lines {
		if i > 0 {
			output += "\n"
		}
		output += l
	}

	m := taskspanel.New().Open().ShowOutput("big job", output)
	m = m.ScrollDetail(10)
	if got := m.DetailScroll(); got != 10 {
		t.Errorf("DetailScroll after +10 = %d, want 10", got)
	}
	m = m.ScrollDetail(-100)
	if got := m.DetailScroll(); got != 0 {
		t.Errorf("DetailScroll should clamp at 0, got %d", got)
	}
}

func TestNoticeSetAndClearedOnRefresh(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	m = m.SetNotice("Action failed: boom")
	if m.Notice() != "Action failed: boom" {
		t.Errorf("Notice() = %q, want 'Action failed: boom'", m.Notice())
	}
	m = m.SetTasks(sampleTasks())
	if m.Notice() != "" {
		t.Errorf("SetTasks should clear the notice, got %q", m.Notice())
	}
}

func TestHandleEscapeBacksOut(t *testing.T) {
	t.Parallel()

	base := taskspanel.New().Open().SetTasks(sampleTasks())

	inDetail := base.ShowOutput("x", "y")
	if escaped := inDetail.HandleEscape(); escaped.InDetail() {
		t.Error("HandleEscape should leave detail mode")
	}

	cronTask := taskspanel.TaskEntry{ID: "job-1", Type: "cron", Label: "c"}
	inConfirm := base.AskConfirm(cronTask)
	if escaped := inConfirm.HandleEscape(); escaped.InConfirm() {
		t.Error("HandleEscape should leave confirm mode")
	}

	// List mode: HandleEscape is a no-op (the model closes the overlay).
	if escaped := base.HandleEscape(); escaped.InDetail() || escaped.InConfirm() {
		t.Error("HandleEscape in list mode should be a no-op")
	}
}

func TestOpenResetsModes(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks()).
		SetNotice("boom").
		ShowOutput("x", "y")
	m = m.Open()
	if m.InDetail() || m.InConfirm() {
		t.Error("Open should reset to list mode")
	}
	if m.Notice() != "" {
		t.Error("Open should clear the notice")
	}
}
