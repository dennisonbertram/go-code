package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBundle_ValidatesDeclaredLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
  "schema_version": 1,
  "name": "example-tools",
  "version": "1.2.3",
  "description": "Example bundle",
  "skills": "skills",
  "commands": "commands",
  "agents": "agents",
  "hooks": "hooks/hooks.json",
  "mcp": ".mcp.json"
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"skills", "commands", "agents"} {
		if err := os.Mkdir(filepath.Join(dir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"hooks/hooks.json", ".mcp.json"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(`[]`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	bundle, err := LoadBundle(dir)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	if bundle.Manifest.Name != "example-tools" || bundle.Manifest.Version != "1.2.3" {
		t.Fatalf("manifest = %#v", bundle.Manifest)
	}
	if bundle.SkillsDir != filepath.Join(dir, "skills") || bundle.HooksPath != filepath.Join(dir, "hooks", "hooks.json") {
		t.Fatalf("bundle layout = %#v", bundle)
	}
}

func TestLoadBundle_RejectsTraversalAndMissingDeclaredContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"schema_version":1,"name":"bad","version":"1.0.0","skills":"../outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(dir); err == nil {
		t.Fatal("LoadBundle() succeeded for traversal path")
	}

	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"schema_version":1,"name":"bad","version":"1.0.0","skills":"skills"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(dir); err == nil {
		t.Fatal("LoadBundle() succeeded for a missing declared directory")
	}
}

func TestManifestValidateRejectsTraversalNameAndVersion(t *testing.T) {
	for _, manifest := range []Manifest{{SchemaVersion: 1, Name: "../bad", Version: "1"}, {SchemaVersion: 1, Name: "good", Version: "../bad"}} {
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate accepted %#v", manifest)
		}
	}
}
