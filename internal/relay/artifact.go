package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ArtifactType classifies what kind of artifact was produced.
type ArtifactType string

const (
	ArtifactPatch       ArtifactType = "patch"
	ArtifactPR          ArtifactType = "pr"
	ArtifactComment     ArtifactType = "comment"
	ArtifactTestLog     ArtifactType = "test_log"
	ArtifactScreenshot  ArtifactType = "screenshot"
	ArtifactSummary     ArtifactType = "summary"
	ArtifactLog         ArtifactType = "log"
	ArtifactApprovalReq ArtifactType = "approval_request"
)

// Artifact represents a durable output of a run.
type Artifact struct {
	// ID is a stable identifier for this artifact.
	ID string `json:"id"`

	// RunID links this artifact to its source run.
	RunID string `json:"run_id"`

	// Type classifies the artifact.
	Type ArtifactType `json:"type"`

	// WorkerID identifies which worker produced this artifact.
	WorkerID string `json:"worker_id"`

	// MIMEType is the content type (e.g. "text/plain", "image/png").
	MIMEType string `json:"mime_type,omitempty"`

	// Data is the artifact content (for small artifacts).
	// For large artifacts, use Ref.
	Data string `json:"data,omitempty"`

	// Ref is an external reference for large artifacts.
	Ref string `json:"ref,omitempty"`

	// URL is an optional public URL for the artifact.
	URL string `json:"url,omitempty"`

	// Visibility controls who can see this artifact.
	Visibility string `json:"visibility"` // "public", "tenant", "run"

	// Redacted is true if sensitive content has been removed.
	Redacted bool `json:"redacted"`

	// CreatedAt is when the artifact was produced.
	CreatedAt time.Time `json:"created_at"`
}

// EventRecord is a persisted transport event from a worker.
type EventRecord struct {
	// Seq is the monotonically increasing sequence number within the run.
	Seq int `json:"seq"`

	// RunID identifies the run.
	RunID string `json:"run_id"`

	// EventID uniquely identifies this event.
	EventID string `json:"event_id"`

	// EventType classifies the event.
	EventType string `json:"event_type"`

	// Payload is the JSON-encoded event payload.
	Payload string `json:"payload"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// WorkerID identifies which worker emitted this event.
	WorkerID string `json:"worker_id"`
}

// EventAndArtifactStore persists events, artifacts, and placement records.
type EventAndArtifactStore interface {
	// SavePlacementRecord persists a placement record.
	SavePlacementRecord(ctx context.Context, record *PlacementRecord) error

	// GetPlacementRecord retrieves the placement record for a run.
	// Returns ErrArtifactNotFound if not found.
	GetPlacementRecord(ctx context.Context, runID string) (*PlacementRecord, error)

	// AppendEvent appends an event record to a run's event log.
	AppendEvent(ctx context.Context, event *EventRecord) error

	// GetEvents returns events for a run with seq > afterSeq.
	// Pass afterSeq=-1 to get all events.
	GetEvents(ctx context.Context, runID string, afterSeq int) ([]*EventRecord, error)

	// SaveArtifact persists an artifact.
	SaveArtifact(ctx context.Context, artifact *Artifact) error

	// GetArtifact retrieves an artifact by ID.
	// Returns ErrArtifactNotFound if not found.
	GetArtifact(ctx context.Context, id string) (*Artifact, error)

	// ListArtifacts returns artifacts for a run.
	ListArtifacts(ctx context.Context, runID string) ([]*Artifact, error)

	// Close releases resources.
	Close() error
}

// Sentinel errors.
var (
	ErrArtifactNotFound      = errors.New("relay: artifact not found")
	ErrArtifactAlreadyExists = errors.New("relay: artifact already exists")
)

// PlacementRecordToJSON marshals a placement record.
func PlacementRecordToJSON(record *PlacementRecord) ([]byte, error) {
	return json.Marshal(record)
}

// PlacementRecordFromJSON unmarshals a placement record.
func PlacementRecordFromJSON(data []byte) (*PlacementRecord, error) {
	pr := &PlacementRecord{}
	if err := json.Unmarshal(data, pr); err != nil {
		return nil, fmt.Errorf("relay: unmarshal placement record: %w", err)
	}
	return pr, nil
}
