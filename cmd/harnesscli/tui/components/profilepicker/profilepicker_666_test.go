package profilepicker_test

// Tests for #666 (profilepicker component half):
//   - Selected row contains a leading text marker ("> " or "▶") in addition to color/reverse

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/profilepicker"
)

// TestTUI666_ProfilepickerSelectedRowHasTextMarker verifies that the selected row
// contains a leading text marker ("> " or "▶") so selection is visible without color.
func TestTUI666_ProfilepickerSelectedRowHasTextMarker(t *testing.T) {
	entries := []profilepicker.ProfileEntry{
		{Name: "alpha", Description: "Alpha profile", Model: "gpt-4", ToolCount: 5, SourceTier: "project"},
		{Name: "beta", Description: "Beta profile", Model: "claude-opus-4-6", ToolCount: 10, SourceTier: "built-in"},
	}
	m := profilepicker.New(entries).Open()
	m.Width = 80
	v := m.View()

	plain := stripANSIProfile(v)
	lines := strings.Split(plain, "\n")

	// The first entry "alpha" is selected by default.
	foundMarker := false
	for _, line := range lines {
		if strings.Contains(line, "alpha") {
			if strings.Contains(line, "> ") || strings.Contains(line, "▶") {
				foundMarker = true
			}
			break
		}
	}
	if !foundMarker {
		t.Errorf("selected row for 'alpha' should contain '> ' or '▶' text marker; plain view:\n%s", plain)
	}
}

// TestTUI666_ProfilepickerUnselectedRowHasNoMarker verifies unselected rows do not
// have the selection marker.
func TestTUI666_ProfilepickerUnselectedRowHasNoMarker(t *testing.T) {
	entries := []profilepicker.ProfileEntry{
		{Name: "alpha", Description: "Alpha profile", Model: "gpt-4", ToolCount: 5, SourceTier: "project"},
		{Name: "beta", Description: "Beta profile", Model: "claude-opus-4-6", ToolCount: 10, SourceTier: "built-in"},
	}
	m := profilepicker.New(entries).Open()
	m.Width = 80
	v := m.View()
	plain := stripANSIProfile(v)
	lines := strings.Split(plain, "\n")

	// "beta" is unselected.
	for _, line := range lines {
		if strings.Contains(line, "beta") {
			trimmed := strings.TrimLeft(line, " ")
			if strings.HasPrefix(trimmed, "> ") || strings.HasPrefix(trimmed, "▶") {
				t.Errorf("unselected row 'beta' should not have selection marker; line: %q", line)
			}
			break
		}
	}
}

// TestTUI666_ProfilepickerFooterHintPresent verifies the footer navigation hint is
// already present in profilepicker (regression guard).
func TestTUI666_ProfilepickerFooterHintPresent(t *testing.T) {
	entries := []profilepicker.ProfileEntry{
		{Name: "alpha", Description: "Alpha", Model: "gpt-4", ToolCount: 1, SourceTier: "built-in"},
	}
	m := profilepicker.New(entries).Open()
	m.Width = 80
	v := m.View()
	if !strings.Contains(v, "navigate") {
		t.Errorf("expected footer navigation hint in profilepicker View(); got:\n%s", v)
	}
}

// stripANSIProfile removes ANSI escape codes for plain-text assertions.
func stripANSIProfile(s string) string {
	var out []rune
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && (runes[i] < 0x40 || runes[i] > 0x7e) {
				i++
			}
			if i < len(runes) {
				i++
			}
			continue
		}
		out = append(out, runes[i])
		i++
	}
	return string(out)
}
