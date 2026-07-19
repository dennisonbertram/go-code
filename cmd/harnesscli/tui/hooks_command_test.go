package tui_test

import (
	"strings"
	"testing"
)

// TestTUI_HooksCommandStartsFetch verifies /hooks dispatches through the
// normal slash-command path and kicks off the server fetch (status message),
// covering executeHooksCommand through the registry like other commands.
func TestTUI_HooksCommandStartsFetch(t *testing.T) {
	m := initModel(t, 80, 24)
	m = sendSlashCommand(m, "/hooks")

	if got := m.StatusMsg(); !strings.Contains(got, "Loading hooks") {
		t.Fatalf("StatusMsg() = %q, want substring %q", got, "Loading hooks")
	}
}
