package tui

// theme_components.go — slice 2 of epic #810: derive per-component styles
// from the resolved Theme so a token change visibly restyles the TUI.
//
// Mappings are zero-drift by default: every component renders byte-identically
// under DefaultTheme() to the hardcoded styles it used before this file
// existed, except the approval overlay (unstyled before — see approval.go).

import (
	"github.com/charmbracelet/lipgloss"

	"go-agent-harness/cmd/harnesscli/tui/components/diffview"
	"go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	"go-agent-harness/cmd/harnesscli/tui/components/spinner"
	"go-agent-harness/cmd/harnesscli/tui/components/statusbar"
)

// statusbarStylesFromTheme maps theme styles onto the status bar. Defaults
// match the status bar's legacy styles exactly (faint dim, bold, amber warn).
func statusbarStylesFromTheme(t Theme) statusbar.Styles {
	return statusbar.Styles{
		Dim:  t.DimStyle,
		Bold: t.BoldStyle,
		Warn: t.WarningStyle,
	}
}

// spinnerStylesFromTheme maps the theme onto the thinking spinner (textDim
// token via DimStyle; default faint matches the legacy spinner).
func spinnerStylesFromTheme(t Theme) spinner.Styles {
	return spinner.Styles{Dim: t.DimStyle}
}

// diffviewStylesFromTheme maps diff tokens onto the diff viewer. The dashed
// border rule takes DimStyle (consistent with the SeparatorStyle ← textDim
// mapping in themes.go — the rule is a separator, not a focusable box
// border), which keeps default diff snapshots byte-identical.
func diffviewStylesFromTheme(t Theme) diffview.Styles {
	return diffview.Styles{
		Add:    t.DiffAddStyle,
		Remove: t.DiffRemoveStyle,
		Hunk:   t.DiffHunkStyle,
		Border: t.DimStyle,
	}
}

// messagebubbleStylesFromTheme maps theme colors onto the message bubbles.
// Bubble shapes have no theme-style equivalent (the user bubble is a
// background block; Theme text styles are foreground-only), so colors are
// extracted token-by-token: roleUser recolors user text, accent recolors the
// assistant ⏺ dot, textStrong recolors the assistant title. Tokens left unset
// keep the component defaults (bg 237 / fg 252 / color 15), so the default
// theme renders byte-identically to the legacy bubbles.
func messagebubbleStylesFromTheme(t Theme) messagebubble.Styles {
	s := messagebubble.DefaultStyles()
	if fg, ok := styleForeground(t.UserMsgStyle); ok { // roleUser token
		s.User = s.User.Foreground(fg)
	}
	if fg, ok := styleForeground(t.StatusBarStyle); ok { // accent token
		s.AssistantDot = s.AssistantDot.Foreground(fg)
	}
	if fg, ok := styleForeground(t.BoldStyle); ok { // textStrong token
		s.Title = s.Title.Foreground(fg)
	}
	return s
}

// styleForeground returns the style's foreground color, or ok=false when the
// style carries no foreground (renderer default).
func styleForeground(s lipgloss.Style) (lipgloss.TerminalColor, bool) {
	fg := s.GetForeground()
	if fg == nil {
		return nil, false
	}
	if _, none := fg.(lipgloss.NoColor); none {
		return nil, false
	}
	return fg, true
}
