package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const conversationSchema = `
CREATE TABLE IF NOT EXISTS conversations (
    id                TEXT PRIMARY KEY,
    title             TEXT NOT NULL DEFAULT '',
    msg_count         INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL NOT NULL DEFAULT 0.0,
    pinned            INTEGER NOT NULL DEFAULT 0,
    workspace         TEXT NOT NULL DEFAULT '',
    tenant_id         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS conversation_messages (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id     TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    step                INTEGER NOT NULL,
    role                TEXT NOT NULL,
    content             TEXT NOT NULL DEFAULT '',
    tool_calls_json     TEXT,
    tool_call_id        TEXT NOT NULL DEFAULT '',
    name                TEXT NOT NULL DEFAULT '',
    is_meta             INTEGER NOT NULL DEFAULT 0,
    is_compact_summary  INTEGER NOT NULL DEFAULT 0,
    UNIQUE(conversation_id, step)
);

CREATE INDEX IF NOT EXISTS idx_conv_msgs_conv_id ON conversation_messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_conversations_updated ON conversations(updated_at);

CREATE TABLE IF NOT EXISTS rewind_points (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    step            INTEGER NOT NULL,
    tool            TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rewind_file_snapshots (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    point_id        TEXT NOT NULL REFERENCES rewind_points(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    content         BLOB,
    existed         INTEGER NOT NULL,
    skipped         INTEGER NOT NULL DEFAULT 0,
    skip_reason     TEXT NOT NULL DEFAULT '',
    expected_hash   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_rewind_points_conversation ON rewind_points(conversation_id, step DESC);

-- FTS5 virtual table for full-text search on message content.
CREATE VIRTUAL TABLE IF NOT EXISTS conversation_messages_fts
USING fts5(conversation_id UNINDEXED, role UNINDEXED, content, content='conversation_messages', content_rowid='id');
`

// SQLiteConversationStore implements ConversationStore using SQLite.
type SQLiteConversationStore struct {
	db *sql.DB
}

// NewSQLiteConversationStore creates a new SQLite-backed conversation store.
func NewSQLiteConversationStore(path string) (*SQLiteConversationStore, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}
	dsn := path + "?_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Limit to 1 connection to avoid SQLITE_BUSY under concurrent writes.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set sqlite foreign keys: %w", err)
	}
	return &SQLiteConversationStore{db: db}, nil
}

// Close closes the database connection.
func (s *SQLiteConversationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Migrate creates the schema tables and applies incremental migrations.
func (s *SQLiteConversationStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, conversationSchema)
	if err != nil {
		return fmt.Errorf("sqlite conversation migrate: %w", err)
	}

	// Idempotent migration: add is_meta column if it doesn't exist.
	if !s.columnExists(ctx, "conversation_messages", "is_meta") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversation_messages ADD COLUMN is_meta INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate add is_meta column: %w", err)
		}
	}

	// Idempotent migration: add pinned column if it doesn't exist (Issue #34).
	if !s.columnExists(ctx, "conversations", "pinned") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate add pinned column: %w", err)
		}
	}

	// Idempotent migration: add token/cost columns to conversations if they don't exist (Issue #32).
	if !s.columnExists(ctx, "conversations", "prompt_tokens") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate add prompt_tokens column: %w", err)
		}
	}
	if !s.columnExists(ctx, "conversations", "completion_tokens") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate add completion_tokens column: %w", err)
		}
	}
	if !s.columnExists(ctx, "conversations", "cost_usd") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0.0`); err != nil {
			return fmt.Errorf("migrate add cost_usd column: %w", err)
		}
	}

	// Idempotent migration: add is_compact_summary column if it doesn't exist (Issue #33).
	if !s.columnExists(ctx, "conversation_messages", "is_compact_summary") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversation_messages ADD COLUMN is_compact_summary INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate add is_compact_summary column: %w", err)
		}
	}

	// Idempotent migration: add workspace and tenant_id columns to conversations if they don't exist.
	if !s.columnExists(ctx, "conversations", "workspace") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate add workspace column: %w", err)
		}
	}
	if !s.columnExists(ctx, "conversations", "tenant_id") {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE conversations ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate add tenant_id column: %w", err)
		}
	}

	// Idempotent migration: create FTS5 triggers if they don't exist.
	// Triggers keep conversation_messages_fts in sync with conversation_messages.
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS conv_msgs_fts_insert AFTER INSERT ON conversation_messages BEGIN
  INSERT INTO conversation_messages_fts(rowid, conversation_id, role, content) VALUES (new.id, new.conversation_id, new.role, new.content);
END`,
		`CREATE TRIGGER IF NOT EXISTS conv_msgs_fts_delete AFTER DELETE ON conversation_messages BEGIN
  INSERT INTO conversation_messages_fts(conversation_messages_fts, rowid, conversation_id, role, content) VALUES ('delete', old.id, old.conversation_id, old.role, old.content);
END`,
		`CREATE TRIGGER IF NOT EXISTS conv_msgs_fts_update AFTER UPDATE ON conversation_messages BEGIN
  INSERT INTO conversation_messages_fts(conversation_messages_fts, rowid, conversation_id, role, content) VALUES ('delete', old.id, old.conversation_id, old.role, old.content);
  INSERT INTO conversation_messages_fts(rowid, conversation_id, role, content) VALUES (new.id, new.conversation_id, new.role, new.content);
END`,
	}
	for _, trigger := range triggers {
		if _, err := s.db.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("migrate create fts trigger: %w", err)
		}
	}

	return nil
}

