package tui_test

// model_664_test.go — regression for #664: resizing the terminal must NOT wipe
// the conversation viewport. The WindowSizeMsg handler used to replace m.vp with
// a fresh empty viewport, discarding all messages and tool cards. It now resizes
// the existing viewport in place, preserving history.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func TestResizePreservesConversationHistory(t *testing.T) {
	m := initModel(t, 120, 40) // first WindowSizeMsg -> ready
	m = m.WithCancelRun(func() {})

	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-664"})
	m = m2.(tui.Model)
	// Stream an assistant message into the viewport.
	m3, _ := m.Update(tui.SSEEventMsg{
		EventType: "assistant.message.delta",
		Raw:       []byte(`{"content":"UNIQUE_HISTORY_MARKER_664 hello there"}`),
	})
	m = m3.(tui.Model)
	if !strings.Contains(m.View(), "UNIQUE_HISTORY_MARKER_664") {
		t.Fatalf("precondition: marker should be visible before resize; view=%q", m.View())
	}

	// A subsequent terminal resize must keep the history.
	m4, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m4.(tui.Model)
	if !strings.Contains(m.View(), "UNIQUE_HISTORY_MARKER_664") {
		t.Errorf("conversation history must survive a terminal resize (#664); view=%q", m.View())
	}

	// Resizing back up should still preserve it.
	m5, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	m = m5.(tui.Model)
	if !strings.Contains(m.View(), "UNIQUE_HISTORY_MARKER_664") {
		t.Errorf("history must survive resizing back up; view=%q", m.View())
	}
}
