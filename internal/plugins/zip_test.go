package plugins

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildZip returns archive bytes containing the given files. Names in
// symlinks become symlink entries pointing at /tmp/target.
func buildZip(t *testing.T, entries map[string]string, symlinks ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range symlinks {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(0o755 | os.ModeSymlink)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("/tmp/target")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZipFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveZip(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNormalizeSource_ZipSources(t *testing.T) {
	t.Run("zip URL is a remote zip", func(t *testing.T) {
		src, err := NormalizeSource("https://example.com/bundles/tools.zip")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Remote || !src.Zip {
			t.Fatalf("source = %#v, want remote zip", src)
		}
	})

	t.Run("GitHub archive URL is a remote zip", func(t *testing.T) {
		src, err := NormalizeSource("https://github.com/owner/repo/archive/refs/heads/main.zip")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Remote || !src.Zip {
			t.Fatalf("source = %#v, want remote zip", src)
		}
	})

	t.Run("GitHub archive URL without suffix is a remote zip", func(t *testing.T) {
		src, err := NormalizeSource("https://github.com/owner/repo/archive/refs/heads/main")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Remote || !src.Zip {
			t.Fatalf("source = %#v, want remote zip", src)
		}
	})

	t.Run("git URL is not a zip", func(t *testing.T) {
		src, err := NormalizeSource("https://github.com/owner/repo.git")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Remote || src.Zip {
			t.Fatalf("source = %#v, want remote git", src)
		}
	})

	t.Run("owner/repo shorthand is not a zip", func(t *testing.T) {
		src, err := NormalizeSource("owner/repo")
		if err != nil {
			t.Fatal(err)
		}
		if src.Zip {
			t.Fatalf("source = %#v, want git shorthand", src)
		}
	})

	t.Run("local zip file is a non-remote zip", func(t *testing.T) {
		path := writeZipFile(t, buildZip(t, map[string]string{"plugin.json": "{}"}))
		src, err := NormalizeSource(path)
		if err != nil {
			t.Fatal(err)
		}
		if src.Remote || !src.Zip {
			t.Fatalf("source = %#v, want local zip", src)
		}
	})

	t.Run("local directory is not a zip", func(t *testing.T) {
		src, err := NormalizeSource(writeTestBundle(t, "dir-tools", "1.0.0"))
		if err != nil {
			t.Fatal(err)
		}
		if src.Remote || src.Zip {
			t.Fatalf("source = %#v, want local dir", src)
		}
	})

	t.Run("local non-zip file is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tools.tar")
		if err := os.WriteFile(path, []byte("nope"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NormalizeSource(path); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("NormalizeSource(local tar) error = %v", err)
		}
	})
}

func TestInstaller_InstallsLocalZipFile(t *testing.T) {
	path := writeZipFile(t, buildZip(t, map[string]string{
		"plugin.json":     `{"schema_version":1,"name":"zip-tools","version":"1.0.0","skills":"skills"}`,
		"skills/SKILL.md": "# zip tools",
	}))
	root := filepath.Join(t.TempDir(), "plugins")

	installed, err := NewInstaller(root).Install(path)
	if err != nil {
		t.Fatalf("Install(local zip) error = %v", err)
	}
	if installed.Remote {
		t.Fatal("local zip install was marked remote")
	}
	want := filepath.Join(root, "zip-tools", "1.0.0")
	if installed.Root != want {
		t.Fatalf("installed root = %q, want %q", installed.Root, want)
	}
	if installed.SkillsDir == "" {
		t.Fatal("expected skills surface to resolve from the extracted zip")
	}
	if _, err := os.Stat(filepath.Join(want, "skills", "SKILL.md")); err != nil {
		t.Fatalf("extracted skill missing: %v", err)
	}
}

func TestInstaller_InstallsGitHubStyleZipOverHTTP(t *testing.T) {
	srv := serveZip(t, buildZip(t, map[string]string{
		"repo-main/":                 "",
		"repo-main/plugin.json":      `{"schema_version":1,"name":"gh-tools","version":"2.1.0","skills":"skills","hooks":"hooks/hooks.json"}`,
		"repo-main/skills/SKILL.md":  "# gh tools",
		"repo-main/hooks/hooks.json": `{"name":"demo","event":"PostMessage","kind":"command","command":["echo"]}`,
	}))
	root := filepath.Join(t.TempDir(), "plugins")

	installed, err := NewInstaller(root).Install(srv.URL + "/archive/refs/heads/main.zip")
	if err != nil {
		t.Fatalf("Install(github archive zip) error = %v", err)
	}
	if !installed.Remote {
		t.Fatal("zip URL install was not marked remote")
	}
	// The single top-level directory is stripped: the bundle root is the
	// versioned install dir itself.
	want := filepath.Join(root, "gh-tools", "2.1.0")
	if installed.Root != want {
		t.Fatalf("installed root = %q, want %q", installed.Root, want)
	}
	if _, err := os.Stat(filepath.Join(want, ManifestFilename)); err != nil {
		t.Fatalf("manifest missing after prefix strip: %v", err)
	}
	if installed.SkillsDir == "" || installed.HooksPath == "" {
		t.Fatalf("declared surfaces did not resolve: %+v", installed.Bundle)
	}
}

func TestInstaller_RejectsZipTraversalEntries(t *testing.T) {
	cases := map[string]string{
		"dotdot after prefix": "repo-main/../evil.txt",
		"absolute path":       "/etc/evil.txt",
		"nested dotdot":       "repo-main/sub/../../evil.txt",
		"backslash path":      `repo-main\evil.txt`,
	}
	for name, badEntry := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeZipFile(t, buildZip(t, map[string]string{
				"repo-main/plugin.json": `{"schema_version":1,"name":"evil-zip","version":"1.0.0"}`,
				badEntry:                "pwned",
			}))
			root := filepath.Join(t.TempDir(), "plugins")
			_, err := NewInstaller(root).Install(path)
			if err == nil {
				t.Fatalf("Install accepted zip with entry %q", badEntry)
			}
			if !strings.Contains(err.Error(), "evil.txt") {
				t.Fatalf("error does not name the offending entry %q: %v", badEntry, err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "evil-zip")); !os.IsNotExist(statErr) {
				t.Fatalf("rejected zip left files: %v", statErr)
			}
		})
	}
}

