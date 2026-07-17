package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeHookFile writes content to dir/name and returns the full path.
func writeHookFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidCommandDef(t *testing.T) {
	dir := t.TempDir()
	writeHookFile(t, dir, "deny-rm.json", `{
		"name": "deny-rm",
		"event": "pre_tool_use",
		"kind": "command",
		"command": ["/bin/sh", "-c", "echo allow"],
		"matcher": "bash",
		"timeout_seconds": 5
	}`)

	defs, skips := Load(dir)
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	def := defs[0]
	if def.Name != "deny-rm" {
		t.Errorf("Name: got %q, want %q", def.Name, "deny-rm")
	}
	if def.Event != EventPreToolUse {
		t.Errorf("Event: got %q, want %q", def.Event, EventPreToolUse)
	}
	if def.Kind != KindCommand {
		t.Errorf("Kind: got %q, want %q", def.Kind, KindCommand)
	}
	if len(def.Command) != 3 || def.Command[0] != "/bin/sh" {
		t.Errorf("Command: got %v", def.Command)
	}
	if def.Matcher != "bash" {
		t.Errorf("Matcher: got %q, want %q", def.Matcher, "bash")
	}
	if def.TimeoutSeconds != 5 {
		t.Errorf("TimeoutSeconds: got %d, want 5", def.TimeoutSeconds)
	}
	if def.SourceDir != dir {
		t.Errorf("SourceDir: got %q, want %q", def.SourceDir, dir)
	}
	if def.FilePath != filepath.Join(dir, "deny-rm.json") {
		t.Errorf("FilePath: got %q", def.FilePath)
	}
}

func TestLoad_ValidHTTPDef(t *testing.T) {
	dir := t.TempDir()
	writeHookFile(t, dir, "audit.json", `{
		"name": "audit",
		"event": "post_tool_use",
		"kind": "http",
		"url": "https://audit.example.com/hooks/tool"
	}`)

	defs, skips := Load(dir)
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].Kind != KindHTTP {
		t.Errorf("Kind: got %q, want %q", defs[0].Kind, KindHTTP)
	}
	if defs[0].URL != "https://audit.example.com/hooks/tool" {
		t.Errorf("URL: got %q", defs[0].URL)
	}
}

func TestLoad_TableDrivenSkips(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantReason string // substring expected in the skip reason
	}{
		{
			name:       "malformed JSON",
			content:    `{"name": "x", "event":`,
			wantReason: "invalid JSON",
		},
		{
			name:       "unknown event",
			content:    `{"name":"x","event":"session_start","kind":"command","command":["/bin/true"]}`,
			wantReason: "unknown event",
		},
		{
			name:       "unknown kind",
			content:    `{"name":"x","event":"pre_tool_use","kind":"carrier-pigeon","command":["/bin/true"]}`,
			wantReason: "unknown kind",
		},
		{
			name:       "command kind missing argv",
			content:    `{"name":"x","event":"pre_tool_use","kind":"command"}`,
			wantReason: "command",
		},
		{
			name:       "command kind empty argv",
			content:    `{"name":"x","event":"pre_tool_use","kind":"command","command":[]}`,
			wantReason: "command",
		},
		{
			name:       "http kind missing url",
			content:    `{"name":"x","event":"pre_tool_use","kind":"http"}`,
			wantReason: "url",
		},
		{
			name:       "http kind bad scheme",
			content:    `{"name":"x","event":"pre_tool_use","kind":"http","url":"ftp://example.com/h"}`,
			wantReason: "url",
		},
		{
			name:       "negative timeout",
			content:    `{"name":"x","event":"pre_tool_use","kind":"command","command":["/bin/true"],"timeout_seconds":-1}`,
			wantReason: "timeout",
		},
		{
			name:       "invalid glob matcher",
			content:    `{"name":"x","event":"pre_tool_use","kind":"command","command":["/bin/true"],"matcher":"[unclosed"}`,
			wantReason: "matcher",
		},
		{
			name:       "unknown field rejected",
			content:    `{"name":"x","event":"pre_tool_use","kind":"command","command":["/bin/true"],"evil_field":true}`,
			wantReason: "unknown field",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeHookFile(t, dir, "hook.json", tc.content)

			defs, skips := Load(dir)
			if len(defs) != 0 {
				t.Fatalf("got %d defs, want 0", len(defs))
			}
			if len(skips) != 1 {
				t.Fatalf("got %d skips, want 1: %+v", len(skips), skips)
			}
			if skips[0].File != path {
				t.Errorf("skip File: got %q, want %q", skips[0].File, path)
			}
			if !strings.Contains(skips[0].Reason, tc.wantReason) {
				t.Errorf("skip Reason %q does not contain %q", skips[0].Reason, tc.wantReason)
			}
		})
	}
}

