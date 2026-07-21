package tui

import (
	"os"
	"path/filepath"

	"go-agent-harness/internal/plugins"
)

// BundleCommandSource identifies one trusted bundle's commands directory for
// slash-command loading (legacy JSON PluginDef files and markdown command
// files).
type BundleCommandSource struct {
	BundleName string
	Dir        string
}

// installablePluginCommandSources returns the commands directories of all
// enabled AND trusted bundles. Command definitions may invoke a shell; unlike
// skills, load them only after the bundle has been explicitly trusted.
func installablePluginCommandSources() []BundleCommandSource {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".go-harness", "plugins")
	bundles, err := plugins.TrustedBundles(root, plugins.NewStateStore(filepath.Join(root, "state.json")))
	if err != nil {
		return nil
	}
	var sources []BundleCommandSource
	for _, bundle := range bundles {
		if bundle.CommandsDir != "" {
			sources = append(sources, BundleCommandSource{BundleName: bundle.Manifest.Name, Dir: bundle.CommandsDir})
		}
	}
	return sources
}

// installablePluginCommandDirs returns only the directories, for the legacy
// JSON PluginDef loading path.
func installablePluginCommandDirs() []string {
	sources := installablePluginCommandSources()
	dirs := make([]string, 0, len(sources))
	for _, s := range sources {
		dirs = append(dirs, s.Dir)
	}
	return dirs
}
