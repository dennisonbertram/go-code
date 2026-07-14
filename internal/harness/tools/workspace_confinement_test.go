package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are written as ATTACKS against the workspace sandbox, per the
// task contract for BUG 1 (P1): the SandboxScopeWorkspace scope must actually
// confine file-tool I/O to the workspace root (and any explicit allowlisted
// roots), defeating absolute-path escape, traversal, symlink escape, and the
// sibling-directory-name-prefix trap. SandboxScopeLocal/Unrestricted remain
// the existing, explicit opt-out and must keep passing through unchanged.

func TestConfineWorkspacePath_AbsolutePathEscape_RejectedUnderWorkspaceScope(t *testing.T) {
	root := t.TempDir()

	_, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, "/etc/passwd")
	if err == nil {
		t.Fatal("expected absolute path outside workspace to be rejected under workspace scope")
	}
	if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("error should describe a sandbox/escape violation, got: %v", err)
	}
}

func TestConfineWorkspacePath_TraversalEscape_RejectedUnderWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	// Simulate what ResolveWorkspacePath would compute for a relative "../../etc/passwd"
	// input joined against a directory *inside* the workspace, then Cleaned — the
	// classic traversal shape that must still be caught on the final canonical path.
	escaped := filepath.Clean(filepath.Join(root, "..", "..", "etc", "passwd"))

	_, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, escaped)
	if err == nil {
		t.Fatal("expected traversal path to be rejected under workspace scope")
	}
}

func TestConfineWorkspacePath_SymlinkEscape_RejectedUnderWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secretOutside := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretOutside, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A symlink *inside* the workspace whose target lives *outside* it — the
	// classic bypass this check must defeat.
	link := filepath.Join(root, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	candidate := filepath.Join(link, "secret.txt")
	_, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, candidate)
	if err == nil {
		t.Fatal("expected symlink escape to be rejected under workspace scope")
	}
}

func TestConfineWorkspacePath_SiblingPrefixDirectory_RejectedUnderWorkspaceScope(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ws")
	evil := filepath.Join(parent, "ws-evil")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	evilFile := filepath.Join(evil, "secret.txt")
	if err := os.WriteFile(evilFile, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A naive string-prefix check ("does the path start with root?") would
	// wrongly allow this, because "/tmp/xyz/ws-evil/..." starts with the
	// STRING "/tmp/xyz/ws". The check must be component-wise.
	_, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, evilFile)
	if err == nil {
		t.Fatalf("expected sibling directory %q (prefix of %q) to be rejected", evil, root)
	}
}

func TestConfineWorkspacePath_LegitimateInWorkspaceAccess_Allowed(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "file.go")
	if err := os.WriteFile(target, []byte("package pkg"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, target)
	if err != nil {
		t.Fatalf("expected legitimate in-workspace path to be allowed, got error: %v", err)
	}
	if resolved == "" {
		t.Fatal("expected a non-empty resolved path")
	}
}

func TestConfineWorkspacePath_InWorkspaceSymlinkStayingInside_Allowed(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realDir, "file.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	candidate := filepath.Join(link, "file.txt")
	if _, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, candidate); err != nil {
		t.Fatalf("expected in-workspace symlink that stays inside the workspace to be allowed, got: %v", err)
	}
}

func TestConfineWorkspacePath_NotYetExistingFile_ConfinedByParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// A symlinked directory inside the workspace pointing outside; the file
	// itself does not exist yet (as with a `write` tool call creating a new
	// file). The not-yet-existing-file case must canonicalize the parent.
	link := filepath.Join(root, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	newFile := filepath.Join(link, "new-file.txt")

	if _, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, newFile); err == nil {
		t.Fatal("expected new file under an escaping symlinked parent to be rejected")
	}

	// And the legitimate case: a new file directly under the workspace root
	// (no symlink involved) must be allowed even though it doesn't exist yet.
	legitNewFile := filepath.Join(root, "brand-new.txt")
	if _, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, nil, legitNewFile); err != nil {
		t.Fatalf("expected legitimate not-yet-existing in-workspace file to be allowed, got: %v", err)
	}
}

