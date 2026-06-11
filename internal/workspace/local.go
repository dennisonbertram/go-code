package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// LocalWorkspace is a directory-based workspace implementation.
// It creates a subdirectory under a base path and uses an externally-running
// harnessd instance (no process management). Suitable for development and
// single-agent use.
type LocalWorkspace struct {
	harnessURL string
	basePath   string
	id         string
	path       string // set after Provision
}

// NewLocal returns a new LocalWorkspace configured with the given harnessURL
// and basePath. Either value may be overridden at Provision time via opts.
func NewLocal(harnessURL, basePath string) *LocalWorkspace {
	return &LocalWorkspace{
		harnessURL: harnessURL,
		basePath:   basePath,
	}
}

// Provision creates the workspace directory at <basePath>/<id>.
// The basePath is taken from opts.BaseDir (if non-empty) or the value set at
// construction time or os.TempDir() as a last resort.
// The harnessURL is taken from opts.Env["HARNESS_URL"] (if non-empty) or the
// value set at construction time or defaultHarnessURL.
// It returns ErrInvalidID if opts.ID is empty.
func (w *LocalWorkspace) Provision(_ context.Context, opts Options) error {
	if opts.ID == "" {
		return ErrInvalidID
	}

	// Resolve harnessURL: opts env > constructor value > default.
	if url, ok := opts.Env["HARNESS_URL"]; ok && url != "" {
		w.harnessURL = url
	} else if w.harnessURL == "" {
		w.harnessURL = defaultHarnessURL
	}

	// Resolve basePath: opts.BaseDir > constructor value > os.TempDir().
	if opts.BaseDir != "" {
		w.basePath = opts.BaseDir
	} else if w.basePath == "" {
		w.basePath = os.TempDir()
	}

	w.id = opts.ID
	w.path = filepath.Join(w.basePath, w.id)

	if err := os.MkdirAll(w.path, 0o755); err != nil {
		return err
	}

	// Write harness.toml if a config was provided.
	if opts.ConfigTOML != "" {
		cfgPath := filepath.Join(w.path, "harness.toml")
		if err := os.WriteFile(cfgPath, []byte(opts.ConfigTOML), 0o600); err != nil {
			return fmt.Errorf("workspace: write harness.toml: %w", err)
		}
	}

	return nil
}

// HarnessURL returns the HTTP endpoint of the harnessd instance.
// Returns the default URL if Provision has not been called yet and no URL was
// set at construction time.
func (w *LocalWorkspace) HarnessURL() string {
	if w.harnessURL == "" {
		return defaultHarnessURL
	}
	return w.harnessURL
}

// WorkspacePath returns the filesystem path of the workspace root.
// Returns an empty string if Provision has not been called.
func (w *LocalWorkspace) WorkspacePath() string {
	return w.path
}

// WaitReady is a no-op for local workspaces — there is no inner harnessd to
// wait for. The local harnessd is expected to be running externally.
func (w *LocalWorkspace) WaitReady(_ context.Context) error {
	return nil
}

// Destroy removes the workspace directory. It is a no-op (returns nil) if the
// workspace has not been provisioned.
func (w *LocalWorkspace) Destroy(_ context.Context) error {
	if w.path == "" {
		return nil
	}
	return os.RemoveAll(w.path)
}

func init() {
	// Ignore the error — the default registry panics on duplicate registration
	// only in tests that share the default registry. In normal usage this runs
	// exactly once.
	_ = Register("local", func() Workspace {
		return &LocalWorkspace{}
	})
}
