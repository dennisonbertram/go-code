package slashcomplete

// Suggestion is a completion candidate.
type Suggestion struct {
	Name        string
	Description string
}

// Model is the autocomplete dropdown state machine.
// All methods return a new Model (value semantics — safe for concurrent use
// when each goroutine holds its own copy).
type Model struct {
	suggestions  []Suggestion
	filtered     []Suggestion // current filtered+ranked results
	query        string       // current filter query (without leading /)
	selected     int          // cursor index in filtered
	scrollOffset int          // index of the first visible item in the scroll window
	active       bool
	maxVisible   int // max rows to show (default 8)
}

// New creates a new Model seeded with the given suggestions.
// The model starts inactive (overlay hidden).
func New(suggestions []Suggestion) Model {
	cp := make([]Suggestion, len(suggestions))
	copy(cp, suggestions)
	m := Model{
		suggestions: cp,
		maxVisible:  8,
	}
	m.filtered = FuzzyFilter(m.suggestions, "")
	return m
}

// Open activates the dropdown overlay.
func (m Model) Open() Model {
	m.active = true
	return m
}

// Close deactivates the dropdown overlay.
func (m Model) Close() Model {
	m.active = false
	return m
}

// IsActive reports whether the dropdown is currently visible.
func (m Model) IsActive() bool {
	return m.active
}

// ScrollOffset returns the index of the first item in the current scroll window.
// This is exported for testing purposes.
func (m Model) ScrollOffset() int {
	return m.scrollOffset
}

// clampScrollWindow adjusts scrollOffset so that m.selected is always within
// the visible window [scrollOffset, scrollOffset+maxVisible).
func (m Model) clampScrollWindow() Model {
	maxVis := m.maxVisible
	if maxVis <= 0 {
		maxVis = 8
	}
	n := len(m.filtered)
	if n == 0 {
		m.scrollOffset = 0
		return m
	}
	// Scroll down if selected has moved past the bottom of the window.
	if m.selected >= m.scrollOffset+maxVis {
		m.scrollOffset = m.selected - maxVis + 1
	}
	// Scroll up if selected has moved above the top of the window.
	if m.selected < m.scrollOffset {
		m.scrollOffset = m.selected
	}
	// Clamp offset to valid range.
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset > n-1 {
		m.scrollOffset = n - 1
	}
	return m
}

// SetQuery updates the filter query and resets the cursor to position 0.
// query should NOT include the leading '/'.
// Uses FuzzyFilter for ranked, fuzzy matching.
func (m Model) SetQuery(query string) Model {
	m.query = query
	if query == "" {
		m.filtered = FuzzyFilter(m.suggestions, "")
	} else {
		m.filtered = FuzzyFilter(m.suggestions, query)
	}
	m.selected = 0
	m.scrollOffset = 0
	return m
}

// Down moves the cursor down by one, wrapping to the top when past the last item.
func (m Model) Down() Model {
	if len(m.filtered) == 0 {
		return m
	}
	m.selected = (m.selected + 1) % len(m.filtered)
	return m.clampScrollWindow()
}

// Up moves the cursor up by one, wrapping to the bottom when past the first item.
func (m Model) Up() Model {
	if len(m.filtered) == 0 {
		return m
	}
	m.selected = (m.selected - 1 + len(m.filtered)) % len(m.filtered)
	return m.clampScrollWindow()
}

// Selected returns the currently highlighted suggestion.
// Returns (zero, false) when the filtered list is empty.
func (m Model) Selected() (Suggestion, bool) {
	if len(m.filtered) == 0 {
		return Suggestion{}, false
	}
	return m.filtered[m.selected], true
}

// Filtered returns a copy of the current filtered results.
func (m Model) Filtered() []Suggestion {
	cp := make([]Suggestion, len(m.filtered))
	copy(cp, m.filtered)
	return cp
}

// Accept selects the current suggestion, closes the dropdown, and returns
// the completed text (e.g. "/clear ").
// If there are no filtered results it returns ("", closed model).
func (m Model) Accept() (Model, string) {
	s, ok := m.Selected()
	m.active = false
	if !ok {
		return m, ""
	}
	return m, "/" + s.Name + " "
}
