package tui_test

// swarm_panel_test.go — epic #808 slice 4: the /subagents live swarm panel.
// Drives the public model surface: agent_swarm tool events start/stop the
// tracked panel, poll responses update it in place, and completion freezes
// it with the exact aggregated statuses.

import (
	"strings"
	"testing"
	"time"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}

func TestAgentSwarmLivePanelFlow(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-swarm-1"})
	model := m2.(tui.Model)

	// The model calls agent_swarm with two items: the panel appears with both
	// items pending.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","arguments":{"prompt_template":"do {{item}}","items":["alpha","beta"]}}`),
	})
	model = m3.(tui.Model)
	view := model.View()
	if !strings.Contains(view, "Swarm:") {
		t.Fatalf("view has no swarm panel after agent_swarm start:\n%s", view)
	}
	if !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") {
		t.Fatalf("panel missing item labels:\n%s", view)
	}

	// A poll response matches the first member by creation window; the panel
	// updates in place (still exactly one block).
	m4, _ := model.Update(tui.SubagentsLoadedMsg{
		SwarmPoll: true,
		Subagents: []tui.RemoteSubagent{
			{ID: "sub-1", Status: "running", Isolation: "inline", CleanupPolicy: "preserve", CreatedAt: time.Now().Add(2 * time.Second)},
		},
	})
	model = m4.(tui.Model)
	view = model.View()
	if !strings.Contains(view, "running") || !strings.Contains(view, "sub-1") {
		t.Fatalf("panel did not pick up the running member:\n%s", view)
	}
	if got := countOccurrences(view, "Swarm:"); got != 1 {
		t.Fatalf("view contains %d swarm panels, want exactly 1 (in-place update):\n%s", got, view)
	}

	// Completion delivers the aggregated report: exact statuses, and the
	// panel freezes.
	report := `{"members":[` +
		`{"id":"sub-1","item":"alpha","prompt":"do alpha","status":"completed","output":"done"},` +
		`{"id":"sub-2","item":"beta","prompt":"do beta","status":"failed","error":"boom"}` +
		`],"total":2,"completed":1,"failed":1}`
	m5, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","output":` + jsonString(report) + `}`),
	})
	model = m5.(tui.Model)
	view = model.View()
	if !strings.Contains(view, "[completed]") || !strings.Contains(view, "[failed") {
		t.Fatalf("panel missing exact report statuses:\n%s", view)
	}
	if !strings.Contains(view, "sub-2") {
		t.Fatalf("panel missing the failed member's id from the report:\n%s", view)
	}
	if got := countOccurrences(view, "Swarm:"); got != 1 {
		t.Fatalf("view contains %d swarm panels after completion, want 1:\n%s", got, view)
	}

	// A stale poll after completion must not resurrect or duplicate the panel.
	m6, _ := model.Update(tui.SubagentsLoadedMsg{SwarmPoll: true})
	model = m6.(tui.Model)
	if got := countOccurrences(model.View(), "Swarm:"); got != 1 {
		t.Fatalf("stale poll changed the frozen panel (count %d):\n%s", got, model.View())
	}
}

// TestAgentSwarmPollTickStopsWithSwarm verifies the poll loop only ticks
// while a swarm is active.
func TestAgentSwarmPollTickStopsWithSwarm(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-swarm-2"})
	model := m2.(tui.Model)

	// No swarm active: the tick is a no-op.
	_, cmd := model.Update(tui.SwarmPollTickMsg{})
	if cmd != nil {
		t.Fatal("tick without an active swarm returned commands, want nil")
	}

	// Start a swarm: the tick must schedule a poll and the next tick.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","arguments":{"prompt_template":"do {{item}}","items":["a"]}}`),
	})
	model = m3.(tui.Model)
	_, cmd = model.Update(tui.SwarmPollTickMsg{})
	if cmd == nil {
		t.Fatal("tick with an active swarm returned nil, want poll + re-tick")
	}

	// Complete it: the tick stops.
	report := `{"members":[{"id":"sub-1","item":"a","prompt":"do a","status":"completed"}],"total":1,"completed":1}`
	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","output":` + jsonString(report) + `}`),
	})
	model = m4.(tui.Model)
	_, cmd = model.Update(tui.SwarmPollTickMsg{})
	if cmd != nil {
		t.Fatal("tick after swarm completion returned commands, want nil")
	}
}

// TestSubagentsListingGroupsSwarm verifies the user-invoked /subagents
// listing embeds the swarm group when a swarm is tracked, and omits it when
// nothing was ever tracked.
func TestSubagentsListingGroupsSwarm(t *testing.T) {
	m := initModel(t, 120, 40)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-swarm-3"})
	model := m2.(tui.Model)

	// Plain listing with no swarm tracked: no swarm section.
	m3, _ := model.Update(tui.SubagentsLoadedMsg{
		Subagents: []tui.RemoteSubagent{{ID: "other", Status: "queued", Isolation: "inline", CleanupPolicy: "preserve"}},
	})
	model = m3.(tui.Model)
	if strings.Contains(model.View(), "Swarm:") {
		t.Fatalf("listing shows a swarm group with nothing tracked:\n%s", model.View())
	}

	// Track a swarm and complete it so the tracker holds exact members.
	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.started",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","arguments":{"prompt_template":"do {{item}}","items":["alpha"]}}`),
	})
	model = m4.(tui.Model)
	report := `{"members":[{"id":"sub-1","item":"alpha","prompt":"do alpha","status":"completed"}],"total":1,"completed":1}`
	m5, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.call.completed",
		Raw:       []byte(`{"tool":"agent_swarm","call_id":"c1","output":` + jsonString(report) + `}`),
	})
	model = m5.(tui.Model)

	// A user-invoked listing afterwards groups the swarm members.
	m6, _ := model.Update(tui.SubagentsLoadedMsg{
		Subagents: []tui.RemoteSubagent{{ID: "other", Status: "queued", Isolation: "inline", CleanupPolicy: "preserve"}},
	})
	model = m6.(tui.Model)
	view := model.View()
	if !strings.Contains(view, "Swarm:") || !strings.Contains(view, "[completed] alpha (sub-1)") {
		t.Fatalf("listing missing the swarm group:\n%s", view)
	}
	if !strings.Contains(view, "other [queued]") {
		t.Fatalf("listing missing the regular subagent entry:\n%s", view)
	}
}

// jsonString wraps s as a JSON string literal (for embedding in event payloads).
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
