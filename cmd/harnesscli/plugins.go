package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"go-agent-harness/internal/plugins"
)

func runPlugin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: harnesscli plugin <install|list|uninstall|update|trust|untrust|marketplace> [source|name]")
		return 1
	}
	switch args[0] {
	case "install":
		return pluginInstall(args[1:])
	case "list":
		return pluginList(args[1:])
	case "uninstall":
		return pluginUninstall(args[1:])
	case "update":
		return pluginUpdate(args[1:])
	case "trust":
		return pluginSetTrusted(args[1:], true)
	case "untrust":
		return pluginSetTrusted(args[1:], false)
	case "marketplace":
		return pluginMarketplace(args[1:])
	default:
		fmt.Fprintf(stderr, "harnesscli plugin: unknown subcommand %q (try: install, list, uninstall, update, trust, untrust)\n", args[0])
		return 1
	}
}

func pluginMarketplace(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: harnesscli plugin marketplace <add|list|update> [name path]")
		return 1
	}
	_, dir, err := pluginStore()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store := plugins.NewMarketplaceStore(filepath.Join(dir, "marketplaces.json"))
	switch args[0] {
	case "add":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "harnesscli plugin marketplace add: name and path required")
			return 1
		}
		if err := store.Add(plugins.MarketplaceSource{Name: args[1], URL: args[2]}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "added marketplace %s\n", args[1])
		return 0
	case "list", "update":
		items, err := store.List()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, item := range items {
			m, e := plugins.LoadMarketplace(item)
			if e != nil {
				fmt.Fprintf(stderr, "marketplace %s: %v\n", item.Name, e)
				continue
			}
			for _, p := range m.Plugins {
				fmt.Fprintf(stdout, "%s %s %s\n", item.Name, p.Name, p.Source)
			}
		}
		return 0
	default:
		fmt.Fprintln(stderr, "harnesscli plugin marketplace: unknown subcommand")
		return 1
	}
}

func pluginHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".go-harness", "plugins"), nil
}

func pluginStore() (*plugins.StateStore, string, error) {
	dir, err := pluginHome()
	if err != nil {
		return nil, "", err
	}
	return plugins.NewStateStore(filepath.Join(dir, "state.json")), dir, nil
}

// stdinIsTerminal reports whether stdin is an interactive terminal. Swappable
// in tests (mirrors the stdin/stdout/stderr pattern).
var stdinIsTerminal = func() bool {
	f, ok := stdin.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// confirmPluginAction decides whether a remote bundle operation proceeds.
// assumeYes (--yes) accepts non-interactively; an interactive terminal
// prompts y/N (default no); anything else refuses with a --yes hint so
// scripts never deadlock waiting for input.
func confirmPluginAction(action string, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	if !stdinIsTerminal() {
		fmt.Fprintf(stderr, "harnesscli plugin: %s requires confirmation; re-run with --yes to accept the declared surfaces\n", action)
		return false
	}
	fmt.Fprintf(stdout, "%s? [y/N]: ", action)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(stdout)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// printPluginSurfaces lists the executable surfaces a manifest declares.
func printPluginSurfaces(w io.Writer, m plugins.Manifest) {
	surfaces := []struct{ name, path string }{
		{"skills", m.Skills},
		{"commands", m.Commands},
		{"agents", m.Agents},
		{"hooks", m.Hooks},
		{"mcp", m.MCP},
	}
	printed := false
	for _, s := range surfaces {
		if s.path == "" {
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", s.name, s.path)
		printed = true
	}
	if !printed {
		fmt.Fprintln(w, "  (no executable surfaces declared)")
	}
}

// sameDeclaredSurfaces reports whether two manifests declare the same
// executable surfaces.
func sameDeclaredSurfaces(a, b plugins.Manifest) bool {
	return a.Skills == b.Skills &&
		a.Commands == b.Commands &&
		a.Agents == b.Agents &&
		a.Hooks == b.Hooks &&
		a.MCP == b.MCP
}

// pluginSetTrusted implements `plugin trust` and `plugin untrust`, the only
// user-facing way to grant or revoke a bundle's executable authority. The
// change applies at the next harnessd/TUI start; there is no hot-reload.
func pluginSetTrusted(args []string, trusted bool) int {
	verb := "trust"
	if !trusted {
		verb = "untrust"
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(stderr, "harnesscli plugin %s: exactly one plugin name is required\n", verb)
		return 1
	}
	store, _, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin %s: %v\n", verb, err)
		return 1
	}
	if err := store.SetTrusted(args[0], trusted); err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin %s: %v\n", verb, err)
		return 1
	}
	state := "trusted"
	if !trusted {
		state = "untrusted"
	}
	fmt.Fprintf(stdout, "%s %s (applies at next harnessd/TUI start)\n", state, args[0])
	return 0
}

func pluginInstall(args []string) int {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var yes bool
	fs.BoolVar(&yes, "yes", false, "confirm remote bundle surfaces without prompting")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "harnesscli plugin install: exactly one git URL, owner/repo, or local path is required")
		return 1
	}
	store, dir, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin install: %v\n", err)
		return 1
	}
	staged, err := plugins.NewInstaller(dir).Stage(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin install: %v\n", err)
		return 1
	}
	defer staged.Discard()
	if staged.Remote {
		fmt.Fprintf(stdout, "remote plugin %s@%s declares executable surfaces:\n", staged.Manifest.Name, staged.Manifest.Version)
		printPluginSurfaces(stdout, staged.Manifest)
		if !confirmPluginAction(fmt.Sprintf("install remote plugin %s@%s", staged.Manifest.Name, staged.Manifest.Version), yes) {
			fmt.Fprintln(stderr, "harnesscli plugin install: aborted; nothing installed")
			return 1
		}
	}
	installed, err := staged.Promote()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin install: %v\n", err)
		return 1
	}
	if err := store.RecordInstall(plugins.InstalledPlugin{Name: installed.Manifest.Name, Version: installed.Manifest.Version, Source: installed.Source.URL, Remote: installed.Remote}); err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin install: record state: %v\n", err)
		return 1
	}
	trust := "trusted"
	if installed.Remote {
		trust = "untrusted"
	}
	fmt.Fprintf(stdout, "installed %s@%s (%s)\n", installed.Manifest.Name, installed.Manifest.Version, trust)
	return 0
}

