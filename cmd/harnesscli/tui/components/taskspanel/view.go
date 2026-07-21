package taskspanel

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Column widths for the task list. Labels get whatever width remains.
const (
	colType   = 9
	colStatus = 12
	colAge    = 6
)

var (
	styleDialog = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255"))

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Faint(true).
			Foreground(lipgloss.Color("244"))

	styleSeparator = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	styleRow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	styleCursorRow = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255"))

	styleStateLine = lipgloss.NewStyle().
			Faint(true).
			Foreground(lipgloss.Color("244"))

	styleErrorLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	styleOverflowIndicator = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Faint(true)

	styleFooterHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Faint(true)
)

// render produces the full panel string at the given dimensions.
func render(m Model, width, height int) string {
	// Dialog inner width accounts for border (2) and page margins (2).
	dialogWidth := width - 4
	if dialogWidth > 84 {
		dialogWidth = 84
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	// Inner height: border (2) + title (1) + separator (1) + header (1) +
	// footer (1) leaves the rest for rows.
	contentLines := height - 8
	if contentLines < 3 {
		contentLines = 3
	}

	switch m.mode {
	case ModeConfirm:
		return renderConfirm(m, width, height, dialogWidth, contentLines)
	case ModeDetail:
		return renderDetail(m, width, height, dialogWidth, contentLines)
	default:
		return renderList(m, width, height, dialogWidth, contentLines)
	}
}

// renderList renders the task list screen (default mode).
func renderList(m Model, width, height, dialogWidth, contentLines int) string {
	title := lipgloss.NewStyle().Width(dialogWidth).Align(lipgloss.Center).
		Render(styleTitle.Render("Background Tasks"))
	sep := styleSeparator.Render(strings.Repeat("─", dialogWidth))
	header := styleHeader.Render("  " + rowCells("TYPE", "STATUS", "AGE", "COMMAND"))
	content := renderContent(m, dialogWidth, contentLines)
	footer := styleFooterHint.Width(dialogWidth).Align(lipgloss.Center).
		Render("↑/↓ navigate  o output  x stop  r refresh  esc close")

	body := title + "\n" + sep + "\n" + header + "\n" + content
	// Transient notice line (action errors) sits above the footer.
	if m.notice != "" {
		body += "\n" + styleErrorLine.Render("  "+truncate(m.notice, dialogWidth-2))
	}
	body += "\n" + footer

	dialog := styleDialog.Width(dialogWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}

// renderConfirm renders the destructive-action confirmation prompt (deleting
// a cron schedule cannot be undone).
func renderConfirm(m Model, width, height, dialogWidth, contentLines int) string {
	title := lipgloss.NewStyle().Width(dialogWidth).Align(lipgloss.Center).
		Render(styleTitle.Render("Confirm Delete"))
	sep := styleSeparator.Render(strings.Repeat("─", dialogWidth))

	prompt := []string{
		"",
		styleRow.Render(fmt.Sprintf("  Delete cron job %q?", truncate(m.confirmTask.Label, dialogWidth-24))),
		styleErrorLine.Render("  This cannot be undone."),
	}
	for len(prompt) < contentLines {
		prompt = append(prompt, "")
	}
	footer := styleFooterHint.Width(dialogWidth).Align(lipgloss.Center).
		Render("y confirm  n cancel")

	body := title + "\n" + sep + "\n" + strings.Join(prompt, "\n") + "\n" + footer
	dialog := styleDialog.Width(dialogWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}

// renderDetail renders the scrollable output view for one task.
func renderDetail(m Model, width, height, dialogWidth, contentLines int) string {
	titleText := "Output: " + truncate(m.detailTitle, dialogWidth-10)
	title := lipgloss.NewStyle().Width(dialogWidth).Align(lipgloss.Center).
		Render(styleTitle.Render(titleText))
	sep := styleSeparator.Render(strings.Repeat("─", dialogWidth))

	// Window the detail lines around the scroll offset with ▲/▼ indicators,
	// mirroring the list windowing.
	scroll := m.detailScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(m.detailLines) {
		scroll = len(m.detailLines)
	}
	visLines := m.detailLines[scroll:]
	hasAbove := scroll > 0
	hasBelow := len(visLines) > contentLines

	contentSlots := contentLines
	if hasAbove {
		contentSlots--
	}
	if hasBelow {
		contentSlots--
	}
	if contentSlots < 1 {
		contentSlots = 1
	}

	var displayLines []string
	if hasAbove {
		displayLines = append(displayLines,
			styleOverflowIndicator.Render(fmt.Sprintf("  ▲ %d more above", scroll)))
	}
	sliceEnd := contentSlots
	if sliceEnd > len(visLines) {
		sliceEnd = len(visLines)
	}
	for _, line := range visLines[:sliceEnd] {
		displayLines = append(displayLines, styleRow.Render("  "+truncate(line, dialogWidth-2)))
	}
	if hasBelow {
		displayLines = append(displayLines,
			styleOverflowIndicator.Render(fmt.Sprintf("  ▼ %d more below", len(visLines)-contentSlots)))
	}
	content := padLines(displayLines, contentLines)

	footer := styleFooterHint.Width(dialogWidth).Align(lipgloss.Center).
		Render("↑/↓ scroll  h/← back  esc close")

	body := title + "\n" + sep + "\n" + content + "\n" + footer
	dialog := styleDialog.Width(dialogWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}

// rowCells formats one table row with fixed column widths; the label column
// is truncated to fit the dialog width.
func rowCells(taskType, status, age, label string) string {
	return fmt.Sprintf("%-*s %-*s %*s %s",
		colType, truncate(taskType, colType),
		colStatus, truncate(status, colStatus),
		colAge, truncate(age, colAge),
		label)
}

// renderContent renders the state line (loading/error/empty) or the scrollable
// task rows with the cursor marker and overflow indicators.
func renderContent(m Model, width, maxLines int) string {
	switch {
	case m.loading:
		return padLines([]string{styleStateLine.Render("  Loading tasks…")}, maxLines)
	case m.err != "":
		return padLines([]string{styleErrorLine.Render("  Failed to load tasks: " + m.err)}, maxLines)
	case len(m.tasks) == 0:
		return padLines([]string{styleStateLine.Render("  No background tasks.")}, maxLines)
	}
	return renderRows(m, width, maxLines)
}

// renderRows renders the task list windowed around the cursor, keeping the
// selected row visible and showing ▲/▼ overflow indicators like helpdialog.
func renderRows(m Model, width, maxLines int) string {
	labelWidth := width - 2 - colType - 1 - colStatus - 1 - colAge - 1
	if labelWidth < 8 {
		labelWidth = 8
	}

	allLines := make([]string, len(m.tasks))
	for i, task := range m.tasks {
		cells := rowCells(task.Type, task.Status, FormatAge(task.AgeSeconds), truncate(task.Label, labelWidth))
		if i == m.cursor {
			allLines[i] = styleCursorRow.Render("› " + cells)
		} else {
			allLines[i] = styleRow.Render("  " + cells)
		}
	}

	// Keep the cursor inside the visible window. The ▲/▼ indicators consume
	// row slots, so scroll position and window size are computed together in
	// a bounded fixed-point loop: reserve indicator slots, re-check that the
	// cursor still fits, adjust, repeat. The result always fits maxLines
	// because contentSlots = maxLines - (indicator slots).
	scroll := m.scroll
	contentSlots := maxLines
	var visLines []string
	var hasAbove, hasBelow bool
	for i := 0; i < 3; i++ {
		if m.cursor < scroll {
			scroll = m.cursor
		}
		if m.cursor >= scroll+contentSlots {
			scroll = m.cursor - contentSlots + 1
		}
		if scroll < 0 {
			scroll = 0
		}
		visLines = allLines[scroll:]
		hasAbove = scroll > 0
		avail := maxLines
		if hasAbove {
			avail--
		}
		hasBelow = len(visLines) > avail
		newSlots := avail
		if hasBelow {
			newSlots--
		}
		if newSlots < 1 {
			newSlots = 1
		}
		if newSlots == contentSlots && m.cursor >= scroll && m.cursor < scroll+contentSlots {
			break
		}
		contentSlots = newSlots
	}

	var displayLines []string
	if hasAbove {
		displayLines = append(displayLines,
			styleOverflowIndicator.Render(fmt.Sprintf("  ▲ %d more above", scroll)))
	}
	sliceEnd := contentSlots
	if sliceEnd > len(visLines) {
		sliceEnd = len(visLines)
	}
	displayLines = append(displayLines, visLines[:sliceEnd]...)
	if hasBelow {
		displayLines = append(displayLines,
			styleOverflowIndicator.Render(fmt.Sprintf("  ▼ %d more below", len(visLines)-contentSlots)))
	}

	return padLines(displayLines, maxLines)
}

// padLines pads to maxLines with blanks so the dialog height is stable.
func padLines(lines []string, maxLines int) string {
	for len(lines) < maxLines {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// truncate clips s to maxLen runes, appending "…" when truncation occurs.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return "…"
	}
	return string(r[:maxLen-1]) + "…"
}
