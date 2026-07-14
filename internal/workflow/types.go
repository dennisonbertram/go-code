// Package workflow provides a Claude Code-style workflow orchestration engine.
//
// It mirrors the Claude Code Workflow feature with one-to-one equivalence:
//   - agent(prompt, opts?) — spawn a sub-agent, optionally with schema validation
//   - parallel(thunks) — run tasks concurrently with a barrier
//   - pipeline(items, stages...) — run items through stages without barriers
//   - phase(title) — start a new progress phase
//   - log(message) — emit a progress message
//   - budget — token budget tracking (total, spent, remaining)
//   - args — input parameters available to scripts
//   - workflow(name, args) — run nested workflows
//
// Scripts are Go functions that receive a *Context and return (any, error).
package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Meta describes a workflow for registration and discovery.
type Meta struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Phases      []PhaseInfo `json:"phases,omitempty"`
	WhenToUse   string      `json:"when_to_use,omitempty"`
}

// PhaseInfo describes a phase for progress display.
type PhaseInfo struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Script is a workflow script function.
// It receives the execution context (including Args and Budget) and returns
// a result or an error. Panics in scripts are recovered and converted to errors.
type Script func(ctx *Context) (any, error)

// RunStatus is the lifecycle status of a workflow run.
type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

// Run represents a single workflow execution.
type Run struct {
	ID           string    `json:"id"`
	WorkflowName string    `json:"workflow_name"`
	Status       RunStatus `json:"status"`
	ResultJSON   string    `json:"result_json,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AgentResult is the result returned by an agent() call.
type AgentResult struct {
	Output string `json:"output"`
	Schema any    `json:"schema,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AgentOpts configures an agent call.
type AgentOpts struct {
	// Label is a short human-readable label shown in progress display.
	Label string `json:"label,omitempty"`
	// Phase assigns this agent to a progress group. Agents with the same
	// phase string appear under the same group in progress display.
	Phase string `json:"phase,omitempty"`
	// Schema is an optional JSON Schema for structured output validation.
	// When set, the agent's output is parsed and validated against this schema.
	Schema map[string]any `json:"schema,omitempty"`
	// Model overrides the model used for this specific agent call.
	Model string `json:"model,omitempty"`
	// Provider overrides the provider used for this specific agent call.
	Provider string `json:"provider,omitempty"`
	// Profile selects a named sub-agent profile.
	Profile string `json:"profile,omitempty"`
	// AllowedTools constrains the sub-agent tool set.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// Isolation selects the isolation mode: "" (inline) or "worktree".
	Isolation string `json:"isolation,omitempty"`
	// CleanupPolicy controls cleanup for isolated sub-agent worktrees.
	CleanupPolicy string `json:"cleanup_policy,omitempty"`
	// AgentType selects a custom sub-agent type (e.g. "Explore", "code-reviewer").
	AgentType string `json:"agent_type,omitempty"`
	// MaxSteps constrains the child run's maximum step count.
	MaxSteps int `json:"max_steps,omitempty"`
	// MaxCostUSD constrains the child run's cost ceiling.
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
}

// Budget tracks token usage across a workflow run. It is thread-safe.
// Total of 0 means unlimited budget.
type Budget struct {
	Total  int
	spent  atomic.Int64
	parent *Budget // for nested workflows sharing a parent budget
}

// Spent returns the number of tokens spent so far.
func (b *Budget) Spent() int {
	return int(b.spent.Load())
}

// Remaining returns the remaining token budget. Returns MaxInt when Total is 0 (unlimited).
func (b *Budget) Remaining() int {
	if b.Total == 0 {
		return int(^uint(0) >> 1) // MaxInt
	}
	remaining := b.Total - b.Spent()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Spend atomically adds n tokens to the spent counter. If a parent budget
// is set (nested workflow), the parent is also charged.
func (b *Budget) Spend(n int) {
	b.spent.Add(int64(n))
	if b.parent != nil {
		b.parent.Spend(n)
	}
}

// Clone returns a copy of the budget. For nested workflows, the clone shares
// the parent so that spending is tracked across the whole tree.
func (b *Budget) Clone() *Budget {
	return &Budget{
		Total:  b.Total,
		parent: b,
	}
}

// NewBudget creates a budget with the given total. Total of 0 means unlimited.
func NewBudget(total int) *Budget {
	return &Budget{Total: total}
}

// newBudget is an internal alias for use within the package.
func newBudget(total int) *Budget {
	return NewBudget(total)
}

// EventType identifies the type of a workflow event.
type EventType string

const (
	EventWorkflowStarted        EventType = "workflow.started"
	EventWorkflowPhaseStarted   EventType = "workflow.phase.started"
	EventWorkflowAgentStarted   EventType = "workflow.agent.started"
	EventWorkflowAgentCompleted EventType = "workflow.agent.completed"
	EventWorkflowAgentFailed    EventType = "workflow.agent.failed"
	EventWorkflowLog            EventType = "workflow.log"
	EventWorkflowFeedback       EventType = "workflow.feedback"
	EventWorkflowFinding        EventType = "workflow.finding"
	EventWorkflowWarning        EventType = "workflow.warning"
	EventWorkflowQuestion       EventType = "workflow.question"
	EventWorkflowCompleted      EventType = "workflow.completed"
	EventWorkflowFailed         EventType = "workflow.failed"
)

// Event is emitted during workflow execution.
type Event struct {
	Seq       int64          `json:"seq"`
	RunID     string         `json:"run_id"`
	Type      EventType      `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// SubagentRequest mirrors the input needed to create a sub-agent run.
type SubagentRequest struct {
	Prompt        string   `json:"prompt"`
	Model         string   `json:"model,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Profile       string   `json:"profile,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	Isolation     string   `json:"isolation,omitempty"`
	CleanupPolicy string   `json:"cleanup_policy,omitempty"`
	AgentType     string   `json:"agent_type,omitempty"`
	MaxSteps      int      `json:"max_steps,omitempty"`
	MaxCostUSD    float64  `json:"max_cost_usd,omitempty"`
}

// SubagentResult is the result of a completed sub-agent run.
type SubagentResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SubagentManager is the interface for creating and tracking sub-agents.
// It is satisfied by subagents.Manager from the internal/subagents package.
type SubagentManager interface {
	Create(ctx context.Context, req SubagentRequest) (SubagentResult, error)
	Get(ctx context.Context, id string) (SubagentResult, error)
}

// QuestionOption is a single selectable answer for a workflow question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionRequest is emitted when a workflow needs input from the parent/user.
type QuestionRequest struct {
	RunID   string           `json:"run_id"`
	CallID  string           `json:"call_id"`
	Prompt  string           `json:"prompt"`
	Choices []QuestionOption `json:"choices,omitempty"`
}

// QuestionResponder handles workflow questions. Implementations may suspend
// through a checkpoint broker, return a deterministic answer in tests, or
// decline with an error when questions are unavailable.
type QuestionResponder interface {
	AskWorkflowQuestion(ctx context.Context, req QuestionRequest) (any, error)
}

// PipelineStage is a function that processes one item through one stage.
// prev is the result from the previous stage (nil for the first stage).
// item is the original input item.
// index is the position of the item in the original list.
// Returns the result for this stage, which becomes prev for the next stage.
type PipelineStage func(prev any, item any, index int) (any, error)

// Store is an optional persistence interface for workflow runs and events.
//
// IMPORTANT — locking contract: AppendEvent is called by Engine.emit
// while the Engine holds its own internal global lock (guarding run
// registration, subscriber fan-out, etc.). Implementations of
// AppendEvent (and, transitively, anything it shares a lock or resource
// with) MUST NOT block on unbounded I/O and MUST NOT call back into the
// Engine (e.g. Subscribe, GetRun, Start) — the Engine's lock is a
// sync.Mutex, which is not reentrant, so a re-entrant call from inside
// AppendEvent self-deadlocks. A Store that blocks for a long time in
// AppendEvent will stall the Engine's global lock, and therefore every
// concurrently-running workflow, for the duration of that block. The
// default in-memory Store meets this contract (AppendEvent is an O(1)
// amortized slice append, no I/O, no callback); a persistent Store
// implementation should offload slow writes (e.g. queue them and flush
// asynchronously) rather than perform them synchronously inside
// AppendEvent.
type Store interface {
	CreateRun(ctx context.Context, run *Run) error
	UpdateRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	AppendEvent(ctx context.Context, event *Event) error
	GetEvents(ctx context.Context, runID string, afterSeq int64) ([]Event, error)
}

// runEvents holds one run's event history behind its own lock. Before
// this type existed, memoryStore used a single RWMutex shared across
// every run's events; a slow read for run A would force AppendEvent for
// run B to wait too, since sync.RWMutex.Lock() must wait for all current
// readers regardless of which run they're reading. Splitting the lock
// per-run fixed THAT specific cross-run case, but on its own does NOT
// make a slow GetEvents engine-wide-safe: see the comment on GetEvents
// below for why the per-run RLock must also only be held for an O(1)
// snapshot, not the whole O(history) copy.
type runEvents struct {
	mu     sync.RWMutex
	events []Event
}

// memoryStoreGetEventsPostSnapshotHook is a test-only seam. See
// memoryStore.GetEvents for where it fires and why.
var memoryStoreGetEventsPostSnapshotHook func()

// memoryStore is an in-memory Store implementation.
type memoryStore struct {
	mu   sync.Mutex // protects only the two maps below (run lookups/creation), never the O(history) work
	runs map[string]*Run

	eventsMu sync.Mutex // protects perRun (creation of new per-run entries) — never held during a history copy
	perRun   map[string]*runEvents
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		runs:   make(map[string]*Run),
		perRun: make(map[string]*runEvents),
	}
}

func (m *memoryStore) CreateRun(_ context.Context, run *Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *memoryStore) UpdateRun(_ context.Context, run *Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *memoryStore) GetRun(_ context.Context, id string) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *run
	return &cp, nil
}

