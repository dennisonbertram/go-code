package tui_test

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ─── Provider overlay tests ──────────────────────────────────────────────────
//
// The /provider command was removed from the command registry (users cannot
// type it directly). The provider overlay is now only accessible via the
// /model config panel. These tests use OverlayOpenMsg{Kind:"provider"} to open
// the overlay directly for testing its internal behaviour.

// openProviderOverlay is a helper that opens the provider overlay via
// OverlayOpenMsg, matching the internal path used by the /model config panel.
func openProviderOverlay(m tui.Model) tui.Model {
	m2, _ := m.Update(tui.OverlayOpenMsg{Kind: "provider"})
	return m2.(tui.Model)
}

func TestProviderCommand_SubmitViaCommandSubmittedMsgIsUnknown(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/provider")

	if m.OverlayActive() {
		t.Fatal("provider overlay must not open from removed /provider command")
	}
	if !strings.Contains(m.StatusMsg(), "Unknown command: /provider") {
		t.Fatalf("StatusMsg() = %q, want unknown-command hint for /provider", m.StatusMsg())
	}
}

// TestProviderOverlay_ContainsExpectedContent verifies the overlay view shows
// the title and both gateway options.
func TestProviderOverlay_ContainsExpectedContent(t *testing.T) {
	m := initModel(t, 80, 24)
	m = openProviderOverlay(m)

	v := m.View()
	if !strings.Contains(v, "Routing Gateway") {
		t.Errorf("View() with provider overlay must contain 'Routing Gateway'; got:\n%s", v)
	}
	if !strings.Contains(v, "Direct") {
		t.Errorf("View() with provider overlay must contain 'Direct'; got:\n%s", v)
	}
	if !strings.Contains(v, "OpenRouter") {
		t.Errorf("View() with provider overlay must contain 'OpenRouter'; got:\n%s", v)
	}
}

// TestProviderOverlay_Navigation verifies Up/Down moves the cursor.
func TestProviderOverlay_Navigation(t *testing.T) {
	m := initModel(t, 80, 24)
	m = openProviderOverlay(m)

	// Default cursor is at index 0 (Direct). Press Down to move to OpenRouter.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(tui.Model)

	// Press Enter to confirm and capture the msg.
	m3, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	if cmds == nil {
		t.Fatal("expected cmd from Enter on provider overlay")
	}
	msg := cmds()
	gw, ok := msg.(tui.GatewaySelectedMsg)
	if !ok {
		t.Fatalf("expected GatewaySelectedMsg, got %T", msg)
	}
	if gw.Gateway != "openrouter" {
		t.Errorf("GatewaySelectedMsg.Gateway = %q, want %q", gw.Gateway, "openrouter")
	}
}

// TestProviderOverlay_NavigationWrap verifies cursor wraps around.
func TestProviderOverlay_NavigationWrap(t *testing.T) {
	m := initModel(t, 80, 24)
	m = openProviderOverlay(m)

	// At index 0 (Direct), press Up to wrap to last (OpenRouter).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(tui.Model)

	// Press Enter to confirm.
	m3, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	if cmds == nil {
		t.Fatal("expected cmd from Enter")
	}
	msg := cmds()
	gw, ok := msg.(tui.GatewaySelectedMsg)
	if !ok {
		t.Fatalf("expected GatewaySelectedMsg, got %T", msg)
	}
	if gw.Gateway != "openrouter" {
		t.Errorf("GatewaySelectedMsg.Gateway = %q, want %q after Up wrap", gw.Gateway, "openrouter")
	}
}

// TestProviderOverlay_EscapeClosesWithoutChange verifies Escape closes the overlay
// without changing the selected gateway.
func TestProviderOverlay_EscapeClosesWithoutChange(t *testing.T) {
	resetGatewayConfig(t)
	t.Cleanup(func() { resetGatewayConfig(t) })

	m := initModel(t, 80, 24)
	// Ensure gateway is "" (direct) initially.
	if m.SelectedGateway() != "" {
		t.Fatalf("precondition: SelectedGateway() = %q, want empty", m.SelectedGateway())
	}

	m = openProviderOverlay(m)

	// Navigate to OpenRouter.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(tui.Model)

	// Press Escape — should close without emitting GatewaySelectedMsg.
	m, _ = sendEscape(m)

	if m.OverlayActive() {
		t.Error("OverlayActive() must be false after Escape")
	}
	if m.ActiveOverlay() != "" {
		t.Errorf("ActiveOverlay() must be empty after Escape, got %q", m.ActiveOverlay())
	}
	// Gateway must not have changed.
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() must still be empty after Escape, got %q", m.SelectedGateway())
	}
}

