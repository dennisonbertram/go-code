package tui

// Shell-command context injection (epic #811, slice 3).
//
// After a foreground shell-mode command finishes, the next agent prompt
// carries the command and its output as a <shell-command> XML block so the
// agent can reason over what the user ran without re-running it. The block
// follows the same wrapping pattern as @-mention file expansion
// (fileexpand.go): attributes XML-escaped, output CDATA-wrapped so command
// text cannot break the context block, and head+tail truncation for long
// output.

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// shellContextMaxOutputBytes caps the output injected into a prompt. This is
// a second, prompt-side bound on top of the executor's 30KB capture cap —
// prompts should stay small even when the card showed more.
const shellContextMaxOutputBytes = 10 * 1024

// shellResult captures the outcome of a completed shell-mode command for
// one-shot injection into the next agent prompt.
type shellResult struct {
	Command  string
	Output   string // bounded head/tail from the executor
	ExitCode int
}

// formatShellContextBlock renders the result as an XML block:
//
//	<shell-command command="git status" exit-code="0">
//	<![CDATA[
//	...output...
//	]]>
//	</shell-command>
func formatShellContextBlock(r shellResult) string {
	output := truncateHeadTail(r.Output, shellContextMaxOutputBytes)
	var sb strings.Builder
	sb.WriteString("<shell-command command=\"")
	sb.WriteString(xmlAttrEscape(r.Command))
	sb.WriteString("\" exit-code=\"")
	sb.WriteString(strconv.Itoa(r.ExitCode))
	sb.WriteString("\">\n<![CDATA[")
	sb.WriteString(cdataSafe(output))
	sb.WriteString("]]>\n</shell-command>")
	return sb.String()
}

// truncateHeadTail keeps the first and last max/2 bytes of s, joined by the
// executor's truncation marker, when s exceeds max. Cut points are aligned to
// rune boundaries so the injected block is always valid UTF-8.
func truncateHeadTail(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max / 2
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - (max - max/2)
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return s[:head] + shellExecTruncationMarker + s[tailStart:]
}
