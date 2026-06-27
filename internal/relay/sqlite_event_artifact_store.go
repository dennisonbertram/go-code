package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// eventArtifactSchema defines the SQLite tables for events, artifacts, and placements.
const eventArtifactSchema = `
CREATE TABLE IF NOT EXISTS relay_placement_records (
    run_id      TEXT PRIMARY KEY,
    payload     TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_event_records (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    event_id    TEXT NOT NULL DEFAULT '',
    event_type  TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    timestamp   TEXT NOT NULL,
    worker_id   TEXT NOT NULL DEFAULT '',
    UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_relay_events_run ON relay_event_records(run_id, seq);

CREATE TABLE IF NOT EXISTS relay_artifacts (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL,
    type        TEXT NOT NULL,
    worker_id   TEXT NOT NULL DEFAULT '',
    mime_type   TEXT NOT NULL DEFAULT '',
    data        TEXT NOT NULL DEFAULT '',
    ref         TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'tenant',
    redacted    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_relay_artifacts_run ON relay_artifacts(run_id);
`

// SQLiteEventArtifactStore is a SQLite-backed implementation of EventAndArtifactStore.
type SQLiteEventArtifactStore struct {
	db *sql.DB
}

// NewSQLiteEventArtifactStore creates an event/artifact store using an existing database.
func NewSQLiteEventArtifactStore(db *sql.DB) (*SQLiteEventArtifactStore, error) {
	if db == nil {
		return nil, fmt.Errorf("relay: database is required")
	}
	return &SQLiteEventArtifactStore{db: db}, nil
}

// Migrate creates the event/artifact schema tables.
func (s *SQLiteEventArtifactStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, eventArtifactSchema); err != nil {
		return fmt.Errorf("relay: migrate events/artifacts: %w", err)
	}
	return nil
}

// Close is a no-op since the database is shared.
func (s *SQLiteEventArtifactStore) Close() error { return nil }

// SavePlacementRecord persists a placement record.
func (s *SQLiteEventArtifactStore) SavePlacementRecord(ctx context.Context, record *PlacementRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("relay: marshal placement: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO relay_placement_records (run_id, payload, created_at)
VALUES (?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET payload = ?, created_at = ?
`,
		record.RunID, string(payload), timeString(record.Timestamp),
		string(payload), timeString(record.Timestamp),
	)
	return err
}

// GetPlacementRecord retrieves a placement record.
func (s *SQLiteEventArtifactStore) GetPlacementRecord(ctx context.Context, runID string) (*PlacementRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT payload FROM relay_placement_records WHERE run_id = ?
`, runID)

	var payloadStr string
	if err := row.Scan(&payloadStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrArtifactNotFound
		}
		return nil, fmt.Errorf("relay: get placement: %w", err)
	}
	return PlacementRecordFromJSON([]byte(payloadStr))
}

// AppendEvent appends an event to a run's event log.
func (s *SQLiteEventArtifactStore) AppendEvent(ctx context.Context, event *EventRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO relay_event_records (run_id, seq, event_id, event_type, payload, timestamp, worker_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
`,
		event.RunID, event.Seq, event.EventID, event.EventType, event.Payload,
		timeString(event.Timestamp), event.WorkerID,
	)
	if err != nil {
		return fmt.Errorf("relay: append event: %w", err)
	}
	return nil
}

// GetEvents returns events for a run with seq > afterSeq.
func (s *SQLiteEventArtifactStore) GetEvents(ctx context.Context, runID string, afterSeq int) ([]*EventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, run_id, event_id, event_type, payload, timestamp, worker_id
FROM relay_event_records
WHERE run_id = ? AND seq > ?
ORDER BY seq ASC
`, runID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("relay: get events: %w", err)
	}
	defer rows.Close()

	var events []*EventRecord
	for rows.Next() {
		e := &EventRecord{}
		var tsText string
		if err := rows.Scan(&e.Seq, &e.RunID, &e.EventID, &e.EventType, &e.Payload, &tsText, &e.WorkerID); err != nil {
			return nil, fmt.Errorf("relay: scan event: %w", err)
		}
		if t, err := timeParse(tsText); err == nil {
			e.Timestamp = t
		}
		events = append(events, e)
	}
	if events == nil {
		events = []*EventRecord{}
	}
	return events, rows.Err()
}

// SaveArtifact persists an artifact.
func (s *SQLiteEventArtifactStore) SaveArtifact(ctx context.Context, a *Artifact) error {
	redacted := 0
	if a.Redacted {
		redacted = 1
	}
	if a.Visibility == "" {
		a.Visibility = "tenant"
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO relay_artifacts (id, run_id, type, worker_id, mime_type, data, ref, url, visibility, redacted, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET data = ?, ref = ?, url = ?, visibility = ?, redacted = ?
`,
		a.ID, a.RunID, string(a.Type), a.WorkerID, a.MIMEType, a.Data, a.Ref, a.URL, a.Visibility, redacted, timeString(a.CreatedAt),
		a.Data, a.Ref, a.URL, a.Visibility, redacted,
	)
	if err != nil {
		return fmt.Errorf("relay: save artifact: %w", err)
	}
	return nil
}

// GetArtifact retrieves an artifact by ID.
func (s *SQLiteEventArtifactStore) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, run_id, type, worker_id, mime_type, data, ref, url, visibility, redacted, created_at
FROM relay_artifacts WHERE id = ?
`, id)

	a := &Artifact{}
	var redacted int
	var createdText string
	if err := row.Scan(&a.ID, &a.RunID, &a.Type, &a.WorkerID, &a.MIMEType, &a.Data, &a.Ref, &a.URL, &a.Visibility, &redacted, &createdText); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrArtifactNotFound
		}
		return nil, fmt.Errorf("relay: get artifact: %w", err)
	}
	a.Redacted = redacted == 1
	if t, err := timeParse(createdText); err == nil {
		a.CreatedAt = t
	}
	return a, nil
}

// ListArtifacts returns artifacts for a run.
func (s *SQLiteEventArtifactStore) ListArtifacts(ctx context.Context, runID string) ([]*Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, run_id, type, worker_id, mime_type, data, ref, url, visibility, redacted, created_at
FROM relay_artifacts WHERE run_id = ? ORDER BY created_at ASC
`, runID)
	if err != nil {
		return nil, fmt.Errorf("relay: list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []*Artifact
	for rows.Next() {
		a := &Artifact{}
		var redacted int
		var createdText string
		if err := rows.Scan(&a.ID, &a.RunID, &a.Type, &a.WorkerID, &a.MIMEType, &a.Data, &a.Ref, &a.URL, &a.Visibility, &redacted, &createdText); err != nil {
			return nil, fmt.Errorf("relay: scan artifact: %w", err)
		}
		a.Redacted = redacted == 1
		if t, err := timeParse(createdText); err == nil {
			a.CreatedAt = t
		}
		artifacts = append(artifacts, a)
	}
	if artifacts == nil {
		artifacts = []*Artifact{}
	}
	return artifacts, rows.Err()
}

// timeParse is a local helper for parsing time strings.
func timeParse(s string) (time.Time, error) {
	// Use package-level time.Parse
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err
}
