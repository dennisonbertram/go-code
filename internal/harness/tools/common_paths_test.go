package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspacePath(t *testing.T) {
	root := "/workspace"
	tests := []struct {
		name        string
		path        string
		wantErr     bool
		errContains string
		wantPath    string
	}{
		{
			name:    "empty root returns error",
			path:    "foo.txt",
			wantErr: true,
		},
		{
			name:     "simple relative path",
			path:     "foo.txt",
			wantPath: "/workspace/foo.txt",
		},
		{
			name:     "nested relative path",
			path:     "dir/sub/file.go",
			wantPath: "/workspace/dir/sub/file.go",
		},
		{
			name:     "empty path returns workspace root",
			path:     "",
			wantPath: "/workspace",
		},
		{
			name:     "dot path returns workspace root",
			path:     ".",
			wantPath: "/workspace",
		},
		{
			name:     "absolute path passes through",
			path:     "/etc/nginx/nginx.conf",
			wantPath: "/etc/nginx/nginx.conf",
		},
		{
			name:     "absolute path cleaned",
			path:     "/var/log/../log/nginx/access.log",
			wantPath: "/var/log/nginx/access.log",
		},
		{
			name:        "path escaping workspace",
			path:        "../../etc/passwd",
			wantErr:     true,
			errContains: "escapes workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r string
			var err error
			if tt.name == "empty root returns error" {
				r, err = ResolveWorkspacePath("", tt.path)
			} else {
				r, err = ResolveWorkspacePath(root, tt.path)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got path %q", r)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := filepath.Clean(tt.wantPath)
			if r != want {
				t.Fatalf("got %q, want %q", r, want)
			}
		})
	}
}

func TestEnsureWorkspaceRootUsable(t *testing.T) {
	t.Run("empty root", func(t *testing.T) {
		if err := EnsureWorkspaceRootUsable(""); err == nil {
			t.Fatal("expected error for empty workspace root")
		}
	})

	t.Run("missing root", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		err := EnsureWorkspaceRootUsable(missing)
		if err == nil {
			t.Fatal("expected error for missing workspace root")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("error %q should mention missing path", err.Error())
		}
	})

	t.Run("non-directory root", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "a-file")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := EnsureWorkspaceRootUsable(file)
		if err == nil {
			t.Fatal("expected error for non-directory workspace root")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("error %q should mention not a directory", err.Error())
		}
	})

	t.Run("non-writable root", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		err := EnsureWorkspaceRootUsable(dir)
		if err == nil {
			t.Fatal("expected error for non-writable workspace root")
		}
		if !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("error %q should mention not writable", err.Error())
		}
	})

	t.Run("usable root", func(t *testing.T) {
		dir := t.TempDir()
		if err := EnsureWorkspaceRootUsable(dir); err != nil {
			t.Fatalf("unexpected error for usable root: %v", err)
		}
	})
}

func TestLegacyWriteToolMissingWorkspaceRootFailsLoudly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	tool := writeTool(missing)
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"test.txt","content":"hello"}`))
	if err == nil {
		t.Fatal("expected error when workspace root does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q should mention missing root", err.Error())
	}
}
