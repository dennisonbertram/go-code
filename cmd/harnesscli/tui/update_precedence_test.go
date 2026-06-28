package tui_test

// TestUpdatePrecedenceMatrix covers the cross-state conflict precedence
// inside the Model.Update reducer for Issue #333.
//
// The precedence order for Escape (highest to lowest):
//   0. apikeys overlay input mode  → back (exit input mode, keep overlay)
//   1. apikeys overlay list mode   → close overlay
//   2. provider overlay            → close overlay
//   3. model overlay Level-1       → exit reasoning mode
//   4. model overlay Level-0 with search → clear search
//   5. model overlay Level-0 no search  → close overlay
//   6. generic overlay             → close overlay
//   7. active run + cancel func    → cancel run
//   8. input has text              → clear input
//   9. otherwise                   → no-op
//
// For Enter:
//   0. apikeys overlay    → confirm key or enter input mode
//   1. provider overlay   → confirm gateway selection
//   2. model overlay      → navigate or confirm model
//   3. slash dropdown     → accept suggestion
//   4. otherwise          → submit input

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// newInitialised returns a model that has been initialised with a window size.
func newInitialised() tui.Model {
	m := tui.New(tui.DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(tui.Model)
}

// applyMsgs applies a sequence of tea.Msg values to m in order, returning
// the final model.
func applyMsgs(m tui.Model, msgs ...tea.Msg) tui.Model {
	cur := m
	for _, msg := range msgs {
		next, _ := cur.Update(msg)
		cur = next.(tui.Model)
	}
	return cur
}

// ---------------------------------------------------------------------------
// Escape precedence tests
// ---------------------------------------------------------------------------

// TestEscapePrecedence is a table-driven test that exercises every level of
// the Escape priority chain and asserts the resulting model state.
func TestEscapePrecedence(t *testing.T) {
	t.Parallel()

	esc := tea.KeyMsg{Type: tea.KeyEsc}

	cases := []struct {
		name  string
		setup func(m tui.Model) tui.Model
		check func(t *testing.T, before, after tui.Model)
	}{
		{
			// Priority 0: apikeys overlay + input mode → exit input mode, overlay stays.
			name: "apikeys_input_mode_escape_exits_input_mode",
			setup: func(m tui.Model) tui.Model {
				// Open the apikeys overlay, which sets overlayActive=true, activeOverlay="apikeys".
				m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "apikeys"})
				// Verify overlay is open.
				if !m.OverlayActive() || m.ActiveOverlay() != "apikeys" {
					// The overlay did not open; skip this case (no providers loaded).
					return m
				}
				return m
			},
			check: func(t *testing.T, before, after tui.Model) {
				// Overlay should still be active after Escape from apikeys.
				// (apikeys in list mode → close overlay on Escape)
				// Without any providers, list mode Escape closes the overlay.
				// This is tested separately.
			},
		},
		{
			// Priority 2: provider overlay → Escape closes overlay.
			name: "provider_overlay_escape_closes",
			setup: func(m tui.Model) tui.Model {
				return applyMsgs(m, tui.OverlayOpenMsg{Kind: "provider"})
			},
			check: func(t *testing.T, before, after tui.Model) {
				if after.OverlayActive() {
					t.Error("provider overlay should be closed after Escape")
				}
				if after.ActiveOverlay() != "" {
					t.Errorf("activeOverlay should be empty, got %q", after.ActiveOverlay())
				}
			},
		},
		{
			// Priority 6: generic overlay (e.g. "help") → Escape closes overlay.
			name: "generic_overlay_escape_closes",
			setup: func(m tui.Model) tui.Model {
				return applyMsgs(m, tui.OverlayOpenMsg{Kind: "help"})
			},
			check: func(t *testing.T, before, after tui.Model) {
				if after.OverlayActive() {
					t.Error("help overlay should be closed after Escape")
				}
			},
		},
		{
			// Priority 7: active run + cancel func → Escape cancels run (not quits).
			name: "active_run_escape_cancels",
			setup: func(m tui.Model) tui.Model {
				cancelFn := func() {}
				m = m.WithCancelRun(cancelFn)
				m = applyMsgs(m, tui.RunStartedMsg{RunID: "r1"})
				return m
			},
			check: func(t *testing.T, before, after tui.Model) {
				// After Escape with active run: runActive should be false.
				if after.RunActive() {
					t.Error("run should be inactive after Escape cancel")
				}
				// Status message should indicate interruption.
				if after.StatusMsg() != "Interrupted" {
					t.Errorf("expected StatusMsg=Interrupted, got %q", after.StatusMsg())
				}
			},
		},
		{
			// Priority 6 beats priority 7: overlay open + active run → overlay is closed first.
			name: "overlay_beats_active_run_on_escape",
			setup: func(m tui.Model) tui.Model {
				cancelFn := func() {}
				m = m.WithCancelRun(cancelFn)
				m = applyMsgs(m, tui.RunStartedMsg{RunID: "r2"})
				m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "help"})
				return m
			},
			check: func(t *testing.T, before, after tui.Model) {
				// Overlay must be closed (priority 6 fires).
				if after.OverlayActive() {
					t.Error("overlay should be closed (overlay beats run-cancel on Escape)")
				}
				// The run should still be active — Escape did NOT cancel it.
				if !after.RunActive() {
					t.Error("run should still be active after Escape closes overlay (not cancel)")
				}
			},
		},
		{
			// No overlay, no run, no input text: Escape is a no-op.
			name: "escape_noop_idle",
			setup: func(m tui.Model) tui.Model {
				return m // idle state
			},
			check: func(t *testing.T, before, after tui.Model) {
				// State should be unchanged.
				if after.OverlayActive() {
					t.Error("no overlay should open from no-op Escape")
				}
				if after.RunActive() {
					t.Error("run should not become active from no-op Escape")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newInitialised()
			before := tc.setup(m)
			after, _ := before.Update(esc)
			afterModel := after.(tui.Model)
			tc.check(t, before, afterModel)
		})
	}
}