// runEventsFor returns the per-run event log, creating it on first use.
// Only the O(1) map lookup/creation happens under eventsMu; the returned
// *runEvents has its own independent lock for the actual O(history) work.
func (m *memoryStore) runEventsFor(runID string) *runEvents {
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	re, ok := m.perRun[runID]
	if !ok {
		re = &runEvents{}
		m.perRun[runID] = re
	}
	return re
}

func (m *memoryStore) AppendEvent(_ context.Context, event *Event) error {
	re := m.runEventsFor(event.RunID)
	re.mu.Lock()
	defer re.mu.Unlock()
	re.events = append(re.events, *event)
	return nil
}

// GetEvents holds the per-run RLock only long enough to take an O(1)
// snapshot of the events slice header, then copies OUTSIDE any lock.
//
// This matters because emit() (engine.go) calls AppendEvent while
// holding e.mu, the engine's single global lock. If GetEvents held its
// per-run RLock for the whole O(history) copy (as an earlier version of
// this method did), a concurrent emit() on THAT SAME run would acquire
// e.mu and then block on AppendEvent's Lock() behind this RLock --
// transitively holding e.mu, and therefore blocking every OTHER run's
// emit (and GetRun/List/Start/Resume), for the full duration of this
// one run's history copy. A per-run lock alone only narrows the
// trigger (which run has to be "slow" to cause it); it does not narrow
// the blast radius (every run stalls regardless), because the stall
// propagates back through e.mu.
//
// Race-free by the append-only invariant: AppendEvent only ever writes
// at index >= len(snapshot) (either into existing spare capacity, which
// the snapshot's capped 3-index slice expression makes invisible to
// this read, or into a freshly-allocated array on realloc, which never
// touches the old one). This reader only ever touches [0, len(snapshot)),
// which is disjoint from anything a concurrent append can write to.
func (m *memoryStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]Event, error) {
	re := m.runEventsFor(runID)
	re.mu.RLock()
	snapshot := re.events[:len(re.events):len(re.events)]
	re.mu.RUnlock()

	// Test-only seam (nil/no-op in production): fires after the per-run
	// RLock above has already been released, letting a test hold up the
	// "slow copy" part of GetEvents deterministically while proving the
	// lock itself is not held during it (a concurrent AppendEvent for the
	// same run should complete immediately). See
	// TestMemoryStoreGetEventsReleasesLockBeforeCopy.
	if memoryStoreGetEventsPostSnapshotHook != nil {
		memoryStoreGetEventsPostSnapshotHook()
	}

	out := make([]Event, 0, len(snapshot))
	for _, ev := range snapshot {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}
