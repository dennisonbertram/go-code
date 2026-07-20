package main

// viz.go — "harnesscli viz [--open]" (epic #812, slice 2).
//
// Prints the session visualizer URL (<base>/viz/) for a configured daemon
// and, with --open, launches it in the OS browser. The visualizer itself is
// the embedded static shell served by harnessd under /viz (slice 1); this
// subcommand is only the one-command path into it.

import (
	"flag"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// vizPlatform selects the browser opener command. It is a var (not
// runtime.GOOS inline) so tests can exercise either platform path — same
// precedent as servicePlatform in service.go.
var vizPlatform = runtime.GOOS

// vizOpenURL launches the OS default browser at url. It is a var so tests
// can substitute a recording fake — unit tests must never exec a real
// browser (same precedent as serviceRunLifecycle in service.go).
var vizOpenURL = func(url string) error {
	return exec.Command(vizOpenerName(vizPlatform), url).Start()
}

// vizOpenerName returns the platform's browser-launch command: open on
// macOS, xdg-open on Linux and other Unixes.
func vizOpenerName(platform string) string {
	if platform == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// vizURL returns the visualizer URL for a daemon base URL, trimming any
// trailing slash the way runStatus/runList build their endpoints.
func vizURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/viz/"
}

// runViz implements "harnesscli viz [--open]". It always prints the
// visualizer URL; with --open it also tries to launch the browser, falling
// back to the already-printed URL when the launch fails.
func runViz(args []string) int {
	fs := flag.NewFlagSet("viz", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	open := fs.Bool("open", false, "open the visualizer URL in the default browser")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli viz: %v\n", err)
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "harnesscli viz: unexpected argument %q (usage: harnesscli viz [--open] [-base-url URL])\n", fs.Arg(0))
		return 1
	}

	url := vizURL(*baseURL)
	fmt.Fprintln(stdout, url)

	if !*open {
		return 0
	}
	if err := vizOpenURL(url); err != nil {
		fmt.Fprintf(stderr, "harnesscli viz: open browser: %v\n", err)
		return 1
	}
	return 0
}
