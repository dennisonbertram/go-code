package tui_test

// resume_conversation_test.go covers the --resume flow: seeding conversationID
// from TUIConfig.ResumeConversationID at startup, and rendering fetched history
// (ConversationHistoryMsg / ConversationHistoryErrorMsg) into the transcript.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestResumeConversationID_FlowsIntoConversationID verifies that setting
// TUIConfig.ResumeConversationID seeds m.conversationID at construction time,
// so a resumed session's follow-up prompts are grouped under the same
// conversation from the very first message.
func TestResumeConversationID_FlowsIntoConversationID(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.ResumeConversationID = "conv-resume-123"

	m := tui.New(cfg)

	if m.ConversationID() != "conv-resume-123" {
		t.Errorf("ConversationID() = %q, want %q", m.ConversationID(), "conv-resume-123")
	}
}

// TestResumeConversationID_EmptyLeavesConversationIDEmpty is the control case:
// with no resume ID configured, a fresh Model has no conversation ID yet.
func TestResumeConversationID_EmptyLeavesConversationIDEmpty(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())

	if m.ConversationID() != "" {
		t.Errorf("ConversationID() = %q, want empty for a non-resumed session", m.ConversationID())
	}
}

// TestConversationHistoryMsg_RendersUserAndAssistantMessages verifies that
// feeding a ConversationHistoryMsg with a user and an assistant message into
// Update renders both into the transcript view, and sets a status message
// summarizing the resume.
func TestConversationHistoryMsg_RendersUserAndAssistantMessages(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.ResumeConversationID = "conv-abcdef1234"
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m3, _ := m2.(tui.Model).Update(tui.ConversationHistoryMsg{
		ConversationID: "conv-abcdef1234",
		Messages: []tui.ConversationMessage{
			{Role: "user", Content: "what is the capital of France"},
			{Role: "assistant", Content: "Paris is the capital of France"},
		},
	})
	after := m3.(tui.Model)

	view := after.View()
	if !strings.Contains(view, "what is the capital of France") {
		t.Errorf("expected view to contain the resumed user message, got:\n%s", view)
	}
	if !strings.Contains(view, "Paris is the capital of France") {
		t.Errorf("expected view to contain the resumed assistant message, got:\n%s", view)
	}
	if !strings.Contains(after.StatusMsg(), "Resumed conversation") {
		t.Errorf("StatusMsg() = %q, want it to mention the resumed conversation", after.StatusMsg())
	}
}

// TestConversationHistoryErrorMsg_SetsClearStatusWithoutCrashing verifies that
// a failed history fetch reports a clear status message and leaves the model
// usable (no crash, no active-run side effects).
func TestConversationHistoryErrorMsg_SetsClearStatusWithoutCrashing(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m3, _ := m2.(tui.Model).Update(tui.ConversationHistoryErrorMsg{
		ConversationID: "conv-missing",
		Err:            "HTTP 404: not found",
	})
	after := m3.(tui.Model)

	if !strings.Contains(after.StatusMsg(), "Could not load conversation conv-missing") {
		t.Errorf("StatusMsg() = %q, want a clear load-failure message", after.StatusMsg())
	}
	if after.RunActive() {
		t.Error("RunActive() must remain false after a history load error")
	}
}
