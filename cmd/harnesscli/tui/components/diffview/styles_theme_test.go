package diffview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const stylesTestDiff = `--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,2 @@
 context
-old line
+new line
`

// forceTrueColor makes styled output deterministic for ANSI assertions.
func forceTrueColor(t *testing.T) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}

func customStyles() *Styles {
	return &Styles{
		Add:    lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")),
		Remove: lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")),
		Hunk:   lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")),
		Border: lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00")),
	}
}

// TestViewRender_CustomStylesChangeColors verifies injected styles replace
// the inline default colors on every diff line kind and the border rule.
func TestViewRender_CustomStylesChangeColors(t *testing.T) {
	forceTrueColor(t)
	v := View{Diff: stylesTestDiff, Width: 60, Styles: customStyles()}
	out := v.Render()

	if !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("add line missing custom green: %q", out)
	}
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("remove line missing custom red: %q", out)
	}
	if !strings.Contains(out, "38;2;0;0;255") {
		t.Errorf("hunk/header line missing custom blue: %q", out)
	}
	if !strings.Contains(out, "38;2;255;255;0") {
		t.Errorf("border rule missing custom yellow: %q", out)
	}
	// Default add color (#23A244 = 35,162,68) must be gone.
	if strings.Contains(out, "38;2;35;162;68") {
		t.Errorf("default add color still present with custom styles: %q", out)
	}
}

// TestModelView_PassesStylesThrough verifies the Model wrapper forwards its
// Styles to the shared renderer.
func TestModelView_PassesStylesThrough(t *testing.T) {
	forceTrueColor(t)
	m := New("foo.go", stylesTestDiff)
	m.Width = 60
	m.Styles = customStyles()
	out := m.View()
	if !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("Model.View() missing custom add color: %q", out)
	}
}

// TestViewRender_NilStylesMatchesLegacyColors pins the default palette:
// #23A244 add, #E05252 remove, dim hunk, faint border — the values that were
// inline literals before Styles existed.
func TestViewRender_NilStylesMatchesLegacyColors(t *testing.T) {
	forceTrueColor(t)
	v := View{Diff: stylesTestDiff, Width: 60}
	out := v.Render()
	// #23A244 = rgb(35,162,68); #E05252 renders as rgb(224,81,81) through
	// termenv's hex conversion (pinned from actual output).
	if !strings.Contains(out, "38;2;35;162;68") {
		t.Errorf("default add color drifted: %q", out)
	}
	if !strings.Contains(out, "38;2;224;81;81") {
		t.Errorf("default remove color drifted: %q", out)
	}
	if !strings.Contains(out, "\x1b[2m") {
		t.Errorf("default faint border/hunk attribute drifted: %q", out)
	}
}