// TestProviderOverlay_EnterEmitsMsg verifies Enter on OpenRouter emits GatewaySelectedMsg.
func TestProviderOverlay_EnterEmitsMsg(t *testing.T) {
	m := initModel(t, 80, 24)
	m = openProviderOverlay(m)

	// Navigate to OpenRouter (index 1).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(tui.Model)

	// Press Enter.
	m3, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m3.(tui.Model)

	// Overlay should be closed.
	if m.OverlayActive() {
		t.Error("overlay must be closed after Enter")
	}

	if cmds == nil {
		t.Fatal("expected cmd from Enter on provider overlay")
	}
	msg := cmds()
	gw, ok := msg.(tui.GatewaySelectedMsg)
	if !ok {
		t.Fatalf("expected GatewaySelectedMsg, got %T", msg)
	}
	if gw.Gateway != "openrouter" {
		t.Errorf("GatewaySelectedMsg.Gateway = %q, want %q", gw.Gateway, "openrouter")
	}
}

// TestGatewaySelectedMsg_SetsGateway verifies GatewaySelectedMsg updates the gateway state.
func TestGatewaySelectedMsg_SetsGateway(t *testing.T) {
	m := initModel(t, 80, 24)

	// Set to openrouter.
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m2.(tui.Model)

	if m.SelectedGateway() != "openrouter" {
		t.Errorf("SelectedGateway() = %q, want %q", m.SelectedGateway(), "openrouter")
	}

	// Set back to direct.
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m3.(tui.Model)

	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty", m.SelectedGateway())
	}
}

// TestGatewaySelectedMsg_SetsStatusMsg verifies GatewaySelectedMsg sets the status bar message.
func TestGatewaySelectedMsg_SetsStatusMsg(t *testing.T) {
	m := initModel(t, 80, 24)

	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m2.(tui.Model)
	if !strings.Contains(m.StatusMsg(), "Gateway: OpenRouter") {
		t.Errorf("StatusMsg() = %q, want containing 'Gateway: OpenRouter'", m.StatusMsg())
	}

	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m3.(tui.Model)
	if !strings.Contains(m.StatusMsg(), "Gateway: Direct") {
		t.Errorf("StatusMsg() = %q, want containing 'Gateway: Direct'", m.StatusMsg())
	}
}

// TestProviderOverlay_ViewDiffersFromViewport verifies that the provider overlay
// produces different View() output than the normal viewport.
func TestProviderOverlay_ViewDiffersFromViewport(t *testing.T) {
	m := initModel(t, 80, 24)
	viewBefore := m.View()

	m = openProviderOverlay(m)
	viewAfter := m.View()

	if viewAfter == viewBefore {
		t.Error("View() must change when provider overlay is active")
	}
}

