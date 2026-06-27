package helpdialog_test

// Tests for #670 (component half) and #666 (helpdialog component half):
//   - Open() resets activeTab to TabCommands and scrollOffset to 0
//   - renderContent overflow indicator ("▼ more" / "▲ more") visible when content exceeds height
//   - Footer navigation hint present in View output

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/helpdialog"
)

// TestTUI670_OpenResetsTabToCommands verifies that Open() always resets activeTab
// to TabCommands, regardless of which tab was active before.
func TestTUI670_OpenResetsTabToCommands(t *testing.T) {
	m := helpdialog.New(sampleCommands(), sampleKeybindings(), sampleAbout())

	// Advance to a non-zero tab, then Open.
	m = m.NextTab() // TabKeybindings
	if m.ActiveTab() != helpdialog.TabKeybindings {
		t.Fatalf("expected TabKeybindings before Open, got %v", m.ActiveTab())
	}

	m = m.Open()
	if m.ActiveTab() != helpdialog.TabCommands {
		t.Errorf("Open() should reset activeTab to TabCommands; got %v", m.ActiveTab())
	}
}

// TestTUI670_OpenResetsScrollOffsetToZero verifies that Open() resets scrollOffset to 0.
func TestTUI670_OpenResetsScrollOffsetToZero(t *testing.T) {
	m := helpdialog.New(sampleCommands(), sampleKeybindings(), sampleAbout())

	// Scroll down, then Open — offset should reset.
	m = m.ScrollDown(5)
	m = m.Open()

	// Render: the dialog at 80x24 should show content from offset 0.
	// We verify this by ensuring the first command name is visible.
	out := m.View(80, 24)
	cmds := sampleCommands()
	if !strings.Contains(out, cmds[0].Name) {
		t.Errorf("after Open(), first command %q should be visible (scrollOffset should be 0), got:\n%s", cmds[0].Name, out)
	}
}

// TestTUI670_OpenResetOnReopenAfterNextTab simulates the reopen pattern:
// Open → NextTab → Close → Open should land back on Commands at offset 0.
func TestTUI670_OpenResetOnReopenAfterNextTab(t *testing.T) {
	m := helpdialog.New(sampleCommands(), sampleKeybindings(), sampleAbout())
	m = m.Open()
	m = m.NextTab() // move to Keybindings
	m = m.NextTab() // move to About
	m = m.ScrollDown(3)
	m = m.Close()
	m = m.Open() // reopen — should reset

	if m.ActiveTab() != helpdialog.TabCommands {
		t.Errorf("after Close+Open cycle, activeTab should be Commands; got %v", m.ActiveTab())
	}
}

// TestTUI670_OverflowIndicatorDownPresent verifies that when content exceeds the
// visible height, a "▼ more" overflow indicator appears in the rendered output.
func TestTUI670_OverflowIndicatorDownPresent(t *testing.T) {
	// Build a large command list to ensure content exceeds visible area.
	var cmds []helpdialog.CommandEntry
	for i := 0; i < 30; i++ {
		cmds = append(cmds, helpdialog.CommandEntry{
			Name:        "cmd" + string(rune('a'+i%26)),
			Description: "description for command",
		})
	}
	m := helpdialog.New(cmds, nil, nil)
	// Render at 80x24 — 30 commands won't all fit.
	out := m.View(80, 24)
	if !strings.Contains(out, "▼") {
		t.Errorf("expected '▼' overflow indicator when content exceeds height, got:\n%s", out)
	}
}

// TestTUI670_OverflowIndicatorUpPresent verifies that when scrolled down,
// a "▲ more" overflow indicator appears at the top.
func TestTUI670_OverflowIndicatorUpPresent(t *testing.T) {
	var cmds []helpdialog.CommandEntry
	for i := 0; i < 30; i++ {
		cmds = append(cmds, helpdialog.CommandEntry{
			Name:        "cmd" + string(rune('a'+i%26)),
			Description: "description for command",
		})
	}
	m := helpdialog.New(cmds, nil, nil)
	// Scroll down enough that there's content above.
	m = m.ScrollDown(10)
	out := m.View(80, 24)
	if !strings.Contains(out, "▲") {
		t.Errorf("expected '▲' overflow indicator when scrolled past content, got:\n%s", out)
	}
}

// TestTUI666_HelpdialogFooterHintPresent verifies that the navigation hint footer
// ("↑/↓ navigate" or similar) is present in the rendered help dialog.
func TestTUI666_HelpdialogFooterHintPresent(t *testing.T) {
	m := helpdialog.New(sampleCommands(), sampleKeybindings(), sampleAbout())
	out := m.View(80, 24)
	if !strings.Contains(out, "navigate") {
		t.Errorf("expected footer navigation hint in help dialog View(); got:\n%s", out)
	}
}
