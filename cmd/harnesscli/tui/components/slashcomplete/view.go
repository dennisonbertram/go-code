package slashcomplete

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	// selectedPrefix is prepended to the currently highlighted row.
	selectedPrefix = "▶ "
	// normalPrefix is prepended to non-selected rows.
	normalPrefix = "  "
)

// View renders the dropdown overlay.
// Returns "" when the model is not active.
// width=0 defaults to 80.
func (m Model) View(width int) string {
	if !m.active {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	maxVis := m.maxVisible
	if maxVis <= 0 {
		maxVis = 8
	}

	filtered := m.filtered
	total := len(filtered)
	if total == 0 {
		return ""
	}

	// Styles — built inline so view.go has no external theme dependency.
	selectedStyle := lipgloss.NewStyle().Reverse(true)
	dimStyle := lipgloss.NewStyle().Faint(true)

	// Determine the longest name for alignment (across the full list for stable columns).
	maxName := 0
	for _, s := range filtered {
		if len(s.Name) > maxName {
			maxName = len(s.Name)
		}
	}
	// Name column: "/" + name padded to maxName+1
	nameColWidth := maxName + 1 // +1 for leading "/"

	// Compute the scroll window: [windowStart, windowEnd).
	// We need to reserve rows for indicators when items exist outside the window.
	// Strategy: start with a maxVis window, then shrink for any needed indicator rows
	// while keeping m.selected within the rendered range.
	windowStart := m.scrollOffset
	if windowStart < 0 {
		windowStart = 0
	}

	// Determine which indicators are needed (based on raw window before shrinking).
	rawEnd := windowStart + maxVis
	if rawEnd > total {
		rawEnd = total
	}
	showAbove := windowStart > 0
	showBelow := rawEnd < total

	// Compute effective content capacity after reserving indicator rows.
	contentCap := maxVis
	if showAbove {
		contentCap--
	}
	if showBelow {
		contentCap--
	}
	if contentCap < 1 {
		contentCap = 1
	}

	// Place the content window so that m.selected is always visible.
	// Window: [windowStart, windowStart+contentCap).
	// If selected is beyond the end, shift windowStart forward.
	if m.selected >= windowStart+contentCap {
		windowStart = m.selected - contentCap + 1
	}
	// If selected is before windowStart, bring windowStart back.
	if m.selected < windowStart {
		windowStart = m.selected
	}
	// Clamp windowStart.
	if windowStart < 0 {
		windowStart = 0
	}
	if windowStart >= total {
		windowStart = total - 1
	}

	windowEnd := windowStart + contentCap
	if windowEnd > total {
		windowEnd = total
	}

	// Recompute indicators based on final window position.
	showAbove = windowStart > 0
	showBelow = windowEnd < total

	var sb strings.Builder

	if showAbove {
		indicator := fmt.Sprintf("  ▲ %d more above", windowStart)
		sb.WriteString(dimStyle.Render(indicator) + "\n")
	}

	for i := windowStart; i < windowEnd; i++ {
		s := filtered[i]
		isSelected := i == m.selected

		// Build the name portion: "/name   " padded
		namePart := "/" + s.Name
		padding := strings.Repeat(" ", nameColWidth-len(namePart)+2)

		// Build the full row content (without prefix)
		rowContent := namePart + padding + s.Description

		// Trim to fit within width (prefix takes 2 chars)
		available := width - len(selectedPrefix)
		if available < 0 {
			available = 0
		}
		// Use rune-aware truncation
		runes := []rune(rowContent)
		if len(runes) > available {
			runes = runes[:available]
			rowContent = string(runes)
		}

		var line string
		if isSelected {
			line = selectedPrefix + selectedStyle.Render(rowContent)
		} else {
			line = normalPrefix + rowContent
		}
		sb.WriteString(line + "\n")
	}

	if showBelow {
		below := total - windowEnd
		indicator := fmt.Sprintf("  ▼ %d more below", below)
		sb.WriteString(dimStyle.Render(indicator) + "\n")
	}

	return sb.String()
}