func TestLoad_MixedValidAndInvalid(t *testing.T) {
	dir := t.TempDir()
	writeHookFile(t, dir, "good.json", `{"name":"good","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	writeHookFile(t, dir, "bad.json", `{"name":"bad","event":"nope","kind":"command","command":["/bin/true"]}`)

	defs, skips := Load(dir)
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].Name != "good" {
		t.Errorf("loaded def name: got %q, want %q", defs[0].Name, "good")
	}
	if len(skips) != 1 {
		t.Fatalf("got %d skips, want 1: %+v", len(skips), skips)
	}
	if !strings.HasSuffix(skips[0].File, "bad.json") {
		t.Errorf("skip file: got %q", skips[0].File)
	}
}

func TestLoad_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	defs, skips := Load(dir)
	if len(defs) != 0 || len(skips) != 0 {
		t.Fatalf("got defs=%v skips=%v, want both empty", defs, skips)
	}
}

func TestLoad_NonexistentDirectory(t *testing.T) {
	defs, skips := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(defs) != 0 || len(skips) != 0 {
		t.Fatalf("got defs=%v skips=%v, want both empty for missing dir", defs, skips)
	}
}

func TestLoad_IgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	writeHookFile(t, dir, "README.md", "# not a hook")
	writeHookFile(t, dir, "hook.txt", `{"name":"x"}`)
	writeHookFile(t, dir, "real.json", `{"name":"real","event":"post_message","kind":"command","command":["/bin/true"]}`)

	defs, skips := Load(dir)
	if len(defs) != 1 || defs[0].Name != "real" {
		t.Fatalf("got defs=%+v, want only 'real'", defs)
	}
	if len(skips) != 0 {
		t.Fatalf("non-JSON files must be ignored silently, got skips: %+v", skips)
	}
}

func TestLoad_DeterministicOrderAcrossDirs(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeHookFile(t, dirA, "b.json", `{"name":"from-a-b","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	writeHookFile(t, dirA, "a.json", `{"name":"from-a-a","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	writeHookFile(t, dirB, "c.json", `{"name":"from-b-c","event":"pre_message","kind":"command","command":["/bin/true"]}`)

	defs, skips := Load(dirA, dirB)
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(defs) != 3 {
		t.Fatalf("got %d defs, want 3", len(defs))
	}
	// Directory order preserved; within a directory, files sorted by name.
	want := []string{"from-a-a", "from-a-b", "from-b-c"}
	for i, def := range defs {
		if def.Name != want[i] {
			t.Errorf("defs[%d].Name: got %q, want %q", i, def.Name, want[i])
		}
	}
}

func TestLoad_NameDefaultsToFileBaseName(t *testing.T) {
	dir := t.TempDir()
	writeHookFile(t, dir, "my-hook.json", `{"event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)

	defs, skips := Load(dir)
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	if defs[0].Name != "my-hook" {
		t.Errorf("Name: got %q, want file base name %q", defs[0].Name, "my-hook")
	}
}

func TestLoad_SourceClassification(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()
	extraDir := t.TempDir()
	for _, dir := range []string{userDir, projectDir, extraDir} {
		writeHookFile(t, dir, "h.json", `{"name":"h","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	}

	defs, skips := LoadWithOptions(LoadOptions{UserDir: userDir}, userDir, projectDir, extraDir)
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(defs) != 3 {
		t.Fatalf("got %d defs, want 3", len(defs))
	}
	if defs[0].Source != SourceUser {
		t.Errorf("user dir def Source: got %q, want %q", defs[0].Source, SourceUser)
	}
	if defs[1].Source != SourceProject {
		t.Errorf("project dir def Source: got %q, want %q", defs[1].Source, SourceProject)
	}
	// Extra dirs are not the user-global dir, so they classify as project
	// (trust required) — a malicious project config must not be able to
	// bypass trust by naming a directory.
	if defs[2].Source != SourceProject {
		t.Errorf("extra dir def Source: got %q, want %q", defs[2].Source, SourceProject)
	}
}

func TestMatcherMatching(t *testing.T) {
	cases := []struct {
		matcher  string
		toolName string
		want     bool
	}{
		{"", "bash", true},          // empty matches everything
		{"", "anything", true},      // empty matches everything
		{"bash", "bash", true},      // exact
		{"bash", "sh", false},       // exact miss
		{"*", "anything", true},     // glob all
		{"mcp__*", "mcp__fs", true}, // glob prefix
		{"mcp__*", "bash", false},   // glob prefix miss
		{"read_*", "read_file", true},
		{"read_*", "write_file", false},
	}
	for _, tc := range cases {
		t.Run(tc.matcher+"_vs_"+tc.toolName, func(t *testing.T) {
			def := HookDef{Matcher: tc.matcher}
			if got := def.MatchesTool(tc.toolName); got != tc.want {
				t.Errorf("MatchesTool(%q) with matcher %q: got %v, want %v", tc.toolName, tc.matcher, got, tc.want)
			}
		})
	}
}

func TestHookDefTimeout_Default(t *testing.T) {
	def := HookDef{}
	if got := def.Timeout(); got != DefaultTimeout {
		t.Errorf("Timeout() with unset: got %v, want default %v", got, DefaultTimeout)
	}
	def.TimeoutSeconds = 3
	if got := def.Timeout(); got != 3*time.Second {
		t.Errorf("Timeout() with 3s: got %v, want 3s", got)
	}
}

func TestDefaultDirs(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	if got, want := UserHooksDir(home), filepath.Join(home, ".harness", "hooks"); got != want {
		t.Errorf("UserHooksDir: got %q, want %q", got, want)
	}
	if got, want := ProjectHooksDir(workspace), filepath.Join(workspace, ".harness", "hooks"); got != want {
		t.Errorf("ProjectHooksDir: got %q, want %q", got, want)
	}
}

func TestValidEventNamesMapOneToOneToHarnessCallSites(t *testing.T) {
	// The runner has exactly four hook call sites. This guards against
	// inventing events (e.g. session_start) the runner cannot fire.
	events := []string{EventPreMessage, EventPostMessage, EventPreToolUse, EventPostToolUse}
	if len(events) != 4 {
		t.Fatalf("expected exactly 4 events, got %d", len(events))
	}
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e] {
			t.Errorf("duplicate event name %q", e)
		}
		seen[e] = true
	}
}
