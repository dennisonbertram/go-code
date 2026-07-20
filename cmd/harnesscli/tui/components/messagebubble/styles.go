package messagebubble

import "github.com/charmbracelet/lipgloss"

// Styles groups the styles used to render message bubbles. Obtain defaults
// via DefaultStyles() and override individual fields (theme injection point,
// epic #810).
type Styles struct {
	// User renders user message lines (background fill + foreground text).
	User lipgloss.Style
	// AssistantDot renders the ⏺ glyph prefixing assistant messages.
	AssistantDot lipgloss.Style
	// Title renders the optional assistant title line.
	Title lipgloss.Style
}

// DefaultStyles returns the styles the bubbles used before theming: user
// bubble bg 237 / fg 252, assistant dot bright white (color 15), title
// bold+italic+underline.
func DefaultStyles() Styles {
	return Styles{
		User: lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("252")),
		AssistantDot: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		Title:        lipgloss.NewStyle().Bold(true).Italic(true).Underline(true),
	}
}

func stylesOrDefault(s *Styles) Styles {
	if s == nil {
		return DefaultStyles()
	}
	return *s
}
