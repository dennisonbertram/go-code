package spinner

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// forceTrueColor makes styled output deterministic for ANSI assertions.
func forceTrueColor(t *testing.T) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}

// TestWithStyles_CustomDimColorInView verifies an injected style replaces the
// hardcoded faint styling on the active spinner line.
func TestWithStyles_CustomDimColorInView(t *testing.T) {
	forceTrueColor(t)
	m := New(1).Start()
	m = m.WithStyles(Styles{Dim: lipgloss.NewStyle().Foreground(lipgloss.Color("#123456"))})
	out := m.View(80)
	if !strings.Contains(out, "38;2;18;52;86") {
		t.Errorf("View() missing injected color: %q", out)
	}
}

// TestWithStyles_CompletionLineUsesInjectedStyle verifies the completion
// summary line is themed by the same injected style.
func TestWithStyles_CompletionLineUsesInjectedStyle(t *testing.T) {
	forceTrueColor(t)
	m := New(1).WithStyles(Styles{Dim: lipgloss.NewStyle().Foreground(lipgloss.Color("#123456"))})
	out := m.CompletionLine(5.0)
	if !strings.Contains(out, "38;2;18;52;86") {
		t.Errorf("CompletionLine() missing injected color: %q", out)
	}
}

// TestDefaultStylesMatchLegacyFaint pins the default: faint styling with no
// color, exactly the pre-injection hardcoded behavior.
func TestDefaultStylesMatchLegacyFaint(t *testing.T) {
	forceTrueColor(t)
	m := New(1).Start()
	out := m.View(80)
	if !strings.Contains(out, "\x1b[2m") {
		t.Errorf("default View() lost faint styling: %q", out)
	}
	if strings.Contains(out, "38;") {
		t.Errorf("default View() gained a foreground color: %q", out)
	}
}
