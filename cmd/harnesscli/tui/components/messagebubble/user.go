package messagebubble

import (
	"strings"
	"unicode/utf8"

	"go-agent-harness/cmd/harnesscli/tui/components/streamrenderer"
)

const (
	userPromptPrefix    = "❯ "
	userContinueIndent  = "  "
	userPromptPrefixLen = 2 // rune count of "❯ "
)

// UserBubble renders a user message with ❯ prefix and dark-gray background fill.
//
// Rendering rules:
//   - Line 1:  "❯ {first line of content}" — background fills full Width
//   - Continuation: "  {next line}" — same background, 2-space indent
//   - Final line: always a blank line (no background) appended after the bubble
type UserBubble struct {
	Content string
	Width   int
	// Styles overrides the bubble styles when non-nil (theme injection
	// point, epic #810); nil uses DefaultStyles().
	Styles *Styles
}

// View renders the user bubble.
//
// The effective content width is Width - userPromptPrefixLen (2 runes for "❯ ").
// Each content line is padded with spaces to Width runes so the dark-gray
// background fills the full terminal width.
//
// An additional blank line is always appended at the end.
func (b UserBubble) View() string {
	width := b.Width
	if width <= 0 {
		width = 80
	}

	// Content area width leaves room for the prefix/indent
	contentWidth := width - userPromptPrefixLen
	if contentWidth <= 0 {
		contentWidth = 1
	}

	// Wrap the content into lines
	lines := streamrenderer.WrapText(b.Content, contentWidth)

	var sb strings.Builder
	for i, line := range lines {
		// Build the full line with prefix or indent
		var full string
		if i == 0 {
			full = userPromptPrefix + line
		} else {
			full = userContinueIndent + line
		}

		// Pad to the full width so the background fills the row
		runeLen := utf8.RuneCountInString(full)
		if runeLen < width {
			full += strings.Repeat(" ", width-runeLen)
		}

		// Apply the background style
		sb.WriteString(stylesOrDefault(b.Styles).User.Render(full))
		sb.WriteString("\n")
	}

	// Always append a blank trailing line (no background)
	sb.WriteString("\n")

	return sb.String()
}
