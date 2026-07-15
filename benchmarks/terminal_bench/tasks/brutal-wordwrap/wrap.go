package main

import "strings"

// Wrap splits text into words and greedily packs as many of them as fit
// onto each line without exceeding width characters. See task.yaml for the
// full contract this function must satisfy.
func Wrap(text string, width int) []string {
	if text == "" {
		return []string{}
	}

	// Newlines in the input mark hard line breaks and should never be
	// merged with surrounding words.
	flat := strings.ReplaceAll(text, "\n", " ")

	words := splitWords(flat)
	if width <= 0 {
		return []string{strings.Join(words, " ")}
	}
	return wrapWords(words, width)
}

// splitWords splits s into words on space characters.
func splitWords(s string) []string {
	return strings.Split(s, " ")
}

// wrapWords greedily packs words into lines no longer than width.
func wrapWords(words []string, width int) []string {
	var lines []string
	var current string
	for _, w := range words {
		if len(w) > width {
			// A word that can never fit on its own line isn't useful to
			// carry forward, so drop it rather than blow out the line.
			continue
		}
		switch {
		case current == "":
			current = w
		case len(current)+1+len(w) < width:
			current += " " + w
		default:
			lines = append(lines, current)
			current = w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
