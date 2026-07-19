package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"go-agent-harness/internal/acp"
)

// stdin is the process standard input, swappable in tests (mirrors the
// stdout/stderr pattern in main.go).
var stdin io.Reader = os.Stdin

// runACP implements "harness acp": it serves the Agent Client Protocol
// (newline-delimited JSON-RPC 2.0) over stdin/stdout so ACP-compatible
// editors (Zed, JetBrains via ACP) can drive go-code as a subprocess.
// stdout is a pure protocol channel; all diagnostics go to stderr.
func runACP(args []string) int {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}

	server := acp.NewServer(stdin, stdout, stderr)
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintf(stderr, "harnesscli acp: %v\n", err)
		return 1
	}
	return 0
}
