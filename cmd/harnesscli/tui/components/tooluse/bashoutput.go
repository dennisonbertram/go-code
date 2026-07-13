package tooluse

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiEscapeRe matches ANSI CSI escape sequences (e.g. \x1b[32m, \x1b[0m).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// StripANSI removes ANSI CSI escape sequences and other C0/C1 control
// characters (preserving newline and tab) from s, replacing any invalid
// UTF-8 byte sequences with the Unicode replacement character. This guards
// the terminal against hostile tool output that could otherwise move the
// cursor, recolor the screen, or corrupt the display.
func StripANSI(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

const (
	// defaultMaxLines is the number of output lines shown before truncation
	// when MaxLines is zero.
	defaultMaxLines = 10

	// expandHint is appended to the last visible line when output is truncated.
	expandHint = "ctrl+o to expand"
)

// BashOutput renders shell command output with optional truncation.
//
// Rendering format:
//
//	⎿  $ echo hello        ← command label (omitted when Command is empty)
//	⎿  hello               ← each output line up to MaxLines
//	⎿  +3 more lines (ctrl+o to expand)   ← truncation hint when output exceeds MaxLines
type BashOutput struct {
	// Command is the shell command that was run. When non-empty, a "$ {command}"
	// label is prepended as the first tree line.
	Command string
	// Output is the combined stdout/stderr. ANSI escape codes are stripped
	// before rendering.
	Output string
	// MaxLines is the maximum number of output lines to display before
	// truncation. 0 means use the default of 10.
	MaxLines int
	// Width is the available terminal width. 0 defaults to 80.
	Width int
}

// View renders the BashOutput component as a multi-line string.
func (b BashOutput) View() string {
	width := b.Width
	if width <= 0 {
		width = defaultWidth
	}

	maxLines := b.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}

	// Guard against OOM from extremely large outputs.
	output := b.Output
	if len(output) > 512*1024 {
		limit := 512 * 1024
		for limit > 0 && !utf8.RuneStart(output[limit]) {
			limit--
		}
		output = output[:limit] + "\n[output truncated at 512KB]"
	}

	// Strip ANSI from output before processing.
	clean := StripANSI(output)

	var sb strings.Builder

	// Emit the command label line if Command is non-empty.
	if b.Command != "" {
		sb.WriteString(renderTreeLine("$ "+b.Command, width))
		sb.WriteString("\n")
	}

	// Split output into lines; skip trailing empty line from a trailing newline.
	outputLines := splitLines(clean)

	totalLines := len(outputLines)

	// Determine how many lines to display.
	displayLines := outputLines
	truncated := false
	if totalLines > maxLines {
		displayLines = outputLines[:maxLines]
		truncated = true
	}

	for _, line := range displayLines {
		sb.WriteString(renderTreeLine(line, width))
		sb.WriteString("\n")
	}

	if truncated {
		remaining := totalLines - maxLines
		hint := fmt.Sprintf("+%d more lines (%s)", remaining, expandHint)
		sb.WriteString(renderTreeLine(hint, width))
		sb.WriteString("\n")
	}

	return sb.String()
}

// splitLines splits s on newlines. A single trailing newline does not produce
// a spurious empty trailing element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	// Trim a single trailing newline to avoid a phantom empty line.
	trimmed := strings.TrimRight(s, "\n")
	return strings.Split(trimmed, "\n")
}
