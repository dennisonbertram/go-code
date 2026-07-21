package tui_test

import (
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestForkCommand_Registered verifies /fork is in the built-in command
// registry and dispatches without error.
func TestForkCommand_Registered(t *testing.T) {
	t.Parallel()

	reg := tui.NewCommandRegistry()
	if !reg.IsRegistered("fork") {
		t.Fatal("command 'fork' must be registered in NewCommandRegistry()")
	}
	result := reg.Dispatch(tui.Command{Name: "fork"})
	if result.Status != tui.CmdOK {
		t.Errorf("fork dispatch: expected CmdOK, got %v", result.Status)
	}
	entry, ok := reg.Lookup("fork")
	if !ok {
		t.Fatal("Lookup('fork') must succeed")
	}
	if entry.Description == "" {
		t.Error("fork must carry a help description")
	}
}

// TestForkCommand_SlashComplete verifies /fork appears in slash-completion
// when typing "/fo".
func TestForkCommand_SlashComplete(t *testing.T) {
	m := initModel(t, 120, 40)
	m = typeIntoModel(m, "/fo")
	v := m.View()

	if !strings.Contains(v, "fork") {
		t.Errorf("slash-complete must contain 'fork' when typing '/fo'; got:\n%s", v)
	}
}

// TestForkCommand_NoActiveConversation verifies /fork without an active
// conversation renders a hint instead of calling the server.
func TestForkCommand_NoActiveConversation(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/fork")

	if got := m.StatusMsg(); !strings.Contains(got, "No active conversation") {
		t.Errorf("StatusMsg() = %q, want a 'No active conversation' hint", got)
	}
	if m.ConversationID() != "" {
		t.Errorf("ConversationID must stay empty, got %q", m.ConversationID())
	}
}

// TestForkResultMsg_SuccessSwitchesConversation verifies that a successful
// fork response switches the model into the fork: conversationID becomes the
// new ID, the session store gains a fork entry pointing at the source, and
// the status hint names both IDs.
func TestForkResultMsg_SuccessSwitchesConversation(t *testing.T) {
	m := initModel(t, 80, 24)

	// Simulate an active conversation.
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "conv-src"})
	m = m2.(tui.Model)
	if m.ConversationID() != "conv-src" {
		t.Fatalf("pre-condition: ConversationID = %q, want conv-src", m.ConversationID())
	}

	m2, _ = m.Update(tui.ForkResultMsg{SrcID: "conv-src", NewID: "conv-fork-1", MessageCount: 4})
	m = m2.(tui.Model)

	if m.ConversationID() != "conv-fork-1" {
		t.Errorf("ConversationID after fork: want %q, got %q", "conv-fork-1", m.ConversationID())
	}

	entry, ok := m.SessionStore().Get("conv-fork-1")
	if !ok {
		t.Fatal("SessionStore must contain the fork entry")
	}
	if entry.LastMsg != "forked from conv-src" {
		t.Errorf("fork entry LastMsg: want %q, got %q", "forked from conv-src", entry.LastMsg)
	}

	status := m.StatusMsg()
	if !strings.Contains(status, "conv-src") || !strings.Contains(status, "conv-fork-1") {
		t.Errorf("status must name both IDs, got %q", status)
	}
	if !strings.Contains(status, "you are now in the fork") {
		t.Errorf("status must tell the user they are in the fork, got %q", status)
	}
}

// TestForkResultMsg_ErrorStaysInConversation verifies that a failed fork
// (404/501/network) renders the error and leaves the current conversation
// untouched.
func TestForkResultMsg_ErrorStaysInConversation(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.RunStartedMsg{RunID: "conv-err-src"})
	m = m2.(tui.Model)

	m2, _ = m.Update(tui.ForkResultMsg{SrcID: "conv-err-src", Err: "HTTP 404: conversation not found"})
	m = m2.(tui.Model)

	if m.ConversationID() != "conv-err-src" {
		t.Errorf("ConversationID must stay %q on fork error, got %q", "conv-err-src", m.ConversationID())
	}
	status := m.StatusMsg()
	if !strings.Contains(status, "Fork failed") {
		t.Errorf("status must report the failure, got %q", status)
	}
	if !strings.Contains(status, "404") {
		t.Errorf("status must include the server error, got %q", status)
	}
	// No fork entry may be added on error. The store is backed by the real
	// sessions.json (shared across tests), so assert no entry references this
	// test's unique source ID rather than a global entry count.
	for _, e := range m.SessionStore().List() {
		if strings.Contains(e.LastMsg, "conv-err-src") {
			t.Errorf("error path must not register a fork entry, found %+v", e)
		}
	}
}
