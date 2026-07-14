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

// EffectiveSandboxScope resolves the sandbox scope to enforce for a single
// tool invocation: the per-call context override set by the step engine
// (tools.WithSandboxScope, see runner_step_engine.go) takes precedence over
// the scope the tool catalog was built with (defaultScope). This mirrors the
// pattern already used by JobManager.sandboxScopeForContext for the bash
// tool, so file tools and bash tools honor the same override semantics.
func EffectiveSandboxScope(ctx context.Context, defaultScope SandboxScope) SandboxScope {
	if scope, ok := SandboxScopeFromContext(ctx); ok && scope != "" {
		return scope
	}
	return defaultScope
}

// ConfineWorkspacePath enforces that absPath lies inside workspaceRoot (or one
// of the explicitly allowlisted extraAllowedRoots) once symlinks are
// resolved, and returns the (unresolved) absPath for I/O when the check
// passes.
//
// This is the enforcement point for SandboxScopeWorkspace. It is a no-op
// (existing pass-through behavior) for any other scope: SandboxScopeLocal and
// SandboxScopeUnrestricted are the existing, explicit opt-in mechanism a
// caller uses when it legitimately needs to reach outside the workspace, and
// this function does not second-guess that choice.
//
// The check is performed on the canonicalized FINAL path, not on the input
// string:
//   - Absolute-path escapes and "../" traversal are caught because the
//     canonical candidate is compared against the canonical root by path
//     COMPONENT (filepath.Rel), never by string-prefix matching — so a
//     sibling directory that merely shares a name prefix with the root
//     (e.g. "/tmp/ws-evil" vs root "/tmp/ws") cannot pass.
//   - Symlink escapes (a symlink inside the workspace whose target lives
//     outside it) are caught because filepath.EvalSymlinks is applied to the
//     path — or to its nearest existing ancestor, for a file that does not
//     exist yet (e.g. a `write` call creating a new file) — before the
//     containment check runs.
func ConfineWorkspacePath(scope SandboxScope, workspaceRoot string, extraAllowedRoots []string, absPath string) (string, error) {
	if scope != SandboxScopeWorkspace {
		return absPath, nil
	}

	canonicalRoot, err := canonicalizeExistingRoot(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve workspace root: %w", err)
	}

	canonicalPath, err := canonicalizePathAllowingMissing(absPath)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve path %q: %w", absPath, err)
	}

	if pathWithinRoot(canonicalPath, canonicalRoot) {
		return absPath, nil
	}

	for _, extraRoot := range extraAllowedRoots {
		canonicalExtra, err := canonicalizeExistingRoot(extraRoot)
		if err != nil {
			// A misconfigured or missing allowlist entry should not itself grant
			// access; skip it rather than fail the whole check.
			continue
		}
		if pathWithinRoot(canonicalPath, canonicalExtra) {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("sandbox violation: path %q escapes the allowed workspace root %q", absPath, workspaceRoot)
}

// pathWithinRoot reports whether candidate lies inside root, comparing by
// path component (filepath.Rel) rather than string prefix so that a sibling
// directory sharing a name prefix with root (e.g. root "/tmp/ws" and
// candidate "/tmp/ws-evil/secret") is correctly rejected.
func pathWithinRoot(candidate, root string) bool {
	if candidate == root {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// canonicalizeExistingRoot resolves symlinks for a path that is expected to
// already exist (a workspace root or an allowlisted extra root).
func canonicalizeExistingRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// canonicalizePathAllowingMissing resolves symlinks for absPath. If absPath
// (or an ancestor of it) does not exist yet — as with a `write` or
// `apply_patch` call creating a brand-new file — it walks up to the nearest
// existing ancestor, resolves THAT ancestor's symlinks, and rejoins the
// non-existent trailing components literally. This defeats a symlink placed
// inside the workspace whose target directory lies outside it, even when the
// final path component has not been created yet.
func canonicalizePathAllowingMissing(absPath string) (string, error) {
	clean := filepath.Clean(absPath)
	resolved, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	parent := filepath.Dir(clean)
	base := filepath.Base(clean)
	if parent == clean {
		// Reached the filesystem root without finding an existing ancestor.
		return clean, nil
	}
	resolvedParent, err := canonicalizePathAllowingMissing(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, base), nil
}

// ResolveWorkspacePathConfined resolves relativePath against workspaceRoot
// exactly like ResolveWorkspacePath, then additionally enforces
// ConfineWorkspacePath using the effective sandbox scope (context override,
// falling back to defaultScope). This is the single call every path-resolving
// tool handler should use going forward.
func ResolveWorkspacePathConfined(ctx context.Context, workspaceRoot, relativePath string, defaultScope SandboxScope) (string, error) {
	absPath, err := ResolveWorkspacePath(workspaceRoot, relativePath)
	if err != nil {
		return "", err
	}
	scope := EffectiveSandboxScope(ctx, defaultScope)
	return ConfineWorkspacePath(scope, workspaceRoot, nil, absPath)
}
