package tui

import (
	"os"
	"testing"
)

func TestWriteTempEditorFileSeedsContent(t *testing.T) {
	path, err := writeTempEditorFile("draft prompt\nsecond line")
	if err != nil {
		t.Fatalf("writeTempEditorFile: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp editor file: %v", err)
	}
	if string(data) != "draft prompt\nsecond line" {
		t.Fatalf("temp editor content = %q", string(data))
	}
}

func TestEditorExecCommandWiresEditorFileAndStdio(t *testing.T) {
	cmd := editorExecCommand("example-editor", "/tmp/harness-edit.txt")

	if cmd.Path != "example-editor" {
		t.Fatalf("cmd.Path = %q, want example-editor", cmd.Path)
	}
	wantArgs := []string{"example-editor", "/tmp/harness-edit.txt"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if cmd.Args[i] != want {
			t.Fatalf("cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}
	if cmd.Stdin != os.Stdin {
		t.Fatal("cmd.Stdin must inherit os.Stdin")
	}
	if cmd.Stdout != os.Stdout {
		t.Fatal("cmd.Stdout must inherit os.Stdout")
	}
	if cmd.Stderr != os.Stderr {
		t.Fatal("cmd.Stderr must inherit os.Stderr")
	}
}
