package tui

// approval.go — tool.approval_required TUI overlay.
// Messages, types, and HTTP commands for the tool-approval TUI integration.
// Structurally mirrors askuser.go, but simpler: the pending call is fully
// described by the tool.approval_required SSE payload itself, so no extra
// fetch round-trip is needed before the overlay can be shown.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// approveToolCmd sends POST /v1/runs/{id}/approve. When a plan approach
// option ID is given it is sent as {"option": id} in the request body (a
// plan-exit approve with the operator's selected approach); otherwise the
// body is empty (a plain approve). On success it returns
// ToolApprovalDecidedMsg{Decision: "approved"}; on failure it returns
// ToolApprovalErrorMsg.
func approveToolCmd(baseURL, runID, apiKey string, option ...string) tea.Cmd {
	opt := ""
	if len(option) > 0 {
		opt = option[0]
	}
	return toolApprovalDecisionCmd(baseURL, runID, "approve", "approved", apiKey, opt)
}

// denyToolCmd sends POST /v1/runs/{id}/deny. On success it returns
// ToolApprovalDecidedMsg{Decision: "denied"}; on failure it returns
// ToolApprovalErrorMsg.
func denyToolCmd(baseURL, runID, apiKey string) tea.Cmd {
	return toolApprovalDecisionCmd(baseURL, runID, "deny", "denied", apiKey, "")
}

// toolApprovalDecisionCmd posts to /v1/runs/{id}/{path} — with a
// {"option": option} body when option is non-empty, an empty body otherwise —
// and translates the response into ToolApprovalDecidedMsg or
// ToolApprovalErrorMsg.
func toolApprovalDecisionCmd(baseURL, runID, path, decision, apiKey, option string) tea.Cmd {
	return func() tea.Msg {
		postURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/" + path
		var body *bytes.Reader
		if option != "" {
			raw, err := json.Marshal(map[string]string{"option": option})
			if err != nil {
				return ToolApprovalErrorMsg{Err: fmt.Sprintf("%s tool call: %s", path, err.Error())}
			}
			body = bytes.NewReader(raw)
		} else {
			body = bytes.NewReader(nil)
		}
		req, err := newHarnessRequest(context.Background(), http.MethodPost, postURL, body, apiKey)
		if err != nil {
			return ToolApprovalErrorMsg{Err: fmt.Sprintf("%s tool call: %s", path, err.Error())}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClientWithTimeout.Do(req)
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
		return toolApprovalState{}, approveToolCmd(m.config.BaseURL, runID, m.config.APIKey)
	case tea.KeyEsc:
		return toolApprovalState{}, cancelRunCmd(m.config.BaseURL, runID, m.config.APIKey)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "a", "A", "y", "Y":
			return toolApprovalState{}, approveToolCmd(m.config.BaseURL, runID, m.config.APIKey)
		case "d", "D", "n", "N":
			return toolApprovalState{}, denyToolCmd(m.config.BaseURL, runID, m.config.APIKey)
		}
	}
	return st, nil
}

// renderToolApprovalOverlay renders the tool-approval overlay as a list of
// lines. Returns nil if no overlay is active. The overlay chrome renders from
// the active theme (epic #810): box in the border-token color, tool name in
// primary, action line in warning.
func (m Model) renderToolApprovalOverlay() []string {
	if !m.toolApproval.active {
		return nil
	}
	chrome := lipgloss.NewStyle()
	if fg := m.theme.BorderStyle.GetBorderTopForeground(); fg != nil {
		if _, none := fg.(lipgloss.NoColor); !none {
			chrome = chrome.Foreground(fg)
		}
	}
	rule := func(s string) string { return chrome.Render(s) }
	lines := []string{
		"",
		rule("┌─ Tool Approval Required ─────────────────────────"),
		rule("│") + "  Tool: " + m.theme.ToolNameStyle.Render(m.toolApproval.tool),
		rule("│"),
	}
	for _, line := range strings.Split(m.toolApproval.arguments, "\n") {
		lines = append(lines, rule("│")+"  "+line)
	}
	lines = append(lines, rule("│"))
	lines = append(lines, rule("│")+"  "+m.theme.WarningStyle.Render("[a]pprove  [d]eny")+rule("  (esc cancels the run)"))
	if !m.toolApproval.deadlineAt.IsZero() {
		remaining := time.Until(m.toolApproval.deadlineAt)
		if remaining > 0 {
			lines = append(lines, rule("│")+fmt.Sprintf("  Deadline: %s remaining", remaining.Round(time.Second)))
		} else {
			lines = append(lines, rule("│")+"  Deadline: expired")
		}
	}
	lines = append(lines, rule("└────────────────────────────────────────"))
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
