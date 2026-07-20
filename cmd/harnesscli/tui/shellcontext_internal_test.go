package tui

// Unit tests for the shell-command context block (epic #811, slice 3):
// the <shell-command> XML block injected into the next agent prompt after a
// shell-mode command completes.

import (
	"strings"
	"testing"
)

func TestFormatShellContextBlock_ContainsCommandExitAndOutput(t *testing.T) {
	block := formatShellContextBlock(shellResult{
		Command:  "git status",
		Output:   "On branch main\nnothing to commit",
		ExitCode: 0,
	})
	for _, want := range []string{
		"<shell-command",
		`command="git status"`,
		`exit-code="0"`,
		"<![CDATA[",
		"On branch main",
		"nothing to commit",
		"</shell-command>",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block must contain %q, got:\n%s", want, block)
		}
	}
}

func TestFormatShellContextBlock_CDATAEscape(t *testing.T) {
	// Output containing the CDATA terminator must be split so it cannot break
	// out of the context block (same cdataSafe pattern as @-mention expansion).
	block := formatShellContextBlock(shellResult{
		Command:  "printf 'x]]>y'",
		Output:   "x]]>y",
		ExitCode: 0,
	})
	if !strings.Contains(block, "x]]>]]><![CDATA[y") {
		t.Errorf("CDATA terminator in output must be split, got:\n%s", block)
	}
	// The raw unsplit sequence "x]]>y" must not appear.
	if strings.Contains(block, "x]]>y") {
		t.Errorf("output must not contain an unsplit CDATA terminator, got:\n%s", block)
	}
}

func TestFormatShellContextBlock_ReportsNonZeroExit(t *testing.T) {
	block := formatShellContextBlock(shellResult{
		Command:  "false",
		Output:   "",
		ExitCode: 1,
	})
	if !strings.Contains(block, `exit-code="1"`) {
		t.Errorf("block must report the non-zero exit code, got:\n%s", block)
	}
}

func TestFormatShellContextBlock_TruncatesLongOutput(t *testing.T) {
	// 40KB of output — above the prompt-side injection cap.
	output := strings.Repeat("h", 20*1024) + strings.Repeat("T", 20*1024)
	block := formatShellContextBlock(shellResult{
		Command:  "big",
		Output:   output,
		ExitCode: 0,
	})
	if !strings.Contains(block, "truncated") {
		t.Error("over-cap output must carry a truncation marker")
	}
	// The block must be bounded near the cap (cap + markup overhead).
	if len(block) > shellContextMaxOutputBytes+1024 {
		t.Errorf("block must be bounded near %d bytes, got %d", shellContextMaxOutputBytes, len(block))
	}
	// Head and tail of the original output survive the cut.
	if !strings.Contains(block, strings.Repeat("h", 1024)) {
		t.Error("truncation must keep the head of the output")
	}
	if !strings.Contains(block, strings.Repeat("T", 1024)) {
		t.Error("truncation must keep the tail of the output")
	}
}

func TestFormatShellContextBlock_EscapesCommandAttribute(t *testing.T) {
	block := formatShellContextBlock(shellResult{
		Command:  `echo "<b>&</b>"`,
		Output:   "ok",
		ExitCode: 0,
	})
	if strings.Contains(block, `command="echo "<b>&</b>""`) {
		t.Errorf("command attribute must be XML-escaped, got:\n%s", block)
	}
	for _, want := range []string{"&lt;b&gt;", "&amp;", "&#34;"} {
		if !strings.Contains(block, want) {
			t.Errorf("escaped command must contain %q, got:\n%s", want, block)
		}
	}
}
