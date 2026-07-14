package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func validateWorkspaceRelativePattern(pattern string) error {
	if filepath.IsAbs(pattern) {
		return fmt.Errorf("absolute patterns are not allowed")
	}
	clean := filepath.Clean(filepath.FromSlash(pattern))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("pattern %q escapes workspace", pattern)
	}
	return nil
}

// ResolveWorkspacePath resolves a relative path against the workspace root,
// ensuring the result does not escape the workspace.
// Exported for use by tools/core and tools/deferred sub-packages.
func ResolveWorkspacePath(workspaceRoot, relativePath string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	path := relativePath
	if path == "" {
		path = "."
	}
	// Absolute paths are passed through directly.
	// This is intentional: harnessd runs in isolated container environments
	// where the agent needs access to system paths (e.g., /etc/nginx/).
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	candidate := filepath.Clean(filepath.Join(absRoot, path))
	rel, err := filepath.Rel(absRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", relativePath)
	}
	return candidate, nil
}

// NormalizeRelPath returns a workspace-relative, forward-slash path.
// Exported for use by tools/core and tools/deferred sub-packages.
func NormalizeRelPath(workspaceRoot, absPath string) string {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		absRoot = workspaceRoot
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return absPath
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

// EnsureWorkspaceRootUsable verifies that the workspace root exists, is a
// directory, and is writable. It should be called by filesystem-mutating
// tool handlers (write, edit, apply_patch) before they resolve paths against
// the workspace root. This prevents silent wrong-behaviour when the
// workspace root points to a path that does not exist on the local machine
// (e.g. a VM workspace path like /workspace when tools execute on the host).
func EnsureWorkspaceRootUsable(workspaceRoot string) error {
	if workspaceRoot == "" {
		return fmt.Errorf("workspace root is required")
	}
	fi, err := os.Stat(workspaceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace root %q does not exist", workspaceRoot)
		}
		return fmt.Errorf("workspace root %q is not accessible: %w", workspaceRoot, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("workspace root %q is not a directory", workspaceRoot)
	}
	// Check writability by attempting to create a temporary file.
	// We use a write check rather than os.IsPermission on the stat result
	// because directory write permission may differ from the owner uid.
	if err := writable(workspaceRoot); err != nil {
		return fmt.Errorf("workspace root %q is not writable: %w", workspaceRoot, err)
	}
	return nil
}

// writable returns nil if the directory at path is writable by the current
// process. It attempts to create a temporary empty file and removes it
// immediately.
func writable(dir string) error {
	tmp, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		// Translate permission errors into a friendlier message.
		if os.IsPermission(err) || isEACCES(err) {
			return fmt.Errorf("permission denied")
		}
		return err
	}
	name := tmp.Name()
	tmp.Close()
	os.Remove(name)
	return nil
}

// isEACCES returns true if err is an EACCES (permission denied) syscall error.
func isEACCES(err error) bool {
	if pe, ok := err.(*os.PathError); ok {
		if se, ok := pe.Err.(syscall.Errno); ok {
			return se == syscall.EACCES
		}
	}
	return false
}

// TODO(BUG-1 red phase): stub only — does not yet confine anything.
// EffectiveSandboxScope will resolve the per-call sandbox scope override.
func EffectiveSandboxScope(ctx context.Context, defaultScope SandboxScope) SandboxScope {
	return defaultScope
}

// TODO(BUG-1 red phase): stub only — passes every path through unchanged,
// which is exactly today's vulnerable behavior. The real implementation
// canonicalizes absPath (symlink-resolved) and verifies containment.
func ConfineWorkspacePath(scope SandboxScope, workspaceRoot string, extraAllowedRoots []string, absPath string) (string, error) {
	return absPath, nil
}

// TODO(BUG-1 red phase): stub only — delegates to the unconfined resolver.
func ResolveWorkspacePathConfined(ctx context.Context, workspaceRoot, relativePath string, defaultScope SandboxScope) (string, error) {
	return ResolveWorkspacePath(workspaceRoot, relativePath)
}
