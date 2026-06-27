package sessionpicker

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

// View renders the session picker as a rounded-border lipgloss box.
// Returns "" when the model is not open.
// width=0 defaults to 80.
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

	// Title row.
	sb.WriteString(boldStyle.Render("Sessions"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(m.entries) == 0 {
		// Center "No sessions found" message.
		msg := "No sessions found"
		pad := (innerWidth - len(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(strings.Repeat(" ", pad))
		sb.WriteString(dimStyle.Render(msg))
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

			// Build columns.
			shortID := e.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}

			dateStr := e.StartedAt.Format("Jan 2")
			turnsStr := fmt.Sprintf("%d turns", e.TurnCount)

			lastMsg := e.LastMsg
			if len([]rune(lastMsg)) > lastMsgMaxLen {
				runes := []rune(lastMsg)
				lastMsg = string(runes[:lastMsgMaxLen])
			}

			// Compose the row: ID  Date  Model  Turns  LastMsg
			metaPart := fmt.Sprintf("  %s  %s  %s  %s  ", shortID, dateStr, e.Model, turnsStr)
			rowContent := metaPart + lastMsg

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
				sb.WriteString(highlightStyle.Render(padded))
			} else {
				// Dim the metadata portion, normal color for last message.
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
				msgRunes := []rune(lastMsg)
				if len(msgRunes) > remainWidth {
					msgRunes = msgRunes[:remainWidth]
				}
				sb.WriteString(dimStyle.Render(string(metaRunes)))
				sb.WriteString(string(msgRunes))
			}
			sb.WriteByte('\n')
		}

		// Footer: "... N more" if list extends beyond visible window.
		below := total - visEnd
		if below > 0 {
			footer := fmt.Sprintf("  ... %d more", below)
			sb.WriteString(dimStyle.Render(footer))
			sb.WriteByte('\n')
		}
	}

	// Footer navigation hint.
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("  ↑/↓ navigate  enter select  esc cancel"))
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
