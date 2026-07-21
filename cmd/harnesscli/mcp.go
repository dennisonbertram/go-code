package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/mcp"
	"go-agent-harness/internal/mcp/oauth"
)

// mcpOpenURL is the browser opener used by "mcp login". Nil selects the
// platform default inside oauth.Flow. Tests replace it with an in-process
// driver.
var mcpOpenURL func(string) error

// newMCPOAuthFlow builds the login/refresh flow over the default token store
// (~/.harness/mcp).
func newMCPOAuthFlow() *oauth.Flow {
	return &oauth.Flow{Store: mcp.DefaultTokenStore(), OpenURL: mcpOpenURL}
}

// configuredMCPServer is one MCP server entry as seen by the CLI, resolved
// from the same config sources the daemon uses.
type configuredMCPServer struct {
	Name          string
	Transport     string
	URL           string
	HasStaticAuth bool
}

// loadConfiguredMCPServers resolves MCP server configs from
// ~/.harness/config.toml, .harness/config.toml (project), and
// HARNESS_MCP_SERVERS, with TOML taking precedence on name collisions —
// mirroring cmd/harnessd registration. The result is sorted by name.
func loadConfiguredMCPServers() ([]configuredMCPServer, error) {
	var userCfgPath string
	if home, err := os.UserHomeDir(); err == nil {
		userCfgPath = filepath.Join(home, ".harness", "config.toml")
	}
	cfg, err := config.Load(config.LoadOptions{
		UserConfigPath:    userCfgPath,
		ProjectConfigPath: filepath.Join(".harness", "config.toml"),
		Getenv:            os.Getenv,
	})
	if err != nil {
		return nil, err
	}

	byName := make(map[string]configuredMCPServer)
	for name, srv := range cfg.MCPServers {
		transport := srv.Transport
		if transport == "" {
			if srv.URL != "" {
				transport = "http"
			} else {
				transport = "stdio"
			}
		}
		byName[name] = configuredMCPServer{
			Name:          name,
			Transport:     transport,
			URL:           srv.URL,
			HasStaticAuth: hasAuthorizationHeader(srv.Headers),
		}
	}

	envServers, err := mcp.ParseMCPServersEnv()
	if err != nil {
		return nil, err
	}
	for _, sc := range envServers {
		if _, exists := byName[sc.Name]; exists {
			continue // TOML wins, mirroring the daemon
		}
		byName[sc.Name] = configuredMCPServer{
			Name:          sc.Name,
			Transport:     sc.Transport,
			URL:           sc.URL,
			HasStaticAuth: hasAuthorizationHeader(sc.Headers),
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]configuredMCPServer, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out, nil
}

// hasAuthorizationHeader reports whether headers configure Authorization
// (any casing).
func hasAuthorizationHeader(headers map[string]string) bool {
	for k := range headers {
		if strings.EqualFold(k, "Authorization") {
			return true
		}
	}
	return false
}

func findConfiguredServer(servers []configuredMCPServer, name string) (configuredMCPServer, bool) {
	for _, s := range servers {
		if s.Name == name {
			return s, true
		}
	}
	return configuredMCPServer{}, false
}

// runMCP dispatches "harnesscli mcp <subcommand>" commands.
func runMCP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "harnesscli mcp: subcommand required (try: login, logout, status)")
		return 1
	}
	switch args[0] {
	case "login":
		return runMCPLogin(args[1:])
	case "logout":
		return runMCPLogout(args[1:])
	case "status":
		return runMCPStatus(args[1:])
	default:
		fmt.Fprintf(stderr, "harnesscli mcp: unknown subcommand %q (try: login, logout, status)\n", args[0])
		return 1
	}
}

// runMCPLogin implements "harnesscli mcp login <server>": it runs the OAuth
// 2.1 + PKCE flow against the named server's configured URL and stores the
// token under ~/.harness/mcp.
func runMCPLogin(args []string) int {
	fs := flag.NewFlagSet("mcp login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	clientID := fs.String("client-id", "", "pre-registered OAuth client ID (dynamic registration is used when omitted and supported)")
	scope := fs.String("scope", "", "space-separated OAuth scopes to request")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "harnesscli mcp login: server name required (usage: harnesscli mcp login [--client-id ID] [--scope \"a b\"] <server>)")
		return 1
	}
	name := fs.Arg(0)

	servers, err := loadConfiguredMCPServers()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli mcp login: load config: %v\n", err)
		return 1
	}
	srv, ok := findConfiguredServer(servers, name)
	if !ok {
		fmt.Fprintf(stderr, "harnesscli mcp login: server %q not found in config (checked ~/.harness/config.toml, .harness/config.toml, and %s)\n", name, mcp.EnvVarMCPServers)
		return 1
	}
	if srv.Transport != "http" {
		fmt.Fprintf(stderr, "harnesscli mcp login: server %q uses the stdio transport; OAuth login only applies to http servers\n", name)
		return 1
	}

	flow := newMCPOAuthFlow()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fmt.Fprintf(stdout, "Opening browser to authorize MCP server %q...\n", name)
	tok, err := flow.Login(ctx, oauth.LoginOptions{
		ServerName:  name,
		ResourceURL: srv.URL,
		ClientID:    *clientID,
		Scopes:      strings.Fields(*scope),
	})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli mcp login %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "Logged in to MCP server %q (issuer %s); token stored in %s\n", name, tok.Issuer, flow.Store.Dir())
	return 0
}

// runMCPLogout implements "harnesscli mcp logout <server>": it deletes the
// stored token. Logout is idempotent.
func runMCPLogout(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "harnesscli mcp logout: server name required (usage: harnesscli mcp logout <server>)")
		return 1
	}
	name := args[0]
	if err := mcp.DefaultTokenStore().Delete(name); err != nil {
		fmt.Fprintf(stderr, "harnesscli mcp logout %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "Removed stored token for MCP server %q (if any existed).\n", name)
	return 0
}

// runMCPStatus implements "harnesscli mcp status": it prints the configured
// MCP servers with their per-server auth state for HTTP servers.
func runMCPStatus(args []string) int {
	servers, err := loadConfiguredMCPServers()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli mcp status: load config: %v\n", err)
		return 1
	}
	if len(servers) == 0 {
		fmt.Fprintln(stdout, "No MCP servers configured.")
		return 0
	}

	store := mcp.DefaultTokenStore()
	for _, srv := range servers {
		url := srv.URL
		if url == "" {
			url = "-"
		}
		fmt.Fprintf(stdout, "%s %s %s %s\n", srv.Name, srv.Transport, url, mcpAuthState(store, srv))
	}
	return 0
}

// mcpAuthState renders the auth state for one configured server.
func mcpAuthState(store *mcp.TokenStore, srv configuredMCPServer) string {
	if srv.Transport != "http" {
		return "-"
	}
	if srv.HasStaticAuth {
		return "static Authorization header"
	}
	tok, err := store.Get(srv.Name)
	switch {
	case err == nil:
		if tok.Expiry.IsZero() {
			return "token valid (no expiry)"
		}
		return "token valid (expires " + tok.Expiry.Format(time.RFC3339) + ")"
	case errors.Is(err, mcp.ErrTokenNotFound):
		return "no token (run `harnesscli mcp login " + srv.Name + "`)"
	case errors.Is(err, mcp.ErrTokenExpired):
		return "token expired (run `harnesscli mcp login " + srv.Name + "`)"
	case errors.Is(err, mcp.ErrTokenCorrupt):
		return "token corrupt (run `harnesscli mcp login " + srv.Name + "`)"
	default:
		return fmt.Sprintf("error reading token: %v", err)
	}
}
