package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func dashboardGroup(status string) string {
	switch status {
	case "running", "queued":
		return "Running"
	case "waiting_for_user", "waiting_for_approval":
		return "Waiting"
	case "completed":
		return "Completed"
	case "failed":
		return "Failed"
	case "cancelled", "cancelling":
		return "Cancelled"
	default:
		return "Other"
	}
}

func (m Model) dashboardView() string {
	groups := map[string][]int{}
	for i, r := range m.dashboard.runs {
		groups[dashboardGroup(r.Status)] = append(groups[dashboardGroup(r.Status)], i)
	}
	order := []string{"Running", "Waiting", "Completed", "Failed", "Cancelled", "Other"}
	lines := []string{"Dashboard — all runs", "↑/↓ navigate • p peek • s steer • x cancel • n new • esc close"}
	for _, group := range order {
		idxs := groups[group]
		if len(idxs) == 0 {
			continue
		}
		sort.Ints(idxs)
		lines = append(lines, "", group)
		for _, i := range idxs {
			r := m.dashboard.runs[i]
			marker := "  "
			if i == m.dashboard.cursor {
				marker = "> "
			}
			model := r.Model
			if model == "" {
				model = "(default)"
			}
			lines = append(lines, fmt.Sprintf("%s%s  %-22s  %s", marker, r.displayID(), r.Status, model))
		}
	}
	if len(m.dashboard.runs) == 0 {
		lines = append(lines, "", "No runs found.")
	}
	if m.dashboard.peekID != "" {
		lines = append(lines, "", "Peek: "+m.dashboard.peekID)
		lines = append(lines, m.dashboard.peek...)
	}
	return strings.Join(lines, "\n")
}

// dashboardState is deliberately owned by the existing Model overlay; its poll
// command is scheduled only while the dashboard is active.
type dashboardState struct {
	runs      []tuiRunRecord
	cursor    int
	peekID    string
	peek      []string
	peekCh    <-chan tea.Msg
	stopPeek  func()
	inputMode string // "steer" or "new"
	input     string
}

type DashboardRunsLoadedMsg struct {
	Runs []tuiRunRecord
	Err  string
}
type dashboardPollTickMsg struct{}

func loadDashboardRunsCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		req, err := newHarnessRequest(context.Background(), http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/runs", nil, apiKey)
		if err != nil {
			return DashboardRunsLoadedMsg{Err: err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return DashboardRunsLoadedMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return DashboardRunsLoadedMsg{Err: fmt.Sprintf("server returned %d", resp.StatusCode)}
		}
		var payload struct {
			Runs []tuiRunRecord `json:"runs"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return DashboardRunsLoadedMsg{Err: err.Error()}
		}
		return DashboardRunsLoadedMsg{Runs: payload.Runs}
	}
}

func dashboardPollCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return dashboardPollTickMsg{} })
}

func (m *Model) dashboardOpenCmds() []tea.Cmd {
	m.overlayActive, m.activeOverlay = true, "dashboard"
	return []tea.Cmd{loadDashboardRunsCmd(m.config.BaseURL, m.config.APIKey), dashboardPollCmd()}
}

func (m *Model) closeDashboard() {
	if m.dashboard.stopPeek != nil {
		m.dashboard.stopPeek()
		m.dashboard.stopPeek = nil
	}
	m.dashboard.peekCh, m.dashboard.peekID, m.dashboard.peek = nil, "", nil
	m.dashboard.inputMode, m.dashboard.input = "", ""
	m.overlayActive, m.activeOverlay = false, ""
}

func (m *Model) dashboardSelected() (tuiRunRecord, bool) {
	if len(m.dashboard.runs) == 0 {
		return tuiRunRecord{}, false
	}
	if m.dashboard.cursor >= len(m.dashboard.runs) {
		m.dashboard.cursor = len(m.dashboard.runs) - 1
	}
	if m.dashboard.cursor < 0 {
		m.dashboard.cursor = 0
	}
	return m.dashboard.runs[m.dashboard.cursor], true
}
