package main

import (
	"testing"

	_ "go-agent-harness/cmd/harnesscli/tui"
	_ "go-agent-harness/cmd/harnesscli/tui/components/configpanel"
	_ "go-agent-harness/cmd/harnesscli/tui/components/contextgrid"
	_ "go-agent-harness/cmd/harnesscli/tui/components/diffview"
	_ "go-agent-harness/cmd/harnesscli/tui/components/helpdialog"
	_ "go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	_ "go-agent-harness/cmd/harnesscli/tui/components/layout"
	_ "go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	_ "go-agent-harness/cmd/harnesscli/tui/components/slashcomplete"
	_ "go-agent-harness/cmd/harnesscli/tui/components/statspanel"
	_ "go-agent-harness/cmd/harnesscli/tui/components/statusbar"
	_ "go-agent-harness/cmd/harnesscli/tui/components/thinkingbar"
	_ "go-agent-harness/cmd/harnesscli/tui/components/tooluse"
	_ "go-agent-harness/cmd/harnesscli/tui/components/viewport"
	_ "go-agent-harness/cmd/harnesscli/tui/testhelpers"
)

// TestTUI002_PackageTreeCanBeBuilt verifies all TUI packages exist and compile.
// This test is satisfied by `go build ./cmd/harnesscli/tui/...` passing.
// The blank imports above ensure every package in the scaffold compiles.
func TestTUI002_PackageTreeCanBeBuilt(t *testing.T) {
	t.Log("TUI package scaffold compiles successfully")
}
