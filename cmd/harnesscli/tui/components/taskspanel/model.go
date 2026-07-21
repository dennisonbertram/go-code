// Package taskspanel implements the /tasks overlay: a scrollable list of
// every piece of background work (bash jobs, subagents, cron jobs, delayed
// callbacks) fetched from GET /v1/tasks (epic #814). The Model mirrors the
// helpdialog component's value semantics — every method returns a new Model.
package taskspanel

// TaskEntry is one row in the panel: a single piece of background work as
// reported by GET /v1/tasks.
type TaskEntry struct {
	ID         string
	Type       string // "bash_job" | "subagent" | "cron" | "callback"
	Status     string
	Label      string
	AgeSeconds int64
	Actions    []string
}

// Model is the tasks panel state. All methods return a new Model (value
// semantics — safe for concurrent use when each goroutine holds its own copy).
type Model struct {
	tasks   []TaskEntry
	active  bool
	loading bool
	err     string
	cursor  int
	scroll  int
}

// New creates an inactive, empty panel.
func New() Model {
	return Model{}
}

// Open activates the overlay and enters the loading state, clearing any stale
// tasks, error, and cursor/scroll position so reopening /tasks always starts
// fresh at the top.
func (m Model) Open() Model {
	m.active = true
	m.loading = true
	m.err = ""
	m.tasks = nil
	m.cursor = 0
	m.scroll = 0
	return m
}

// Close deactivates the overlay.
func (m Model) Close() Model {
	m.active = false
	return m
}

// IsActive reports whether the panel is currently visible.
func (m Model) IsActive() bool {
	return m.active
}

// Loading reports whether a fetch is in flight.
func (m Model) Loading() bool {
	return m.loading
}

// Err returns the fetch error message, or "" when there is none.
func (m Model) Err() string {
	return m.err
}

// TaskCount returns the number of listed tasks.
func (m Model) TaskCount() int {
	return len(m.tasks)
}

// CursorIndex returns the selected row index (0 when empty).
func (m Model) CursorIndex() int {
	return m.cursor
}

// SetTasks replaces the task list after a successful fetch, clearing the
// loading and error states and clamping the cursor into the new list.
func (m Model) SetTasks(tasks []TaskEntry) Model {
	m.tasks = append([]TaskEntry(nil), tasks...)
	m.loading = false
	m.err = ""
	if m.cursor >= len(m.tasks) {
		m.cursor = len(m.tasks) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// SetError records a fetch failure, clearing the loading state.
func (m Model) SetError(msg string) Model {
	m.loading = false
	m.err = msg
	m.tasks = nil
	m.cursor = 0
	m.scroll = 0
	return m
}

// MoveDown moves the cursor down by n rows, clamped to the last row.
func (m Model) MoveDown(n int) Model {
	m.cursor += n
	if m.cursor >= len(m.tasks) {
		m.cursor = len(m.tasks) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// MoveUp moves the cursor up by n rows, clamped to 0.
func (m Model) MoveUp(n int) Model {
	m.cursor -= n
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// Selected returns the task under the cursor, or false when the list is empty.
func (m Model) Selected() (TaskEntry, bool) {
	if len(m.tasks) == 0 || m.cursor < 0 || m.cursor >= len(m.tasks) {
		return TaskEntry{}, false
	}
	return m.tasks[m.cursor], true
}

// View renders the panel at the given terminal dimensions. Zero dimensions
// fall back to 80x24.
func (m Model) View(width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return render(m, width, height)
}

// FormatAge renders an age in whole seconds compactly for the AGE column:
// "5s" under a minute, "2m5s" under an hour, "1h3m" beyond.
func FormatAge(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return itoa(seconds) + "s"
	}
	if seconds < 3600 {
		return itoa(seconds/60) + "m" + itoa(seconds%60) + "s"
	}
	return itoa(seconds/3600) + "h" + itoa((seconds%3600)/60) + "m"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
