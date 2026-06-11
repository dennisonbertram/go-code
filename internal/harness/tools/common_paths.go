package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// EnsureWorkspaceRootUsable returns an error if the configured workspace root
// does not exist, is not a directory, or is not writable. This prevents tools
// from silently creating a missing configured root (e.g. /workspace from a
// VM-mode workspace that shouldn't exist on the host) and landing files in
// the wrong place.
func EnsureWorkspaceRootUsable(workspaceRoot string) error {
	if workspaceRoot == "" {
		return fmt.Errorf("workspace root is required")
	}
	info, err := os.Stat(workspaceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace root %q does not exist", workspaceRoot)
		}
		return fmt.Errorf("workspace root %q: %w", workspaceRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace root %q is not a directory", workspaceRoot)
	}
	f, err := os.CreateTemp(workspaceRoot, ".writable-check-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("workspace root %q is not writable", workspaceRoot)
		}
		return fmt.Errorf("workspace root %q writability check: %w", workspaceRoot, err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
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
