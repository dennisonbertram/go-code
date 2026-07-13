package tui_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// ─── (A) mid-tool cancel finalizes the timer/view ────────────────────────────

// TestModel_CancelMidTool_ToolViewNoLongerRunning verifies that cancelling an
// active run (via the confirmed second ctrl+c) while a tool call is still
// "running" finalizes that tool call's view instead of leaving it stuck
// rendering "running" forever.
func TestModel_CancelMidTool_ToolViewNoLongerRunning(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tui.ToolStartMsg{CallID: "call-1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 30"}`)})
	m = m2.(tui.Model)

	if m.ActiveToolCallStatus() != "running" {
		t.Fatalf("precondition: tool call status = %q, want running", m.ActiveToolCallStatus())
	}

	// First ctrl+c shows the confirmation banner.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = m2.(tui.Model)
	// Second ctrl+c confirms the interrupt.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = m2.(tui.Model)

	if m.ActiveToolCallStatus() == "running" {
		t.Errorf("tool call status = %q, want it to be finalized (not running) after cancel", m.ActiveToolCallStatus())
	}
}

// TestModel_EscCancelMidTool_ToolViewNoLongerRunning verifies the same
// finalization happens via the direct Esc-cancel path (no banner involved).
func TestModel_EscCancelMidTool_ToolViewNoLongerRunning(t *testing.T) {
	m := activeModel(t, func() {})

	m2, _ := m.Update(tui.ToolStartMsg{CallID: "call-2", Name: "bash", Input: json.RawMessage(`{"command":"sleep 30"}`)})
	m = m2.(tui.Model)

	if m.ActiveToolCallStatus() != "running" {
		t.Fatalf("precondition: tool call status = %q, want running", m.ActiveToolCallStatus())
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(tui.Model)

	if m.RunActive() {
		t.Fatal("expected RunActive() to be false after Esc cancel")
	}
	if m.ActiveToolCallStatus() == "running" {
		t.Errorf("tool call status = %q, want it to be finalized (not running) after Esc cancel", m.ActiveToolCallStatus())
	}
}

// ─── (C) Home/End viewport navigation ────────────────────────────────────────

// TestModel_EndKey_JumpsToBottom verifies that pressing End on a scrolled-up
// transcript jumps straight to the bottom.
func TestModel_EndKey_JumpsToBottom(t *testing.T) {
	m := newScrollTestModel(80, 30)
	m = appendNLines(m, 200)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = m2.(tui.Model)
	if m.ViewportAtBottom() {
		t.Fatal("precondition: expected viewport to be scrolled up after PgUp")
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = m2.(tui.Model)

	if !m.ViewportAtBottom() {
		t.Errorf("expected viewport to be at the bottom after End, offset=%d", m.ViewportScrollOffset())
	}
}

// TestModel_HomeKey_JumpsToTop verifies that pressing Home on a long
// transcript scrolls all the way to the top (offset saturates at max).
func TestModel_HomeKey_JumpsToTop(t *testing.T) {
	m := newScrollTestModel(80, 30)
	m = appendNLines(m, 200)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = m2.(tui.Model)

	topOffset := m.ViewportScrollOffset()
	if topOffset <= 0 {
		t.Fatalf("expected scroll offset > 0 after Home, got %d", topOffset)
	}

	// Scrolling up further by a single line must not move the offset (already
	// clamped at the top), confirming Home reached the actual top.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = m2.(tui.Model)
	if m.ViewportScrollOffset() != topOffset {
		t.Errorf("offset changed after PgUp from Home position: before=%d after=%d", topOffset, m.ViewportScrollOffset())
	}
}

// ─── (D) plugin bash commands dispatch asynchronously ────────────────────────

// TestModel_PluginBashCommand_ReturnsCmdInsteadOfBlocking verifies that
// submitting a slow bash plugin slash command returns a tea.Cmd to run
// asynchronously rather than blocking inside Update() until the command
// finishes.
func TestModel_PluginBashCommand_ReturnsCmdInsteadOfBlocking(t *testing.T) {
	dir := t.TempDir()
	pluginJSON := `{
		"name": "slowcmd",
		"description": "a slow bash plugin",
		"handler": "bash",
		"command": "sleep 5 && echo done"
	}`
	if err := os.WriteFile(filepath.Join(dir, "slowcmd.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	m := tui.New(tui.DefaultTUIConfig()).WithPluginsDir(dir)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(tui.Model)

	// Update() is called directly (no goroutine): if the plugin ran inline it
	// would block here for the full 5s sleep, so a return well under that proves
	// the dispatch is asynchronous. The threshold sits far below the sleep and
	// far above any realistic async-dispatch cost, so it does not flake under load.
	start := time.Now()
	_, resultCmd := m.Update(inputarea.CommandSubmittedMsg{Value: "/slowcmd"})
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("Update() blocked for %s dispatching a plugin bash command; it must run asynchronously via a tea.Cmd", elapsed)
	}

	if resultCmd == nil {
		t.Fatal("expected Update() to return a non-nil tea.Cmd for the async plugin dispatch")
	}
}

// TestModel_PluginCommandResult_AppliedOnArrival verifies that once the
// asynchronous plugin command's tea.Cmd is invoked and its resulting message
// is fed back into Update(), the plugin's output is applied (appended to the
// transcript) exactly as it would be for a synchronous command.
func TestModel_PluginCommandResult_AppliedOnArrival(t *testing.T) {
	dir := t.TempDir()
	pluginJSON := `{
		"name": "fastcmd",
		"description": "a fast bash plugin",
		"handler": "bash",
		"command": "echo plugin-output-marker"
	}`
	if err := os.WriteFile(filepath.Join(dir, "fastcmd.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}

	m := tui.New(tui.DefaultTUIConfig()).WithPluginsDir(dir)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(tui.Model)

	m2, cmd := m.Update(inputarea.CommandSubmittedMsg{Value: "/fastcmd"})
	m = m2.(tui.Model)
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd from dispatching the plugin command")
	}
	if strings.Contains(m.View(), "plugin-output-marker") {
		t.Fatal("plugin output must not appear before its async result message is applied")
	}

	batchMsg := cmd()
	batch, ok := batchMsg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("expected a non-empty tea.BatchMsg, got %T", batchMsg)
	}
	// The plugin dispatch cmd is appended last (after the status-message cmd);
	// invoke only that one — the status-message cmd is a multi-second tea.Tick
	// and isn't relevant to this test.
	pluginResultMsg := batch[len(batch)-1]()
	m2, _ = m.Update(pluginResultMsg)
	m = m2.(tui.Model)

	if !strings.Contains(m.View(), "plugin-output-marker") {
		t.Errorf("expected plugin output to appear in the view after the result message is applied:\n%s", m.View())
	}
}

// ─── (E) whitespace-only submissions are rejected ────────────────────────────

// TestModel_WhitespaceOnlySubmit_DoesNotSendMessage verifies that submitting a
// CommandSubmittedMsg containing only whitespace is a no-op: it must not
// start a run and must not appear as a user message in the transcript.
func TestModel_WhitespaceOnlySubmit_DoesNotSendMessage(t *testing.T) {
	m := newScrollTestModel(80, 24)

	m2, cmd := m.Update(inputarea.CommandSubmittedMsg{Value: "   \t  "})
	m = m2.(tui.Model)

	if len(m.Transcript()) != 0 {
		t.Errorf("expected no transcript entries after a whitespace-only submission, got %d", len(m.Transcript()))
	}
	if cmd != nil {
		t.Error("expected no cmd (e.g. a run start) to be returned for a whitespace-only submission")
	}
}

// TestInputArea_EnterWithWhitespaceOnlyValue_DoesNotSubmit verifies that the
// input area itself refuses to fire CommandSubmittedMsg when its buffer is
// whitespace-only (not just fully empty), guarding at the source.
func TestInputArea_EnterWithWhitespaceOnlyValue_DoesNotSubmit(t *testing.T) {
	ia := inputarea.New(80)
	ia = ia.SetValue("   ")

	updated, cmd := ia.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd != nil {
		t.Fatal("expected no CommandSubmittedMsg cmd for a whitespace-only value")
	}
	if updated.Value() != "   " {
		t.Errorf("expected the whitespace-only buffer to be left untouched, got %q", updated.Value())
	}
}
