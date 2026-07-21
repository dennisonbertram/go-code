package tui_test

// Tests for epic #814 slice 3: the /tasks overlay lists unified background
// tasks from GET /v1/tasks with type, status, age, and command columns, plus
// empty/loading/error states and cursor navigation. Row actions are slice 4.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// --- slice 4: view output + stop actions ---

// initModelWithURL builds a model pointed at the given harnessd test server.
func initModelWithURL(t *testing.T, w, h int, baseURL string) tui.Model {
	t.Helper()
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = baseURL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m2.(tui.Model)
}

// execCmds runs a (possibly batched) tea.Cmd tree and collects the messages.
func execCmds(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, execCmds(t, c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// openTasksWithRows opens /tasks and loads the given rows.
func openTasksWithRows(t *testing.T, m tui.Model, tasks []tui.RemoteTask) tui.Model {
	t.Helper()
	m = sendSlashCommand(m, "/tasks")
	if !m.OverlayActive() || m.ActiveOverlay() != "tasks" {
		t.Fatalf("precondition: tasks overlay must be open (active=%v kind=%q)", m.OverlayActive(), m.ActiveOverlay())
	}
	m2, _ := m.Update(tui.TasksLoadedMsg{Tasks: tasks})
	return m2.(tui.Model)
}

// TestTasks_ViewOutputBashJob verifies 'o' on a bash job fetches its output
// and shows the detail view; 'h' returns to the list.
func TestTasks_ViewOutputBashJob(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/jm1:job_1/output" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"shell_id": "job_1", "running": true, "output": "partial output here"})
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "jm1:job_1", Type: "bash_job", Status: "running", Label: "sleep 30", AgeSeconds: 5, Actions: []string{"cancel"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = m2.(tui.Model)
	msgs := execCmds(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("'o' should issue exactly one fetch cmd, got %d msgs: %v", len(msgs), msgs)
	}
	loaded, ok := msgs[0].(tui.TaskOutputLoadedMsg)
	if !ok {
		t.Fatalf("expected TaskOutputLoadedMsg, got %T", msgs[0])
	}

	m3, _ := m.Update(loaded)
	m = m3.(tui.Model)
	view := m.View()
	if !strings.Contains(view, "partial output here") {
		t.Errorf("detail view should show the job output:\n%s", view)
	}

	// 'h' returns to the list.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = m4.(tui.Model)
	if view := m.View(); !strings.Contains(view, "COMMAND") {
		t.Errorf("'h' should return to the task list:\n%s", view)
	}
}

// TestTasks_ViewOutputCronStaticDetail verifies 'o' on a cron row shows the
// task's own details without any fetch.
func TestTasks_ViewOutputCronStaticDetail(t *testing.T) {
	m := initModel(t, 100, 30)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", AgeSeconds: 3780, Actions: []string{"pause", "delete"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = m2.(tui.Model)
	if msgs := execCmds(t, cmd); len(msgs) != 0 {
		t.Errorf("cron detail should not issue a fetch, got %v", msgs)
	}
	view := m.View()
	if !strings.Contains(view, "Output: nightly-sync") {
		t.Errorf("cron 'o' should open the detail view titled 'Output: nightly-sync':\n%s", view)
	}
	if !strings.Contains(view, "cron") || !strings.Contains(view, "active") {
		t.Errorf("cron detail should show the task's own info:\n%s", view)
	}
}

// TestTasks_EnterAlsoOpensOutput verifies Enter is an alias for 'o'.
func TestTasks_EnterAlsoOpensOutput(t *testing.T) {
	m := initModel(t, 100, 30)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "cb-1", Type: "callback", Status: "pending", Label: "check deploy", AgeSeconds: 42, Actions: []string{"cancel"}},
	})

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)
	if view := m.View(); !strings.Contains(view, "Output: check deploy") {
		t.Errorf("Enter should open the detail view:\n%s", view)
	}
}

// TestTasks_StopBashJobKillsAndRefreshes verifies 'x' on a running bash job
// calls the kill endpoint and the completed action triggers a list refresh.
func TestTasks_StopBashJobKillsAndRefreshes(t *testing.T) {
	killCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jobs/jm1:job_1/kill" && r.Method == http.MethodPost {
			killCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "jm1:job_1", "killed": true})
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "jm1:job_1", Type: "bash_job", Status: "running", Label: "sleep 30", AgeSeconds: 5, Actions: []string{"cancel"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)
	msgs := execCmds(t, cmd)
	if !killCalled {
		t.Fatal("'x' on a bash job must call POST /v1/jobs/{id}/kill")
	}
	// The action result feeds back into the model and triggers a refresh cmd.
	var result tui.TaskActionResultMsg
	for _, msg := range msgs {
		if r, ok := msg.(tui.TaskActionResultMsg); ok {
			result = r
		}
	}
	m3, refreshCmd := m.Update(result)
	m = m3.(tui.Model)
	if result.Err != "" {
		t.Fatalf("kill result.Err = %q, want empty", result.Err)
	}
	if refreshCmd == nil {
		t.Error("successful stop should trigger a list refresh")
	}
}

