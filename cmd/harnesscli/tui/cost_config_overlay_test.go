package tui_test

// cost_config_overlay_test.go — regression tests wiring the costdisplay and
// configpanel components as the /cost and /config overlays.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestCost_SlashCommandActivatesOverlay verifies that /cost opens the cost
// overlay and that View() reflects the current cumulative cost.
func TestCost_SlashCommandActivatesOverlay(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.SSEEventMsg{
		EventType: "usage.delta",
		Raw:       []byte(`{"cumulative_usage":{"total_tokens":1234},"cumulative_cost_usd":0.4567}`),
	})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/cost")

	if !m.OverlayActive() {
		t.Fatal("overlayActive must be true after /cost")
	}
	if m.ActiveOverlay() != "cost" {
		t.Errorf("activeOverlay: want %q, got %q", "cost", m.ActiveOverlay())
	}

	v := m.View()
	if !strings.Contains(v, "0.4567") {
		t.Errorf("View() with cost overlay must show the current cost 0.4567; got:\n%s", v)
	}
}

// TestCost_SlashCommandTogglesOverlayClosed verifies that sending /cost a
// second time closes the overlay (toggle behavior).
func TestCost_SlashCommandTogglesOverlayClosed(t *testing.T) {
	m := initModel(t, 80, 24)

	m = sendSlashCommand(m, "/cost")
	if !m.OverlayActive() || m.ActiveOverlay() != "cost" {
		t.Fatal("precondition: cost overlay must be open after first /cost")
	}

	m = sendSlashCommand(m, "/cost")
	if m.OverlayActive() {
		t.Errorf("second /cost must close the overlay; overlayActive=%v activeOverlay=%q", m.OverlayActive(), m.ActiveOverlay())
	}
}

// TestCost_EscapeClosesOverlay verifies that Escape closes the /cost overlay
// the same way it closes other overlays.
func TestCost_EscapeClosesOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/cost")
	if !m.OverlayActive() {
		t.Fatal("precondition: cost overlay must be open")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(tui.Model)

	if m.OverlayActive() {
		t.Errorf("Escape must close the cost overlay; overlayActive=%v", m.OverlayActive())
	}
}

// TestCost_OverlayRefreshesModelOnSwitchWhileOpen verifies that the /cost
// overlay reflects a newly selected model immediately, even if no
// usage.delta event arrives after the switch. Regression coverage: the /cost
// overlay's snapshot was previously refreshed only on open or on
// usage.delta, so switching models while the overlay was already open left
// it showing the stale model name.
func TestCost_OverlayRefreshesModelOnSwitchWhileOpen(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.Model = "gpt-4.1-mini"
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/cost")
	if !m.OverlayActive() || m.ActiveOverlay() != "cost" {
		t.Fatal("precondition: cost overlay must be open")
	}

	// Precondition: overlay shows the initial model.
	if !strings.Contains(m.View(), "gpt-4.1-mini") {
		t.Fatalf("precondition: cost overlay should show initial model gpt-4.1-mini; got:\n%s", m.View())
	}

	// Switch model while the cost overlay remains open, with no intervening
	// usage.delta event.
	m3, _ := m.Update(tui.ModelSelectedMsg{ModelID: "claude-3-7-sonnet-20250219", Provider: "anthropic"})
	m = m3.(tui.Model)

	v := m.View()
	if strings.Contains(v, "gpt-4.1-mini") {
		t.Errorf("cost overlay must not still show the old model gpt-4.1-mini after switch; got:\n%s", v)
	}
	if !strings.Contains(v, "claude-3-7-sonnet-20250219") {
		t.Errorf("cost overlay must show the newly selected model claude-3-7-sonnet-20250219 after switch; got:\n%s", v)
	}
}

// TestCost_OverlayModelStaysCurrentAcrossRepeatedSwitchesAndUsageDelta is a
// regression test covering a different angle than the switch-while-open
// test above: it exercises multiple consecutive model switches (to make
// sure the fix in the ModelSelectedMsg handler isn't a one-shot correction
// that only happens to work for the first switch) interleaved with a
// usage.delta event, verifying the /cost overlay always converges on the
// latest model and never resurrects a stale one. If the ModelSelectedMsg
// handler stopped refreshing costDisplay, the second switch below would
// still show the first switch's model.
func TestCost_OverlayModelStaysCurrentAcrossRepeatedSwitchesAndUsageDelta(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.Model = "gpt-4.1-mini"
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/cost")
	if !m.OverlayActive() || m.ActiveOverlay() != "cost" {
		t.Fatal("precondition: cost overlay must be open")
	}

	// First switch: gpt-4.1-mini -> claude-3-7-sonnet-20250219.
	m3, _ := m.Update(tui.ModelSelectedMsg{ModelID: "claude-3-7-sonnet-20250219", Provider: "anthropic"})
	m = m3.(tui.Model)

	// Second switch, before any usage.delta: claude-3-7-sonnet-20250219 -> gpt-4.1.
	m4, _ := m.Update(tui.ModelSelectedMsg{ModelID: "gpt-4.1", Provider: "openai"})
	m = m4.(tui.Model)

	v := m.View()
	if strings.Contains(v, "gpt-4.1-mini") || strings.Contains(v, "claude-3-7-sonnet-20250219") {
		t.Errorf("cost overlay must not show a stale model after two switches; got:\n%s", v)
	}
	if !strings.Contains(v, "[gpt-4.1]") {
		t.Errorf("cost overlay must show the latest model gpt-4.1 after two switches; got:\n%s", v)
	}

	// A subsequent usage.delta must preserve the (already-current) model
	// label alongside the new cost figures.
	m5, _ := m.Update(tui.SSEEventMsg{
		EventType: "usage.delta",
		Raw:       []byte(`{"cumulative_usage":{"total_tokens":42},"cumulative_cost_usd":0.0099}`),
	})
	m = m5.(tui.Model)

	v = m.View()
	if !strings.Contains(v, "[gpt-4.1]") {
		t.Errorf("cost overlay must still show gpt-4.1 after usage.delta; got:\n%s", v)
	}
	if !strings.Contains(v, "0.0099") {
		t.Errorf("cost overlay must reflect the latest cost after usage.delta; got:\n%s", v)
	}
}

// TestConfig_SlashCommandActivatesOverlay verifies that /config opens the
// config overlay and that View() lists the current session's config values.
func TestConfig_SlashCommandActivatesOverlay(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = "http://x.test:9999"
	cfg.Workspace = "/tmp/my-workspace"
	cfg.Model = "gpt-4.1-mini"
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/config")

	if !m.OverlayActive() {
		t.Fatal("overlayActive must be true after /config")
	}
	if m.ActiveOverlay() != "config" {
		t.Errorf("activeOverlay: want %q, got %q", "config", m.ActiveOverlay())
	}

	v := m.View()
	for _, want := range []string{"x.test:9999", "my-workspace", "gpt-4.1-mini"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() with config overlay must contain %q; got:\n%s", want, v)
		}
	}
}

// TestConfig_EscapeClosesOverlay verifies that Escape closes the /config
// overlay the same way it closes other overlays.
func TestConfig_EscapeClosesOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/config")
	if !m.OverlayActive() {
		t.Fatal("precondition: config overlay must be open")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(tui.Model)

	if m.OverlayActive() {
		t.Errorf("Escape must close the config overlay; overlayActive=%v", m.OverlayActive())
	}
}