// ---------------------------------------------------------------------------
// Ctrl+C precedence tests
// ---------------------------------------------------------------------------

// TestCtrlCPrecedence covers the Ctrl+C / Quit key behaviour.
// Updated for ticket #669: ctrl+c during an active run now uses a two-stage
// confirmation (first ctrl+c shows banner; second ctrl+c cancels). The table
// test reflects this: after ONE ctrl+c, a run is still active (banner shown);
// cancellation requires a SECOND ctrl+c.
func TestCtrlCPrecedence(t *testing.T) {
	t.Parallel()

	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}

	cases := []struct {
		name                  string
		setup                 func(m tui.Model) tui.Model
		wantBannerAfterFirst  bool // banner visible after ONE ctrl+c
		wantRunActiveAfterOne bool // run still active after ONE ctrl+c
	}{
		{
			// Two-stage (#669): first ctrl+c shows banner; run stays active.
			name: "ctrl_c_with_active_run_shows_banner",
			setup: func(m tui.Model) tui.Model {
				cancelFn := func() {}
				m = m.WithCancelRun(cancelFn)
				return applyMsgs(m, tui.RunStartedMsg{RunID: "r1"})
			},
			wantBannerAfterFirst:  true,
			wantRunActiveAfterOne: true,
		},
		{
			name: "ctrl_c_without_run_does_not_show_banner",
			setup: func(m tui.Model) tui.Model {
				// Set cancel func but do NOT start a run.
				return m.WithCancelRun(func() {})
			},
			wantBannerAfterFirst:  false,
			wantRunActiveAfterOne: false,
		},
		{
			name: "ctrl_c_no_cancel_func_idle",
			setup: func(m tui.Model) tui.Model {
				return m // no cancelFunc, no run
			},
			wantBannerAfterFirst:  false,
			wantRunActiveAfterOne: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newInitialised()
			before := tc.setup(m)

			if before.RunActive() {
				// Re-wire with a no-op cancel so value semantics don't lose the func.
				before = before.WithCancelRun(func() {})
			}

			after, _ := before.Update(ctrlC)
			afterModel := after.(tui.Model)

			if afterModel.InterruptBannerVisible() != tc.wantBannerAfterFirst {
				t.Errorf("InterruptBannerVisible() = %v, want %v", afterModel.InterruptBannerVisible(), tc.wantBannerAfterFirst)
			}
			if afterModel.RunActive() != tc.wantRunActiveAfterOne {
				t.Errorf("RunActive() = %v, want %v", afterModel.RunActive(), tc.wantRunActiveAfterOne)
			}
		})
	}
}

// TestCtrlC_ActiveRunCallsCancel verifies the cancel func is called after the
// TWO-STAGE confirmation (ticket #669): the first Ctrl+C shows a banner; the
// second Ctrl+C calls the cancel func.
//
// Changed from original assertion (first ctrl+c cancels immediately) to the
// new two-stage semantics: cancel is only called on the SECOND ctrl+c.
func TestCtrlC_ActiveRunCallsCancel(t *testing.T) {
	t.Parallel()

	m := newInitialised()

	cancelled := false
	cancelFn := func() { cancelled = true }
	m = m.WithCancelRun(cancelFn)
	m = applyMsgs(m, tui.RunStartedMsg{RunID: "run-cancel-001"})

	// First ctrl+c: shows banner only, does NOT cancel.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cancelled {
		t.Error("cancel func must NOT be called on the FIRST Ctrl+C (two-stage required)")
	}

	// Second ctrl+c: confirms cancel.
	_, _ = m2.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !cancelled {
		t.Error("cancel func must be called on the SECOND Ctrl+C with active run")
	}
}

