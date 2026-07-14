package tui

// askuser.go — TUI #476
// Messages, types, and HTTP commands for AskUserQuestion TUI integration.

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
)

// httpClientWithTimeout is the shared HTTP client used for ask-user calls.
// A 10-second timeout prevents hanging indefinitely on slow or unreachable servers.
var httpClientWithTimeout = &http.Client{Timeout: 10 * time.Second}

// ─── Ask User Types ──────────────────────────────────────────────────────────

// AskUserOption is a single selectable option in an AskUserQuestion.
type AskUserOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AskUserQuestion is a single question presented to the user.
type AskUserQuestion struct {
	Question    string          `json:"question"`
	Header      string          `json:"header"`
	Options     []AskUserOption `json:"options"`
	MultiSelect bool            `json:"multiSelect"`
}

// ─── Ask User Messages ───────────────────────────────────────────────────────

// AskUserPendingMsg is sent to the model when pending questions have been
// fetched from GET /v1/runs/{id}/input and are ready to display.
type AskUserPendingMsg struct {
	RunID      string
	CallID     string
	Questions  []AskUserQuestion
	DeadlineAt time.Time
}

// AskUserSubmittedMsg is sent when the POST /v1/runs/{id}/input succeeds.
type AskUserSubmittedMsg struct {
	RunID string
}

// AskUserSubmitErrorMsg is sent when the POST /v1/runs/{id}/input fails.
type AskUserSubmitErrorMsg struct {
	Err string
}

// AskUserTimeoutMsg is sent when the question deadline has passed.
// CallID scopes the timeout to a specific question invocation so that a stale
// timer from an earlier call cannot dismiss a newer question overlay.
type AskUserTimeoutMsg struct {
	RunID  string
	CallID string
}

// askUserFetchErrorMsg is sent when GET /v1/runs/{id}/input fails.
// This is unexported — it is handled inside the model to set a status message.
type askUserFetchErrorMsg struct {
	err string
}

// ─── Ask User State (stored on Model) ────────────────────────────────────────

// askUserState holds all state for the in-progress AskUserQuestion interaction.
// Zero value = no active question.
type askUserState struct {
	active     bool
	runID      string
	callID     string
	questions  []AskUserQuestion
	deadlineAt time.Time
	// qIdx is the index of the question currently displayed (for multi-question sets).
	qIdx int
	// selectedIdx is the cursor position in the current question's option list.
	selectedIdx int
}

// ─── HTTP Commands ───────────────────────────────────────────────────────────

