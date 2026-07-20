package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"go-agent-harness/internal/cron"
)

var (
	runCommand           = run
	exitFunc             = os.Exit
	osArgs               = os.Args
	stdout     io.Writer = os.Stdout
	stderr     io.Writer = os.Stderr
)

func main() {
	exitFunc(runCommand(osArgs[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cronctl <command> [flags]")
		fmt.Fprintln(stderr, "commands: create, list, get, delete, history, pause, resume, health")
		return 1
	}

	baseURL := os.Getenv("CRONSD_URL")
	if baseURL == "" {
		baseURL = "http://localhost:9090"
	}
	client := cron.NewClient(baseURL)
	ctx := context.Background()

	switch args[0] {
	case "create":
		return cmdCreate(ctx, client, args[1:])
	case "list":
		return cmdList(ctx, client)
	case "get":
		return cmdGet(ctx, client, args[1:])
	case "delete":
		return cmdDelete(ctx, client, args[1:])
	case "history":
		return cmdHistory(ctx, client, args[1:])
	case "pause":
		return cmdPause(ctx, client, args[1:])
	case "resume":
		return cmdResume(ctx, client, args[1:])
	case "health":
		return cmdHealth(ctx, client)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 1
	}
}

func cmdCreate(ctx context.Context, client *cron.Client, args []string) int {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "job name")
	schedule := flags.String("schedule", "", "cron schedule (e.g. '*/5 * * * *')")
	execType := flags.String("type", "shell", "execution type (shell or harness)")
	command := flags.String("command", "", "command to execute")
	timeout := flags.Int("timeout", 30, "timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return 1
	}

	if *name == "" || *schedule == "" || *command == "" {
		fmt.Fprintln(stderr, "create requires --name, --schedule, and --command")
		return 1
	}

	config, _ := json.Marshal(map[string]string{"command": *command})
	job, err := client.CreateJob(ctx, cron.CreateJobRequest{
		Name:       *name,
		Schedule:   *schedule,
		ExecType:   *execType,
		ExecConfig: string(config),
		TimeoutSec: *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Created job:\n")
	printJob(job)
	return 0
}

func cmdList(ctx context.Context, client *cron.Client) int {
	jobs, err := client.ListJobs(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if len(jobs) == 0 {
		fmt.Fprintln(stdout, "No jobs found.")
		return 0
	}

	fmt.Fprintf(stdout, "%-36s  %-20s  %-15s  %-8s  %s\n", "ID", "NAME", "SCHEDULE", "STATUS", "NEXT RUN")
	for _, j := range jobs {
		fmt.Fprintf(stdout, "%-36s  %-20s  %-15s  %-8s  %s\n",
			j.ID, truncate(j.Name, 20), truncate(j.Schedule, 15), j.Status, formatTime(j.NextRunAt))
	}
	return 0
}

func cmdGet(ctx context.Context, client *cron.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "get requires a job ID or name")
		return 1
	}

	job, err := client.GetJob(ctx, args[0])
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	printJob(job)
	return 0
}

func cmdDelete(ctx context.Context, client *cron.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "delete requires a job ID or name")
		return 1
	}

	if err := client.DeleteJob(ctx, args[0]); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Job deleted.")
	return 0
}

func cmdHistory(ctx context.Context, client *cron.Client, args []string) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 20, "number of executions to show")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "history requires a job ID or name")
		return 1
	}

	execs, err := client.ListExecutions(ctx, remaining[0], *limit, 0)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if len(execs) == 0 {
		fmt.Fprintln(stdout, "No executions found.")
		return 0
	}

	fmt.Fprintf(stdout, "%-36s  %-10s  %-20s  %s\n", "EXECUTION ID", "STATUS", "STARTED", "DURATION")
	for _, e := range execs {
		fmt.Fprintf(stdout, "%-36s  %-10s  %-20s  %dms\n",
			e.ID, e.Status, formatTime(e.StartedAt), e.DurationMs)
	}
	return 0
}

func cmdPause(ctx context.Context, client *cron.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "pause requires a job ID or name")
		return 1
	}

	status := cron.StatusPaused
	_, err := client.UpdateJob(ctx, args[0], cron.UpdateJobRequest{Status: &status})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Job paused.")
	return 0
}

func cmdResume(ctx context.Context, client *cron.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "resume requires a job ID or name")
		return 1
	}

	status := cron.StatusActive
	_, err := client.UpdateJob(ctx, args[0], cron.UpdateJobRequest{Status: &status})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Job resumed.")
	return 0
}

func cmdHealth(ctx context.Context, client *cron.Client) int {
	if err := client.Health(ctx); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "cronsd is healthy.")
	return 0
}

func printJob(j cron.Job) {
	fmt.Fprintf(stdout, "  ID:         %s\n", j.ID)
	fmt.Fprintf(stdout, "  Name:       %s\n", j.Name)
	fmt.Fprintf(stdout, "  Schedule:   %s\n", j.Schedule)
	fmt.Fprintf(stdout, "  Type:       %s\n", j.ExecType)
	fmt.Fprintf(stdout, "  Status:     %s\n", j.Status)
	fmt.Fprintf(stdout, "  Timeout:    %ds\n", j.TimeoutSec)
	fmt.Fprintf(stdout, "  Next Run:   %s\n", formatTime(j.NextRunAt))
	if !j.LastRunAt.IsZero() {
		fmt.Fprintf(stdout, "  Last Run:   %s\n", formatTime(j.LastRunAt))
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// getBaseURL returns the cronsd URL from env or default (exported for testing).
func getBaseURL() string {
	baseURL := os.Getenv("CRONSD_URL")
	if baseURL == "" {
		return "http://localhost:9090"
	}
	return baseURL
}

// newClientFromEnv is a convenience for testing override.
var newClientFromEnv = func() *cron.Client {
	return cron.NewClient(getBaseURL())
}
