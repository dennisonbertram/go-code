package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	htools "go-agent-harness/internal/harness/tools"
)

// awaitPlanApproval presents the run's plan for explicit operator approval when
// the run would otherwise complete. Pinned semantics (covered by
// plan_mode_semantics_test.go — do not relax without changing those tests):
//
//   - Exit approval is mandatory in EVERY ToolApprovalMode, including
//     ToolApprovalModeFullAuto. This gate must never be keyed on approval mode:
//     full_auto auto-approves tool calls, never the plan-exit checkpoint.
//   - Approval transitions the run to PlanModeInactive, emits
//     plan.approval_granted, and the run completes.
//   - Denial transitions the run back to PlanModeActive and emits
//     plan.approval_denied; the caller appends the operator-feedback user
//     message ("The operator requested changes to the plan...") and the run
//     continues in plan mode until an exit is approved.
//   - A nil ApprovalBroker fails the run explicitly
//     ("plan mode requires an approval broker"). This fail-closed behavior is
//     deliberate: silently auto-approving would defeat the checkpoint, and
//     auto-denying would loop forever because the model would immediately
//     re-present the same plan.
//   - Broker timeout or context cancellation propagates as an error and fails
//     the run — the defined outcome instead of waiting forever.
//
// Approach options (epic #819, slice 3): when the presented plan ends with a
// "## Approaches" section of 1-3 labeled items, the options are included in
// the plan.approval_required payload and the broker request; the operator's
// selected option is returned to the caller (echoed in plan.approval_granted
// as option/option_label and relayed to the model). An option ID that does
// not match a presented option degrades to a plain approve.
func (r *Runner) awaitPlanApproval(ctx context.Context, runID, content string) (bool, PlanApproachOption, error) {
	rc := r.configForRun(runID)
	r.mu.Lock()
	st := r.runs[runID]
	if st == nil || st.planMode != PlanModeActive {
		r.mu.Unlock()
		return true, PlanApproachOption{}, nil
	}
	st.planMode = PlanModeExitPending
	r.mu.Unlock()
	if rc.ApprovalBroker == nil {
		return false, PlanApproachOption{}, fmt.Errorf("plan mode requires an approval broker")
	}
	r.setStatus(runID, RunStatusWaitingForApproval, "", "")
	if plans, ok := rc.ConversationStore.(PlanContentStore); ok {
		r.mu.RLock()
		st := r.runs[runID]
		var convID string
		if st != nil {
			convID = st.run.ConversationID
		}
		r.mu.RUnlock()
		if err := plans.SavePlanContent(ctx, convID, runID, content); err != nil {
			return false, PlanApproachOption{}, err
		}
	}
	options := parsePlanApproaches(content)
	required := map[string]any{"tool": "plan_exit", "plan": content}
	if len(options) > 0 {
		required["options"] = options
	}
	r.emit(runID, EventPlanApprovalRequired, required)
	approved, selectedID, err := rc.ApprovalBroker.Ask(ctx, ApprovalRequest{RunID: runID, CallID: "plan_exit", Tool: "plan_exit", Args: content, Timeout: rc.AskUserTimeout, Options: options})
	if err != nil {
		return false, PlanApproachOption{}, err
	}
	r.mu.Lock()
	st = r.runs[runID]
	if st != nil {
		if approved {
			st.planMode = PlanModeInactive
		} else {
			st.planMode = PlanModeActive
		}
	}
	r.mu.Unlock()
	r.setStatus(runID, RunStatusRunning, "", "")
	if approved {
		granted := map[string]any{"plan": content}
		var selected PlanApproachOption
		for _, opt := range options {
			if opt.ID == selectedID {
				selected = opt
				break
			}
		}
		if selected.ID != "" {
			granted["option"] = selected.ID
			granted["option_label"] = selected.Label
		}
		r.emit(runID, EventPlanApprovalGranted, granted)
		return true, selected, nil
	}
	r.emit(runID, EventPlanApprovalDenied, map[string]any{"plan": content})
	return false, PlanApproachOption{}, nil
}

// parsePlanApproaches extracts 1-3 approach options from the presented plan's
// trailing "## Approaches" section (the convention the plan-mode guidance
// teaches). Option IDs are assigned positionally: "a", "b", "c". It returns
// nil when the section is absent or holds anything other than 1-3 valid list
// items — such plans behave exactly as no-option plans.
func parsePlanApproaches(plan string) []PlanApproachOption {
	lines := strings.Split(plan, "\n")
	start := -1
	for i, line := range lines {
		if isApproachesHeading(line) {
			start = i + 1 // keep the last Approaches section
		}
	}
	if start < 0 {
		return nil
	}
	var items []string
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			break // the next heading ends the section
		}
		if item, ok := parseApproachItem(trimmed); ok {
			items = append(items, item)
		}
	}
	if len(items) < 1 || len(items) > 3 {
		return nil
	}
	ids := []string{"a", "b", "c"}
	options := make([]PlanApproachOption, 0, len(items))
	for i, item := range items {
		label, description := splitApproachLabel(item)
		if label == "" {
			return nil // a malformed item invalidates the whole section
		}
		options = append(options, PlanApproachOption{ID: ids[i], Label: label, Description: description})
	}
	return options
}

