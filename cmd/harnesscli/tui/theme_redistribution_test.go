package tui

// theme_redistribution_test.go — slice 2 of epic #810: SetTheme must
// re-distribute the resolved theme's styles into every themed component
// (statusbar, spinner, message bubbles, tool-card diffs, approval overlay),
// and the distribution must survive component re-creation (window resize,
// run start).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	"go-agent-harness/cmd/harnesscli/tui/components/tooluse"
)

// forceTrueColor makes styled output deterministic for ANSI assertions.
func forceTrueColor(t *testing.T) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}

const wildThemeJSON = `{
  "warning": "#FF0000",
  "diffAdd": "#00FF00",
  "roleUser": "#0000FF",
  "textDim": "#FF00FF",
  "accent": "#00FFFF",
  "border": "#123456"
}`

func loadWildTheme(t *testing.T) Theme {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wild.json"), []byte(wildThemeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := LoadTheme(dir, "wild")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	return th
}

func sizedModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(DefaultTUIConfig())
	um, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return um.(Model)
}

// TestSetTheme_StatusbarUsesWarningToken verifies the status bar's warning
// segments take the resolved warning color after SetTheme.
func TestSetTheme_StatusbarUsesWarningToken(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	m.statusBar.SetPermMode("plan")
	out := m.statusBar.View()
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("statusbar after SetTheme missing themed warning color: %q", out)
	}
	if !strings.Contains(out, "[plan]") {
		t.Errorf("statusbar missing permission segment: %q", out)
	}
}

// TestSetTheme_StylesSurviveStatusbarRecreation verifies the WindowSizeMsg
// re-creation of the status bar keeps the active theme's styles (the hot
// reload foundation).
func TestSetTheme_StylesSurviveStatusbarRecreation(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	um, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	m = um.(Model)
	m.statusBar.SetPermMode("plan")
	out := m.statusBar.View()
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("statusbar lost themed warning color after resize: %q", out)
	}
}

// TestSetTheme_SpinnerUsesTextDimToken verifies the thinking spinner renders
// through the resolved textDim color, including after the spinner is
// re-created on run start.
func TestSetTheme_SpinnerUsesTextDimToken(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	m.spinner = m.spinner.Start()
	if out := m.spinner.View(80); !strings.Contains(out, "38;2;255;0;255") {
		t.Errorf("spinner after SetTheme missing themed textDim color: %q", out)
	}

	// Run start re-creates the spinner; styles must be re-applied.
	um, _ := m.Update(RunStartedMsg{RunID: "run-theme-1"})
	m = um.(Model)
	if out := m.spinner.View(80); !strings.Contains(out, "38;2;255;0;255") {
		t.Errorf("spinner lost themed color after run start: %q", out)
	}
}

// TestSetTheme_UserBubbleUsesRoleUserToken verifies user message bubbles take
// the resolved roleUser foreground color.
func TestSetTheme_UserBubbleUsesRoleUserToken(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	lines := m.renderMessageBubble(messagebubble.RoleUser, "hello themed world")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "38;2;0;0;255") {
		t.Errorf("user bubble missing themed roleUser color: %q", joined)
	}
}

// TestSetTheme_DiffRenderingUsesDiffAddToken verifies the diff rendered
// inside a tool card takes the resolved diffAdd color (the full model →
// tooluse → diffview path).
func TestSetTheme_DiffRenderingUsesDiffAddToken(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	diff := "--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	m.ensureToolStateMaps()
	m.appendToolUseView(tooluse.Model{
		CallID:   "call-theme-1",
		ToolName: "edit",
		Status:   "completed",
		Expanded: true,
		Result:   diff,
		Width:    80,
	})
	out := m.vp.View()
	if !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("tool-card diff missing themed diffAdd color: %q", out)
	}
}

// TestSetTheme_ApprovalOverlayUsesThemeTokens verifies the approval overlay
// chrome renders from the active theme: border-token color on the box,
// warning-token color on the action line.
func TestSetTheme_ApprovalOverlayUsesThemeTokens(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)
	m.SetTheme(loadWildTheme(t))

	m.toolApproval = toolApprovalState{
		active:    true,
		runID:     "run-theme-2",
		callID:    "call-theme-2",
		tool:      "delete_database",
		arguments: "{}",
	}
	joined := strings.Join(m.renderToolApprovalOverlay(), "\n")
	if !strings.Contains(joined, "38;2;18;52;86") {
		t.Errorf("approval overlay chrome missing themed border color: %q", joined)
	}
	if !strings.Contains(joined, "38;2;255;0;0") {
		t.Errorf("approval overlay action line missing themed warning color: %q", joined)
	}
	if !strings.Contains(joined, "delete_database") {
		t.Errorf("approval overlay lost tool name: %q", joined)
	}
}

// TestDefaultTheme_ComponentsMatchLegacyRendering is the zero-drift guard:
// with the default theme, component rendering must be identical to the
// pre-slice-2 hardcoded styles.
func TestDefaultTheme_ComponentsMatchLegacyRendering(t *testing.T) {
	forceTrueColor(t)
	m := sizedModel(t, 100, 30)

	// Statusbar warning segment: amber #FFAF00 = 38;2;255;175;0.
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("default statusbar warning color drifted: %q", out)
	}

	// Tool-card diff: add color #23A244 = 38;2;35;162;68.
	diff := "--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	m.ensureToolStateMaps()
	m.appendToolUseView(tooluse.Model{
		CallID:   "call-default-1",
		ToolName: "edit",
		Status:   "completed",
		Expanded: true,
		Result:   diff,
		Width:    80,
	})
	if out := m.vp.View(); !strings.Contains(out, "38;2;35;162;68") {
		t.Errorf("default diff add color drifted: %q", out)
	}

	// Spinner: faint, no foreground color.
	m.spinner = m.spinner.Start()
	if out := m.spinner.View(80); strings.Contains(out, "38;") {
		t.Errorf("default spinner gained a color: %q", out)
	}
}
