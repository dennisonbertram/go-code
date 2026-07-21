package plugins

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var githubShorthandRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Source is a normalized bundle source. Remote sources are never trusted by
// default; callers retain this fact in their installed state. Zip sources are
// fetched and extracted instead of git-cloned or copied.
type Source struct {
	URL    string
	Remote bool
	Zip    bool
}

// NormalizeSource accepts a local path (directory or zip file), git URL,
// zip URL, GitHub archive URL, or owner/repo shorthand.
func NormalizeSource(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Source{}, fmt.Errorf("plugin source is required")
	}
	if githubShorthandRE.MatchString(raw) {
		return Source{URL: "https://github.com/" + raw + ".git", Remote: true}, nil
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return Source{URL: raw, Remote: true, Zip: isZipSource(raw)}, nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return Source{}, fmt.Errorf("resolve local plugin source: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Source{}, fmt.Errorf("local plugin source: %w", err)
	}
	if info.IsDir() {
		return Source{URL: abs}, nil
	}
	if isZipSource(raw) {
		return Source{URL: abs, Zip: true}, nil
	}
	return Source{}, fmt.Errorf("local plugin source %q is not a directory", raw)
}

// isZipSource reports whether raw names a zip archive: a .zip suffix, or a
// GitHub /archive/ URL (which serves a zip even without the suffix).
func isZipSource(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.HasSuffix(lower, ".zip") {
		return true
	}
	return strings.Contains(lower, "github.com/") && strings.Contains(lower, "/archive/")
}

// InstalledBundle is an installed bundle plus source trust-boundary metadata.
type InstalledBundle struct {
	*Bundle
	Source Source
	Remote bool
}

// Installer installs bundle trees below Dir as <name>/<version>.
type Installer struct{ Dir string }

func NewInstaller(dir string) *Installer { return &Installer{Dir: dir} }

// StagedBundle is a fetched, validated bundle tree in a private staging
// directory under the install root. Callers inspect the declared surfaces
// (for example, to confirm a remote install) and then either Promote the
// bundle into the versioned install layout or Discard it.
type StagedBundle struct {
	*Bundle
	Source Source
	Remote bool

	installRoot string
	stage       string
}

// Stage fetches rawSource into a private staging directory, rejects symlinks,
// and validates the manifest — without promoting anything into the versioned
// install layout and without executing repository content. The caller must
// Promote or Discard the result.
func (i *Installer) Stage(rawSource string) (*StagedBundle, error) {
	source, err := NormalizeSource(rawSource)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(i.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create plugin directory: %w", err)
	}
	stage, err := os.MkdirTemp(i.Dir, ".install-")
	if err != nil {
		return nil, fmt.Errorf("create install staging directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	if source.Zip {
		if err := fetchAndExtractZip(source, stage); err != nil {
			return nil, err
		}
	} else if source.Remote {
		cmd := exec.Command("git", "clone", "--depth", "1", "--", source.URL, stage)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("clone plugin source: %w: %s", err, strings.TrimSpace(string(output)))
		}
	} else if err := copyTree(source.URL, stage); err != nil {
		return nil, fmt.Errorf("copy plugin source: %w", err)
	}
	if err := rejectSymlinks(stage); err != nil {
		return nil, err
	}
	bundle, err := LoadBundle(stage)
	if err != nil {
		return nil, fmt.Errorf("validate plugin bundle: %w", err)
	}
	keep = true
	return &StagedBundle{Bundle: bundle, Source: source, Remote: source.Remote, installRoot: i.Dir, stage: stage}, nil
}

