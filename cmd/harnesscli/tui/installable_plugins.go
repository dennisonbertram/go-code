package tui

import (
	"os"
	"path/filepath"

	"go-agent-harness/internal/plugins"
)

func installablePluginCommandDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".go-harness", "plugins")
	// Command definitions may invoke a shell; unlike skills, load them only
	// after the bundle has been explicitly trusted.
	bundles, err := plugins.TrustedBundles(root, plugins.NewStateStore(filepath.Join(root, "state.json")))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, bundle := range bundles {
		if bundle.CommandsDir != "" {
			dirs = append(dirs, bundle.CommandsDir)
		}
	}
	return dirs
}
