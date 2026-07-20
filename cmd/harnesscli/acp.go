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

// defaultACPServerURL is used when neither -server nor a saved config names
// a harnessd.
const defaultACPServerURL = "http://localhost:8080"

// runACP implements "harness acp": it serves the Agent Client Protocol
// (newline-delimited JSON-RPC 2.0) over stdin/stdout so ACP-compatible
// editors (Zed, JetBrains via ACP) can drive go-code as a subprocess.
// stdout is a pure protocol channel; all diagnostics go to stderr.
func runACP(args []string) int {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serverFlag := fs.String("server", "", "harness server URL (default: ~/.harness/config.json server or "+defaultACPServerURL+")")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	baseURL, apiKey := resolveACPServer(*serverFlag)
	server := acp.NewServer(stdin, stdout, stderr)
	server.EnableSessions(acp.NewRunsClient(baseURL, apiKey))
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintf(stderr, "harnesscli acp: %v\n", err)
		return 1
	}
	return 0
}

// resolveACPServer picks the harnessd base URL and API key for the ACP
// adapter. Precedence: -server flag > saved config server > default
// localhost. The API key comes from the saved config (harness auth login);
// a missing or unreadable config just means no credentials, matching the
// rest of the CLI.
func resolveACPServer(flagValue string) (baseURL, apiKey string) {
	if flagValue != "" {
		baseURL = flagValue
	}
	if cfg, err := loadConfig(); err == nil && cfg != nil {
		if baseURL == "" && cfg.Server != "" {
			baseURL = cfg.Server
		}
		apiKey = cfg.APIKey
	}
	if baseURL == "" {
		baseURL = defaultACPServerURL
	}
	return baseURL, apiKey
}
