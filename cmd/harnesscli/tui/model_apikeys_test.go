package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// ─── /keys command tests ─────────────────────────────────────────────────────

// TestKeysCommand_OpensOverlay verifies /keys opens the apikeys overlay.
func TestKeysCommand_OpensOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	if !m.OverlayActive() {
		t.Fatal("OverlayActive() must be true after /keys")
	}
	if m.ActiveOverlay() != "apikeys" {
		t.Errorf("ActiveOverlay(): want %q, got %q", "apikeys", m.ActiveOverlay())
	}
}

// TestKeysOverlay_ProvidersLoadedMsg verifies ProvidersLoadedMsg populates the
// provider list and the view shows provider names.
func TestKeysOverlay_ProvidersLoadedMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Inject ProvidersLoadedMsg.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: true, APIKeyEnv: "OPENAI_API_KEY"},
			{Name: "groq", Configured: false, APIKeyEnv: "GROQ_API_KEY"},
		},
	})
	m = m2.(tui.Model)

	providers := m.APIKeyProviders()
	if len(providers) != 2 {
		t.Fatalf("APIKeyProviders() length: got %d, want 2", len(providers))
	}
	if providers[0].Name != "openai" {
		t.Errorf("providers[0].Name = %q, want %q", providers[0].Name, "openai")
	}

	// View should contain the provider names.
	v := m.View()
	if !strings.Contains(v, "openai") {
		t.Errorf("View() must contain 'openai'; got:\n%s", v)
	}
	if !strings.Contains(v, "groq") {
		t.Errorf("View() must contain 'groq'; got:\n%s", v)
	}
}

func TestKeysOverlay_ShowsCodexSubscriptionStatusWithoutEditingAPIKey(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")
	m2, _ := m.Update(tui.ProvidersLoadedMsg{Providers: []tui.ProviderInfo{
		{Name: "codex-subscription", Configured: true, AuthType: "subscription"},
	}})
	m = m2.(tui.Model)
	view := m.View()
	if !strings.Contains(view, "ChatGPT subscription") || !strings.Contains(view, "connected") {
		t.Fatalf("Codex subscription status missing from /keys view:\n%s", view)
	}
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)
	if m.APIKeyInputMode() {
		t.Fatal("subscription row must not open API-key editing")
	}
}

// TestKeysOverlay_EnterInputMode verifies Enter with providers loaded enters input mode.
func TestKeysOverlay_EnterInputMode(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: true, APIKeyEnv: "OPENAI_API_KEY"},
		},
	})
	m = m2.(tui.Model)

	// Press Enter to enter input mode.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	if !m.APIKeyInputMode() {
		t.Error("APIKeyInputMode() must be true after Enter")
	}
}

// TestKeysOverlay_EscFromInputModeReturnsToList verifies Esc in input mode goes back to list.
func TestKeysOverlay_EscFromInputModeReturnsToList(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers and enter input mode.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: true, APIKeyEnv: "OPENAI_API_KEY"},
		},
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	if !m.APIKeyInputMode() {
		t.Fatal("precondition: must be in input mode")
	}

	// Press Escape — should go back to list, not close overlay.
	m, _ = sendEscape(m)
	if m.APIKeyInputMode() {
		t.Error("APIKeyInputMode() must be false after Esc in input mode")
	}
	if !m.OverlayActive() {
		t.Error("OverlayActive() must still be true after Esc from input mode")
	}
	if m.ActiveOverlay() != "apikeys" {
		t.Errorf("ActiveOverlay() must be 'apikeys' after Esc from input mode, got %q", m.ActiveOverlay())
	}
}

// TestKeysOverlay_EscFromListCloses verifies Esc in list mode closes the overlay.
func TestKeysOverlay_EscFromListCloses(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Press Escape in list mode — should close overlay.
	m, _ = sendEscape(m)
	if m.OverlayActive() {
		t.Error("OverlayActive() must be false after Esc in list mode")
	}
	if m.ActiveOverlay() != "" {
		t.Errorf("ActiveOverlay() must be empty after Esc, got %q", m.ActiveOverlay())
	}
}

// TestKeysOverlay_TypingAccumulatesInput verifies typing chars accumulates in APIKeyInput().
func TestKeysOverlay_TypingAccumulatesInput(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers and enter input mode.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: false, APIKeyEnv: "OPENAI_API_KEY"},
		},
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Type some characters.
	for _, r := range "sk-test" {
		m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m4.(tui.Model)
	}

	if m.APIKeyInput() != "sk-test" {
		t.Errorf("APIKeyInput() = %q, want %q", m.APIKeyInput(), "sk-test")
	}
}

// TestKeysOverlay_BackspaceRemovesChar verifies backspace removes the last character.
func TestKeysOverlay_BackspaceRemovesChar(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers and enter input mode.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: false, APIKeyEnv: "OPENAI_API_KEY"},
		},
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Type "abc".
	for _, r := range "abc" {
		m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m4.(tui.Model)
	}

	// Press backspace.
	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m5.(tui.Model)

	if m.APIKeyInput() != "ab" {
		t.Errorf("APIKeyInput() after backspace = %q, want %q", m.APIKeyInput(), "ab")
	}
}

