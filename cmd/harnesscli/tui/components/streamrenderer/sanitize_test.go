package streamrenderer_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"go-agent-harness/cmd/harnesscli/tui/components/streamrenderer"
)

// TestSanitize_AppendDeltaStripsANSIAndControlChars verifies that raw ANSI
// escape sequences (color codes and a cursor-move/clear-screen sequence) and
// embedded control characters fed into AppendDelta never reach Content()/View().
func TestSanitize_AppendDeltaStripsANSIAndControlChars(t *testing.T) {
	sr := streamrenderer.New()
	sr.AppendDelta("\x1b[31mred\x1b[0m \x1b[2Jwiped\x07\x00 text")

	content := sr.Content()
	if strings.ContainsAny(content, "\x1b\x07\x00") {
		t.Fatalf("Content() must not contain raw escape/control bytes, got: %q", content)
	}
	if !strings.Contains(content, "red") || !strings.Contains(content, "wiped") || !strings.Contains(content, "text") {
		t.Fatalf("Content() should preserve printable text, got: %q", content)
	}

	view := sr.View(80)
	if strings.ContainsAny(view, "\x1b\x07\x00") {
		t.Fatalf("View() must not contain raw escape/control bytes, got: %q", view)
	}
	if !utf8.ValidString(view) {
		t.Fatalf("View() must be valid UTF-8, got: %q", view)
	}
}

// TestSanitize_AppendDeltaFixesInvalidUTF8 verifies that invalid UTF-8 byte
// sequences fed into AppendDelta are replaced rather than passed through raw.
func TestSanitize_AppendDeltaFixesInvalidUTF8(t *testing.T) {
	sr := streamrenderer.New()
	invalid := "before\xff\xfe" + string([]byte{0xc3, 0x28}) + "after"
	if utf8.ValidString(invalid) {
		t.Fatal("test fixture must itself be invalid UTF-8")
	}

	sr.AppendDelta(invalid)

	content := sr.Content()
	if !utf8.ValidString(content) {
		t.Fatalf("Content() must be valid UTF-8 after AppendDelta, got: %q", content)
	}
	if !strings.Contains(content, "before") || !strings.Contains(content, "after") {
		t.Fatalf("Content() should preserve surrounding valid text, got: %q", content)
	}
}

// TestSanitize_AppendThinkingDeltaStripsANSI verifies the thinking-chunk path
// is sanitized the same way as the main content path.
func TestSanitize_AppendThinkingDeltaStripsANSI(t *testing.T) {
	sr := streamrenderer.New()
	sr.StartThinking()
	sr.AppendThinkingDelta("\x1b[31mthinking\x1b[0m\x1b[2J")

	view := sr.View(80)
	if strings.Contains(view, "\x1b") {
		t.Fatalf("thinking view must not contain raw ESC bytes, got: %q", view)
	}
	if !strings.Contains(view, "thinking") {
		t.Fatalf("thinking view should preserve printable text, got: %q", view)
	}
}

// TestSanitize_WrapTextStripsANSIAndControlChars verifies that WrapText, the
// public wrap-width entry point used directly by other components, sanitizes
// its input before computing wrap widths.
func TestSanitize_WrapTextStripsANSIAndControlChars(t *testing.T) {
	text := "\x1b[31mred\x1b[0m \x1b[2J text\x00with\x07control chars"
	lines := streamrenderer.WrapText(text, 80)

	for _, line := range lines {
		if strings.ContainsAny(line, "\x1b\x00\x07") {
			t.Fatalf("WrapText() output must not contain raw escape/control bytes, got: %q", line)
		}
		if !utf8.ValidString(line) {
			t.Fatalf("WrapText() output must be valid UTF-8, got: %q", line)
		}
	}
}

// TestSanitize_WrapTextFixesInvalidUTF8 verifies WrapText does not desync its
// wrap-width math or emit raw bytes when fed invalid UTF-8.
func TestSanitize_WrapTextFixesInvalidUTF8(t *testing.T) {
	invalid := "abc\xffdef\xfeghi"
	lines := streamrenderer.WrapText(invalid, 80)

	for _, line := range lines {
		if !utf8.ValidString(line) {
			t.Fatalf("WrapText() output must be valid UTF-8, got: %q", line)
		}
	}
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "abc") || !strings.Contains(joined, "def") || !strings.Contains(joined, "ghi") {
		t.Fatalf("WrapText() should preserve surrounding valid text, got: %q", joined)
	}
}

// TestSanitize_PreservesNewlineAndTab verifies that the sanitizer keeps
// newline and tab characters (explicitly handled) while dropping other
// control characters.
func TestSanitize_PreservesNewlineAndTab(t *testing.T) {
	sr := streamrenderer.New()
	sr.AppendDelta("line1\n\tindented\x01line2")

	content := sr.Content()
	if !strings.Contains(content, "\n") {
		t.Errorf("newline should be preserved, got: %q", content)
	}
	if !strings.Contains(content, "\t") {
		t.Errorf("tab should be preserved, got: %q", content)
	}
	if strings.Contains(content, "\x01") {
		t.Errorf("other control chars should be stripped, got: %q", content)
	}
}
