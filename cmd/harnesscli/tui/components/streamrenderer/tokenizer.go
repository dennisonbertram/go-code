package streamrenderer

import (
	"strings"
	"unicode/utf8"
)

// WrapText wraps text to the given width, respecting existing newlines.
// Returns a slice of lines (never nil -- at minimum []string{""} for empty input).
func WrapText(text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	text = sanitizeText(text)
	if text == "" {
		return []string{""}
	}

	var result []string
	for _, paragraph := range strings.Split(text, "\n") {
		result = append(result, wrapParagraph(paragraph, width)...)
	}
	return result
}

// WrapWithPrefix wraps text with a prefix on the first line,
// and indents continuation lines to align with prefix width.
func WrapWithPrefix(text, prefix string, width int) []string {
	prefixWidth := utf8.RuneCountInString(prefix)
	contentWidth := width - prefixWidth
	if contentWidth <= 0 {
		contentWidth = width
	}

	lines := WrapText(text, contentWidth)
	indent := strings.Repeat(" ", prefixWidth)

	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			result = append(result, prefix+line)
		} else {
			result = append(result, indent+line)
		}
	}
	return result
}

// wrapParagraph wraps a single paragraph (no embedded newlines) to width.
func wrapParagraph(text string, width int) []string {
	if utf8.RuneCountInString(text) <= width {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	var current []rune

	flushLine := func() {
		lines = append(lines, string(current))
		current = current[:0]
	}

	for _, word := range words {
		wordRunes := []rune(word)

		// Hard wrap if word itself exceeds width
		if len(wordRunes) > width {
			if len(current) > 0 {
				flushLine()
			}
			for len(wordRunes) > width {
				lines = append(lines, string(wordRunes[:width]))
				wordRunes = wordRunes[width:]
			}
			current = append(current[:0], wordRunes...)
			continue
		}

		// Would adding this word exceed width?
		needed := len(current)
		if needed > 0 {
			needed++ // space
		}
		needed += len(wordRunes)

		if needed > width && len(current) > 0 {
			flushLine()
		}

		if len(current) > 0 {
			current = append(current, ' ')
		}
		current = append(current, wordRunes...)
	}

	if len(current) > 0 {
		lines = append(lines, string(current))
	}

	return lines
}
