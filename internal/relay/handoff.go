package relay

import (
	"context"
	"fmt"
	"time"
)

// HandoffStatus represents the current state of a task handoff.
type HandoffStatus string

const (
	HandoffNone       HandoffStatus = "none"
	HandoffPending    HandoffStatus = "pending"
	HandoffInProgress HandoffStatus = "in_progress"
	HandoffCompleted  HandoffStatus = "completed"
	HandoffFailed     HandoffStatus = "failed"
)

// HandoffPackage contains everything needed to resume a run on another worker.
type HandoffPackage struct {
	// RunID identifies the run being handed off.
	RunID string `json:"run_id"`

	// SourceWorker is the worker the run is leaving.
	SourceWorker string `json:"source_worker"`

	// TargetWorker is the worker the run will continue on.
	TargetWorker string `json:"target_worker,omitempty"`

	// Status is the current state of the handoff.
	Status HandoffStatus `json:"status"`

	// Reason describes why the handoff was initiated.
	Reason string `json:"reason"`

	// Checkpoint captures the safe boundary where the run was paused.
	Checkpoint HandoffCheckpoint `json:"checkpoint"`

	// ConversationSummary is a summary of the conversation so far.
	ConversationSummary string `json:"conversation_summary,omitempty"`

	// RunContract is the original run contract (possibly amended).
	RunContract *RunContract `json:"run_contract"`

	// CurrentTodos are the outstanding todos at the checkpoint.
	CurrentTodos []string `json:"current_todos,omitempty"`

	// PatchRefs are references to any patches produced so far.
	PatchRefs []string `json:"patch_refs,omitempty"`

	// ArtifactRefs are references to artifacts produced so far.
	ArtifactRefs []string `json:"artifact_refs,omitempty"`

	// WorkspaceFingerprint is an opaque identifier for the workspace state.
	WorkspaceFingerprint string `json:"workspace_fingerprint,omitempty"`

	// NonPortableState describes state that cannot be moved.
	NonPortableState []string `json:"non_portable_state,omitempty"`

	// Lineage tracks the chain of workers that have executed this run.
	Lineage []HandoffLineageEntry `json:"lineage"`

	// CreatedAt is when the handoff package was created.
	CreatedAt time.Time `json:"created_at"`
}

// HandoffCheckpoint describes the safe boundary where a run was paused.
type HandoffCheckpoint struct {
	// Boundary identifies where the pause occurred.
	Boundary string `json:"boundary"` // "before_start", "after_llm_turn", "after_tool_call", "after_tests", "before_final_review"

	// TurnNumber is the LLM turn number at the checkpoint.
	TurnNumber int `json:"turn_number"`

	// LastToolCall is the last tool call executed before the checkpoint.
	LastToolCall string `json:"last_tool_call,omitempty"`

	// PendingApprovals are any approvals waiting at the checkpoint.
	PendingApprovals []string `json:"pending_approvals,omitempty"`
}