func TestInstaller_RejectsZipSymlinkEntries(t *testing.T) {
	path := writeZipFile(t, buildZip(t, map[string]string{
		"plugin.json": `{"schema_version":1,"name":"link-zip","version":"1.0.0"}`,
	}, "escape-link"))
	root := filepath.Join(t.TempDir(), "plugins")

	_, err := NewInstaller(root).Install(path)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Install(zip with symlink) error = %v, want symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "link-zip")); !os.IsNotExist(statErr) {
		t.Fatalf("rejected zip left files: %v", statErr)
	}
}

func TestInstaller_CorruptZipNamesTheSource(t *testing.T) {
	t.Run("local garbage file", func(t *testing.T) {
		path := writeZipFile(t, []byte("this is not a zip archive at all"))
		_, err := NewInstaller(filepath.Join(t.TempDir(), "plugins")).Install(path)
		if err == nil || !strings.Contains(err.Error(), "bundle.zip") {
			t.Fatalf("corrupt zip error = %v, want it to name the source", err)
		}
	})

	t.Run("HTTP non-200 names the URL and status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		}))
		defer srv.Close()
		url := srv.URL + "/missing.zip"
		_, err := NewInstaller(filepath.Join(t.TempDir(), "plugins")).Install(url)
		if err == nil || !strings.Contains(err.Error(), url) || !strings.Contains(err.Error(), "404") {
			t.Fatalf("HTTP zip error = %v, want it to name %q and the status", err, url)
		}
	})
}
