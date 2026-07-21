package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds all key bindings for the TUI.
type KeyMap struct {
	// Input / submit
	Submit  key.Binding
	Newline key.Binding
	// Navigation
	ScrollUp   key.Binding
	ScrollDown key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	GotoTop    key.Binding
	GotoBottom key.Binding
	// Commands
	SlashCmd  key.Binding
	AtMention key.Binding
	// ShellMode is entered with "!" on an empty input and left with
	// Backspace/Esc on an empty input (epic #811).
	ShellMode key.Binding
	// Actions
	Interrupt key.Binding
	Help      key.Binding
	Dashboard key.Binding
	Quit      key.Binding
	Copy      key.Binding
	// Steer injects the input-box content into the active run as a steering
	// message (epic #820) without cancelling it. Bound to ctrl+g: ctrl+s is
	// taken by Copy, and ctrl+r stays reserved for a future history-search
	// binding per terminal convention (both verified unbound under
	// cmd/harnesscli at implementation time).
	Steer key.Binding
	// PasteImage attaches a clipboard image to the input as a placeholder
	// chip (epic #818, slice 2).
	PasteImage key.Binding
	// Modes
	EditMode key.Binding
	// ExpandTool toggles the expanded/collapsed view for the active tool call.
	ExpandTool key.Binding
}

// DefaultKeyMap returns the standard key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "submit"),
		),
		Newline: key.NewBinding(
			key.WithKeys("shift+enter", "ctrl+j"),
			key.WithHelp("shift+enter", "newline"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("up", "ctrl+p"),
			key.WithHelp("up", "history up"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("down", "ctrl+n"),
			key.WithHelp("down", "history down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdn", "page down"),
		),
		GotoTop: key.NewBinding(
			key.WithKeys("home"),
			key.WithHelp("home", "scroll to top"),
		),
		GotoBottom: key.NewBinding(
			key.WithKeys("end"),
			key.WithHelp("end", "scroll to bottom"),
		),
		SlashCmd: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "commands"),
		),
		AtMention: key.NewBinding(
			key.WithKeys("@"),
			key.WithHelp("@", "mention file"),
		),
		ShellMode: key.NewBinding(
			key.WithKeys("!"),
			key.WithHelp("!", "shell mode (esc/backspace on empty to exit)"),
		),
		Interrupt: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "interrupt"),
		),
		Help: key.NewBinding(
			key.WithKeys("ctrl+h", "?"),
			key.WithHelp("?", "help"),
		),
		Dashboard: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "dashboard"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Copy: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "copy last response"),
		),
		Steer: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "steer run"),
		),
		PasteImage: key.NewBinding(
			key.WithKeys("ctrl+v"),
			key.WithHelp("ctrl+v", "paste image from clipboard"),
		),
		EditMode: key.NewBinding(
			key.WithKeys("ctrl+e"),
			key.WithHelp("ctrl+e", "editor"),
		),
		ExpandTool: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "expand tool"),
		),
	}
}

// ShortHelp implements key.Map for the help component.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Interrupt, k.Steer, k.SlashCmd, k.Help, k.Dashboard, k.Quit, k.Newline, k.Copy}
}

// FullHelp implements key.Map for the help component.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Submit, k.Newline, k.Interrupt, k.Steer, k.Quit},
		{k.ScrollUp, k.ScrollDown, k.PageUp, k.PageDown, k.GotoTop, k.GotoBottom},
		{k.SlashCmd, k.AtMention, k.ShellMode, k.Help, k.Dashboard},
		{k.EditMode, k.ExpandTool},
	}
}
