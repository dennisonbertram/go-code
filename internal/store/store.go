// Package store provides a persistence layer for run state (runs, messages, events).
// It is separate from the existing ConversationStore in the harness package which
// persists conversation message history.
package store

import (
	"context"
	"time"
)

// RunStatus mirrors harness.RunStatus to avoid a circular import.
type RunStatus string

const (
	RunStatusQueued         RunStatus = "queued"
	RunStatusRunning        RunStatus = "running"
	RunStatusWaitingForUser RunStatus = "waiting_for_user"
	RunStatusCompleted      RunStatus = "completed"
	RunStatusFailed         RunStatus = "failed"
)

// WorkflowRecap is a searchable, deterministic summary of a completed personal
// harness task. It is derived from run state and tool traffic, not from another
// model call, so it works in offline/fake-provider workflows.
type WorkflowRecap struct {
	Goal                   string   `json:"goal,omitempty"`
	ChangedFiles           []string `json:"changed_files,omitempty"`
	TestsRun               []string `json:"tests_run,omitempty"`
	FailureCause           string   `json:"failure_cause,omitempty"`
	FixPattern             string   `json:"fix_pattern,omitempty"`
	UsefulCommands         []string `json:"useful_commands,omitempty"`
	NextContinuationPrompt string   `json:"next_continuation_prompt,omitempty"`
}

// Run holds persisted run state.
type Run struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	TenantID       string    `json:"tenant_id"`
	AgentID        string    `json:"agent_id"`
	Model          string    `json:"model"`
	ProviderName   string    `json:"provider_name,omitempty"`
	Prompt         string    `json:"prompt"`
	Status         RunStatus `json:"status"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	// UsageTotalsJSON is a JSON blob of RunUsageTotals.
	UsageTotalsJSON string `json:"-"`
	// CostTotalsJSON is a JSON blob of RunCostTotals.
	CostTotalsJSON string         `json:"-"`
	Recap          *WorkflowRecap `json:"recap,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Message holds a single message persisted for a run.
type Message struct {
	Seq              int    `json:"seq"`
	RunID            string `json:"run_id"`
	Role             string `json:"role"`
	Content          string `json:"content,omitempty"`
	ToolCallsJSON    string `json:"-"` // JSON-encoded []harness.ToolCall
	ToolCallID       string `json:"tool_call_id,omitempty"`
	Name             string `json:"name,omitempty"`
	IsMeta           bool   `json:"is_meta,omitempty"`
	IsCompactSummary bool   `json:"is_compact_summary,omitempty"`
}

// Event holds a single SSE event persisted for a run.
type Event struct {
	Seq       int       `json:"seq"`
	RunID     string    `json:"run_id"`
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	Payload   string    `json:"payload"` // JSON blob
	Timestamp time.Time `json:"timestamp"`
}

// RunFilter scopes ListRuns results.
// Zero-value fields are ignored (no filtering on that dimension).
type RunFilter struct {
	ConversationID string
	TenantID       string
	Status         RunStatus
}

// Store is the persistence interface for run state.
//
// All methods accept a context and return an error; callers should treat
// context.DeadlineExceeded / context.Canceled appropriately.
//
// Thread-safety: implementations must be safe for concurrent use.
type Store interface {
	// APIKeyStore embeds API key authentication operations (issue #9).
	APIKeyStore

	// CreateRun persists a new run record. Returns an error if a run with the
	// same ID already exists.
	CreateRun(ctx context.Context, run *Run) error

	// UpdateRun overwrites an existing run record with the supplied values.
	// The run must already exist (use CreateRun for initial persistence).
	UpdateRun(ctx context.Context, run *Run) error

	// GetRun retrieves a run by ID. Returns ErrNotFound if it does not exist.
	GetRun(ctx context.Context, id string) (*Run, error)

	// ListRuns returns runs matching filter, ordered by created_at DESC.
	// An empty filter returns all runs.
	ListRuns(ctx context.Context, filter RunFilter) ([]*Run, error)

	// AppendMessage appends a message to a run's message log.
	// seq must be monotonically increasing per run.
	AppendMessage(ctx context.Context, msg *Message) error

	// GetMessages returns all messages for a run, ordered by seq ASC.
	GetMessages(ctx context.Context, runID string) ([]*Message, error)

	// AppendEvent appends an event to a run's event log.
	// seq must be monotonically increasing per run.
	AppendEvent(ctx context.Context, event *Event) error

	// GetEvents returns events for a run with seq > afterSeq, ordered by seq ASC.
	// Pass afterSeq=-1 to get all events.
	GetEvents(ctx context.Context, runID string, afterSeq int) ([]*Event, error)

	// Close releases any resources held by the store (e.g. database connections).
	Close() error
}

// ErrNotFound is returned by GetRun when the requested run does not exist.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	return "store: run not found: " + e.ID
}

// IsNotFound returns true if the error is a NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}