func TestConfineWorkspacePath_NonWorkspaceScopes_PassThroughUnchanged(t *testing.T) {
	root := t.TempDir()
	outsidePath := "/etc/passwd"

	for _, scope := range []SandboxScope{SandboxScopeLocal, SandboxScopeUnrestricted, ""} {
		resolved, err := ConfineWorkspacePath(scope, root, nil, outsidePath)
		if err != nil {
			t.Fatalf("scope %q: expected pass-through (existing opt-in behavior) for absolute path, got error: %v", scope, err)
		}
		if resolved != outsidePath {
			t.Fatalf("scope %q: expected unchanged path %q, got %q", scope, outsidePath, resolved)
		}
	}
}

func TestConfineWorkspacePath_ExtraAllowedRoots_Permitted(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	allowedFile := filepath.Join(extra, "catalog.yaml")
	if err := os.WriteFile(allowedFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, []string{extra}, allowedFile); err != nil {
		t.Fatalf("expected explicitly allowlisted extra root to be permitted, got: %v", err)
	}

	// Something outside both the workspace and the allowlisted root is still rejected.
	other := t.TempDir()
	otherFile := filepath.Join(other, "nope.txt")
	if err := os.WriteFile(otherFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfineWorkspacePath(SandboxScopeWorkspace, root, []string{extra}, otherFile); err == nil {
		t.Fatal("expected path outside both workspace root and allowlisted roots to be rejected")
	}
}

func TestEffectiveSandboxScope_ContextOverrideTakesPrecedence(t *testing.T) {
	ctx := WithSandboxScope(context.Background(), SandboxScopeWorkspace)
	if got := EffectiveSandboxScope(ctx, SandboxScopeUnrestricted); got != SandboxScopeWorkspace {
		t.Fatalf("expected context override %q, got %q", SandboxScopeWorkspace, got)
	}
}

func TestEffectiveSandboxScope_FallsBackToDefaultWhenNoContextOverride(t *testing.T) {
	if got := EffectiveSandboxScope(context.Background(), SandboxScopeWorkspace); got != SandboxScopeWorkspace {
		t.Fatalf("expected default %q, got %q", SandboxScopeWorkspace, got)
	}
}

func TestResolveWorkspacePathConfined_AbsoluteEscape_RejectedUnderWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	if _, err := ResolveWorkspacePathConfined(ctx, root, "/etc/passwd", SandboxScopeWorkspace); err == nil {
		t.Fatal("expected absolute-path escape via ResolveWorkspacePathConfined to be rejected")
	}
}

func TestResolveWorkspacePathConfined_RelativeInWorkspace_AllowedUnderWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	resolved, err := ResolveWorkspacePathConfined(ctx, root, "ok.txt", SandboxScopeWorkspace)
	if err != nil {
		t.Fatalf("expected legitimate relative in-workspace write to succeed, got: %v", err)
	}
	if filepath.Base(resolved) != "ok.txt" {
		t.Fatalf("unexpected resolved path %q", resolved)
	}
}

func TestResolveWorkspacePathConfined_ContextScopeOverridesDefault(t *testing.T) {
	root := t.TempDir()
	ctx := WithSandboxScope(context.Background(), SandboxScopeWorkspace)

	// defaultScope passed at construction is unrestricted, but the per-run
	// context override (workspace) must still confine — proving the step
	// engine's per-call scope override is honored, not just the build-time default.
	if _, err := ResolveWorkspacePathConfined(ctx, root, "/etc/passwd", SandboxScopeUnrestricted); err == nil {
		t.Fatal("expected context-level workspace scope override to confine the path even though the default scope is unrestricted")
	}
}
