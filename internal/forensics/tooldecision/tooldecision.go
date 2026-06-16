// Package tooldecision provides types for forensic tool decision tracing,
// anti-pattern detection, and hook mutation tracing in the agent harness.
package tooldecision

import "fmt"

// ToolDecisionSnapshot captures the tool selection context for one step.
// It records which tools were available and which tools the model selected,
// allowing post-hoc analysis of model decision making.
type ToolDecisionSnapshot struct {
	// Step is the 1-based step number within the run.
	Step int `json:"step"`
	// CallSequence is the sequential call number within the run (call_1, call_2, …).
	// It increments across all steps, not resetting per step.
	CallSequence int `json:"call_sequence"`
	// AvailableTools is the list of tool names sent to the model for this step.
	AvailableTools []string `json:"available_tools"`
	// SelectedTools is the list of tool names the model chose to call.
	SelectedTools []string `json:"selected_tools"`
}

// CallSequenceID returns the human-readable sequential ID for this snapshot,
// e.g. "call_1", "call_2".
func (s ToolDecisionSnapshot) CallSequenceID() string {
	return fmt.Sprintf("call_%d", s.CallSequence)
}

// AntiPatternType identifies which anti-pattern was detected.
type AntiPatternType string

const (
	// AntiPatternRetryLoop is emitted when the same tool is called with the
	// same arguments 3 or more times within a single run.
	AntiPatternRetryLoop AntiPatternType = "retry_loop"
	// AntiPatternHedgeAssertion is emitted when the model qualifies its
	// conclusions with hedging language instead of asserting definite findings.
	AntiPatternHedgeAssertion AntiPatternType = "hedge_assertion"
	// AntiPatternUnverifiedFileClaim is emitted when the model makes claims
	// about file contents without executing a read or verification tool.
	AntiPatternUnverifiedFileClaim AntiPatternType = "unverified_file_claim"
	// AntiPatternPrematureCompletion is emitted when the model attempts to
	// finish the task before completing all required verification steps.
	AntiPatternPrematureCompletion AntiPatternType = "premature_completion"
	// AntiPatternSkippedDiagnostic is emitted when the model skips a
	// diagnostic step (e.g. running tests) that was explicitly requested.
	AntiPatternSkippedDiagnostic AntiPatternType = "skipped_diagnostic"
	// AntiPatternArchitectureAssumption is emitted when the model makes
	// architectural decisions based on assumptions without verifying them
	// against the actual codebase.
	AntiPatternArchitectureAssumption AntiPatternType = "architecture_assumption"
)

// AntiPatternAlert describes a detected anti-pattern in tool call behaviour.
type AntiPatternAlert struct {
	// Type is the category of anti-pattern detected.
	Type AntiPatternType `json:"type"`
	// ToolName is the name of the tool involved.
	ToolName string `json:"tool_name"`
	// CallCount is the number of times the tool/args pair has been seen
	// at the point the alert was raised (>= 3 for retry_loop).
	CallCount int `json:"call_count"`
	// Step is the step number at which the threshold was crossed.
	Step int `json:"step"`
}

// HookMutationAction describes what a hook did to a tool call.
type HookMutationAction string

const (
	// HookActionAllow means the hook allowed the call without modification.
	HookActionAllow HookMutationAction = "Allow"
	// HookActionBlock means the hook denied/blocked the call.
	HookActionBlock HookMutationAction = "Block"
	// HookActionModify means the hook modified the arguments.
	HookActionModify HookMutationAction = "Modify"
	// HookActionInject means the hook injected new arguments (original was empty).
	HookActionInject HookMutationAction = "Inject"
)

// HookMutation records the before/after snapshot of a hook's effect on a
// tool call's arguments.
type HookMutation struct {
	// ToolCallID is the ID of the tool call from the LLM response.
	ToolCallID string `json:"tool_call_id"`
	// HookName is the name of the hook that processed the call.
	HookName string `json:"hook_name"`
	// Action classifies what the hook did: Block, Modify, Inject, or Allow.
	Action HookMutationAction `json:"action"`
	// ArgsBefore is the JSON arguments string before the hook ran.
	ArgsBefore string `json:"args_before,omitempty"`
	// ArgsAfter is the JSON arguments string after the hook ran.
	// Empty when action is Block or Allow.
	ArgsAfter string `json:"args_after,omitempty"`
}

// ClassifyHookAction determines the HookMutationAction given the before/after
// args and whether the hook blocked the call.
func ClassifyHookAction(blocked bool, argsBefore, argsAfter string) HookMutationAction {
	if blocked {
		return HookActionBlock
	}
	if argsBefore == argsAfter {
		return HookActionAllow
	}
	if argsBefore == "" || argsBefore == "null" {
		return HookActionInject
	}
	return HookActionModify
}
