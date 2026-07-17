package tui

import (
	"sort"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Command represents a parsed slash command.
type Command struct {
	Name string   // lowercase, no leading slash
	Args []string // split arguments (may be empty)
	Raw  string   // original text including slash
}

// ParseCommand parses a string like "/clear" or "/help foo bar" into a Command.
// Returns (cmd, true) on success, (zero, false) if input doesn't start with '/'.
// Names are lowercased. Arguments are whitespace-split.
// Empty command name (after trimming) returns (zero, false).
func ParseCommand(input string) (Command, bool) {
	// Must start with '/'
	if !strings.HasPrefix(input, "/") {
		return Command{}, false
	}

	// Strip the leading slash and split on whitespace
	rest := input[1:]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		// "/" or "/  " with only whitespace
		return Command{}, false
	}

	name := strings.ToLower(fields[0])
	if name == "" {
		return Command{}, false
	}

	args := []string{}
	if len(fields) > 1 {
		args = fields[1:]
	}

	return Command{
		Name: name,
		Args: args,
		Raw:  input,
	}, true
}

// CommandEntry describes one registered command.
type CommandEntry struct {
	Name        string
	Aliases     []string
	Description string
	Handler     func(cmd Command) CommandResult
	Execute     func(m *Model, cmd Command) ([]tea.Cmd, bool)
}

// CommandRegistry is the dispatch table for built-in commands.
// It is safe for concurrent use after construction.
type CommandRegistry struct {
	mu      sync.RWMutex
	entries []CommandEntry
	// index maps name and aliases to entry index for O(1) lookup
	index map[string]int
}

// newEmptyCommandRegistry creates an empty registry with no pre-registered commands.
// Useful for testing.
func newEmptyCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		index: make(map[string]int),
	}
}

func builtinCommandEntries() []CommandEntry {
	return []CommandEntry{
		{
			Name:        "clear",
			Description: "Clear conversation history",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeClearCommand,
		},
		{
			Name:        "context",
			Description: "View context window usage",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeContextCommand,
		},
		{
			Name:        "export",
			Description: "Export conversation to markdown",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeExportCommand,
		},
		{
			Name:        "help",
			Description: "Show help dialog",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeHelpCommand,
		},
		{
			Name:        "keys",
			Description: "Manage provider API keys",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeKeysCommand,
		},
		{
			Name:        "model",
			Description: "Select AI model",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeModelCommand,
		},
		{
			Name:        "quit",
			Description: "Quit the TUI",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeQuitCommand,
		},
		{
			Name:        "stats",
			Description: "Show cost and token statistics",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeStatsCommand,
		},
		{
			Name:        "cost",
			Description: "Show running cost and token usage",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeCostCommand,
		},
		{
			Name:        "config",
			Description: "View current session configuration",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeConfigCommand,
		},
		{
			Name:        "subagents",
			Description: "View active subagent processes",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeSubagentsCommand,
		},
		{
			Name:        "hooks",
			Description: "List loaded and skipped lifecycle hooks",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeHooksCommand,
		},
		{
			Name:        "profiles",
			Description: "View and select a profile for next run",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeProfilesCommand,
		},
		{
			Name:        "sessions",
			Description: "Browse and switch between past sessions",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeSessionsCommand,
		},
		{
			Name:        "new",
			Description: "Start a new session (resets conversation)",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeNewSessionCommand,
		},
		{
			Name:        "search",
			Description: "Search current session transcript",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeSearchCommand,
		},
		{
			Name:        "history",
			Description: "Search across stored session metadata",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeHistoryCommand,
		},
		{
			Name:        "attach",
			Description: "Attach file context with @path tokens",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeAttachCommand,
		},
		{
			Name:        "runs",
			Description: "List recent harness runs",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeRunsCommand,
		},
		{
			Name:        "cancel",
			Description: "Cancel a harness run",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeCancelCommand,
		},
		{
			Name:        "replay",
			Description: "Replay a run",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeReplayCommand,
		},
		{
			Name:        "resume",
			Aliases:     []string{"continue"},
			Description: "Continue a completed run",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeResumeCommand,
		},
		{
			Name:        "doctor",
			Description: "Show local harness diagnostic commands",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executeDoctorCommand,
		},
		{
			Name:        "permissions",
			Description: "View session tool permissions",
			Handler: func(cmd Command) CommandResult {
				return CommandResult{Status: CmdOK}
			},
			Execute: executePermissionsCommand,
		},
	}
}

// NewCommandRegistry creates a registry pre-populated with the built-in command entries.
func NewCommandRegistry() *CommandRegistry {
	r := &CommandRegistry{
		index: make(map[string]int),
	}

	for _, e := range builtinCommandEntries() {
		r.Register(e)
	}

	return r
}

// Register adds a CommandEntry to the registry.
// If an entry with the same Name already exists it is replaced.
// Aliases are also indexed for dispatch.
func (r *CommandRegistry) Register(entry CommandEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if name already exists; if so replace it.
	if idx, ok := r.index[entry.Name]; ok {
		// Remove all old aliases that pointed to this entry before replacing,
		// so stale aliases don't remain in the index after the update.
		old := r.entries[idx]
		for _, alias := range old.Aliases {
			if r.index[alias] == idx {
				delete(r.index, alias)
			}
		}
		r.entries[idx] = entry
		// Index the new aliases.
		for _, alias := range entry.Aliases {
			r.index[alias] = idx
		}
		return
	}

	idx := len(r.entries)
	r.entries = append(r.entries, entry)
	r.index[entry.Name] = idx
	for _, alias := range entry.Aliases {
		r.index[alias] = idx
	}
}

// Dispatch looks up the command by Name in the registry and calls its handler.
// Returns UnknownResult if the command is not found.
func (r *CommandRegistry) Dispatch(cmd Command) CommandResult {
	r.mu.RLock()
	idx, ok := r.index[cmd.Name]
	var handler func(Command) CommandResult
	if ok {
		handler = r.entries[idx].Handler
	}
	r.mu.RUnlock()

	if !ok || handler == nil {
		return UnknownResult(cmd.Name)
	}
	return handler(cmd)
}

// All returns a copy of all registered entries sorted by Name.
func (r *CommandRegistry) All() []CommandEntry {
	r.mu.RLock()
	cp := make([]CommandEntry, len(r.entries))
	copy(cp, r.entries)
	r.mu.RUnlock()

	sort.Slice(cp, func(i, j int) bool {
		return cp[i].Name < cp[j].Name
	})
	return cp
}

// Lookup returns the CommandEntry for the given name (or alias).
// Returns (zero, false) if not found.
func (r *CommandRegistry) Lookup(name string) (CommandEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	idx, ok := r.index[name]
	if !ok {
		return CommandEntry{}, false
	}
	return r.entries[idx], true
}

// IsRegistered reports whether the given name (or alias) is registered.
func (r *CommandRegistry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.index[name]
	return ok
}
