package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}

// runImprove implements "harnesscli improve".
// It exposes the existing autoresearch/test loop as a first-class command.
func runImprove(args []string) int {
	fs := flag.NewFlagSet("improve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var targets repeatedStringFlag
	fs.Var(&targets, "target", "target seam to inspect; may be repeated")
	dryRun := fs.Bool("dry-run", false, "print the planned autoresearch command without running it")
	scoreOnly := fs.Bool("score-only", false, "run repo-native score commands and exit")
	iterations := fs.String("iterations", "1", "autoresearch loop iterations")
	pause := fs.String("pause", "0", "seconds to pause between autoresearch runs")
	reportDir := fs.String("report-dir", ".tmp/autoresearch", "autoresearch report directory")
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	profile := fs.String("profile", "full", "run profile sent to harnessd")
	promptProfile := fs.String("prompt-profile", "autoresearch", "prompt routing profile")
	model := fs.String("model", "", "optional model override")
	maxSteps := fs.String("max-steps", "50", "step budget passed to autoresearch runs")
	defaultTestCmd := fs.String("test-cmd", "./scripts/test-regression.sh", "default validation command for unknown targets")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli improve: %v\n", err)
		return 1
	}

	scoreCommands := []string{
		"go test ./...",
		"go test ./... -race",
		"./scripts/test-regression.sh",
	}

	loopArgs := []string{
		"--iterations", *iterations,
		"--pause", *pause,
		"--report-dir", *reportDir,
		"--base-url", *baseURL,
		"--profile", *profile,
		"--prompt-profile", *promptProfile,
		"--max-steps", *maxSteps,
	}
	if *model != "" {
		loopArgs = append(loopArgs, "--model", *model)
	}
	for _, target := range targets {
		loopArgs = append(loopArgs, "--target", target)
	}

	if *dryRun {
		printImprovePlan([]string(targets), loopArgs, scoreCommands)
		return 0
	}
	if *scoreOnly {
		return runScoreCommands(scoreCommands)
	}

	script, err := resolveAutoresearchLoopScript()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli improve: %v\n", err)
		return 1
	}
	cmd := exec.Command(script, loopArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), "HARNESS_AUTORESEARCH_DEFAULT_TEST_CMD="+*defaultTestCmd)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "harnesscli improve: autoresearch loop failed: %v\n", err)
		return 1
	}
	return 0
}

func printImprovePlan(targets []string, loopArgs []string, scoreCommands []string) {
	fmt.Fprintln(stdout, "Self-improvement plan")
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "Targets: default coverage-gap-driven list")
	} else {
		fmt.Fprintln(stdout, "Targets:")
		for _, target := range targets {
			fmt.Fprintf(stdout, "  - %s\n", target)
		}
	}
	fmt.Fprintln(stdout, "Autoresearch command:")
	fmt.Fprintf(stdout, "  scripts/autoresearch-loop.sh %s\n", strings.Join(quoteArgs(loopArgs), " "))
	fmt.Fprintln(stdout, "Score commands:")
	for _, cmd := range scoreCommands {
		fmt.Fprintf(stdout, "  - %s\n", cmd)
	}
}

func runScoreCommands(commands []string) int {
	for _, command := range commands {
		fmt.Fprintf(stdout, "$ %s\n", command)
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "harnesscli improve: score command failed: %s: %v\n", command, err)
			return 1
		}
	}
	return 0
}

func resolveAutoresearchLoopScript() (string, error) {
	candidates := []string{
		"scripts/autoresearch-loop.sh",
		"./scripts/autoresearch-loop.sh",
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("scripts/autoresearch-loop.sh not found; run from the go-code repository")
}

func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			out[i] = fmt.Sprintf("%q", arg)
		} else {
			out[i] = arg
		}
	}
	return out
}
