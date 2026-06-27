package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// capabilitySchema defines the SQLite tables for capability persistence.
const capabilitySchema = `
CREATE TABLE IF NOT EXISTS relay_capability_inventory (
    worker_id   TEXT PRIMARY KEY,
    payload     TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_capability_packs (
    run_id      TEXT PRIMARY KEY,
    payload     TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
`

// SQLiteCapabilityStore is a SQLite-backed implementation of CapabilityStore
// that shares a database connection with other relay stores.
type SQLiteCapabilityStore struct {
	db *sql.DB
}

// NewSQLiteCapabilityStore creates a capability store using an existing database.
func NewSQLiteCapabilityStore(db *sql.DB) (*SQLiteCapabilityStore, error) {
	if db == nil {
		return nil, fmt.Errorf("relay: database is required")
	}
	return &SQLiteCapabilityStore{db: db}, nil
}

// Migrate creates the capability schema tables.
func (s *SQLiteCapabilityStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, capabilitySchema); err != nil {
		return fmt.Errorf("relay: migrate capabilities: %w", err)
	}
	return nil
}

// Close is a no-op since the database is shared.
func (s *SQLiteCapabilityStore) Close() error { return nil }

// SetInventory stores or replaces the capability inventory for a worker.
func (s *SQLiteCapabilityStore) SetInventory(ctx context.Context, inv *CapabilityInventory) error {
	if inv.WorkerID == "" {
		return fmt.Errorf("relay: worker ID is required for capability inventory")
	}

	payload, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("relay: marshal capability inventory: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO relay_capability_inventory (worker_id, payload, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(worker_id) DO UPDATE SET payload = ?, updated_at = ?
`,
		inv.WorkerID, string(payload), inv.UpdatedAt,
		string(payload), inv.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("relay: set capability inventory: %w", err)
	}
	return nil
}

// GetInventory retrieves the capability inventory for a worker.
func (s *SQLiteCapabilityStore) GetInventory(ctx context.Context, workerID string) (*CapabilityInventory, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT payload FROM relay_capability_inventory WHERE worker_id = ?
`, workerID)

	var payloadStr string
	if err := row.Scan(&payloadStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCapabilityNotFound
		}
		return nil, fmt.Errorf("relay: get capability inventory: %w", err)
	}

	inv := &CapabilityInventory{}
	if err := json.Unmarshal([]byte(payloadStr), inv); err != nil {
		return nil, fmt.Errorf("relay: unmarshal capability inventory: %w", err)
	}
	return inv, nil
}

// DeleteInventory removes the capability inventory for a worker.
func (s *SQLiteCapabilityStore) DeleteInventory(ctx context.Context, workerID string) error {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM relay_capability_inventory WHERE worker_id = ?
`, workerID)
	if err != nil {
		return fmt.Errorf("relay: delete capability inventory: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relay: delete inventory rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCapabilityNotFound
	}
	return nil
}

// SetPack stores or replaces the capability pack for a run.
func (s *SQLiteCapabilityStore) SetPack(ctx context.Context, pack *CapabilityPack) error {
	if pack.RunID == "" {
		return fmt.Errorf("relay: run ID is required for capability pack")
	}

	payload, err := json.Marshal(pack)
	if err != nil {
		return fmt.Errorf("relay: marshal capability pack: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO relay_capability_packs (run_id, payload, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET payload = ?, updated_at = ?
`,
		pack.RunID, string(payload), timeString(time.Now()),
		string(payload), timeString(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("relay: set capability pack: %w", err)
	}
	return nil
}

// GetPack retrieves the capability pack for a run.
func (s *SQLiteCapabilityStore) GetPack(ctx context.Context, runID string) (*CapabilityPack, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT payload FROM relay_capability_packs WHERE run_id = ?
`, runID)

	var payloadStr string
	if err := row.Scan(&payloadStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCapabilityNotFound
		}
		return nil, fmt.Errorf("relay: get capability pack: %w", err)
	}

	pack := &CapabilityPack{}
	if err := json.Unmarshal([]byte(payloadStr), pack); err != nil {
		return nil, fmt.Errorf("relay: unmarshal capability pack: %w", err)
	}
	return pack, nil
}

// DeletePack removes the capability pack for a run.
func (s *SQLiteCapabilityStore) DeletePack(ctx context.Context, runID string) error {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM relay_capability_packs WHERE run_id = ?
`, runID)
	if err != nil {
		return fmt.Errorf("relay: delete capability pack: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relay: delete pack rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCapabilityNotFound
	}
	return nil
}
