package permissionspanel

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette — matches the TUI-052 spec.
var (
	specialColor   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"} // green check
	errorColor     = lipgloss.AdaptiveColor{Light: "#FF5F87", Dark: "#FF5F87"} // red X
	highlightColor = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"} // tool name
	dimColor       = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"} // dim text
)

// styles are package-level so they are initialised once.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	checkStyle    = lipgloss.NewStyle().Foreground(specialColor)
	crossStyle    = lipgloss.NewStyle().Foreground(errorColor)
	toolStyle     = lipgloss.NewStyle().Foreground(highlightColor)
	dimStyle      = lipgloss.NewStyle().Foreground(dimColor)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
)

// View renders the permissions panel as a full-width block.
//
// The caller must set m.Width (and optionally m.Height) before calling View().
// A Width of 0 is treated as 80 to avoid degenerate output.
func (m Model) View() string {
	width := m.Width
	if width < 20 {
		width = 20
	}

	var sb strings.Builder

	// ── Title ──────────────────────────────────────────────────────────────
	sb.WriteString(titleStyle.Render("Permissions"))
	sb.WriteByte('\n')

	// ── Separator ──────────────────────────────────────────────────────────
	sep := strings.Repeat("─", width)
	sb.WriteString(dimStyle.Render(sep))
	sb.WriteByte('\n')

	// ── Rule rows or empty state ────────────────────────────────────────────
	if len(m.Rules) == 0 {
		sb.WriteString(dimStyle.Render("No permission rules active"))
		sb.WriteByte('\n')
	} else {
		for i, rule := range m.Rules {
			row := renderRow(rule, i == m.Selected, width)
			sb.WriteString(row)
			sb.WriteByte('\n')
		}
	}

	// ── Footer hint ────────────────────────────────────────────────────────
	sb.WriteByte('\n')
	footer := "↑/↓ navigate  t toggle  d delete  esc close"
	sb.WriteString(dimStyle.Render(footer))

	return sb.String()
}

// renderRow renders a single permission rule row.
//
// Format (unselected): "  [✓/✗] [toolname]  once/permanent"
// Format (selected):   "> [✓/✗] [toolname]  once/permanent" (reverse video)
func renderRow(rule PermissionRule, selected bool, width int) string {
	// Build the check/cross symbol.
	var symbol string
	if rule.Allowed {
		symbol = checkStyle.Render("✓")
	} else {
		symbol = crossStyle.Render("✗")
	}

	// Permanence label.
	var permLabel string
	if rule.Permanent {
		permLabel = dimStyle.Render("permanent")
	} else {
		permLabel = dimStyle.Render("once")
	}

	// Tool name styled.
	toolName := toolStyle.Render(rule.ToolName)

	// Combine the plain-text parts first for length measurement, then build
	// the styled version.
	inner := symbol + " " + toolName + "  " + permLabel

	var row string
	if selected {
		// Cursor prefix + reverse-video the entire row.
		prefix := "> "
		row = selectedStyle.Render(prefix + inner)
	} else {
		row = "  " + inner
	}

	return row
}
