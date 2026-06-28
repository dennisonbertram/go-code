package viewport

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Model is the scrollable viewport for conversation content.
type Model struct {
	width           int
	height          int
	lines           []string
	offset          int // lines from the bottom (0 = at bottom)
	autoScroll      bool
	lastLen         int // tracks when new content arrives while scrolled up
	newContentCount int // lines added while scrolled up
	maxHistory      int // 0 = unlimited
}

// New creates a viewport with given dimensions.
func New(width, height int) Model {
	return Model{width: width, height: height, autoScroll: true}
}

// AppendLine adds a line to the viewport.
// If auto-scroll is enabled, the viewport stays at the bottom.
// If maxHistory > 0 and len(lines) exceeds maxHistory, the oldest lines
// are pruned and offset is adjusted to stay within range.
func (m *Model) AppendLine(line string) {
	m.lines = append(m.lines, line)

	// Prune history if maxHistory is set.
	if m.maxHistory > 0 && len(m.lines) > m.maxHistory {
		dropped := len(m.lines) - m.maxHistory
		m.lines = m.lines[dropped:]
		// offset is from-the-bottom, so dropping from the front
		// doesn't affect it directly — but we clamp below just in case.
	}

	if m.autoScroll {
		m.offset = 0
		m.newContentCount = 0
	} else {
		m.newContentCount++
		// Clamp offset so it stays within valid scrollable range.
		total := len(m.lines)
		maxOff := total - m.height
		if maxOff < 0 {
			maxOff = 0
		}
		if m.offset > maxOff {
			m.offset = maxOff
		}
	}
}

// AppendChunk appends text to the last line in the viewport without adding
// a newline. Use this for streaming token output where each delta should
// accumulate on the same line. If the viewport is empty, it starts a new line.
// If chunk contains embedded newline characters, the content is split and each
// segment after a newline begins on a new viewport line.
func (m *Model) AppendChunk(chunk string) {
	if len(chunk) == 0 {
		return
	}
	parts := strings.Split(chunk, "\n")
	if len(m.lines) == 0 {
		m.lines = append(m.lines, parts[0])
	} else {
		m.lines[len(m.lines)-1] += parts[0]
	}
	for _, part := range parts[1:] {
		m.lines = append(m.lines, part)
	}
	if m.autoScroll {
		m.offset = 0
		m.newContentCount = 0
	}
}

// AppendLines adds multiple lines.
func (m *Model) AppendLines(lines []string) {
	for _, l := range lines {
		m.AppendLine(l)
	}
}

// ReplaceTailLines replaces the last count lines with the provided lines.
// It preserves the viewport's scroll semantics the same way SetContent does.
func (m *Model) ReplaceTailLines(count int, lines []string) {
	if count < 0 {
		count = 0
	}
	if count > len(m.lines) {
		count = len(m.lines)
	}

	prefixLen := len(m.lines) - count
	next := make([]string, 0, prefixLen+len(lines))
	next = append(next, m.lines[:prefixLen]...)
	next = append(next, lines...)
	m.lines = next

	if m.maxHistory > 0 && len(m.lines) > m.maxHistory {
		dropped := len(m.lines) - m.maxHistory
		m.lines = m.lines[dropped:]
	}

	if m.autoScroll {
		m.offset = 0
		m.newContentCount = 0
		return
	}

	maxOff := len(m.lines) - m.height
	if maxOff < 0 {
		maxOff = 0
	}
	if m.offset > maxOff {
		m.offset = maxOff
	}
}

// ReplaceLineRange replaces lines[start : start+count] with the provided lines.
// start and count are clamped to valid ranges. If count is zero, lines are
// inserted at start. Autoscroll and offset semantics are preserved the same
// way as ReplaceTailLines. Use this for in-place updates to arbitrary viewport
// segments (e.g. updating a tool card that is not at the tail).
func (m *Model) ReplaceLineRange(start, count int, lines []string) {
	if start < 0 {
		start = 0
	}
	if start > len(m.lines) {
		start = len(m.lines)
	}
	if count < 0 {
		count = 0
	}
	if start+count > len(m.lines) {
		count = len(m.lines) - start
	}

	end := start + count
	next := make([]string, 0, start+len(lines)+(len(m.lines)-end))
	next = append(next, m.lines[:start]...)
	next = append(next, lines...)
	next = append(next, m.lines[end:]...)
	m.lines = next

	if m.maxHistory > 0 && len(m.lines) > m.maxHistory {
		dropped := len(m.lines) - m.maxHistory
		m.lines = m.lines[dropped:]
	}

	if m.autoScroll {
		m.offset = 0
		m.newContentCount = 0
		return
	}

	maxOff := len(m.lines) - m.height
	if maxOff < 0 {
		maxOff = 0
	}
	if m.offset > maxOff {
		m.offset = maxOff
	}
}

