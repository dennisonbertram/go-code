package tui

// approval.go — tool.approval_required TUI overlay.
// Messages, types, and HTTP commands for the tool-approval TUI integration.
// Structurally mirrors askuser.go, but simpler: the pending call is fully
// described by the tool.approval_required SSE payload itself, so no extra
// fetch round-trip is needed before the overlay can be shown.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── Tool Approval State (stored on Model) ───────────────────────────────────

// toolApprovalState holds all state for an in-progress tool-approval decision.
// Zero value = no pending approval.
type toolApprovalState struct {
	active     bool
	runID      string
	callID     string
	tool       string
	arguments  string // pretty-printed, truncated for display
	deadlineAt time.Time
}

// ─── Tool Approval Messages ──────────────────────────────────────────────────

// ToolApprovalDecidedMsg is sent when POST /v1/runs/{id}/approve or
// POST /v1/runs/{id}/deny succeeds. Decision is "approved" or "denied".
type ToolApprovalDecidedMsg struct {
	RunID    string
	Decision string
}

// ToolApprovalErrorMsg is sent when the approve/deny POST fails.
type ToolApprovalErrorMsg struct {
	Err string
}

// ─── HTTP Commands ───────────────────────────────────────────────────────────

// approveToolCmd sends POST /v1/runs/{id}/approve. On success it returns
// ToolApprovalDecidedMsg{Decision: "approved"}; on failure it returns
// ToolApprovalErrorMsg.
func approveToolCmd(baseURL, runID string) tea.Cmd {
	return toolApprovalDecisionCmd(baseURL, runID, "approve", "approved")
}

// denyToolCmd sends POST /v1/runs/{id}/deny. On success it returns
// ToolApprovalDecidedMsg{Decision: "denied"}; on failure it returns
// ToolApprovalErrorMsg.
func denyToolCmd(baseURL, runID string) tea.Cmd {
	return toolApprovalDecisionCmd(baseURL, runID, "deny", "denied")
}

// toolApprovalDecisionCmd posts an empty body to /v1/runs/{id}/{path} and
// translates the response into ToolApprovalDecidedMsg or ToolApprovalErrorMsg.
func toolApprovalDecisionCmd(baseURL, runID, path, decision string) tea.Cmd {
	return func() tea.Msg {
		postURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/" + path
		resp, err := httpClientWithTimeout.Post(postURL, "application/json", bytes.NewReader(nil))
		if err != nil {
			return ToolApprovalErrorMsg{Err: fmt.Sprintf("%s tool call: %s", path, err.Error())}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return ToolApprovalErrorMsg{Err: fmt.Sprintf("%s tool call: HTTP %d", path, resp.StatusCode)}
		}
		return ToolApprovalDecidedMsg{RunID: runID, Decision: decision}
	}
}

// handleToolApprovalKey processes a key message when the tool-approval overlay
// is active. It returns the new toolApprovalState and an optional tea.Cmd
// using value semantics (no mutation of the receiver).
//
// Key routing:
//   - a / y / enter: approve the pending tool call
//   - d / n: deny the pending tool call
//   - esc: cancel the run via the existing /cancel plumbing
func (m Model) handleToolApprovalKey(msg tea.KeyMsg) (toolApprovalState, tea.Cmd) {
	st := m.toolApproval
	if !st.active {
		return st, nil
	}

	runID := st.runID

	switch msg.Type {
	case tea.KeyEnter:
		return toolApprovalState{}, approveToolCmd(m.config.BaseURL, runID)
	case tea.KeyEsc:
		return toolApprovalState{}, cancelRunCmd(m.config.BaseURL, runID)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "a", "A", "y", "Y":
			return toolApprovalState{}, approveToolCmd(m.config.BaseURL, runID)
		case "d", "D", "n", "N":
			return toolApprovalState{}, denyToolCmd(m.config.BaseURL, runID)
		}
	}
	return st, nil
}

// renderToolApprovalOverlay renders the tool-approval overlay as a list of
// lines. Returns nil if no overlay is active.
func (m Model) renderToolApprovalOverlay() []string {
	if !m.toolApproval.active {
		return nil
	}
	lines := []string{
		"",
		"┌─ Tool Approval Required ─────────────────────────",
		"│  Tool: " + m.toolApproval.tool,
		"│",
	}
	for _, line := range strings.Split(m.toolApproval.arguments, "\n") {
		lines = append(lines, "│  "+line)
	}
	lines = append(lines, "│")
	lines = append(lines, "│  [a]pprove  [d]eny  (esc cancels the run)")
	if !m.toolApproval.deadlineAt.IsZero() {
		remaining := time.Until(m.toolApproval.deadlineAt)
		if remaining > 0 {
			lines = append(lines, fmt.Sprintf("│  Deadline: %s remaining", remaining.Round(time.Second)))
		} else {
			lines = append(lines, "│  Deadline: expired")
		}
	}
	lines = append(lines, "└────────────────────────────────────────")
	lines = append(lines, "")
	return lines
}

// formatToolApprovalArguments renders raw tool-call arguments (possibly
// double-encoded, per unwrapToolInput) as a pretty-printed, length-capped
// string suitable for the approval overlay.
func formatToolApprovalArguments(raw json.RawMessage) string {
	unwrapped := bytes.TrimSpace(unwrapToolInput(raw))
	if len(unwrapped) == 0 {
		return "(no arguments)"
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, unwrapped, "", "  "); err != nil {
		return string(unwrapped)
	}
	out := pretty.String()
	const maxLen = 800
	if len(out) > maxLen {
		out = out[:maxLen] + "\n…"
	}
	return out
}
