package themepicker

import (
	tea "github.com/charmbracelet/bubbletea"
)

const maxVisibleRows = 10

// ThemeEntry holds display data for a single theme.
type ThemeEntry struct {
	Name    string // theme name (filename minus .json, or a built-in name)
	Builtin bool   // true for the built-in base themes (default-dark/default-light)
	Active  bool   // true for the theme currently applied to the TUI
}

// ThemeSelectedMsg is emitted when the user confirms a theme with Enter.
type ThemeSelectedMsg struct {
	Entry ThemeEntry
}

// Model is the theme picker list state machine.
// All methods return a new Model (value semantics), mirroring profilepicker.
type Model struct {
	entries      []ThemeEntry
	selected     int
	scrollOffset int
	open         bool
	Width        int
	Height       int
}

// New creates a new Model seeded with the given entries.
// The model starts closed.
func New(entries []ThemeEntry) Model {
	cp := make([]ThemeEntry, len(entries))
	copy(cp, entries)
	return Model{entries: cp}
}

// Open opens the theme picker overlay.
func (m Model) Open() Model {
	m.open = true
	return m
}

// Close closes the theme picker overlay.
func (m Model) Close() Model {
	m.open = false
	return m
}

// IsOpen reports whether the theme picker is currently visible.
func (m Model) IsOpen() bool {
	return m.open
}

// Entries returns a copy of the current entries (post re-scan).
func (m Model) Entries() []ThemeEntry {
	cp := make([]ThemeEntry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

// SetEntries replaces the entries list and resets selection and scroll.
func (m Model) SetEntries(entries []ThemeEntry) Model {
	cp := make([]ThemeEntry, len(entries))
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
func (m Model) Selected() (ThemeEntry, bool) {
	if len(m.entries) == 0 {
		return ThemeEntry{}, false
	}
	return m.entries[m.selected], true
}

// Update processes a tea.Msg and returns the updated Model plus any commands.
// Key bindings:
//   - Up / 'k' → SelectUp
//   - Down / 'j' → SelectDown
//   - Enter → emit ThemeSelectedMsg
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
			return ThemeSelectedMsg{Entry: entry}
		}

	case key.Type == tea.KeyEsc:
		return m.Close(), nil
	}

	return m, nil
}

// adjustScroll ensures the selected index is visible within the scroll window.
func adjustScroll(offset, selected, total, maxVisible int) int {
	if total <= maxVisible {
		return 0
	}
	if selected >= offset+maxVisible {
		offset = selected - maxVisible + 1
	}
	if selected < offset {
		offset = selected
	}
	maxOffset := total - maxVisible
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
