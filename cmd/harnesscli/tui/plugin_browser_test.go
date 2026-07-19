package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"go-agent-harness/internal/plugins"
)

func TestPluginBrowserKeyboardEnablesSelectedPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".go-harness", "plugins", "demo", "1.0.0")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"schema_version":1,"name":"demo","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugins.NewStateStore(filepath.Join(home, ".go-harness", "plugins", "state.json")).RecordInstall(plugins.InstalledPlugin{Name: "demo", Version: "1.0.0", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	m := New(DefaultTUIConfig())
	executePluginsCommand(&m, Command{Name: "plugins"})
	if got := m.pluginBrowser.View(80); got == "" {
		t.Fatal("browser view is empty")
	}
	if !m.overlayActive || m.activeOverlay != "plugins" {
		t.Fatalf("overlay=%q", m.activeOverlay)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.pluginBrowser.items[0].Enabled {
		t.Fatal("enter should enable selected plugin")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.overlayActive {
		t.Fatal("escape should close plugin browser")
	}
}

func TestInstallableBashCommandsRequirePluginTrust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".go-harness", "plugins", "remote", "1.0.0")
	if err := os.MkdirAll(filepath.Join(root, "commands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"schema_version":1,"name":"remote","version":"1.0.0","commands":"commands"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := plugins.NewStateStore(filepath.Join(home, ".go-harness", "plugins", "state.json"))
	if err := store.RecordInstall(plugins.InstalledPlugin{Name: "remote", Version: "1.0.0", Remote: true}); err != nil {
		t.Fatal(err)
	}
	if got := installablePluginCommandDirs(); len(got) != 0 {
		t.Fatalf("untrusted dirs=%v", got)
	}
	if err := store.SetTrusted("remote", true); err != nil {
		t.Fatal(err)
	}
	if got := installablePluginCommandDirs(); len(got) != 1 {
		t.Fatalf("trusted dirs=%v", got)
	}
}
