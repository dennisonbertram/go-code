package tui_test

import (
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestTUI041_ParseClearCommand verifies "/clear" parses to Name="clear" with no args.
func TestTUI041_ParseClearCommand(t *testing.T) {
	cmd, ok := tui.ParseCommand("/clear")
	if !ok {
		t.Fatal("ParseCommand(\"/clear\") returned ok=false")
	}
	if cmd.Name != "clear" {
		t.Errorf("Name: got %q, want %q", cmd.Name, "clear")
	}
	if len(cmd.Args) != 0 {
		t.Errorf("Args: got %v, want empty", cmd.Args)
	}
	if cmd.Raw != "/clear" {
		t.Errorf("Raw: got %q, want %q", cmd.Raw, "/clear")
	}
}

// TestTUI041_ParseWithArgs verifies "/help foo bar" yields Args=["foo","bar"].
func TestTUI041_ParseWithArgs(t *testing.T) {
	cmd, ok := tui.ParseCommand("/help foo bar")
	if !ok {
		t.Fatal("ParseCommand(\"/help foo bar\") returned ok=false")
	}
	if cmd.Name != "help" {
		t.Errorf("Name: got %q, want %q", cmd.Name, "help")
	}
	if len(cmd.Args) != 2 {
		t.Fatalf("Args: got %v (len=%d), want 2 args", cmd.Args, len(cmd.Args))
	}
	if cmd.Args[0] != "foo" {
		t.Errorf("Args[0]: got %q, want %q", cmd.Args[0], "foo")
	}
	if cmd.Args[1] != "bar" {
		t.Errorf("Args[1]: got %q, want %q", cmd.Args[1], "bar")
	}
}

// TestTUI041_UnknownCommandReturnsHint verifies dispatching an unknown command returns CmdUnknown.
func TestTUI041_UnknownCommandReturnsHint(t *testing.T) {
	r := tui.NewCommandRegistry()
	cmd, ok := tui.ParseCommand("/doesnotexist")
	if !ok {
		t.Fatal("ParseCommand returned ok=false for valid slash command")
	}
	result := r.Dispatch(cmd)
	if result.Status != tui.CmdUnknown {
		t.Errorf("Status: got %v, want CmdUnknown", result.Status)
	}
	if result.Hint == "" {
		t.Error("Hint should be non-empty for unknown command")
	}
}

// TestTUI041_CaseInsensitive verifies "/CLEAR" lowercases to name="clear".
func TestTUI041_CaseInsensitive(t *testing.T) {
	cmd, ok := tui.ParseCommand("/CLEAR")
	if !ok {
		t.Fatal("ParseCommand(\"/CLEAR\") returned ok=false")
	}
	if cmd.Name != "clear" {
		t.Errorf("Name: got %q, want %q", cmd.Name, "clear")
	}
}

// TestTUI041_EmptyCommandFails verifies "/" and "/ " return (zero, false).
func TestTUI041_EmptyCommandFails(t *testing.T) {
	cases := []string{"/", "/ ", "/  "}
	for _, input := range cases {
		cmd, ok := tui.ParseCommand(input)
		if ok {
			t.Errorf("ParseCommand(%q): expected ok=false, got cmd=%+v", input, cmd)
		}
		if cmd.Name != "" || cmd.Raw != "" || len(cmd.Args) != 0 {
			t.Errorf("ParseCommand(%q): expected zero Command, got %+v", input, cmd)
		}
	}
}

// TestTUI041_WhitespaceOnly verifies "   " returns (zero, false).
func TestTUI041_WhitespaceOnly(t *testing.T) {
	cmd, ok := tui.ParseCommand("   ")
	if ok {
		t.Errorf("ParseCommand(\"   \"): expected ok=false, got cmd=%+v", cmd)
	}
	if cmd.Name != "" {
		t.Errorf("expected zero Command, got Name=%q", cmd.Name)
	}
}

// TestTUI041_NoSlashFails verifies "hello" (no leading slash) returns (zero, false).
func TestTUI041_NoSlashFails(t *testing.T) {
	cmd, ok := tui.ParseCommand("hello")
	if ok {
		t.Errorf("ParseCommand(\"hello\"): expected ok=false, got cmd=%+v", cmd)
	}
	if cmd.Name != "" {
		t.Errorf("expected zero Command, got Name=%q", cmd.Name)
	}
}

// TestTUI041_RegisterAndDispatch verifies a registered handler is called on dispatch.
func TestTUI041_RegisterAndDispatch(t *testing.T) {
	r := tui.NewCommandRegistry()
	called := false
	r.Register(tui.CommandEntry{
		Name:        "test",
		Description: "A test command",
		Handler: func(cmd tui.Command) tui.CommandResult {
			called = true
			return tui.CommandResult{Status: tui.CmdOK, Output: "test ran"}
		},
	})

	cmd, ok := tui.ParseCommand("/test")
	if !ok {
		t.Fatal("ParseCommand returned ok=false")
	}
	result := r.Dispatch(cmd)
	if !called {
		t.Error("handler was not called")
	}
	if result.Status != tui.CmdOK {
		t.Errorf("Status: got %v, want CmdOK", result.Status)
	}
	if result.Output != "test ran" {
		t.Errorf("Output: got %q, want %q", result.Output, "test ran")
	}
}

// TestTUI041_AliasResolution verifies dispatching by alias calls the registered handler.
func TestTUI041_AliasResolution(t *testing.T) {
	r := tui.NewCommandRegistry()
	called := false
	r.Register(tui.CommandEntry{
		Name:        "quit",
		Aliases:     []string{"q", "exit"},
		Description: "Quit the TUI",
		Handler: func(cmd tui.Command) tui.CommandResult {
			called = true
			return tui.CommandResult{Status: tui.CmdOK, Output: "quitting"}
		},
	})

	// Dispatch by alias "q"
	cmd, ok := tui.ParseCommand("/q")
	if !ok {
		t.Fatal("ParseCommand returned ok=false")
	}
	result := r.Dispatch(cmd)
	if !called {
		t.Error("alias handler was not called")
	}
	if result.Status != tui.CmdOK {
		t.Errorf("Status: got %v, want CmdOK", result.Status)
	}

	// Dispatch by alias "exit"
	called = false
	cmd2, _ := tui.ParseCommand("/exit")
	result2 := r.Dispatch(cmd2)
	if !called {
		t.Error("alias 'exit' handler was not called")
	}
	_ = result2
}

// TestTUI041_AllReturnsAllEntries verifies All() returns entries sorted by Name.
// It registers unique names that don't conflict with built-ins and verifies
// the full list is sorted.
func TestTUI041_AllReturnsAllEntries(t *testing.T) {
	r := tui.NewCommandRegistry()
	// Register entries with names that sort before/after built-ins but are unique.
	r.Register(tui.CommandEntry{Name: "zzz-last", Handler: func(cmd tui.Command) tui.CommandResult { return tui.CommandResult{} }})
	r.Register(tui.CommandEntry{Name: "aaa-first", Handler: func(cmd tui.Command) tui.CommandResult { return tui.CommandResult{} }})
	r.Register(tui.CommandEntry{Name: "mmm-middle", Handler: func(cmd tui.Command) tui.CommandResult { return tui.CommandResult{} }})

	entries := r.All()
	// Must have at least our 3 new entries plus the 10 built-ins
	if len(entries) < 3 {
		t.Fatalf("All(): got %d entries, want at least 3", len(entries))
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("All() entries not sorted by Name: %v", names)
	}
	// Verify our specific entries appear in sorted order relative to each other
	var custom []string
	for _, n := range names {
		if n == "aaa-first" || n == "mmm-middle" || n == "zzz-last" {
			custom = append(custom, n)
		}
	}
	if len(custom) != 3 {
		t.Fatalf("expected 3 custom entries in All(), found: %v", custom)
	}
	if custom[0] != "aaa-first" || custom[1] != "mmm-middle" || custom[2] != "zzz-last" {
		t.Errorf("custom entries not in sorted order: %v", custom)
	}
}

// TestTUI041_ConcurrentDispatch verifies 10 goroutines can dispatch the same command without race.
func TestTUI041_ConcurrentDispatch(t *testing.T) {
	r := tui.NewCommandRegistry()
	r.Register(tui.CommandEntry{
		Name:        "ping",
		Description: "ping",
		Handler: func(cmd tui.Command) tui.CommandResult {
			return tui.CommandResult{Status: tui.CmdOK, Output: "pong"}
		},
	})

	cmd, ok := tui.ParseCommand("/ping")
	if !ok {
		t.Fatal("ParseCommand returned ok=false")
	}

	var wg sync.WaitGroup
	results := make([]tui.CommandResult, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = r.Dispatch(cmd)
		}(i)
	}
	wg.Wait()

	for i, res := range results {
		if res.Status != tui.CmdOK {
			t.Errorf("goroutine %d: Status got %v, want CmdOK", i, res.Status)
		}
	}
}

