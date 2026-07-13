package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SymbolSet holds all Unicode symbols used across TUI components.
type SymbolSet struct {
	Dot     string   // ⏺  tool call / list bullet
	Tree    string   // ⎿  tree connector / continuation
	Arrow   string   // ❯  prompt / active indicator
	Check   string   // ✓  success
	Cross   string   // ✗  failure / error
	Spinner []string // 6-frame braille spinner
}

// Symbols is the global symbol set used by all components.
var Symbols = SymbolSet{
	Dot:     "⏺",
	Tree:    "⎿",
	Arrow:   "❯",
	Check:   "✓",
	Cross:   "✗",
	Spinner: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟"},
}

// Theme holds all lipgloss styles for the TUI.
type Theme struct {
	// Message styles
	UserMsgStyle      lipgloss.Style
	AssistantMsgStyle lipgloss.Style
	ThinkingStyle     lipgloss.Style

	// Tool use styles
	ToolNameStyle   lipgloss.Style
	ToolResultStyle lipgloss.Style
	ToolInputStyle  lipgloss.Style

	// Status and metadata
	StatusBarStyle   lipgloss.Style
	StatusModelStyle lipgloss.Style
	CostStyle        lipgloss.Style
	TimingStyle      lipgloss.Style

	// Typography
	DimStyle    lipgloss.Style
	BoldStyle   lipgloss.Style
	CodeStyle   lipgloss.Style
	ItalicStyle lipgloss.Style

	// Diff viewer
	DiffAddStyle    lipgloss.Style
	DiffRemoveStyle lipgloss.Style
	DiffHunkStyle   lipgloss.Style

	// Alerts
	ErrorStyle   lipgloss.Style
	WarningStyle lipgloss.Style
	SuccessStyle lipgloss.Style

	// Input area
	InputStyle       lipgloss.Style
	InputPromptStyle lipgloss.Style

	// Borders and separators
	SeparatorStyle lipgloss.Style
	BorderStyle    lipgloss.Style
}

// DefaultTheme returns the standard color theme based on Claude Code UX research.
func DefaultTheme() Theme {
	// Colors from claude-code-ux-chat-streaming.md
	subtle := lipgloss.AdaptiveColor{Light: "#D9D9D9", Dark: "#383838"}
	highlight := lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special := lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	dimColor := lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	errorColor := lipgloss.AdaptiveColor{Light: "#FF5F87", Dark: "#FF5F87"}
	warningColor := lipgloss.AdaptiveColor{Light: "#FFAF00", Dark: "#FFAF00"}
	addColor := lipgloss.AdaptiveColor{Light: "#23A244", Dark: "#23A244"}
	removeColor := lipgloss.AdaptiveColor{Light: "#E05252", Dark: "#E05252"}

	return Theme{
		UserMsgStyle:      lipgloss.NewStyle().Bold(true),
		AssistantMsgStyle: lipgloss.NewStyle(),
		ThinkingStyle:     lipgloss.NewStyle().Foreground(dimColor).Italic(true),

		ToolNameStyle:   lipgloss.NewStyle().Bold(true).Foreground(highlight),
		ToolResultStyle: lipgloss.NewStyle().Foreground(dimColor),
		ToolInputStyle:  lipgloss.NewStyle().Foreground(subtle),

		StatusBarStyle:   lipgloss.NewStyle().Faint(true),
		StatusModelStyle: lipgloss.NewStyle().Bold(true),
		CostStyle:        lipgloss.NewStyle().Foreground(dimColor).Faint(true),
		TimingStyle:      lipgloss.NewStyle().Foreground(dimColor).Faint(true),

		DimStyle:    lipgloss.NewStyle().Faint(true),
		BoldStyle:   lipgloss.NewStyle().Bold(true),
		CodeStyle:   lipgloss.NewStyle().Background(subtle),
		ItalicStyle: lipgloss.NewStyle().Italic(true),

		DiffAddStyle:    lipgloss.NewStyle().Foreground(addColor),
		DiffRemoveStyle: lipgloss.NewStyle().Foreground(removeColor),
		DiffHunkStyle:   lipgloss.NewStyle().Foreground(dimColor).Faint(true),

		ErrorStyle:   lipgloss.NewStyle().Foreground(errorColor).Bold(true),
		WarningStyle: lipgloss.NewStyle().Foreground(warningColor),
		SuccessStyle: lipgloss.NewStyle().Foreground(special),

		InputStyle:       lipgloss.NewStyle(),
		InputPromptStyle: lipgloss.NewStyle().Foreground(highlight),

		SeparatorStyle: lipgloss.NewStyle().Foreground(dimColor),
		BorderStyle:    lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(subtle),
	}
}

// SeparatorFor returns a themed separator string for the given width.
// Call from components instead of constructing separators directly.
func (t Theme) SeparatorFor(width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Faint(true).Render(strings.Repeat("\u2500", width))
}

// ClampWidth returns w clamped to [min, max].
// Used to prevent negative padding or zero-width renders.
func (t Theme) ClampWidth(w, min, max int) int {
	if w < min {
		return min
	}
	if w > max {
		return max
	}
	return w
}
