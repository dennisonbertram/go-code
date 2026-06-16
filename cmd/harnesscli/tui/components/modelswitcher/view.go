package modelswitcher

import (
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
			Foreground(lipgloss.Color("220")) // gold/yellow

	// unavailableStyle renders the "(unavailable)" suffix and dims the whole row
	// when a model's provider is not configured.
	unavailableStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#444444"})

	// unavailableSuffixStyle renders the "(unavailable)" label itself.
	unavailableSuffixStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#AAAAAA", Dark: "#555555"})

	// scrollIndicatorStyle renders "... N more above/below" overflow indicators.
	scrollIndicatorStyle = lipgloss.NewStyle().
				Faint(true).
				Foreground(dimColor)
)

// View renders the model switcher dropdown.
// Returns "" when IsVisible() is false.
// width=0 defaults to 60.
func (m Model) View(width int) string {
	if !m.IsOpen {
		return ""
	}
	if m.reasoningMode {
		return m.viewReasoning(width)
	}
	return m.viewModelList(width)
}

// viewModelList dispatches to the appropriate view based on browse level and search state.
func (m Model) viewModelList(width int) string {
	if width <= 0 {
		width = 60
	}
	// When searching (any level): flat cross-provider search results.
	if m.searchQuery != "" {
		return m.viewFlatModelList(width)
	}
	// Level 0: provider list.
	if m.browseLevel == 0 {
		return m.viewProviderList(width)
	}
	// Level 1: models for the active provider.
	return m.viewModelsForProvider(width)
}

// viewProviderList renders the Level-0 provider list.
func (m Model) viewProviderList(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	var sb strings.Builder

	// Title.
	sb.WriteString(titleStyle.Render("Switch Model"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	// Error state.
	if m.loadError != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		sb.WriteString(errStyle.Render(m.loadError))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		sb.WriteString(dimStyle.Render("esc cancel"))
		return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
	}

	// Loading indicator.
	if m.loading {
		sb.WriteString(dimStyle.Render("Loading models..."))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
	}

	provs := m.providers()
	if len(provs) == 0 && !m.loading {
		sb.WriteString(dimStyle.Render("No providers available"))
		sb.WriteByte('\n')
	} else {
		// Right-align the count column. Compute right portion width: " (NNN)" → max 6 chars.
		// We'll right-pad the label and right-align the count.
		// Format: "  {Label}...{spaces}({Count})"
		// Available inner text width after the 2-char indent and 2-char box L/R padding.
		textWidth := innerWidth - 4
		if textWidth < 10 {
			textWidth = 10
		}

		// Compute scroll window for providers (Issue #572).
		maxVisible := m.maxVisibleContentRows()
		windowStart := m.providerScrollOffset
		windowEnd := windowStart + maxVisible
		if windowEnd > len(provs) {
			windowEnd = len(provs)
		}
		// Reserve room for scroll indicators when items are hidden.
		showAbove := windowStart > 0
		showBelow := windowEnd < len(provs)
		if showAbove && windowEnd > windowStart {
			windowEnd--
		}
		if showBelow && windowEnd > windowStart {
			windowEnd--
		}

		if showAbove {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(windowStart) + " more above"))
			sb.WriteByte('\n')
		}

		for i := windowStart; i < windowEnd; i++ {
			p := provs[i]
			isSelected := i == m.providerCursor

			// Build count string.
			countStr := "(" + itoa(p.Count) + ")"

			// Build indicator: ● if configured, ○ if not (only when availabilitySet).
			var indicator string
			if m.availabilitySet {
				if p.Configured {
					indicator = " ●"
				} else {
					indicator = " ○"
				}
			}

			// Build current suffix.
			var currentSuffix string
			if p.HasCurrent {
				currentSuffix = "  ← current"
			}

			if isSelected {
				// Highlighted row: "> {Label}{padding}{count}{indicator}{current}"
				full := p.Label + currentSuffix + indicator
				countPart := "  " + countStr
				runes := []rune("> " + full + countPart)
				// Pad to innerWidth-2 for consistent highlight width (accounting for box L/R padding).
				padNeeded := innerWidth - 2 - len(runes)
				if padNeeded < 0 {
					padNeeded = 0
				}
				highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
				sb.WriteString(highlighted)
			} else {
				// Dim/normal row: "  {Label}{spaces}{count}{indicator}"
				label := p.Label + currentSuffix
				countPart := countStr + indicator
				// Compute padding to right-align count within textWidth.
				labelRunes := []rune(label)
				countRunes := []rune(countPart)
				pad := textWidth - len(labelRunes) - len(countRunes)
				if pad < 1 {
					pad = 1
				}
				sb.WriteString("  ")
				sb.WriteString(label)
				sb.WriteString(strings.Repeat(" ", pad))
				sb.WriteString(dimStyle.Render(countPart))
			}
			sb.WriteByte('\n')
		}

		if showBelow {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(len(provs)-windowEnd) + " more below"))
			sb.WriteByte('\n')
		}
	}

	// Footer.
	sb.WriteByte('\n')
	if m.loadError != "" {
		sb.WriteString(dimStyle.Render("esc cancel"))
	} else {
		sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  / search  esc cancel"))
	}

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