// LineCount returns the total number of lines currently in the viewport.
// This is used by callers that track per-card start offsets.
func (m Model) LineCount() int {
	return len(m.lines)
}

// SetContent replaces all lines (e.g., for re-render of last message).
// If the new content is shorter than the current offset, the offset is
// clamped so it cannot exceed the maximum scrollable range.
func (m *Model) SetContent(content string) {
	m.lines = strings.Split(content, "\n")
	if m.autoScroll {
		m.offset = 0
	} else {
		// Clamp offset so it stays within valid range of new content.
		maxOff := len(m.lines) - m.height
		if maxOff < 0 {
			maxOff = 0
		}
		if m.offset > maxOff {
			m.offset = maxOff
		}
	}
}

// ScrollUp scrolls up by n lines and disables auto-scroll.
func (m *Model) ScrollUp(n int) {
	m.autoScroll = false
	maxOff := len(m.lines) - m.height
	if maxOff < 0 {
		maxOff = 0
	}
	m.offset += n
	if m.offset > maxOff {
		m.offset = maxOff
	}
	m.lastLen = len(m.lines)
}

// ScrollDown scrolls down by n lines. Re-enables auto-scroll if reaching the bottom.
func (m *Model) ScrollDown(n int) {
	m.offset -= n
	if m.offset < 0 {
		m.offset = 0
		m.autoScroll = true
	}
}

// ScrollToBottom jumps to the bottom and re-enables auto-scroll.
func (m *Model) ScrollToBottom() {
	m.offset = 0
	m.autoScroll = true
	m.lastLen = len(m.lines)
	m.newContentCount = 0
}

// AtBottom reports whether viewport is at the bottom.
func (m Model) AtBottom() bool { return m.offset == 0 }

// IsEmpty reports whether the viewport has no content lines.
func (m Model) IsEmpty() bool { return len(m.lines) == 0 }

// AutoScrollEnabled reports whether auto-scroll is active.
func (m Model) AutoScrollEnabled() bool { return m.autoScroll }

// ScrollOffset returns the current scroll offset (lines from bottom).
func (m Model) ScrollOffset() int { return m.offset }

// HasNewContent reports if new lines arrived while scrolled up.
func (m Model) HasNewContent() bool {
	return m.newContentCount > 0
}

// NewContentIndicator returns a "▼ N new" string when scrolled up with new content,
// or "" when at bottom or no new content.
func (m Model) NewContentIndicator() string {
	if m.autoScroll || m.newContentCount == 0 {
		return ""
	}
	return fmt.Sprintf("▼ %d new", m.newContentCount)
}

// Height returns the current viewport height.
func (m Model) Height() int { return m.height }

// SetSize updates viewport dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Update handles key messages for scrolling.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyPgUp:
			m.ScrollUp(m.height / 2)
		case tea.KeyPgDown:
			m.ScrollDown(m.height / 2)
		case tea.KeyUp:
			m.ScrollUp(1)
		case tea.KeyDown:
			m.ScrollDown(1)
		}
	}
	return m, nil
}

// View renders the visible portion of the conversation.
func (m Model) View() string {
	if m.height <= 0 || m.width <= 0 {
		return ""
	}

	total := len(m.lines)
	if total == 0 {
		return strings.Repeat("\n", m.height-1)
	}

	// Convert from-the-bottom offset to absolute start offset for WindowSlice.
	// offset=0 means bottom; absoluteOffset = total - offset - height.
	end := total - m.offset
	if end > total {
		end = total
	}
	start := end - m.height

	// When content is shorter than viewport height, start goes negative.
	// Pad with blank lines ABOVE the content so it anchors at the bottom
	// (chat-style), rather than clamping to 0 and showing blank space below.
	topPad := 0
	if start < 0 {
		topPad = -start
		start = 0
	}

	// Use absolute start offset clamped via ClampOffset.
	absOffset := ClampOffset(start, m.height, total)

	visible := WindowSlice(m.lines, absOffset, m.height)

	var sb strings.Builder

	// Prepend blank padding lines so content anchors at the bottom.
	for i := 0; i < topPad; i++ {
		sb.WriteString("\n")
	}

	for _, line := range visible {
		runes := []rune(line)
		if len(runes) > m.width {
			line = string(runes[:m.width])
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Pad remaining lines to fill height (only needed when content < height and
	// topPad was not already sufficient — typically len(visible)+topPad == height).
	for i := topPad + len(visible); i < m.height; i++ {
		sb.WriteString("\n")
	}

	result := sb.String()
	// Trim trailing newline from padding.
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}
	return result
}

// SetMaxHistory sets the maximum number of lines to retain.
// When maxHistory > 0 and lines exceed that count, the oldest lines are
// dropped on the next AppendLine call. 0 means unlimited.
func (m *Model) SetMaxHistory(n int) Model {
	if n < 0 {
		n = 0
	}
	m.maxHistory = n
	return *m
}
