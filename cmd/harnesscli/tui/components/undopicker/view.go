package undopicker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	dimColor  = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

	dimStyle       = lipgloss.NewStyle().Foreground(dimColor)
	highlightStyle = lipgloss.NewStyle().Background(highlight).Foreground(lipgloss.Color("#FFFFFF"))
	boldStyle      = lipgloss.NewStyle().Bold(true)
)

// View renders the undo picker as a rounded-border lipgloss box.
// Returns "" when the model is not open. width=0 defaults to 80.
func (m Model) View(width int) string {
	if !m.open {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	// Inner content width: rounded border uses 2 cols (border+space) on each side.
	const padding = 4
	innerWidth := width - padding
	if innerWidth < 20 {
		innerWidth = 20
	}

	var sb strings.Builder

	sb.WriteString(boldStyle.Render("Undo to prompt"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(m.entries) == 0 {
		sb.WriteString(dimStyle.Render("  Nothing to undo yet"))
		sb.WriteByte('\n')
	} else {
		total := len(m.entries)
		visStart := m.scrollOffset
		visEnd := visStart + maxVisibleRows
		if visEnd > total {
			visEnd = total
		}

		for i := visStart; i < visEnd; i++ {
			e := m.entries[i]
			isSelected := i == m.selected

			rowContent := fmt.Sprintf("%d. %s", e.Count, e.Preview)
			if e.Disabled {
				rowContent += "  (compaction boundary — cannot undo)"
			}
			rowContent = truncateStr(rowContent, innerWidth)

			switch {
			case isSelected:
				const marker = "> "
				marked := marker + truncateStr(rowContent, innerWidth-len(marker))
				padded := marked + strings.Repeat(" ", max(0, innerWidth-len([]rune(marked))))
				sb.WriteString(highlightStyle.Render(padded))
			case e.Disabled:
				sb.WriteString(dimStyle.Render("  " + rowContent))
			default:
				sb.WriteString("  " + rowContent)
			}
			sb.WriteByte('\n')
		}

		if below := total - visEnd; below > 0 {
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d more", below)))
			sb.WriteByte('\n')
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("  ↑/↓ navigate  enter undo to here  esc cancel"))
	sb.WriteByte('\n')

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth)

	return boxStyle.Render(sb.String())
}

// truncateStr clips s to at most maxLen runes.
func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
