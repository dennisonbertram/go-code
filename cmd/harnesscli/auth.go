package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/provider/kimi"
	"go-agent-harness/internal/store"
)

// harnessConfig is the on-disk configuration saved by "harness auth login".
type harnessConfig struct {
	Server string `json:"server"`
	APIKey string `json:"api_key"`
}

// configPath returns the default path to ~/.harness/config.yaml.
// Uses JSON encoding despite the .yaml extension for simplicity.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".harness", "config.json")
	}
	return filepath.Join(home, ".harness", "config.json")
}

// runAuthLogin implements "harness auth login".
// When the server is localhost it generates a key locally (without hitting the API).
func runAuthLogin(args []string) int {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serverURL := fs.String("server", "http://localhost:8080", "harness server URL")
	tenantID := fs.String("tenant", "default", "tenant ID for the new key")
	name := fs.String("name", "cli", "human-readable name for this key")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli auth login: %v\n", err)
		return 1
	}

	rawToken, _, err := store.GenerateAPIKey(*tenantID, *name, []string{store.ScopeRunsRead, store.ScopeRunsWrite, store.ScopeAdmin})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli auth login: generate key: %v\n", err)
		return 1
	}

	cfg := harnessConfig{
		Server: strings.TrimRight(*serverURL, "/"),
		APIKey: rawToken,
	}

	cfgPath := configPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		fmt.Fprintf(stderr, "harnesscli auth login: create config dir: %v\n", err)
		return 1
	}

	f, err := os.OpenFile(cfgPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli auth login: write config: %v\n", err)
		return 1
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		fmt.Fprintf(stderr, "harnesscli auth login: encode config: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "API key generated and saved to %s\n", cfgPath)
	fmt.Fprintf(stdout, "\nYour API key (save this — it cannot be recovered):\n\n  %s\n\n", rawToken)
	fmt.Fprintf(stdout, "Use it with: Authorization: Bearer %s\n", rawToken)
	return 0
}

// runAuth dispatches "harness auth <subcommand>" commands.
func runAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "harnesscli auth: subcommand required (try: login)")
		return 1
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:])
	case "kimi":
		return runAuthKimi(args[1:])
	default:
		fmt.Fprintf(stderr, "harnesscli auth: unknown subcommand %q\n", args[0])
		return 1
	}
}

func runAuthKimi(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "harnesscli auth kimi: subcommand required (login, status, or logout)")
		return 1
	}
	storePath := kimi.DefaultStorePath()
	switch args[0] {
	case "login":
		if err := kimi.Import(kimi.VendorCredentialPath(), storePath); err != nil {
			fmt.Fprintln(stderr, "harnesscli auth kimi: run kimi-code login, then retry harnesscli auth kimi login")
			return 1
		}
		fmt.Fprintln(stdout, "Kimi Code subscription credential imported.")
		return 0
	case "status":
		creds, err := kimi.Load(storePath)
		if err != nil {
			fmt.Fprintln(stdout, "Kimi Code subscription: not configured (run kimi-code login, then harnesscli auth kimi login)")
			return 1
		}
		if time.Unix(creds.ExpiresAt, 0).After(time.Now()) {
			fmt.Fprintln(stdout, "Kimi Code subscription: configured (access credential valid)")
		} else {
			fmt.Fprintln(stdout, "Kimi Code subscription: configured (access credential will refresh on use)")
		}
		return 0
	case "logout":
		if err := os.Remove(storePath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "harnesscli auth kimi: remove stored credential: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Kimi Code subscription credential removed.")
		return 0
	default:
		fmt.Fprintf(stderr, "harnesscli auth kimi: unknown subcommand %q\n", args[0])
		return 1
	}
}

// dispatch is the top-level command dispatcher.
// It routes "auth" to runAuth and everything else to run.
func dispatch(args []string) int {
	if len(args) == 0 {
		return run(args)
	}
	switch args[0] {
	case "acp":
		return runACP(args[1:])
	case "plugin":
		return runPlugin(args[1:])
	case "auth":
		return runAuth(args[1:])
	case "hooks":
		return runHooks(args[1:])
	case "service":
		return runService(args[1:])
	case "list":
		return runList(args[1:])
	case "runs":
		return runList(args[1:])
	case "cancel":
		return runCancel(args[1:])
	case "status":
		return runStatus(args[1:])
	case "show":
		return runStatus(args[1:])
	case "continue":
		return runContinue(args[1:])
	case "replay":
		return runReplay(args[1:])
	case "search":
		return runSearch(args[1:])
	case "improve":
		return runImprove(args[1:])
	default:
		return run(args)
	}
}

// loadConfig reads ~/.harness/config.json if present.
// Returns nil if the file does not exist.
func loadConfig() (*harnessConfig, error) {
	data, err := os.ReadFile(configPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg harnessConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(stderr, "harnesscli: warning: config at %s is corrupt and could not be parsed: %v\n", configPath(), err)
		return nil, err
	}
	return &cfg, nil
}

// newAuthedRequest builds an HTTP request and, if a config file with an API
// key is present, attaches it as a Bearer Authorization header. Config load
// failures (missing or corrupt config) are treated as "no credentials
// available" — the request is still returned unauthenticated so callers can
// surface the server's own auth error rather than failing here.
func newAuthedRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if cfg, err := loadConfig(); err == nil && cfg != nil && cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	return req, nil
}
