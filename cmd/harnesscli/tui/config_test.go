package tui_test

import (
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func TestTUI010_DefaultTUIConfigIsNonInteractive(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	if cfg.EnableTUI {
		t.Error("DefaultTUIConfig should not enable TUI by default (opt-in)")
	}
}

func TestTUI010_TUIConfigRoundTrip(t *testing.T) {
	cfg := tui.TUIConfig{
		BaseURL:      "http://localhost:9090",
		EnableTUI:    true,
		ColorProfile: "truecolor",
		AltScreen:    true,
	}
	if !cfg.EnableTUI {
		t.Error("EnableTUI not preserved")
	}
	if cfg.BaseURL == "" {
		t.Error("BaseURL empty")
	}
	if cfg.ColorProfile != "truecolor" {
		t.Errorf("ColorProfile = %q, want truecolor", cfg.ColorProfile)
	}
	if !cfg.AltScreen {
		t.Error("AltScreen not preserved")
	}
}

func TestTUI010_DefaultTUIConfigValues(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q, want http://localhost:8080", cfg.BaseURL)
	}
	if cfg.ColorProfile != "auto" {
		t.Errorf("ColorProfile = %q, want auto (detected/applied at startup)", cfg.ColorProfile)
	}
	if !cfg.AltScreen {
		t.Error("AltScreen should default to true")
	}
}
