package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestIsEACCESRecognizesPathPermissionError(t *testing.T) {
	t.Parallel()

	if !isEACCES(&os.PathError{Op: "open", Path: "secret", Err: syscall.EACCES}) {
		t.Fatal("expected EACCES path error")
	}
	if isEACCES(fmt.Errorf("permission denied")) {
		t.Fatal("plain errors must not be treated as syscall EACCES")
	}
}

func TestEnsureWorkspaceRootUsable(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		dir := t.TempDir()
		err := EnsureWorkspaceRootUsable(filepath.Join(dir, "nonexistent"))
		if err == nil {
			t.Fatal("expected error for missing workspace root")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("error should mention 'does not exist', got: %v", err)
		}
	})

	t.Run("empty root", func(t *testing.T) {
		err := EnsureWorkspaceRootUsable("")
		if err == nil {
			t.Fatal("expected error for empty workspace root")
		}
		if !strings.Contains(err.Error(), "required") {
			t.Fatalf("error should mention 'required', got: %v", err)
		}
	})

	t.Run("non-directory root", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "regular-file")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := EnsureWorkspaceRootUsable(f)
		if err == nil {
			t.Fatal("expected error for non-directory workspace root")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("error should mention 'not a directory', got: %v", err)
		}
	})

	t.Run("non-writable root", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		err := EnsureWorkspaceRootUsable(dir)
		if err == nil {
			t.Fatal("expected error for non-writable workspace root")
		}
		if !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("error should mention 'not writable', got: %v", err)
		}
	})

	t.Run("usable root", func(t *testing.T) {
		dir := t.TempDir()
		err := EnsureWorkspaceRootUsable(dir)
		if err != nil {
			t.Fatalf("expected no error for usable workspace root, got: %v", err)
		}
	})
}
