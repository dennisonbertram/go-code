package goals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// goalsSchema defines the goals table. Slice/map fields are stored as JSON TEXT
// and timestamps as RFC3339Nano strings. completed_at is nullable (NULL when the
// goal has no completion time).
const goalsSchema = `
CREATE TABLE IF NOT EXISTS goals (
	id                 TEXT PRIMARY KEY,
	name               TEXT NOT NULL DEFAULT '',
	description        TEXT NOT NULL DEFAULT '',
	status             TEXT NOT NULL DEFAULT '',
	progress_total     INTEGER NOT NULL DEFAULT 0,
	progress_completed INTEGER NOT NULL DEFAULT 0,
	progress_percent   INTEGER NOT NULL DEFAULT 0,
	depends_on         TEXT NOT NULL DEFAULT '[]',
	blocks             TEXT NOT NULL DEFAULT '[]',
	verify_criteria    TEXT NOT NULL DEFAULT '',
	metadata           TEXT NOT NULL DEFAULT '{}',
	result             TEXT NOT NULL DEFAULT '',
	error              TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL,
	updated_at         TEXT NOT NULL,
	completed_at       TEXT
);

CREATE INDEX IF NOT EXISTS idx_goals_status  ON goals(status);
CREATE INDEX IF NOT EXISTS idx_goals_created ON goals(created_at);
`

// SQLiteStore is a durable, SQLite-backed implementation of the Store interface.
// It is safe for concurrent use.
type SQLiteStore struct {
	mu sync.Mutex
	db *sql.DB
}

