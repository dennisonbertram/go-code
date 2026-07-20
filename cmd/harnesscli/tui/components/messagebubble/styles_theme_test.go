package messagebubble

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

// TestModelView_CustomStylesRestyleUserBubble verifies injected Styles replace
// the package-default user-bubble colors.
func TestModelView_CustomStylesRestyleUserBubble(t *testing.T) {
	forceTrueColor(t)
	styles := DefaultStyles()
	styles.User = lipgloss.NewStyle().
		Background(lipgloss.Color("#111111")).
		Foreground(lipgloss.Color("#FF0000"))
	m := New(RoleUser, "hello theme")
	m.Width = 40
	m.Styles = &styles
	out := m.View()
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("user bubble missing custom fg: %q", out)
	}
	if !strings.Contains(out, "48;2;17;17;17") {
		t.Errorf("user bubble missing custom bg: %q", out)
	}
}

// TestModelView_CustomStylesRestyleAssistantDotAndTitle verifies the ⏺ glyph
// and title take injected colors.
func TestModelView_CustomStylesRestyleAssistantDotAndTitle(t *testing.T) {
	forceTrueColor(t)
	styles := DefaultStyles()
	styles.AssistantDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	styles.Title = lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")).Bold(true)
	b := AssistantBubble{Title: "Intro", Content: "plain body", Width: 40, Styles: &styles}
	out := b.View()
	if !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("assistant dot missing custom color: %q", out)
	}
	if !strings.Contains(out, "38;2;0;0;255") {
		t.Errorf("assistant title missing custom color: %q", out)
	}
}

// TestDefaultStylesMatchLegacyColors pins the defaults to the pre-injection
// hardcoded values: user bubble bg 237 / fg 252, assistant dot color 15,
// title bold+italic+underline.
func TestDefaultStylesMatchLegacyColors(t *testing.T) {
	forceTrueColor(t)
	s := DefaultStyles()
	if got := s.User.GetBackground(); got != lipgloss.Color("237") {
		t.Errorf("default user bg = %v, want 237", got)
	}
	if got := s.User.GetForeground(); got != lipgloss.Color("252") {
		t.Errorf("default user fg = %v, want 252", got)
	}
	if got := s.AssistantDot.GetForeground(); got != lipgloss.Color("15") {
		t.Errorf("default assistant dot = %v, want 15", got)
	}
	if !s.Title.GetBold() || !s.Title.GetItalic() || !s.Title.GetUnderline() {
		t.Error("default title style lost bold+italic+underline")
	}

	// Nil Styles must render identically to the old package vars.
	m := New(RoleUser, "hello")
	m.Width = 40
	out := m.View()
	if !strings.Contains(out, "48;5;237") || !strings.Contains(out, "38;5;252") {
		t.Errorf("default user bubble colors drifted: %q", out)
	}
}
