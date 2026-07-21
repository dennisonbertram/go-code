package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	"go-agent-harness/cmd/harnesscli/tui/components/transcriptexport"
	tuiplugin "go-agent-harness/cmd/harnesscli/tui/plugin"
	"go-agent-harness/internal/skills"
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

// LoadAndRegisterBundleCommands loads markdown command files (*.md) from each
// trusted bundle's commands directory and registers them as slash commands
// whose expanded bodies are submitted as prompts (unlike legacy JSON prompt
// plugins, which display their output). A name that collides with an existing
// command is registered as "<bundle>:<name>" instead; if that is taken too,
// the command is skipped. Namespacings and skips are returned as warnings.
func LoadAndRegisterBundleCommands(registry *CommandRegistry, sources ...BundleCommandSource) []string {
	if registry == nil {
		return nil
	}

	var warnings []string
	for _, source := range sources {
		defs, errs := tuiplugin.LoadMarkdownCommands(source.Dir)
		for _, err := range errs {
			warnings = append(warnings, err.Error())
		}
		for _, def := range defs {
			name := def.Name
			if registry.IsRegistered(name) {
				namespaced := source.BundleName + ":" + def.Name
				if registry.IsRegistered(namespaced) {
					warnings = append(warnings, fmt.Sprintf("plugin command %q from bundle %q skipped: %q and %q are both registered", def.Name, source.BundleName, def.Name, namespaced))
					continue
				}
				warnings = append(warnings, fmt.Sprintf("plugin command %q from bundle %q registered as %q: plain name already registered", def.Name, source.BundleName, namespaced))
				name = namespaced
			}
			registry.Register(markdownCommandEntry(name, def))
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// markdownCommandEntry builds the registry entry for a bundle markdown
// command. The Handler is a no-op like the built-in commands; Execute expands
// the body template with skills argument semantics and submits the result as
// a user prompt, mirroring the normal submit path (transcript, bubble, run).
func markdownCommandEntry(name string, def tuiplugin.MarkdownCommand) CommandEntry {
	return CommandEntry{
		Name:        name,
		Description: def.Description,
		Handler:     func(Command) CommandResult { return CommandResult{Status: CmdOK} },
		Execute: func(m *Model, cmd Command) ([]tea.Cmd, bool) {
			prompt := expandMarkdownCommand(def, m.config.Workspace, cmd)
			// Reset assistant text accumulator for the new user turn.
			m.lastAssistantText = ""
			m.responseStarted = false
			m.activeAssistantLineCount = 0
			m.clearThinkingBar()
			m.pendingLastMsg = truncateStr(prompt, 60)
			m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
				Role:      "user",
				Content:   prompt,
				Timestamp: time.Now(),
			})
			m.appendMessageBubble(messagebubble.RoleUser, prompt)
			effModel, effProvider := m.effectiveModelAndProvider()
			return []tea.Cmd{startRunCmd(m.config.BaseURL, prompt, m.conversationID, effModel, effProvider, m.selectedReasoningEffort, m.selectedProfile, m.config.Workspace, m.config.APIKey, nil, m.extraDirs, m.planMode)}, false
		},
	}
}

// expandMarkdownCommand expands a markdown command's body template with the
// shared skills argument-expansion contract: $ARGUMENTS is the raw argument
// text as typed, $0..$n are SplitArgs tokens, $WORKSPACE is the session
// workspace, $SKILL_DIR is the command file's directory, and args are
// appended as a trailing "ARGUMENTS: <args>" line when the body references no
// placeholder.
func expandMarkdownCommand(def tuiplugin.MarkdownCommand, workspace string, cmd Command) string {
	return skills.ExpandTemplate(def.Body, nil, rawCommandArgs(cmd), workspace, filepath.Dir(def.FilePath))
}

// rawCommandArgs returns the argument text exactly as typed after the command
// name, preserving quotes, so markdown command expansion tokenizes with the
// same SplitArgs semantics as skill invocation.
func rawCommandArgs(cmd Command) string {
	rest := strings.TrimPrefix(cmd.Raw, "/")
	if idx := strings.IndexAny(rest, " \t"); idx >= 0 {
		return strings.TrimSpace(rest[idx+1:])
	}
	return ""
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