// viewModelsForProvider renders the Level-1 model list for the active provider.
func (m Model) viewModelsForProvider(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	var sb strings.Builder

	// Breadcrumb title: "< Back  [ProviderName]"
	sb.WriteString(dimStyle.Render("< Back"))
	sb.WriteString("  [" + m.activeProvider + "]")
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	visible := m.visibleModels() // already filtered to activeProvider at level 1
	if len(visible) == 0 {
		sb.WriteString(dimStyle.Render("No models available"))
		sb.WriteByte('\n')
	} else {
		// Compute scroll window for models (Issue #572).
		maxVisible := m.maxVisibleContentRows()
		windowStart := m.scrollOffset
		windowEnd := windowStart + maxVisible
		if windowEnd > len(visible) {
			windowEnd = len(visible)
		}
		// Reserve room for scroll indicators when items are hidden.
		showAbove := windowStart > 0
		showBelow := windowEnd < len(visible)
		if showAbove && windowEnd > windowStart {
			windowEnd--
		}
		if showBelow && windowEnd > windowStart {
			windowEnd--
		}

		if showAbove {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(windowStart) + " more above"))
			sb.WriteByte('\n')
		}

		for i := windowStart; i < windowEnd; i++ {
			entry := visible[i]
			isSelected := i == m.Selected
			isStarred := m.starred[entry.ID]
			isUnavailable := m.availabilitySet && !entry.Available

			// Build star prefix.
			var starPrefix string
			if isStarred {
				starPrefix = starStyle.Render("★") + " "
			} else {
				starPrefix = "  "
			}

			// Build key status suffix.
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
				padNeeded := innerWidth - 2 - len(runes)
				if padNeeded < 0 {
					padNeeded = 0
				}
				highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
				sb.WriteString(highlighted)
			} else if isUnavailable {
				sb.WriteString("  ")
				sb.WriteString(starPrefix)
				sb.WriteString(unavailableStyle.Render(entry.DisplayName))
				if entry.ReasoningMode {
					sb.WriteString(" ")
					sb.WriteString(unavailableStyle.Render("[R]"))
				}
				sb.WriteString(" ")
				sb.WriteString(unavailableSuffixStyle.Render("(unavailable)"))
				if entry.IsCurrent {
					sb.WriteString("  " + currentStyle.Render("← current"))
				}
				if keySuffix != "" {
					sb.WriteString(dimStyle.Render(keySuffix))
				}
			} else {
				sb.WriteString("  ")
				sb.WriteString(starPrefix)
				sb.WriteString(entry.DisplayName)
				if entry.ReasoningMode {
					sb.WriteString(" ")
					sb.WriteString(reasoningBadgeStyle.Render("[R]"))
				}
				if entry.IsCurrent {
					sb.WriteString("  " + currentStyle.Render("← current"))
				}
				if keySuffix != "" {
					sb.WriteString(dimStyle.Render(keySuffix))
				}
			}
			sb.WriteByte('\n')
		}

		if showBelow {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(len(visible)-windowEnd) + " more below"))
			sb.WriteByte('\n')
		}
	}

	// Footer.
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  s star  esc back"))

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