// TestTUI041_MultiArgWithQuotedArgs verifies "/search foo bar" yields 2 args.
func TestTUI041_MultiArgWithQuotedArgs(t *testing.T) {
	cmd, ok := tui.ParseCommand("/search foo bar")
	if !ok {
		t.Fatal("ParseCommand returned ok=false")
	}
	if cmd.Name != "search" {
		t.Errorf("Name: got %q, want %q", cmd.Name, "search")
	}
	if len(cmd.Args) != 2 {
		t.Fatalf("Args: got %v (len=%d), want 2", cmd.Args, len(cmd.Args))
	}
	if cmd.Args[0] != "foo" || cmd.Args[1] != "bar" {
		t.Errorf("Args: got %v, want [foo bar]", cmd.Args)
	}
}

// TestTUI041_VisualSnapshot_80x24 writes a snapshot of the command registry help output at 80 columns.
func TestTUI041_VisualSnapshot_80x24(t *testing.T) {
	r := tui.NewCommandRegistry()
	entries := r.All()

	var sb strings.Builder
	sb.WriteString("Command Registry - 80x24\n")
	sb.WriteString(strings.Repeat("-", 40) + "\n")
	for _, e := range entries {
		line := "/" + e.Name
		if len(e.Aliases) > 0 {
			line += " (aliases: " + strings.Join(e.Aliases, ", ") + ")"
		}
		line += " — " + e.Description
		sb.WriteString(line + "\n")
	}
	output := sb.String()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-041-parser-80x24.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)

	if len(entries) == 0 {
		t.Error("registry must have built-in entries")
	}
}

