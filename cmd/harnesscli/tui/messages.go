package tui

import (
	"encoding/json"
	"time"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// ─── SSE Stream Messages ────────────────────────────────────────────────────

// SSEEventMsg carries a decoded harness event from the SSE stream.
type SSEEventMsg struct {
	EventType string
	Raw       json.RawMessage
}

// SSEErrorMsg signals a stream read/parse error.
type SSEErrorMsg struct{ Err error }

// SSEDoneMsg signals the stream ended (run.completed or run.failed).
type SSEDoneMsg struct {
	EventType string
	Error     string // non-empty on run.failed
}

// SSEDropMsg signals a message was dropped due to channel backpressure.
type SSEDropMsg struct{}

// ─── Assistant Messages ──────────────────────────────────────────────────────

// AssistantDeltaMsg carries a streaming text delta from the assistant.
type AssistantDeltaMsg struct{ Delta string }

// ThinkingDeltaMsg carries a streaming thinking/reasoning delta.
type ThinkingDeltaMsg struct{ Delta string }

// ─── Tool Call Messages ──────────────────────────────────────────────────────

// ToolStartMsg signals a tool call has begun.
type ToolStartMsg struct {
	CallID string
	Name   string
	Input  json.RawMessage
}

// ToolResultMsg signals a tool call completed with output.
type ToolResultMsg struct {
	CallID string
	Output string
}

// ToolErrorMsg signals a tool call failed.
type ToolErrorMsg struct {
	CallID string
	Err    error
}

// ToolCallChunkMsg is emitted when a streaming tool result chunk arrives.
type ToolCallChunkMsg struct {
	CallID string
	Chunk  string
	Done   bool // true when this is the final chunk
}

// ─── Run Lifecycle Messages ──────────────────────────────────────────────────

// RunStartedMsg signals a new run has been started.
type RunStartedMsg struct{ RunID string }

// RunCompletedMsg signals a run completed successfully.
type RunCompletedMsg struct{ RunID string }

// RunFailedMsg signals a run failed.
type RunFailedMsg struct {
	RunID string
	Error string
}

// ─── Usage / Cost Messages ───────────────────────────────────────────────────

// UsageDeltaMsg carries incremental token and cost usage.
type UsageDeltaMsg struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// ─── UI Action Messages ──────────────────────────────────────────────────────

// SpinnerTickMsg advances the thinking spinner animation.
type SpinnerTickMsg struct{}

// CommandMsg carries a parsed slash command from the input area.
type CommandMsg struct{ Input string }

// ClearMsg requests clearing the conversation view.
type ClearMsg struct{}

// OverlayOpenMsg requests opening a named overlay (help, context, stats, etc.).
type OverlayOpenMsg struct{ Kind string }

// OverlayCloseMsg requests closing the current overlay.
type OverlayCloseMsg struct{}

// WindowSizeMsg carries terminal dimension changes.
// Components that need size can also handle tea.WindowSizeMsg directly.
type WindowSizeMsg struct {
	Width  int
	Height int
}

// InterruptedMsg is emitted when the user cancels an active run.
type InterruptedMsg struct{ At time.Time }

// EscapeMsg is emitted when Escape closes an overlay.
type EscapeMsg struct{}

// ExportTranscriptMsg is emitted when a transcript export completes.
type ExportTranscriptMsg struct{ FilePath string }

// ─── Plan Mode Messages ───────────────────────────────────────────────────────

// PlanProposedMsg is sent (by tests or by the SSE stub) to display a plan in
// the plan overlay without requiring a live server event.
type PlanProposedMsg struct {
	Plan string // plan content (plain text / markdown)
}

// PlanApprovedMsg is emitted when the user approves the current plan.
type PlanApprovedMsg struct{}

// PlanRejectedMsg is emitted when the user rejects the current plan.
type PlanRejectedMsg struct{}

// ModelSwitchedMsg is emitted when the user selects a new model in the model switcher.
type ModelSwitchedMsg struct{ ModelID string }

// ModelSelectedMsg is emitted when the user confirms model + reasoning selection.
type ModelSelectedMsg struct {
	ModelID         string
	Provider        string
	ReasoningEffort string // "" | "low" | "medium" | "high"
}

type SubagentsLoadedMsg struct{ Subagents []RemoteSubagent }

type SubagentsLoadFailedMsg struct{ Err string }

// RunsFetchedMsg carries recent run metadata fetched by /runs.
type RunsFetchedMsg struct {
	Runs []tuiRunRecord
	Err  string
}

// RunControlResultMsg carries a one-shot run control result for commands such
// as /cancel and /replay.
type RunControlResultMsg struct {
	Kind   string
	RunID  string
	Output string
	Err    string
}

// statusTickMsg is sent after statusMsgDuration to clear the transient status bar message.
type statusTickMsg struct{}

// ModelsFetchedMsg carries the model list fetched from the server.
type ModelsFetchedMsg struct {
	Models []modelswitcher.ServerModelEntry
	Source string // "openrouter" or "" (harnessd)
}

// ModelsFetchErrorMsg carries a fetch error from the /v1/models endpoint.
type ModelsFetchErrorMsg struct {
	Err string
}

// GatewaySelectedMsg is emitted when the user confirms a routing gateway.
type GatewaySelectedMsg struct {
	Gateway string // "" = direct, "openrouter" = OpenRouter
}

// ProviderInfo describes a single provider from GET /v1/providers.
type ProviderInfo struct {
	Name       string
	Configured bool
	APIKeyEnv  string
}

// ProvidersLoadedMsg carries results from GET /v1/providers.
type ProvidersLoadedMsg struct {
	Providers []ProviderInfo
}

// APIKeySetMsg is emitted after a key is successfully sent to the server.
type APIKeySetMsg struct {
	Provider string
	Key      string
}

// ProfilesLoadedMsg carries the profile list fetched from GET /v1/profiles.
type ProfilesLoadedMsg struct {
	Entries []ProfileEntry
	Err     error
}

// ProfileEntry is a simplified view of a profile for the TUI picker.
type ProfileEntry struct {
	Name        string
	Description string
	Model       string
	ToolCount   int
	SourceTier  string
}

// SessionPickerSelectedMsg is emitted when the user selects a session from the
// session picker overlay.  The model wires this to update conversationID.
type SessionPickerSelectedMsg struct {
	SessionID string
}

// SessionRunsFetchedMsg carries the run IDs for a conversation fetched from
// GET /v1/conversations/{id}/runs.  RunIDs is empty when the server returns 501
// (no run store configured) or on any other error.
type SessionRunsFetchedMsg struct {
	ConversationID string
	RunIDs         []string
}

// SessionDeletedMsg is emitted when the user deletes a session from the picker.
// The model should remove it from the persistent store.
type SessionDeletedMsg struct {
	ID string
}

// TranscriptEntryMsg injects a transcript entry directly into the model.
// Used in tests to set up transcript state without running a full SSE session.
type TranscriptEntryMsg struct {
	Role    string
	Content string
}

// ConversationMessage is a minimal view of harness.Message used to render
// resumed conversation history in the TUI transcript.
type ConversationMessage struct {
	Role    string
	Content string
}

// ConversationHistoryMsg carries the message history for a resumed
// conversation, fetched from GET /v1/conversations/{id}/messages.
type ConversationHistoryMsg struct {
	ConversationID string
	Messages       []ConversationMessage
}

// ConversationHistoryErrorMsg signals that fetching a resumed conversation's
// history failed.
type ConversationHistoryErrorMsg struct {
	ConversationID string
	Err            string
}
