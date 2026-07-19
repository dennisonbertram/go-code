package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	htools "go-agent-harness/internal/harness/tools"
)

func (r *Runner) awaitPlanApproval(ctx context.Context, runID, content string) (bool, error) {
	r.mu.Lock()
	st := r.runs[runID]
	if st == nil || st.planMode != PlanModeActive {
		r.mu.Unlock()
		return true, nil
	}
	st.planMode = PlanModeExitPending
	r.mu.Unlock()
	if r.config.ApprovalBroker == nil {
		return false, fmt.Errorf("plan mode requires an approval broker")
	}
	r.setStatus(runID, RunStatusWaitingForApproval, "", "")
	r.emit(runID, EventPlanApprovalRequired, map[string]any{"tool": "plan_exit", "plan": content})
	approved, err := r.config.ApprovalBroker.Ask(ctx, ApprovalRequest{RunID: runID, CallID: "plan_exit", Tool: "plan_exit", Args: content, Timeout: r.config.AskUserTimeout})
	if err != nil {
		return false, err
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
		r.emit(runID, EventPlanApprovalGranted, map[string]any{"plan": content})
	} else {
		r.emit(runID, EventPlanApprovalDenied, map[string]any{"plan": content})
	}
	return approved, nil
}

// PlanModeState is the per-run lifecycle for an enforced planning phase.
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
