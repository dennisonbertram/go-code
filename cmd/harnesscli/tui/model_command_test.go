package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// TestTUI_DailyHarnessCommandsSetGuidance verifies the TUI-first harness
// command entry points are executable through the normal slash-command path.
func TestTUI_DailyHarnessCommandsSetGuidance(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{command: "/attach", want: "Attach files by typing @path"},
		{command: "/runs", want: "Loading runs"},
		{command: "/cancel", want: "Usage: /cancel <run-id>"},
		{command: "/replay", want: "Usage: /replay <run-id-or-rollout-path>"},
		{command: "/resume", want: "Usage: /resume <run-id> <prompt>"},
		{command: "/continue", want: "Usage: /resume <run-id> <prompt>"},
		{command: "/doctor", want: "go test ./cmd/harnesscli"},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			m := initModel(t, 80, 24)
			m = sendSlashCommand(m, tc.command)
			if got := m.StatusMsg(); !strings.Contains(got, tc.want) {
				t.Fatalf("StatusMsg() = %q, want substring %q", got, tc.want)
			}
		})
	}
}

// ─── /model command tests ─────────────────────────────────────────────────────

// TestTUI137_ModelCommandOpensOverlay verifies /model opens the model overlay.
func TestTUI137_ModelCommandOpensOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	if !m.OverlayActive() {
		t.Fatal("OverlayActive() must be true after /model")
	}
	if m.ActiveOverlay() != "model" {
		t.Errorf("ActiveOverlay(): want %q, got %q", "model", m.ActiveOverlay())
	}
}

// TestTUI137_ModelOverlayEscapeLevel0ClosesOverlay verifies Escape at Level-0 closes the overlay.
func TestTUI137_ModelOverlayEscapeLevel0ClosesOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")
	if !m.OverlayActive() {
		t.Fatal("pre-condition: overlay must be open")
	}

	m, _ = sendEscape(m)

	if m.OverlayActive() {
		t.Error("OverlayActive() must be false after Escape at Level-0")
	}
	if m.ActiveOverlay() != "" {
		t.Errorf("ActiveOverlay() must be '' after Escape, got %q", m.ActiveOverlay())
	}
}

// TestTUI137_ModelOverlayEscapeLevel1ReturnsToLevel0 verifies Escape at the model-level
// browse level (level 1) goes back to the provider list (level 0) without closing the overlay.
func TestTUI137_ModelOverlayEscapeLevel1ReturnsToLevel0(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// Press Enter to drill into the first provider (level 0 → level 1).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	// Overlay should still be active and now showing Level-1 (model list for provider).
	if !m.OverlayActive() {
		t.Fatal("overlay must still be active after drilling into provider")
	}
	v := m.View()
	if !strings.Contains(v, "< Back") {
		t.Errorf("view must contain '< Back' at Level-1 (model list):\n%s", v)
	}

	// Escape from Level-1: should go back to Level-0 (provider list, overlay still open).
	m, _ = sendEscape(m)
	if !m.OverlayActive() {
		t.Error("overlay must remain active after Escape at Level-1")
	}
	if m.ActiveOverlay() != "model" {
		t.Errorf("ActiveOverlay() must be 'model' after Escape from Level-1, got %q", m.ActiveOverlay())
	}

	// View should now show Level-0 again (provider list, no "< Back").
	v2 := m.View()
	if strings.Contains(v2, "< Back") {
		t.Errorf("view must not contain '< Back' after returning to Level-0:\n%s", v2)
	}
	if !strings.Contains(v2, "Switch Model") {
		t.Errorf("view must show 'Switch Model' title at Level-0:\n%s", v2)
	}
}

