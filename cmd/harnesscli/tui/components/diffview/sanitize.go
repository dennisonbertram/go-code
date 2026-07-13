package diffview

import (
	"regexp"
	"strings"
)

// ansiEscapeRe matches ANSI CSI escape sequences (e.g. \x1b[32m, \x1b[2J).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// sanitizeText strips ANSI escape sequences and other C0/C1 control
// characters (preserving newline and tab) from diff content before it is
// parsed and rendered, so hostile tool output cannot move the cursor,
// recolor the terminal, or desync the wrap-width math. Invalid UTF-8 byte
// sequences are replaced with the Unicode replacement character.
func sanitizeText(s string) string {
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