// columnExists checks if a column exists in a table using PRAGMA table_info.
func (s *SQLiteConversationStore) columnExists(ctx context.Context, table, column string) bool {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// SaveConversation persists a conversation's messages, replacing any existing messages.
// Token/cost fields are left unchanged (or zero for new conversations).
func (s *SQLiteConversationStore) SaveConversation(ctx context.Context, convID string, msgs []Message) error {
	return s.SaveConversationWithCost(ctx, convID, msgs, ConversationTokenCost{})
}

// SaveConversationWithCost persists a conversation's messages along with cumulative
// token usage and cost totals. It replaces any existing messages and overwrites
// the token/cost fields with the provided values.
func (s *SQLiteConversationStore) SaveConversationWithCost(ctx context.Context, convID string, msgs []Message, cost ConversationTokenCost) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	title := extractTitle(msgs)

	// Upsert conversations row (preserves created_at and pinned on conflict).
	// Only set the title when the row is first inserted; subsequent saves
	// preserve whatever title was set previously (auto-generated or user-provided).
	_, err = tx.ExecContext(ctx, `
INSERT INTO conversations (id, title, msg_count, created_at, updated_at, prompt_tokens, completion_tokens, cost_usd, pinned)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
ON CONFLICT(id) DO UPDATE SET
    msg_count         = excluded.msg_count,
    updated_at        = excluded.updated_at,
    prompt_tokens     = excluded.prompt_tokens,
    completion_tokens = excluded.completion_tokens,
    cost_usd          = excluded.cost_usd,
    title             = CASE WHEN conversations.title = '' THEN excluded.title ELSE conversations.title END
`, convID, title, len(msgs), now, now, cost.PromptTokens, cost.CompletionTokens, cost.CostUSD)
	if err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}

	// Delete old messages
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("delete old messages: %w", err)
	}

	// Insert new messages
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO conversation_messages (conversation_id, step, role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for i, msg := range msgs {
		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			data, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("marshal tool calls at step %d: %w", i, err)
			}
			str := string(data)
			toolCallsJSON = &str
		}

		isMeta := 0
		if msg.IsMeta {
			isMeta = 1
		}
		isCompactSummary := 0
		if msg.IsCompactSummary {
			isCompactSummary = 1
		}

		if _, err := stmt.ExecContext(ctx, convID, i, msg.Role, msg.Content, toolCallsJSON, msg.ToolCallID, msg.Name, isMeta, isCompactSummary); err != nil {
			return fmt.Errorf("insert message at step %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// LoadMessages retrieves all messages for a conversation, ordered by step.
func (s *SQLiteConversationStore) LoadMessages(ctx context.Context, convID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary
FROM conversation_messages
WHERE conversation_id = ?
ORDER BY step ASC
`, convID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msg Message
		var toolCallsJSON sql.NullString
		var isMeta, isCompactSummary int
		if err := rows.Scan(&msg.Role, &msg.Content, &toolCallsJSON, &msg.ToolCallID, &msg.Name, &isMeta, &isCompactSummary); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msg.IsMeta = isMeta == 1
		msg.IsCompactSummary = isCompactSummary == 1
		if toolCallsJSON.Valid && toolCallsJSON.String != "" {
			if err := json.Unmarshal([]byte(toolCallsJSON.String), &msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("unmarshal tool calls: %w", err)
			}
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, nil
}

// ListConversations returns conversations ordered by updated_at DESC,
// including cumulative token usage and cost totals.
// When filter.Workspace or filter.TenantID are non-empty, results are restricted to matching rows.
func (s *SQLiteConversationStore) ListConversations(ctx context.Context, filter ConversationFilter, limit, offset int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, title, msg_count, created_at, updated_at, prompt_tokens, completion_tokens, cost_usd, pinned, workspace, tenant_id FROM conversations`
	args := make([]any, 0, 4)
	conditions := make([]string, 0, 2)

	if filter.Workspace != "" {
		conditions = append(conditions, "workspace = ?")
		args = append(args, filter.Workspace)
	}
	if filter.TenantID != "" {
		conditions = append(conditions, "tenant_id = ?")
		args = append(args, filter.TenantID)
	}

	if len(conditions) > 0 {
		query += " WHERE " + conditions[0]
		for _, c := range conditions[1:] {
			query += " AND " + c
		}
	}

	query += " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		var createdText, updatedText string
		var pinned int
		if err := rows.Scan(&c.ID, &c.Title, &c.MsgCount, &createdText, &updatedText, &c.PromptTokens, &c.CompletionTokens, &c.CostUSD, &pinned, &c.Workspace, &c.TenantID); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdText)
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedText)
		c.Pinned = pinned == 1
		convs = append(convs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	if convs == nil {
		convs = []Conversation{}
	}
	return convs, nil
}

// UpdateConversationMeta sets the workspace and tenant_id on a conversation row.
// It is safe to call multiple times (idempotent). Skips if convID does not exist.
func (s *SQLiteConversationStore) UpdateConversationMeta(ctx context.Context, convID, workspace, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE conversations SET workspace = ?, tenant_id = ? WHERE id = ?
`, workspace, tenantID, convID)
	if err != nil {
		return fmt.Errorf("update conversation meta: %w", err)
	}
	return nil
}

// GetConversationOwner returns the Conversation metadata row for convID, or nil
// if the conversation does not exist in the store. Only the id, tenant_id, and
// workspace fields are guaranteed to be populated; other fields may be zero.
func (s *SQLiteConversationStore) GetConversationOwner(ctx context.Context, convID string) (*Conversation, error) {
	var c Conversation
	var createdText, updatedText string
	var pinned int
	err := s.db.QueryRowContext(ctx, `
SELECT id, title, msg_count, created_at, updated_at, prompt_tokens, completion_tokens, cost_usd, pinned, workspace, tenant_id
FROM conversations WHERE id = ?
`, convID).Scan(&c.ID, &c.Title, &c.MsgCount, &createdText, &updatedText,
		&c.PromptTokens, &c.CompletionTokens, &c.CostUSD, &pinned, &c.Workspace, &c.TenantID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation owner: %w", err)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdText)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedText)
	c.Pinned = pinned == 1
	return &c, nil
}

// DeleteConversation removes a conversation and its messages (via CASCADE).
func (s *SQLiteConversationStore) DeleteConversation(ctx context.Context, convID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, convID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

// SaveRewindPoint persists a pre-tool snapshot atomically with its files.
func (s *SQLiteConversationStore) SaveRewindPoint(ctx context.Context, point RewindPoint) error {
	if point.ID == "" || point.ConversationID == "" {
		return fmt.Errorf("rewind point id and conversation id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rewind begin tx: %w", err)
	}
	defer tx.Rollback()
	created := point.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	// Tool snapshots can arrive before the run's normal terminal persistence.
	// Create the minimal conversation row first so the foreign key remains real.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO conversations (id, created_at, updated_at) VALUES (?, ?, ?)`, point.ConversationID, created.Format(time.RFC3339Nano), created.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("create rewind conversation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rewind_points (id, conversation_id, step, tool, created_at) VALUES (?, ?, ?, ?, ?)`, point.ID, point.ConversationID, point.Step, point.Tool, created.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert rewind point: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO rewind_file_snapshots (point_id, path, content, existed, skipped, skip_reason, expected_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare rewind file: %w", err)
	}
	defer stmt.Close()
	for _, file := range point.Files {
		existed, skipped := 0, 0
		if file.Exists {
			existed = 1
		}
		if file.Skipped {
			skipped = 1
		}
		if _, err := stmt.ExecContext(ctx, point.ID, file.Path, file.Content, existed, skipped, file.SkipReason, file.ExpectedHash); err != nil {
			return fmt.Errorf("insert rewind file: %w", err)
		}
	}
	return tx.Commit()
}

