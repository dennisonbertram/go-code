package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDashboardRunListCmdLoadsRunsAndModelStoresThem(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"runs":[{"id":"run-1","status":"running","model":"gpt","prompt":"work"}]}`))
	}))
	defer ts.Close()

	msg := loadDashboardRunsCmd(ts.URL, "test-key")()
	m := New(TUIConfig{BaseURL: ts.URL})
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if len(m.dashboard.runs) != 1 || m.dashboard.runs[0].displayID() != "run-1" {
		t.Fatalf("dashboard runs = %#v", m.dashboard.runs)
	}
}

func TestDashboardViewGroupsRunsAndArrowKeysNavigate(t *testing.T) {
	m := New(TUIConfig{})
	m.overlayActive, m.activeOverlay = true, "dashboard"
	m.dashboard.runs = []tuiRunRecord{{ID: "one", Status: "running"}, {ID: "two", Status: "waiting_for_approval"}, {ID: "three", Status: "completed"}}
	if got := m.dashboardView(); !strings.Contains(got, "Running") || !strings.Contains(got, "Waiting") || !strings.Contains(got, "Completed") {
		t.Fatalf("missing groups: %s", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := updated.(Model).dashboard.cursor; got != 1 {
		t.Fatalf("cursor = %d", got)
	}
}

func TestDashboardCommandAndKeybindingOpenOverlay(t *testing.T) {
	m := New(TUIConfig{})
	if _, ok := m.commandRegistry.Lookup("dashboard"); !ok {
		t.Fatal("/dashboard must be registered")
	}
	executeDashboardCommand(&m, Command{})
	if !m.overlayActive || m.activeOverlay != "dashboard" {
		t.Fatalf("overlay = %q", m.activeOverlay)
	}
	m.closeDashboard()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if m.activeOverlay != "dashboard" {
		t.Fatalf("keybinding overlay = %q", m.activeOverlay)
	}
}

func TestDashboardPeekStartsSingleBridgeAndClosesBeforeOverlay(t *testing.T) {
	m := New(TUIConfig{BaseURL: "http://example.invalid"})
	m.overlayActive, m.activeOverlay = true, "dashboard"
	m.dashboard.runs = []tuiRunRecord{{ID: "run-1", Status: "running"}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(Model)
	if m.dashboard.peekID != "run-1" || m.dashboard.stopPeek == nil {
		t.Fatalf("peek not started: %#v", m.dashboard)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.dashboard.peekID != "" || m.activeOverlay != "dashboard" {
		t.Fatal("escape must close peek first")
	}
}
