package store

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

// schema defines the SQLite tables for run persistence.
const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id               TEXT PRIMARY KEY,
	conversation_id  TEXT NOT NULL DEFAULT '',
	tenant_id        TEXT NOT NULL DEFAULT '',
	agent_id         TEXT NOT NULL DEFAULT '',
	model            TEXT NOT NULL DEFAULT '',
	provider_name    TEXT NOT NULL DEFAULT '',
	prompt           TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL,
	output           TEXT NOT NULL DEFAULT '',
	error            TEXT NOT NULL DEFAULT '',
	usage_totals_json TEXT NOT NULL DEFAULT '',
	cost_totals_json  TEXT NOT NULL DEFAULT '',
	recap_json        TEXT NOT NULL DEFAULT '',
	created_at       TEXT NOT NULL,
	updated_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_conversation ON runs(conversation_id);
CREATE INDEX IF NOT EXISTS idx_runs_tenant      ON runs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_runs_status      ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_created     ON runs(created_at);

CREATE TABLE IF NOT EXISTS run_messages (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id          TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	seq             INTEGER NOT NULL,
	role            TEXT NOT NULL,
	content         TEXT NOT NULL DEFAULT '',
	tool_calls_json TEXT,
	tool_call_id    TEXT NOT NULL DEFAULT '',
	name            TEXT NOT NULL DEFAULT '',
	is_meta         INTEGER NOT NULL DEFAULT 0,
	is_compact_summary INTEGER NOT NULL DEFAULT 0,
	UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_run_messages_run ON run_messages(run_id, seq);

CREATE TABLE IF NOT EXISTS run_events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	event_id   TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload    TEXT NOT NULL DEFAULT '',
	timestamp  TEXT NOT NULL,
	UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id, seq);
`

// SQLiteStore is a SQLite-backed implementation of Store.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite database at path.
// Call Migrate before using the store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("store: sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create sqlite directory: %w", err)
	}
	dsn := path + "?_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	// Limit to a single writer connection to avoid SQLITE_BUSY under concurrent writes.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: set busy timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: set foreign keys: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Migrate creates the schema tables.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	if !s.columnExists(ctx, "runs", "recap_json") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE runs ADD COLUMN recap_json TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("store: migrate add recap_json: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, table, column string) bool {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateRun persists a new run record.
func (s *SQLiteStore) CreateRun(ctx context.Context, run *Run) error {
	if run.ID == "" {
		return fmt.Errorf("store: run ID is required")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO runs (id, conversation_id, tenant_id, agent_id, model, provider_name, prompt,
                  status, output, error, usage_totals_json, cost_totals_json, recap_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		run.ID,
		run.ConversationID,
		run.TenantID,
		run.AgentID,
		run.Model,
		run.ProviderName,
		run.Prompt,
		string(run.Status),
		run.Output,
		run.Error,
		run.UsageTotalsJSON,
		run.CostTotalsJSON,
		workflowRecapJSON(run.Recap),
		timeString(run.CreatedAt),
		timeString(run.UpdatedAt),
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("store: run %q already exists", run.ID)
		}
		return fmt.Errorf("store: create run: %w", err)
	}
	return nil
}

// UpdateRun overwrites an existing run record.
func (s *SQLiteStore) UpdateRun(ctx context.Context, run *Run) error {
	if run.ID == "" {
		return fmt.Errorf("store: run ID is required")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE runs
SET conversation_id  = ?,
    tenant_id        = ?,
    agent_id         = ?,
    model            = ?,
    provider_name    = ?,
    prompt           = ?,
    status           = ?,
    output           = ?,
    error            = ?,
    usage_totals_json = ?,
    cost_totals_json  = ?,
    recap_json       = ?,
    updated_at       = ?
WHERE id = ?
`,
		run.ConversationID,
		run.TenantID,
		run.AgentID,
		run.Model,
		run.ProviderName,
		run.Prompt,
		string(run.Status),
		run.Output,
		run.Error,
		run.UsageTotalsJSON,
		run.CostTotalsJSON,
		workflowRecapJSON(run.Recap),
		timeString(run.UpdatedAt),
		run.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update run: %w", err)
	}
	return nil
}

