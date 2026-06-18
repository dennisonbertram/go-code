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
	// Isolation selects the isolation mode: "" (inline) or "worktree".
	Isolation string `json:"isolation,omitempty"`
	// AgentType selects a custom sub-agent type (e.g. "Explore", "code-reviewer").
	AgentType string `json:"agent_type,omitempty"`
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
	EventWorkflowStarted       EventType = "workflow.started"
	EventWorkflowPhaseStarted  EventType = "workflow.phase.started"
	EventWorkflowAgentStarted  EventType = "workflow.agent.started"
	EventWorkflowAgentCompleted EventType = "workflow.agent.completed"
	EventWorkflowAgentFailed   EventType = "workflow.agent.failed"
	EventWorkflowLog           EventType = "workflow.log"
	EventWorkflowCompleted     EventType = "workflow.completed"
	EventWorkflowFailed        EventType = "workflow.failed"
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
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`
	Isolation   string `json:"isolation,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	MaxSteps    int    `json:"max_steps,omitempty"`
	MaxCostUSD  float64 `json:"max_cost_usd,omitempty"`
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

// PipelineStage is a function that processes one item through one stage.
// prev is the result from the previous stage (nil for the first stage).
// item is the original input item.
// index is the position of the item in the original list.
// Returns the result for this stage, which becomes prev for the next stage.
type PipelineStage func(prev any, item any, index int) (any, error)

// Store is an optional persistence interface for workflow runs and events.
type Store interface {
	CreateRun(ctx context.Context, run *Run) error
	UpdateRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	AppendEvent(ctx context.Context, event *Event) error
	GetEvents(ctx context.Context, runID string, afterSeq int64) ([]Event, error)
}

// memoryStore is an in-memory Store implementation.
type memoryStore struct {
	mu     sync.RWMutex
	runs   map[string]*Run
	events map[string][]Event
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		runs:   make(map[string]*Run),
		events: make(map[string][]Event),
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *run
	return &cp, nil
}

func (m *memoryStore) AppendEvent(_ context.Context, event *Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[event.RunID] = append(m.events[event.RunID], *event)
	return nil
}

func (m *memoryStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	source := m.events[runID]
	out := make([]Event, 0, len(source))
	for _, ev := range source {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}
