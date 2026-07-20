package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/plugins"
)

func TestPluginCLI_InstallListUpdateAndUninstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"schema_version":1,"name":"cli-tools","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runPlugin := func(args ...string) (int, string) {
		t.Helper()
		oldOut, oldErr := stdout, stderr
		t.Cleanup(func() { stdout, stderr = oldOut, oldErr })
		var out, errOut bytes.Buffer
		stdout, stderr = &out, &errOut
		code := dispatch(append([]string{"plugin"}, args...))
		return code, out.String() + errOut.String()
	}
	if code, output := runPlugin("install", source); code != 0 || !strings.Contains(output, "installed cli-tools@1.0.0") {
		t.Fatalf("install = %d, %q", code, output)
	}
	if code, output := runPlugin("list"); code != 0 || !strings.Contains(output, "enabled=true") || !strings.Contains(output, "trusted=true") {
		t.Fatalf("list = %d, %q", code, output)
	}
	if code, output := runPlugin("update", "cli-tools"); code != 0 || !strings.Contains(output, "updated cli-tools@1.0.0") {
		t.Fatalf("update = %d, %q", code, output)
	}
	if code, output := runPlugin("uninstall", "cli-tools"); code != 0 || !strings.Contains(output, "uninstalled cli-tools") {
		t.Fatalf("uninstall = %d, %q", code, output)
	}
}

func TestPluginMarketplaceCLIListsLocalIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	index := filepath.Join(t.TempDir(), "marketplace.json")
	if err := os.WriteFile(index, []byte(`{"plugins":[{"name":"demo","source":"owner/demo"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := stdout, stderr
	defer func() { stdout, stderr = oldOut, oldErr }()
	var out, errOut bytes.Buffer
	stdout = &out
	stderr = &errOut
	if code := dispatch([]string{"plugin", "marketplace", "add", "local", index}); code != 0 {
		t.Fatalf("add %d: %s", code, errOut.String())
	}
	if code := dispatch([]string{"plugin", "marketplace", "list"}); code != 0 || !strings.Contains(out.String(), "demo") {
		t.Fatalf("list %d: %s", code, out.String())
	}
}

// --- Slice 2: trust lifecycle and install-time confirmation ---

// runPluginCLI dispatches a plugin subcommand with captured output.
func runPluginCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	oldOut, oldErr := stdout, stderr
	t.Cleanup(func() { stdout, stderr = oldOut, oldErr })
	var out, errOut bytes.Buffer
	stdout, stderr = &out, &errOut
	code := dispatch(append([]string{"plugin"}, args...))
	return code, out.String() + errOut.String()
}

// setPluginInput replaces the stdin reader and terminal detection for
// confirmation prompts, restoring both at test cleanup.
func setPluginInput(t *testing.T, input string, terminal bool) {
	t.Helper()
	oldIn, oldTerm := stdin, stdinIsTerminal
	t.Cleanup(func() { stdin, stdinIsTerminal = oldIn, oldTerm })
	stdin = strings.NewReader(input)
	stdinIsTerminal = func() bool { return terminal }
}

func pluginHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".go-harness", "plugins")
}

func writeBundleFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitBundleRepo builds a bundle in a local git repository and returns its
// directory and a file:// URL, which NormalizeSource treats as a remote
// source without needing network access.
func gitBundleRepo(t *testing.T, files map[string]string) (dir, url string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	writeBundleFiles(t, dir, files)
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", "fixture")
	return dir, "file://" + dir
}

func trustedBundleNames(t *testing.T) []string {
	t.Helper()
	root := pluginHomeDir(t)
	bundles, err := plugins.TrustedBundles(root, plugins.NewStateStore(filepath.Join(root, "state.json")))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(bundles))
	for _, b := range bundles {
		names = append(names, b.Manifest.Name+"@"+b.Manifest.Version)
	}
	return names
}

func TestPluginCLI_TrustUntrustRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := t.TempDir()
	writeBundleFiles(t, source, map[string]string{
		"plugin.json":     `{"schema_version":1,"name":"cli-tools","version":"1.0.0","skills":"skills"}`,
		"skills/SKILL.md": "# cli tools",
	})
	if code, out := runPluginCLI(t, "install", source); code != 0 {
		t.Fatalf("install = %d, %q", code, out)
	}

	// Local installs start trusted; revoke first to prove untrust works.
	if code, out := runPluginCLI(t, "untrust", "cli-tools"); code != 0 || !strings.Contains(out, "untrusted cli-tools") {
		t.Fatalf("untrust = %d, %q", code, out)
	}
	if names := trustedBundleNames(t); len(names) != 0 {
		t.Fatalf("TrustedBundles after untrust = %v, want none", names)
	}
	if code, out := runPluginCLI(t, "list"); code != 0 ||
		!strings.Contains(out, "trusted=false") ||
		!strings.Contains(out, "untrusted — commands/hooks/MCP inactive") {
		t.Fatalf("list = %d, %q", code, out)
	}

	// Trust makes the bundle's executable surfaces discoverable again.
	if code, out := runPluginCLI(t, "trust", "cli-tools"); code != 0 || !strings.Contains(out, "trusted cli-tools") {
		t.Fatalf("trust = %d, %q", code, out)
	}
	if names := trustedBundleNames(t); len(names) != 1 || names[0] != "cli-tools@1.0.0" {
		t.Fatalf("TrustedBundles after trust = %v", names)
	}
}

func TestPluginCLI_TrustRequiresInstalledPlugin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code, out := runPluginCLI(t, "trust", "nope"); code == 0 || !strings.Contains(out, `plugin "nope" is not installed`) {
		t.Fatalf("trust unknown = %d, %q", code, out)
	}
	if code, out := runPluginCLI(t, "untrust"); code == 0 || !strings.Contains(out, "exactly one plugin name is required") {
		t.Fatalf("untrust without name = %d, %q", code, out)
	}
}

func TestPluginCLI_RemoteInstallDeclinedLeavesNoState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, url := gitBundleRepo(t, map[string]string{
		"plugin.json":      `{"schema_version":1,"name":"remote-tools","version":"1.0.0","skills":"skills","hooks":"hooks/hooks.json"}`,
		"skills/SKILL.md":  "# remote tools",
		"hooks/hooks.json": `{"name":"demo","event":"PostMessage","kind":"command","command":["echo","hi"]}`,
	})
	setPluginInput(t, "n\n", true)

	code, out := runPluginCLI(t, "install", url)
	if code == 0 {
		t.Fatalf("declined install succeeded: %q", out)
	}
	if !strings.Contains(out, "skills: skills") || !strings.Contains(out, "hooks: hooks/hooks.json") {
		t.Fatalf("declared surfaces not shown before confirmation: %q", out)
	}
	if !strings.Contains(out, "aborted") {
		t.Fatalf("expected abort message, got %q", out)
	}

	// Nothing executable and no state record remain.
	if _, err := os.Stat(filepath.Join(pluginHomeDir(t), "remote-tools")); !os.IsNotExist(err) {
		t.Fatalf("declined install left files: %v", err)
	}
	if code, out := runPluginCLI(t, "list"); code != 0 || !strings.Contains(out, "no installed plugin bundles") {
		t.Fatalf("list after declined install = %d, %q", code, out)
	}
}

func TestPluginCLI_RemoteInstallNonTerminalRefusesWithoutYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, url := gitBundleRepo(t, map[string]string{
		"plugin.json": `{"schema_version":1,"name":"remote-tools","version":"1.0.0"}`,
	})
	setPluginInput(t, "", false) // piped stdin, no --yes

	code, out := runPluginCLI(t, "install", url)
	if code == 0 || !strings.Contains(out, "--yes") {
		t.Fatalf("non-terminal install = %d, %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(pluginHomeDir(t), "remote-tools")); !os.IsNotExist(err) {
		t.Fatalf("refused install left files: %v", err)
	}
}

func TestPluginCLI_RemoteInstallYesFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, url := gitBundleRepo(t, map[string]string{
		"plugin.json":     `{"schema_version":1,"name":"remote-tools","version":"1.0.0","skills":"skills","mcp":"mcp.json"}`,
		"skills/SKILL.md": "# remote tools",
		"mcp.json":        `[]`,
	})
	setPluginInput(t, "", false) // non-interactive; --yes must suffice

	code, out := runPluginCLI(t, "install", "--yes", url)
	if code != 0 {
		t.Fatalf("install --yes = %d, %q", code, out)
	}
	for _, want := range []string{"skills: skills", "mcp: mcp.json", "installed remote-tools@1.0.0 (untrusted)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install --yes output missing %q: %q", want, out)
		}
	}
	if code, out := runPluginCLI(t, "list"); code != 0 ||
		!strings.Contains(out, "trusted=false") ||
		!strings.Contains(out, "untrusted — commands/hooks/MCP inactive") {
		t.Fatalf("list = %d, %q", code, out)
	}
}

func TestPluginCLI_RemoteInstallPromptAccept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, url := gitBundleRepo(t, map[string]string{
		"plugin.json": `{"schema_version":1,"name":"remote-tools","version":"1.0.0"}`,
	})
	setPluginInput(t, "y\n", true)

	if code, out := runPluginCLI(t, "install", url); code != 0 || !strings.Contains(out, "installed remote-tools@1.0.0 (untrusted)") {
		t.Fatalf("install with prompt accept = %d, %q", code, out)
	}
}

func TestPluginCLI_UpdatePreservesTrustWhenSurfacesUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, url := gitBundleRepo(t, map[string]string{
		"plugin.json":     `{"schema_version":1,"name":"remote-tools","version":"1.0.0","skills":"skills"}`,
		"skills/SKILL.md": "# v1",
	})
	if code, out := runPluginCLI(t, "install", "--yes", url); code != 0 {
		t.Fatalf("install = %d, %q", code, out)
	}
	if code, out := runPluginCLI(t, "trust", "remote-tools"); code != 0 {
		t.Fatalf("trust = %d, %q", code, out)
	}

	// New upstream commit changes content but not the declared surfaces.
	writeBundleFiles(t, repoDir, map[string]string{"skills/SKILL.md": "# v1.1"})
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", "content")

	setPluginInput(t, "", false) // no confirmation should be required
	if code, out := runPluginCLI(t, "update", "remote-tools"); code != 0 || !strings.Contains(out, "updated remote-tools@1.0.0") {
		t.Fatalf("update = %d, %q", code, out)
	}
	if names := trustedBundleNames(t); len(names) != 1 || names[0] != "remote-tools@1.0.0" {
		t.Fatalf("TrustedBundles after unchanged update = %v, want trust preserved", names)
	}
}

func TestPluginCLI_UpdateChangedSurfacesRequiresConfirmation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, url := gitBundleRepo(t, map[string]string{
		"plugin.json":     `{"schema_version":1,"name":"remote-tools","version":"1.0.0","skills":"skills"}`,
		"skills/SKILL.md": "# v1",
	})
	if code, out := runPluginCLI(t, "install", "--yes", url); code != 0 {
		t.Fatalf("install = %d, %q", code, out)
	}
	if code, out := runPluginCLI(t, "trust", "remote-tools"); code != 0 {
		t.Fatalf("trust = %d, %q", code, out)
	}

	// v1.1.0 declares a new hooks surface.
	writeBundleFiles(t, repoDir, map[string]string{
		"plugin.json":      `{"schema_version":1,"name":"remote-tools","version":"1.1.0","skills":"skills","hooks":"hooks/hooks.json"}`,
		"hooks/hooks.json": `{"name":"demo","event":"PostMessage","kind":"command","command":["echo","hi"]}`,
	})
	gitIn(t, repoDir, "add", ".")
	gitIn(t, repoDir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", "add hooks")

	// Declined: the old version stays installed and trusted.
	setPluginInput(t, "n\n", true)
	code, out := runPluginCLI(t, "update", "remote-tools")
	if code == 0 || !strings.Contains(out, "hooks: hooks/hooks.json") || !strings.Contains(out, "aborted") {
		t.Fatalf("declined update = %d, %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(pluginHomeDir(t), "remote-tools", "1.0.0")); err != nil {
		t.Fatalf("declined update removed the old version: %v", err)
	}
	root := pluginHomeDir(t)
	store := plugins.NewStateStore(filepath.Join(root, "state.json"))
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Version != "1.0.0" || !items[0].Trusted {
		t.Fatalf("state after declined update = %+v", items)
	}

	// Confirmed via --yes: the update proceeds and trust is preserved.
	if code, out := runPluginCLI(t, "update", "--yes", "remote-tools"); code != 0 || !strings.Contains(out, "updated remote-tools@1.1.0") {
		t.Fatalf("update --yes = %d, %q", code, out)
	}
	if names := trustedBundleNames(t); len(names) != 1 || names[0] != "remote-tools@1.1.0" {
		t.Fatalf("TrustedBundles after confirmed update = %v, want trust preserved at new version", names)
	}
}