// TestTUI137_ModelOverlayEnterNonReasoningEmitsMsg verifies that for a non-reasoning
// model (gpt-4.1), navigating to it via the two-level hierarchy and pressing Enter
// opens the config panel, and Enter at the config panel emits ModelSelectedMsg and
// GatewaySelectedMsg, closing the overlay.
func TestTUI137_ModelOverlayEnterNonReasoningEmitsMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// At level 0 (provider list): navigate to OpenAI and drill in.
	// OpenAI provider cursor — navigate until we find OpenAI.
	for i := 0; i < len(m.ModelSwitcher().Providers()); i++ {
		if m.ModelSwitcher().Providers()[m.ModelSwitcher().ProviderCursorIndex()].Label == "OpenAI" {
			break
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}
	// Enter to drill into OpenAI.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	// Now at level 1 (OpenAI models). Navigate to gpt-4.1 (should be first).
	for i := 0; i < 10; i++ {
		entry, _ := m.ModelSwitcher().Accept()
		if entry.ID == "gpt-4.1" {
			break
		}
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}

	// Verify gpt-4.1 is selected.
	entry, _ := m.ModelSwitcher().Accept()
	if entry.ID != "gpt-4.1" {
		t.Fatalf("pre-condition: expected gpt-4.1 selected, got %q", entry.ID)
	}

	// Press Enter to enter the config panel.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Overlay must still be active (now showing config panel).
	if !m.OverlayActive() {
		t.Fatal("overlay must remain active after Enter at Level-1 (config panel opened)")
	}
	if !m.ModelConfigMode() {
		t.Fatal("ModelConfigMode() must be true after Enter at Level-1")
	}
	if m.ModelConfigEntry().ID != "gpt-4.1" {
		t.Errorf("ModelConfigEntry().ID = %q, want %q", m.ModelConfigEntry().ID, "gpt-4.1")
	}

	// Press Enter at the config panel to confirm and close.
	m4, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(tui.Model)

	// Overlay should now be closed.
	if m.OverlayActive() {
		t.Error("overlay must be closed after Enter in config panel")
	}

	// The returned batch must contain a ModelSelectedMsg.
	if cmds == nil {
		t.Fatal("expected cmd from Enter in config panel")
	}
	batchMsg := cmds()
	// BubbleTea Batch returns a tea.BatchMsg (slice of tea.Msg).
	batch, ok := batchMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg from config panel Enter, got %T", batchMsg)
	}
	var foundSelected *tui.ModelSelectedMsg
	for _, cmdFn := range batch {
		if cmdFn == nil {
			continue
		}
		inner := cmdFn()
		if sel, ok2 := inner.(tui.ModelSelectedMsg); ok2 {
			sel := sel
			foundSelected = &sel
		}
	}
	if foundSelected == nil {
		t.Fatal("batch must contain a ModelSelectedMsg")
	}
	if foundSelected.ModelID != "gpt-4.1" {
		t.Errorf("ModelSelectedMsg.ModelID = %q, want %q", foundSelected.ModelID, "gpt-4.1")
	}
	if foundSelected.ReasoningEffort != "" {
		t.Errorf("ModelSelectedMsg.ReasoningEffort = %q, want empty", foundSelected.ReasoningEffort)
	}
}

