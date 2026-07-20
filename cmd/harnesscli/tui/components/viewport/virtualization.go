package viewport

// WindowSlice returns the slice of lines that should be rendered for the given
// viewport height and scroll offset. This is the core virtualization primitive.
// It never panics on empty slices or out-of-range offsets.
//
// offset is a zero-based index into lines indicating the first visible line.
// height is the number of lines that can be displayed.
func WindowSlice(lines []string, offset, height int) []string {
	if len(lines) == 0 || height <= 0 {
		return []string{}
	}
	offset = ClampOffset(offset, height, len(lines))
	end := offset + height
	if end > len(lines) {
		end = len(lines)
	}
	return lines[offset:end]
}

// TotalHeight returns the total number of lines in the backing store.
func TotalHeight(lines []string) int {
	return len(lines)
}

// ClampOffset returns offset clamped so that [offset, offset+height) is always
// within [0, len(lines)).  Returns 0 if lines is empty or totalLines <= 0.
func ClampOffset(offset, height, totalLines int) int {
	if totalLines <= 0 {
		return 0
	}
	if offset < 0 {
		return 0
	}
	maxOffset := totalLines - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

// VirtualizedModel wraps a backing []string store with a bounded visible window.
// It supports adding lines, scrolling, and pruning by max-history policy.
//
// VirtualizedModel is an immutable value type: all mutating methods return a
// new VirtualizedModel. This makes it safe for concurrent use when each
// goroutine operates on its own copy.
//
// By default, VirtualizedModel auto-scrolls to the bottom when new lines are
// appended (atBottom=true). Scrolling up disables auto-scroll. ScrollToBottom
// or ScrollDown to the end re-enables it.
type VirtualizedModel struct {
	lines      []string
	offset     int // absolute index of first visible line
	height     int
	maxHistory int  // 0 = unlimited
	atBottom   bool // whether to pin to bottom on append
}

// NewVirtualizedModel creates a new VirtualizedModel with the given height and
// maxHistory. maxHistory=0 means unlimited. Auto-scroll is enabled by default.
func NewVirtualizedModel(height, maxHistory int) VirtualizedModel {
	if height < 0 {
		height = 0
	}
	if maxHistory < 0 {
		maxHistory = 0
	}
	return VirtualizedModel{
		lines:      []string{},
		offset:     0,
		height:     height,
		maxHistory: maxHistory,
		atBottom:   true,
	}
}

// AppendLine adds a line to the end of the backing store.
// If the model is at the bottom (auto-scroll), the offset advances so the
// window stays pinned to the newest content.
// If maxHistory > 0 and the store would exceed maxHistory, the oldest lines
// are pruned and offset is adjusted to keep the same logical position.
func (v VirtualizedModel) AppendLine(line string) VirtualizedModel {
	newLines := make([]string, len(v.lines)+1)
	copy(newLines, v.lines)
	newLines[len(v.lines)] = line

	offset := v.offset
	atBottom := v.atBottom

	// Prune if needed.
	if v.maxHistory > 0 && len(newLines) > v.maxHistory {
		dropped := len(newLines) - v.maxHistory
		newLines = newLines[dropped:]
		offset -= dropped
		if offset < 0 {
			offset = 0
		}
	}

	// If auto-scrolling, pin to the bottom.
	if atBottom {
		maxOffset := len(newLines) - v.height
		if maxOffset < 0 {
			maxOffset = 0
		}
		offset = maxOffset
	} else {
		// Clamp to valid range without pinning.
		offset = ClampOffset(offset, v.height, len(newLines))
	}

	return VirtualizedModel{
		lines:      newLines,
		offset:     offset,
		height:     v.height,
		maxHistory: v.maxHistory,
		atBottom:   atBottom,
	}
}

// ScrollUp scrolls up by n lines (shows older content).
// Disables auto-scroll (atBottom=false).
func (v VirtualizedModel) ScrollUp(n int) VirtualizedModel {
	if n < 0 {
		n = 0
	}
	newOffset := v.offset - n
	if newOffset < 0 {
		newOffset = 0
	}
	return VirtualizedModel{
		lines:      v.lines,
		offset:     newOffset,
		height:     v.height,
		maxHistory: v.maxHistory,
		atBottom:   false,
	}
}

// ScrollDown scrolls down by n lines (shows newer content).
// Re-enables auto-scroll (atBottom=true) if the window reaches the end.
func (v VirtualizedModel) ScrollDown(n int) VirtualizedModel {
	if n < 0 {
		n = 0
	}
	newOffset := v.offset + n
	maxOffset := len(v.lines) - v.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	atBottom := false
	if newOffset >= maxOffset {
		newOffset = maxOffset
		atBottom = true
	}
	return VirtualizedModel{
		lines:      v.lines,
		offset:     newOffset,
		height:     v.height,
		maxHistory: v.maxHistory,
		atBottom:   atBottom,
	}
}

// ScrollToBottom positions the window at the end of the backing store
// and re-enables auto-scroll.
func (v VirtualizedModel) ScrollToBottom() VirtualizedModel {
	maxOffset := len(v.lines) - v.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	return VirtualizedModel{
		lines:      v.lines,
		offset:     maxOffset,
		height:     v.height,
		maxHistory: v.maxHistory,
		atBottom:   true,
	}
}

// View returns the slice of lines currently visible in the window.
// It never returns more than height lines.
func (v VirtualizedModel) View() []string {
	if v.height <= 0 {
		return []string{}
	}
	return WindowSlice(v.lines, v.offset, v.height)
}

// AtBottom reports whether the window is positioned at the end of the store
// and auto-scroll is active.
func (v VirtualizedModel) AtBottom() bool {
	return v.atBottom
}

// TotalLines returns the total number of lines in the backing store.
func (v VirtualizedModel) TotalLines() int {
	return len(v.lines)
}

// VisibleLines returns the height (number of lines in the visible window).
func (v VirtualizedModel) VisibleLines() int {
	return v.height
}
