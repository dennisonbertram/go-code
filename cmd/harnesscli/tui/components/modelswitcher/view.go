package modelswitcher

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	dimColor  = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}

	titleStyle = lipgloss.NewStyle().Bold(true)

	highlightStyle = lipgloss.NewStyle().
			Reverse(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

	dimStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(dimColor)

	providerStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(dimColor)

	currentStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(dimColor)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	reasoningBadgeStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(dimColor)

	starStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220"))

	unavailableStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#444444"})

	unavailableSuffixStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#AAAAAA", Dark: "#555555"})
)

func (m Model) View(width int) string {
	if !m.IsOpen {
		return ""
	}
	if m.reasoningMode {
		return m.viewReasoning(width)
	}
	return m.viewModelList(width)
}

func (m Model) viewModelList(width int) string {
	if width <= 0 {
		width = 60
	}
	if m.searchQuery != "" {
		return m.viewFlatModelList(width)
	}
	if m.browseLevel == 0 {
		return m.viewProviderList(width)
	}
	return m.viewModelsForProvider(width)
}

func (m Model) viewProviderList(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	if m.loadError != "" {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Switch Model"))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		sb.WriteString(errStyle.Render(m.loadError))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		sb.WriteString(dimStyle.Render("esc cancel"))
		return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
	}

	provs := m.providers()

	textWidth := innerWidth - 2
	if textWidth < 10 {
		textWidth = 10
	}
	var provRows []string
	for i, p := range provs {
		isSelected := i == m.providerCursor
		countStr := "(" + itoa(p.Count) + ")"

		var indicator string
		if m.availabilitySet {
			if p.Configured {
				indicator = " ●"
			} else {
				indicator = " ○"
			}
		}

		var currentSuffix string
		if p.HasCurrent {
			currentSuffix = "  ← current"
		}

		if isSelected {
			full := p.Label + currentSuffix + indicator
			countPart := "  " + countStr
			runes := []rune("> " + full + countPart)
			padNeeded := innerWidth - len(runes)
			if padNeeded < 0 {
				padNeeded = 0
			}
			highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
			provRows = append(provRows, highlighted)
		} else {
			label := p.Label + currentSuffix
			countPart := countStr + indicator
			labelRunes := []rune(label)
			countRunes := []rune(countPart)
			pad := textWidth - len(labelRunes) - len(countRunes)
			if pad < 1 {
				pad = 1
			}
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(label)
			row.WriteString(strings.Repeat(" ", pad))
			row.WriteString(dimStyle.Render(countPart))
			provRows = append(provRows, row.String())
		}
	}

	fixedLines := 6
	if m.loading {
		fixedLines++
	}
	if m.MaxHeight > 0 && len(provRows) > 0 {
		provRows = windowRows(provRows, m.providerCursor, m.MaxHeight-fixedLines)
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Switch Model"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if m.loading {
		sb.WriteString(dimStyle.Render("Loading models..."))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
	}

	if len(provs) == 0 && !m.loading {
		sb.WriteString(dimStyle.Render("No providers available"))
		sb.WriteByte('\n')
	} else if len(provRows) > 0 {
		for _, row := range provRows {
			sb.WriteString(row)
			sb.WriteByte('\n')
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  / search  esc cancel"))

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

func (m Model) viewModelsForProvider(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	visible := m.visibleModels()

	var modelRows []string
	for i, entry := range visible {
		isSelected := i == m.Selected
		isStarred := m.starred[entry.ID]
		isUnavailable := m.availabilitySet && !entry.Available

		var starPrefix string
		if isStarred {
			starPrefix = starStyle.Render("★") + " "
		} else {
			starPrefix = "  "
		}

		var keySuffix string
		if m.keyStatus != nil {
			if m.keyStatus(entry.Provider) {
				keySuffix = " ●"
			} else {
				keySuffix = " ○"
			}
		}

		if isSelected {
			nameAndSuffix := entry.DisplayName
			if entry.ReasoningMode {
				nameAndSuffix += " [R]"
			}
			if isUnavailable {
				nameAndSuffix += " (unavailable)"
			}
			if entry.IsCurrent {
				nameAndSuffix += "  ← current"
			}
			nameAndSuffix += keySuffix
			var starRaw string
			if isStarred {
				starRaw = "★ "
			} else {
				starRaw = "  "
			}
			runes := []rune("> " + starRaw + nameAndSuffix)
			padNeeded := innerWidth - len(runes)
			if padNeeded < 0 {
				padNeeded = 0
			}
			highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
			modelRows = append(modelRows, highlighted)
		} else if isUnavailable {
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(starPrefix)
			row.WriteString(unavailableStyle.Render(entry.DisplayName))
			if entry.ReasoningMode {
				row.WriteString(" ")
				row.WriteString(unavailableStyle.Render("[R]"))
			}
			row.WriteString(" ")
			row.WriteString(unavailableSuffixStyle.Render("(unavailable)"))
			if entry.IsCurrent {
				row.WriteString("  " + currentStyle.Render("← current"))
			}
			if keySuffix != "" {
				row.WriteString(dimStyle.Render(keySuffix))
			}
			modelRows = append(modelRows, row.String())
		} else {
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(starPrefix)
			row.WriteString(entry.DisplayName)
			if entry.ReasoningMode {
				row.WriteString(" ")
				row.WriteString(reasoningBadgeStyle.Render("[R]"))
			}
			if entry.IsCurrent {
				row.WriteString("  " + currentStyle.Render("← current"))
			}
			if keySuffix != "" {
				row.WriteString(dimStyle.Render(keySuffix))
			}
			modelRows = append(modelRows, row.String())
		}
	}

	fixedLines := 6
	if m.MaxHeight > 0 && len(modelRows) > 0 {
		modelRows = windowRows(modelRows, m.Selected, m.MaxHeight-fixedLines)
	}

	var sb strings.Builder
	sb.WriteString(dimStyle.Render("< Back"))
	sb.WriteString("  [" + m.activeProvider + "]")
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(visible) == 0 {
		sb.WriteString(dimStyle.Render("No models available"))
		sb.WriteByte('\n')
	} else if len(modelRows) > 0 {
		for _, row := range modelRows {
			sb.WriteString(row)
			sb.WriteByte('\n')
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  s star  esc back"))

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

func (m Model) viewFlatModelList(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	if m.loadError != "" {
		var sb strings.Builder
		sb.WriteString(titleStyle.Render("Switch Model"))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		sb.WriteString(dimStyle.Render("Filter: "))
		sb.WriteString(m.searchQuery)
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		sb.WriteString(errStyle.Render(m.loadError))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		sb.WriteString(dimStyle.Render("esc cancel"))
		return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
	}

	visible := m.visibleModels()

	var modelRows []string
	for i, entry := range visible {
		isSelected := i == m.Selected
		isStarred := m.starred[entry.ID]
		isUnavailable := m.availabilitySet && !entry.Available

		provLabel := entry.ProviderLabel
		if provLabel == "" {
			provLabel = entry.Provider
		}
		provPrefix := "[" + provLabel + "] "

		var starPrefix string
		if isStarred {
			starPrefix = starStyle.Render("★") + " "
		} else {
			starPrefix = "  "
		}

		var keySuffix string
		if m.keyStatus != nil {
			if m.keyStatus(entry.Provider) {
				keySuffix = " ●"
			} else {
				keySuffix = " ○"
			}
		}

		if isSelected {
			nameAndSuffix := provPrefix + entry.DisplayName
			if entry.ReasoningMode {
				nameAndSuffix += " [R]"
			}
			if isUnavailable {
				nameAndSuffix += " (unavailable)"
			}
			if entry.IsCurrent {
				nameAndSuffix += "  ← current"
			}
			nameAndSuffix += keySuffix
			var starRaw string
			if isStarred {
				starRaw = "★ "
			} else {
				starRaw = "  "
			}
			runes := []rune("> " + starRaw + nameAndSuffix)
			padNeeded := innerWidth - len(runes)
			if padNeeded < 0 {
				padNeeded = 0
			}
			highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
			modelRows = append(modelRows, highlighted)
		} else if isUnavailable {
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(starPrefix)
			row.WriteString(dimStyle.Render(provPrefix))
			row.WriteString(unavailableStyle.Render(entry.DisplayName))
			if entry.ReasoningMode {
				row.WriteString(" ")
				row.WriteString(unavailableStyle.Render("[R]"))
			}
			row.WriteString(" ")
			row.WriteString(unavailableSuffixStyle.Render("(unavailable)"))
			if entry.IsCurrent {
				row.WriteString("  " + currentStyle.Render("← current"))
			}
			if keySuffix != "" {
				row.WriteString(dimStyle.Render(keySuffix))
			}
			modelRows = append(modelRows, row.String())
		} else {
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(starPrefix)
			row.WriteString(dimStyle.Render(provPrefix))
			row.WriteString(entry.DisplayName)
			if entry.ReasoningMode {
				row.WriteString(" ")
				row.WriteString(reasoningBadgeStyle.Render("[R]"))
			}
			if entry.IsCurrent {
				row.WriteString("  " + currentStyle.Render("← current"))
			}
			if keySuffix != "" {
				row.WriteString(dimStyle.Render(keySuffix))
			}
			modelRows = append(modelRows, row.String())
		}
	}

	fixedLines := 8
	if m.loading {
		fixedLines++
	}
	if m.MaxHeight > 0 && len(modelRows) > 0 {
		modelRows = windowRows(modelRows, m.Selected, m.MaxHeight-fixedLines)
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Switch Model"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("Filter: "))
	sb.WriteString(m.searchQuery)
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if m.loading {
		sb.WriteString(dimStyle.Render("Loading models..."))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
	}

	if len(visible) == 0 && !m.loading {
		sb.WriteString(dimStyle.Render("No models match"))
		sb.WriteByte('\n')
	} else if len(modelRows) > 0 {
		for _, row := range modelRows {
			sb.WriteString(row)
			sb.WriteByte('\n')
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  esc cancel search"))

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

func (m Model) viewReasoning(width int) string {
	if width <= 0 {
		width = 60
	}

	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	currentModelDisplayName := ""
	visible := m.visibleModels()
	if m.Selected >= 0 && m.Selected < len(visible) {
		currentModelDisplayName = visible[m.Selected].DisplayName
	}

	var reasonRows []string
	for i, entry := range ReasoningLevels {
		isSelected := i == m.reasoningSelected
		isCurrent := entry.ID == m.currentReasoning

		if isSelected {
			nameAndSuffix := entry.DisplayName
			if isCurrent {
				nameAndSuffix += "  ← current"
			}
			runes := []rune("> " + nameAndSuffix)
			padNeeded := innerWidth - len(runes)
			if padNeeded < 0 {
				padNeeded = 0
			}
			highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
			reasonRows = append(reasonRows, highlighted)
		} else {
			var row strings.Builder
			row.WriteString("  ")
			row.WriteString(entry.DisplayName)
			if isCurrent {
				row.WriteString("  " + currentStyle.Render("← current"))
			}
			reasonRows = append(reasonRows, row.String())
		}
	}

	fixedLines := 6
	if m.MaxHeight > 0 && len(reasonRows) > 0 {
		reasonRows = windowRows(reasonRows, m.reasoningSelected, m.MaxHeight-fixedLines)
	}

	var sb strings.Builder
	title := "Reasoning Effort  [" + currentModelDisplayName + "]"
	sb.WriteString(titleStyle.Render(title))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	for _, row := range reasonRows {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}

	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter confirm  esc back"))

	return boxStyle.
		Width(innerWidth).
		BorderForeground(lipgloss.Color("240")).
		Render(sb.String())
}

func windowRows(rows []string, cursor, maxRows int) []string {
	n := len(rows)
	if maxRows <= 0 || n <= maxRows {
		return rows
	}

	start := cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > n {
		end = n
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}

	needTop := start > 0
	needBot := end < n
	if !needTop && !needBot {
		return rows
	}

	itemStart, itemEnd := start, end
	if needTop {
		itemStart++
	}
	if needBot {
		itemEnd--
	}

	if cursor < itemStart {
		shift := itemStart - cursor
		itemStart -= shift
		itemEnd -= shift
	}
	if cursor >= itemEnd {
		shift := cursor - itemEnd + 1
		itemStart += shift
		itemEnd += shift
	}

	if itemStart < 0 {
		itemStart = 0
	}
	if itemEnd > n {
		itemEnd = n
	}
	if itemEnd <= itemStart {
		itemEnd = itemStart + 1
		if itemEnd > n {
			itemEnd = n
			itemStart = n - 1
			if itemStart < 0 {
				itemStart = 0
			}
		}
	}

	var result []string
	if itemStart > 0 {
		result = append(result, dimStyle.Render(fmt.Sprintf("  ▲ %d more", itemStart)))
	}
	result = append(result, rows[itemStart:itemEnd]...)
	if itemEnd < n {
		result = append(result, dimStyle.Render(fmt.Sprintf("  ▼ %d more", n-itemEnd)))
	}

	if len(result) > maxRows {
		result = result[:maxRows]
	}

	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
