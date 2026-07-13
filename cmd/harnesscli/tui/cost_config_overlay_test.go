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
