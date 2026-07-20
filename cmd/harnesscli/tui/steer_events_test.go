package tui_test

// steer_events_test.go — epic #820 slice 2
// The TUI must not drop steering.received SSE events: a server-confirmed
// steering injection (payload {"message": "..."}) is rendered as a user bubble
// carrying a "steered ⟂" marker — visually distinct from a typed prompt — and
// recorded in the transcript (role "user") so exports include it. Malformed or
// empty payloads are ignored without panic.

import (
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

func TestSteeringReceived_RendersMarkerBubbleAndTranscript(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-steer-1"})
	model := m2.(tui.Model)

	transcriptBefore := len(model.Transcript())

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"focus on X"}`),
		ID:        "run-steer-1:7",
	})
	model = m3.(tui.Model)

	view := model.View()
	if !strings.Contains(view, "focus on X") {
		t.Fatalf("expected steered message in viewport; view=\n%s", view)
	}
	if !strings.Contains(view, "steered") {
		t.Fatalf("expected steering marker in viewport; view=\n%s", view)
	}
	// The marker must sit on the same rendered line as the steered message so
	// the bubble reads as steered input, not as a typed prompt.
	markerOnMessageLine := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "focus on X") && strings.Contains(line, "steered") {
			markerOnMessageLine = true
		}
	}
	if !markerOnMessageLine {
		t.Errorf("no viewport line carries both the marker and the steered message; view=\n%s", view)
	}

	entries := model.Transcript()
	if len(entries) != transcriptBefore+1 {
		t.Fatalf("transcript should gain exactly one entry, before=%d after=%d", transcriptBefore, len(entries))
	}
	last := entries[len(entries)-1]
	if last.Role != "user" {
		t.Errorf("transcript entry role = %q, want %q", last.Role, "user")
	}
	if !strings.Contains(last.Content, "focus on X") {
		t.Errorf("transcript entry missing steered message: %+v", last)
	}
	if !strings.Contains(last.Content, "steered") {
		t.Errorf("transcript entry missing steering marker (exports must show it was steered): %+v", last)
	}

	if !model.RunActive() {
		t.Error("run must stay active after steering.received")
	}
}

func TestSteeringReceived_MarkerDistinctFromTypedPrompt(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})

	// A typed prompt renders as a normal user bubble...
	m1, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "write the tests"})
	model := m1.(tui.Model)

	// ...and a server-confirmed steer renders with the marker.
	m2, _ := model.Update(tui.SSEEventMsg{
		EventType: "steering.received",
		Raw:       []byte(`{"message":"switch to approach B"}`),
	})
	model = m2.(tui.Model)

	view := model.View()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "write the tests") && strings.Contains(line, "steered") {
			t.Errorf("typed prompt must NOT carry the steering marker: %q", line)
		}
		if strings.Contains(line, "switch to approach B") && !strings.Contains(line, "steered") {
			t.Errorf("steered message MUST carry the steering marker: %q", line)
		}
	}

	// Transcript: typed prompt entry is unmarked; steered entry is marked.
	var typedEntry, steeredEntry *struct{ role, content string }
	for _, e := range model.Transcript() {
		e := e
		if strings.Contains(e.Content, "write the tests") {
			typedEntry = &struct{ role, content string }{e.Role, e.Content}
		}
		if strings.Contains(e.Content, "switch to approach B") {
			steeredEntry = &struct{ role, content string }{e.Role, e.Content}
		}
	}
	if typedEntry == nil || steeredEntry == nil {
		t.Fatalf("expected both transcript entries, got %+v", model.Transcript())
	}
	if strings.Contains(typedEntry.content, "steered") {
		t.Errorf("typed prompt transcript entry must NOT carry the marker: %+v", *typedEntry)
	}
	if !strings.Contains(steeredEntry.content, "steered") {
		t.Errorf("steered transcript entry MUST carry the marker: %+v", *steeredEntry)
	}
}

func TestSteeringReceived_MalformedPayload_NoPanicNoMarker(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", `not-json`},
		{"empty object", `{}`},
		{"wrong type", `{"message":42}`},
		{"whitespace message", `{"message":"   "}`},
		{"empty message", `{"message":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := initModel(t, 80, 24)
			m = m.WithCancelRun(func() {})
			m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-steer-bad"})
			model := m2.(tui.Model)

			transcriptBefore := len(model.Transcript())

			m3, _ := model.Update(tui.SSEEventMsg{
				EventType: "steering.received",
				Raw:       []byte(tc.raw),
			})
			if m3 == nil {
				t.Fatal("Update returned nil model for malformed steering.received")
			}
			model = m3.(tui.Model)

			if got := len(model.Transcript()); got != transcriptBefore {
				t.Errorf("malformed payload must not append transcript entries: before=%d after=%d", transcriptBefore, got)
			}
			if strings.Contains(model.View(), "steered") {
				t.Errorf("malformed payload must not render a steering marker; view=\n%s", model.View())
			}
			if !model.RunActive() {
				t.Error("run must stay active after malformed steering.received")
			}
		})
	}
}
