package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// initModelForTitle creates a Model with an isolated HOME so the session store
// writes to a temp dir, then starts a session so a conversation ID exists.
func initModelForTitle(t *testing.T) tui.Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-1"})
	return m2.(tui.Model)
}

// TestTitleCommand_SetPersistsAndShowsInStatusbar verifies that
// `/title fix auth bug` stores the title on the current session and renders it
// in the status bar.
func TestTitleCommand_SetPersistsAndShowsInStatusbar(t *testing.T) {
	m := initModelForTitle(t)

	m = sendSlashCommand(m, "/title fix auth bug")

	entry, ok := m.SessionStore().Get("run-1")
	if !ok {
		t.Fatal("session entry missing from store after RunStartedMsg")
	}
	if entry.Title != "fix auth bug" {
		t.Errorf("stored Title: want %q, got %q", "fix auth bug", entry.Title)
	}
	if got := m.StatusMsg(); !strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusMsg() = %q, want it to confirm the new title", got)
	}
	if got := m.StatusBarView(); !strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusBarView() = %q, want it to contain the session title", got)
	}
}

// TestTitleCommand_NoArgsPrintsCurrentTitle verifies that `/title` with no
// arguments reports the current title.
func TestTitleCommand_NoArgsPrintsCurrentTitle(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")

	m = sendSlashCommand(m, "/title")

	if got := m.StatusMsg(); !strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusMsg() = %q, want it to show the current title", got)
	}
}

// TestTitleCommand_NoArgsWithoutTitle verifies that `/title` with no title set
// explains how to set one.
func TestTitleCommand_NoArgsWithoutTitle(t *testing.T) {
	m := initModelForTitle(t)

	m = sendSlashCommand(m, "/title")

	if got := m.StatusMsg(); !strings.Contains(got, "No title set") {
		t.Errorf("StatusMsg() = %q, want a 'No title set' hint", got)
	}
}

// TestTitleCommand_ClearRemovesTitle verifies that `/title clear` removes the
// title from the store and the status bar.
func TestTitleCommand_ClearRemovesTitle(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")

	m = sendSlashCommand(m, "/title clear")

	entry, _ := m.SessionStore().Get("run-1")
	if entry.Title != "" {
		t.Errorf("stored Title after /title clear: want empty, got %q", entry.Title)
	}
	if got := m.StatusBarView(); strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusBarView() = %q, must not show the cleared title", got)
	}
}

// TestTitleCommand_ClearOnlyWhenSoleArgument documents that `clear` only
// clears when it is the entire argument list; `/title clear screen` sets a
// literal title.
func TestTitleCommand_ClearOnlyWhenSoleArgument(t *testing.T) {
	m := initModelForTitle(t)

	m = sendSlashCommand(m, "/title clear screen")

	entry, _ := m.SessionStore().Get("run-1")
	if entry.Title != "clear screen" {
		t.Errorf("stored Title: want %q, got %q", "clear screen", entry.Title)
	}
}

// TestTitleCommand_RequiresActiveSession verifies that `/title` before any
// session exists explains that a session is required and stores nothing.
func TestTitleCommand_RequiresActiveSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 80, 24) // no RunStartedMsg: no active session

	m = sendSlashCommand(m, "/title foo")

	if got := m.StatusMsg(); !strings.Contains(got, "No active session") {
		t.Errorf("StatusMsg() = %q, want a 'No active session' explanation", got)
	}
	if n := len(m.SessionStore().List()); n != 0 {
		t.Errorf("store must stay empty when there is no active session, got %d entries", n)
	}
}

// TestTitleCommand_PersistsAcrossReload simulates a TUI restart: a new
// SessionStore reading the same sessions.json must see the title.
func TestTitleCommand_PersistsAcrossReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := initModel(t, 80, 24)
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-1"})
	m = m2.(tui.Model)

	m = sendSlashCommand(m, "/title fix auth bug")

	reloaded := tui.NewSessionStore(filepath.Join(home, ".config", "harnesscli"))
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load after simulated restart: %v", err)
	}
	entry, ok := reloaded.Get("run-1")
	if !ok {
		t.Fatal("session missing after simulated restart")
	}
	if entry.Title != "fix auth bug" {
		t.Errorf("Title after restart: want %q, got %q", "fix auth bug", entry.Title)
	}
}

