package skills

import (
	"strings"
	"unicode"
)

// SplitArgs splits a skill argument string into tokens with shell-like,
// quote-aware semantics. It is the single tokenizer shared by every skill
// argument path (positional placeholders, named arguments, and plugin
// argument expansion), so all invocation surfaces agree on how an args
// string becomes tokens.
//
// Tokenization rules:
//   - Unicode whitespace separates tokens; consecutive whitespace collapses.
//   - Single quotes group literal text: everything up to the closing quote
//     is kept verbatim (backslash has no special meaning inside single quotes).
//   - Double quotes group text; inside them a backslash escapes the next
//     character (e.g. \" produces a literal quote).
//   - Outside quotes a backslash escapes the next character, so "\ " is a
//     literal space and "\\" is a literal backslash. A trailing backslash at
//     end of input is kept literally.
//   - Adjacent quoted and unquoted segments join into one token, so
//     --flag="some value" is a single token.
//   - An empty quoted segment yields an empty token (or extends the
//     current one); this applies to both quote styles.
//   - An unterminated quote is not an error: the rest of the string joins
//     the current token. Callers never need to handle a parse error.
//
// The error return is reserved for future extensions; SplitArgs currently
// always returns nil.
func SplitArgs(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	started := false // current token exists even if empty (handles "")

	flush := func() {
		if started {
			tokens = append(tokens, cur.String())
			cur.Reset()
			started = false
		}
	}

	runes := []rune(s)
	n := len(runes)
	i := 0
	for i < n {
		r := runes[i]
		switch {
		case r == '\\':
			started = true
			if i+1 < n {
				cur.WriteRune(runes[i+1])
				i += 2
			} else {
				cur.WriteRune(r) // trailing backslash stays literal
				i++
			}
		case r == '\'':
			started = true
			i++
			for i < n && runes[i] != '\'' {
				cur.WriteRune(runes[i])
				i++
			}
			if i < n {
				i++ // consume closing quote
			}
		case r == '"':
			started = true
			i++
			for i < n && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < n {
					cur.WriteRune(runes[i+1])
					i += 2
					continue
				}
				cur.WriteRune(runes[i])
				i++
			}
			if i < n {
				i++ // consume closing quote
			}
		case unicode.IsSpace(r):
			flush()
			i++
		default:
			started = true
			cur.WriteRune(r)
			i++
		}
	}
	flush()

	return tokens, nil
}
