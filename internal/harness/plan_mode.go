package harness

import "strings"

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
