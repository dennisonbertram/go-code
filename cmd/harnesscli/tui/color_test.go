package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestProfileFromString(t *testing.T) {
	t.Parallel()
	concrete := map[string]termenv.Profile{
		"truecolor": termenv.TrueColor,
		"TrueColor": termenv.TrueColor,
		"256":       termenv.ANSI256,
		"ansi256":   termenv.ANSI256,
		"ansi":      termenv.ANSI,
		"16":        termenv.ANSI,
		"none":      termenv.Ascii,
		"ascii":     termenv.Ascii,
		"no-color":  termenv.Ascii,
	}
	for in, want := range concrete {
		got, ok := profileFromString(in)
		if !ok {
			t.Errorf("profileFromString(%q): expected ok=true", in)
			continue
		}
		if got != want {
			t.Errorf("profileFromString(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "auto", "  ", "rainbow", "16m"} {
		if _, ok := profileFromString(in); ok {
			t.Errorf("profileFromString(%q): expected ok=false (auto/unknown)", in)
		}
	}
}

func TestProfileToStringRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"truecolor", "256", "ansi", "none"} {
		p, ok := profileFromString(s)
		if !ok {
			t.Fatalf("profileFromString(%q) not ok", s)
		}
		if got := profileToString(p); got != s {
			t.Errorf("round trip %q -> %v -> %q", s, p, got)
		}
	}
}

// TestApplyColorProfile_Override applies a concrete profile and verifies both
// the returned string and that the renderer's global profile changed. Global
// lipgloss state is saved and restored so other tests are unaffected.
func TestApplyColorProfile_Override(t *testing.T) {
	orig := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })

	if got := ApplyColorProfile("none"); got != "none" {
		t.Errorf("ApplyColorProfile(none) = %q, want none", got)
	}
	if lipgloss.ColorProfile() != termenv.Ascii {
		t.Errorf("override not applied to renderer: %v", lipgloss.ColorProfile())
	}

	if got := ApplyColorProfile("256"); got != "256" {
		t.Errorf("ApplyColorProfile(256) = %q, want 256", got)
	}
	if lipgloss.ColorProfile() != termenv.ANSI256 {
		t.Errorf("override not applied to renderer: %v", lipgloss.ColorProfile())
	}
}

// TestApplyColorProfile_AutoDoesNotPanic verifies the auto path returns a valid
// profile string and leaves the renderer's detection in place.
func TestApplyColorProfile_AutoDoesNotPanic(t *testing.T) {
	orig := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })

	got := ApplyColorProfile("auto")
	switch got {
	case "truecolor", "256", "ansi", "none":
		// valid
	default:
		t.Errorf("ApplyColorProfile(auto) returned unexpected %q", got)
	}
}