// compile-time assertion that *SQLiteStore satisfies the Store interface.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens (or creates) a SQLite database at path and applies the
// goals schema. The returned store is ready to use immediately.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("goals store: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("goals store: create directory: %w", err)
	}
	dsn := path + "?_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("goals store: open: %w", err)
	}
	// Single writer to avoid SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("goals store: set WAL: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("goals store: busy timeout: %w", err)
	}
	if _, err := db.Exec(goalsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("goals store: migrate: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close releases the database connection. It is safe to call on a nil store.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Create inserts a new goal. The caller is expected to have set the ID and
// timestamps (the Manager does this before calling the store).
func (s *SQLiteStore) Create(ctx context.Context, goal *Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dependsOn, err := json.Marshal(goal.DependsOn)
	if err != nil {
		return fmt.Errorf("goals store: marshal depends_on: %w", err)
	}
	blocks, err := json.Marshal(goal.Blocks)
	if err != nil {
		return fmt.Errorf("goals store: marshal blocks: %w", err)
	}
	metadata, err := json.Marshal(goal.Metadata)
	if err != nil {
		return fmt.Errorf("goals store: marshal metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO goals
	(id, name, description, status,
	 progress_total, progress_completed, progress_percent,
	 depends_on, blocks, verify_criteria, metadata,
	 result, error, created_at, updated_at, completed_at)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		goal.ID,
		goal.Name,
		goal.Description,
		string(goal.Status),
		goal.Progress.Total,
		goal.Progress.Completed,
		goal.Progress.Percent,
		string(dependsOn),
		string(blocks),
		goal.VerifyCriteria,
		string(metadata),
		goal.Result,
		goal.Error,
		timeString(goal.CreatedAt),
		timeString(goal.UpdatedAt),
		nullableTime(goal.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("goals store: create goal: %w", err)
	}
	return nil
}

// Get returns the goal with the given id, or a "not found" error.
func (s *SQLiteStore) Get(ctx context.Context, id string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)
	goal, err := scanGoal(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("goal %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("goals store: get goal: %w", err)
	}
	return goal, nil
}

// Update replaces an existing goal. It returns a "not found" error if no goal
// with the given ID exists.
func (s *SQLiteStore) Update(ctx context.Context, goal *Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dependsOn, err := json.Marshal(goal.DependsOn)
	if err != nil {
		return fmt.Errorf("goals store: marshal depends_on: %w", err)
	}
	blocks, err := json.Marshal(goal.Blocks)
	if err != nil {
		return fmt.Errorf("goals store: marshal blocks: %w", err)
	}
	metadata, err := json.Marshal(goal.Metadata)
	if err != nil {
		return fmt.Errorf("goals store: marshal metadata: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE goals SET
	name = ?, description = ?, status = ?,
	progress_total = ?, progress_completed = ?, progress_percent = ?,
	depends_on = ?, blocks = ?, verify_criteria = ?, metadata = ?,
	result = ?, error = ?, created_at = ?, updated_at = ?, completed_at = ?
WHERE id = ?
`,
		goal.Name,
		goal.Description,
		string(goal.Status),
		goal.Progress.Total,
		goal.Progress.Completed,
		goal.Progress.Percent,
		string(dependsOn),
		string(blocks),
		goal.VerifyCriteria,
		string(metadata),
		goal.Result,
		goal.Error,
		timeString(goal.CreatedAt),
		timeString(goal.UpdatedAt),
		nullableTime(goal.CompletedAt),
		goal.ID,
	)
	if err != nil {
		return fmt.Errorf("goals store: update goal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("goals store: update goal rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("goal %q not found", goal.ID)
	}
	return nil
}

// Delete removes the goal with the given id, or returns a "not found" error.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("goals store: delete goal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("goals store: delete goal rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("goal %q not found", id)
	}
	return nil
}

// List returns goals matching the filter. It filters by Status (empty = all),
// sorts by SortBy ("created_at" default, "updated_at", "name"), reverses when
// SortDesc is set, then applies Offset and Limit (Limit==0 = unlimited).
// Sorting and pagination are performed in Go so that variable-width
// RFC3339Nano timestamps compare chronologically rather than lexically.
func (s *SQLiteStore) List(ctx context.Context, filter GoalFilter) ([]Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := selectColumns
	var args []any
	if filter.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(filter.Status))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("goals store: list goals: %w", err)
	}
	defer rows.Close()

	var goals []Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, fmt.Errorf("goals store: scan goal: %w", err)
		}
		goals = append(goals, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("goals store: list goals rows: %w", err)
	}

	sortGoals(goals, filter.SortBy, filter.SortDesc)

	// Apply offset then limit.
	if filter.Offset > 0 {
		if filter.Offset < len(goals) {
			goals = goals[filter.Offset:]
		} else {
			goals = nil
		}
	}
	if filter.Limit > 0 && filter.Limit < len(goals) {
		goals = goals[:filter.Limit]
	}
	return goals, nil
}

// selectColumns is the shared SELECT prefix for Get and List.
const selectColumns = `
SELECT id, name, description, status,
       progress_total, progress_completed, progress_percent,
       depends_on, blocks, verify_criteria, metadata,
       result, error, created_at, updated_at, completed_at
FROM goals`

// rowScanner abstracts *sql.Row and *sql.Rows so scanGoal works with both.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanGoal reads one goal row into a *Goal.
func scanGoal(sc rowScanner) (*Goal, error) {
	var g Goal
	var status string
	var dependsOnJSON, blocksJSON, metadataJSON string
	var createdText, updatedText string
	var completedText sql.NullString

	if err := sc.Scan(
		&g.ID,
		&g.Name,
		&g.Description,
		&status,
		&g.Progress.Total,
		&g.Progress.Completed,
		&g.Progress.Percent,
		&dependsOnJSON,
		&blocksJSON,
		&g.VerifyCriteria,
		&metadataJSON,
		&g.Result,
		&g.Error,
		&createdText,
		&updatedText,
		&completedText,
	); err != nil {
		return nil, err
	}

	g.Status = Status(status)

	if dependsOnJSON != "" && dependsOnJSON != "null" {
		if err := json.Unmarshal([]byte(dependsOnJSON), &g.DependsOn); err != nil {
			return nil, fmt.Errorf("unmarshal depends_on: %w", err)
		}
	}
	if blocksJSON != "" && blocksJSON != "null" {
		if err := json.Unmarshal([]byte(blocksJSON), &g.Blocks); err != nil {
			return nil, fmt.Errorf("unmarshal blocks: %w", err)
		}
	}
	if metadataJSON != "" && metadataJSON != "null" {
		if err := json.Unmarshal([]byte(metadataJSON), &g.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	if t, err := time.Parse(time.RFC3339Nano, createdText); err == nil {
		g.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedText); err == nil {
		g.UpdatedAt = t
	}
	if completedText.Valid && completedText.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, completedText.String); err == nil {
			g.CompletedAt = &t
		}
	}

	return &g, nil
}

// sortGoals sorts goals in place per the given field and direction.
func sortGoals(goals []Goal, sortBy string, desc bool) {
	less := func(i, j int) bool {
		switch sortBy {
		case "name":
			return goals[i].Name < goals[j].Name
		case "updated_at":
			return goals[i].UpdatedAt.Before(goals[j].UpdatedAt)
		default: // "created_at" and empty
			return goals[i].CreatedAt.Before(goals[j].CreatedAt)
		}
	}
	sort.SliceStable(goals, func(i, j int) bool {
		if desc {
			return less(j, i)
		}
		return less(i, j)
	})
}

// timeString formats a time as an RFC3339Nano UTC string for storage.
func timeString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// nullableTime returns a sql-storable value for a *time.Time: NULL when nil,
// otherwise an RFC3339Nano string.
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeString(*t)
}
