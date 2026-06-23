package harness

import (
	"context"
	"time"
)

// Conversation holds metadata for a conversation.
type Conversation struct {
	ID               string    `json:"id"`
	Title            string    `json:"title,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	MsgCount         int       `json:"message_count"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	Pinned           bool      `json:"pinned,omitempty"`
	Workspace        string    `json:"workspace,omitempty"`
	TenantID         string    `json:"tenant_id,omitempty"`
}

// ConversationTokenCost holds token usage and cost data for a conversation run.
// All fields reflect cumulative totals for the entire conversation (not a single turn).
type ConversationTokenCost struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// ConversationFilter optionally scopes ListConversations results.
// Empty fields are ignored (no filtering on that dimension).
type ConversationFilter struct {
	Workspace string
	TenantID  string
}

// MessageSearchResult is a single result from a full-text search over messages.
type MessageSearchResult struct {
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
	Snippet        string `json:"snippet"` // short excerpt around the match
}

// ConversationStore persists conversation messages across server restarts.
type ConversationStore interface {
	Migrate(ctx context.Context) error
	Close() error
	SaveConversation(ctx context.Context, convID string, msgs []Message) error
	// SaveConversationWithCost persists a conversation's messages along with
	// cumulative token usage and cost totals for the run.
	SaveConversationWithCost(ctx context.Context, convID string, msgs []Message, cost ConversationTokenCost) error
	LoadMessages(ctx context.Context, convID string) ([]Message, error)
	ListConversations(ctx context.Context, filter ConversationFilter, limit, offset int) ([]Conversation, error)
	DeleteConversation(ctx context.Context, convID string) error
	// UpdateConversationMeta sets the workspace and tenant_id on a conversation row.
	// It is safe to call multiple times; subsequent calls are no-ops if the values already match.
	UpdateConversationMeta(ctx context.Context, convID, workspace, tenantID string) error
	// GetConversationOwner returns the Conversation metadata row for convID,
	// or nil if the conversation does not exist in the store. It is used to
	// validate that a caller-supplied ConversationID belongs to the requesting
	// tenant before loading its history (cross-tenant disclosure prevention).
	GetConversationOwner(ctx context.Context, convID string) (*Conversation, error)
	// SearchMessages performs a full-text search over message content.
	// Returns up to limit results ordered by relevance. Returns empty slice (not error) for no matches.
	// When tenantID is non-empty, results are restricted to conversations owned by
	// that tenant (cross-tenant search-leak prevention). An empty tenantID disables
	// the tenant filter and searches all conversations (auth-disabled callers).
	SearchMessages(ctx context.Context, tenantID, query string, limit int) ([]MessageSearchResult, error)
	// DeleteOldConversations removes all non-pinned conversations whose updated_at is
	// before the given threshold. Returns the number of conversations deleted.
	// A zero threshold is a no-op (returns 0, nil) to prevent accidental mass deletion.
	DeleteOldConversations(ctx context.Context, olderThan time.Time) (int, error)
	// PinConversation sets or clears the pinned flag on a conversation.
	// Pinned conversations are never removed by the retention cleaner.
	// Returns an error if the conversation does not exist.
	PinConversation(ctx context.Context, convID string, pin bool) error
	// CompactConversation summarizes early conversation history. Messages with
	// step index >= keepFromStep are retained; older messages are discarded and
	// replaced by a single summary message inserted at step 0. Retained messages
	// are renumbered starting at step 1.
	//
	// keepFromStep=0 keeps all existing messages and prepends the summary.
	// keepFromStep > max_step keeps no existing messages (only the summary remains).
	// Returns an error if the conversation does not exist or keepFromStep < 0.
	CompactConversation(ctx context.Context, convID string, keepFromStep int, summary Message) error
}
