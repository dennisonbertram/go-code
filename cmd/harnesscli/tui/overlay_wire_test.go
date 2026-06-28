package tui_test

import (
	"encoding/json"
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestHelpOverlay_ShowsCommands verifies that the help dialog is populated with
// the registered slash commands (clear, help, quit, etc.).
func TestHelpOverlay_ShowsCommands(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/help")

	if !m.OverlayActive() {
		t.Fatal("overlay must be active after /help")
	}

	view := m.View()

	// With many registered commands and a fixed dialog height (80x24), not all
	// commands fit on the first visible page — overflow pagination clips the list.
	// Check commands that are guaranteed to appear on the first visible page
	// (alphabetically first batch). Commands past the page boundary (e.g. "search",
	// "sessions") are only visible after scrolling and are intentionally omitted here.
	wantCommands := []string{"attach", "cancel", "clear", "context", "doctor", "export"}
	for _, cmd := range wantCommands {
		if !strings.Contains(view, cmd) {
			t.Errorf("help overlay View() must contain command %q; got:\n%s", cmd, view)
		}
	}
	// Verify the overflow indicator is visible when content is clipped.
	if !strings.Contains(view, "▼") {
		t.Errorf("help overlay should show '▼' overflow indicator when commands exceed visible height; got:\n%s", view)
	}
}

// buildUsageDeltaRaw constructs a JSON payload for a usage.delta SSE event.
func buildUsageDeltaRaw(totalTokens int, costUSD float64) json.RawMessage {
	type usage struct {
		TotalTokens int `json:"total_tokens"`
	}
	type payload struct {
		CumulativeUsage   usage   `json:"cumulative_usage"`
		CumulativeCostUSD float64 `json:"cumulative_cost_usd"`
	}
	p := payload{
		CumulativeUsage:   usage{TotalTokens: totalTokens},
		CumulativeCostUSD: costUSD,
	}
	b, _ := json.Marshal(p)
	return json.RawMessage(b)
}

// TestStatsPanel_ShowsUsageData verifies that after receiving a usage.delta SSE
// event, the stats overlay shows a non-zero activity entry.
func TestStatsPanel_ShowsUsageData(t *testing.T) {
	m := initModel(t, 80, 24)

	// Send a usage.delta event with some token/cost data.
	m2, _ := m.Update(tui.SSEEventMsg{
		EventType: "usage.delta",
		Raw:       buildUsageDeltaRaw(1500, 0.025),
	})
	m = m2.(tui.Model)

	// Open the stats overlay.
	m = sendSlashCommand(m, "/stats")
	if !m.OverlayActive() {
		t.Fatal("overlay must be active after /stats")
	}

	view := m.View()

	// Stats panel always renders "Activity" header.
	if !strings.Contains(view, "Activity") {
		t.Errorf("stats overlay must contain 'Activity'; got:\n%s", view)
	}

	// After receiving usage data, the total runs count should be non-zero.
	// The stats panel renders "Total runs: N" — verify it's not "Total runs: 0".
	if strings.Contains(view, "Total runs: 0") {
		t.Errorf("stats overlay must show non-zero runs after usage.delta; got:\n%s", view)
	}
}

// TestContextGrid_ShowsContextData verifies that the context overlay renders
// token usage data (not the old empty stub fallback).
func TestContextGrid_ShowsContextData(t *testing.T) {
	m := initModel(t, 80, 24)

	// Send a usage.delta event to populate token counts.
	m2, _ := m.Update(tui.SSEEventMsg{
		EventType: "usage.delta",
		Raw:       buildUsageDeltaRaw(8192, 0.01),
	})
	m = m2.(tui.Model)

	// Open the context overlay.
	m = sendSlashCommand(m, "/context")
	if !m.OverlayActive() {
		t.Fatal("overlay must be active after /context")
	}

	view := m.View()

	// Context grid now renders real content — check for its header.
	if !strings.Contains(view, "Context Window Usage") {
		t.Errorf("context overlay must contain 'Context Window Usage'; got:\n%s", view)
	}

	// The view must not be empty.
	if strings.TrimSpace(view) == "" {
		t.Error("context overlay View() must not be empty")
	}
}
