package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	htools "go-agent-harness/internal/harness/tools"
)

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
