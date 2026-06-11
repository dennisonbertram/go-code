package modelswitcher_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestProviderCountsOnSameLine verifies that provider count labels like "(3)"
// appear on the same line as the provider name (not wrapped to separate lines).
// Regression test for issue #571.
func TestProviderCountsOnSameLine(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	v := m.View(80)

	lines := strings.Split(v, "\n")

	// Look for lines whose visible content is ONLY a count "(N)" —
	// these are wrapping artifacts where the count landed on its own line.
	for i, line := range lines {
		// Strip ANSI escape sequences for content inspection.
		clean := stripANSITest(line)
		trimmed := strings.TrimSpace(clean)
		if len(trimmed) == 0 {
			continue
		}
		// A count-only line: starts with "(", ends with ")", short.
		if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") &&
			utf8.RuneCountInString(trimmed) <= 4 { // "(3)", "(12)" etc
			t.Errorf("Line %d: provider count wrapped to separate line: %q\nFull view:\n%s", i, trimmed, v)
		}
	}

	// Verify each provider label has its count on the same line.
	provs := m.Providers()
	for _, p := range provs {
		countStr := "(" + itoa(p.Count) + ")"
		foundOnSameLine := false
		for _, line := range lines {
			if strings.Contains(line, p.Label) && strings.Contains(line, countStr) {
				foundOnSameLine = true
				break
			}
		}
		if !foundOnSameLine {
			t.Errorf("Provider %q count %q not on same line as label\nView:\n%s", p.Label, countStr, v)
		}
	}

	// Verify no visual line exceeds 80 display columns (use rune count,
	// not byte count, because box-drawing chars are multi-byte UTF-8).
	for i, line := range lines {
		colWidth := utf8.RuneCountInString(line)
		if colWidth > 80 {
			t.Errorf("Line %d exceeds 80 visual columns (%d columns): %q", i, colWidth, line)
		}
	}
}

// stripANSITest is a simple ANSI escape sequence stripper for test assertions.
func stripANSITest(s string) string {
	var b strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if inEscape {
			if s[i] >= '@' && s[i] <= '~' {
				inEscape = false
			}
			continue
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			inEscape = true
			i++ // skip '['
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
