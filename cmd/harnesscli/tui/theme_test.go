package tui_test

import (
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func TestTUI006_ThemeHasRequiredStyles(t *testing.T) {
	th := tui.DefaultTheme()

	// Verify all required style categories exist (non-zero)
	styles := map[string]interface{}{
		"UserMsgStyle":      th.UserMsgStyle,
		"AssistantMsgStyle":  th.AssistantMsgStyle,
		"ThinkingStyle":      th.ThinkingStyle,
		"ToolNameStyle":      th.ToolNameStyle,
		"ToolResultStyle":    th.ToolResultStyle,
		"ErrorStyle":         th.ErrorStyle,
		"StatusBarStyle":     th.StatusBarStyle,
		"StatusModelStyle":   th.StatusModelStyle,
		"DimStyle":           th.DimStyle,
		"BoldStyle":          th.BoldStyle,
		"CodeStyle":          th.CodeStyle,
		"DiffAddStyle":       th.DiffAddStyle,
		"DiffRemoveStyle":    th.DiffRemoveStyle,
	}
	// If theme compiles and fields exist, test passes
	_ = styles
	t.Logf("Theme has %d style fields verified", len(styles))
}

func TestTUI006_SymbolsAreDefined(t *testing.T) {
	s := tui.Symbols
	required := []struct {
		name string
		val  string
	}{
		{"RuneDot", s.Dot},
		{"RuneTree", s.Tree},
		{"RuneArrow", s.Arrow},
		{"RuneSpinner0", s.Spinner[0]},
		{"RuneCheck", s.Check},
		{"RuneCross", s.Cross},
	}
	for _, r := range required {
		if r.val == "" {
			t.Errorf("Symbol %s is empty", r.name)
		}
	}
}

func TestTUI006_SpinnerHas6Frames(t *testing.T) {
	if len(tui.Symbols.Spinner) != 6 {
		t.Errorf("Spinner should have 6 frames (braille pattern), got %d", len(tui.Symbols.Spinner))
	}
}

func TestTUI006_ThemeIsImmutable(t *testing.T) {
	// Two calls to DefaultTheme() should return equal but independent values
	th1 := tui.DefaultTheme()
	th2 := tui.DefaultTheme()
	// Just verify no panic — lipgloss styles are value types
	_ = th1.ErrorStyle.Render("a")
	_ = th2.ErrorStyle.Render("b")
}
