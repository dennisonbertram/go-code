package themepicker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	tpDimColor  = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	tpHighlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

	tpDimStyle       = lipgloss.NewStyle().Foreground(tpDimColor)
	tpHighlightStyle = lipgloss.NewStyle().Background(tpHighlight).Foreground(lipgloss.Color("#FFFFFF"))
	tpBoldStyle      = lipgloss.NewStyle().Bold(true)
)

// View renders the theme picker as a rounded-border lipgloss box.
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
	sb.WriteString(tpBoldStyle.Render("Themes"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(m.entries) == 0 {
		msg := "No themes found"
		pad := (innerWidth - len(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(strings.Repeat(" ", pad))
		sb.WriteString(tpDimStyle.Render(msg))
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

			// Row: name + tags ("built-in" tag, "(active)" marker).
			name := truncateStr(e.Name, 32)
			var tags []string
			if e.Builtin {
				tags = append(tags, "built-in")
			}
			if e.Active {
				tags = append(tags, "(active)")
			}
			rowContent := name
			if len(tags) > 0 {
				rowContent += "  " + tpDimStyle.Render(strings.Join(tags, " "))
			}

			if i == m.selected {
				const marker = "> "
				sb.WriteString(tpHighlightStyle.Render(marker + name))
				if len(tags) > 0 {
					sb.WriteString("  " + tpDimStyle.Render(strings.Join(tags, " ")))
				}
			} else {
				sb.WriteString("  " + rowContent)
			}
			sb.WriteByte('\n')
		}

		// Footer: "... N more" if list extends beyond visible window.
		below := total - visEnd
		if below > 0 {
			sb.WriteString(tpDimStyle.Render(fmt.Sprintf("  ... %d more", below)))
			sb.WriteByte('\n')
		}
	}

	// Instructions footer.
	sb.WriteByte('\n')
	sb.WriteString(tpDimStyle.Render("  ↑/↓ navigate  enter select  esc cancel"))
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
