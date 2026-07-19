// Command harness-acp exposes harnessd as an Agent Client Protocol server over stdio.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	acp "github.com/coder/acp-go-sdk"
	"go-agent-harness/internal/harnessacp"
)

var stdinReader io.Reader = os.Stdin
var stdoutWriter io.Writer = os.Stdout
var getenv = os.Getenv

func main() {
	if err := run(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "harness-acp: %v\\n", err)
		os.Exit(1)
	}
}

func run() error {
	addr := getenv("HARNESS_ADDR")
	if addr == "" {
		addr = "http://localhost:8080"
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return runWithIO(ctx, stdinReader, stdoutWriter, addr)
}

func runWithIO(ctx context.Context, in io.Reader, out io.Writer, addr string) error {
	agent := harnessacp.NewAgent(addr)
	conn := acp.NewAgentSideConnection(agent, out, in)
	agent.SetAgentConnection(conn)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.Done():
		return nil
	}
}
