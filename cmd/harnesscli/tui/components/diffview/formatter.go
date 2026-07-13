package diffview

import "strings"

// Kind identifies the type of a diff line.
type Kind int

const (
	// KindContext is an unchanged context line (space prefix in unified diff).
	KindContext Kind = iota
	// KindAdd is an added line (+ prefix).
	KindAdd
	// KindRemove is a removed line (- prefix).
	KindRemove
	// KindHunk is a hunk header (@@ ... @@).
	KindHunk
	// KindHeader is a file header (--- / +++ / diff --git / index ...).
	KindHeader
)

// DiffLine represents a single parsed diff line.
type DiffLine struct {
	// LineNum is the original line number (0 if not applicable, e.g. hunk/header lines).
	LineNum int
	// Kind is the type of this line.
	Kind Kind
	// Text is the content without the leading +/-/@/space prefix.
	Text string
}

// Parse parses a unified diff string into a slice of DiffLines.
// Returns nil on empty input without error.
func Parse(diff string) []DiffLine {
	diff = sanitizeText(diff)
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	rawLines := strings.Split(diff, "\n")
	result := make([]DiffLine, 0, len(rawLines))

	// Track line numbers for context/remove/add lines within a hunk.
	// We track the "old" line number (left side) for removes/context, and
	// "new" line number (right side) for adds/context.
	oldLine := 0
	newLine := 0

	for _, raw := range rawLines {
		// Skip trailing empty lines that result from a trailing newline in the diff.
		if raw == "" {
			// Preserve blank context lines inside hunks but skip trailing empties
			// by checking whether we've started parsing.
			if len(result) == 0 {
				continue
			}
			// A blank raw line inside a diff is treated as context.
			result = append(result, DiffLine{
				LineNum: newLine,
				Kind:    KindContext,
				Text:    "",
			})
			newLine++
			oldLine++
			continue
		}

		switch {
		case strings.HasPrefix(raw, "@@"):
			// Parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
			oldLine, newLine = parseHunkHeader(raw)
			result = append(result, DiffLine{
				LineNum: 0,
				Kind:    KindHunk,
				Text:    raw,
			})

		case strings.HasPrefix(raw, "--- ") || strings.HasPrefix(raw, "+++ ") ||
			strings.HasPrefix(raw, "diff ") || strings.HasPrefix(raw, "index ") ||
			strings.HasPrefix(raw, "new file") || strings.HasPrefix(raw, "deleted file") ||
			strings.HasPrefix(raw, "old mode") || strings.HasPrefix(raw, "new mode"):
			result = append(result, DiffLine{
				LineNum: 0,
				Kind:    KindHeader,
				Text:    raw,
			})

		case strings.HasPrefix(raw, "+"):
			result = append(result, DiffLine{
				LineNum: newLine,
				Kind:    KindAdd,
				Text:    raw[1:],
			})
			newLine++

		case strings.HasPrefix(raw, "-"):
			result = append(result, DiffLine{
				LineNum: oldLine,
				Kind:    KindRemove,
				Text:    raw[1:],
			})
			oldLine++

		case strings.HasPrefix(raw, " "):
			result = append(result, DiffLine{
				LineNum: newLine,
				Kind:    KindContext,
				Text:    raw[1:],
			})
			newLine++
			oldLine++

		case strings.HasPrefix(raw, "\\"):
			// "\ No newline at end of file" — treat as header/meta
			result = append(result, DiffLine{
				LineNum: 0,
				Kind:    KindHeader,
				Text:    raw,
			})

		default:
			// Unrecognized line — treat as context to avoid dropping content.
			result = append(result, DiffLine{
				LineNum: newLine,
				Kind:    KindContext,
				Text:    raw,
			})
			newLine++
		}
	}

	return result
}

// parseHunkHeader extracts the starting line numbers from a hunk header like:
// "@@ -1,5 +1,7 @@"
// Returns (oldStart, newStart). Defaults to 1 on parse failure.
func parseHunkHeader(header string) (oldStart, newStart int) {
	oldStart = 1
	newStart = 1

	// Find the -N,M part and +N,M part
	parts := strings.Fields(header)
	for _, p := range parts {
		if strings.HasPrefix(p, "-") && len(p) > 1 {
			n := parseLineNum(p[1:])
			if n > 0 {
				oldStart = n
			}
		}
		if strings.HasPrefix(p, "+") && len(p) > 1 {
			n := parseLineNum(p[1:])
			if n > 0 {
				newStart = n
			}
		}
	}
	return oldStart, newStart
}

// parseLineNum parses "N" or "N,M" and returns N. Returns 0 on failure.
func parseLineNum(s string) int {
	// Strip comma and count portion
	if idx := strings.IndexByte(s, ','); idx >= 0 {
		s = s[:idx]
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// Clip returns at most maxLines lines from the diff, plus a bool indicating
// whether truncation occurred. If maxLines is 0, all lines are returned without
// truncation.
func Clip(lines []DiffLine, maxLines int) ([]DiffLine, bool) {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines, false
	}
	return lines[:maxLines], true
}
