package sessionpicker

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	maxVisibleRows = 10
	lastMsgMaxLen  = 60
	titleMaxLen    = 20
)

// SessionEntry holds display data for a single past session.
type SessionEntry struct {
	ID        string    // session UUID (short display: first 8 chars)
	StartedAt time.Time // when session started
	Model     string    // LLM model used
	TurnCount int       // number of turns
	LastMsg   string    // first 60 chars of last user message
	Title     string    // optional user-assigned label; shown instead of the ID when set
}

// Model is the session picker list state machine.
// All methods return a new Model (value semantics — safe for concurrent use
// when each goroutine holds its own copy).
type Model struct {
	entries      []SessionEntry
	selected     int
	scrollOffset int
	open         bool
	Width        int
	Height       int
}

// New creates a new Model seeded with the given entries.
// The model starts closed.
func New(entries []SessionEntry) Model {
	cp := make([]SessionEntry, len(entries))
	copy(cp, entries)
	return Model{
		entries: cp,
	}
}

// Open opens the session picker overlay.
func (m Model) Open() Model {
	m.open = true
	return m
}

// Close closes the session picker overlay.
func (m Model) Close() Model {
	m.open = false
	return m
}

// IsOpen reports whether the session picker is currently visible.
func (m Model) IsOpen() bool {
	return m.open
}

// SetEntries replaces the entries list and resets selection and scroll.
func (m Model) SetEntries(entries []SessionEntry) Model {
	cp := make([]SessionEntry, len(entries))
	copy(cp, entries)
	m.entries = cp
	m.selected = 0
	m.scrollOffset = 0
	return m
}

// SelectUp moves the selection up by one, wrapping to the last entry.
func (m Model) SelectUp() Model {
	if len(m.entries) == 0 {
		return m
	}
	m.selected = (m.selected - 1 + len(m.entries)) % len(m.entries)
	m.scrollOffset = adjustScroll(m.scrollOffset, m.selected, len(m.entries), maxVisibleRows)
	return m
}

// SelectDown moves the selection down by one, wrapping to the first entry.
func (m Model) SelectDown() Model {
	if len(m.entries) == 0 {
		return m
	}
	m.selected = (m.selected + 1) % len(m.entries)
	m.scrollOffset = adjustScroll(m.scrollOffset, m.selected, len(m.entries), maxVisibleRows)
	return m
}

// Selected returns the currently selected entry.
// Returns (zero, false) when the entries list is empty.
func (m Model) Selected() (SessionEntry, bool) {
	if len(m.entries) == 0 {
		return SessionEntry{}, false
	}
	return m.entries[m.selected], true
}

// Update processes a tea.Msg and returns the updated Model plus any commands.
// Key bindings:
//   - Up / 'k' → SelectUp
//   - Down / 'j' → SelectDown
//   - Enter → emit SessionSelectedMsg
//   - Escape → Close
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.open {
		return m, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch {
	case key.Type == tea.KeyUp || (key.Type == tea.KeyRunes && string(key.Runes) == "k"):
		return m.SelectUp(), nil

	case key.Type == tea.KeyDown || (key.Type == tea.KeyRunes && string(key.Runes) == "j"):
		return m.SelectDown(), nil

	case key.Type == tea.KeyEnter:
		entry, ok := m.Selected()
		if !ok {
			return m, nil
		}
		return m, func() tea.Msg {
			return SessionSelectedMsg{Entry: entry}
		}

	case key.Type == tea.KeyEsc:
		return m.Close(), nil

	case key.Type == tea.KeyRunes && string(key.Runes) == "d":
		entry, ok := m.Selected()
		if !ok {
			return m, nil
		}
		// Remove the entry from the local list immediately so the UI refreshes.
		newEntries := make([]SessionEntry, 0, len(m.entries)-1)
		for _, e := range m.entries {
			if e.ID != entry.ID {
				newEntries = append(newEntries, e)
			}
		}
		m.entries = newEntries
		if m.selected >= len(m.entries) && m.selected > 0 {
			m.selected--
		}
		m.scrollOffset = adjustScroll(m.scrollOffset, m.selected, len(m.entries), maxVisibleRows)
		deletedID := entry.ID
		return m, func() tea.Msg {
			return SessionDeletedMsg{ID: deletedID}
		}
	}

	return m, nil
}

// adjustScroll ensures the selected index is visible within the scroll window.
func adjustScroll(offset, selected, total, maxVisible int) int {
	if total <= maxVisible {
		return 0
	}
	// Scroll down if selected is below visible window.
	if selected >= offset+maxVisible {
		offset = selected - maxVisible + 1
	}
	// Scroll up if selected is above visible window.
	if selected < offset {
		offset = selected
	}
	// Clamp offset.
	maxOffset := total - maxVisible
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