func pluginList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "harnesscli plugin list: no arguments expected")
		return 1
	}
	store, _, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin list: %v\n", err)
		return 1
	}
	items, err := store.List()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin list: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no installed plugin bundles")
		return 0
	}
	for _, item := range items {
		line := fmt.Sprintf("%s@%s enabled=%t trusted=%t source=%s", item.Name, item.Version, item.Enabled, item.Trusted, item.Source)
		if !item.Trusted {
			line += " (untrusted — commands/hooks/MCP inactive)"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

func pluginUninstall(args []string) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "harnesscli plugin uninstall: exactly one plugin name is required")
		return 1
	}
	store, dir, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin uninstall: %v\n", err)
		return 1
	}
	items, err := store.List()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin uninstall: %v\n", err)
		return 1
	}
	var found *plugins.InstalledPlugin
	for i := range items {
		if items[i].Name == args[0] {
			found = &items[i]
			break
		}
	}
	if found == nil {
		fmt.Fprintf(stderr, "harnesscli plugin uninstall: plugin %q is not installed\n", args[0])
		return 1
	}
	if err := os.RemoveAll(filepath.Join(dir, found.Name)); err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin uninstall: remove files: %v\n", err)
		return 1
	}
	if err := store.Remove(found.Name); err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin uninstall: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "uninstalled %s\n", found.Name)
	return 0
}

func pluginUpdate(args []string) int {
	fs := flag.NewFlagSet("plugin update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var yes bool
	fs.BoolVar(&yes, "yes", false, "confirm changed remote bundle surfaces without prompting")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "harnesscli plugin update: exactly one plugin name is required")
		return 1
	}
	store, dir, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin update: %v\n", err)
		return 1
	}
	items, err := store.List()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin update: %v\n", err)
		return 1
	}
	for _, item := range items {
		if item.Name != rest[0] {
			continue
		}
		staged, err := plugins.NewInstaller(dir).Stage(item.Source)
		if err != nil {
			fmt.Fprintf(stderr, "harnesscli plugin update: %v\n", err)
			return 1
		}
		defer staged.Discard()
		// A remote bundle whose declared surfaces changed crosses the trust
		// boundary again: show the new surfaces and re-require confirmation.
		changed := true
		if old, err := plugins.LoadBundle(filepath.Join(dir, item.Name, item.Version)); err == nil {
			changed = !sameDeclaredSurfaces(old.Manifest, staged.Manifest)
		}
		if item.Remote && changed {
			fmt.Fprintf(stdout, "remote plugin %s@%s declares new executable surfaces:\n", staged.Manifest.Name, staged.Manifest.Version)
			printPluginSurfaces(stdout, staged.Manifest)
			if !confirmPluginAction(fmt.Sprintf("update remote plugin %s to %s", item.Name, staged.Manifest.Version), yes) {
				fmt.Fprintln(stderr, "harnesscli plugin update: aborted; installed version unchanged")
				return 1
			}
		}
		installed, err := staged.Promote()
		if err != nil {
			fmt.Fprintf(stderr, "harnesscli plugin update: %v\n", err)
			return 1
		}
		if err := store.RecordInstall(plugins.InstalledPlugin{Name: installed.Manifest.Name, Version: installed.Manifest.Version, Source: installed.Source.URL, Remote: installed.Remote}); err != nil {
			fmt.Fprintf(stderr, "harnesscli plugin update: record state: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "updated %s@%s\n", installed.Manifest.Name, installed.Manifest.Version)
		return 0
	}
	fmt.Fprintf(stderr, "harnesscli plugin update: plugin %q is not installed\n", rest[0])
	return 1
}
