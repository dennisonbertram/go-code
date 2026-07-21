package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// initModelForAddDir creates a Model whose workspace is a temp directory, so
// relative /add-dir paths resolve inside test-controlled storage.
func initModelForAddDir(t *testing.T) (tui.Model, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	cfg := tui.DefaultTUIConfig()
	cfg.Workspace = ws
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(tui.Model), ws
}

// TestAddDirCommand_AddAndList verifies /add-dir <path> records the directory
// and bare /add-dir lists it.
func TestAddDirCommand_AddAndList(t *testing.T) {
	m, _ := initModelForAddDir(t)
	extra := t.TempDir()

	m = sendSlashCommand(m, "/add-dir "+extra)
	if got := m.StatusMsg(); !strings.Contains(got, "Added") || !strings.Contains(got, extra) {
		t.Fatalf("StatusMsg() = %q, want an 'Added <dir>' confirmation", got)
	}
	dirs := m.ExtraDirs()
	if len(dirs) != 1 || dirs[0] != extra {
		t.Fatalf("ExtraDirs() = %v, want [%s]", dirs, extra)
	}

	m = sendSlashCommand(m, "/add-dir")
	if got := m.StatusMsg(); !strings.Contains(got, extra) {
		t.Errorf("StatusMsg() = %q after bare /add-dir, want the added dir listed", got)
	}
}

// TestAddDirCommand_ListEmpty verifies bare /add-dir explains the empty state.
func TestAddDirCommand_ListEmpty(t *testing.T) {
	m, _ := initModelForAddDir(t)

	m = sendSlashCommand(m, "/add-dir")
	if got := m.StatusMsg(); !strings.Contains(got, "No extra directories") {
		t.Errorf("StatusMsg() = %q, want a 'No extra directories' hint", got)
	}
}

// TestAddDirCommand_RelativeResolvedAgainstWorkspace verifies a relative path
// is resolved against the session workspace and stored absolute.
func TestAddDirCommand_RelativeResolvedAgainstWorkspace(t *testing.T) {
	m, ws := initModelForAddDir(t)
	if err := os.Mkdir(filepath.Join(ws, "libs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	m = sendSlashCommand(m, "/add-dir libs")

	want := filepath.Join(ws, "libs")
	dirs := m.ExtraDirs()
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("ExtraDirs() = %v, want [%s]", dirs, want)
	}
}

// TestAddDirCommand_RejectsNonexistent verifies a missing directory is not added.
func TestAddDirCommand_RejectsNonexistent(t *testing.T) {
	m, ws := initModelForAddDir(t)

	m = sendSlashCommand(m, "/add-dir "+filepath.Join(ws, "nope"))

	if got := m.StatusMsg(); !strings.Contains(got, "not") {
		t.Errorf("StatusMsg() = %q, want a not-a-directory / does-not-exist error", got)
	}
	if n := len(m.ExtraDirs()); n != 0 {
		t.Errorf("ExtraDirs() len = %d, want 0 after rejecting a missing dir", n)
	}
}

// TestAddDirCommand_RejectsFiles verifies a regular file is not accepted as a
// directory.
func TestAddDirCommand_RejectsFiles(t *testing.T) {
	m, ws := initModelForAddDir(t)
	file := filepath.Join(ws, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	m = sendSlashCommand(m, "/add-dir "+file)

	if got := m.StatusMsg(); !strings.Contains(got, "not a directory") {
		t.Errorf("StatusMsg() = %q, want a 'not a directory' error", got)
	}
	if n := len(m.ExtraDirs()); n != 0 {
		t.Errorf("ExtraDirs() len = %d, want 0 after rejecting a file", n)
	}
}

// TestAddDirCommand_Dedupes verifies adding the same directory twice keeps one
// entry and tells the user.
func TestAddDirCommand_Dedupes(t *testing.T) {
	m, _ := initModelForAddDir(t)
	extra := t.TempDir()

	m = sendSlashCommand(m, "/add-dir "+extra)
	m = sendSlashCommand(m, "/add-dir "+extra)

	if got := m.StatusMsg(); !strings.Contains(got, "Already") {
		t.Errorf("StatusMsg() = %q, want an 'already added' notice", got)
	}
	if n := len(m.ExtraDirs()); n != 1 {
		t.Errorf("ExtraDirs() len = %d, want 1 after duplicate add", n)
	}
}

// TestAddDirCommand_Remove verifies /add-dir remove drops the directory.
func TestAddDirCommand_Remove(t *testing.T) {
	m, _ := initModelForAddDir(t)
	extra := t.TempDir()

	m = sendSlashCommand(m, "/add-dir "+extra)
	m = sendSlashCommand(m, "/add-dir remove "+extra)

	if got := m.StatusMsg(); !strings.Contains(got, "Removed") || !strings.Contains(got, extra) {
		t.Fatalf("StatusMsg() = %q, want a 'Removed <dir>' confirmation", got)
	}
	if n := len(m.ExtraDirs()); n != 0 {
		t.Fatalf("ExtraDirs() len = %d, want 0 after remove", n)
	}

	m = sendSlashCommand(m, "/add-dir")
	if got := m.StatusMsg(); !strings.Contains(got, "No extra directories") {
		t.Errorf("StatusMsg() = %q, want empty state after remove", got)
	}
}

// TestAddDirCommand_RemoveNotPresent verifies removing a directory that was
// never added says so.
func TestAddDirCommand_RemoveNotPresent(t *testing.T) {
	m, _ := initModelForAddDir(t)
	extra := t.TempDir()

	m = sendSlashCommand(m, "/add-dir remove "+extra)

	if got := m.StatusMsg(); !strings.Contains(got, "not in the extra directories") {
		t.Errorf("StatusMsg() = %q, want a 'not in the extra directories' notice", got)
	}
}

// TestAddDirCommand_RemoveKeywordWithoutArgAddsLiteralDir documents the
// collision rule: "remove" is a subcommand only when followed by a path, so a
// directory literally named "remove" can still be added.
func TestAddDirCommand_RemoveKeywordWithoutArgAddsLiteralDir(t *testing.T) {
	m, ws := initModelForAddDir(t)
	if err := os.Mkdir(filepath.Join(ws, "remove"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	m = sendSlashCommand(m, "/add-dir remove")

	want := filepath.Join(ws, "remove")
	dirs := m.ExtraDirs()
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("ExtraDirs() = %v, want [%s]", dirs, want)
	}
}

// TestAddDirCommand_Registered verifies the add-dir command is registered.
func TestAddDirCommand_Registered(t *testing.T) {
	r := tui.NewCommandRegistry()
	if !r.IsRegistered("add-dir") {
		t.Fatal("built-in registry must register the add-dir command")
	}
	entry, ok := r.Lookup("add-dir")
	if !ok || entry.Description == "" {
		t.Fatal("add-dir command must have a description for /help and autocomplete")
	}
}

// TestAddDirCommand_InSlashComplete verifies /add-dir appears in autocomplete.
func TestAddDirCommand_InSlashComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "/add")
	if v := m.View(); !strings.Contains(v, "add-dir") {
		t.Errorf("slash-complete must contain 'add-dir' when typing '/add'; got:\n%s", v)
	}
}
