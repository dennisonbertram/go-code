package main

import (
	"os"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/plugins"
)

func TestRegisterTrustedPluginHooksOnlyLoadsTrustedBundles(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, "remote", "1.0.0", "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(hooksDir), "plugin.json"), []byte(`{"schema_version":1,"name":"remote","version":"1.0.0","hooks":"hooks/hooks.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(`{"name":"plugin-hook","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := plugins.NewStateStore(filepath.Join(root, "state.json"))
	if err := store.RecordInstall(plugins.InstalledPlugin{Name: "remote", Version: "1.0.0", Remote: true}); err != nil {
		t.Fatal(err)
	}
	cfg := harness.RunnerConfig{}
	registerTrustedPluginHooks(root, store, &cfg)
	if len(cfg.PreToolUseHooks) != 0 {
		t.Fatal("untrusted plugin hook was registered")
	}
	if err := store.SetTrusted("remote", true); err != nil {
		t.Fatal(err)
	}
	registerTrustedPluginHooks(root, store, &cfg)
	if len(cfg.PreToolUseHooks) != 1 {
		t.Fatalf("trusted hooks = %d", len(cfg.PreToolUseHooks))
	}
}
