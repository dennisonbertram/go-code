package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// profileFromString maps a config color-profile string to a termenv.Profile.
// The second return is false for "auto", empty, or unrecognised values, which
// signal "detect from the terminal" rather than a concrete override.
func profileFromString(s string) (termenv.Profile, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "truecolor", "24bit", "24-bit":
		return termenv.TrueColor, true
	case "256", "ansi256", "8bit", "8-bit":
		return termenv.ANSI256, true
	case "ansi", "16", "4bit", "4-bit":
		return termenv.ANSI, true
	case "none", "ascii", "off", "nocolor", "no-color":
		return termenv.Ascii, true
	default:
		return termenv.TrueColor, false
	}
}

// profileToString renders a termenv.Profile as its canonical config string.
func profileToString(p termenv.Profile) string {
	switch p {
	case termenv.ANSI256:
		return "256"
	case termenv.ANSI:
		return "ansi"
	case termenv.Ascii:
		return "none"
	default:
		return "truecolor"
	}
}

// DetectColorProfile returns the terminal's auto-detected color profile as a
// config string ("truecolor", "256", "ansi", or "none"). termenv's detection
// already honours NO_COLOR and non-terminal outputs.
func DetectColorProfile() string {
	return profileToString(lipgloss.ColorProfile())
}

// ApplyColorProfile resolves a color-profile preference and applies it to the
// default lipgloss renderer, returning the effective profile as a string for
// display.
//
// When pref names a concrete profile it is applied as an override (so, e.g.,
// "none" forces monochrome and "256" caps a truecolor terminal). When pref is
// "auto"/empty/unrecognised, the terminal's auto-detected profile is used
// unchanged — nothing is overridden, so lipgloss keeps its own detection.
func ApplyColorProfile(pref string) string {
	if p, ok := profileFromString(pref); ok {
		lipgloss.SetColorProfile(p)
		return profileToString(p)
	}
	return DetectColorProfile()
}
