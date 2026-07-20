package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstaller_InstallsLocalBundleIntoVersionedDirectory(t *testing.T) {
	source := writeTestBundle(t, "local-tools", "1.2.3")
	installer := NewInstaller(filepath.Join(t.TempDir(), "plugins"))

	installed, err := installer.Install(source)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := filepath.Join(installer.Dir, "local-tools", "1.2.3")
	if installed.Root != want {
		t.Fatalf("installed root = %q, want %q", installed.Root, want)
	}
	if _, err := os.Stat(filepath.Join(want, ManifestFilename)); err != nil {
		t.Fatalf("installed manifest missing: %v", err)
	}
	if installed.Remote {
		t.Fatal("local install was marked remote")
	}
}

func TestNormalizeSource_GitHubShorthandAndGitURLAreRemote(t *testing.T) {
	shorthand, err := NormalizeSource("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if shorthand.URL != "https://github.com/owner/repo.git" || !shorthand.Remote {
		t.Fatalf("shorthand = %#v", shorthand)
	}
	remote, err := NormalizeSource("https://github.com/owner/repo.git")
	if err != nil || !remote.Remote {
		t.Fatalf("remote = %#v, %v", remote, err)
	}
}

func TestInstaller_RejectsSymlinksBeforePromotion(t *testing.T) {
	source := writeTestBundle(t, "unsafe", "1.0.0")
	if err := os.Symlink("/tmp", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	installer := NewInstaller(filepath.Join(t.TempDir(), "plugins"))
	if _, err := installer.Install(source); err == nil {
		t.Fatal("Install() succeeded for bundle containing a symlink")
	}
}

func TestInstaller_RejectsTraversalVersionWithoutEscapingRoot(t *testing.T) {
	source := writeTestBundle(t, "safe", "../../../evil")
	root := filepath.Join(t.TempDir(), "plugins")
	outside := filepath.Join(filepath.Dir(root), "evil")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewInstaller(root).Install(source); err == nil {
		t.Fatal("Install accepted traversal version")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("outside changed: %q %v", data, err)
	}
}

func writeTestBundle(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"schema_version":1,"name":"` + name + `","version":"` + version + `","skills":"skills"}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstaller_StagePromoteAndDiscard(t *testing.T) {
	source := writeTestBundle(t, "staged-tools", "2.0.0")
	root := filepath.Join(t.TempDir(), "plugins")
	installer := NewInstaller(root)

	// Stage fetches and validates without promoting anything into the
	// versioned layout.
	staged, err := installer.Stage(source)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if staged.Manifest.Name != "staged-tools" || staged.Manifest.Version != "2.0.0" {
		t.Fatalf("staged manifest = %+v", staged.Manifest)
	}
	if staged.Remote {
		t.Fatal("local stage was marked remote")
	}
	if _, err := os.Stat(filepath.Join(root, "staged-tools")); !os.IsNotExist(err) {
		t.Fatalf("Stage promoted into the install layout: %v", err)
	}

	// Promote moves the staged tree into <name>/<version>; Discard afterwards
	// is a no-op.
	installed, err := staged.Promote()
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	want := filepath.Join(root, "staged-tools", "2.0.0")
	if installed.Root != want {
		t.Fatalf("installed root = %q, want %q", installed.Root, want)
	}
	staged.Discard()
	if _, err := os.Stat(filepath.Join(want, ManifestFilename)); err != nil {
		t.Fatalf("Discard after Promote removed the installed bundle: %v", err)
	}

	// A discarded stage leaves nothing behind in the install root.
	again, err := installer.Stage(source)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	again.Discard()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "staged-tools" {
			t.Fatalf("Discard left residue %q in the install root", entry.Name())
		}
	}
}
