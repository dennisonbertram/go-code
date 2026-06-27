package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testRunControlModel(baseURL string) Model {
	cfg := DefaultTUIConfig()
	cfg.BaseURL = baseURL
	m := New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(Model)
}

func lastCmd(t *testing.T, cmds []tea.Cmd) tea.Cmd {
	t.Helper()
	if len(cmds) == 0 {
		t.Fatal("expected command")
	}
	cmd := cmds[len(cmds)-1]
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	return cmd
}

func TestRunControl_RunsCommandFetchesAndDisplaysRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs": []map[string]any{{
				"id":     "run_daily_1",
				"status": "completed",
				"model":  "gpt-4.1",
				"prompt": "fix terminal workflow",
			}},
		})
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	cmds, quit := executeRunsCommand(&m, Command{Name: "runs"})
	if quit {
		t.Fatal("/runs must not quit")
	}
	msg := lastCmd(t, cmds)()
	m2, _ := m.Update(msg)
	m = m2.(Model)

	view := m.View()
	for _, want := range []string{"Runs", "run_daily_1", "completed", "gpt-4.1", "fix terminal"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/runs view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(m.StatusMsg(), "Loaded 1 run") {
		t.Fatalf("StatusMsg() = %q, want loaded run count", m.StatusMsg())
	}
}

func TestRunControl_CancelCommandCallsCancelEndpoint(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/run_cancel_1/cancel" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"cancelling"}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	cmds, quit := executeCancelCommand(&m, Command{Name: "cancel", Args: []string{"run_cancel_1"}})
	if quit {
		t.Fatal("/cancel must not quit")
	}
	msg := lastCmd(t, cmds)()
	m2, _ := m.Update(msg)
	m = m2.(Model)

	if !called {
		t.Fatal("cancel endpoint was not called")
	}
	if !strings.Contains(m.StatusMsg(), "Run run_cancel_1 cancelling") {
		t.Fatalf("StatusMsg() = %q, want cancelling status", m.StatusMsg())
	}
}

func TestRunControl_ReplayCommandCallsReplayEndpoint(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/replay" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"simulate","events_replayed":3,"matched":true}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	cmds, quit := executeReplayCommand(&m, Command{Name: "replay", Args: []string{"run_replay_1"}})
	if quit {
		t.Fatal("/replay must not quit")
	}
	msg := lastCmd(t, cmds)()
	m2, _ := m.Update(msg)
	m = m2.(Model)

	if payload["rollout_path"] != "run_replay_1" || payload["mode"] != "simulate" {
		t.Fatalf("unexpected replay payload: %#v", payload)
	}
	view := m.View()
	for _, want := range []string{"Replay result", "events_replayed", "3", "matched"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/replay view missing %q:\n%s", want, view)
		}
	}
}

func TestRunControl_ResumeCommandStartsContinuationRun(t *testing.T) {
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/run_prev/continue" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		prompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"run_next","status":"queued"}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	m = m.WithCancelRun(func() {})
	cmds, quit := executeResumeCommand(&m, Command{Name: "resume", Args: []string{"run_prev", "keep", "going"}})
	if quit {
		t.Fatal("/resume must not quit")
	}
	msg := lastCmd(t, cmds)()
	m2, _ := m.Update(msg)
	m = m2.(Model)

	if prompt != "keep going" {
		t.Fatalf("continuation prompt = %q, want %q", prompt, "keep going")
	}
	if m.RunID != "run_next" {
		t.Fatalf("RunID = %q, want run_next", m.RunID)
	}
	if !m.RunActive() {
		t.Fatal("continuation run should be active")
	}
}

func TestRunControl_RunsSnapshot80x24(t *testing.T) {
	writeRunsSnapshot(t, 80, 24, "TUI-058-runs-80x24.txt")
}

func TestRunControl_RunsSnapshot120x40(t *testing.T) {
	writeRunsSnapshot(t, 120, 40, "TUI-058-runs-120x40.txt")
}

func TestRunControl_RunsSnapshot200x50(t *testing.T) {
	writeRunsSnapshot(t, 200, 50, "TUI-058-runs-200x50.txt")
}

func writeRunsSnapshot(t *testing.T, width, height int, name string) {
	t.Helper()
	m := testRunControlModel("http://localhost:8080")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = m2.(Model)
	m3, _ := m.Update(RunsFetchedMsg{Runs: []tuiRunRecord{
		{ID: "run_daily_1", Status: "completed", Model: "gpt-4.1", Prompt: "fix terminal workflow and replay search"},
		{ID: "run_daily_2", Status: "running", Model: "claude-sonnet-4", Prompt: "continue trusted harness loop"},
	}})
	m = m3.(Model)

	output := m.View()
	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	if !strings.Contains(output, "run_daily_1") || !strings.Contains(output, "run_daily_2") {
		t.Fatalf("snapshot must contain both run IDs, got:\n%s", output)
	}
}
