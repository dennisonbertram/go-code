package main

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/term"
)

func TestRunTUIRequiresTerminal(t *testing.T) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		t.Skip("stdout is a terminal in this environment")
	}

	err := runTUI("http://localhost:8080", "/tmp/project", "")
	if err == nil {
		t.Fatal("expected non-terminal runTUI call to fail")
	}
	if !strings.Contains(err.Error(), "--tui requires a terminal") {
		t.Fatalf("unexpected error: %v", err)
	}
}