// ListRewindPoints returns newest rewind points first with their captured files.
func (s *SQLiteConversationStore) ListRewindPoints(ctx context.Context, convID string) ([]RewindPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.step, p.tool, p.created_at, f.path, f.content, COALESCE(f.existed,0), COALESCE(f.skipped,0), COALESCE(f.skip_reason,''), COALESCE(f.expected_hash,'') FROM rewind_points p LEFT JOIN rewind_file_snapshots f ON f.point_id=p.id WHERE p.conversation_id=? ORDER BY p.step DESC, p.created_at DESC, f.id ASC`, convID)
	if err != nil {
		return nil, fmt.Errorf("list rewind points: %w", err)
	}
	defer rows.Close()
	byID := map[string]int{}
	points := []RewindPoint{}
	for rows.Next() {
		var id, tool, created, reason, expected string
		var path sql.NullString
		var step, existed, skipped int
		var content []byte
		if err := rows.Scan(&id, &step, &tool, &created, &path, &content, &existed, &skipped, &reason, &expected); err != nil {
			return nil, fmt.Errorf("scan rewind point: %w", err)
		}
		i, ok := byID[id]
		if !ok {
			t, _ := time.Parse(time.RFC3339Nano, created)
			i = len(points)
			byID[id] = i
			points = append(points, RewindPoint{ID: id, ConversationID: convID, Step: step, Tool: tool, CreatedAt: t})
		}
		if path.Valid && path.String != "" {
			points[i].Files = append(points[i].Files, RewindFileSnapshot{Path: path.String, Content: content, Exists: existed == 1, Skipped: skipped == 1, SkipReason: reason, ExpectedHash: expected})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rewind points rows: %w", err)
	}
	return points, nil
}

// RestoreRewindPoint restores captured pre-images and then truncates messages
// after the point in one database transaction. Files are checked before any
// write, so external changes refuse the whole restore unless force is true.
func (s *SQLiteConversationStore) RestoreRewindPoint(ctx context.Context, convID, pointID, workspace string, force bool) (RewindRestoreResult, error) {
	points, err := s.ListRewindPoints(ctx, convID)
	if err != nil {
		return RewindRestoreResult{}, err
	}
	var point *RewindPoint
	for i := range points {
		if points[i].ID == pointID {
			point = &points[i]
			break
		}
	}
	if point == nil {
		return RewindRestoreResult{}, fmt.Errorf("rewind point %q not found", pointID)
	}
	if workspace == "" {
		return RewindRestoreResult{}, fmt.Errorf("workspace is required")
	}
	for _, file := range point.Files {
		if file.Skipped {
			continue
		}
		current, readErr := os.ReadFile(filepath.Join(workspace, file.Path))
		actual := ""
		if readErr == nil {
			actual = RewindContentHash(current)
		} else if !os.IsNotExist(readErr) {
			return RewindRestoreResult{}, fmt.Errorf("read %s: %w", file.Path, readErr)
		}
		if !force && file.ExpectedHash != "" && actual != file.ExpectedHash {
			return RewindRestoreResult{}, fmt.Errorf("rewind refused: %s was modified outside the agent", file.Path)
		}
	}
	result := RewindRestoreResult{}
	for _, file := range point.Files {
		if file.Skipped {
			continue
		}
		path := filepath.Join(workspace, file.Path)
		if file.Exists {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return result, err
			}
			if err := os.WriteFile(path, file.Content, 0o644); err != nil {
				return result, err
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		result.FilesRestored++
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("rewind begin tx: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE conversation_id=? AND step>?`, convID, point.Step)
	if err != nil {
		return result, fmt.Errorf("rewind truncate messages: %w", err)
	}
	n, _ := res.RowsAffected()
	result.MessagesTruncated = int(n)
	if _, err := tx.ExecContext(ctx, `DELETE FROM rewind_points WHERE conversation_id=? AND (step>? OR (step=? AND id<>?))`, convID, point.Step, point.Step, point.ID); err != nil {
		return result, fmt.Errorf("rewind delete future points: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET msg_count=(SELECT COUNT(*) FROM conversation_messages WHERE conversation_id=?), updated_at=? WHERE id=?`, convID, time.Now().UTC().Format(time.RFC3339Nano), convID); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("rewind commit: %w", err)
	}
	return result, nil
}

