package inputarea

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CommandSubmittedMsg is sent when the user presses Enter with content.
type CommandSubmittedMsg struct{ Value string }

// AutocompleteProvider is called when Tab is pressed to get completions.
// Returns a list of completion strings for the current input value.
type AutocompleteProvider func(input string) []string

// Model is the multiline input area component.
type Model struct {
	width        int
	value        string
	cursor       int
	history      History
	focused      bool
	autocomplete AutocompleteProvider // may be nil
	shellMode    bool
	attachments  []Attachment
}

var (
	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
			Bold(true)
	inputStyle = lipgloss.NewStyle()
	// shellBorderStyle frames the input area while shell mode is active. The
	// violet border mirrors kimi-code's shell mode so the mode is visible at a
	// glance; the "!" prompt marker uses the same violet as the normal prompt.
	shellBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})
)

const promptSymbol = "❯"
const shellPromptSymbol = "!"

// New creates a new input area for the given width.
func New(width int) Model {
	return Model{width: width, history: NewHistory(100), focused: true}
}

// NewWithHistory creates a new input area for the given width, pre-populated
// with the given History. This is used to restore history from persistent storage.
func NewWithHistory(width int, h History) Model {
	return Model{width: width, history: h, focused: true}
}

// HistoryState returns the current History for testing and introspection.
func (m Model) HistoryState() History { return m.history }

// Value returns the current input text.
func (m Model) Value() string { return m.value }

// SetValue replaces the input text and moves the cursor to the end.
func (m Model) SetValue(v string) Model {
	m.value = v
	m.cursor = len([]rune(v))
	return m
}

// SetShellMode toggles shell-mode rendering ("!" prompt marker + violet
// border). The buffer, cursor, and history are untouched. Value semantics.
func (m Model) SetShellMode(on bool) Model {
	m.shellMode = on
	return m
}

// ShellMode reports whether shell-mode rendering is active.
func (m Model) ShellMode() bool { return m.shellMode }

// SetWidth updates the display width.
func (m *Model) SetWidth(w int) { m.width = w }

// Focus sets keyboard focus.
func (m *Model) Focus() { m.focused = true }

// Blur removes keyboard focus.
func (m *Model) Blur() { m.focused = false }

// SetAutocompleteProvider sets the provider used for Tab completion.
func (m Model) SetAutocompleteProvider(fn AutocompleteProvider) Model {
	m.autocomplete = fn
	return m
}

// Clear resets the text buffer and cursor position to empty without affecting
// history or other state. Returns the updated Model (value semantics).
func (m Model) Clear() Model {
	m.value = ""
	m.cursor = 0
	m.history = m.history.ResetPos()
	return m
}

// CompleteTab applies tab completion:
//  1. Call autocomplete(current input)
//  2. If exactly one result, replace input with that result + " "
//  3. If multiple results, return the common prefix completion
//  4. If zero results, no-op
//
// Returns (new Model, completed bool).
func (m Model) CompleteTab() (Model, bool) {
	if m.autocomplete == nil {
		return m, false
	}
	if m.value == "" {
		return m, false
	}

	completions := m.autocomplete(m.value)
	if len(completions) == 0 {
		return m, false
	}

	if len(completions) == 1 {
		m.value = completions[0] + " "
		m.cursor = len([]rune(m.value))
		return m, true
	}

	// Multiple completions: compute common prefix
	prefix := commonPrefix(completions)
	if prefix == "" || prefix == m.value {
		// Nothing to complete further
		return m, false
	}
	m.value = prefix
	m.cursor = len([]rune(m.value))
	return m, true
}

// commonPrefix returns the longest common prefix among all strings in ss.
func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	prefix := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, prefix) {
			if len(prefix) == 0 {
				return ""
			}
			// Shrink prefix by one rune
			runes := []rune(prefix)
			prefix = string(runes[:len(runes)-1])
		}
	}
	return prefix
}

