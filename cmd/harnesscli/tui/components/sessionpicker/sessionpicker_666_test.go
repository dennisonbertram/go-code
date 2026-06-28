package sessionpicker_test

// Tests for #666 (sessionpicker component half):
//   - Selected row contains a leading text marker ("> " or "▶") in addition to color
//   - Footer navigation hint present ("↑/↓ navigate  enter select  esc cancel")

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/sessionpicker"
)

// TestTUI666_SessionpickerSelectedRowHasTextMarker verifies that the selected row
// contains a leading text marker ("> " or "▶") so selection is visible without color.
func TestTUI666_SessionpickerSelectedRowHasTextMarker(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()
	// Default selection is index 0.
	v := m.View(80)

	// Strip ANSI for plain-text check.
	plain := stripANSISession(v)
	lines := strings.Split(plain, "\n")

	// The selected row should contain the marker "> " or "▶".
	foundMarker := false
	for _, line := range lines {
		if strings.Contains(line, entries[0].ID[:8]) {
			// This is the selected row.
			if strings.Contains(line, "> ") || strings.Contains(line, "▶") {
				foundMarker = true
			}
			break
		}
	}
	if !foundMarker {
		t.Errorf("selected row for entry %q should contain '> ' or '▶' text marker; plain view:\n%s",
			entries[0].ID[:8], plain)
	}
}

// TestTUI666_SessionpickerUnselectedRowHasNoMarker verifies that unselected rows
// do NOT have the selection marker.
func TestTUI666_SessionpickerUnselectedRowHasNoMarker(t *testing.T) {
	entries := testEntries()
	m := sessionpicker.New(entries).Open()
	// Default selection is index 0; entry[1] is unselected.
	v := m.View(80)
	plain := stripANSISession(v)
	lines := strings.Split(plain, "\n")

	for _, line := range lines {
		if strings.Contains(line, entries[1].ID[:8]) {
			// This is an unselected row — should NOT start with "> " or "▶".
			if strings.HasPrefix(strings.TrimSpace(line), "> ") || strings.HasPrefix(strings.TrimSpace(line), "▶") {
				t.Errorf("unselected row should not have selection marker; line: %q", line)
			}
			break
		}
	}
}

// TestTUI666_SessionpickerFooterHintPresent verifies the footer navigation hint
// is present in the rendered session picker view.
func TestTUI666_SessionpickerFooterHintPresent(t *testing.T) {
	m := sessionpicker.New(testEntries()).Open()
	v := m.View(80)
	if !strings.Contains(v, "navigate") {
		t.Errorf("expected footer navigation hint in sessionpicker View(); got:\n%s", v)
	}
}

// stripANSISession removes ANSI escape codes for plain-text assertion.
func stripANSISession(s string) string {
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
