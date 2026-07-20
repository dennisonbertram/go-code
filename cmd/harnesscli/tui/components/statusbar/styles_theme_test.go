package statusbar

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

// TestSetStyles_CustomWarnColorsWarningSegments verifies that an injected
// Styles replaces the package-default warn style on warning segments
// (permission mode, MCP failures, high context usage).
func TestSetStyles_CustomWarnColorsWarningSegments(t *testing.T) {
	forceTrueColor(t)
	m := New(120)
	m.SetPermMode("plan")
	m.SetStyles(Styles{
		Dim:  lipgloss.NewStyle().Faint(true),
		Bold: lipgloss.NewStyle().Bold(true),
		Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")),
	})
	out := m.View()
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("View() with injected warn color missing red fg sequence: %q", out)
	}
	if !strings.Contains(out, "[plan]") {
		t.Errorf("View() missing permission-mode segment: %q", out)
	}
}

// TestSetStyles_CustomDimAndBold verifies dim/bold segment styling comes from
// the injected Styles, not the package defaults.
func TestSetStyles_CustomDimAndBold(t *testing.T) {
	forceTrueColor(t)
	m := New(120)
	m.SetModel("gpt-5")
	m.SetBranch("main")
	m.SetStyles(Styles{
		Dim:  lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")),
		Bold: lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")).Bold(true),
		Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAF00")),
	})
	out := m.View()
	if !strings.Contains(out, "38;2;0;0;255") {
		t.Errorf("View() missing injected bold blue for model segment: %q", out)
	}
	if !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("View() missing injected dim green for branch segment: %q", out)
	}
}

// TestZeroValueModelUsesDefaultStyles verifies a Model constructed without
// New/SetStyles still renders with the default styles (faint separators,
// amber warnings) instead of zero styles.
func TestZeroValueModelUsesDefaultStyles(t *testing.T) {
	forceTrueColor(t)
	var m Model
	m.SetPermMode("plan")
	m.SetWidth(80)
	out := m.View()
	if !strings.Contains(out, "[plan]") {
		t.Errorf("zero-value View() missing segment: %q", out)
	}
	// Default warn color #FFAF00 = rgb(255,175,0).
	if !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("zero-value View() missing default warn color: %q", out)
	}
}

// TestDefaultStylesMatchLegacyRendering pins the default styles to the
// pre-injection hardcoded values (faint, bold, #FFAF00).
func TestDefaultStylesMatchLegacyRendering(t *testing.T) {
	forceTrueColor(t)
	m := New(120)
	m.SetModel("gpt-5")
	m.SetPermMode("plan")
	out := m.View()
	if !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("default warn color drifted: %q", out)
	}
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("default bold attribute missing for model segment: %q", out)
	}
}