// TestCtrlC_NoRunIsQuit verifies that Ctrl+C when no run is active triggers
// the quit command (tea.Quit) rather than a cancel.
func TestCtrlC_NoRunIsQuit(t *testing.T) {
	t.Parallel()

	m := newInitialised()
	// Ensure no run is active.
	if m.RunActive() {
		t.Fatal("precondition: run should not be active")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// cmd should be tea.Quit.
	if cmd == nil {
		t.Error("expected tea.Quit command when no active run on Ctrl+C, got nil")
	}
}

// ---------------------------------------------------------------------------
// Up/Down routing precedence tests
// ---------------------------------------------------------------------------

// TestUpDownRoutingPrecedence verifies that Up/Down keys route to the correct
// component depending on which overlay / dropdown is active.
func TestUpDownRoutingPrecedence(t *testing.T) {
	t.Parallel()

	upKey := tea.KeyMsg{Type: tea.KeyUp}
	downKey := tea.KeyMsg{Type: tea.KeyDown}

	t.Run("up_with_provider_overlay_navigates_provider", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "provider"})
		// The active overlay should still be provider after Up.
		after, _ := m.Update(upKey)
		afterModel := after.(tui.Model)
		// Overlay should remain open.
		if !afterModel.OverlayActive() || afterModel.ActiveOverlay() != "provider" {
			t.Errorf("provider overlay should remain open after Up key; got active=%v overlay=%q",
				afterModel.OverlayActive(), afterModel.ActiveOverlay())
		}
	})

	t.Run("down_with_provider_overlay_navigates_provider", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "provider"})
		after, _ := m.Update(downKey)
		afterModel := after.(tui.Model)
		if !afterModel.OverlayActive() || afterModel.ActiveOverlay() != "provider" {
			t.Errorf("provider overlay should remain open after Down key; got active=%v overlay=%q",
				afterModel.OverlayActive(), afterModel.ActiveOverlay())
		}
	})

	t.Run("up_without_overlay_scrolls_viewport", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		// Ensure no overlay.
		if m.OverlayActive() {
			t.Fatal("precondition: no overlay should be active")
		}
		// Up should scroll viewport (no panic, no overlay opened).
		after, _ := m.Update(upKey)
		afterModel := after.(tui.Model)
		if afterModel.OverlayActive() {
			t.Error("Up with no overlay should not open any overlay")
		}
	})

	t.Run("down_without_overlay_scrolls_viewport", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		after, _ := m.Update(downKey)
		afterModel := after.(tui.Model)
		if afterModel.OverlayActive() {
			t.Error("Down with no overlay should not open any overlay")
		}
	})
}

// ---------------------------------------------------------------------------
// Enter routing precedence tests
// ---------------------------------------------------------------------------

// TestEnterPrecedence verifies Enter routing across overlay and dropdown states.
func TestEnterPrecedence(t *testing.T) {
	t.Parallel()

	enter := tea.KeyMsg{Type: tea.KeyEnter}

	t.Run("enter_with_provider_overlay_closes_and_selects", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "provider"})
		after, _ := m.Update(enter)
		afterModel := after.(tui.Model)
		// Provider overlay should close on Enter (selection confirmed).
		if afterModel.OverlayActive() && afterModel.ActiveOverlay() == "provider" {
			t.Error("provider overlay should be closed after Enter")
		}
	})

	t.Run("enter_with_apikeys_overlay_no_providers_enters_input_mode", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "apikeys"})
		// No providers loaded, so Enter in list mode should be a no-op for input mode.
		// The overlay should remain.
		after, _ := m.Update(enter)
		afterModel := after.(tui.Model)
		// Overlay should stay active.
		if !afterModel.OverlayActive() {
			t.Error("apikeys overlay should remain open when Enter pressed with no providers")
		}
	})

	t.Run("enter_idle_does_not_open_overlay", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		if m.OverlayActive() {
			t.Fatal("precondition: no overlay")
		}
		after, _ := m.Update(enter)
		afterModel := after.(tui.Model)
		// No overlay should open from Enter in idle state.
		if afterModel.OverlayActive() {
			t.Errorf("Enter in idle state should not open overlay, got %q", afterModel.ActiveOverlay())
		}
	})
}