// Update handles key messages for the input area.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.Type {
	case tea.KeyCtrlC:
		// Clear current input buffer; do NOT quit — parent handles quit.
		if m.value != "" {
			m.value = ""
			m.cursor = 0
			// Reset history navigation position back to draft without losing history.
			m.history = m.history.ResetPos()
		}
		return m, nil

	case tea.KeyEnter:
		if strings.TrimSpace(m.value) == "" {
			return m, nil
		}
		submitted := m.value
		m.history = m.history.Push(submitted)
		m.value = ""
		m.cursor = 0
		return m, func() tea.Msg { return CommandSubmittedMsg{Value: submitted} }

	case tea.KeyCtrlJ: // alternative newline (ctrl+j / shift+enter)
		m.value = m.value[:m.cursor] + "\n" + m.value[m.cursor:]
		m.cursor++

	case tea.KeyTab:
		newM, _ := m.CompleteTab()
		return newM, nil

	case tea.KeyBackspace, tea.KeyDelete:
		if m.value == "" && len(m.attachments) > 0 {
			// Backspace on an empty buffer removes the most recent
			// attachment chip (and its temp files) instead of deleting text.
			m.removeLastAttachment()
			break
		}
		if m.cursor > 0 && len(m.value) > 0 {
			runes := []rune(m.value)
			if m.cursor <= len(runes) {
				runes = append(runes[:m.cursor-1], runes[m.cursor:]...)
				m.value = string(runes)
				m.cursor--
			}
		}

	case tea.KeyLeft:
		if m.cursor > 0 {
			m.cursor--
		}

	case tea.KeyRight:
		if m.cursor < len([]rune(m.value)) {
			m.cursor++
		}

	case tea.KeyUp:
		// History navigation — newest-first.
		var text string
		m.history, text = m.history.Up(m.value)
		m.value = text
		m.cursor = len([]rune(m.value))

	case tea.KeyDown:
		if m.history.AtDraft() {
			return m, nil
		}
		var text string
		m.history, text = m.history.Down()
		m.value = text
		m.cursor = len([]rune(m.value))

	case tea.KeyRunes:
		runes := []rune(m.value)
		insert := key.Runes
		newRunes := make([]rune, 0, len(runes)+len(insert))
		newRunes = append(newRunes, runes[:m.cursor]...)
		newRunes = append(newRunes, insert...)
		newRunes = append(newRunes, runes[m.cursor:]...)
		m.value = string(newRunes)
		m.cursor += len(insert)

	case tea.KeySpace:
		runes := []rune(m.value)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.cursor]...)
		newRunes = append(newRunes, ' ')
		newRunes = append(newRunes, runes[m.cursor:]...)
		m.value = string(newRunes)
		m.cursor++
	}

	return m, nil
}

// View renders the input area with prompt symbol and cursor across multiple lines.
func (m Model) View() string {
	return m.renderLines(0)
}

// MultilineView renders the input area, limiting output to maxLines visible lines.
// If maxLines <= 0, all lines are shown.
func (m Model) MultilineView(maxLines int) string {
	return m.renderLines(maxLines)
}

// renderLines is the shared implementation for View and MultilineView.
func (m Model) renderLines(maxLines int) string {
	prompt := promptStyle.Render(promptSymbol)
	if m.shellMode {
		prompt = promptStyle.Render(shellPromptSymbol)
	}
	indent := "  " // align continuation lines under text after prompt

	runes := []rune(m.value)
	width := m.width - 3 // "❯ " = 2 + 1 margin
	if m.shellMode {
		width -= 2 // the shell-mode border occupies one column on each side
	}
	if width < 10 {
		width = 10
	}

	// Split value into logical lines at newlines.
	var logicalLines [][]rune
	current := []rune{}
	for _, r := range runes {
		if r == '\n' {
			logicalLines = append(logicalLines, current)
			current = []rune{}
		} else {
			current = append(current, r)
		}
	}
	logicalLines = append(logicalLines, current)

	// Determine which logical line and column the cursor is on.
	cursorLine, cursorCol := 0, 0
	pos := 0
	for i, line := range logicalLines {
		lineEnd := pos + len(line)
		if m.cursor <= lineEnd {
			cursorLine = i
			cursorCol = m.cursor - pos
			break
		}
		pos = lineEnd + 1 // +1 for the \n
		if i == len(logicalLines)-1 {
			cursorLine = i
			cursorCol = m.cursor - pos
		}
	}

	caretStyle := lipgloss.NewStyle().Reverse(true)

	var sb strings.Builder
	for i, line := range logicalLines {
		if maxLines > 0 && i >= maxLines {
			break
		}

		var rendered string
		if i == cursorLine {
			col := cursorCol
			if col < 0 {
				col = 0
			}
			if col > len(line) {
				col = len(line)
			}
			before := string(line[:col])
			var caret, after string
			if col < len(line) {
				caret = caretStyle.Render(string(line[col]))
				after = string(line[col+1:])
			} else {
				caret = caretStyle.Render(" ")
				after = ""
			}
			rendered = before + caret + after
		} else {
			rendered = string(line)
		}

		if i == 0 {
			sb.WriteString(prompt + " " + rendered)
		} else {
			sb.WriteString("\n" + indent + rendered)
		}
	}

	out := sb.String()
	if chips := m.chipsView(); chips != "" {
		// Attachment chips render on their own row above the prompt.
		out = chips + "\n" + out
	}
	if m.shellMode {
		// Frame the whole input in the violet shell-mode border. Width() sets
		// the content width; the border columns sit outside it, so subtract 2
		// to keep the framed box within the component's total width.
		frameWidth := m.width - 2
		if frameWidth < 12 {
			frameWidth = 12
		}
		out = shellBorderStyle.Width(frameWidth).Render(out)
	}
	return out
}

// Init satisfies tea.Model for standalone use.
func (m Model) Init() tea.Cmd { return nil }
