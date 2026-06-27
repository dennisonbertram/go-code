package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// workerSchema defines the SQLite tables for worker persistence.
const workerSchema = `
CREATE TABLE IF NOT EXISTS relay_workers (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL DEFAULT '',
    name                     TEXT NOT NULL DEFAULT '',
    location_type            TEXT NOT NULL DEFAULT 'local',
    status                   TEXT NOT NULL DEFAULT 'online',
    trust_tier               TEXT NOT NULL DEFAULT 'standard',
    load                     INTEGER NOT NULL DEFAULT 0,
    labels_json              TEXT NOT NULL DEFAULT '{}',
    supported_workspace_modes_json TEXT NOT NULL DEFAULT '[]',
    last_heartbeat           TEXT NOT NULL,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_relay_workers_tenant ON relay_workers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_relay_workers_status ON relay_workers(status);
CREATE INDEX IF NOT EXISTS idx_relay_workers_location ON relay_workers(location_type);
CREATE INDEX IF NOT EXISTS idx_relay_workers_heartbeat ON relay_workers(last_heartbeat);
`

// SQLiteWorkerStore is a SQLite-backed implementation of WorkerStore.
type SQLiteWorkerStore struct {
	db *sql.DB
}

// NewSQLiteWorkerStore opens (or creates) a SQLite database at path.
// Call Migrate before using the store.
func NewSQLiteWorkerStore(path string) (*SQLiteWorkerStore, error) {
	if path == "" {
		return nil, fmt.Errorf("relay: sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("relay: create sqlite directory: %w", err)
	}
	dsn := path + "?_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("relay: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("relay: set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("relay: set busy timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("relay: set foreign keys: %w", err)
	}
	return &SQLiteWorkerStore{db: db}, nil
}

// Migrate creates the schema tables.
func (s *SQLiteWorkerStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, workerSchema); err != nil {
		return fmt.Errorf("relay: migrate: %w", err)
	}
	return nil
}