// TestTUI137_ModelOverlayEnterReasoningModelEntersLevel1 verifies that navigating to
// a reasoning model via the two-level hierarchy and pressing Enter opens the config
// panel (Level-1 config), which shows reasoning effort options.
func TestTUI137_ModelOverlayEnterReasoningModelEntersLevel1(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// Navigate to DeepSeek provider at level 0.
	for i := 0; i < len(m.ModelSwitcher().Providers()); i++ {
		if m.ModelSwitcher().Providers()[m.ModelSwitcher().ProviderCursorIndex()].Label == "DeepSeek" {
			break
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}
	// Drill into DeepSeek.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	// Navigate to deepseek-reasoner (reasoning model).
	for i := 0; i < 10; i++ {
		entry, _ := m.ModelSwitcher().Accept()
		if entry.ID == "deepseek-reasoner" {
			break
		}
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}

	// Verify deepseek-reasoner is selected.
	entry, _ := m.ModelSwitcher().Accept()
	if entry.ID != "deepseek-reasoner" {
		t.Fatalf("pre-condition: expected deepseek-reasoner selected, got %q", entry.ID)
	}

	// Press Enter — should open config panel (with reasoning section).
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	if !m.OverlayActive() {
		t.Error("overlay must remain active after Enter on reasoning model")
	}
	if !m.ModelConfigMode() {
		t.Error("ModelConfigMode() must be true after Enter on reasoning model")
	}
	// The config panel should show reasoning effort options.
	v := m.View()
	if !strings.Contains(v, "Reasoning") {
		t.Errorf("view must show reasoning effort section after entering config panel for reasoning model:\n%s", v)
	}
}

// TestTUI137_ModelOverlayEnterAtConfigPanelClosesAndSetsModel verifies that:
//   - Navigating to deepseek-reasoner via the two-level hierarchy opens the config panel.
//   - Navigating to the Reasoning section and selecting "low" effort.
//   - Enter at the config panel closes the overlay and emits ModelSelectedMsg with
//     ReasoningEffort set correctly.
func TestTUI137_ModelOverlayEnterAtConfigPanelClosesAndSetsModel(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// Navigate to DeepSeek provider at level 0.
	for i := 0; i < len(m.ModelSwitcher().Providers()); i++ {
		if m.ModelSwitcher().Providers()[m.ModelSwitcher().ProviderCursorIndex()].Label == "DeepSeek" {
			break
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}
	// Drill into DeepSeek.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	// Navigate to deepseek-reasoner.
	for i := 0; i < 10; i++ {
		entry, _ := m.ModelSwitcher().Accept()
		if entry.ID == "deepseek-reasoner" {
			break
		}
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}

	// Enter config panel for deepseek-reasoner.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Must be in config panel mode.
	if !m.ModelConfigMode() {
		t.Fatal("ModelConfigMode() must be true after Enter on deepseek-reasoner")
	}
	if m.ModelConfigEntry().ID != "deepseek-reasoner" {
		t.Errorf("ModelConfigEntry().ID = %q, want %q", m.ModelConfigEntry().ID, "deepseek-reasoner")
	}

	// Navigate to Reasoning section: press j twice (section 0 → 1 → 2).
	for i := 0; i < 2; i++ {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = m3.(tui.Model)
	}
	if m.ModelConfigSection() != 2 {
		t.Errorf("ModelConfigSection() = %d, want 2 (reasoning)", m.ModelConfigSection())
	}

	// Navigate down to "low" (index 1 in ReasoningLevels) using j.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m4.(tui.Model)
	if m.ModelConfigReasoningCursor() != 1 {
		t.Errorf("ModelConfigReasoningCursor() = %d, want 1 (low)", m.ModelConfigReasoningCursor())
	}

	// Press Enter at config panel to confirm and close.
	m5, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m5.(tui.Model)

	// Overlay should be closed.
	if m.OverlayActive() {
		t.Error("overlay must be closed after Enter in config panel")
	}

	// Returned batch must contain a ModelSelectedMsg with ReasoningEffort="low".
	if cmds == nil {
		t.Fatal("expected cmd from Enter in config panel")
	}
	batchMsg := cmds()
	batch, ok := batchMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg from config panel Enter, got %T", batchMsg)
	}
	var foundSelected *tui.ModelSelectedMsg
	for _, cmdFn := range batch {
		if cmdFn == nil {
			continue
		}
		inner := cmdFn()
		if sel, ok2 := inner.(tui.ModelSelectedMsg); ok2 {
			sel := sel
			foundSelected = &sel
		}
	}
	if foundSelected == nil {
		t.Fatal("batch must contain a ModelSelectedMsg")
	}
	if foundSelected.ReasoningEffort != "low" {
		t.Errorf("ModelSelectedMsg.ReasoningEffort = %q, want %q", foundSelected.ReasoningEffort, "low")
	}

	// Apply ModelSelectedMsg to update model state.
	m6, _ := m.Update(*foundSelected)
	m = m6.(tui.Model)

	if m.SelectedModel() != "deepseek-reasoner" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "deepseek-reasoner")
	}
	if m.SelectedReasoningEffort() != "low" {
		t.Errorf("SelectedReasoningEffort() = %q, want %q", m.SelectedReasoningEffort(), "low")
	}
}

