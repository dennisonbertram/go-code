// Command forensics provides CLI tools for analyzing and comparing rollout files.
//
// Usage:
//
//	forensics diff <rollout_a.jsonl> <rollout_b.jsonl>
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"go-agent-harness/internal/forensics/differ"
	"go-agent-harness/internal/forensics/rollout"
)

var (
	runCommand           = run
	exitFunc             = os.Exit
	osArgs               = os.Args
	stdout     io.Writer = os.Stdout
	stderr     io.Writer = os.Stderr
)

func main() {
	if err := runCommand(osArgs[1:]); err != nil {
		fmt.Fprintf(stderr, "forensics: %s\n", sanitize(err.Error()))
		exitFunc(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: forensics <command> [args]\n\nCommands:\n  diff <rollout_a.jsonl> <rollout_b.jsonl>  Compare two rollout files")
	}

	switch args[0] {
	case "diff":
		return runDiff(args[1:])
	default:
		// Sanitize before including in error to prevent terminal injection via
		// adversarially named arguments.
		return fmt.Errorf("unknown command: %s", sanitize(args[0]))
	}
}

func runDiff(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: forensics diff <rollout_a.jsonl> <rollout_b.jsonl>")
	}

	// Sanitize file path in error messages to prevent terminal injection via
	// specially crafted file names containing control/bidi characters.
	eventsA, err := rollout.LoadFile(args[0])
	if err != nil {
		return fmt.Errorf("loading run A (%s): %w", sanitize(args[0]), err)
	}

	eventsB, err := rollout.LoadFile(args[1])
	if err != nil {
		return fmt.Errorf("loading run B (%s): %w", sanitize(args[1]), err)
	}

	// Canonicalize before diffing.
	canonA := rollout.Canonicalize(eventsA, rollout.DefaultOptions)
	canonB := rollout.Canonicalize(eventsB, rollout.DefaultOptions)

	result := differ.Diff(canonA, canonB)

	printDiffResult(eventsA, eventsB, result)
	return nil
}

func printDiffResult(a, b []rollout.RolloutEvent, result differ.DiffResult) {
	stepsA := countMaxStep(a)
	stepsB := countMaxStep(b)
	costA := extractCost(a)
	costB := extractCost(b)

	fmt.Fprintf(stdout, "Run A: %d steps, $%.5f\n", stepsA, costA)
	fmt.Fprintf(stdout, "Run B: %d steps, $%.5f\n", stepsB, costB)

	// Count step statuses.
	identical, diverged, onlyA, onlyB := 0, 0, 0, 0
	for _, sd := range result.StepDiffs {
		switch sd.Status {
		case "identical":
			identical++
		case "diverged":
			diverged++
		case "only_in_a":
			onlyA++
		case "only_in_b":
			onlyB++
		}
	}

	parts := []string{}
	if identical > 0 {
		parts = append(parts, fmt.Sprintf("%d identical", identical))
	}
	if diverged > 0 {
		parts = append(parts, fmt.Sprintf("%d diverged", diverged))
	}
	if onlyA > 0 {
		parts = append(parts, fmt.Sprintf("%d only in A", onlyA))
	}
	if onlyB > 0 {
		parts = append(parts, fmt.Sprintf("%d only in B", onlyB))
	}

	fmt.Fprintf(stdout, "Steps: ")
	for i, p := range parts {
		if i > 0 {
			fmt.Fprintf(stdout, ", ")
		}
		fmt.Fprintf(stdout, "%s", p)
	}
	fmt.Fprintln(stdout)

	// Winner summary.
	winnerLabel := "Tie"
	if result.Score.Winner == "a" {
		winnerLabel = "A"
	} else if result.Score.Winner == "b" {
		winnerLabel = "B"
	}

	reasons := ""
	for i, r := range result.Score.Reasons {
		if i > 0 {
			reasons += ", "
		}
		reasons += sanitize(r)
	}
	fmt.Fprintf(stdout, "Winner: %s (%s)\n", sanitize(winnerLabel), reasons)
}

// sanitize removes ASCII control characters (including ANSI escape sequences
// and newlines), and Unicode format/bidi-override characters from untrusted
// strings before printing to the terminal. This prevents terminal escape
// injection, bidi spoofing, and log-line forgery via malicious file names or
// rollout content. Note: \n is NOT allowed because it enables log-line forging.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		// Drop all ASCII control characters including \n, \r, \t, ESC.
		if unicode.IsControl(r) {
			return -1
		}
		// Drop Unicode format characters (category Cf), which includes
		// right-to-left overrides (U+202E), zero-width joiners, bidi controls, etc.
		if unicode.In(r, unicode.Cf) {
			return -1
		}
		// Drop Unicode line/paragraph separators (U+2028 LINE SEPARATOR,
		// U+2029 PARAGRAPH SEPARATOR) which render as real line breaks in
		// many terminals and log pipelines, enabling log-line forging even
		// when \n is removed.
		if unicode.In(r, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
}

func countMaxStep(events []rollout.RolloutEvent) int {
	max := 0
	for _, ev := range events {
		if ev.Step > max {
			max = ev.Step
		}
	}
	return max
}

func extractCost(events []rollout.RolloutEvent) float64 {
	var maxCost float64
	for _, ev := range events {
		if ev.Type == "usage.delta" && ev.Payload != nil {
			if c, ok := ev.Payload["cumulative_cost_usd"].(float64); ok && c > maxCost {
				maxCost = c
			}
		}
	}
	return maxCost
}