// TestTUI041_VisualSnapshot_120x40 writes a snapshot at 120 columns.
func TestTUI041_VisualSnapshot_120x40(t *testing.T) {
	r := tui.NewCommandRegistry()
	entries := r.All()

	var sb strings.Builder
	sb.WriteString("Command Registry - 120x40\n")
	sb.WriteString(strings.Repeat("-", 60) + "\n")
	for _, e := range entries {
		line := "/" + e.Name
		if len(e.Aliases) > 0 {
			line += " (aliases: " + strings.Join(e.Aliases, ", ") + ")"
		}
		line += " — " + e.Description
		sb.WriteString(line + "\n")
	}
	output := sb.String()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-041-parser-120x40.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI041_VisualSnapshot_200x50 writes a snapshot at 200 columns.
func TestTUI041_VisualSnapshot_200x50(t *testing.T) {
	r := tui.NewCommandRegistry()
	entries := r.All()

	var sb strings.Builder
	sb.WriteString("Command Registry - 200x50\n")
	sb.WriteString(strings.Repeat("-", 80) + "\n")
	for _, e := range entries {
		line := "/" + e.Name
		if len(e.Aliases) > 0 {
			line += " (aliases: " + strings.Join(e.Aliases, ", ") + ")"
		}
		line += " — " + e.Description
		sb.WriteString(line + "\n")
	}
	output := sb.String()

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-041-parser-200x50.txt"
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI041_BuiltinCommandsRegistered verifies all built-in commands are present,
// have non-empty descriptions, and return CmdOK from their handlers.
func TestTUI041_BuiltinCommandsRegistered(t *testing.T) {
	r := tui.NewCommandRegistry()
	required := []string{
		"clear", "compact", "context", "export", "help", "keys",
		"model", "quit", "stats", "subagents", "tasks",
		"attach", "cancel", "doctor", "permissions", "replay", "resume", "runs",
	}
	for _, name := range required {
		entry, ok := r.Lookup(name)
		if !ok {
			t.Errorf("built-in command %q not registered", name)
			continue
		}
		if entry.Description == "" {
			t.Errorf("command %q has empty description", name)
		}
		if entry.Handler == nil {
			t.Errorf("command %q has nil handler", name)
			continue
		}
		if entry.Execute == nil {
			t.Errorf("command %q has nil execute handler", name)
			continue
		}
		cmd, _ := tui.ParseCommand("/" + name)
		result := entry.Handler(cmd)
		if result.Status != tui.CmdOK {
			t.Errorf("command %q handler: expected CmdOK, got %v", name, result.Status)
		}
	}
}

// TestTUI364_RegistryCompleteness verifies that NewCommandRegistry contains all
// commands that are handled in the Update() switch. This is the single source of
// truth for "what commands exist."
func TestTUI364_RegistryCompleteness(t *testing.T) {
	// These are the exact built-in slash commands the TUI exposes.
	knownCommands := []string{
		"add-dir", "attach", "cancel", "clear", "compact", "config", "context", "cost", "dashboard", "doctor", "export", "feedback", "fork", "help", "history", "hooks", "init", "keys",
		"model", "new", "permissions", "plugins", "profiles", "quit", "replay", "resume", "runs", "search",
		"sessions", "stats", "subagents", "tasks", "rewind", "theme", "title", "undo",
	}

	r := tui.NewCommandRegistry()
	for _, name := range knownCommands {
		if !r.IsRegistered(name) {
			t.Errorf("command %q has a switch case in Update() but is not registered in NewCommandRegistry()", name)
		}
	}

	// Also verify the registry has no extra commands that lack switch cases
	// (registry entries that are unexecutable would be misleading).
	knownCases := make(map[string]bool)
	for _, name := range knownCommands {
		knownCases[name] = true
	}
	for _, entry := range r.All() {
		if !knownCases[entry.Name] {
			t.Errorf("command %q is registered but is not part of the supported built-in set", entry.Name)
		}
	}
}

// TestTUI041_ErrorResult verifies ErrorResult() creates a CmdError result.
func TestTUI041_ErrorResult(t *testing.T) {
	result := tui.ErrorResult("something went wrong")
	if result.Status != tui.CmdError {
		t.Errorf("Status: got %v, want CmdError", result.Status)
	}
	if result.Output != "something went wrong" {
		t.Errorf("Output: got %q, want %q", result.Output, "something went wrong")
	}
}

// TestTUI041_UnknownResult verifies UnknownResult() creates a CmdUnknown result with hint.
func TestTUI041_UnknownResult(t *testing.T) {
	result := tui.UnknownResult("bogus")
	if result.Status != tui.CmdUnknown {
		t.Errorf("Status: got %v, want CmdUnknown", result.Status)
	}
	if result.Hint == "" {
		t.Error("Hint must be non-empty for unknown result")
	}
	if !strings.Contains(result.Hint, "bogus") {
		t.Errorf("Hint should reference the attempted command, got: %q", result.Hint)
	}
}

// TestTUI041_LookupNotFound verifies Lookup returns false for unknown names.
func TestTUI041_LookupNotFound(t *testing.T) {
	r := tui.NewCommandRegistry()
	_, ok := r.Lookup("doesnotexist")
	if ok {
		t.Error("Lookup should return false for unregistered name")
	}
}
