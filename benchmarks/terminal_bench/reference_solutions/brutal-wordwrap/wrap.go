package main

import "strings"

// Wrap splits text into words (runs of spaces/tabs are treated as a single
// separator; leading/trailing runs produce no empty words) and greedily
// packs as many words as fit onto each line without exceeding width
// characters, joining words with a single space.
//
// A '\n' byte always forces a hard line break. Wrap("", width) returns an
// empty slice. If width <= 0, wrapping is disabled and each hard line
// becomes exactly one (unwrapped) output line. A hard line with no words
// becomes an empty string ("") line. See task.yaml for the full contract.
func Wrap(text string, width int) []string {
	if text == "" {
		return []string{}
	}

	var lines []string
	for _, hardLine := range strings.Split(text, "\n") {
		words := splitWords(hardLine)
		switch {
		case len(words) == 0:
			lines = append(lines, "")
		case width <= 0:
			lines = append(lines, strings.Join(words, " "))
		default:
			lines = append(lines, wrapWords(words, width)...)
		}
	}
	return lines
}

// splitWords splits s into words on runs of spaces/tabs, discarding any
// empty tokens produced by leading, trailing, or repeated separators.
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t'
	})
}

// wrapWords greedily packs words into lines no longer than width. A word
// longer than width simply becomes its own line: it is assigned to an empty
// current line, and the next word (if any) will not fit alongside it, so it
// is flushed on its own without ever being split, truncated, or dropped.
func wrapWords(words []string, width int) []string {
	var lines []string
	var current string
	for _, w := range words {
		switch {
		case current == "":
			current = w
		case len(current)+1+len(w) <= width:
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
