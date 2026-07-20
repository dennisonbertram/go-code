package streamrenderer

import "github.com/charmbracelet/lipgloss"

// Emphasis constants for styled text segments.
type Emphasis int

const (
	EmphasisNone Emphasis = iota
	EmphasisBold
	EmphasisItalic
	EmphasisUnderline
	EmphasisBoldItalic
	EmphasisCode
)

// emphasisStyles maps each Emphasis constant to a pre-built lipgloss style.
// Package-level vars are safe for concurrent use since lipgloss.Style is a value type.
var emphasisStyles = map[Emphasis]lipgloss.Style{
	EmphasisNone:       lipgloss.NewStyle(),
	EmphasisBold:       lipgloss.NewStyle().Bold(true),
	EmphasisItalic:     lipgloss.NewStyle().Italic(true),
	EmphasisUnderline:  lipgloss.NewStyle().Underline(true),
	EmphasisBoldItalic: lipgloss.NewStyle().Bold(true).Italic(true),
	EmphasisCode:       lipgloss.NewStyle().Background(lipgloss.Color("238")).Foreground(lipgloss.Color("252")),
}

// ApplyEmphasis applies the given emphasis to text using lipgloss.
// Unknown emphasis values fall back to EmphasisNone (no styling).
func ApplyEmphasis(text string, e Emphasis) string {
	style, ok := emphasisStyles[e]
	if !ok {
		style = emphasisStyles[EmphasisNone]
	}
	return style.Render(text)
}
