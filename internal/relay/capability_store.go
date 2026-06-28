package relay

import "context"

// CapabilityStore is the persistence interface for worker capability inventory
// and run capability packs.
//
// All methods accept a context and return an error.
// Thread-safety: implementations must be safe for concurrent use.
type CapabilityStore interface {
	// SetInventory stores or replaces the capability inventory for a worker.
	SetInventory(ctx context.Context, inv *CapabilityInventory) error

	// GetInventory retrieves the capability inventory for a worker.
	// Returns ErrCapabilityNotFound if no inventory exists for this worker.
	GetInventory(ctx context.Context, workerID string) (*CapabilityInventory, error)

	// DeleteInventory removes the capability inventory for a worker.
	DeleteInventory(ctx context.Context, workerID string) error

	// SetPack stores or replaces the capability pack for a run.
	SetPack(ctx context.Context, pack *CapabilityPack) error

	// GetPack retrieves the capability pack for a run.
	// Returns ErrCapabilityNotFound if no pack exists for this run.
	GetPack(ctx context.Context, runID string) (*CapabilityPack, error)

	// DeletePack removes the capability pack for a run.
	DeletePack(ctx context.Context, runID string) error

	// Close releases any resources held by the store.
	Close() error
}