// GetRun retrieves a run by ID.
func (s *SQLiteStore) GetRun(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, conversation_id, tenant_id, agent_id, model, provider_name, prompt,
       status, output, error, usage_totals_json, cost_totals_json, recap_json, created_at, updated_at
FROM runs
WHERE id = ?
`, id)
	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("store: get run %q: %w", id, err)
	}
	return run, nil
}

// ListRuns returns runs matching filter, ordered by created_at DESC.
func (s *SQLiteStore) ListRuns(ctx context.Context, filter RunFilter) ([]*Run, error) {
	query := `SELECT id, conversation_id, tenant_id, agent_id, model, provider_name, prompt,
	                  status, output, error, usage_totals_json, cost_totals_json, recap_json, created_at, updated_at
	           FROM runs`
	args := make([]any, 0, 3)
	conditions := make([]string, 0, 3)

	if filter.ConversationID != "" {
		conditions = append(conditions, "conversation_id = ?")
		args = append(args, filter.ConversationID)
	}
	if filter.TenantID != "" {
		conditions = append(conditions, "tenant_id = ?")
		args = append(args, filter.TenantID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(filter.Status))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list runs rows: %w", err)
	}
	if runs == nil {
		runs = []*Run{}
	}
	return runs, nil
}

// AppendMessage appends a message to a run's message log.
func (s *SQLiteStore) AppendMessage(ctx context.Context, msg *Message) error {
	isMeta := 0
	if msg.IsMeta {
		isMeta = 1
	}
	isCompact := 0
	if msg.IsCompactSummary {
		isCompact = 1
	}
	var toolCallsJSON *string
	if msg.ToolCallsJSON != "" {
		toolCallsJSON = &msg.ToolCallsJSON
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_messages (run_id, seq, role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		msg.RunID, msg.Seq, msg.Role, msg.Content,
		toolCallsJSON, msg.ToolCallID, msg.Name,
		isMeta, isCompact,
	)
	if err != nil {
		return fmt.Errorf("store: append message: %w", err)
	}
	return nil
}

// GetMessages returns all messages for a run, ordered by seq ASC.
func (s *SQLiteStore) GetMessages(ctx context.Context, runID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, run_id, role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary
FROM run_messages
WHERE run_id = ?
ORDER BY seq ASC
`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: get messages: %w", err)
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		msg := &Message{}
		var toolCallsJSON sql.NullString
		var isMeta, isCompact int
		if err := rows.Scan(
			&msg.Seq, &msg.RunID, &msg.Role, &msg.Content,
			&toolCallsJSON, &msg.ToolCallID, &msg.Name,
			&isMeta, &isCompact,
		); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		msg.IsMeta = isMeta == 1
		msg.IsCompactSummary = isCompact == 1
		if toolCallsJSON.Valid {
			msg.ToolCallsJSON = toolCallsJSON.String
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: messages rows: %w", err)
	}
	if msgs == nil {
		msgs = []*Message{}
	}
	return msgs, nil
}

// AppendEvent appends an event to a run's event log.
func (s *SQLiteStore) AppendEvent(ctx context.Context, event *Event) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_events (run_id, seq, event_id, event_type, payload, timestamp)
VALUES (?, ?, ?, ?, ?, ?)
`,
		event.RunID, event.Seq, event.EventID, event.EventType, event.Payload,
		timeString(event.Timestamp),
	)
	if err != nil {
		return fmt.Errorf("store: append event: %w", err)
	}
	return nil
}

// GetEvents returns events for a run with seq > afterSeq, ordered by seq ASC.
// Pass afterSeq=-1 to get all events.
func (s *SQLiteStore) GetEvents(ctx context.Context, runID string, afterSeq int) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, run_id, event_id, event_type, payload, timestamp
FROM run_events
WHERE run_id = ? AND seq > ?
ORDER BY seq ASC
`, runID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("store: get events: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e := &Event{}
		var tsText string
		if err := rows.Scan(&e.Seq, &e.RunID, &e.EventID, &e.EventType, &e.Payload, &tsText); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, tsText); err == nil {
			e.Timestamp = t
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: events rows: %w", err)
	}
	if events == nil {
		events = []*Event{}
	}
	return events, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for shared scanning.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (*Run, error) {
	run := &Run{}
	var createdText, updatedText, recapJSON string
	err := row.Scan(
		&run.ID,
		&run.ConversationID,
		&run.TenantID,
		&run.AgentID,
		&run.Model,
		&run.ProviderName,
		&run.Prompt,
		&run.Status,
		&run.Output,
		&run.Error,
		&run.UsageTotalsJSON,
		&run.CostTotalsJSON,
		&recapJSON,
		&createdText,
		&updatedText,
	)
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, createdText); err == nil {
		run.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedText); err == nil {
		run.UpdatedAt = t
	}
	run.Recap = workflowRecapFromJSON(recapJSON)
	return run, nil
}

func workflowRecapJSON(recap *WorkflowRecap) string {
	if recap == nil {
		return ""
	}
	data, err := json.Marshal(recap)
	if err != nil {
		return ""
	}
	return string(data)
}

func workflowRecapFromJSON(raw string) *WorkflowRecap {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var recap WorkflowRecap
	if err := json.Unmarshal([]byte(raw), &recap); err != nil {
		return nil
	}
	return &recap
}

func timeString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// isDuplicateKeyError returns true if err is a SQLite UNIQUE constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "constraint failed")
}
