package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var (
	newRootCmdFunc           = newRootCmd
	exitFunc                 = os.Exit
	stderr         io.Writer = os.Stderr
)

func main() {
	root := newRootCmdFunc()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		exitFunc(1)
	}
}

func newRootCmd() *cobra.Command {
	var dbPath string
	var logLevel string

	root := &cobra.Command{
		Use:   "trainerd",
		Short: "Training mode CLI for analyzing and scoring agent runs",
		Long: `trainerd analyzes rollout traces from agent runs, scores them using
structural metrics, and optionally sends them to Claude for deeper analysis.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&dbPath, "db-path", defaultDBPath(), "Path to SQLite database")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	root.AddCommand(
		newScoreCmd(&dbPath),
		newAnalyzeCmd(&dbPath),
		newLoopCmd(&dbPath),
		newStatusCmd(&dbPath),
		newHistoryCmd(&dbPath),
	)

	return root
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "training.db"
	}
	return home + "/.trainerd/training.db"
}

func defaultRolloutDir() string {
	if v := os.Getenv("HARNESS_ROLLOUT_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "rollouts"
	}
	return home + "/.trainerd/rollouts"
}
