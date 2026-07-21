package tui

// theme_picker_test.go — slice 3 of epic #810: the /theme overlay lists
// built-in and file-based themes (re-scanning the themes directory on every
// open), applies a selection live via SetTheme, and keeps the current theme
// when the loader fails.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/themepicker"
)

// openThemePicker runs the /theme command path against the given themes dir.
func openThemePicker(t *testing.T, m *Model, dir string) {
	t.Helper()
	m.themesDir = dir
	executeThemeCommand(m, Command{})
	if !m.overlayActive || m.activeOverlay != "theme" {
		t.Fatalf("/theme did not open the theme overlay (active=%v kind=%q)", m.overlayActive, m.activeOverlay)
	}
	if !m.themePicker.IsOpen() {
		t.Fatal("/theme did not open the theme picker")
	}
}

// selectTheme drives Enter on the picker and feeds the resulting
// ThemeSelectedMsg back through Update, like the real key loop does.
func selectTheme(t *testing.T, m Model, name string) Model {
	t.Helper()
	for {
		e, ok := m.themePicker.Selected()
		if !ok {
			t.Fatalf("picker has no entries; wanted %q", name)
		}
		if e.Name == name {
			break
		}
		um, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = um.(Model)
	}
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if cmd == nil {
		t.Fatalf("Enter on %q produced no command", name)
	}
	if _, ok := cmd().(themepicker.ThemeSelectedMsg); !ok {
		t.Fatalf("Enter produced %T, want themepicker.ThemeSelectedMsg", cmd())
	}
	um2, _ := m.Update(cmd())
	return um2.(Model)
}

func pickerNames(m Model) []string {
	var names []string
	for _, e := range m.themePicker.Entries() {
		names = append(names, e.Name)
	}
	return names
}

// TestThemeCommand_Registered verifies /theme is a registered slash command.
func TestThemeCommand_Registered(t *testing.T) {
	reg := NewCommandRegistry()
	if _, ok := reg.Lookup("theme"); !ok {
		t.Fatal("theme command must be registered in NewCommandRegistry()")
	}
}

// TestThemeCommand_OpensListingBuiltinsAndFiles verifies the overlay lists
// the built-in base themes plus every theme file in the themes dir.
func TestThemeCommand_OpensListingBuiltinsAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ocean.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sizedModel(t, 80, 24)
	openThemePicker(t, &m, dir)

	names := pickerNames(m)
	for _, want := range []string{"default-dark", "default-light", "ocean"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("picker entries %v missing %q", names, want)
		}
	}
}

// TestThemePicker_RescansDirectoryOnEveryOpen verifies a theme file added
// after the picker was opened appears the next time it opens (no restart).
func TestThemePicker_RescansDirectoryOnEveryOpen(t *testing.T) {
	dir := t.TempDir()
	m := sizedModel(t, 80, 24)
	openThemePicker(t, &m, dir)
	for _, n := range pickerNames(m) {
		if n == "fresh" {
			t.Fatal("fresh theme present before its file existed")
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "fresh.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.themePicker = m.themePicker.Close()
	openThemePicker(t, &m, dir)
	found := false
	for _, n := range pickerNames(m) {
		if n == "fresh" {
			found = true
		}
	}
	if !found {
		t.Errorf("re-opened picker did not re-scan directory; entries %v missing fresh", pickerNames(m))
	}
}

// TestThemePicker_SelectAppliesThemeLive verifies selection restyles the
// running model without restart: themeName updates, the status bar takes the
// new warning color, and the overlay closes.
func TestThemePicker_SelectAppliesThemeLive(t *testing.T) {
	forceTrueColor(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wild.json"), []byte(`{"warning": "#FF0000"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sizedModel(t, 80, 24)
	if m.themeName != "default-dark" {
		t.Fatalf("fresh model themeName = %q, want default-dark", m.themeName)
	}
	openThemePicker(t, &m, dir)

	m = selectTheme(t, m, "wild")

	if m.themeName != "wild" {
		t.Errorf("themeName = %q, want wild", m.themeName)
	}
	if m.overlayActive || m.themePicker.IsOpen() {
		t.Error("overlay should be closed after selection")
	}
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("statusbar did not restyle after live theme apply: %q", out)
	}
	if !strings.Contains(m.statusMsg, "wild") {
		t.Errorf("status message should name the applied theme, got %q", m.statusMsg)
	}
}

// TestThemePicker_LoaderFailureKeepsCurrentTheme verifies a malformed theme
// file leaves the active theme untouched and surfaces an error status.
func TestThemePicker_LoaderFailureKeepsCurrentTheme(t *testing.T) {
	forceTrueColor(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(`{"warning": "#00FF00"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zzbroken.json"), []byte(`{"warning":`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sizedModel(t, 80, 24)
	openThemePicker(t, &m, dir)
	m = selectTheme(t, m, "good")
	if m.themeName != "good" {
		t.Fatalf("setup: themeName = %q, want good", m.themeName)
	}

	openThemePicker(t, &m, dir)
	m = selectTheme(t, m, "zzbroken")

	if m.themeName != "good" {
		t.Errorf("failed load must keep current theme: themeName = %q, want good", m.themeName)
	}
	if !strings.Contains(strings.ToLower(m.statusMsg), "fail") {
		t.Errorf("status message should report the load failure, got %q", m.statusMsg)
	}
	// The good theme's warning color must still be active.
	m.statusBar.SetPermMode("plan")
	if out := m.statusBar.View(); !strings.Contains(out, "38;2;0;255;0") {
		t.Errorf("current theme changed after failed load: %q", out)
	}
}

// TestThemePicker_EscapeClosesOverlay verifies Esc dismisses the picker
// without changing the theme.
func TestThemePicker_EscapeClosesOverlay(t *testing.T) {
	m := sizedModel(t, 80, 24)
	openThemePicker(t, &m, t.TempDir())

	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if m.overlayActive || m.themePicker.IsOpen() {
		t.Error("Esc should close the theme overlay")
	}
	if m.themeName != "default-dark" {
		t.Errorf("Esc must not change the theme, themeName = %q", m.themeName)
	}
}
