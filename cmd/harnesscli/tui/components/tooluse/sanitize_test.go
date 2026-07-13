package tooluse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitize_StripANSIRemovesControlCharsAndFixesUTF8 verifies that
// StripANSI, in addition to its original ANSI-escape stripping, also drops
// bare C0/C1 control characters (preserving newline/tab) and replaces
// invalid UTF-8 byte sequences with the Unicode replacement character.
func TestSanitize_StripANSIRemovesControlCharsAndFixesUTF8(t *testing.T) {
	hostile := "\x1b[31mred\x1b[0m\x1b[2J\x07\x00 text\n\tindented\x01line"
	out := StripANSI(hostile)

	if strings.ContainsAny(out, "\x1b\x07\x00\x01") {
		t.Fatalf("StripANSI() must strip raw escape/control bytes, got: %q", out)
	}
	if !strings.Contains(out, "red") || !strings.Contains(out, "text") || !strings.Contains(out, "indented") {
		t.Fatalf("StripANSI() should preserve printable text, got: %q", out)
	}
	if !strings.Contains(out, "\n") || !strings.Contains(out, "\t") {
		t.Fatalf("StripANSI() should preserve newline/tab, got: %q", out)
	}

	invalid := "before\xff\xfe" + string([]byte{0xc3, 0x28}) + "after"
	if utf8.ValidString(invalid) {
		t.Fatal("test fixture must itself be invalid UTF-8")
	}
	fixed := StripANSI(invalid)
	if !utf8.ValidString(fixed) {
		t.Fatalf("StripANSI() must return valid UTF-8, got: %q", fixed)
	}
	if !strings.Contains(fixed, "before") || !strings.Contains(fixed, "after") {
		t.Fatalf("StripANSI() should preserve surrounding valid text, got: %q", fixed)
	}
}

// TestSanitize_BashOutputViewHandlesHostileContent verifies that
// BashOutput.View() output is free of raw escape/control bytes and valid
// UTF-8 even when fed ANSI escapes, control chars, and invalid UTF-8.
func TestSanitize_BashOutputViewHandlesHostileContent(t *testing.T) {
	hostile := "\x1b[31mred\x1b[0m\x1b[2J\x07\x00 line\xffbad" + string([]byte{0xc3, 0x28})

	b := BashOutput{
		Command: "cat hostile.txt",
		Output:  hostile,
		Width:   80,
	}
	out := b.View()

	if strings.ContainsAny(out, "\x1b\x07\x00") {
		t.Fatalf("View() must not contain raw escape/control bytes, got: %q", out)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("View() must produce valid UTF-8, got: %q", out)
	}
}

// TestSanitize_TruncationIsRuneBoundaryAware verifies that the 512KB
// byte-truncation in BashOutput.View() never splits a multi-byte UTF-8
// sequence, even when the boundary falls in the middle of a repeated
// multi-byte rune.
func TestSanitize_TruncationIsRuneBoundaryAware(t *testing.T) {
	// U+00E9 ("é") encodes as the 2 bytes 0xC3 0xA9. Repeating it means the
	// naive byte-offset 512*1024 (an even number) will always land exactly
	// on a rune boundary for this rune width, so instead build the output
	// out of a 3-byte rune (U+20AC "€") whose width does not evenly divide
	// the 512KB cutoff, forcing the boundary to fall mid-rune under naive
	// byte slicing.
	euro := "€" // 3 bytes: 0xE2 0x82 0xAC
	repeatCount := (512*1024)/len(euro) + 10
	hostile := strings.Repeat(euro, repeatCount)

	b := BashOutput{Output: hostile, Width: 80}
	out := b.View()

	if !utf8.ValidString(out) {
		t.Fatalf("View() must produce valid UTF-8 after truncating a repeating multi-byte rune, got invalid output of length %d", len(out))
	}
	if !strings.Contains(out, "truncated at 512KB") {
		t.Fatalf("expected truncation hint in output, got: %q", out)
	}
	if strings.Contains(out, "�") {
		t.Fatalf("truncation must not split a multi-byte rune (no replacement char expected), got replacement char in output")
	}
}
