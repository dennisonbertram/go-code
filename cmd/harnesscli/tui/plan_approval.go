package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// planApproachOption is one labeled approach offered in a plan exit, carried
// in the plan.approval_required payload (epic #819).
type planApproachOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type planApprovalState struct {
	active         bool
	runID, content string
	offset         int
	options        []planApproachOption
	selectedIdx    int
}

func (m Model) handlePlanApprovalKey(msg tea.KeyMsg) (planApprovalState, tea.Cmd) {
	st := m.planApproval
	switch msg.String() {
	case "a", "A", "y", "Y", "enter":
		// With approach options present, approve with the highlighted option.
		if len(st.options) > 0 && st.selectedIdx >= 0 && st.selectedIdx < len(st.options) {
			return planApprovalState{}, approveToolCmd(m.config.BaseURL, st.runID, m.config.APIKey, st.options[st.selectedIdx].ID)
		}
		return planApprovalState{}, approveToolCmd(m.config.BaseURL, st.runID, m.config.APIKey)
	case "d", "D", "n", "N":
		return planApprovalState{}, denyToolCmd(m.config.BaseURL, st.runID, m.config.APIKey)
	case "down", "j":
		// With options, up/down moves the selection cursor (askuser idiom);
		// without options it scrolls the plan content as before.
		if len(st.options) > 0 {
			if st.selectedIdx < len(st.options)-1 {
				st.selectedIdx++
			}
		} else {
			st.offset++
		}
	case "up", "k":
		if len(st.options) > 0 {
			if st.selectedIdx > 0 {
				st.selectedIdx--
			}
		} else if st.offset > 0 {
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
	if len(m.planApproval.options) == 0 {
		out = append(out, fmt.Sprintf("│  [%d/%d]  ↑/↓ scroll", m.planApproval.offset+1, len(lines)), "│", "│  [a]pprove  [d] request changes", "└────────────────────────────────────────", "")
		return out
	}
	out = append(out, fmt.Sprintf("│  [%d/%d]", m.planApproval.offset+1, len(lines)), "│", "│  Approaches:")
	idx := m.planApproval.selectedIdx
	if idx > len(m.planApproval.options)-1 {
		idx = len(m.planApproval.options) - 1
	}
	for i, opt := range m.planApproval.options {
		cursor := "  "
		if i == idx {
			cursor = "▶ "
		}
		line := "│ " + cursor + opt.Label
		if opt.Description != "" {
			line += " — " + opt.Description
		}
		out = append(out, line)
	}
	out = append(out, "│", "│  [↑↓] select approach  [a/enter] approve  [d] request changes", "└────────────────────────────────────────", "")
	return out
}