// Close releases the database connection.
func (s *SQLiteWorkerStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying database for use by other relay stores
// that share the same database.
func (s *SQLiteWorkerStore) DB() *sql.DB {
	return s.db
}

// RegisterWorker persists a new worker record.
func (s *SQLiteWorkerStore) RegisterWorker(ctx context.Context, w *Worker) error {
	if err := ValidateWorkerID(w.ID); err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(w.Labels)
	if err != nil {
		return fmt.Errorf("relay: marshal labels: %w", err)
	}
	modesJSON, err := json.Marshal(w.SupportedWorkspaceModes)
	if err != nil {
		return fmt.Errorf("relay: marshal workspace modes: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO relay_workers (id, tenant_id, name, location_type, status, trust_tier, load,
                           labels_json, supported_workspace_modes_json, last_heartbeat, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		w.ID,
		w.TenantID,
		w.Name,
		string(w.LocationType),
		string(w.Status),
		string(w.TrustTier),
		w.Load,
		string(labelsJSON),
		string(modesJSON),
		timeString(w.LastHeartbeat),
		timeString(w.CreatedAt),
		timeString(w.UpdatedAt),
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrWorkerAlreadyExists
		}
		return fmt.Errorf("relay: register worker: %w", err)
	}
	return nil
}

// UpdateWorker overwrites an existing worker record.
func (s *SQLiteWorkerStore) UpdateWorker(ctx context.Context, w *Worker) error {
	if err := ValidateWorkerID(w.ID); err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(w.Labels)
	if err != nil {
		return fmt.Errorf("relay: marshal labels: %w", err)
	}
	modesJSON, err := json.Marshal(w.SupportedWorkspaceModes)
	if err != nil {
		return fmt.Errorf("relay: marshal workspace modes: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE relay_workers
SET tenant_id      = ?,
    name           = ?,
    location_type  = ?,
    status         = ?,
    trust_tier     = ?,
    load           = ?,
    labels_json    = ?,
    supported_workspace_modes_json = ?,
    last_heartbeat = ?,
    updated_at     = ?
WHERE id = ?
`,
		w.TenantID,
		w.Name,
		string(w.LocationType),
		string(w.Status),
		string(w.TrustTier),
		w.Load,
		string(labelsJSON),
		string(modesJSON),
		timeString(w.LastHeartbeat),
		timeString(w.UpdatedAt),
		w.ID,
	)
	if err != nil {
		return fmt.Errorf("relay: update worker: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relay: update worker rows affected: %w", err)
	}
	if rows == 0 {
		return ErrWorkerNotFound
	}
	return nil
}

// GetWorker retrieves a worker by ID.
func (s *SQLiteWorkerStore) GetWorker(ctx context.Context, id string) (*Worker, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, tenant_id, name, location_type, status, trust_tier, load,
       labels_json, supported_workspace_modes_json, last_heartbeat, created_at, updated_at
FROM relay_workers
WHERE id = ?
`, id)
	w, err := scanWorker(row)
	if err == sql.ErrNoRows {
		return nil, ErrWorkerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("relay: get worker %q: %w", id, err)
	}
	return w, nil
}

// ListWorkers returns workers matching the filter, ordered by created_at DESC.
func (s *SQLiteWorkerStore) ListWorkers(ctx context.Context, filter WorkerFilter) ([]*Worker, error) {
	query := `SELECT id, tenant_id, name, location_type, status, trust_tier, load,
	                  labels_json, supported_workspace_modes_json, last_heartbeat, created_at, updated_at
	           FROM relay_workers`
	args := make([]any, 0, 4)
	conditions := make([]string, 0, 4)

	if filter.TenantID != "" {
		conditions = append(conditions, "tenant_id = ?")
		args = append(args, filter.TenantID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(filter.Status))
	} else {
		// By default, exclude stale workers unless explicitly requested.
		conditions = append(conditions, "status != ?")
		args = append(args, string(WorkerStatusStale))
	}
	if filter.LocationType != "" {
		conditions = append(conditions, "location_type = ?")
		args = append(args, string(filter.LocationType))
	}
	if filter.TrustTier != "" {
		conditions = append(conditions, "trust_tier = ?")
		args = append(args, string(filter.TrustTier))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("relay: list workers: %w", err)
	}
	defer rows.Close()

	var workers []*Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("relay: scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("relay: list workers rows: %w", err)
	}
	if workers == nil {
		workers = []*Worker{}
	}
	return workers, nil
}

// DeleteWorker removes a worker record.
func (s *SQLiteWorkerStore) DeleteWorker(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM relay_workers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("relay: delete worker: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relay: delete worker rows affected: %w", err)
	}
	if rows == 0 {
		return ErrWorkerNotFound
	}
	return nil
}

// RecordHeartbeat records a heartbeat for a worker.
func (s *SQLiteWorkerStore) RecordHeartbeat(ctx context.Context, hb Heartbeat) error {
	// Update the worker's last_heartbeat, load, and status.
	result, err := s.db.ExecContext(ctx, `
UPDATE relay_workers
SET last_heartbeat = ?,
    load           = ?,
    status         = ?,
    updated_at     = ?
WHERE id = ?
`,
		timeString(hb.Timestamp),
		hb.Load,
		string(hb.Status),
		timeString(time.Now()),
		hb.WorkerID,
	)
	if err != nil {
		return fmt.Errorf("relay: record heartbeat: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relay: record heartbeat rows affected: %w", err)
	}
	if rows == 0 {
		return ErrWorkerNotFound
	}
	return nil
}

// MarkStaleWorkers transitions workers whose last heartbeat is older than
// StaleDuration to WorkerStatusStale.
func (s *SQLiteWorkerStore) MarkStaleWorkers(ctx context.Context) (int, error) {
	staleBefore := time.Now().Add(-StaleDuration)
	result, err := s.db.ExecContext(ctx, `
UPDATE relay_workers
SET status     = ?,
    updated_at = ?
WHERE status IN (?, ?)
  AND last_heartbeat < ?
`,
		string(WorkerStatusStale),
		timeString(time.Now()),
		string(WorkerStatusOnline),
		string(WorkerStatusDraining),
		timeString(staleBefore),
	)
	if err != nil {
		return 0, fmt.Errorf("relay: mark stale workers: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("relay: mark stale rows affected: %w", err)
	}
	return int(rows), nil
}

// workerScanner abstracts *sql.Row and *sql.Rows for shared scanning.
type workerScanner interface {
	Scan(dest ...any) error
}

func scanWorker(row workerScanner) (*Worker, error) {
	w := &Worker{}
	var labelsJSON, modesJSON string
	var lastHBText, createdText, updatedText string
	err := row.Scan(
		&w.ID,
		&w.TenantID,
		&w.Name,
		&w.LocationType,
		&w.Status,
		&w.TrustTier,
		&w.Load,
		&labelsJSON,
		&modesJSON,
		&lastHBText,
		&createdText,
		&updatedText,
	)
	if err != nil {
		return nil, err
	}

	if labelsJSON != "" {
		if err := json.Unmarshal([]byte(labelsJSON), &w.Labels); err != nil {
			w.Labels = make(map[string]string)
		}
	}
	if w.Labels == nil {
		w.Labels = make(map[string]string)
	}

	if modesJSON != "" {
		if err := json.Unmarshal([]byte(modesJSON), &w.SupportedWorkspaceModes); err != nil {
			w.SupportedWorkspaceModes = []string{}
		}
	}
	if w.SupportedWorkspaceModes == nil {
		w.SupportedWorkspaceModes = []string{}
	}

	if t, err := time.Parse(time.RFC3339Nano, lastHBText); err == nil {
		w.LastHeartbeat = t
	}
	if t, err := time.Parse(time.RFC3339Nano, createdText); err == nil {
		w.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedText); err == nil {
		w.UpdatedAt = t
	}
	return w, nil
}

func timeString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "constraint failed")
}
