// Package relay implements the Go Relay multi-location control plane:
// worker registration, capability inventory, placement routing, run contract
// composition, event/artifact relay, and checkpointed handoff.
//
// Go Relay sits above go-code's execution runtime. It owns location
// registration, routing decisions, workflow composition, capability policy,
// event/artifact relay, and checkpointed handoff between locations.
// go-code owns executing one run inside one workspace.
package relay

import (
	"errors"
	"time"
)

// LocationType classifies where a worker runs.
type LocationType string

const (
	LocationLocal     LocationType = "local"
	LocationWorktree  LocationType = "worktree"
	LocationContainer LocationType = "container"
	LocationVM        LocationType = "vm"
	LocationSandbox   LocationType = "sandbox"
)

// ValidLocationTypes is the set of recognized location types.
var ValidLocationTypes = map[LocationType]bool{
	LocationLocal:     true,
	LocationWorktree:  true,
	LocationContainer: true,
	LocationVM:        true,
	LocationSandbox:   true,
}

// WorkerStatus represents the current liveness/availability of a worker.
type WorkerStatus string

const (
	// WorkerStatusOnline means the worker is connected and available.
	WorkerStatusOnline WorkerStatus = "online"
	// WorkerStatusOffline means the worker has explicitly disconnected.
	WorkerStatusOffline WorkerStatus = "offline"
	// WorkerStatusStale means the worker has not heartbeated within the expected interval.
	WorkerStatusStale WorkerStatus = "stale"
	// WorkerStatusDraining means the worker is finishing existing work but not accepting new runs.
	WorkerStatusDraining WorkerStatus = "draining"
)

// TrustTier represents the coarse trust level of a worker.
type TrustTier string

const (
	TrustTierUntrusted  TrustTier = "untrusted"
	TrustTierStandard   TrustTier = "standard"
	TrustTierPrivileged TrustTier = "privileged"
)

// ValidTrustTiers is the set of recognized trust tiers.
var ValidTrustTiers = map[TrustTier]bool{
	TrustTierUntrusted:  true,
	TrustTierStandard:   true,
	TrustTierPrivileged: true,
}

// Worker represents a registered execution location capable of running go-code tasks.
type Worker struct {
	// ID is a stable, deterministic identifier for this worker.
	// For local workers this is derived from host identity; for cloud workers
	// it is provisioned by the cloud backend.
	ID string `json:"id"`

	// TenantID scopes the worker to a tenant for isolation.
	TenantID string `json:"tenant_id"`

	// Name is a human-readable display name for the worker.
	Name string `json:"name"`

	// LocationType classifies where this worker runs.
	LocationType LocationType `json:"location_type"`

	// Status is the current liveness/availability state.
	Status WorkerStatus `json:"status"`

	// TrustTier is the coarse trust level assigned to this worker.
	TrustTier TrustTier `json:"trust_tier"`

	// Load is the current number of active runs assigned to this worker.
	Load int `json:"load"`

	// Labels are arbitrary key-value pairs for flexible routing (e.g. "gpu": "true").
	Labels map[string]string `json:"labels,omitempty"`

	// SupportedWorkspaceModes lists the workspace types this worker can provision.
	SupportedWorkspaceModes []string `json:"supported_workspace_modes,omitempty"`

	// LastHeartbeat is the timestamp of the most recent heartbeat from this worker.
	LastHeartbeat time.Time `json:"last_heartbeat"`

	// CreatedAt is when this worker was first registered.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when this worker record was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// Heartbeat represents a liveness signal from a worker.
type Heartbeat struct {
	// WorkerID identifies the worker sending the heartbeat.
	WorkerID string `json:"worker_id"`

	// Timestamp is when the heartbeat was received.
	Timestamp time.Time `json:"timestamp"`

	// Load is the current number of active runs on the worker.
	Load int `json:"load"`

	// Status is the worker's self-reported status.
	Status WorkerStatus `json:"status"`
}

// WorkerFilter scopes ListWorkers queries.
// Zero-value fields are ignored (no filtering on that dimension).
type WorkerFilter struct {
	TenantID     string
	Status       WorkerStatus
	LocationType LocationType
	TrustTier    TrustTier
}

// StaleDuration is how long after the last heartbeat a worker is considered stale.
// Workers that don't heartbeat within this window transition to WorkerStatusStale.
const StaleDuration = 30 * time.Second

// Sentinel errors for worker operations.
var (
	// ErrWorkerNotFound is returned when a worker does not exist.
	ErrWorkerNotFound = errors.New("relay: worker not found")

	// ErrWorkerAlreadyExists is returned when registering a worker with a duplicate ID.
	ErrWorkerAlreadyExists = errors.New("relay: worker already exists")

	// ErrInvalidWorkerID is returned when a worker ID is empty or invalid.
	ErrInvalidWorkerID = errors.New("relay: worker ID must not be empty")

	// ErrInvalidLocationType is returned when the location type is not recognized.
	ErrInvalidLocationType = errors.New("relay: invalid location type")

	// ErrInvalidTrustTier is returned when the trust tier is not recognized.
	ErrInvalidTrustTier = errors.New("relay: invalid trust tier")
)

// ValidateWorkerID checks that a worker ID is non-empty and well-formed.
func ValidateWorkerID(id string) error {
	if id == "" {
		return ErrInvalidWorkerID
	}
	return nil
}

// ValidateLocationType checks that a location type is recognized.
func ValidateLocationType(lt LocationType) error {
	if !ValidLocationTypes[lt] {
		return ErrInvalidLocationType
	}
	return nil
}

// ValidateTrustTier checks that a trust tier is recognized.
func ValidateTrustTier(tt TrustTier) error {
	if !ValidTrustTiers[tt] {
		return ErrInvalidTrustTier
	}
	return nil
}
