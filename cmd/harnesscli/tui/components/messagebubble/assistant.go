package messagebubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"go-agent-harness/cmd/harnesscli/tui/components/streamrenderer"
)

// looksLikeMarkdown returns true if the text contains common markdown markers:
// headings (#), bold/italic (**), code (backtick), or table (|).
func looksLikeMarkdown(text string) bool {
	return strings.Contains(text, "# ") ||
		strings.Contains(text, " **") ||
		strings.Contains(text, "**") ||
		strings.ContainsRune(text, '`') ||
		strings.ContainsRune(text, '|')
}

const (
	assistantDotPrefix      = "⏺ "
	assistantBodyIndent     = "  "
	assistantDotPrefixRunes = 2 // rune count of "⏺ "
)

// dotStyle renders the ⏺ symbol in bright white (color 15).
var dotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

// titleStyle renders the optional title with bold + italic + underline.
var titleStyle = lipgloss.NewStyle().Bold(true).Italic(true).Underline(true)

// AssistantBubble renders an assistant message with ⏺ prefix.
//
// Rendering rules:
//   - If Title != "": first line is "⏺ " + styledTitle
//   - Content lines use WrapText(content, Width-2), each indented with "  " (2 spaces)
//   - No background fill (unlike UserBubble)
//   - Always ends with a blank line
type AssistantBubble struct {
	Title   string // optional bold/italic/underline title
	Content string // main response text
	Width   int    // available terminal width
}

// View renders the assistant bubble.
//
// The effective content width is Width-2 (to allow for 2-space indent).
// A blank trailing line is always appended at the end.
func (b AssistantBubble) View() string {
	width := b.Width
	if width <= 0 {
		width = 80
	}

	var sb strings.Builder

	dotRendered := dotStyle.Render("⏺")

	if b.Title != "" {
		// Title line: "⏺ " + styledTitle
		sb.WriteString(dotRendered)
		sb.WriteString(" ")
		sb.WriteString(titleStyle.Render(b.Title))
		sb.WriteString("\n")
	}

	// Content area: wrap at Width-2 to leave room for 2-space indent
	contentWidth := width - len([]rune(assistantBodyIndent))
	if contentWidth <= 0 {
		contentWidth = 1
	}

	if b.Content != "" {
		if looksLikeMarkdown(b.Content) {
			// Render via glamour; strip trailing newlines so we control spacing.
			rendered := RenderMarkdown(b.Content, width)
			rendered = strings.TrimRight(rendered, "\n")
			// Split rendered output into lines for prefix/indent handling.
			mdLines := strings.Split(rendered, "\n")
			for i, line := range mdLines {
				if b.Title == "" && i == 0 {
					sb.WriteString(dotRendered)
					sb.WriteString(" ")
					sb.WriteString(line)
				} else {
					sb.WriteString(assistantBodyIndent)
					sb.WriteString(line)
				}
				sb.WriteString("\n")
			}
		} else {
			lines := streamrenderer.WrapText(b.Content, contentWidth)
			for i, line := range lines {
				if b.Title == "" && i == 0 {
					// When there's no title, prefix the first content line with ⏺
					sb.WriteString(dotRendered)
					sb.WriteString(" ")
					sb.WriteString(line)
				} else {
					// Continuation lines: 2-space indent
					sb.WriteString(assistantBodyIndent)
					sb.WriteString(line)
				}
				sb.WriteString("\n")
			}
		}
	} else {
		// Empty content: render just the ⏺ line (if no title was rendered above)
		if b.Title == "" {
			sb.WriteString(dotRendered)
			sb.WriteString("\n")
		}
	}

	// Always append a blank trailing line
	sb.WriteString("\n")

	return sb.String()
}