// viewFlatModelList renders a flat cross-provider search results list (any level when searchQuery != "").
func (m Model) viewFlatModelList(width int) string {
	if width <= 0 {
		width = 60
	}
	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	var sb strings.Builder

	// Title with search bar.
	sb.WriteString(titleStyle.Render("Switch Model"))
	sb.WriteByte('\n')
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("Filter: "))
	sb.WriteString(m.searchQuery)
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	// Error state.
	if m.loadError != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		sb.WriteString(errStyle.Render(m.loadError))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
		sb.WriteString(dimStyle.Render("esc cancel"))
		return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
	}

	if m.loading {
		sb.WriteString(dimStyle.Render("Loading models..."))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
	}

	visible := m.visibleModels()
	if len(visible) == 0 && !m.loading {
		sb.WriteString(dimStyle.Render("No models match"))
		sb.WriteByte('\n')
	} else {
		// Compute scroll window for search results (Issue #572).
		maxVisible := m.maxVisibleContentRows()
		windowStart := m.scrollOffset
		windowEnd := windowStart + maxVisible
		if windowEnd > len(visible) {
			windowEnd = len(visible)
		}
		// Reserve room for scroll indicators when items are hidden.
		showAbove := windowStart > 0
		showBelow := windowEnd < len(visible)
		if showAbove && windowEnd > windowStart {
			windowEnd--
		}
		if showBelow && windowEnd > windowStart {
			windowEnd--
		}

		if showAbove {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(windowStart) + " more above"))
			sb.WriteByte('\n')
		}

		for i := windowStart; i < windowEnd; i++ {
			entry := visible[i]
			isSelected := i == m.Selected
			isStarred := m.starred[entry.ID]
			isUnavailable := m.availabilitySet && !entry.Available

			// Provider prefix (dim).
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
				padNeeded := innerWidth - 2 - len(runes)
				if padNeeded < 0 {
					padNeeded = 0
				}
				highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
				sb.WriteString(highlighted)
			} else if isUnavailable {
				sb.WriteString("  ")
				sb.WriteString(starPrefix)
				sb.WriteString(dimStyle.Render(provPrefix))
				sb.WriteString(unavailableStyle.Render(entry.DisplayName))
				if entry.ReasoningMode {
					sb.WriteString(" ")
					sb.WriteString(unavailableStyle.Render("[R]"))
				}
				sb.WriteString(" ")
				sb.WriteString(unavailableSuffixStyle.Render("(unavailable)"))
				if entry.IsCurrent {
					sb.WriteString("  " + currentStyle.Render("← current"))
				}
				if keySuffix != "" {
					sb.WriteString(dimStyle.Render(keySuffix))
				}
			} else {
				sb.WriteString("  ")
				sb.WriteString(starPrefix)
				sb.WriteString(dimStyle.Render(provPrefix))
				sb.WriteString(entry.DisplayName)
				if entry.ReasoningMode {
					sb.WriteString(" ")
					sb.WriteString(reasoningBadgeStyle.Render("[R]"))
				}
				if entry.IsCurrent {
					sb.WriteString("  " + currentStyle.Render("← current"))
				}
				if keySuffix != "" {
					sb.WriteString(dimStyle.Render(keySuffix))
				}
			}
			sb.WriteByte('\n')
		}

		if showBelow {
			sb.WriteString(scrollIndicatorStyle.Render("... " + itoa(len(visible)-windowEnd) + " more below"))
			sb.WriteByte('\n')
		}
	}

	// Footer.
	sb.WriteByte('\n')
	if m.loadError != "" {
		sb.WriteString(dimStyle.Render("esc cancel"))
	} else {
		sb.WriteString(dimStyle.Render("↑/↓ navigate  enter select  esc cancel search"))
	}

	return boxStyle.Width(innerWidth).BorderForeground(lipgloss.Color("240")).Render(sb.String())
}

// itoa converts an int to its decimal string representation without importing strconv.
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

// viewReasoning renders the Level-1 reasoning effort selection list.
func (m Model) viewReasoning(width int) string {
	if width <= 0 {
		width = 60
	}

	const borderAndPad = 4
	innerWidth := width - borderAndPad
	if innerWidth < 20 {
		innerWidth = 20
	}

	// Look up the current model's display name from visible models.
	currentModelDisplayName := ""
	visible := m.visibleModels()
	if m.Selected >= 0 && m.Selected < len(visible) {
		currentModelDisplayName = visible[m.Selected].DisplayName
	}

	var sb strings.Builder

	// Title shows context of which model we are configuring.
	title := "Reasoning Effort  [" + currentModelDisplayName + "]"
	sb.WriteString(titleStyle.Render(title))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	for i, entry := range ReasoningLevels {
		isSelected := i == m.reasoningSelected
		isCurrent := entry.ID == m.currentReasoning

		var prefix string
		if isSelected {
			prefix = "> "
		} else {
			prefix = "  "
		}

		var currentPart string
		if isCurrent {
			currentPart = "  " + currentStyle.Render("← current")
		}

		if isSelected {
			nameAndSuffix := entry.DisplayName
			if isCurrent {
				nameAndSuffix += "  ← current"
			}
			runes := []rune(prefix + nameAndSuffix)
			padNeeded := innerWidth - 2 - len(runes)
			if padNeeded < 0 {
				padNeeded = 0
			}
			highlighted := highlightStyle.Render(string(runes) + strings.Repeat(" ", padNeeded))
			sb.WriteString(highlighted)
		} else {
			sb.WriteString("  ")
			sb.WriteString(entry.DisplayName)
			if isCurrent {
				sb.WriteString(currentPart)
			}
		}
		sb.WriteByte('\n')
	}

	// Footer hint.
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("↑/↓ navigate  enter confirm  esc back"))

	box := boxStyle.
		Width(innerWidth).
		BorderForeground(lipgloss.Color("240")).
		Render(sb.String())

	return box
}
