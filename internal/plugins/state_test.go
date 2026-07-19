package plugins

import (
	"path/filepath"
	"testing"
)

func TestStateStore_PersistsIndependentEnableAndTrustFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	store := NewStateStore(path)
	if err := store.RecordInstall(InstalledPlugin{Name: "remote-tools", Version: "1.0.0", Source: "https://example.test/tools.git", Remote: true}); err != nil {
		t.Fatal(err)
	}
	installed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || !installed[0].Enabled || installed[0].Trusted {
		t.Fatalf("installed = %#v; remote installs should be enabled but untrusted", installed)
	}
	if err := store.SetTrusted("remote-tools", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("remote-tools", false); err != nil {
		t.Fatal(err)
	}
	reopened := NewStateStore(path)
	installed, err = reopened.List()
	if err != nil {
		t.Fatal(err)
	}
	if installed[0].Enabled || !installed[0].Trusted {
		t.Fatalf("flags were not independently persisted: %#v", installed[0])
	}
}
