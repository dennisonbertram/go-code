package diffview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	// defaultWidth is used when Width is 0.
	defaultWidth = 80
	// defaultMaxLines is the practical default truncation limit.
	defaultMaxLines = 40
	// borderChar is the ╌ (U+254C) dashed box drawing character.
	borderChar = "\u254c"
)

// Styles groups the styles used to render a diff view. Obtain defaults via
// DefaultStyles() and override individual fields.
type Styles struct {
	Add    lipgloss.Style // added lines
	Remove lipgloss.Style // removed lines
	Hunk   lipgloss.Style // hunk headers and file headers
	Border lipgloss.Style // dashed ╌ border rule
}

// DefaultStyles returns the palette diffview used before theming: #23A244
// adds, #E05252 removes, dim hunks, faint border.
func DefaultStyles() Styles {
	return Styles{
		Add:    lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#23A244", Dark: "#23A244"}),
		Remove: lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#E05252", Dark: "#E05252"}),
		Hunk:   lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}).Faint(true),
		Border: lipgloss.NewStyle().Faint(true),
	}
}

// View renders a diff inside a ╌ bordered box.
// MaxLines controls truncation (0 = no limit).
// Width controls the rendering width (0 defaults to 80).
type View struct {
	// Diff is the raw unified diff string.
	Diff string
	// MaxLines controls the maximum number of diff lines rendered (0 = unlimited).
	MaxLines int
	// Width is the terminal rendering width (0 defaults to 80).
	Width int
	// Styles overrides the render palette when non-nil (theme injection
	// point, epic #810); nil uses DefaultStyles().
	Styles *Styles
}

func (v View) stylesOrDefault() Styles {
	if v.Styles == nil {
		return DefaultStyles()
	}
	return *v.Styles
}

// Render formats the diff as a bordered box string.
//
// Format:
//
//	╌╌╌ path/to/file ╌╌╌
//	  1 │ context line
//	+ 2 │ added line
//	- 3 │ removed line
//	@@ -4,3 +4,5 @@
//	╌╌╌ [+12 more lines] ╌╌╌
func (v View) Render() string {
	width := v.Width
	if width <= 0 {
		width = defaultWidth
	}

	lines := Parse(v.Diff)
	if len(lines) == 0 {
		return ""
	}

	// Extract filename from header lines for the title.
	filename := extractFilename(lines)

	// Apply truncation.
	maxLines := v.MaxLines
	clipped, truncated := Clip(lines, maxLines)

	// Build styles (stateless — safe for concurrent use).
	styles := v.stylesOrDefault()
	addStyle := styles.Add
	removeStyle := styles.Remove
	hunkStyle := styles.Hunk
	borderStyle := styles.Border

	var sb strings.Builder

	// ╌╌╌ filename ╌╌╌ header
	header := buildBorderLine(filename, width, borderStyle)
	sb.WriteString(header)
	sb.WriteByte('\n')

	// Render each clipped line.
	for _, dl := range clipped {
		rendered := renderLine(dl, addStyle, removeStyle, hunkStyle)
		sb.WriteString(rendered)
		sb.WriteByte('\n')
	}

	// Truncation footer.
	if truncated {
		remaining := len(lines) - len(clipped)
		footer := buildBorderLine(fmt.Sprintf("[+%d more lines]", remaining), width, borderStyle)
		sb.WriteString(footer)
		sb.WriteByte('\n')
	}

	return sb.String()
}

// renderLine formats a single DiffLine with prefix and styling.
func renderLine(dl DiffLine, addStyle, removeStyle, hunkStyle lipgloss.Style) string {
	switch dl.Kind {
	case KindAdd:
		content := fmt.Sprintf("+ %d \u2502 %s", dl.LineNum, dl.Text)
		return addStyle.Render(content)

	case KindRemove:
		content := fmt.Sprintf("- %d \u2502 %s", dl.LineNum, dl.Text)
		return removeStyle.Render(content)

	case KindHunk:
		return hunkStyle.Render(dl.Text)

	case KindHeader:
		return hunkStyle.Render(dl.Text)

	default: // KindContext
		return fmt.Sprintf("  %d \u2502 %s", dl.LineNum, dl.Text)
	}
}

// buildBorderLine builds a ╌╌╌ label ╌╌╌ border line at the given width.
// If the label is empty, returns a full border line.
func buildBorderLine(label string, width int, style lipgloss.Style) string {
	if label == "" {
		dashes := strings.Repeat(borderChar, max(width, 3))
		return style.Render(dashes)
	}

	// Format: "╌╌╌ label ╌╌╌"
	inner := " " + label + " "
	// Minimum: 3 dashes on each side
	minDashes := 3
	totalDashes := width - len([]rune(inner))
	if totalDashes < minDashes*2 {
		totalDashes = minDashes * 2
	}
	leftDashes := totalDashes / 2
	rightDashes := totalDashes - leftDashes

	line := strings.Repeat(borderChar, leftDashes) + inner + strings.Repeat(borderChar, rightDashes)
	return style.Render(line)
}

// extractFilename returns the file path from the +++ b/path header line.
// Falls back to the --- a/path header if +++ is not found.
// Returns "" if no header lines exist.
func extractFilename(lines []DiffLine) string {
	plusFile := ""
	minusFile := ""

	for _, dl := range lines {
		if dl.Kind != KindHeader {
			continue
		}
		text := dl.Text
		if strings.HasPrefix(text, "+++ b/") {
			plusFile = text[6:] // strip "+++ b/"
			break
		}
		if strings.HasPrefix(text, "+++ ") {
			plusFile = text[4:] // strip "+++ "
			break
		}
		if strings.HasPrefix(text, "--- b/") && minusFile == "" {
			minusFile = text[6:]
		}
		if strings.HasPrefix(text, "--- a/") && minusFile == "" {
			minusFile = text[6:]
		}
		if strings.HasPrefix(text, "--- ") && minusFile == "" {
			minusFile = text[4:]
		}
	}

	if plusFile != "" {
		return plusFile
	}
	return minusFile
}

// max returns the larger of two ints.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
