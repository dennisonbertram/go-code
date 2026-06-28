package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestPermissionsCommand_OpensOverlay verifies that /permissions sets
// overlayActive=true and activeOverlay=="permissions".
func TestPermissionsCommand_OpensOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/permissions")

	if !m.OverlayActive() {
		t.Fatal("overlayActive must be true after /permissions")
	}
	if m.ActiveOverlay() != "permissions" {
		t.Errorf("activeOverlay: want %q, got %q", "permissions", m.ActiveOverlay())
	}
}

// TestPermissionsCommand_ViewContainsPanelHeader verifies the rendered view
// contains the panel's header text ("Permissions") when the overlay is open.
func TestPermissionsCommand_ViewContainsPanelHeader(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/permissions")

	view := m.View()
	if !strings.Contains(view, "Permissions") {
		t.Errorf("View() after /permissions must contain 'Permissions'; got:\n%s", view)
	}
}

// TestPermissionsCommand_NotUnknown verifies the /permissions command does not
// produce an "Unknown command" status message.
func TestPermissionsCommand_NotUnknown(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/permissions")

	status := m.StatusMsg()
	if strings.Contains(strings.ToLower(status), "unknown") {
		t.Errorf("StatusMsg must not contain 'unknown' after /permissions; got %q", status)
	}
}

// TestPermissionsCommand_IsRegistered verifies that "permissions" is a
// registered command in a fresh NewCommandRegistry().
func TestPermissionsCommand_IsRegistered(t *testing.T) {
	reg := tui.NewCommandRegistry()
	_, ok := reg.Lookup("permissions")
	if !ok {
		t.Fatal("'permissions' command must be registered in NewCommandRegistry()")
	}
}

// TestPermissionsOverlay_EscapeCloses verifies that pressing Escape while the
// permissions overlay is open closes it.
func TestPermissionsOverlay_EscapeCloses(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/permissions")
	if !m.OverlayActive() {
		t.Fatal("precondition: overlayActive must be true after /permissions")
	}

	// Send Escape.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(tui.Model)

	if m.OverlayActive() {
		t.Error("overlayActive must be false after pressing Escape on permissions overlay")
	}
	if m.ActiveOverlay() != "" {
		t.Errorf("activeOverlay must be empty after Escape; got %q", m.ActiveOverlay())
	}
}

// TestPermissionsOverlay_EmptyState verifies the panel shows the honest empty
// state message ("No permission rules active") when there are no rules.
func TestPermissionsOverlay_EmptyState(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/permissions")

	view := m.View()
	if !strings.Contains(view, "No permission rules active") {
		t.Errorf("View() must show empty state when no rules exist; got:\n%s", view)
	}
}