// TestProviderOverlay_ConcurrentAccess verifies no race condition with value-type copies.
func TestProviderOverlay_ConcurrentAccess(t *testing.T) {
	base := initModel(t, 80, 24)
	base = openProviderOverlay(base)

	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			m := base
			_ = m.View()
			_ = m.OverlayActive()
			_ = m.ActiveOverlay()
			_ = m.SelectedGateway()
			m, _ = sendEscape(m)
			_ = m.OverlayActive()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestProviderOverlay_EnterOnDirectEmitsEmptyGateway verifies Enter on Direct
// (index 0) emits GatewaySelectedMsg with empty gateway.
func TestProviderOverlay_EnterOnDirectEmitsEmptyGateway(t *testing.T) {
	m := initModel(t, 80, 24)
	m = openProviderOverlay(m)

	// Index 0 is already Direct. Press Enter.
	m2, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(tui.Model)

	if m.OverlayActive() {
		t.Error("overlay must be closed after Enter")
	}
	if cmds == nil {
		t.Fatal("expected cmd from Enter")
	}
	msg := cmds()
	gw, ok := msg.(tui.GatewaySelectedMsg)
	if !ok {
		t.Fatalf("expected GatewaySelectedMsg, got %T", msg)
	}
	if gw.Gateway != "" {
		t.Errorf("GatewaySelectedMsg.Gateway = %q, want empty for Direct", gw.Gateway)
	}
}

// resetGatewayConfig resets the persisted gateway configuration to empty (direct)
// by applying a GatewaySelectedMsg{Gateway: ""}. This is used as a t.Cleanup
// function in tests that write "openrouter" to the persistent config, preventing
// contamination of subsequent test runs.
func resetGatewayConfig(t *testing.T) {
	t.Helper()
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	_ = m3
}

// ─── effectiveModelAndProvider regression tests ───────────────────────────────

// TestEffectiveModelAndProvider_DirectGateway verifies that with the direct gateway
// selected, the model state is set correctly (no OpenRouter slug transformation).
// effectiveModelAndProvider() returns selectedModel and selectedProvider unchanged
// when selectedGateway is "".
func TestEffectiveModelAndProvider_DirectGateway(t *testing.T) {
	m := initModel(t, 80, 24)

	// Explicitly clear the gateway to direct and set a known model.
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "gpt-4.1",
		Provider: "openai",
	})
	m = m3.(tui.Model)

	// Verify the state that effectiveModelAndProvider() reads.
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty (direct)", m.SelectedGateway())
	}
	if m.SelectedModel() != "gpt-4.1" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "gpt-4.1")
	}
	// The status bar label should NOT contain the OpenRouter indicator.
	// StatusMsg shows "Model: GPT-4.1" after ModelSelectedMsg — verify that contains the model name.
	if !strings.Contains(m.StatusMsg(), "GPT-4.1") {
		t.Errorf("StatusMsg() = %q, want containing model name 'GPT-4.1'", m.StatusMsg())
	}
	if strings.Contains(m.StatusMsg(), "↗OR") {
		t.Errorf("StatusMsg() = %q, must NOT contain '↗OR' for direct gateway", m.StatusMsg())
	}
}

// TestEffectiveModelAndProvider_OpenRouterGateway verifies that when the OpenRouter
// gateway is active, the state that effectiveModelAndProvider reads contains
// selectedModel = "claude-sonnet-4-6" and selectedGateway = "openrouter".
// effectiveModelAndProvider() maps selectedModel to the OpenRouter slug and returns
// "openrouter" as the provider.
func TestEffectiveModelAndProvider_OpenRouterGateway(t *testing.T) {
	t.Cleanup(func() { resetGatewayConfig(t) })
	m := initModel(t, 80, 24)

	// Select claude-sonnet-4-6 with anthropic provider, then set openrouter gateway.
	m2, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "claude-sonnet-4-6",
		Provider: "anthropic",
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m3.(tui.Model)

	// Verify the state effectiveModelAndProvider reads from.
	if m.SelectedGateway() != "openrouter" {
		t.Errorf("SelectedGateway() = %q, want %q", m.SelectedGateway(), "openrouter")
	}
	if m.SelectedModel() != "claude-sonnet-4-6" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "claude-sonnet-4-6")
	}
	// GatewaySelectedMsg sets StatusMsg("Gateway: OpenRouter") — confirms handler ran.
	if !strings.Contains(m.StatusMsg(), "OpenRouter") {
		t.Errorf("StatusMsg() = %q, want containing 'OpenRouter' after gateway set", m.StatusMsg())
	}
}

