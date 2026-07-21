package tui_test

// Tests for epic #814 slice 3: the /tasks overlay lists unified background
// tasks from GET /v1/tasks with type, status, age, and command columns, plus
// empty/loading/error states and cursor navigation. Row actions are slice 4.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func loadedTasks() []tui.RemoteTask {
	return []tui.RemoteTask{
		{ID: "jm1:job_1", Type: "bash_job", Status: "running", Label: "sleep 30", StartedAt: time.Now().UTC(), AgeSeconds: 5, Actions: []string{"cancel"}},
		{ID: "sub-1", Type: "subagent", Status: "running", Label: "workspace-sub-1", StartedAt: time.Now().UTC(), AgeSeconds: 125, Actions: []string{"cancel"}},
		{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", StartedAt: time.Now().UTC(), AgeSeconds: 3780, Actions: []string{"pause", "delete"}},
	}
}

// TestTasks_SlashCommandOpensOverlayLoading verifies /tasks opens the tasks
// overlay and shows the loading state while the fetch is in flight.
func TestTasks_SlashCommandOpensOverlayLoading(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")

	if !m.OverlayActive() || m.ActiveOverlay() != "tasks" {
		t.Fatalf("/tasks should open the tasks overlay; active=%v kind=%q", m.OverlayActive(), m.ActiveOverlay())
	}
	if view := m.View(); !strings.Contains(view, "Loading tasks") {
		t.Errorf("freshly opened /tasks should show the loading state:\n%s", view)
	}
}

// TestTasks_OverlayOpenMsgOpens verifies the overlay plumbing (OverlayOpenMsg)
// also opens the tasks panel, mirroring the help overlay.
func TestTasks_OverlayOpenMsgOpens(t *testing.T) {
	m := initModel(t, 100, 30)
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "tasks"})
	m = m2.(tui.Model)

	if !m.OverlayActive() || m.ActiveOverlay() != "tasks" {
		t.Fatalf("OverlayOpenMsg{tasks} should open the tasks overlay; active=%v kind=%q", m.OverlayActive(), m.ActiveOverlay())
	}
}

// TestTasks_LoadedRendersRows verifies TasksLoadedMsg populates the panel
// with one row per task showing type, status, age, and label.
func TestTasks_LoadedRendersRows(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")

	m2, _ := m.Update(tui.TasksLoadedMsg{Tasks: loadedTasks()})
	m = m2.(tui.Model)

	view := m.View()
	for _, want := range []string{"bash_job", "subagent", "cron", "sleep 30", "workspace-sub-1", "nightly-sync", "running", "active", "2m5s", "1h3m"} {
		if !strings.Contains(view, want) {
			t.Errorf("tasks overlay missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Loading tasks") {
		t.Error("loading state must clear once tasks arrive")
	}
}

// TestTasks_EmptyUnion verifies the empty list renders the required empty state.
func TestTasks_EmptyUnion(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")

	m2, _ := m.Update(tui.TasksLoadedMsg{Tasks: nil})
	m = m2.(tui.Model)

	if view := m.View(); !strings.Contains(view, "No background tasks.") {
		t.Errorf("empty union should render 'No background tasks.':\n%s", view)
	}
}

// TestTasks_FetchError verifies a failed fetch shows the error state and a
// status message.
func TestTasks_FetchError(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")

	m2, _ := m.Update(tui.TasksLoadFailedMsg{Err: "connection refused"})
	m = m2.(tui.Model)

	view := m.View()
	if !strings.Contains(view, "Failed to load tasks") || !strings.Contains(view, "connection refused") {
		t.Errorf("fetch failure should render the error state:\n%s", view)
	}
	if !strings.Contains(m.StatusMsg(), "Load tasks failed") {
		t.Errorf("StatusMsg = %q, want it to contain 'Load tasks failed'", m.StatusMsg())
	}
}

// TestTasks_EscCloses verifies Escape closes the overlay.
func TestTasks_EscCloses(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be open")
	}

	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("Escape should close the tasks overlay")
	}
	if m.ActiveOverlay() != "" {
		t.Errorf("ActiveOverlay after Escape = %q, want empty", m.ActiveOverlay())
	}
}

// TestTasks_CursorNavigation verifies j/k and arrow keys move the selection.
func TestTasks_CursorNavigation(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")
	m2, _ := m.Update(tui.TasksLoadedMsg{Tasks: loadedTasks()})
	m = m2.(tui.Model)

	if got := m.TasksPanelCursor(); got != 0 {
		t.Fatalf("cursor should start at 0, got %d", got)
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)
	if got := m.TasksPanelCursor(); got != 1 {
		t.Errorf("KeyDown should move cursor to 1, got %d", got)
	}

	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = m4.(tui.Model)
	if got := m.TasksPanelCursor(); got != 2 {
		t.Errorf("'j' should move cursor to 2, got %d", got)
	}

	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = m5.(tui.Model)
	if got := m.TasksPanelCursor(); got != 1 {
		t.Errorf("'k' should move cursor back to 1, got %d", got)
	}

	// The cursor row marker follows the selection.
	view := m.View()
	cursorOnSecond := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "›") {
			cursorOnSecond = strings.Contains(line, "workspace-sub-1")
			break
		}
	}
	if !cursorOnSecond {
		t.Errorf("cursor marker should be on the second row ('workspace-sub-1'):\n%s", view)
	}
}

// TestTasks_RefreshReentersLoading verifies 'r' re-enters the loading state
// and issues a new fetch command.
func TestTasks_RefreshReentersLoading(t *testing.T) {
	m := initModel(t, 100, 30)
	m = sendSlashCommand(m, "/tasks")
	m2, _ := m.Update(tui.TasksLoadedMsg{Tasks: loadedTasks()})
	m = m2.(tui.Model)

	m3, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = m3.(tui.Model)

	if view := m.View(); !strings.Contains(view, "Loading tasks") {
		t.Errorf("'r' should re-enter the loading state:\n%s", view)
	}
	if cmd == nil {
		t.Error("'r' should issue a fetch command")
	}
}

// TestTasks_SlashComplete verifies /tasks appears in autocomplete.
func TestTasks_SlashComplete(t *testing.T) {
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "/tas")
	if v := m.View(); !strings.Contains(v, "tasks") {
		t.Errorf("slash-complete must contain 'tasks' when typing '/tas'; got:\n%s", v)
	}
}

// TestTasks_HelpListsCommand verifies /help includes /tasks (help content is
// registry-driven, so this guards the registration). The command list is
// taller than the dialog, so scroll down to reveal the tail entries.
func TestTasks_HelpListsCommand(t *testing.T) {
	m := initModel(t, 120, 40)
	m = sendSlashCommand(m, "/help")
	for i := 0; i < 10; i++ {
		if strings.Contains(m.View(), "tasks") {
			return
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}
	t.Errorf("/help must list the /tasks command (after scrolling); got:\n%s", m.View())
}