// TestTUI137_ModelOverlayEnterAtLevel1ClosesAndSetsModel tests that using the
// two-level hierarchy to select deepseek-reasoner and configuring reasoning effort
// works end-to-end.
func TestTUI137_ModelOverlayEnterAtLevel1ClosesAndSetsModel(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// Navigate to DeepSeek provider at level 0.
	for i := 0; i < len(m.ModelSwitcher().Providers()); i++ {
		if m.ModelSwitcher().Providers()[m.ModelSwitcher().ProviderCursorIndex()].Label == "DeepSeek" {
			break
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}
	// Drill into DeepSeek (Enter at level 0).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	// Navigate to deepseek-reasoner (level 1).
	for i := 0; i < 10; i++ {
		entry, _ := m.ModelSwitcher().Accept()
		if entry.ID == "deepseek-reasoner" {
			break
		}
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m3.(tui.Model)
	}

	// Enter config panel.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Navigate to Reasoning section (section 2) via j twice.
	for i := 0; i < 2; i++ {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = m3.(tui.Model)
	}

	// Navigate down to "low" in the reasoning cursor (j navigates cursor when in reasoning section).
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = m4.(tui.Model)

	// Press Enter at config panel to confirm.
	m5, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m5.(tui.Model)

	// Overlay should be closed.
	if m.OverlayActive() {
		t.Error("overlay must be closed after Enter at config panel")
	}

	// Execute returned batch to get ModelSelectedMsg.
	if cmds == nil {
		t.Fatal("expected cmd from Enter at config panel")
	}
	batchMsg := cmds()
	batch, ok := batchMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", batchMsg)
	}
	var selected *tui.ModelSelectedMsg
	for _, cmdFn := range batch {
		if cmdFn == nil {
			continue
		}
		if sel, ok2 := cmdFn().(tui.ModelSelectedMsg); ok2 {
			sel := sel
			selected = &sel
		}
	}
	if selected == nil {
		t.Fatal("batch must contain a ModelSelectedMsg")
	}
	if selected.ReasoningEffort != "low" {
		t.Errorf("ModelSelectedMsg.ReasoningEffort = %q, want %q", selected.ReasoningEffort, "low")
	}

	// Apply ModelSelectedMsg to update model state.
	m6, _ := m.Update(*selected)
	m = m6.(tui.Model)

	if m.SelectedModel() != "deepseek-reasoner" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "deepseek-reasoner")
	}
	if m.SelectedReasoningEffort() != "low" {
		t.Errorf("SelectedReasoningEffort() = %q, want %q", m.SelectedReasoningEffort(), "low")
	}
}

// TestTUI137_ModelAppearsInHelpCommand verifies /model appears in /help output.
func TestTUI137_ModelAppearsInHelpCommand(t *testing.T) {
	// A taller window is needed so /model is within the help dialog's first
	// visible page now that /cost and /config have been added to the
	// (alphabetically-earlier) command set.
	m := initModel(t, 80, 30)
	m = sendSlashCommand(m, "/help")

	v := m.View()
	if !strings.Contains(v, "model") {
		t.Errorf("/model must appear in /help view:\n%s", v)
	}
}

// TestTUI137_ModelSelectedMsgUpdatesState verifies ModelSelectedMsg handler
// updates selectedModel, selectedProvider, and selectedReasoningEffort.
func TestTUI137_ModelSelectedMsgUpdatesState(t *testing.T) {
	m := initModel(t, 80, 24)

	msg := tui.ModelSelectedMsg{
		ModelID:         "o4-mini",
		Provider:        "openai",
		ReasoningEffort: "medium",
	}
	m2, _ := m.Update(msg)
	m = m2.(tui.Model)

	if m.SelectedModel() != "o4-mini" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "o4-mini")
	}
	if m.SelectedReasoningEffort() != "medium" {
		t.Errorf("SelectedReasoningEffort() = %q, want %q", m.SelectedReasoningEffort(), "medium")
	}
}