// HandoffLineageEntry records a worker in the run's execution chain.
type HandoffLineageEntry struct {
	WorkerID  string    `json:"worker_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// HandoffManager orchestrates checkpointed task movement between workers.
type HandoffManager struct {
	workerStore WorkerStore
	packages    map[string]*HandoffPackage // runID → handoff package
}

// NewHandoffManager creates a new handoff manager.
func NewHandoffManager(ws WorkerStore) *HandoffManager {
	return &HandoffManager{
		workerStore: ws,
		packages:    make(map[string]*HandoffPackage),
	}
}

// CanHandoff checks whether a run with the given mobility class can be handed off.
func (hm *HandoffManager) CanHandoff(mobility MobilityClass) error {
	switch mobility {
	case MobilityPinned:
		return fmt.Errorf("handoff: run is pinned, cannot be moved")
	case MobilityEphemeral:
		return fmt.Errorf("handoff: run is ephemeral, cannot be handed off (state is not preserved)")
	case MobilityResumable, MobilityCloneable:
		return nil
	default:
		return fmt.Errorf("handoff: unknown mobility class %q", mobility)
	}
}

// ValidateHandoffTarget checks that the target worker is eligible to receive the handoff.
func (hm *HandoffManager) ValidateHandoffTarget(ctx context.Context, targetWorkerID string, pkg *HandoffPackage) error {
	target, err := hm.workerStore.GetWorker(ctx, targetWorkerID)
	if err != nil {
		return fmt.Errorf("handoff: target worker %q: %w", targetWorkerID, err)
	}

	// Target must be online or draining.
	if target.Status != WorkerStatusOnline && target.Status != WorkerStatusDraining {
		return fmt.Errorf("handoff: target worker %q is %s (must be online or draining)", targetWorkerID, target.Status)
	}

	// Target must not be the source worker.
	if target.ID == pkg.SourceWorker {
		return fmt.Errorf("handoff: target worker must differ from source worker %q", pkg.SourceWorker)
	}

	// Target must support required workspace modes.
	if pkg.RunContract != nil {
		requiredMode := pkg.RunContract.Workspace.Mode
		if requiredMode != "" && !hasString(target.SupportedWorkspaceModes, requiredMode) {
			return fmt.Errorf("handoff: target worker %q does not support workspace mode %q", targetWorkerID, requiredMode)
		}
	}

	return nil
}

// CreateHandoffPackage creates a handoff package for a run at a safe checkpoint.
func (hm *HandoffManager) CreateHandoffPackage(contract *RunContract, sourceWorker string, checkpoint HandoffCheckpoint, reason string) (*HandoffPackage, error) {
	if err := hm.CanHandoff(contract.Mobility); err != nil {
		return nil, err
	}

	pkg := &HandoffPackage{
		RunID:         contract.ID,
		SourceWorker:  sourceWorker,
		Status:        HandoffPending,
		Reason:        reason,
		Checkpoint:    checkpoint,
		RunContract:   contract,
		Lineage: []HandoffLineageEntry{
			{
				WorkerID:  sourceWorker,
				StartedAt: contract.Metadata.CreatedAt,
				EndedAt:   time.Now(),
				Reason:    reason,
			},
		},
		NonPortableState: hm.identifyNonPortableState(contract),
		CreatedAt:        time.Now(),
	}

	hm.packages[contract.ID] = pkg
	return pkg, nil
}

// AssignTarget assigns a target worker to a pending handoff package.
func (hm *HandoffManager) AssignTarget(ctx context.Context, runID, targetWorkerID string) error {
	pkg, ok := hm.packages[runID]
	if !ok {
		return fmt.Errorf("handoff: no package for run %q", runID)
	}

	if pkg.Status != HandoffPending {
		return fmt.Errorf("handoff: package for run %q is in status %q, expected pending", runID, pkg.Status)
	}

	if err := hm.ValidateHandoffTarget(ctx, targetWorkerID, pkg); err != nil {
		return err
	}

	pkg.TargetWorker = targetWorkerID
	pkg.Status = HandoffInProgress
	return nil
}

// CompleteHandoff marks a handoff as complete and records the lineage.
func (hm *HandoffManager) CompleteHandoff(runID string) error {
	pkg, ok := hm.packages[runID]
	if !ok {
		return fmt.Errorf("handoff: no package for run %q", runID)
	}

	pkg.Status = HandoffCompleted
	pkg.Lineage = append(pkg.Lineage, HandoffLineageEntry{
		WorkerID:  pkg.TargetWorker,
		StartedAt: time.Now(),
	})

	return nil
}

// GetHandoffStatus returns the handoff status for a run.
func (hm *HandoffManager) GetHandoffStatus(runID string) HandoffStatus {
	pkg, ok := hm.packages[runID]
	if !ok {
		return HandoffNone
	}
	return pkg.Status
}

// GetHandoffPackage returns the handoff package for a run.
func (hm *HandoffManager) GetHandoffPackage(runID string) (*HandoffPackage, error) {
	pkg, ok := hm.packages[runID]
	if !ok {
		return nil, fmt.Errorf("handoff: no package for run %q", runID)
	}
	return pkg, nil
}

// identifyNonPortableState returns a list of state that cannot be moved.
func (hm *HandoffManager) identifyNonPortableState(contract *RunContract) []string {
	var nonPortable []string

	if contract.Workspace.Mode == "local" && contract.Workspace.RepoURL == "" {
		nonPortable = append(nonPortable, "dirty local workspace (no repo URL)")
	}
	if contract.Workspace.Mode == "local" {
		nonPortable = append(nonPortable, "local file changes outside git")
	}

	return nonPortable
}

// hasString returns true if the slice contains the given string.
func hasString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
