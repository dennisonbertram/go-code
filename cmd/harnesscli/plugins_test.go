package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
