package helpdialog

// Tab identifies which help tab is active.
type Tab int

const (
	TabCommands    Tab = iota // slash command list
	TabKeybindings            // keyboard shortcuts
	TabAbout                  // version + runtime info
)

const tabCount = 3

// KeyEntry is one keybinding for display.
type KeyEntry struct {
	Keys        string
	Description string
}

// CommandEntry is one slash command for display.
type CommandEntry struct {
	Name        string
	Description string
}

// Model is the help dialog state.
// All methods return a new Model (value semantics — safe for concurrent use
// when each goroutine holds its own copy).
type Model struct {
	activeTab    Tab
	commands     []CommandEntry
	keybindings  []KeyEntry
	aboutLines   []string
	active       bool
	scrollOffset int
}

// New creates a new help dialog Model with the given content.
// The dialog starts inactive (not shown).
func New(commands []CommandEntry, keybindings []KeyEntry, aboutLines []string) Model {
	cmdCopy := make([]CommandEntry, len(commands))
	copy(cmdCopy, commands)

	keyCopy := make([]KeyEntry, len(keybindings))
	copy(keyCopy, keybindings)

	aboutCopy := make([]string, len(aboutLines))
	copy(aboutCopy, aboutLines)

	return Model{
		activeTab:   TabCommands,
		commands:    cmdCopy,
		keybindings: keyCopy,
		aboutLines:  aboutCopy,
	}
}

// Open activates the dialog overlay, always resetting to the Commands tab at scroll offset 0.
// This ensures reopening /help always starts fresh on Commands at the top.
func (m Model) Open() Model {
	m.active = true
	m.activeTab = TabCommands
	m.scrollOffset = 0
	return m
}

// Close deactivates the dialog overlay.
func (m Model) Close() Model {
	m.active = false
	return m
}

// IsActive reports whether the dialog is currently visible.
func (m Model) IsActive() bool {
	return m.active
}

// NextTab cycles the active tab forward (wraps around).
func (m Model) NextTab() Model {
	m.activeTab = Tab((int(m.activeTab) + 1) % tabCount)
	m.scrollOffset = 0
	return m
}

// PrevTab cycles the active tab backward (wraps around).
func (m Model) PrevTab() Model {
	m.activeTab = Tab((int(m.activeTab) - 1 + tabCount) % tabCount)
	m.scrollOffset = 0
	return m
}

// ScrollDown moves the scroll offset down by n lines.
func (m Model) ScrollDown(n int) Model {
	m.scrollOffset += n
	return m
}

// ScrollUp moves the scroll offset up by n lines (clamped to 0).
func (m Model) ScrollUp(n int) Model {
	m.scrollOffset -= n
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	return m
}

// ActiveTab returns the currently active tab.
func (m Model) ActiveTab() Tab {
	return m.activeTab
}

// View renders the help dialog at the given terminal dimensions.
// If width or height are zero, defaults of 60x15 are used.
func (m Model) View(width, height int) string {
	if width <= 0 {
		width = 60
	}
	if height <= 0 {
		height = 15
	}
	return render(m, width, height)
}