// DeleteOldConversations removes all non-pinned conversations whose updated_at is
// before olderThan. A zero olderThan is a no-op. Returns the number deleted.
func (s *SQLiteConversationStore) DeleteOldConversations(ctx context.Context, olderThan time.Time) (int, error) {
	if olderThan.IsZero() {
		return 0, nil
	}
	threshold := olderThan.UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM conversations WHERE updated_at < ? AND pinned = 0`,
		threshold,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old conversations: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(n), nil
}

// PinConversation sets or clears the pinned flag on a conversation.
// Returns an error if the conversation does not exist.
func (s *SQLiteConversationStore) PinConversation(ctx context.Context, convID string, pin bool) error {
	pinVal := 0
	if pin {
		pinVal = 1
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET pinned = ? WHERE id = ?`,
		pinVal, convID,
	)
	if err != nil {
		return fmt.Errorf("pin conversation: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pin conversation rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("conversation %q not found", convID)
	}
	return nil
}

// CompactConversation replaces messages before keepFromStep with a single summary
// message, then renumbers the remaining messages starting at step 1.
// keepFromStep=0 means only the summary remains. keepFromStep >= len(msgs) means
// all messages are kept and the summary is prepended. The conversation must exist.
func (s *SQLiteConversationStore) CompactConversation(ctx context.Context, convID string, keepFromStep int, summary Message) error {
	if keepFromStep < 0 {
		return fmt.Errorf("keepFromStep must be >= 0, got %d", keepFromStep)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compact: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the conversation exists.
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations WHERE id = ?`, convID).Scan(&exists); err != nil {
		return fmt.Errorf("compact: check conversation: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("compact: conversation %q not found", convID)
	}

	// Load messages that will be kept (step >= keepFromStep), in order.
	rows, err := tx.QueryContext(ctx, `
SELECT role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary
FROM conversation_messages
WHERE conversation_id = ? AND step >= ?
ORDER BY step ASC
`, convID, keepFromStep)
	if err != nil {
		return fmt.Errorf("compact: query kept messages: %w", err)
	}

	var kept []Message
	for rows.Next() {
		var msg Message
		var toolCallsJSON sql.NullString
		var isMeta, isCompact int
		if err := rows.Scan(&msg.Role, &msg.Content, &toolCallsJSON, &msg.ToolCallID, &msg.Name, &isMeta, &isCompact); err != nil {
			rows.Close()
			return fmt.Errorf("compact: scan kept message: %w", err)
		}
		msg.IsMeta = isMeta == 1
		msg.IsCompactSummary = isCompact == 1
		if toolCallsJSON.Valid && toolCallsJSON.String != "" {
			if err := json.Unmarshal([]byte(toolCallsJSON.String), &msg.ToolCalls); err != nil {
				rows.Close()
				return fmt.Errorf("compact: unmarshal tool calls: %w", err)
			}
		}
		kept = append(kept, msg)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("compact: rows error: %w", err)
	}

	// Delete all existing messages for this conversation.
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("compact: delete messages: %w", err)
	}

	// Build the new message list: summary first, then kept messages.
	newMsgs := make([]Message, 0, 1+len(kept))
	newMsgs = append(newMsgs, summary)
	newMsgs = append(newMsgs, kept...)

	// Re-insert all messages.
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO conversation_messages (conversation_id, step, role, content, tool_calls_json, tool_call_id, name, is_meta, is_compact_summary)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("compact: prepare insert: %w", err)
	}
	defer stmt.Close()

	for i, msg := range newMsgs {
		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			data, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("compact: marshal tool calls at step %d: %w", i, err)
			}
			str := string(data)
			toolCallsJSON = &str
		}
		isMeta := 0
		if msg.IsMeta {
			isMeta = 1
		}
		isCompactSummary := 0
		if msg.IsCompactSummary {
			isCompactSummary = 1
		}
		if _, err := stmt.ExecContext(ctx, convID, i, msg.Role, msg.Content, toolCallsJSON, msg.ToolCallID, msg.Name, isMeta, isCompactSummary); err != nil {
			return fmt.Errorf("compact: insert message at step %d: %w", i, err)
		}
	}

	// Update the conversation's msg_count and updated_at.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET msg_count = ?, updated_at = ? WHERE id = ?`, len(newMsgs), now, convID); err != nil {
		return fmt.Errorf("compact: update conversation: %w", err)
	}

	return tx.Commit()
}