// fetchAskUserPendingCmd fetches the pending AskUserQuestion for the given runID
// via GET /v1/runs/{id}/input and returns an AskUserPendingMsg or askUserFetchErrorMsg.
func fetchAskUserPendingCmd(baseURL, runID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		fetchURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/input"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, fetchURL, nil, apiKey)
		if err != nil {
			return askUserFetchErrorMsg{err: fmt.Sprintf("fetch pending input: %s", err.Error())}
		}
		resp, err := httpClientWithTimeout.Do(req)
		if err != nil {
			return askUserFetchErrorMsg{err: fmt.Sprintf("fetch pending input: %s", err.Error())}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return askUserFetchErrorMsg{err: fmt.Sprintf("fetch pending input: HTTP %d", resp.StatusCode)}
		}

		// Parse the AskUserQuestionPending payload from the server.
		var payload struct {
			RunID      string            `json:"run_id"`
			CallID     string            `json:"call_id"`
			Questions  []AskUserQuestion `json:"questions"`
			DeadlineAt time.Time         `json:"deadline_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return askUserFetchErrorMsg{err: fmt.Sprintf("decode pending input: %s", err.Error())}
		}
		return AskUserPendingMsg{
			RunID:      payload.RunID,
			CallID:     payload.CallID,
			Questions:  payload.Questions,
			DeadlineAt: payload.DeadlineAt,
		}
	}
}

// submitAskUserAnswerCmd sends POST /v1/runs/{id}/input with the given answers.
// On success it returns AskUserSubmittedMsg; on failure it returns AskUserSubmitErrorMsg.
func submitAskUserAnswerCmd(baseURL, runID string, answers map[string]string, apiKey string) tea.Cmd {
	return func() tea.Msg {
		postURL := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/input"
		payload := map[string]interface{}{"answers": answers}
		body, _ := json.Marshal(payload)
		req, err := newHarnessRequest(context.Background(), http.MethodPost, postURL, bytes.NewReader(body), apiKey)
		if err != nil {
			return AskUserSubmitErrorMsg{Err: fmt.Sprintf("submit answer: %s", err.Error())}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClientWithTimeout.Do(req)
		if err != nil {
			return AskUserSubmitErrorMsg{Err: fmt.Sprintf("submit answer: %s", err.Error())}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return AskUserSubmitErrorMsg{Err: fmt.Sprintf("submit answer: HTTP %d", resp.StatusCode)}
		}
		return AskUserSubmittedMsg{RunID: runID}
	}
}

// askUserDeadlineCmd returns a tea.Cmd that fires AskUserTimeoutMsg after the
// deadline. If the deadline is already past, it fires immediately.
// callID is embedded in the message so the model can ignore stale timers.
func askUserDeadlineCmd(runID, callID string, deadline time.Time) tea.Cmd {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = 0
	}
	return tea.Tick(remaining, func(time.Time) tea.Msg {
		return AskUserTimeoutMsg{RunID: runID, CallID: callID}
	})
}

// handleAskUserKey processes a key message when the ask-user overlay is active.
// It returns the new askUserState and an optional tea.Cmd using value semantics
// (no mutation of the receiver).
//
// Key routing:
//   - Up/Down: navigate option list for current question
//   - Enter: confirm selection, submit answer
//   - Esc: dismiss overlay without answering (timeout will fire anyway)
func (m Model) handleAskUserKey(msg tea.KeyMsg) (askUserState, tea.Cmd) {
	st := m.askUser
	if !st.active || len(st.questions) == 0 {
		return st, nil
	}

	q := st.questions[st.qIdx]

	switch msg.Type {
	case tea.KeyUp:
		if st.selectedIdx > 0 {
			st.selectedIdx--
		}
	case tea.KeyDown:
		if st.selectedIdx < len(q.Options)-1 {
			st.selectedIdx++
		}
	case tea.KeyEnter:
		if len(q.Options) == 0 {
			return st, nil
		}
		selectedLabel := q.Options[st.selectedIdx].Label
		// Build answers map for the current question.
		answers := map[string]string{
			q.Question: selectedLabel,
		}
		// Dismiss overlay immediately.
		runID := st.runID
		return askUserState{}, submitAskUserAnswerCmd(m.config.BaseURL, runID, answers, m.config.APIKey)
	case tea.KeyEsc:
		// Dismiss without answering — the server will timeout eventually.
		return askUserState{}, nil
	}
	return st, nil
}

// renderAskUserOverlay renders the ask-user question overlay as a list of lines.
// Returns nil if no overlay is active.
func (m Model) renderAskUserOverlay() []string {
	if !m.askUser.active || len(m.askUser.questions) == 0 {
		return nil
	}
	q := m.askUser.questions[m.askUser.qIdx]
	lines := []string{
		"",
		"┌─ " + q.Header + " ─────────────────────────────────",
		"│  " + q.Question,
		"│",
	}
	// Multi-select is not yet supported by the TUI; show a visible warning.
	if q.MultiSelect {
		lines = append(lines, "│  ⚠ [multi-select not supported, selecting one]")
	}
	for i, opt := range q.Options {
		cursor := "  "
		if i == m.askUser.selectedIdx {
			cursor = "▶ "
		}
		lines = append(lines, "│ "+cursor+opt.Label+" — "+opt.Description)
	}
	lines = append(lines, "│")
	lines = append(lines, "│  [↑↓] navigate  [enter] confirm  [esc] dismiss")
	if !m.askUser.deadlineAt.IsZero() {
		remaining := time.Until(m.askUser.deadlineAt)
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
