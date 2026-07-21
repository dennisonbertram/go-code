package tui

import (
	"fmt"
	"strings"
)

// compaction_block.go — epic #817 slice 4: compaction summary blocks in the
// transcript. Manual /compact results (CompactResultMsg) and auto_compact.*
// SSE events both render a collapsed-by-default block; ctrl+o toggles the most
// recent block between the collapsed title line and the expanded detail view,
// reusing the tool-card pattern of in-place viewport replacement with tracked
// line offsets.

// compactionBlock tracks one rendered compaction block in the viewport.
type compactionBlock struct {
	id        string
	title     string   // collapsed one-liner, e.g. "Compacted context — 3 messages removed"
	details   []string // revealed when expanded (mode, token counts, summary text)
	expanded  bool
	lineStart int // absolute viewport line offset of the block's first line
	lineCount int // number of viewport lines the block currently occupies
}

// compactionToggleIndicators mirror the tool-card expand/collapse affordance.
const (
	compactionCollapsedPrefix = "▸ "
	compactionExpandedPrefix  = "▾ "
	compactionDetailPrefix    = "⎿  "
)

// render produces the block's viewport lines at the given width.
func (b *compactionBlock) render(width int) []string {
	if !b.expanded {
		return []string{compactionCollapsedPrefix + b.title}
	}
	lines := []string{compactionExpandedPrefix + b.title}
	detailWidth := width - len([]rune(compactionDetailPrefix)) - 1
	for _, d := range b.details {
		for _, w := range wrapCompactionText(d, detailWidth) {
			lines = append(lines, compactionDetailPrefix+w)
		}
	}
	return lines
}

// wrapCompactionText greedily wraps plain text to width runes per line,
// preserving existing newlines. Widths <= 0 fall back to 76.
func wrapCompactionText(text string, width int) []string {
	if width <= 0 {
		width = 76
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len([]rune(line))+1+len([]rune(w)) > width {
				out = append(out, line)
				line = w
			} else {
				line += " " + w
			}
		}
		out = append(out, line)
	}
	return out
}

// addCompactionBlock appends a collapsed compaction block at the viewport tail
// and returns it.
func (m *Model) addCompactionBlock(title string, details []string) *compactionBlock {
	m.compactionSeq++
	b := &compactionBlock{
		id:        fmt.Sprintf("compact-%d", m.compactionSeq),
		title:     title,
		details:   details,
		lineStart: m.vp.LineCount(),
	}
	lines := b.render(m.width)
	b.lineCount = len(lines)
	m.vp.AppendLines(lines)
	m.compactionBlocks = append(m.compactionBlocks, b)
	return b
}

// findCompactionBlock returns the block with the given id, or nil.
func (m *Model) findCompactionBlock(id string) *compactionBlock {
	if id == "" {
		return nil
	}
	for _, b := range m.compactionBlocks {
		if b.id == id {
			return b
		}
	}
	return nil
}

// updateCompactionBlock re-renders a block in place after its title, details,
// or expanded state changed. When the line count changes, the tracked start
// offsets of tool cards and compaction blocks below it are shifted, mirroring
// appendToolUseView's in-place update.
func (m *Model) updateCompactionBlock(b *compactionBlock) {
	lines := b.render(m.width)
	delta := len(lines) - b.lineCount
	m.vp.ReplaceLineRange(b.lineStart, b.lineCount, lines)
	b.lineCount = len(lines)
	if delta == 0 {
		return
	}
	for id, start := range m.toolLineStarts {
		if start > b.lineStart {
			m.toolLineStarts[id] = start + delta
		}
	}
	for _, ob := range m.compactionBlocks {
		if ob != b && ob.lineStart > b.lineStart {
			ob.lineStart += delta
		}
	}
}

// toggleLatestCompactionBlock flips the expanded state of the most recent
// compaction block and re-renders it. Returns false when no block exists.
func (m *Model) toggleLatestCompactionBlock() bool {
	if len(m.compactionBlocks) == 0 {
		return false
	}
	b := m.compactionBlocks[len(m.compactionBlocks)-1]
	b.expanded = !b.expanded
	m.updateCompactionBlock(b)
	return true
}

// clearCompactionBlocks drops all compaction block tracking. Must be called
// wherever the viewport is rebuilt (new session, /clear, session switch).
func (m *Model) clearCompactionBlocks() {
	m.compactionBlocks = nil
	m.pendingAutoCompactID = ""
}