// TestKeysOverlay_CtrlUClearsInput verifies Ctrl+U clears the input.
func TestKeysOverlay_CtrlUClearsInput(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers and enter input mode.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: false, APIKeyEnv: "OPENAI_API_KEY"},
		},
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Type "sk-test-key".
	for _, r := range "sk-test-key" {
		m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m4.(tui.Model)
	}

	// Press Ctrl+U.
	m5, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = m5.(tui.Model)

	if m.APIKeyInput() != "" {
		t.Errorf("APIKeyInput() after Ctrl+U = %q, want empty", m.APIKeyInput())
	}
}

// TestKeysOverlay_EnterWithInputEmitsSetMsg verifies Enter with non-empty input
// emits a command that produces APIKeySetMsg and exits input mode.
func TestKeysOverlay_EnterWithInputEmitsSetMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers and enter input mode.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "groq", Configured: false, APIKeyEnv: "GROQ_API_KEY"},
		},
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Type a key.
	for _, r := range "gsk-key-123" {
		m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m4.(tui.Model)
	}

	// Press Enter to confirm.
	m5, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m5.(tui.Model)

	// Should exit input mode.
	if m.APIKeyInputMode() {
		t.Error("APIKeyInputMode() must be false after Enter with input")
	}
	if m.APIKeyInput() != "" {
		t.Errorf("APIKeyInput() must be empty after confirm, got %q", m.APIKeyInput())
	}

	// The returned cmd should be non-nil (it calls setProviderKeyCmd).
	if cmds == nil {
		t.Fatal("expected non-nil cmd from Enter with input")
	}
	// Execute the cmd — it will try to connect to a server that doesn't exist,
	// so it will return a ProvidersLoadedMsg (fallback on error).
	msg := cmds()
	// We can't guarantee the HTTP call succeeds in tests, so we just verify
	// the cmd returned a non-nil message.
	if msg == nil {
		t.Error("cmd() returned nil msg")
	}
}

// TestKeysCommand_InHelpList verifies /keys appears in /help output.
func TestKeysCommand_InHelpList(t *testing.T) {
	// A taller window is needed so /keys is within the help dialog's first
	// visible page now that /cost and /config have been added to the
	// (alphabetically-earlier) command set.
	m := initModel(t, 80, 30)
	m = sendSlashCommand(m, "/help")

	v := m.View()
	if !strings.Contains(v, "keys") {
		t.Errorf("/keys must appear in /help view:\n%s", v)
	}
}

// TestKeysOverlay_SubmitViaCommandSubmittedMsg verifies CommandSubmittedMsg{/keys}
// also opens the overlay.
func TestKeysOverlay_SubmitViaCommandSubmittedMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m2, _ := m.Update(inputarea.CommandSubmittedMsg{Value: "/keys"})
	m = m2.(tui.Model)

	if !m.OverlayActive() {
		t.Fatal("OverlayActive() must be true after CommandSubmittedMsg{/keys}")
	}
	if m.ActiveOverlay() != "apikeys" {
		t.Errorf("ActiveOverlay() = %q, want %q", m.ActiveOverlay(), "apikeys")
	}
}

// TestKeysOverlay_Navigation verifies Up/Down moves the cursor in the provider list.
func TestKeysOverlay_Navigation(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/keys")

	// Load providers.
	m2, _ := m.Update(tui.ProvidersLoadedMsg{
		Providers: []tui.ProviderInfo{
			{Name: "openai", Configured: true, APIKeyEnv: "OPENAI_API_KEY"},
			{Name: "groq", Configured: false, APIKeyEnv: "GROQ_API_KEY"},
			{Name: "anthropic", Configured: true, APIKeyEnv: "ANTHROPIC_API_KEY"},
		},
	})
	m = m2.(tui.Model)

	// Press Down to move from index 0 to index 1.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)

	// Enter input mode at index 1 (groq).
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(tui.Model)

	// The view should show the groq provider name in input mode.
	v := m.View()
	if !strings.Contains(v, "groq") {
		t.Errorf("View in input mode must show provider name 'groq'; got:\n%s", v)
	}
}

// TestKeysOverlay_ViewDiffersFromViewport verifies that the /keys overlay
// produces different View() output than the normal viewport.
func TestKeysOverlay_ViewDiffersFromViewport(t *testing.T) {
	m := initModel(t, 80, 24)
	viewBefore := m.View()

	m = sendSlashCommand(m, "/keys")
	viewAfter := m.View()

	if viewAfter == viewBefore {
		t.Error("View() must change when apikeys overlay is active")
	}
}

// TestKeysOverlay_APIKeySetMsgSetsStatusMsg verifies APIKeySetMsg updates the status bar.
func TestKeysOverlay_APIKeySetMsgSetsStatusMsg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.APIKeySetMsg{Provider: "groq", Key: "gsk-123"})
	m = m2.(tui.Model)

	if !strings.Contains(m.StatusMsg(), "Key saved for groq") {
		t.Errorf("StatusMsg() = %q, want containing 'Key saved for groq'", m.StatusMsg())
	}
}

// TestKeysCommand_InSlashCompleteDropdown verifies /keys appears in slash-complete
// suggestions when typing "/k".
func TestKeysCommand_InSlashCompleteDropdown(t *testing.T) {
	m := initModel(t, 80, 24)
	m = typeIntoModel(m, "/k")

	v := m.View()
	if !strings.Contains(v, "keys") {
		t.Errorf("slash-complete dropdown must contain 'keys' when typing '/k':\n%s", v)
	}
}
