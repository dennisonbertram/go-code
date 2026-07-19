package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type planApprovalState struct {
	active         bool
	runID, content string
	offset         int
}

func (m Model) handlePlanApprovalKey(msg tea.KeyMsg) (planApprovalState, tea.Cmd) {
	st := m.planApproval
	switch msg.String() {
	case "a", "A", "y", "Y", "enter":
		return planApprovalState{}, approveToolCmd(m.config.BaseURL, st.runID, m.config.APIKey)
	case "d", "D", "n", "N":
		return planApprovalState{}, denyToolCmd(m.config.BaseURL, st.runID, m.config.APIKey)
	case "down", "j":
		st.offset++
	case "up", "k":
		if st.offset > 0 {
			st.offset--
		}
	}
	return st, nil
}

func (m Model) renderPlanApprovalOverlay() []string {
	if !m.planApproval.active {
		return nil
	}
	lines := strings.Split(m.planApproval.content, "\n")
	const page = 10
	if m.planApproval.offset > len(lines)-1 {
		m.planApproval.offset = len(lines) - 1
	}
	end := m.planApproval.offset + page
	if end > len(lines) {
		end = len(lines)
	}
	out := []string{"", "┌─ Plan Approval Required ─────────────────────────", "│  Review the proposed plan:"}
	for _, line := range lines[m.planApproval.offset:end] {
		out = append(out, "│  "+line)
	}
	out = append(out, fmt.Sprintf("│  [%d/%d]  ↑/↓ scroll", m.planApproval.offset+1, len(lines)), "│", "│  [a]pprove  [d] request changes", "└────────────────────────────────────────", "")
	return out
}