// SearchMessages performs a full-text search over message content using the FTS5 index.
// When tenantID is non-empty, results are restricted to conversations owned by that
// tenant by joining the FTS hits against the conversations table on conversation_id.
// An empty tenantID disables the filter (auth-disabled callers).
func (s *SQLiteConversationStore) SearchMessages(ctx context.Context, tenantID, query string, limit int) ([]MessageSearchResult, error) {
	if query == "" {
		return []MessageSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	// The FTS table does not carry tenant_id (it indexes message content only), so
	// scope by joining FTS hits to the owning conversation row.
	sqlText := `
SELECT f.conversation_id, f.role, snippet(conversation_messages_fts, 2, '<b>', '</b>', '…', 20)
FROM conversation_messages_fts f
`
	args := []any{}
	if tenantID != "" {
		sqlText += `JOIN conversations c ON c.id = f.conversation_id
WHERE conversation_messages_fts MATCH ? AND c.tenant_id = ?
`
		args = append(args, query, tenantID)
	} else {
		sqlText += `WHERE conversation_messages_fts MATCH ?
`
		args = append(args, query)
	}
	sqlText += `ORDER BY rank
LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var results []MessageSearchResult
	for rows.Next() {
		var r MessageSearchResult
		if err := rows.Scan(&r.ConversationID, &r.Role, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search rows error: %w", err)
	}
	if results == nil {
		results = []MessageSearchResult{}
	}
	return results, nil
}

// extractTitle derives a short title from the first user message in msgs.
// It returns the first sentence (up to the first ". ", "! ", or "? ") or the
// first 80 characters, whichever is shorter. Returns "" if no user message
// with non-empty content is found.
func extractTitle(msgs []Message) string {
	const maxLen = 80
	for _, m := range msgs {
		if m.Role != "user" || m.IsMeta {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		// Take only the first line.
		if idx := strings.IndexByte(content, '\n'); idx >= 0 {
			content = strings.TrimSpace(content[:idx])
		}
		// Find the first sentence boundary (". ", "! ", "? ").
		for _, sep := range []string{". ", "! ", "? "} {
			if idx := strings.Index(content, sep); idx >= 0 {
				candidate := content[:idx+1] // include the punctuation
				if utf8.RuneCountInString(candidate) <= maxLen {
					return candidate
				}
			}
		}
		// Truncate to maxLen runes.
		if utf8.RuneCountInString(content) > maxLen {
			runes := []rune(content)
			content = string(runes[:maxLen]) + "…"
		}
		return content
	}
	return ""
}
