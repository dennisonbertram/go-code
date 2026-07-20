package tui_test

// steer_key_test.go — epic #820 slice 3
// ctrl+g steers the active run with the input-box content: one keypress POSTs
// the text to /v1/runs/{id}/steer (steerRunCmd, slice 1), clears the input,
// and leaves the run alive. Ungated presses (no run, empty input) are status
// hints, never errors, and never hit the network. SteerErrorMsg kinds map to
// human status text.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// steerKeyModel builds a model pointed at baseURL with a cancel func wired, an
// active run (runID, when non-empty), and text typed into the input box.
func steerKeyModel(t *testing.T, baseURL, runID, input string, cancel *bool) tui.Model {
	t.Helper()
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = baseURL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3 := m2.(tui.Model).WithCancelRun(func() {
		if cancel != nil {
			*cancel = true
		}
	})
	if runID != "" {
		m4, _ := m3.Update(tui.RunStartedMsg{RunID: runID})
		m3 = m4.(tui.Model)
	}
	if input != "" {
		m3 = typeIntoModel(m3, input)
	}
	return m3
}

func TestSteerKey_ActiveRunSendsInputClearsAndKeepsRunAlive(t *testing.T) {
	var gotMethod, gotPath, gotPrompt string
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode steer body: %v", err)
		}
		gotPrompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	cancelCalled := false
	model := steerKeyModel(t, srv.URL, "run-steer-key-1", "switch to approach B", &cancelCalled)

	m2, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)

	if cmd == nil {
		t.Fatal("expected a non-nil command on ctrl+g during an active run with input")
	}
	runCmd(cmd)

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("steer endpoint hits = %d, want 1", got)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/runs/run-steer-key-1/steer" {
		t.Errorf("path = %q, want /v1/runs/run-steer-key-1/steer", gotPath)
	}
	if gotPrompt != "switch to approach B" {
		t.Errorf("prompt = %q, want %q", gotPrompt, "switch to approach B")
	}

	if got := model.Input(); got != "" {
		t.Errorf("input should be cleared after steering, got %q", got)
	}
	if !model.RunActive() {
		t.Error("run must stay active after ctrl+g steer (it is not a cancel)")
	}
	if cancelCalled {
		t.Error("cancelRun must NOT be called on ctrl+g steer")
	}
	if got := model.StatusMsg(); !strings.Contains(got, "Steering sent") {
		t.Errorf("StatusMsg() = %q, want it to contain %q", got, "Steering sent")
	}
}

func TestSteerKey_NoActiveRunIsStatusHintOnly(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// No RunStartedMsg — idle model with text typed.
	model := steerKeyModel(t, srv.URL, "", "redirect please", nil)

	m2, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)
	runCmd(cmd)

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("steer endpoint hits = %d, want 0 with no active run", got)
	}
	if got := model.StatusMsg(); !strings.Contains(got, "No active run") {
		t.Errorf("StatusMsg() = %q, want a 'No active run' hint", got)
	}
	// Input is NOT cleared when nothing was sent — the user's text is kept.
	if got := model.Input(); got != "redirect please" {
		t.Errorf("input must be preserved when the steer is a no-op, got %q", got)
	}
}

func TestSteerKey_EmptyOrWhitespaceInputIsStatusHintOnly(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()

			model := steerKeyModel(t, srv.URL, "run-steer-key-empty", tc.input, nil)

			m2, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			model = m2.(tui.Model)
			runCmd(cmd)

			if got := atomic.LoadInt32(&hits); got != 0 {
				t.Errorf("steer endpoint hits = %d, want 0 with %s input", got, tc.name)
			}
			if got := model.StatusMsg(); !strings.Contains(got, "steer") {
				t.Errorf("StatusMsg() = %q, want a hint telling the user to type a message to steer", got)
			}
			if !model.RunActive() {
				t.Error("run must stay active on a no-op ctrl+g")
			}
		})
	}
}

func TestSteerErrorMsg_KindsMapToStatusText(t *testing.T) {
	cases := []struct {
		name string
		msg  tui.SteerErrorMsg
		want string
	}{
		{"run not active (409)", tui.SteerErrorMsg{RunID: "run-x", Kind: "run_not_active", Err: "HTTP 409"}, "run already finished"},
		{"buffer full (429)", tui.SteerErrorMsg{RunID: "run-x", Kind: "steering_buffer_full", Err: "HTTP 429"}, "steering buffer full"},
		{"not found (404)", tui.SteerErrorMsg{RunID: "run-x", Kind: "not_found", Err: "HTTP 404"}, "run not found"},
		{"transport", tui.SteerErrorMsg{RunID: "run-x", Kind: "transport", Err: "request failed: connection refused"}, "connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := steerKeyModel(t, "http://localhost:1", "run-x", "", nil)

			m2, _ := model.Update(tc.msg)
			model = m2.(tui.Model)

			if got := model.StatusMsg(); !strings.Contains(got, tc.want) {
				t.Errorf("StatusMsg() = %q, want it to contain %q", got, tc.want)
			}
			if !model.RunActive() {
				t.Error("a failed steer must not kill the run")
			}
		})
	}
}

func TestSteerAcceptedMsg_KeepsRunAlive(t *testing.T) {
	model := steerKeyModel(t, "http://localhost:1", "run-x", "", nil)

	m2, _ := model.Update(tui.SteerAcceptedMsg{RunID: "run-x"})
	model = m2.(tui.Model)

	if !model.RunActive() {
		t.Error("run must stay active after SteerAcceptedMsg")
	}
}