// Promote atomically moves the staged tree into <root>/<name>/<version> and
// re-validates the installed copy.
func (s *StagedBundle) Promote() (*InstalledBundle, error) {
	destination := filepath.Join(s.installRoot, s.Manifest.Name, s.Manifest.Version)
	if err := containedPath(s.installRoot, destination); err != nil {
		return nil, fmt.Errorf("plugin destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return nil, fmt.Errorf("create plugin destination: %w", err)
	}
	if err := os.RemoveAll(destination); err != nil {
		return nil, fmt.Errorf("replace plugin version: %w", err)
	}
	if err := os.Rename(s.stage, destination); err != nil {
		return nil, fmt.Errorf("promote plugin bundle: %w", err)
	}
	installed, err := LoadBundle(destination)
	if err != nil {
		return nil, fmt.Errorf("re-read installed plugin: %w", err)
	}
	return &InstalledBundle{Bundle: installed, Source: s.Source, Remote: s.Remote}, nil
}

// Discard removes the staged tree. It is a no-op after a successful Promote,
// because the staging directory has been renamed into the install layout.
func (s *StagedBundle) Discard() {
	_ = os.RemoveAll(s.stage)
}

// Install copies or clones a source into a private temporary directory,
// validates it without executing repository content, then atomically promotes
// it into the versioned install directory. It is Stage followed by Promote;
// callers that need to review declared surfaces before promotion use Stage.
func (i *Installer) Install(rawSource string) (*InstalledBundle, error) {
	staged, err := i.Stage(rawSource)
	if err != nil {
		return nil, err
	}
	defer staged.Discard()
	return staged.Promote()
}

func containedPath(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes plugins root")
	}
	return nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %q is not allowed", rel)
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func rejectSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin bundle contains forbidden symlink %q", path)
		}
		return nil
	})
}

// fetchAndExtractZip retrieves a zip source into dest: over HTTP for remote
// sources, from the filesystem for local ones. Every entry name is validated
// before anything is written — absolute paths, .. escapes, backslash paths,
// and symlink entries are rejected — and a single shared top-level directory
// (the GitHub archive convention) is stripped so the bundle root lands at
// dest. Errors name the source.
func fetchAndExtractZip(source Source, dest string) error {
	data, err := readZipSource(source)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open plugin zip %s: %w", source.URL, err)
	}
	for _, f := range zr.File {
		if err := checkZipEntryName(f.Name); err != nil {
			return fmt.Errorf("plugin zip %s: %w", source.URL, err)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin zip %s: symlink entry %q is not allowed", source.URL, f.Name)
		}
	}
	prefix := singleTopLevelDir(zr)
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" {
			continue
		}
		target := filepath.Join(dest, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("plugin zip %s: %w", source.URL, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("plugin zip %s: %w", source.URL, err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("plugin zip %s: open entry %q: %w", source.URL, f.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("plugin zip %s: %w", source.URL, err)
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rcErr := rc.Close()
		if copyErr != nil {
			return fmt.Errorf("plugin zip %s: extract entry %q: %w", source.URL, f.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("plugin zip %s: %w", source.URL, closeErr)
		}
		if rcErr != nil {
			return fmt.Errorf("plugin zip %s: %w", source.URL, rcErr)
		}
	}
	return nil
}

// readZipSource loads the zip archive bytes for a source, over HTTP for
// remote URLs and from disk for local files.
func readZipSource(source Source) ([]byte, error) {
	if !source.Remote {
		data, err := os.ReadFile(source.URL)
		if err != nil {
			return nil, fmt.Errorf("read plugin zip %s: %w", source.URL, err)
		}
		return data, nil
	}
	resp, err := http.Get(source.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch plugin zip %s: %w", source.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch plugin zip %s: %s", source.URL, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read plugin zip %s: %w", source.URL, err)
	}
	return data, nil
}

// checkZipEntryName rejects entries that could escape the extraction
// directory or behave non-portably: absolute paths, .. elements, and
// backslash separators.
func checkZipEntryName(name string) error {
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return fmt.Errorf("zip entry %q: absolute paths are not allowed", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("zip entry %q: path escapes the bundle root", name)
		}
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("zip entry %q: backslash paths are not allowed", name)
	}
	return nil
}

// singleTopLevelDir returns the shared "<dir>/" prefix when every entry in
// the archive lives under one top-level directory (the GitHub archive
// convention), or "" when entries sit at the archive root or under multiple
// top-level names.
func singleTopLevelDir(zr *zip.Reader) string {
	prefix := ""
	for _, f := range zr.File {
		seg, rest, found := strings.Cut(f.Name, "/")
		if !found {
			return ""
		}
		if rest != "" && prefix != "" && seg+"/" != prefix {
			return ""
		}
		if prefix == "" {
			prefix = seg + "/"
		}
	}
	return prefix
}
