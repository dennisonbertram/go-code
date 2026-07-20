package tooluse

import (
	"strings"

	"go-agent-harness/cmd/harnesscli/tui/components/diffview"
)

// Model renders a tool call with its status, arguments, and result.
type Model struct {
	// CallID uniquely identifies this tool call.
	CallID string
	// ToolName is the name of the tool being called.
	ToolName string
	// Status tracks the call lifecycle (pending, running, completed, failed).
	Status string
	// Expanded controls whether the full output is shown.
	Expanded bool
	// Width is the available rendering width.
	Width int
	// Args is the pre-formatted argument string shown in the header.
	Args string
	// Params are optional parsed key/value parameters for expanded rendering.
	Params []Param
	// Result is the result content for completed calls.
	Result string
	// ErrorText is the error content for failed calls.
	ErrorText string
	// Hint is an optional actionable hint for error rendering.
	Hint string
	// Duration is an optional pre-formatted duration string.
	Duration string
	// Timestamp is an optional footer timestamp for expanded rendering.
	Timestamp string
	// Timer tracks lifecycle duration when the root model measures it.
	Timer Timer
	// Command is an optional shell command label for bash-style output.
	Command string
	// MaxLines limits bash output before truncation. 0 uses the component default.
	MaxLines int
	// DiffStyles overrides the palette used to render unified-diff results
	// when non-nil (theme injection point, epic #810); nil uses
	// diffview.DefaultStyles().
	DiffStyles *diffview.Styles
}

// New creates a new tool use display model.
func New(callID, toolName string) Model {
	return Model{CallID: callID, ToolName: toolName}
}

func (m Model) viewState() State {
	switch strings.ToLower(m.Status) {
	case "completed", "done", "success":
		return StateCompleted
	case "error", "failed":
		return StateError
	default:
		return StateRunning
	}
}

func (m Model) viewArgs() string {
	if m.Args != "" {
		return m.Args
	}
	return m.CallID
}

func looksLikeUnifiedDiff(result string) bool {
	return strings.HasPrefix(result, "diff --git ") ||
		strings.HasPrefix(result, "--- ") ||
		strings.HasPrefix(result, "@@ ") ||
		strings.Contains(result, "\ndiff --git ") ||
		strings.Contains(result, "\n--- ") ||
		strings.Contains(result, "\n@@ ")
}

// View renders the tool use display through the existing component hierarchy.
func (m Model) View() string {
	width := m.Width
	if width <= 0 {
		width = defaultWidth
	}

	state := m.viewState()
	if state == StateError || m.ErrorText != "" {
		return ErrorView{
			ToolName:  m.ToolName,
			ErrorText: m.ErrorText,
			Hint:      m.Hint,
			Width:     width,
		}.View()
	}

	if m.Expanded {
		if m.Command != "" || strings.EqualFold(m.ToolName, "bash") {
			var sb strings.Builder
			sb.WriteString(CollapsedView{
				ToolName: m.ToolName,
				Args:     m.viewArgs(),
				State:    state,
				Width:    width,
				Timer:    m.Timer,
			}.View())
			sb.WriteString(BashOutput{
				Command:  m.Command,
				Output:   m.Result,
				MaxLines: m.MaxLines,
				Width:    width,
			}.View())
			return sb.String()
		}

		if looksLikeUnifiedDiff(m.Result) {
			diff := diffview.Model{
				FilePath: m.viewArgs(),
				Diff:     m.Result,
				Width:    width,
				Styles:   m.DiffStyles,
			}
			if renderedDiff := diff.View(); renderedDiff != "" {
				var sb strings.Builder
				sb.WriteString(CollapsedView{
					ToolName: m.ToolName,
					Args:     m.viewArgs(),
					State:    state,
					Width:    width,
					Timer:    m.Timer,
				}.View())
				for _, p := range m.Params {
					sb.WriteString(renderTreeLine(p.Key+": "+p.Value, width))
					sb.WriteString("\n")
				}
				sb.WriteString(renderedDiff)
				dur := ExpandedView{
					Duration:  m.Duration,
					Timestamp: m.Timestamp,
					Timer:     m.Timer,
				}.effectiveDuration()
				if dur != "" || m.Timestamp != "" {
					sb.WriteString(renderDurationLine(dur, m.Timestamp, width))
					sb.WriteString("\n")
				}
				return sb.String()
			}
		}

		return ExpandedView{
			ToolName:  m.ToolName,
			Args:      m.viewArgs(),
			Params:    m.Params,
			Result:    m.Result,
			State:     state,
			Duration:  m.Duration,
			Timestamp: m.Timestamp,
			Width:     width,
			Timer:     m.Timer,
		}.View()
	}

	return CollapsedView{
		ToolName: m.ToolName,
		Args:     m.viewArgs(),
		State:    state,
		Width:    width,
		Hint:     m.Hint,
		Timer:    m.Timer,
	}.View()
}
