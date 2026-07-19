package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPlanApprovalOverlayScrollsAndRendersChoices(t *testing.T) {
	m := New(TUIConfig{})
	m.planApproval = planApprovalState{active: true, runID: "r", content: strings.Repeat("line\n", 15)}
	view := strings.Join(m.renderPlanApprovalOverlay(), "\n")
	if !strings.Contains(view, "Plan Approval Required") || !strings.Contains(view, "request changes") {
		t.Fatalf("view=%s", view)
	}
	st, _ := m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyDown})
	if st.offset != 1 {
		t.Fatalf("offset=%d", st.offset)
	}
}
