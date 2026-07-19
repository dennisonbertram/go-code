package tui

import (
	"fmt"

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
				result = tuiplugin.ExecutePrompt(def, cmd.Args)
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
