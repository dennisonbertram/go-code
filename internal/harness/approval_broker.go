package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrNoPendingApproval is returned by Approve/Deny when there is no
	// pending approval request for the given run ID.
	ErrNoPendingApproval = errors.New("no pending approval")
)

// PlanApproachOption is one labeled approach the agent offered in its plan's
// trailing "## Approaches" section. IDs are assigned positionally ("a", "b",
// "c"); 1-3 options are allowed, anything else is treated as no options.
type PlanApproachOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ApprovalRequest holds the details of a tool call awaiting operator approval.
type ApprovalRequest struct {
	RunID   string
	CallID  string
	Tool    string
	Args    string
	Timeout time.Duration
	// Options lists the approach options presented for a plan-exit approval
	// (Tool == "plan_exit"). Empty for ordinary tool approvals.
	Options []PlanApproachOption
}

// PendingApproval is the read-only view of a pending approval entry.
// Returned by InMemoryApprovalBroker.Pending().
type PendingApproval struct {
	RunID      string               `json:"run_id"`
	CallID     string               `json:"call_id"`
	Tool       string               `json:"tool"`
	Args       string               `json:"args"`
	DeadlineAt time.Time            `json:"deadline_at"`
	Options    []PlanApproachOption `json:"options,omitempty"`
}

// ApprovalTimeoutError is returned by InMemoryApprovalBroker.Ask when the
// deadline passes without an approve or deny decision.
type ApprovalTimeoutError struct {
	RunID      string
	CallID     string
	DeadlineAt time.Time
}

func (e *ApprovalTimeoutError) Error() string {
	return fmt.Sprintf("approval timeout for run %q call %q (deadline %s)",
		e.RunID, e.CallID, e.DeadlineAt.Format(time.RFC3339))
}

// IsApprovalTimeout reports whether err is an *ApprovalTimeoutError.
func IsApprovalTimeout(err error) bool {
	var te *ApprovalTimeoutError
	return errors.As(err, &te)
}

// approvalDecision is the outcome sent over the internal channel.
type approvalDecision struct {
	approved bool   // true = approve, false = deny
	option   string // selected plan approach option ID, "" for a plain approve/deny
}

type pendingApprovalEntry struct {
	pending    PendingApproval
	decisionCh chan approvalDecision // buffered(1), closed on decision
}

// ApprovalBroker is the interface for pausing/resuming tool calls that require
// operator approval.
//
// Ask() is called by the runner's execute() loop to pause a tool call pending
// approval. It blocks until Approve(), ApproveWithOption(), Deny(), context
// cancellation, or timeout. The returned option ID is the operator's selected
// plan approach option ("" when none was offered or chosen). Approve() and
// Deny() are called by HTTP handlers for POST /v1/runs/{id}/approve and
// POST /v1/runs/{id}/deny; ApproveWithOption is used when the operator picked
// one of the presented plan approach options.
type ApprovalBroker interface {
	Ask(ctx context.Context, req ApprovalRequest) (approved bool, selectedOption string, err error)
	Pending(runID string) (PendingApproval, bool)
	Approve(runID string) error
	ApproveWithOption(runID, option string) error
	Deny(runID string) error
}

// InMemoryApprovalBroker implements ApprovalBroker using in-process channels.
// It is safe for concurrent use and has no external dependencies.
type InMemoryApprovalBroker struct {
	mu      sync.Mutex
	pending map[string]*pendingApprovalEntry
}

// NewInMemoryApprovalBroker constructs a new InMemoryApprovalBroker.
func NewInMemoryApprovalBroker() *InMemoryApprovalBroker {
	return &InMemoryApprovalBroker{
		pending: make(map[string]*pendingApprovalEntry),
	}
}

// Ask registers a pending approval for the given run and blocks until an
// operator calls Approve(), ApproveWithOption(), or Deny(), the context is
// cancelled, or the timeout elapses. Returns (true, option, nil) on approval —
// option is the ID of the operator's selected plan approach option, or "" for
// a plain approve — (false, "", nil) on denial, and (false, "", err) on
// timeout or context cancellation.
func (b *InMemoryApprovalBroker) Ask(ctx context.Context, req ApprovalRequest) (bool, string, error) {
	if req.Timeout <= 0 {
		req.Timeout = 5 * time.Minute
	}
	deadlineAt := time.Now().UTC().Add(req.Timeout)

	entry := &pendingApprovalEntry{
		pending: PendingApproval{
			RunID:      req.RunID,
			CallID:     req.CallID,
			Tool:       req.Tool,
			Args:       req.Args,
			DeadlineAt: deadlineAt,
			Options:    req.Options,
		},
		decisionCh: make(chan approvalDecision, 1),
	}

	b.mu.Lock()
	if _, exists := b.pending[req.RunID]; exists {
		b.mu.Unlock()
		return false, "", fmt.Errorf("pending approval already exists for run %q", req.RunID)
	}
	b.pending[req.RunID] = entry
	b.mu.Unlock()

	timer := time.NewTimer(req.Timeout)
	defer timer.Stop()

	select {
	case decision := <-entry.decisionCh:
		return decision.approved, decision.option, nil
	case <-timer.C:
		b.clearPendingIfMatch(req.RunID, entry)
		return false, "", &ApprovalTimeoutError{
			RunID:      req.RunID,
			CallID:     req.CallID,
			DeadlineAt: deadlineAt,
		}
	case <-ctx.Done():
		b.clearPendingIfMatch(req.RunID, entry)
		return false, "", ctx.Err()
	}
}

// Pending returns the PendingApproval for the given run ID, if any.
func (b *InMemoryApprovalBroker) Pending(runID string) (PendingApproval, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.pending[runID]
	if !ok {
		return PendingApproval{}, false
	}
	return entry.pending, true
}

// Approve resolves the pending approval for runID as approved with no selected
// option. Returns ErrNoPendingApproval if no approval is pending for that run.
func (b *InMemoryApprovalBroker) Approve(runID string) error {
	return b.resolve(runID, true, "")
}

// ApproveWithOption resolves the pending approval for runID as approved and
// records the operator's selected plan approach option ID.
// Returns ErrNoPendingApproval if no approval is pending for that run.
func (b *InMemoryApprovalBroker) ApproveWithOption(runID, option string) error {
	return b.resolve(runID, true, option)
}

// Deny resolves the pending approval for runID as denied.
// Returns ErrNoPendingApproval if no approval is pending for that run.
func (b *InMemoryApprovalBroker) Deny(runID string) error {
	return b.resolve(runID, false, "")
}

func (b *InMemoryApprovalBroker) resolve(runID string, approved bool, option string) error {
	b.mu.Lock()
	entry, ok := b.pending[runID]
	if !ok {
		b.mu.Unlock()
		return ErrNoPendingApproval
	}
	delete(b.pending, runID)
	b.mu.Unlock()

	entry.decisionCh <- approvalDecision{approved: approved, option: option}
	return nil
}

func (b *InMemoryApprovalBroker) clearPendingIfMatch(runID string, entry *pendingApprovalEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	current, ok := b.pending[runID]
	if !ok {
		return
	}
	if current == entry {
		delete(b.pending, runID)
	}
}
