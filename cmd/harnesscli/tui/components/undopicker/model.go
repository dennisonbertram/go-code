// Package undopicker implements the overlay shown by bare /undo (epic #805
// slice 4): the most recent user prompts, newest first, so the user can pick
// the prompt to undo back to. Prompts at or below the most recent compaction
// summary are shown disabled — undoing across a compaction is refused by the
// store, so the picker never offers it.
package undopicker

import (
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// maxEntries caps how many prompts the picker lists (the K newest).
	maxEntries = 10
	// maxVisibleRows bounds the rendered window; longer lists scroll.
	maxVisibleRows = 10
)

// MessageView is the subset of a conversation message the picker needs.
type MessageView struct {
	Role             string
	Content          string
	IsMeta           bool
	IsCompactSummary bool
}

// UndoEntry is one selectable prompt in the picker.
type UndoEntry struct {
	Step     int    // index of the prompt in the fetched history (== store step)
	Count    int    // prompts the server must drop to undo back here (1 = newest)
	Preview  string // first line of the prompt, for display
	Disabled bool   // at/below the compaction boundary — cannot be undone
}

// EntriesFromMessages converts fetched conversation history into picker
// entries, newest first. Only non-meta user messages are prompts. Messages at
// or below the most recent is_compact_summary row are marked Disabled, since
// the server refuses to undo across a compaction boundary. Index in the slice
// equals the store step (the store keeps steps contiguous from 0).
func EntriesFromMessages(msgs []MessageView) []UndoEntry {
	boundary := -1
	for i, m := range msgs {
		if m.IsCompactSummary {
			boundary = i
		}
	}
	var entries []UndoEntry
	count := 0
	for i := len(msgs) - 1; i >= 0 && len(entries) < maxEntries; i-- {
		m := msgs[i]
		if m.Role != "user" || m.IsMeta {
			continue
		}
		count++
		entries = append(entries, UndoEntry{
			Step:     i,
			Count:    count,
			Preview:  firstLine(m.Content),
			Disabled: i <= boundary,
		})
	}
	return entries
}

// firstLine returns s up to the first newline, trimmed of trailing space.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// Model is the undo picker list state machine.
// All methods return a new Model (value semantics), mirroring sessionpicker.
type Model struct {
	entries      []UndoEntry
	selected     int
	scrollOffset int
	open         bool
	Width        int
	Height       int
}

// New creates a closed Model seeded with the given entries (newest first).
// The initial selection is the first enabled entry.
func New(entries []UndoEntry) Model {
	cp := make([]UndoEntry, len(entries))
	copy(cp, entries)
	m := Model{entries: cp}
	for i, e := range cp {
		if !e.Disabled {
			m.selected = i
			break
		}
	}
	return m
}

// Open opens the picker overlay.
func (m Model) Open() Model {
	m.open = true
	return m
}

// Close closes the picker overlay.
func (m Model) Close() Model {
	m.open = false
	return m
}

// IsOpen reports whether the picker is currently visible.
func (m Model) IsOpen() bool {
	return m.open
}

// Entries returns the picker entries (newest first).
func (m Model) Entries() []UndoEntry {
	cp := make([]UndoEntry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

// Selected returns the currently selected entry.
// Returns (zero, false) when the entries list is empty.
func (m Model) Selected() (UndoEntry, bool) {
	if len(m.entries) == 0 {
		return UndoEntry{}, false
	}
	return m.entries[m.selected], true
}

// SelectUp moves the selection to the previous enabled entry, wrapping.
func (m Model) SelectUp() Model {
	return m.move(-1)
}

// SelectDown moves the selection to the next enabled entry, wrapping.
func (m Model) SelectDown() Model {
	return m.move(1)
}

// move advances the selection by delta, skipping disabled rows.
func (m Model) move(delta int) Model {
	n := len(m.entries)
	if n == 0 {
		return m
	}
	for i := 0; i < n; i++ {
		m.selected = (m.selected + delta + n) % n
		if !m.entries[m.selected].Disabled {
			break
		}
	}
	m.scrollOffset = adjustScroll(m.scrollOffset, m.selected, n, maxVisibleRows)
	return m
}

// Update processes a tea.Msg and returns the updated Model plus any commands.
// Key bindings:
//   - Up / 'k' → SelectUp
//   - Down / 'j' → SelectDown
//   - Enter → emit UndoSelectedMsg for enabled entries only
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
		if !ok || entry.Disabled {
			return m, nil
		}
		return m, func() tea.Msg {
			return UndoSelectedMsg{Entry: entry}
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
