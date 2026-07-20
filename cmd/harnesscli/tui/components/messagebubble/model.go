package messagebubble

import "strings"

// Role identifies the sender of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Model renders a single conversation message bubble.
type Model struct {
	// Role is the message sender role.
	Role Role
	// Content is the message text.
	Content string
	// Width is the available rendering width.
	Width int
	// Styles overrides the bubble styles when non-nil (theme injection
	// point, epic #810); nil uses DefaultStyles().
	Styles *Styles
}

// New creates a new message bubble.
func New(role Role, content string) Model {
	return Model{Role: role, Content: content}
}

// View renders the message bubble via the role-specific renderer.
func (m Model) View() string {
	switch m.Role {
	case RoleUser:
		return UserBubble{Content: m.Content, Width: m.Width, Styles: m.Styles}.View()
	case RoleAssistant:
		return AssistantBubble{Content: m.Content, Width: m.Width, Styles: m.Styles}.View()
	case RoleTool:
		lines := WrapToolResult(m.Content, m.Width)
		if len(lines) == 0 {
			return ""
		}
		return strings.Join(lines, "\n") + "\n\n"
	default:
		return ""
	}
}
