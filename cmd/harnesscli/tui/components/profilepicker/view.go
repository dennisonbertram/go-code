package profilepicker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	ppDimColor  = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	ppHighlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

	ppDimStyle       = lipgloss.NewStyle().Foreground(ppDimColor)
	ppHighlightStyle = lipgloss.NewStyle().Background(ppHighlight).Foreground(lipgloss.Color("#FFFFFF"))
	ppBoldStyle      = lipgloss.NewStyle().Bold(true)
)

// View renders the profile picker as a rounded-border lipgloss box.
// Returns "" when the model is not open.
// Width=0 defaults to 80.
func (m Model) View() string {
	if !m.open {
		return ""
	}
	width := m.Width
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

	// Title row.
	sb.WriteString(ppBoldStyle.Render("Profiles"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(m.entries) == 0 {
		msg := "No profiles available"
		pad := (innerWidth - len(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(strings.Repeat(" ", pad))
		sb.WriteString(ppDimStyle.Render(msg))
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

			// Build description truncated to reasonable length.
			desc := truncateStr(e.Description, 40)
			sourceTier := e.SourceTier
			if sourceTier == "" {
				sourceTier = "built-in"
			}

			// Compose the row: Name  Model  SourceTier  Description
			metaPart := fmt.Sprintf("  %-20s  %-20s  %-10s  ", truncateStr(e.Name, 20), truncateStr(e.Model, 20), sourceTier)
			rowContent := metaPart + desc

			// Truncate to innerWidth.
			runes := []rune(rowContent)
			if len(runes) > innerWidth {
				runes = runes[:innerWidth]
				rowContent = string(runes)
			}

			if isSelected {
				// Prepend a visible text selection marker so selection is clear without color.
				const marker = "> "
				markerLen := len([]rune(marker))
				// Trim rowContent to leave room for marker within innerWidth.
				maxRowWidth := innerWidth - markerLen
				if maxRowWidth < 0 {
					maxRowWidth = 0
				}
				rr := []rune(rowContent)
				if len(rr) > maxRowWidth {
					rr = rr[:maxRowWidth]
					rowContent = string(rr)
				}
				markedContent := marker + rowContent
				// Pad to innerWidth for consistent highlight width.
				padded := markedContent + strings.Repeat(" ", innerWidth-len([]rune(markedContent)))
				sb.WriteString(ppHighlightStyle.Render(padded))
			} else {
				// Dim the metadata portion, normal color for description.
				// Unselected rows use 2-space indent (matching marker width) for alignment.
				const indent = "  "
				metaRunes := []rune(indent + metaPart)
				maxMeta := innerWidth
				if len(metaRunes) > maxMeta {
					metaRunes = metaRunes[:maxMeta]
				}
				remainWidth := innerWidth - len(metaRunes)
				if remainWidth < 0 {
					remainWidth = 0
				}
				descRunes := []rune(desc)
				if len(descRunes) > remainWidth {
					descRunes = descRunes[:remainWidth]
				}
				sb.WriteString(ppDimStyle.Render(string(metaRunes)))
				sb.WriteString(string(descRunes))
			}
			sb.WriteByte('\n')
		}

		// Footer: "... N more" if list extends beyond visible window.
		below := total - visEnd
		if below > 0 {
			footer := fmt.Sprintf("  ... %d more", below)
			sb.WriteString(ppDimStyle.Render(footer))
			sb.WriteByte('\n')
		}
	}

	// Instructions footer.
	sb.WriteByte('\n')
	sb.WriteString(ppDimStyle.Render("  ↑/↓ navigate  enter select  esc cancel"))
	sb.WriteByte('\n')

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth)

	return boxStyle.Render(sb.String())
}

// truncateStr clips s to at most maxLen runes, appending "…" if truncated.
func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}
