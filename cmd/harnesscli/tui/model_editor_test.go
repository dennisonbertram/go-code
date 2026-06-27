package tui

import (
	"os"
	"testing"
)

func TestWriteTempEditorFileSeedsContent(t *testing.T) {
	t.Parallel()

	path, err := writeTempEditorFile("line one\nline two\n")
	if err != nil {
		t.Fatalf("writeTempEditorFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "line one\nline two\n" {
		t.Fatalf("temp file content = %q", string(got))
	}
}

func TestEditorExecCommandWiresEditorFileAndStandardStreams(t *testing.T) {
	t.Parallel()

	cmd := editorExecCommand("editor-bin", "/tmp/input.txt")
	if cmd.Path != "editor-bin" {
		t.Fatalf("cmd path = %q, want editor-bin", cmd.Path)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "editor-bin" || cmd.Args[1] != "/tmp/input.txt" {
		t.Fatalf("cmd args = %#v", cmd.Args)
	}
	if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
		t.Fatal("editor command should inherit standard streams")
	}
}
