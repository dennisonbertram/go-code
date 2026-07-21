package tui

// theme_persistence_test.go — slice 4 of epic #810: the /theme selection is
// persisted to ~/.config/harnesscli/config.json and re-applied at startup.
// Every test redirects HOME to a temp dir so nothing touches the real
// user config.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessconfig "go-agent-harness/cmd/harnesscli/config"
)

// writeThemeInHome writes a theme file into $HOME/.config/harnesscli/themes.
func writeThemeInHome(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "harnesscli", "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStartupTheme_SavedValidThemeResolves verifies TUIConfig.Theme is
// resolved through the loader at startup and restyles components.
func TestStartupTheme_SavedValidThemeResolves(t *testing.T) {
	forceTrueColor(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeThemeInHome(t, home, "wild", `{"warning": "#FF0000"}`)

	cfg := DefaultTUIConfig()
	cfg.Theme = "wild"
	m := New(cfg)

	if m.themeName != "wild" {
		t.Errorf("themeName = %q, want wild", m.themeName)
	}
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("startup theme did not restyle statusbar: %q", out)
	}
}

// TestStartupTheme_MissingFileFallsBackSilently verifies a saved theme whose
// file was deleted starts the TUI in the default palette without failing.
func TestStartupTheme_MissingFileFallsBackSilently(t *testing.T) {
	forceTrueColor(t)
	home := t.TempDir()
	t.Setenv("HOME", home) // no themes dir at all

	cfg := DefaultTUIConfig()
	cfg.Theme = "ghost"
	m := New(cfg) // must not fail or panic

	// Default rendering: amber statusbar warning.
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("missing theme file should render default palette: %q", out)
	}
}

// TestStartupTheme_MalformedFallsBackToDefault verifies a saved theme with
// broken JSON keeps the default theme entirely (name included).
func TestStartupTheme_MalformedFallsBackToDefault(t *testing.T) {
	forceTrueColor(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeThemeInHome(t, home, "broken", `{"warning":`)

	cfg := DefaultTUIConfig()
	cfg.Theme = "broken"
	m := New(cfg)

	if m.themeName != "default-dark" {
		t.Errorf("malformed startup theme: themeName = %q, want default-dark", m.themeName)
	}
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("malformed startup theme should render default palette: %q", out)
	}
}

// TestThemePickerSelect_PersistsAcrossRestart is the acceptance flow: select
// a theme via /theme, quit, relaunch — the TUI starts in that theme.
func TestThemePickerSelect_PersistsAcrossRestart(t *testing.T) {
	forceTrueColor(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeThemeInHome(t, home, "wild", `{"warning": "#FF0000"}`)

	// First "run": open /theme via the real command path (no themesDir
	// override — exercises DefaultThemesDir) and select wild.
	m := sizedModel(t, 80, 24)
	executeThemeCommand(&m, Command{})
	if !m.themePicker.IsOpen() {
		t.Fatal("/theme did not open the picker")
	}
	m = selectTheme(t, m, "wild")
	if m.themeName != "wild" {
		t.Fatalf("themeName = %q, want wild", m.themeName)
	}

	// The selection must be persisted to config.json.
	saved, err := harnessconfig.Load()
	if err != nil {
		t.Fatalf("Load() after selection: %v", err)
	}
	if saved.Theme != "wild" {
		t.Fatalf("saved config Theme = %q, want wild", saved.Theme)
	}

	// "Relaunch": a fresh model started with the saved name must resolve it.
	cfg := DefaultTUIConfig()
	cfg.Theme = saved.Theme
	m2 := New(cfg)
	if m2.themeName != "wild" {
		t.Errorf("relaunched model themeName = %q, want wild", m2.themeName)
	}
	m2.statusBar.SetPermMode("plan")
	if out := m2.statusBar.View(); !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("relaunched model did not start in the saved theme: %q", out)
	}
}

// TestThemePickerSelect_DeleteFileFallsBackToDefault is the second half of
// the acceptance flow: delete the theme file — the TUI starts in the
// default theme.
func TestThemePickerSelect_DeleteFileFallsBackToDefault(t *testing.T) {
	forceTrueColor(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeThemeInHome(t, home, "wild", `{"warning": "#FF0000"}`)

	m := sizedModel(t, 80, 24)
	executeThemeCommand(&m, Command{})
	m = selectTheme(t, m, "wild")

	// User deletes the theme file; next launch must render the default.
	if err := os.Remove(filepath.Join(home, ".config", "harnesscli", "themes", "wild.json")); err != nil {
		t.Fatal(err)
	}
	saved, err := harnessconfig.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	cfg := DefaultTUIConfig()
	cfg.Theme = saved.Theme
	m2 := New(cfg)
	m2.statusBar.SetPermMode("plan")
	if out := m2.statusBar.View(); !strings.Contains(out, "38;2;255;175;0") {
		t.Errorf("deleted theme file should fall back to default palette: %q", out)
	}
}

// TestConfigPanel_ThemeRowReflectsActiveTheme verifies the config panel's
// theme row shows the active theme (m.themeName), not the startup
// TUIConfig.Theme string.
func TestConfigPanel_ThemeRowReflectsActiveTheme(t *testing.T) {
	m := sizedModel(t, 80, 24)

	rowValue := func() string {
		for _, e := range configEntriesFromModel(&m) {
			if e.Key == "theme" {
				return e.Value
			}
		}
		return ""
	}
	if got := rowValue(); got != "default-dark" {
		t.Errorf("theme row = %q, want default-dark", got)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	writeThemeInHome(t, home, "wild", `{"warning": "#FF0000"}`)
	executeThemeCommand(&m, Command{})
	m = selectTheme(t, m, "wild")

	if got := rowValue(); got != "wild" {
		t.Errorf("theme row after live apply = %q, want wild", got)
	}
}
