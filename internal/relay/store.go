package relay

import "context"

// WorkerStore is the persistence interface for worker registration and heartbeats.
//
// All methods accept a context and return an error; callers should treat
// context.DeadlineExceeded / context.Canceled appropriately.
//
// Thread-safety: implementations must be safe for concurrent use.
type WorkerStore interface {
	// RegisterWorker persists a new worker record. Returns ErrWorkerAlreadyExists
	// if a worker with the same ID is already registered.
	RegisterWorker(ctx context.Context, w *Worker) error

	// UpdateWorker overwrites an existing worker record. Returns ErrWorkerNotFound
	// if the worker does not exist.
	UpdateWorker(ctx context.Context, w *Worker) error

	// GetWorker retrieves a worker by ID. Returns ErrWorkerNotFound if not found.
	GetWorker(ctx context.Context, id string) (*Worker, error)

	// ListWorkers returns workers matching the filter, ordered by created_at DESC.
	// An empty filter returns all workers. Automatically filters out stale workers
	// (Status != WorkerStatusStale) unless the filter explicitly requests them.
	ListWorkers(ctx context.Context, filter WorkerFilter) ([]*Worker, error)

	// DeleteWorker removes a worker record. Returns ErrWorkerNotFound if not found.
	DeleteWorker(ctx context.Context, id string) error

	// RecordHeartbeat records a heartbeat for a worker and updates its status
	// and last_heartbeat timestamp. Returns ErrWorkerNotFound if the worker
	// does not exist.
	RecordHeartbeat(ctx context.Context, hb Heartbeat) error

	// MarkStaleWorkers transitions workers whose last heartbeat is older than
	// StaleDuration to WorkerStatusStale, unless they are already stale or offline.
	// Returns the count of workers marked stale.
	MarkStaleWorkers(ctx context.Context) (int, error)

	// Close releases any resources held by the store.
	Close() error
}