// TestTitleCommand_SessionPickerShowsTitle verifies that /sessions renders the
// title instead of the session ID for titled sessions.
func TestTitleCommand_SessionPickerShowsTitle(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")

	m = sendSlashCommand(m, "/sessions")
	v := m.View()

	if !strings.Contains(v, "fix auth bug") {
		t.Errorf("/sessions view must contain the session title:\n%s", v)
	}
	if strings.Contains(v, "run-1") {
		t.Errorf("/sessions view must not show the bare session ID when a title is set:\n%s", v)
	}
}

// TestTitleCommand_SwitchingSessionLoadsItsTitle verifies that picking a
// titled session in /sessions updates the status bar to that session's title.
func TestTitleCommand_SwitchingSessionLoadsItsTitle(t *testing.T) {
	m := initModelForTitle(t)
	m.SessionStore().Add(tui.StoredSessionEntry{
		ID:        "sess-b",
		StartedAt: time.Now(),
		Title:     "second session",
	})

	m2, _ := m.Update(tui.SessionPickerSelectedMsg{SessionID: "sess-b"})
	m = m2.(tui.Model)

	if got := m.StatusBarView(); !strings.Contains(got, "second session") {
		t.Errorf("StatusBarView() = %q, want the switched session's title", got)
	}
}

// TestTitleCommand_SwitchingToUntitledSessionClearsStatusbar verifies that
// switching from a titled session to an untitled one removes the title from
// the status bar.
func TestTitleCommand_SwitchingToUntitledSessionClearsStatusbar(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")
	m.SessionStore().Add(tui.StoredSessionEntry{ID: "sess-plain", StartedAt: time.Now()})

	m2, _ := m.Update(tui.SessionPickerSelectedMsg{SessionID: "sess-plain"})
	m = m2.(tui.Model)

	if got := m.StatusBarView(); strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusBarView() = %q, must not keep the previous session's title", got)
	}
}

// TestTitleCommand_NewSessionClearsStatusbar verifies that /new drops the
// title from the status bar.
func TestTitleCommand_NewSessionClearsStatusbar(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")

	m = sendSlashCommand(m, "/new")

	if got := m.StatusBarView(); strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusBarView() = %q after /new, must not show the old session's title", got)
	}
}

// TestTitleCommand_TitleSurvivesWindowResize verifies the status bar title is
// re-applied when the status bar is rebuilt on a WindowSizeMsg.
func TestTitleCommand_TitleSurvivesWindowResize(t *testing.T) {
	m := initModelForTitle(t)
	m = sendSlashCommand(m, "/title fix auth bug")

	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = m2.(tui.Model)

	if got := m.StatusBarView(); !strings.Contains(got, "fix auth bug") {
		t.Errorf("StatusBarView() = %q after resize, want the title re-applied", got)
	}
}

// TestTitleCommand_Registered verifies the title command is registered and
// dispatchable in the built-in registry.
func TestTitleCommand_Registered(t *testing.T) {
	r := tui.NewCommandRegistry()
	if !r.IsRegistered("title") {
		t.Fatal("built-in registry must register the title command")
	}
	entry, ok := r.Lookup("title")
	if !ok {
		t.Fatal("Lookup(title) returned false")
	}
	if entry.Description == "" {
		t.Error("title command must have a description for /help and autocomplete")
	}
}

// TestTitleCommand_InSlashComplete verifies /title appears in the autocomplete
// dropdown when typing "/ti".
func TestTitleCommand_InSlashComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "/ti")
	v := m.View()
	if !strings.Contains(v, "title") {
		t.Errorf("slash-complete must contain 'title' when typing '/ti'; got:\n%s", v)
	}
}

// TestTitleCommand_InHelpDialog verifies /title is listed in the /help overlay.
// The window must be tall enough to show every registered command: the help
// dialog renders height-13 content lines and the registry has ~28 commands.
func TestTitleCommand_InHelpDialog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 120, 50)
	m = sendSlashCommand(m, "/help")
	if v := m.View(); !strings.Contains(v, "title") {
		t.Errorf("/help overlay must list the title command; got:\n%s", v)
	}
}
