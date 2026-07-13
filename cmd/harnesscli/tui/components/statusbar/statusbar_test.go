package statusbar_test

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/statusbar"
)

func TestTUI011_StatusbarShowsDefaultState(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetWorkdir("/home/user/project")
	sb.SetBranch("main")
	view := sb.View()
	if !strings.Contains(view, "gpt-4.1-mini") {
		t.Errorf("status bar missing model name: %q", view)
	}
}

func TestTUI011_StatusbarShowsMCPFailureCount(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetMCPFailures(3)
	view := sb.View()
	if !strings.Contains(view, "3") {
		t.Errorf("status bar missing MCP failure count: %q", view)
	}
}

func TestTUI011_StatusbarTruncatesForNarrowTerminal(t *testing.T) {
	sb := statusbar.New(20)
	sb.SetModel("claude-3-7-sonnet-20250219")
	sb.SetWorkdir("/very/long/path/to/project")
	view := sb.View()
	// Should not overflow 20 chars visible width
	stripped := stripANSI(view)
	lines := strings.Split(stripped, "\n")
	for _, line := range lines {
		if len([]rune(line)) > 22 { // small tolerance for ANSI
			t.Errorf("line too long at width 20: %d chars: %q", len([]rune(line)), line)
		}
	}
}

func TestTUI011_StatusbarShowsModeHint(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetPermMode("plan")
	view := sb.View()
	if !strings.Contains(view, "plan") {
		t.Errorf("status bar missing plan mode indicator: %q", view)
	}
}

func TestTUI011_StatusbarShowsRunningState(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetRunning(true)
	view := sb.View()
	_ = view // just ensure no panic when running
}

func TestTUI011_StatusbarShowsWorkdir(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetWorkdir("/home/user/project")
	view := sb.View()
	if !strings.Contains(view, "project") {
		t.Errorf("status bar missing workdir: %q", view)
	}
}

func TestTUI011_StatusbarShowsBranch(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetBranch("feat/tui")
	view := sb.View()
	if !strings.Contains(view, "feat/tui") {
		t.Errorf("status bar missing branch: %q", view)
	}
}

func TestTUI011_StatusbarShowsCost(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetCost(0.0042)
	view := sb.View()
	if !strings.Contains(view, "0.0042") {
		t.Errorf("status bar missing cost: %q", view)
	}
}

func TestTUI011_StatusbarZeroWidthSafe(t *testing.T) {
	sb := statusbar.New(0)
	sb.SetModel("gpt-4.1-mini")
	view := sb.View()
	_ = view // should not panic
}

func TestTUI011_StatusbarEmptyState(t *testing.T) {
	sb := statusbar.New(80)
	view := sb.View()
	// Empty state should not panic and should return something (even empty)
	_ = view
}

func TestTUI011_StatusbarSetWidth(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetWidth(40)
	view := sb.View()
	stripped := stripANSI(view)
	lines := strings.Split(stripped, "\n")
	for _, line := range lines {
		if len([]rune(line)) > 42 { // tolerance
			t.Errorf("line too long at width 40: %d chars: %q", len([]rune(line)), line)
		}
	}
}

func TestTUI011_StatusbarNoMCPFailuresHidden(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetMCPFailures(0)
	view := sb.View()
	if strings.Contains(view, "MCP") {
		t.Errorf("status bar should not show MCP when 0 failures: %q", view)
	}
}

func TestTUI011_StatusbarDefaultModeHidden(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetPermMode("default")
	view := sb.View()
	if strings.Contains(view, "default") {
		t.Errorf("status bar should not show 'default' mode: %q", view)
	}
}

func TestStatusbarShowsContextMeter(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetContext(56000, 200000)
	view := sb.View()
	if !strings.Contains(view, "◫ 28%/200K") {
		t.Errorf("status bar missing context meter, want '◫ 28%%/200K': %q", view)
	}
}

func TestStatusbarContextMeterOmittedWhenUnset(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	view := sb.View()
	if strings.Contains(view, "◫") {
		t.Errorf("status bar should not show context meter when unset: %q", view)
	}
}

func TestStatusbarContextMeterOmittedWhenZero(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetContext(0, 0)
	view := sb.View()
	if strings.Contains(view, "◫") {
		t.Errorf("status bar should not show context meter when used/total are 0: %q", view)
	}
}

func TestStatusbarContextMeterWarnStyleWhenHigh(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetContext(180000, 200000)
	view := sb.View()
	if !strings.Contains(view, "◫ 90%/200K") {
		t.Errorf("status bar missing high-usage context meter, want '◫ 90%%/200K': %q", view)
	}
}

func TestStatusbarContextMeterCompactMillions(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetContext(500000, 1000000)
	view := sb.View()
	if !strings.Contains(view, "◫ 50%/1M") {
		t.Errorf("status bar missing compact million total, want '◫ 50%%/1M': %q", view)
	}
}

// stripANSI removes ANSI escape sequences for length testing.
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
