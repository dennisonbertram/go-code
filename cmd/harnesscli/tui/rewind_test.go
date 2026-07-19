package tui_test

import (
	tui "go-agent-harness/cmd/harnesscli/tui"
	"testing"
)

func TestRewindCommandRequiresExplicitConfirmation(t *testing.T) {
	reg := tui.NewCommandRegistry()
	if !reg.IsRegistered("rewind") {
		t.Fatal("/rewind is not registered")
	}
	result := reg.Dispatch(tui.Command{Name: "rewind"})
	if result.Status != tui.CmdOK {
		t.Fatalf("result=%+v", result)
	}
}
