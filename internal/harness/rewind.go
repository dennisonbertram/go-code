package harness

import (
	"context"
	"time"
)

// RewindFileSnapshot is the pre-edit state of one workspace file. Exists=false
// records that the agent created the file, so rewinding removes it.
type RewindFileSnapshot struct {
	Path         string `json:"path"`
	Content      []byte `json:"-"`
	Exists       bool   `json:"exists"`
	Skipped      bool   `json:"skipped,omitempty"`
	SkipReason   string `json:"skip_reason,omitempty"`
	ExpectedHash string `json:"expected_hash,omitempty"`
}

// RewindPoint groups pre-images captured immediately before a mutating tool call.
type RewindPoint struct {
	ID             string               `json:"id"`
	ConversationID string               `json:"conversation_id"`
	Step           int                  `json:"step"`
	Tool           string               `json:"tool"`
	CreatedAt      time.Time            `json:"created_at"`
	Files          []RewindFileSnapshot `json:"files"`
}

// RewindStore is deliberately optional so existing ConversationStore adapters
// remain source compatible.
type RewindStore interface {
	SaveRewindPoint(context.Context, RewindPoint) error
	ListRewindPoints(context.Context, string) ([]RewindPoint, error)
}
