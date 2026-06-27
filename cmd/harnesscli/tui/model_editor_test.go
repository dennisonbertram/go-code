package tui

import (
	"os"
	"testing"
)

func TestWriteTempEditorFileSeedsContent(t *testing.T) {
	path, err := writeTempEditorFile("draft message")
	if err != nil {
		t.Fatalf("writeTempEditorFile: %v", err)
	}
	defer os.Remove(path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp editor file: %v", err)
	}
	if string(raw) != "draft message" {
		t.Fatalf("temp editor content = %q", string(raw))
	}
}

func TestEditorExecCommandUsesEditorAndFileWithStdio(t *testing.T) {
	cmd := editorExecCommand("test-editor", "/tmp/message.txt")
	if cmd.Path != "test-editor" {
		t.Fatalf("Path = %q", cmd.Path)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "test-editor" || cmd.Args[1] != "/tmp/message.txt" {
		t.Fatalf("Args = %#v", cmd.Args)
	}
	if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
		t.Fatal("expected editor command to inherit stdio")
	}
}
