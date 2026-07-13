package diffview

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitize_ViewStripsANSIAndControlChars verifies that raw ANSI escape
// sequences (color codes and a cursor-move/clear-screen sequence) and
// embedded control characters embedded in a diff never reach the rendered
// view.
func TestSanitize_ViewStripsANSIAndControlChars(t *testing.T) {
	hostile := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,1 @@\n" +
		"-\x1b[31mold\x1b[0m\n" +
		"+\x1b[2Jnew\x07\x00 line"

	v := View{Diff: hostile, Width: 80}
	out := v.Render()

	if strings.ContainsAny(out, "\x1b\x07\x00") {
		t.Fatalf("Render() must not contain raw escape/control bytes, got: %q", out)
	}
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") {
		t.Fatalf("Render() should preserve printable diff text, got: %q", out)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("Render() must produce valid UTF-8, got: %q", out)
	}
}

// TestSanitize_ViewFixesInvalidUTF8 verifies that invalid UTF-8 byte
// sequences embedded in a diff are replaced rather than passed through raw.
func TestSanitize_ViewFixesInvalidUTF8(t *testing.T) {
	invalid := "--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n" +
		"+before\xff\xfe" + string([]byte{0xc3, 0x28}) + "after"
	if utf8.ValidString(invalid) {
		t.Fatal("test fixture must itself be invalid UTF-8")
	}

	v := View{Diff: invalid, Width: 80}
	out := v.Render()

	if !utf8.ValidString(out) {
		t.Fatalf("Render() must produce valid UTF-8, got: %q", out)
	}
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Fatalf("Render() should preserve surrounding valid text, got: %q", out)
	}
}

// TestSanitize_ParsePreservesNewlineStructure verifies that sanitization
// does not disturb the line-based diff parsing (only control chars other
// than newline/tab are stripped).
func TestSanitize_ParsePreservesNewlineStructure(t *testing.T) {
	diff := "--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n+ok\x01line"
	lines := Parse(diff)

	found := false
	for _, l := range lines {
		if l.Kind == KindAdd {
			found = true
			if strings.Contains(l.Text, "\x01") {
				t.Errorf("expected control char stripped from add line, got: %q", l.Text)
			}
			if !strings.Contains(l.Text, "ok") || !strings.Contains(l.Text, "line") {
				t.Errorf("expected printable text preserved, got: %q", l.Text)
			}
		}
	}
	if !found {
		t.Fatal("expected at least one KindAdd line")
	}
}
