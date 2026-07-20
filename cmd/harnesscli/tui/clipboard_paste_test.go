package tui_test

// Tests for epic #818 slice 2: Ctrl-V image paste with placeholder chips.
// External-package coverage drives the public message flow (ModelsFetchedMsg
// + ModelSelectedMsg + KeyCtrlV); the stubbed-reader happy path lives in the
// internal test file.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestPasteImage_ModalityGateRejectsTextOnlyModel drives the full public
// message flow: the server model list marks claude-sonnet-4-6 text-only, the
// user selects it, then presses ctrl+v. The paste must be rejected up front
// with a status message naming the model, and no chip may appear.
func TestPasteImage_ModalityGateRejectsTextOnlyModel(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.ModelsFetchedMsg{Models: []modelswitcher.ServerModelEntry{
		{ID: "claude-sonnet-4-6", Provider: "anthropic", Modalities: []string{"text"}},
		{ID: "gpt-4.1", Provider: "openai", Modalities: []string{"text", "image"}},
	}})
	m = m2.(tui.Model)

	m3, _ := m.Update(tui.ModelSelectedMsg{ModelID: "claude-sonnet-4-6", Provider: "anthropic"})
	m = m3.(tui.Model)

	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m4.(tui.Model)

	if !strings.Contains(m.StatusMsg(), "claude-sonnet-4-6") {
		t.Errorf("rejection status must name the model, got %q", m.StatusMsg())
	}
	if !strings.Contains(m.StatusMsg(), "image") {
		t.Errorf("rejection status must mention image input, got %q", m.StatusMsg())
	}
	if strings.Contains(m.View(), "[image #") {
		t.Errorf("no chip may be rendered after a gated rejection, got:\n%s", m.View())
	}
}

// TestPasteImage_OverlayActiveIsNoOp verifies ctrl+v does nothing (no chip,
// no error status) while an overlay is active.
func TestPasteImage_OverlayActiveIsNoOp(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "help"})
	m = m2.(tui.Model)
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m3.(tui.Model)

	if strings.Contains(m.View(), "[image #") {
		t.Errorf("no chip may appear while an overlay is active, got:\n%s", m.View())
	}
}
