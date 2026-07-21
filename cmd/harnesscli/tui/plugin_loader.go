package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tuiplugin "go-agent-harness/cmd/harnesscli/tui/plugin"
)

// LoadAndRegisterPlugins loads plugin command definitions from dir and registers
// them into the provided slash-command registry. It returns warning strings for
// loader errors and skipped plugin registrations.
func LoadAndRegisterPlugins(registry *CommandRegistry, dirs ...string) []string {
	if registry == nil {
		return nil
	}

	var warnings []string
	for _, dir := range dirs {
		defs, errs := tuiplugin.LoadPlugins(dir)
		for _, err := range errs {
			warnings = append(warnings, err.Error())
		}
		for _, def := range defs {
			if registry.IsRegistered(def.Name) {
				warnings = append(warnings, fmt.Sprintf("plugin %q skipped: command already registered", def.Name))
				continue
			}
			registry.Register(pluginCommandEntry(def))
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// legacyPluginsDirWarning returns a deprecation warning when dir (the legacy
// ~/.config/harnesscli/plugins directory) contains JSON plugin definitions,
// pointing the user at the installable bundle format and its plugin home. It
// returns "" when the directory is missing, unreadable, or holds no JSON
// files; broken files inside an existing dir are already surfaced as loader
// errors by LoadAndRegisterPlugins.
func legacyPluginsDirWarning(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("legacy plugin directory %s contains %d JSON plugin(s); the legacy JSON plugin format is deprecated — migrate to installable plugin bundles (plugin.json manifest) under ~/.go-harness/plugins", dir, count)
}

func pluginCommandEntry(def tuiplugin.PluginDef) CommandEntry {
	return CommandEntry{
		Name:        def.Name,
		Description: def.Description,
		Handler: func(cmd Command) CommandResult {
			var result tuiplugin.CommandResult
			switch def.Handler {
			case tuiplugin.HandlerBash:
				result = tuiplugin.ExecuteBash(def, cmd.Args)
			case tuiplugin.HandlerPrompt:
				result = tuiplugin.ExecutePrompt(def, slashArgsText(cmd.Raw))
			default:
				return ErrorResult(fmt.Sprintf("unsupported plugin handler %q", def.Handler))
			}
			if result.IsError {
				return ErrorResult(result.Output)
			}
			return CommandResult{
				Status: CmdOK,
				Output: result.Output,
			}
		},
	}
}
