package relay

import (
	"context"
	"fmt"
)

// ControlPlane bundles the self-contained relay control-plane components that
// operate over the shared relay database and in-memory logic: the capability
// and event/artifact stores plus the placement router, contract composer,
// capability policy, and operator visibility surface.
//
// These components function offline — they require no worker agent or wire
// transport. The transport/command/cloud-provisioning pieces of internal/relay
// are intentionally NOT bundled here: they presuppose a remote worker runtime
// that this repository does not yet provide, so wiring them would expose a
// non-functional API.
//
// The worker store is managed and shared in separately (see WorkerStore), since
// it owns the database handle the other stores attach to.
type ControlPlane struct {
	Capabilities CapabilityStore
	Events       EventAndArtifactStore
	Router       *PlacementRouter
	Composer     *Composer
	Policy       *CapabilityPolicy
	Operator     *OperatorUX
}

// NewControlPlane builds the control plane over the same database as the given
// SQLite worker store, migrating the capability and event/artifact schemas. All
// three stores share the worker store's single-connection WAL database (the
// intended pattern), so only the worker store owns and closes the handle.
func NewControlPlane(ctx context.Context, workers *SQLiteWorkerStore) (*ControlPlane, error) {
	if workers == nil {
		return nil, fmt.Errorf("relay control plane: worker store is required")
	}

	caps, err := NewSQLiteCapabilityStore(workers.DB())
	if err != nil {
		return nil, fmt.Errorf("relay capability store: %w", err)
	}
	if err := caps.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate relay capability store: %w", err)
	}

	events, err := NewSQLiteEventArtifactStore(workers.DB())
	if err != nil {
		return nil, fmt.Errorf("relay event/artifact store: %w", err)
	}
	if err := events.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate relay event/artifact store: %w", err)
	}

	return &ControlPlane{
		Capabilities: caps,
		Events:       events,
		Router:       NewPlacementRouter(workers, caps),
		Composer:     NewComposer(),
		Policy:       NewCapabilityPolicy(),
		Operator:     NewOperatorUX(workers, caps, nil, events),
	}, nil
}
