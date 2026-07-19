package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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
