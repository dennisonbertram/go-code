package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnabledBundles_OnlyReturnsEnabledInstalledRoots(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"enabled", "disabled"} {
		path := filepath.Join(root, name, "1.0.0")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, ManifestFilename), []byte(`{"schema_version":1,"name":"`+name+`","version":"1.0.0"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStateStore(filepath.Join(root, "state.json"))
	if err := store.RecordInstall(InstalledPlugin{Name: "enabled", Version: "1.0.0", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInstall(InstalledPlugin{Name: "disabled", Version: "1.0.0", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("disabled", false); err != nil {
		t.Fatal(err)
	}
	bundles, err := EnabledBundles(root, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].Manifest.Name != "enabled" {
		t.Fatalf("bundles = %#v", bundles)
	}
}

func TestTrustedBundles_GatesMCPUntilExplicitTrust(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "remote", "1.0.0")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ManifestFilename), []byte(`{"schema_version":1,"name":"remote","version":"1.0.0","mcp":".mcp.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".mcp.json"), []byte(`[{"name":"safe","command":"echo"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStateStore(filepath.Join(root, "state.json"))
	if err := store.RecordInstall(InstalledPlugin{Name: "remote", Version: "1.0.0", Remote: true}); err != nil {
		t.Fatal(err)
	}
	bundles, err := TrustedBundles(root, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 0 {
		t.Fatalf("untrusted bundles = %#v", bundles)
	}
	if err := store.SetTrusted("remote", true); err != nil {
		t.Fatal(err)
	}
	bundles, err = TrustedBundles(root, store)
	if err != nil {
		t.Fatal(err)
	}
	servers, err := MCPServers(bundles)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "safe" {
		t.Fatalf("servers = %#v", servers)
	}
}
