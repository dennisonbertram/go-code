package main

import (
	"fmt"
	"os"
	"time"

	"go-agent-harness/internal/hooks"
)

// runHooks dispatches "harnesscli hooks <subcommand>" — the maintenance
// surface for the config-driven hook trust model. Trust state lives in the
// user-global directory (~/.harness/hooks-trust.json), never in a project
// tree, so a cloned repository cannot trust its own hooks.
func runHooks(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "harnesscli hooks: subcommand required")
		fmt.Fprintln(stderr, "usage: harnesscli hooks <trust|revoke|list> [hook-file]")
		return 1
	}
	switch args[0] {
	case "trust":
		return hooksTrustRevoke(args[1:], true)
	case "revoke":
		return hooksTrustRevoke(args[1:], false)
	case "list":
		return hooksList(args[1:])
	default:
		fmt.Fprintf(stderr, "harnesscli hooks: unknown subcommand %q (try: trust, revoke, list)\n", args[0])
		return 1
	}
}

// hooksTrustStore opens the user-global trust store.
func hooksTrustStore() (*hooks.TrustStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return hooks.LoadTrustStore(hooks.TrustStorePath(home))
}

func hooksTrustRevoke(args []string, trust bool) int {
	verb := "trust"
	if !trust {
		verb = "revoke"
	}
	if len(args) != 1 {
		fmt.Fprintf(stderr, "harnesscli hooks %s: exactly one hook file path required\n", verb)
		return 1
	}
	store, err := hooksTrustStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli hooks %s: %v\n", verb, err)
		return 1
	}
	if trust {
		if err := store.Trust(args[0]); err != nil {
			fmt.Fprintf(stderr, "harnesscli hooks trust %s: %v\n", args[0], err)
			return 1
		}
		fmt.Fprintf(stdout, "trusted %s\n", args[0])
		fmt.Fprintln(stdout, "note: trust is keyed by content hash — editing the file revokes it automatically")
		return 0
	}
	if err := store.Revoke(args[0]); err != nil {
		fmt.Fprintf(stderr, "harnesscli hooks revoke %s: %v\n", args[0], err)
		return 1
	}
	fmt.Fprintf(stdout, "revoked %s\n", args[0])
	return 0
}

func hooksList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "harnesscli hooks list: no arguments expected")
		return 1
	}
	store, err := hooksTrustStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli hooks list: %v\n", err)
		return 1
	}
	trusted := store.List()
	if len(trusted) == 0 {
		fmt.Fprintln(stdout, "no trusted hook files")
		return 0
	}
	for _, tf := range trusted {
		short := tf.SHA256
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(stdout, "%s  sha256:%s  trusted %s\n",
			tf.Path, short, tf.TrustedAt.Format(time.RFC3339))
	}
	return 0
}
