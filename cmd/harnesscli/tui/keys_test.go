package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
)

func TestTUI008_KeyMapHasRequiredBindings(t *testing.T) {
	km := tui.DefaultKeyMap()
	short := km.ShortHelp()
	if len(short) < 4 {
		t.Errorf("ShortHelp needs >= 4 bindings, got %d", len(short))
	}
	full := km.FullHelp()
	if len(full) == 0 || len(full[0]) == 0 {
		t.Error("FullHelp returned empty rows")
	}
}

func TestTUI008_HelpTextContainsRequiredShortcuts(t *testing.T) {
	km := tui.DefaultKeyMap()
	help := km.ShortHelp()
	if len(help) < 6 {
		t.Errorf("ShortHelp should have at least 6 bindings, got %d", len(help))
	}
	full := km.FullHelp()
	if len(full) == 0 {
		t.Error("FullHelp returned empty")
	}
}

func TestTUI008_ShortHelpContainsExpectedKeys(t *testing.T) {
	km := tui.DefaultKeyMap()
	short := km.ShortHelp()

	expectedHelps := map[string]bool{
		"submit":    false,
		"quit":      false,
		"interrupt": false,
		"commands":  false,
	}

	for _, b := range short {
		h := b.Help().Desc
		if _, ok := expectedHelps[h]; ok {
			expectedHelps[h] = true
		}
	}

	for desc, found := range expectedHelps {
		if !found {
			t.Errorf("ShortHelp missing binding with description %q", desc)
		}
	}
}

func TestTUI008_FullHelpGroupsCorrectly(t *testing.T) {
	km := tui.DefaultKeyMap()
	full := km.FullHelp()

	if len(full) < 3 {
		t.Fatalf("FullHelp should have at least 3 groups, got %d", len(full))
	}

	// Count total bindings across all groups
	total := 0
	for _, group := range full {
		total += len(group)
	}

	if total < 10 {
		t.Errorf("FullHelp should have at least 10 bindings total, got %d", total)
	}
}

func TestTUI008_UnknownKeyIgnored(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF12})
	if m2 == nil {
		t.Fatal("nil model after F12")
	}
	_ = cmd
}

func TestTUI008_CtrlCQuitsModel(t *testing.T) {
	m := tui.New(tui.DefaultTUIConfig())
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Ctrl+C should return tea.Quit cmd")
	}
}

func TestTUI008_KeyMapImplementsHelpKeyMap(t *testing.T) {
	km := tui.DefaultKeyMap()
	// Verify ShortHelp and FullHelp exist (help.KeyMap interface)
	short := km.ShortHelp()
	full := km.FullHelp()
	if short == nil {
		t.Error("ShortHelp returned nil")
	}
	if full == nil {
		t.Error("FullHelp returned nil")
	}
}

func TestTUI008_EachBindingHasKeys(t *testing.T) {
	km := tui.DefaultKeyMap()

	type namedBinding struct {
		name    string
		enabled bool
		helpKey string
		helpDsc string
	}

	bindings := []namedBinding{
		{"Submit", km.Submit.Enabled(), km.Submit.Help().Key, km.Submit.Help().Desc},
		{"Newline", km.Newline.Enabled(), km.Newline.Help().Key, km.Newline.Help().Desc},
		{"ScrollUp", km.ScrollUp.Enabled(), km.ScrollUp.Help().Key, km.ScrollUp.Help().Desc},
		{"ScrollDown", km.ScrollDown.Enabled(), km.ScrollDown.Help().Key, km.ScrollDown.Help().Desc},
		{"PageUp", km.PageUp.Enabled(), km.PageUp.Help().Key, km.PageUp.Help().Desc},
		{"PageDown", km.PageDown.Enabled(), km.PageDown.Help().Key, km.PageDown.Help().Desc},
		{"SlashCmd", km.SlashCmd.Enabled(), km.SlashCmd.Help().Key, km.SlashCmd.Help().Desc},
		{"AtMention", km.AtMention.Enabled(), km.AtMention.Help().Key, km.AtMention.Help().Desc},
		{"Interrupt", km.Interrupt.Enabled(), km.Interrupt.Help().Key, km.Interrupt.Help().Desc},
		{"Help", km.Help.Enabled(), km.Help.Help().Key, km.Help.Help().Desc},
		{"Quit", km.Quit.Enabled(), km.Quit.Help().Key, km.Quit.Help().Desc},
		{"EditMode", km.EditMode.Enabled(), km.EditMode.Help().Key, km.EditMode.Help().Desc},
	}

	for _, b := range bindings {
		if !b.enabled {
			t.Errorf("binding %s is not enabled", b.name)
		}
		if b.helpKey == "" {
			t.Errorf("binding %s has empty help key", b.name)
		}
		if b.helpDsc == "" {
			t.Errorf("binding %s has empty help description", b.name)
		}
	}
}
