package tui_test

// steer_echo_test.go — epic #820 slice 4
// A TUI-originated steer echoes in the transcript/viewport immediately on send
// (not only at the next step boundary), and the later server-confirmed
// steering.received event dedupes against the echo instead of double-rendering.
// A failed steer (409/429/…) removes the echo — no orphan entry claiming a
// steer the server rejected.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// driveCmdsFolding executes a tea.Cmd tree (tea.Batch aware) and folds every
// resulting non-batch message back through the model's Update — the same loop
// the BubbleTea runtime performs, so async results like SteerErrorMsg reach
// their handler.
func driveCmdsFolding(t *testing.T, model tui.Model, cmd tea.Cmd) tui.Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg := cmd()
	if msg == nil {
		return model
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			model = driveCmdsFolding(t, model, sub)
		}
		return model
	}
	m2, _ := model.Update(msg)
	return m2.(tui.Model)
}

// steerEchoModel builds a model with an active run and text typed in the input.
func steerEchoModel(t *testing.T, baseURL, runID, input string) tui.Model {
	t.Helper()
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = baseURL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m3 := m2.(tui.Model).WithCancelRun(func() {})
	if runID != "" {
		m4, _ := m3.Update(tui.RunStartedMsg{RunID: runID})
		m3 = m4.(tui.Model)
	}
	if input != "" {
		m3 = typeIntoModel(m3, input)
	}
	return m3
}

// countMarkerLines counts rendered viewport lines that carry both the steering
// marker and the given message text.
func countMarkerLines(view, text string) int {
	n := 0
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, text) && strings.Contains(line, "steered") {
			n++
		}
	}
	return n
}

// countTranscriptEntries counts transcript entries whose content carries text.
func countTranscriptEntries(model tui.Model, text string) int {
	n := 0
	for _, e := range model.Transcript() {
		if strings.Contains(e.Content, text) {
			n++
		}
	}
	return n
}

func TestSteerEcho_AppearsImmediatelyOnSend(t *testing.T) {
	model := steerEchoModel(t, "http://localhost:1", "run-echo-1", "switch to approach B")

	transcriptBefore := len(model.Transcript())

	m2, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)
	if cmd == nil {
		t.Fatal("expected a non-nil command on ctrl+g during an active run with input")
	}
	// NOTE: cmd deliberately NOT executed — the echo must already be visible
	// synchronously, before any HTTP round-trip or SSE event.

	view := model.View()
	if got := countMarkerLines(view, "switch to approach B"); got != 1 {
		t.Fatalf("expected exactly one local-echo marker line before any SSE event, got %d; view=\n%s", got, view)
	}

	entries := model.Transcript()
	if len(entries) != transcriptBefore+1 {
		t.Fatalf("transcript should gain exactly one echo entry, before=%d after=%d", transcriptBefore, len(entries))
	}
	last := entries[len(entries)-1]
	if last.Role != "user" {
		t.Errorf("echo entry role = %q, want user", last.Role)
	}
	if !strings.Contains(last.Content, "switch to approach B") || !strings.Contains(last.Content, "steered") {
		t.Errorf("echo entry missing marker/text: %+v", last)
	}
	if !strings.Contains(last.Content, "pending") {
		t.Errorf("echo entry should be marked pending until server confirmation: %+v", last)
	}

	if !model.RunActive() {
		t.Error("run must stay active after sending a steer")
	}
}

func TestSteerEcho_ServerConfirmationDedupes(t *testing.T) {
	model := steerEchoModel(t, "http://localhost:1", "run-echo-2", "switch to approach B")

	m2, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)

	// Server confirms the injection at the next step boundary.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"switch to approach B"}`),
	})
	model = m3.(tui.Model)

	view := model.View()
	if got := countMarkerLines(view, "switch to approach B"); got != 1 {
		t.Errorf("steering.received matching the local echo must NOT append a duplicate marker; found %d; view=\n%s", got, view)
	}
	if got := countTranscriptEntries(model, "switch to approach B"); got != 1 {
		t.Errorf("transcript must show the steered message exactly once, found %d entries", got)
	}
	for _, e := range model.Transcript() {
		if strings.Contains(e.Content, "switch to approach B") && strings.Contains(e.Content, "pending") {
			t.Errorf("confirmed echo entry must no longer be marked pending: %+v", e)
		}
	}
}

func TestSteerEcho_ExternalSteerAppendsSecondMarker(t *testing.T) {
	model := steerEchoModel(t, "http://localhost:1", "run-echo-3", "local steer")

	m2, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)

	// Confirm the local steer, then an external steer (e.g. webhook) arrives.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"local steer"}`),
	})
	model = m3.(tui.Model)
	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"external steer"}`),
	})
	model = m4.(tui.Model)

	view := model.View()
	if got := countMarkerLines(view, "local steer"); got != 1 {
		t.Errorf("local steer should render exactly once, found %d", got)
	}
	if got := countMarkerLines(view, "external steer"); got != 1 {
		t.Errorf("external steer (no local echo) must append its own marker, found %d; view=\n%s", got, view)
	}
	if got := countTranscriptEntries(model, "steer"); got != 2 {
		t.Errorf("transcript must hold exactly one entry per origin, found %d steer entries", got)
	}
}

func TestSteerEcho_ConsumedDedupeTreatsSecondIdenticalAsExternal(t *testing.T) {
	model := steerEchoModel(t, "http://localhost:1", "run-echo-4", "repeat instruction")

	m2, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = m2.(tui.Model)

	// First confirmation consumes the pending echo.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"repeat instruction"}`),
	})
	model = m3.(tui.Model)
	// A second identical payload can only be a NEW steer (e.g. the user steered
	// the same text from another client) — it must render as its own marker.
	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"repeat instruction"}`),
	})
	model = m4.(tui.Model)

	if got := countMarkerLines(model.View(), "repeat instruction"); got != 2 {
		t.Errorf("a second identical steering.received after confirmation must append a second marker, found %d", got)
	}
}

func TestSteerEcho_FailureRemovesEchoAndEntry(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantStat string
	}{
		{"run not active (409)", http.StatusConflict, "run already finished"},
		{"buffer full (429)", http.StatusTooManyRequests, "steering buffer full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"x","message":"nope"}}`))
			}))
			defer srv.Close()

			model := steerEchoModel(t, srv.URL, "run-echo-fail", "doomed steer")

			m2, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			model = m2.(tui.Model)

			// Precondition: the echo rendered optimistically.
			if got := countMarkerLines(model.View(), "doomed steer"); got != 1 {
				t.Fatalf("precondition: echo should be visible after send, found %d", got)
			}
			if got := countTranscriptEntries(model, "doomed steer"); got != 1 {
				t.Fatalf("precondition: echo entry should exist after send, found %d", got)
			}

			// Drive the send end-to-end: HTTP → SteerErrorMsg → Update.
			model = driveCmdsFolding(t, model, cmd)

			if got := countTranscriptEntries(model, "doomed steer"); got != 0 {
				t.Errorf("failed steer must not leave an orphan transcript entry, found %d", got)
			}
			if got := countMarkerLines(model.View(), "doomed steer"); got != 0 {
				t.Errorf("failed steer echo should be removed from the viewport (nothing appended after it), found %d; view=\n%s", got, model.View())
			}
			if got := model.StatusMsg(); !strings.Contains(got, tc.wantStat) {
				t.Errorf("StatusMsg() = %q, want it to contain %q", got, tc.wantStat)
			}
			if !model.RunActive() {
				t.Error("a failed steer must not kill the run")
			}
		})
	}
}