// TestTUI137_ModelSelectedMsgSetsStatusMsg verifies ModelSelectedMsg sets the status bar message.
func TestTUI137_ModelSelectedMsgSetsStatusMsg(t *testing.T) {
	m := initModel(t, 80, 24)

	msg := tui.ModelSelectedMsg{
		ModelID:         "o3",
		Provider:        "openai",
		ReasoningEffort: "high",
	}
	m2, _ := m.Update(msg)
	m = m2.(tui.Model)

	if !strings.Contains(m.StatusMsg(), "Model:") {
		t.Errorf("StatusMsg() must contain 'Model:' after ModelSelectedMsg, got %q", m.StatusMsg())
	}
	if !strings.Contains(m.StatusMsg(), "high") {
		t.Errorf("StatusMsg() must contain reasoning effort 'high', got %q", m.StatusMsg())
	}
}

// TestTUI137_ModelOverlayUpDownNavigates verifies Up/Down keys navigate the provider list
// at level 0, and navigate the model list at level 1.
func TestTUI137_ModelOverlayUpDownNavigates(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/model")

	// At level 0 (provider list): Down moves the provider cursor.
	// Navigate to OpenAI provider.
	for i := 0; i < len(m.ModelSwitcher().Providers()); i++ {
		if m.ModelSwitcher().Providers()[m.ModelSwitcher().ProviderCursorIndex()].Label == "OpenAI" {
			break
		}
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = m2.(tui.Model)
	}

	// Drill into OpenAI (Enter at level 0).
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// At level 1: Down moves model cursor. OpenAI has gpt-4.1 and gpt-4.1-mini.
	// Navigate to gpt-4.1-mini by pressing Down.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m4.(tui.Model)

	// The view should now show gpt-4.1-mini highlighted.
	v := m.View()
	if !strings.Contains(v, "GPT-4.1 Mini") {
		t.Errorf("view must contain 'GPT-4.1 Mini' after Down at level 1:\n%s", v)
	}

	// Press Up to go back to gpt-4.1.
	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m5.(tui.Model)

	// Verify gpt-4.1 is selected.
	entry, _ := m.ModelSwitcher().Accept()
	if entry.ID != "gpt-4.1" {
		t.Errorf("after Down+Up at level 1: selected model = %q, want %q", entry.ID, "gpt-4.1")
	}
}

// TestTUI137_ModelCommandInSlashCompleteDropdown verifies /model appears in the
// slash-complete suggestions when typing "/".
func TestTUI137_ModelCommandInSlashCompleteDropdown(t *testing.T) {
	m := initModel(t, 80, 24)
	// Type "/m" to trigger autocomplete.
	m = typeIntoModel(m, "/m")

	v := m.View()
	// The slash-complete dropdown should contain "model".
	if !strings.Contains(v, "model") {
		t.Errorf("slash-complete dropdown must contain 'model' when typing '/m':\n%s", v)
	}
}

// TestTUI137_SelectedModelInitialisedFromConfig verifies that SelectedModel()
// is initialised from TUIConfig.Model.
func TestTUI137_SelectedModelInitialisedFromConfig(t *testing.T) {
	cfg := tui.DefaultTUIConfig()
	cfg.Model = "gpt-4.1-mini"
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(tui.Model)

	if m.SelectedModel() != "gpt-4.1-mini" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "gpt-4.1-mini")
	}
}

// TestTUI137_ModelOverlaySubmitViaCommandSubmittedMsg verifies that dispatching
// CommandSubmittedMsg{Value:"/model"} also opens the overlay.
func TestTUI137_ModelOverlaySubmitViaCommandSubmittedMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "/model"})
	m = m2.(tui.Model)

	if !m.OverlayActive() {
		t.Fatal("OverlayActive() must be true after CommandSubmittedMsg{/model}")
	}
	if m.ActiveOverlay() != "model" {
		t.Errorf("ActiveOverlay() = %q, want %q", m.ActiveOverlay(), "model")
	}
}