// TestEffectiveModelAndProvider_OpenRouterUnknownModel verifies that an unknown model
// ID does not crash the system when gateway=openrouter. effectiveModelAndProvider()
// calls modelswitcher.OpenRouterSlug which falls back to the raw ID.
func TestEffectiveModelAndProvider_OpenRouterUnknownModel(t *testing.T) {
	t.Cleanup(func() { resetGatewayConfig(t) })
	m := initModel(t, 80, 24)

	// Set an unknown model ID via ModelSelectedMsg, then set openrouter gateway.
	m2, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "unknown-model-xyz",
		Provider: "custom",
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m3.(tui.Model)

	// Verify state — no crash and gateway is set correctly.
	if m.SelectedModel() != "unknown-model-xyz" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "unknown-model-xyz")
	}
	if m.SelectedGateway() != "openrouter" {
		t.Errorf("SelectedGateway() = %q, want %q", m.SelectedGateway(), "openrouter")
	}
	// View() must not panic.
	v := m.View()
	if v == "" {
		t.Error("View() must return non-empty string for unknown model + openrouter gateway")
	}
}

// TestEffectiveModelAndProvider_EmptyModel verifies that an empty selectedModel with
// gateway=openrouter does not cause a panic in effectiveModelAndProvider().
func TestEffectiveModelAndProvider_EmptyModel(t *testing.T) {
	t.Cleanup(func() { resetGatewayConfig(t) })
	m := initModel(t, 80, 24)

	// Set gateway without setting an explicit model.
	// (Use a fresh model with empty model ID by injecting an empty ModelSelectedMsg.)
	m2, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "",
		Provider: "",
	})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m3.(tui.Model)

	if m.SelectedGateway() != "openrouter" {
		t.Errorf("SelectedGateway() = %q, want %q", m.SelectedGateway(), "openrouter")
	}
	// Must not panic; View() must be renderable.
	v := m.View()
	if v == "" {
		t.Error("View() must not be empty with empty model and openrouter gateway")
	}
}

// TestEffectiveModelAndProvider_DirectGatewayNormalizesOpenRouterSlug verifies
// that when the direct gateway is active and selectedModel contains an
// OpenRouter-qualified slug (e.g. "deepseek/deepseek-v4-flash"), the state is
// set correctly for the downstream effectiveModelAndProvider call to normalise
// the slug before sending to the direct provider.
func TestEffectiveModelAndProvider_DirectGatewayNormalizesOpenRouterSlug(t *testing.T) {
	resetGatewayConfig(t)
	t.Cleanup(func() { resetGatewayConfig(t) })

	m := initModel(t, 80, 24)

	// Direct gateway with an OpenRouter-qualified slug (as would happen when the
	// model list was sourced from OpenRouter and the user selected deepseek).
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "deepseek/deepseek-v4-flash",
		Provider: "deepseek",
	})
	m = m3.(tui.Model)

	// Verify state: gateway is direct, selectedModel is the OR slug.
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty (direct)", m.SelectedGateway())
	}
	if m.SelectedModel() != "deepseek/deepseek-v4-flash" {
		t.Errorf("SelectedModel() = %q, want deepseek/deepseek-v4-flash", m.SelectedModel())
	}
	// View must be renderable (no panic).
	v := m.View()
	if v == "" {
		t.Error("View() must be non-empty")
	}
	// StatusMsg is set by ModelSelectedMsg using the provided model ID.
	if !strings.Contains(m.StatusMsg(), "deepseek/deepseek-v4-flash") {
		t.Errorf("StatusMsg() = %q, want containing the stored model ID", m.StatusMsg())
	}
}

// TestEffectiveModelAndProvider_DirectGatewayNormalizesGenericORPrefix verifies
// state is correctly set when a generic OpenRouter-qualified slug is selected
// with a direct provider gateway.
func TestEffectiveModelAndProvider_DirectGatewayNormalizesGenericORPrefix(t *testing.T) {
	resetGatewayConfig(t)
	t.Cleanup(func() { resetGatewayConfig(t) })

	m := initModel(t, 80, 24)

	// Direct gateway with a generic OpenRouter-qualified slug.
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)
	m3, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:  "novita/deepseek-v4-flash",
		Provider: "deepseek",
	})
	m = m3.(tui.Model)

	// Verify state is set correctly.
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty (direct)", m.SelectedGateway())
	}
	if m.SelectedModel() != "novita/deepseek-v4-flash" {
		t.Errorf("SelectedModel() = %q, want novita/deepseek-v4-flash", m.SelectedModel())
	}
	// View must be renderable.
	v := m.View()
	if v == "" {
		t.Error("View() must be non-empty")
	}
}

