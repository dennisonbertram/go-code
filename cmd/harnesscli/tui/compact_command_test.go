package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Tests in this file cover epic #817 slice 3: the /compact [instruction]
// slash command against the active run.
// Helpers testRunControlModel and lastCmd live in run_control_command_test.go.

// TestCompactCommand_NoActiveRunShowsUsage verifies /compact without an
// active run shows a usage/status error and issues no HTTP request.
func TestCompactCommand_NoActiveRunShowsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected without an active run, got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	// No active run: m.runActive == false, m.RunID == "".
	cmds, quit := executeCompactCommand(&m, Command{Name: "compact", Args: []string{"keep", "x"}})
	if quit {
		t.Fatal("/compact must not quit")
	}
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 status cmd, got %d", len(cmds))
	}
	// setStatusMsg mutates the model directly; the returned cmd is only the
	// auto-dismiss tick.
	status := m.StatusMsg()
	if !strings.Contains(status, "Usage: /compact") {
		t.Fatalf("StatusMsg() = %q, want usage hint", status)
	}
	if !strings.Contains(status, "active run") {
		t.Fatalf("StatusMsg() = %q, want active-run requirement", status)
	}
}

// TestCompactCommand_JoinsInstructionAndPostsHybrid verifies /compact with an
// active run POSTs {"mode":"hybrid","instruction":<args joined verbatim>} to
// /v1/runs/{id}/compact and reports messages removed on success.
func TestCompactCommand_JoinsInstructionAndPostsHybrid(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/run_active_1/compact" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages_removed":3,"mode":"hybrid","summary":"kept the essentials"}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	m.runActive = true
	m.RunID = "run_active_1"

	cmds, quit := executeCompactCommand(&m, Command{
		Name: "compact",
		Args: []string{"keep", "the", "failing", "test", "output"},
	})
	if quit {
		t.Fatal("/compact must not quit")
	}
	msg := lastCmd(t, cmds)()

	if payload["mode"] != "hybrid" {
		t.Fatalf("payload mode = %v, want hybrid", payload["mode"])
	}
	if payload["instruction"] != "keep the failing test output" {
		t.Fatalf("payload instruction = %v, want args joined verbatim", payload["instruction"])
	}

	result, ok := msg.(CompactResultMsg)
	if !ok {
		t.Fatalf("expected CompactResultMsg, got %T: %+v", msg, msg)
	}
	if result.Err != "" {
		t.Fatalf("unexpected error: %s", result.Err)
	}
	if result.MessagesRemoved != 3 {
		t.Fatalf("MessagesRemoved = %d, want 3", result.MessagesRemoved)
	}
	if result.Mode != "hybrid" || result.Summary != "kept the essentials" {
		t.Fatalf("Mode/Summary = %q/%q, want hybrid/kept the essentials", result.Mode, result.Summary)
	}

	m2, _ := m.Update(msg)
	m = m2.(Model)
	if !strings.Contains(m.StatusMsg(), "3 messages removed") {
		t.Fatalf("StatusMsg() = %q, want messages-removed report", m.StatusMsg())
	}
}

// TestCompactCommand_NoArgsSendsEmptyInstruction verifies /compact with no
// arguments still compacts (mode hybrid, empty instruction).
func TestCompactCommand_NoArgsSendsEmptyInstruction(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages_removed":1,"mode":"hybrid","summary":""}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	m.runActive = true
	m.RunID = "run_active_1"

	cmds, quit := executeCompactCommand(&m, Command{Name: "compact"})
	if quit {
		t.Fatal("/compact must not quit")
	}
	msg := lastCmd(t, cmds)()
	if result, ok := msg.(CompactResultMsg); !ok || result.Err != "" {
		t.Fatalf("expected successful CompactResultMsg, got %+v", msg)
	}

	if payload["mode"] != "hybrid" {
		t.Fatalf("payload mode = %v, want hybrid", payload["mode"])
	}
	if payload["instruction"] != "" {
		t.Fatalf("payload instruction = %v, want empty for bare /compact", payload["instruction"])
	}
}

// TestCompactCommand_ServerErrorShowsFailureStatus verifies a server-side
// failure (e.g. 409 run_not_active) surfaces as a failed status line.
func TestCompactCommand_ServerErrorShowsFailureStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"run_not_active","message":"run is not active"}}`))
	}))
	defer srv.Close()

	m := testRunControlModel(srv.URL)
	m.runActive = true
	m.RunID = "run_active_1"

	cmds, quit := executeCompactCommand(&m, Command{Name: "compact"})
	if quit {
		t.Fatal("/compact must not quit")
	}
	msg := lastCmd(t, cmds)()
	result, ok := msg.(CompactResultMsg)
	if !ok {
		t.Fatalf("expected CompactResultMsg, got %T", msg)
	}
	if result.Err == "" {
		t.Fatal("expected Err on 409 response")
	}

	m2, _ := m.Update(msg)
	m = m2.(Model)
	if !strings.Contains(m.StatusMsg(), "compact failed") {
		t.Fatalf("StatusMsg() = %q, want compact failed", m.StatusMsg())
	}
}

// TestCompactCommand_AppearsInRegistryHelpAndSlashComplete verifies the
// command is registered with a description and shows up in /help and slash
// completion (both are registry-driven).
func TestCompactCommand_AppearsInRegistryHelpAndSlashComplete(t *testing.T) {
	reg := NewCommandRegistry()
	entry, ok := reg.Lookup("compact")
	if !ok {
		t.Fatal("compact command not registered")
	}
	if entry.Description == "" {
		t.Error("compact command has empty description")
	}
	if entry.Execute == nil {
		t.Error("compact command has nil Execute")
	}

	// /help view lists the command.
	m := testRunControlModel("http://localhost:8080")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = m2.(Model)
	helpCmds, _ := executeHelpCommand(&m, Command{Name: "help"})
	if len(helpCmds) != 0 {
		for _, c := range helpCmds {
			if c == nil {
				continue
			}
			if msg := c(); msg != nil {
				m3, _ := m.Update(msg)
				m = m3.(Model)
			}
		}
	}
	if v := m.View(); !strings.Contains(v, "compact") {
		t.Errorf("/compact must appear in /help view:\n%s", v)
	}

	// Slash completion suggests /compact.
	sc := buildSlashComplete(NewCommandRegistry(), nil).Open().SetQuery("comp")
	found := false
	for _, s := range sc.Filtered() {
		if s.Name == "compact" {
			found = true
		}
	}
	if !found {
		t.Error("slash completion for \"comp\" must suggest compact")
	}
}
