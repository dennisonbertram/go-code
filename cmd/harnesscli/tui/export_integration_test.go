package tui_test

import (
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	"go-agent-harness/cmd/harnesscli/tui/components/transcriptexport"
)

func TestExportCommandWritesOutsideWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)

	m := newReadyModel()
	m1, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "hello"})
	m2, _ := m1.(tui.Model).Update(tui.RunStartedMsg{RunID: "r1"})
	m3, _ := m2.(tui.Model).Update(sseContentMsg("exportable reply"))
	m4, _ := m3.(tui.Model).Update(tui.SSEDoneMsg{EventType: "run.completed"})

	updated, cmd := m4.(tui.Model).Update(inputarea.CommandSubmittedMsg{Value: "/export"})
	if _, ok := updated.(tui.Model); !ok {
		t.Fatalf("expected updated model, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected export command")
	}

	msg := cmd()
	exportMsg, ok := msg.(tui.ExportTranscriptMsg)
	if !ok {
		t.Fatalf("expected ExportTranscriptMsg, got %T", msg)
	}
	if exportMsg.FilePath == "" {
		t.Fatal("expected exported file path")
	}
	expectedPrefix := filepath.ToSlash(transcriptexport.DefaultOutputDir())
	if !strings.HasPrefix(filepath.ToSlash(exportMsg.FilePath), expectedPrefix) {
		t.Fatalf("expected export path under %q, got %q", expectedPrefix, exportMsg.FilePath)
	}

	matches, err := filepath.Glob(filepath.Join(workingDir, "transcript-*.md"))
	if err != nil {
		t.Fatalf("glob working directory: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no transcript files in working directory, found %v", matches)
	}
}