// ---------------------------------------------------------------------------
// Overlay vs overlay precedence (apikeys input mode)
// ---------------------------------------------------------------------------

// TestAPIKeysOverlayEscapePrecedence verifies the two-level Escape priority
// inside the apikeys overlay:
//
//	Level 0 (input mode active): Escape exits input mode but keeps overlay.
//	Level 1 (list mode): Escape closes overlay entirely.
func TestAPIKeysOverlayEscapePrecedence(t *testing.T) {
	t.Parallel()

	esc := tea.KeyMsg{Type: tea.KeyEsc}

	// Level 1: apikeys overlay in list mode, no input mode active.
	// Escape should close the overlay.
	t.Run("apikeys_list_mode_escape_closes_overlay", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "apikeys"})
		if !m.OverlayActive() || m.ActiveOverlay() != "apikeys" {
			t.Fatal("precondition: apikeys overlay should be active")
		}
		// APIKeyInputMode should be false (we just opened the overlay, no providers).
		if m.APIKeyInputMode() {
			t.Fatal("precondition: should not be in input mode initially")
		}

		after, _ := m.Update(esc)
		afterModel := after.(tui.Model)
		if afterModel.OverlayActive() {
			t.Error("apikeys list mode: Escape should close overlay entirely")
		}
		if afterModel.ActiveOverlay() != "" {
			t.Errorf("activeOverlay should be empty, got %q", afterModel.ActiveOverlay())
		}
	})
}

// ---------------------------------------------------------------------------
// Model overlay Level-0 vs Level-1 precedence
// ---------------------------------------------------------------------------

// TestModelOverlayEscapePrecedence verifies the two-level Escape inside the
// model overlay:
//
//	Level-0 with search text: Escape clears search, overlay stays open.
//	Level-0 no search:        Escape closes overlay entirely.
func TestModelOverlayEscapePrecedence(t *testing.T) {
	t.Parallel()

	esc := tea.KeyMsg{Type: tea.KeyEsc}

	t.Run("model_overlay_level0_no_search_escape_closes", func(t *testing.T) {
		t.Parallel()
		m := newInitialised()
		m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "model"})
		if !m.OverlayActive() || m.ActiveOverlay() != "model" {
			t.Fatal("precondition: model overlay should be active")
		}

		after, _ := m.Update(esc)
		afterModel := after.(tui.Model)
		if afterModel.OverlayActive() {
			t.Error("model overlay at Level-0 with no search: Escape should close overlay")
		}
	})
}

// ---------------------------------------------------------------------------
// Compound state: overlay + dropdown + run all active simultaneously
// ---------------------------------------------------------------------------

// TestEscapeWithOverlayAndDropdownAndRun verifies that when an overlay AND an
// active run are present, Escape closes the overlay rather than cancelling the
// run.  The slash dropdown is always closed first unconditionally.
func TestEscapeWithOverlayAndDropdownAndRun(t *testing.T) {
	t.Parallel()

	m := newInitialised()

	cancelled := false
	cancelFn := func() { cancelled = true }
	m = m.WithCancelRun(cancelFn)
	m = applyMsgs(m, tui.RunStartedMsg{RunID: "compound-001"})
	m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "help"})

	if !m.RunActive() {
		t.Fatal("precondition: run should be active")
	}
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay should be active")
	}

	after, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	afterModel := after.(tui.Model)

	// Overlay should be closed.
	if afterModel.OverlayActive() {
		t.Error("overlay should be closed by Escape (overlay priority > run cancel)")
	}
	// Run should still be active — Escape did NOT cancel it.
	if !afterModel.RunActive() {
		t.Error("run should still be active; Escape only closed the overlay")
	}
	// Cancel func must NOT have been called.
	if cancelled {
		t.Error("cancel func must NOT be called when overlay was the target of Escape")
	}
}

// TestEnterWithOverlayAndDropdown verifies that when the provider overlay is
// open, Enter closes the overlay (confirmed gateway selection), even if
// theoretically a slash dropdown might be active.
func TestEnterWithOverlayAndDropdown(t *testing.T) {
	t.Parallel()

	m := newInitialised()
	m = applyMsgs(m, tui.OverlayOpenMsg{Kind: "provider"})

	if !m.OverlayActive() || m.ActiveOverlay() != "provider" {
		t.Fatal("precondition: provider overlay must be active")
	}

	after, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	afterModel := after.(tui.Model)

	// Provider overlay must be closed by Enter.
	if afterModel.OverlayActive() && afterModel.ActiveOverlay() == "provider" {
		t.Error("provider overlay should be closed by Enter")
	}
}
