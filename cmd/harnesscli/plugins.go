package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-agent-harness/internal/plugins"
)

func runPlugin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: harnesscli plugin <install|list|uninstall|update> [source|name]")
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
	default:
		fmt.Fprintf(stderr, "harnesscli plugin: unknown subcommand %q (try: install, list, uninstall, update)\n", args[0])
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

func pluginInstall(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "harnesscli plugin install: exactly one git URL, owner/repo, or local path is required")
		return 1
	}
	store, dir, err := pluginStore()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli plugin install: %v\n", err)
		return 1
	}
	installed, err := plugins.NewInstaller(dir).Install(args[0])
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
		fmt.Fprintf(stdout, "%s@%s enabled=%t trusted=%t source=%s\n", item.Name, item.Version, item.Enabled, item.Trusted, item.Source)
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
	if len(args) != 1 {
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
		if item.Name != args[0] {
			continue
		}
		installed, err := plugins.NewInstaller(dir).Install(item.Source)
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
	fmt.Fprintf(stderr, "harnesscli plugin update: plugin %q is not installed\n", args[0])
	return 1
}