// TestTasks_StopCronRequiresConfirm verifies 'x' on a cron job shows the
// confirmation prompt and only fires DELETE after 'y'.
func TestTasks_StopCronRequiresConfirm(t *testing.T) {
	deleteCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	cronRow := tui.RemoteTask{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", AgeSeconds: 100, Actions: []string{"pause", "delete"}}
	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{cronRow})

	// 'x' shows the confirm prompt; nothing is deleted yet.
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)
	if msgs := execCmds(t, cmd); len(msgs) != 0 {
		t.Errorf("cron stop must wait for confirmation, got msgs %v", msgs)
	}
	if deleteCalled {
		t.Fatal("DELETE fired before confirmation")
	}
	if view := m.View(); !strings.Contains(view, "Delete cron job") || !strings.Contains(view, "nightly-sync") {
		t.Errorf("confirm prompt should name the cron job:\n%s", view)
	}

	// 'n' cancels — back to the list, still no DELETE.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m3.(tui.Model)
	if deleteCalled {
		t.Fatal("DELETE fired after 'n'")
	}
	if view := m.View(); !strings.Contains(view, "COMMAND") {
		t.Errorf("'n' should return to the list:\n%s", view)
	}

	// 'x' then 'y' fires the DELETE.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m4.(tui.Model)
	m5, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = m5.(tui.Model)
	execCmds(t, cmd)
	if !deleteCalled {
		t.Error("DELETE did not fire after 'y' confirmation")
	}
	if view := m.View(); !strings.Contains(view, "COMMAND") {
		t.Errorf("after confirming, the panel should return to the list:\n%s", view)
	}
}

// TestTasks_StopSubagentCancels verifies 'x' on a subagent cancels it
// immediately (no confirmation).
func TestTasks_StopSubagentCancels(t *testing.T) {
	cancelCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/subagents/sub-1/cancel" && r.Method == http.MethodPost {
			cancelCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sub-1","status":"cancelling"}`))
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "sub-1", Type: "subagent", Status: "running", Label: "workspace-sub-1", AgeSeconds: 9, Actions: []string{"cancel"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)
	execCmds(t, cmd)
	if !cancelCalled {
		t.Error("'x' on a subagent must call POST /v1/subagents/{id}/cancel")
	}
}

// TestTasks_StopCallbackCancels verifies 'x' on a callback cancels it.
func TestTasks_StopCallbackCancels(t *testing.T) {
	cancelCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/callbacks/cb-1/cancel" && r.Method == http.MethodPost {
			cancelCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cb-1","status":"canceled"}`))
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "cb-1", Type: "callback", Status: "pending", Label: "check deploy", AgeSeconds: 42, Actions: []string{"cancel"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)
	execCmds(t, cmd)
	if !cancelCalled {
		t.Error("'x' on a callback must call POST /v1/callbacks/{id}/cancel")
	}
}

// TestTasks_ActionErrorSurfacesInPanel verifies a failed stop action renders
// the error inside the panel (not just the status line).
func TestTasks_ActionErrorSurfacesInPanel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "jm1:job_9", Type: "bash_job", Status: "running", Label: "sleep 30", AgeSeconds: 5, Actions: []string{"cancel"}},
	})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)
	for _, msg := range execCmds(t, cmd) {
		m3, _ := m.Update(msg)
		m = m3.(tui.Model)
	}

	if view := m.View(); !strings.Contains(view, "server returned 404") {
		t.Errorf("panel should surface the action error:\n%s", view)
	}
	if !strings.Contains(m.StatusMsg(), "Task stop failed") {
		t.Errorf("StatusMsg = %q, want it to contain 'Task stop failed'", m.StatusMsg())
	}
}

// TestTasks_EscInDetailReturnsToList verifies Escape backs out of the detail
// view to the list instead of closing the overlay.
func TestTasks_EscInDetailReturnsToList(t *testing.T) {
	m := initModel(t, 100, 30)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", AgeSeconds: 100, Actions: []string{"delete"}},
	})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = m2.(tui.Model)
	if view := m.View(); !strings.Contains(view, "nightly-sync") {
		t.Fatalf("precondition: detail view must be open:\n%s", view)
	}

	m, _ = sendEscape(m)
	if !m.OverlayActive() || m.ActiveOverlay() != "tasks" {
		t.Fatal("Escape in detail mode should not close the tasks overlay")
	}
	if view := m.View(); !strings.Contains(view, "COMMAND") {
		t.Errorf("Escape in detail mode should return to the list:\n%s", view)
	}

	// A second Escape closes the overlay.
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("second Escape should close the tasks overlay")
	}
}

// TestTasks_EscInConfirmCancels verifies Escape dismisses the cron delete
// confirmation without firing.
func TestTasks_EscInConfirmCancels(t *testing.T) {
	deleteCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalled = true
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	m := initModelWithURL(t, 100, 30, ts.URL)
	m = openTasksWithRows(t, m, []tui.RemoteTask{
		{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync", AgeSeconds: 100, Actions: []string{"delete"}},
	})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(tui.Model)

	m, _ = sendEscape(m)
	if deleteCalled {
		t.Error("Escape on the confirm prompt must not fire the DELETE")
	}
	if !m.OverlayActive() || m.ActiveOverlay() != "tasks" {
		t.Error("Escape on the confirm prompt should return to the list, not close the overlay")
	}
	if view := m.View(); !strings.Contains(view, "COMMAND") {
		t.Errorf("Escape on the confirm prompt should return to the list:\n%s", view)
	}
}
