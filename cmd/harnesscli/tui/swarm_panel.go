package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// swarmToolName is the wire name of the swarm fan-out tool (kept as a local
// literal the same way the SSE switch matches event types as literals).
const swarmToolName = "agent_swarm"

// swarmMemberCap is the hard cap on swarm members from the agent_swarm tool
// contract (subagents.SwarmMaxMembers). Duplicated as a literal to avoid a
// TUI -> subagents package dependency for one constant.
const swarmMemberCap = 128

// swarmMatchSkew is the small margin subtracted from the swarm start time
// when matching member subagents by creation window, absorbing clock jitter
// between the tool-call event and subagent creation.
const swarmMatchSkew = 2 * time.Second

// SwarmPollTickMsg drives the /v1/subagents poll loop while a swarm is active.
type SwarmPollTickMsg struct{}

// swarmPanelMember is one rendered swarm member row.
type swarmPanelMember struct {
	Item   string
	ID     string
	Status string
}

// swarmPanel is the view model for the live swarm section: a summary line
// plus one row per member.
type swarmPanel struct {
	Active  bool
	Members []swarmPanelMember
}

// swarmTracker tracks the current run's in-flight (or most recently
// completed) agent_swarm call so the /subagents view can group and live-update
// its members. agent_swarm is a sole-call, blocking tool, so at most one
// swarm per run is ever active.
type swarmTracker struct {
	active      bool
	callID      string
	items       []string
	resumeCount int
	startedAt   time.Time
	// report holds the exact members parsed from the aggregated report once
	// the tool call completes; until then members are matched heuristically.
	report    []swarmPanelMember
	reportSet bool
	// panelStart/panelCount locate the live panel block in the viewport so
	// poll refreshes replace it in place instead of appending duplicates.
	panelStart int
	panelCount int
}

// hasData reports whether the tracker holds anything renderable.
func (t *swarmTracker) hasData() bool {
	return len(t.items) > 0 || t.reportSet
}

// resolvePanel builds the panel view model. Before the aggregated report
// arrives, members are matched by creation window in schedule order (item i
// pairs with the i-th subagent created after the swarm started); resume
// entries render as "resumed" until the report provides exact statuses.
func (t *swarmTracker) resolvePanel(remote []RemoteSubagent) swarmPanel {
	p := swarmPanel{Active: t.active}
	if t.reportSet {
		p.Members = append([]swarmPanelMember(nil), t.report...)
		return p
	}
	candidates := make([]RemoteSubagent, 0, len(t.items))
	for _, sa := range remote {
		if sa.CreatedAt.IsZero() {
			continue
		}
		if sa.CreatedAt.Before(t.startedAt.Add(-swarmMatchSkew)) {
			continue
		}
		candidates = append(candidates, sa)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })

	p.Members = make([]swarmPanelMember, 0, len(t.items))
	for i, item := range t.items {
		m := swarmPanelMember{Item: item, Status: "pending"}
		switch {
		case i < t.resumeCount:
			// Resumed members are pre-existing subagents; creation-window
			// matching cannot see them, and the exact status arrives with
			// the aggregated report.
			m.Status = "resumed"
		default:
			if cand := i - t.resumeCount; cand < len(candidates) {
				m.ID = candidates[cand].ID
				m.Status = candidates[cand].Status
			}
		}
		p.Members = append(p.Members, m)
	}
	return p
}

// formatSwarmPanelLines renders the summary line and per-member rows.
func formatSwarmPanelLines(p swarmPanel) []string {
	launched, completed := 0, 0
	for _, m := range p.Members {
		if m.ID != "" {
			launched++
		}
		if m.Status == "completed" {
			completed++
		}
	}
	lines := []string{
		fmt.Sprintf("Swarm: %d launched, %d/%d completed (cap %d)", launched, completed, len(p.Members), swarmMemberCap),
	}
	for _, m := range p.Members {
		row := fmt.Sprintf("  [%-9s] %s", m.Status, m.Item)
		if m.ID != "" {
			row += " (" + m.ID + ")"
		}
		lines = append(lines, row)
	}
	return lines
}

// swarmPanelAllTerminal reports whether every member reached a terminal
// state, which stops the poll loop even without the tool-call completion.
func swarmPanelAllTerminal(p swarmPanel) bool {
	if len(p.Members) == 0 {
		return false
	}
	for _, m := range p.Members {
		switch m.Status {
		case "completed", "failed", "cancelled":
		default:
			return false
		}
	}
	return true
}

// parseSwarmToolArgs extracts items and resume_agent_ids from agent_swarm
// call arguments (already unwrapped by the caller's handleToolStart path).
func parseSwarmToolArgs(args json.RawMessage) (items []string, resumeIDs []string) {
	var parsed struct {
		Items          []string `json:"items"`
		ResumeAgentIDs []string `json:"resume_agent_ids"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, nil
	}
	return parsed.Items, parsed.ResumeAgentIDs
}

// parseSwarmReport extracts per-member results from the aggregated report
// JSON returned by the agent_swarm tool call.
func parseSwarmReport(output string) []swarmPanelMember {
	var report struct {
		Members []struct {
			ID     string `json:"id"`
			Item   string `json:"item"`
			Status string `json:"status"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Members) == 0 {
		return nil
	}
	members := make([]swarmPanelMember, 0, len(report.Members))
	for _, m := range report.Members {
		members = append(members, swarmPanelMember{Item: m.Item, ID: m.ID, Status: m.Status})
	}
	return members
}
