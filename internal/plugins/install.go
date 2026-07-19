package plugins

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var githubShorthandRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Source is a normalized bundle source. Remote sources are never trusted by
// default; callers retain this fact in their installed state.
type Source struct {
	URL    string
	Remote bool
}

// NormalizeSource accepts a local path, git URL, or owner/repo shorthand.
func NormalizeSource(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Source{}, fmt.Errorf("plugin source is required")
	}
	if githubShorthandRE.MatchString(raw) {
		return Source{URL: "https://github.com/" + raw + ".git", Remote: true}, nil
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return Source{URL: raw, Remote: true}, nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return Source{}, fmt.Errorf("resolve local plugin source: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Source{}, fmt.Errorf("local plugin source: %w", err)
	}
	if !info.IsDir() {
		return Source{}, fmt.Errorf("local plugin source %q is not a directory", raw)
	}
	return Source{URL: abs}, nil
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

// Install copies or clones a source into a private temporary directory,
// validates it without executing repository content, then atomically promotes
// it into the versioned install directory.
func (i *Installer) Install(rawSource string) (*InstalledBundle, error) {
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
	defer os.RemoveAll(stage)
	if source.Remote {
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
	destination := filepath.Join(i.Dir, bundle.Manifest.Name, bundle.Manifest.Version)
	if err := containedPath(i.Dir, destination); err != nil {
		return nil, fmt.Errorf("plugin destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return nil, fmt.Errorf("create plugin destination: %w", err)
	}
	if err := os.RemoveAll(destination); err != nil {
		return nil, fmt.Errorf("replace plugin version: %w", err)
	}
	if err := os.Rename(stage, destination); err != nil {
		return nil, fmt.Errorf("promote plugin bundle: %w", err)
	}
	installed, err := LoadBundle(destination)
	if err != nil {
		return nil, fmt.Errorf("re-read installed plugin: %w", err)
	}
	return &InstalledBundle{Bundle: installed, Source: source, Remote: source.Remote}, nil
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