// ─── Status bar label composition tests ──────────────────────────────────────

// TestStatusBarLabel_ModelAndReasoningAndGateway verifies that when a model,
// reasoning effort, and openrouter gateway are all active, the StatusMsg set by
// ModelSelectedMsg contains the reasoning effort, and the following GatewaySelectedMsg
// correctly updates SelectedGateway. The label composition (model + reasoning + "↗OR")
// is validated by inspecting the individual state accessors.
func TestStatusBarLabel_ModelAndReasoningAndGateway(t *testing.T) {
	t.Cleanup(func() { resetGatewayConfig(t) })
	m := initModel(t, 80, 24)

	// Select gpt-4.1 with reasoning effort "high".
	m2, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:         "gpt-4.1",
		Provider:        "openai",
		ReasoningEffort: "high",
	})
	m = m2.(tui.Model)

	// Verify ModelSelectedMsg sets StatusMsg containing both model and reasoning.
	if !strings.Contains(m.StatusMsg(), "high") {
		t.Errorf("StatusMsg() after ModelSelectedMsg = %q, want containing 'high'", m.StatusMsg())
	}

	// Activate OpenRouter gateway.
	m3, _ := m.Update(tui.GatewaySelectedMsg{Gateway: "openrouter"})
	m = m3.(tui.Model)

	// Verify the combined state that statusBarModelLabel() composes from.
	if m.SelectedModel() != "gpt-4.1" {
		t.Errorf("SelectedModel() = %q, want %q", m.SelectedModel(), "gpt-4.1")
	}
	if m.SelectedReasoningEffort() != "high" {
		t.Errorf("SelectedReasoningEffort() = %q, want %q", m.SelectedReasoningEffort(), "high")
	}
	if m.SelectedGateway() != "openrouter" {
		t.Errorf("SelectedGateway() = %q, want %q", m.SelectedGateway(), "openrouter")
	}
	// The GatewaySelectedMsg StatusMsg confirms the gateway handler updated state.
	if !strings.Contains(m.StatusMsg(), "Gateway: OpenRouter") {
		t.Errorf("StatusMsg() = %q, want containing 'Gateway: OpenRouter'", m.StatusMsg())
	}
}

// TestStatusBarLabel_ModelOnlyNoReasoningNoGateway verifies that after selecting
// a model with no reasoning effort and setting gateway to direct, the state has
// no reasoning effort and no gateway. The status bar label would be just the model name.
func TestStatusBarLabel_ModelOnlyNoReasoningNoGateway(t *testing.T) {
	m := initModel(t, 80, 24)

	// Explicitly clear gateway to direct.
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)

	// Select model with no reasoning effort.
	m3, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:         "gpt-4.1",
		Provider:        "openai",
		ReasoningEffort: "",
	})
	m = m3.(tui.Model)

	// Verify state: no reasoning, no gateway.
	if m.SelectedReasoningEffort() != "" {
		t.Errorf("SelectedReasoningEffort() = %q, want empty", m.SelectedReasoningEffort())
	}
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty (direct)", m.SelectedGateway())
	}
	// StatusMsg set by ModelSelectedMsg should contain the model display name.
	if !strings.Contains(m.StatusMsg(), "GPT-4.1") {
		t.Errorf("StatusMsg() = %q, want containing 'GPT-4.1'", m.StatusMsg())
	}
	// StatusMsg must NOT contain the OpenRouter gateway suffix.
	if strings.Contains(m.StatusMsg(), "↗OR") {
		t.Errorf("StatusMsg() = %q, must NOT contain '↗OR' with direct gateway", m.StatusMsg())
	}
}

