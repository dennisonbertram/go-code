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

func TestStatusbarShowsTitle(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	sb.SetTitle("fix auth bug")
	view := sb.View()
	if !strings.Contains(view, "fix auth bug") {
		t.Errorf("status bar missing session title: %q", view)
	}
}

func TestStatusbarTitleOmittedWhenEmpty(t *testing.T) {
	withEmpty := statusbar.New(80)
	withEmpty.SetModel("gpt-4.1-mini")
	withEmpty.SetTitle("")

	unset := statusbar.New(80)
	unset.SetModel("gpt-4.1-mini")

	if withEmpty.View() != unset.View() {
		t.Errorf("empty title must render identically to an unset title:\nwith-empty: %q\nunset:      %q", withEmpty.View(), unset.View())
	}
}

func TestStatusbarTitleTruncated(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	long := "this is an extremely long session title that must be clipped"
	sb.SetTitle(long)
	view := sb.View()
	if strings.Contains(view, long) {
		t.Errorf("status bar must truncate long titles, got full title in: %q", view)
	}
	stripped := stripANSI(view)
	if len([]rune(stripped)) > 82 { // width + small ANSI tolerance
		t.Errorf("status bar overflowed width 80 with a long title: %d runes: %q", len([]rune(stripped)), stripped)
	}
}

func TestStatusbarTitleClearRestoresBaseline(t *testing.T) {
	sb := statusbar.New(80)
	sb.SetModel("gpt-4.1-mini")
	baseline := sb.View()

	sb.SetTitle("temporary")
	if sb.View() == baseline {
		t.Fatal("setting a title must change the rendered bar")
	}

	sb.SetTitle("")
	if sb.View() != baseline {
		t.Errorf("clearing the title must restore the baseline bar:\nbaseline: %q\ncleared:  %q", baseline, sb.View())
	}
}
