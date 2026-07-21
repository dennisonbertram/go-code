package harness

// extra_dirs_test.go — acceptance and validation coverage for RunRequest.ExtraDirs
// (TUI /add-dir, epic #822 slice 3). The acceptance tests mirror
// default_sandbox_acceptance_test.go: a real Runner + real default tool
// registry + real step engine, proving the file-tool confinement honors
// caller-granted extra roots while everything outside all roots stays denied.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runReadOnce runs a single-turn read of absPath and returns the read tool's
// result payload. extraDirs may be nil.
func runReadOnce(t *testing.T, workspace string, extraDirs []string, absPath string) map[string]any {
	t.Helper()

	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{})
	provider := &continuationProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call_read",
					Name:      "read",
					Arguments: fmt.Sprintf(`{"path":%q}`, absPath),
				}},
			},
			{Content: "done"},
		},
	}
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:    "read the file",
		ExtraDirs: extraDirs,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}
	return toolMessagePayload(t, runner, run.ID, "read")
}

func writeExtraDirsFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}

// TestExtraDirs_ReadUnderAddedRootSucceeds is the acceptance test: a run
// granted an extra directory via RunRequest.ExtraDirs can read a file under
// that root through the read tool.
func TestExtraDirs_ReadUnderAddedRootSucceeds(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	extraDir := t.TempDir()
	libPath := writeExtraDirsFixture(t, extraDir, "lib.go", "package sharedlibs\n")

	payload := runReadOnce(t, workspace, []string{extraDir}, libPath)

	content, _ := payload["content"].(string)
	if !strings.Contains(content, "package sharedlibs") {
		t.Fatalf("read under the added root must succeed; payload=%+v", payload)
	}
	if errMsg, _ := payload["error"].(string); errMsg != "" {
		t.Fatalf("read under the added root returned an error: %q", errMsg)
	}
}

// TestExtraDirs_ReadOutsideAllRootsDenied verifies that granting one extra
// root does not open anything else: a path outside both the workspace and the
// added root is still denied.
func TestExtraDirs_ReadOutsideAllRootsDenied(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	extraDir := t.TempDir()
	outsideDir := t.TempDir()
	secretPath := writeExtraDirsFixture(t, outsideDir, "secret.txt", "TOPSECRET\n")

	payload := runReadOnce(t, workspace, []string{extraDir}, secretPath)

	if content, _ := payload["content"].(string); strings.Contains(content, "TOPSECRET") {
		t.Fatalf("SECURITY REGRESSION: run read a file outside all roots: %+v", payload)
	}
	errMsg, _ := payload["error"].(string)
	if !strings.Contains(errMsg, "sandbox") && !strings.Contains(errMsg, "escapes") {
		t.Fatalf("expected a sandbox/escape violation, got: %q", errMsg)
	}
}

// TestExtraDirs_ControlRunWithoutExtraDirsDenied verifies the added root is
// not special: without ExtraDirs the same read is denied (the confinement
// default is unchanged).
func TestExtraDirs_ControlRunWithoutExtraDirsDenied(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	extraDir := t.TempDir()
	libPath := writeExtraDirsFixture(t, extraDir, "lib.go", "package sharedlibs\n")

	payload := runReadOnce(t, workspace, nil, libPath)

	if content, _ := payload["content"].(string); strings.Contains(content, "package sharedlibs") {
		t.Fatalf("run without ExtraDirs must not read outside the workspace: %+v", payload)
	}
	errMsg, _ := payload["error"].(string)
	if !strings.Contains(errMsg, "sandbox") && !strings.Contains(errMsg, "escapes") {
		t.Fatalf("expected a sandbox/escape violation, got: %q", errMsg)
	}
}

// TestStartRunExtraDirsValidation covers the synchronous StartRun validation:
// extra_dirs entries must be absolute paths to existing directories.
func TestStartRunExtraDirsValidation(t *testing.T) {
	t.Parallel()

	validDir := t.TempDir()
	aFile := writeExtraDirsFixture(t, t.TempDir(), "file.txt", "x")

	cases := []struct {
		name      string
		extraDirs []string
		wantErr   string
	}{
		{name: "nil is fine", extraDirs: nil, wantErr: ""},
		{name: "valid directory", extraDirs: []string{validDir}, wantErr: ""},
		{name: "two valid directories", extraDirs: []string{validDir, t.TempDir()}, wantErr: ""},
		{name: "empty entry rejected", extraDirs: []string{""}, wantErr: "extra_dirs"},
		{name: "relative path rejected", extraDirs: []string{"shared-libs"}, wantErr: "absolute"},
		{name: "dot-dot relative rejected", extraDirs: []string{"../escape"}, wantErr: "absolute"},
		{name: "nonexistent rejected", extraDirs: []string{filepath.Join(t.TempDir(), "nope")}, wantErr: "does not exist"},
		{name: "file not directory rejected", extraDirs: []string{aFile}, wantErr: "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := NewRunner(&continuationProvider{}, NewRegistry(), RunnerConfig{DefaultModel: "test-model"})
			t.Cleanup(func() { _ = runner.Shutdown(context.Background()) })
			_, err := runner.StartRun(RunRequest{Prompt: "hi", ExtraDirs: tc.extraDirs})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("StartRun with extra_dirs=%v: unexpected error: %v", tc.extraDirs, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("StartRun with extra_dirs=%v: expected error containing %q, got nil", tc.extraDirs, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "extra_dirs") || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q must name extra_dirs and contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
