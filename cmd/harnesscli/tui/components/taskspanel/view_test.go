package taskspanel_test

import (
	"fmt"
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/taskspanel"
)

// TestViewRendersHeaderAndRows verifies the column header and one row per
// task with type, status, formatted age, and label.
func TestViewRendersHeaderAndRows(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	view := m.View(100, 30)

	for _, want := range []string{"Background Tasks", "TYPE", "STATUS", "AGE", "COMMAND"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// Every task contributes type, status, and label.
	for _, task := range sampleTasks() {
		for _, want := range []string{task.Type, task.Status, task.Label} {
			if !strings.Contains(view, want) {
				t.Errorf("view missing %q for task %s:\n%s", want, task.ID, view)
			}
		}
	}
	// Ages are formatted, not raw seconds.
	for _, want := range []string{"5s", "2m5s", "1h3m", "42s"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing formatted age %q:\n%s", want, view)
		}
	}
}

// TestViewEmptyState verifies the empty union renders the required message.
func TestViewEmptyState(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(nil)
	view := m.View(80, 24)
	if !strings.Contains(view, "No background tasks.") {
		t.Errorf("empty view missing 'No background tasks.':\n%s", view)
	}
}

// TestViewLoadingState verifies the in-flight fetch state.
func TestViewLoadingState(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open()
	view := m.View(80, 24)
	if !strings.Contains(view, "Loading tasks") {
		t.Errorf("loading view missing 'Loading tasks':\n%s", view)
	}
}

// TestViewErrorState verifies the fetch-error state.
func TestViewErrorState(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetError("connection refused")
	view := m.View(80, 24)
	if !strings.Contains(view, "Failed to load tasks") || !strings.Contains(view, "connection refused") {
		t.Errorf("error view missing failure text:\n%s", view)
	}
}

// TestViewCursorMarkerFollowsNavigation verifies the selected row carries a
// visible cursor marker that moves with MoveDown.
func TestViewCursorMarkerFollowsNavigation(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())

	cursorLine := func(view string) string {
		t.Helper()
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "›") {
				return line
			}
		}
		return ""
	}

	line := cursorLine(m.View(100, 30))
	if !strings.Contains(line, "sleep 30") {
		t.Errorf("cursor should start on the first row ('sleep 30'), cursor line: %q", line)
	}

	m = m.MoveDown(2)
	line = m.View(100, 30)
	for _, l := range strings.Split(line, "\n") {
		if strings.Contains(l, "›") {
			line = l
			break
		}
	}
	if !strings.Contains(line, "nightly-sync") {
		t.Errorf("cursor should be on 'nightly-sync' after MoveDown(2), cursor line: %q", line)
	}
}

// TestViewOverflowIndicators verifies long lists show ▲/▼ scroll indicators.
func TestViewOverflowIndicators(t *testing.T) {
	t.Parallel()

	tasks := make([]taskspanel.TaskEntry, 0, 30)
	for i := 0; i < 30; i++ {
		tasks = append(tasks, taskspanel.TaskEntry{
			ID:     fmt.Sprintf("jm1:job_%d", i+1),
			Type:   "bash_job",
			Status: "running",
			Label:  fmt.Sprintf("sleep %d", i+1),
		})
	}

	m := taskspanel.New().Open().SetTasks(tasks)
	view := m.View(80, 16)
	if !strings.Contains(view, "more below") {
		t.Errorf("tall list at cursor 0 should show 'more below':\n%s", view)
	}

	m = m.MoveDown(29)
	view = m.View(80, 16)
	if !strings.Contains(view, "more above") {
		t.Errorf("scrolled list should show 'more above':\n%s", view)
	}
	// The last row must be reachable and visible.
	if !strings.Contains(view, "sleep 30") {
		t.Errorf("last row should be visible after moving to the end:\n%s", view)
	}
}

// TestFormatAge covers the age column formatting contract.
func TestFormatAge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		seconds int64
		want    string
	}{
		{0, "0s"},
		{5, "5s"},
		{59, "59s"},
		{60, "1m0s"},
		{125, "2m5s"},
		{3599, "59m59s"},
		{3600, "1h0m"},
		{3780, "1h3m"},
		{90061, "25h1m"},
	}
	for _, tc := range cases {
		if got := taskspanel.FormatAge(tc.seconds); got != tc.want {
			t.Errorf("FormatAge(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

// TestViewZeroSizeDoesNotPanic verifies defensive defaults.
func TestViewZeroSizeDoesNotPanic(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	if view := m.View(0, 0); view == "" {
		t.Error("View(0,0) should render with default dimensions, got empty string")
	}
}

// --- slice 4: confirm + detail rendering ---

// TestViewConfirmPrompt verifies the destructive-action confirmation prompt
// (cron delete) names the action and the task.
func TestViewConfirmPrompt(t *testing.T) {
	t.Parallel()

	cronTask := taskspanel.TaskEntry{ID: "job-1", Type: "cron", Status: "active", Label: "nightly-sync"}
	m := taskspanel.New().Open().SetTasks([]taskspanel.TaskEntry{cronTask}).AskConfirm(cronTask)
	view := m.View(100, 30)

	for _, want := range []string{"Delete cron job", "nightly-sync", "cannot be undone", "y confirm", "n"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm view missing %q:\n%s", want, view)
		}
	}
}

// TestViewDetailMode verifies output detail rendering with title, content,
// and a back hint.
func TestViewDetailMode(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks())
	m = m.ShowOutput("sleep 30", "first line\nsecond line")
	view := m.View(100, 30)

	for _, want := range []string{"sleep 30", "first line", "second line", "back"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}

// TestViewDetailScrollIndicators verifies long output scrolls with overflow
// indicators.
func TestViewDetailScrollIndicators(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 1; i <= 40; i++ {
		if i > 1 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("output line %d", i))
	}

	m := taskspanel.New().Open().ShowOutput("big job", b.String())
	view := m.View(80, 16)
	if !strings.Contains(view, "more below") {
		t.Errorf("long output should show 'more below':\n%s", view)
	}

	m = m.ScrollDetail(35)
	view = m.View(80, 16)
	if !strings.Contains(view, "more above") {
		t.Errorf("scrolled output should show 'more above':\n%s", view)
	}
	if !strings.Contains(view, "output line 40") {
		t.Errorf("last output line should be visible after scrolling to the end:\n%s", view)
	}
}

// TestViewNoticeLine verifies action errors surface inside the panel.
func TestViewNoticeLine(t *testing.T) {
	t.Parallel()

	m := taskspanel.New().Open().SetTasks(sampleTasks()).SetNotice("kill failed: boom")
	view := m.View(100, 30)
	if !strings.Contains(view, "kill failed: boom") {
		t.Errorf("panel should render the notice line:\n%s", view)
	}
}