// TestStatusBarLabel_ModelAndReasoningNoGateway verifies that with model + reasoning
// but no gateway, the state has reasoning effort set and no gateway indicator.
func TestStatusBarLabel_ModelAndReasoningNoGateway(t *testing.T) {
	m := initModel(t, 80, 24)

	// Explicitly clear gateway to direct.
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)

	// Select model with reasoning effort "medium".
	m3, _ := m.Update(tui.ModelSelectedMsg{
		ModelID:         "gpt-4.1",
		Provider:        "openai",
		ReasoningEffort: "medium",
	})
	m = m3.(tui.Model)

	// Verify state: reasoning set, no gateway.
	if m.SelectedReasoningEffort() != "medium" {
		t.Errorf("SelectedReasoningEffort() = %q, want %q", m.SelectedReasoningEffort(), "medium")
	}
	if m.SelectedGateway() != "" {
		t.Errorf("SelectedGateway() = %q, want empty (direct)", m.SelectedGateway())
	}
	// StatusMsg set by ModelSelectedMsg should contain the reasoning effort.
	if !strings.Contains(m.StatusMsg(), "medium") {
		t.Errorf("StatusMsg() = %q, want containing 'medium'", m.StatusMsg())
	}
	// StatusMsg must NOT contain the OpenRouter gateway suffix.
	if strings.Contains(m.StatusMsg(), "↗OR") {
		t.Errorf("StatusMsg() = %q, must NOT contain '↗OR' with direct gateway", m.StatusMsg())
	}
}

// ─── Overlay cursor wrap regression tests ────────────────────────────────────

// TestProviderOverlay_DownFromLastWrapsToFirst verifies that pressing Down
// from the last gateway option wraps to the first (Direct), regardless of the
// initial selectedGateway state. The test navigates the overlay to the last
// option (OpenRouter), then presses Down once more to wrap to Direct.
func TestProviderOverlay_DownFromLastWrapsToFirst(t *testing.T) {
	m := initModel(t, 80, 24)

	// Explicitly force gateway to "" so the overlay opens at index 0 (Direct).
	m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: ""})
	m = m2.(tui.Model)

	// Open the provider overlay — selectedGateway="" so cursor starts at 0 (Direct).
	m = openProviderOverlay(m)

	// Press Down once: cursor moves from 0 (Direct) to 1 (OpenRouter).
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(tui.Model)

	// Press Down again: cursor wraps from 1 (last) back to 0 (Direct).
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m4.(tui.Model)

	// Press Enter — should emit GatewaySelectedMsg with empty gateway (Direct = index 0).
	m5, cmds := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m5.(tui.Model)

	if cmds == nil {
		t.Fatal("expected cmd from Enter after Down wrap-around")
	}
	msg := cmds()
	gw, ok := msg.(tui.GatewaySelectedMsg)
	if !ok {
		t.Fatalf("expected GatewaySelectedMsg, got %T", msg)
	}
	if gw.Gateway != "" {
		t.Errorf("GatewaySelectedMsg.Gateway = %q, want empty (Direct) after Down-Down wrap", gw.Gateway)
	}
}

// ─── Concurrent GatewaySelectedMsg race test ─────────────────────────────────

// TestGatewaySelectedMsg_ConcurrentUpdates verifies that sending multiple
// GatewaySelectedMsgs in concurrent goroutines (each with its own model copy)
// does not trigger the race detector. BubbleTea value semantics make this safe.
func TestGatewaySelectedMsg_ConcurrentUpdates(t *testing.T) {
	t.Cleanup(func() { resetGatewayConfig(t) })
	base := initModel(t, 80, 24)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m := base // own copy per goroutine
			gw := "openrouter"
			if idx%2 == 0 {
				gw = ""
			}
			m2, _ := m.Update(tui.GatewaySelectedMsg{Gateway: gw})
			m = m2.(tui.Model)
			_ = m.SelectedGateway()
			_ = m.View()
		}(i)
	}
	wg.Wait()
}

// TestModelConfigAccessors_DefaultZeroValues verifies that the config panel
// accessor methods exported for test observability return their zero values
// on a freshly initialised model.
func TestModelConfigAccessors_DefaultZeroValues(t *testing.T) {
	t.Parallel()
	m := initModel(t, 80, 24)

	if got := m.ModelConfigGatewayCursor(); got != 0 {
		t.Errorf("ModelConfigGatewayCursor() = %d, want 0", got)
	}
	if got := m.ModelConfigKeyInputMode(); got != false {
		t.Errorf("ModelConfigKeyInputMode() = %v, want false", got)
	}
	if got := m.ModelConfigKeyInput(); got != "" {
		t.Errorf("ModelConfigKeyInput() = %q, want empty", got)
	}
}
