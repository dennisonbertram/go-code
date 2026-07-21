package tools

// extra_roots_context_test.go — the per-run extra allowed roots reach file-tool
// confinement through context (TUI /add-dir, epic #822 slice 3), mirroring
// workspace_confinement_test.go's patterns.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePathConfined_ExtraRootsFromContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extra := t.TempDir()
	other := t.TempDir()

	extraFile := filepath.Join(extra, "lib.go")
	if err := os.WriteFile(extraFile, []byte("package lib\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	otherFile := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(otherFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx := WithExtraAllowedRoots(context.Background(), []string{extra})

	// A path inside the context-granted extra root is allowed...
	if _, err := ResolveWorkspacePathConfined(ctx, root, extraFile, SandboxScopeWorkspace); err != nil {
		t.Fatalf("path inside the extra root must be allowed: %v", err)
	}
	// ...while a path outside every root stays denied.
	if _, err := ResolveWorkspacePathConfined(ctx, root, otherFile, SandboxScopeWorkspace); err == nil {
		t.Fatal("path outside all roots must be denied even with extra roots set")
	}
}

func TestResolveWorkspacePathConfined_NoContextRootsKeepsDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extra := t.TempDir()
	extraFile := filepath.Join(extra, "lib.go")
	if err := os.WriteFile(extraFile, []byte("package lib\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Without the context value nothing changes: outside the workspace is denied.
	if _, err := ResolveWorkspacePathConfined(context.Background(), root, extraFile, SandboxScopeWorkspace); err == nil {
		t.Fatal("path outside the workspace must be denied without context roots")
	}
}

func TestWithExtraAllowedRoots_CopiesSlice(t *testing.T) {
	t.Parallel()

	extra := t.TempDir()
	roots := []string{extra}
	ctx := WithExtraAllowedRoots(context.Background(), roots)
	roots[0] = t.TempDir() // mutate after the call

	got, ok := ExtraAllowedRootsFromContext(ctx)
	if !ok || len(got) != 1 || got[0] != extra {
		t.Fatalf("context must hold a copy of the roots, got %v (ok=%v)", got, ok)
	}
}

func TestExtraAllowedRootsFromContext_UnsetReturnsFalse(t *testing.T) {
	t.Parallel()

	if got, ok := ExtraAllowedRootsFromContext(context.Background()); ok || got != nil {
		t.Fatalf("unset context must return (nil, false), got %v, %v", got, ok)
	}
}