// isApproachesHeading reports whether line is a "## Approaches" heading.
func isApproachesHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "###") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "##")), "Approaches")
}

// parseApproachItem strips a numbered ("1." / "2)") or bullet ("-" / "*") list
// marker from a line, returning the item text.
func parseApproachItem(line string) (string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	if i := strings.IndexAny(line, ".)"); i > 0 && i+1 < len(line) && line[i+1] == ' ' {
		if _, err := strconv.Atoi(line[:i]); err == nil {
			return strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", false
}

// splitApproachLabel splits an approaches item into label and description on
// the first " — ", " – ", ": ", or " - " separator, stripping markdown bold.
func splitApproachLabel(item string) (string, string) {
	item = strings.ReplaceAll(item, "**", "")
	for _, sep := range []string{" — ", " – ", ": ", " - "} {
		if i := strings.Index(item, sep); i > 0 {
			return strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+len(sep):])
		}
	}
	return strings.TrimSpace(item), ""
}

// planModePromptBlock returns the model-facing guidance injected into the
// outgoing provider messages (as a trailing system message) while the run is
// actively in plan mode. It tells the model that it is in read-only planning,
// names the resolved plan file as the only writable target, and explains how to
// finish: write the plan to the plan file and end the turn to present it,
// optionally with 1-3 clearly labeled approaches under "## Approaches" (the
// structured convention plan-exit option extraction relies on). It returns ""
// when the run is not in PlanModeActive, so non-plan runs and post-approval
// turns carry no block.
func (r *Runner) planModePromptBlock(runID string) string {
	r.mu.RLock()
	st := r.runs[runID]
	if st == nil || st.planMode != PlanModeActive {
		r.mu.RUnlock()
		return ""
	}
	planFile := st.planFile
	r.mu.RUnlock()
	return fmt.Sprintf(`PLAN MODE ACTIVE — you are in read-only planning mode.
- Explore the codebase with read-only tools (read, grep, glob, ls, non-mutating bash commands).
- The only file you may write is the designated plan file: %s. All other writes, edits, and mutating tool calls are denied (plan_mode_denied).
- When the plan is ready, write it to %s and end your turn; ending your turn presents the plan to the operator for approval.
- If you want to offer alternatives, end the plan with a "## Approaches" section listing 1-3 clearly labeled approaches.`, planFile, planFile)
}

// planModeDenialFeedback returns the user message appended to the run when the
// operator denies a plan exit, reminding the model which plan file to revise.
func (r *Runner) planModeDenialFeedback(runID string) string {
	r.mu.RLock()
	var planFile string
	if st := r.runs[runID]; st != nil {
		planFile = st.planFile
	}
	r.mu.RUnlock()
	if strings.TrimSpace(planFile) == "" {
		planFile = defaultPlanFile
	}
	return fmt.Sprintf("The operator requested changes to the plan. Revise the designated plan file (%s) and present the updated plan.", planFile)
}

// PlanModeState is the per-run lifecycle for an enforced planning phase.
// inactive → active when the run starts with PlanMode enabled; active →
// exit_pending while awaitPlanApproval blocks on the operator; exit_pending →
// inactive on approval or back to active on denial. Plan mode is only ever
// deactivated by explicit operator approval — never by approval mode, timeout,
// or retry.
type PlanModeState string

const (
	PlanModeInactive    PlanModeState = "inactive"
	PlanModeActive      PlanModeState = "active"
	PlanModeExitPending PlanModeState = "exit_pending"
)

const defaultPlanFile = ".harness/plan.md"

func initialPlanModeState(enabled bool) PlanModeState {
	if enabled {
		return PlanModeActive
	}
	return PlanModeInactive
}

func normalizedPlanFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return defaultPlanFile
	}
	return path
}

type runPlanModeGate struct {
	runner *Runner
	runID  string
}

// PlanModeState returns the live plan-mode state for a run. It is primarily
// useful to transports and integration tests which must observe approval
// transitions without reaching into runner internals.
func (r *Runner) PlanModeState(runID string) (PlanModeState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st, ok := r.runs[runID]
	if !ok {
		return "", false
	}
	return st.planMode, true
}

func (g runPlanModeGate) Active() bool {
	g.runner.mu.RLock()
	defer g.runner.mu.RUnlock()
	st := g.runner.runs[g.runID]
	return st != nil && st.planMode == PlanModeActive
}
func (g runPlanModeGate) AllowMutation(def htools.Definition, args json.RawMessage) bool {
	if !isPathPermissionTool(def.Name) {
		return false
	}
	g.runner.mu.RLock()
	st := g.runner.runs[g.runID]
	if st == nil {
		g.runner.mu.RUnlock()
		return false
	}
	planFile, workspace := st.planFile, st.permissionWorkspaceRoot
	g.runner.mu.RUnlock()
	rules := []PermissionRule{{Pattern: fmt.Sprintf("%s(**)", def.Name), Effect: PermissionEffectDeny}, {Pattern: fmt.Sprintf("%s(%s)", def.Name, planFile), Effect: PermissionEffectAllow}}
	effect, err := EvaluatePermissionRules(rules, def.Name, args, workspace)
	return err == nil && effect == PermissionEffectAllow
}
