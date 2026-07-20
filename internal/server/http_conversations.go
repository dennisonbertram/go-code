package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
)

func (s *Server) registerConversationRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("/v1/conversations/", auth(http.HandlerFunc(s.handleConversations)))
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/conversations/")

	// GET /v1/conversations/ — list conversations (runs:read)
	if path == "" || r.URL.Path == "/v1/conversations/" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleListConversations(w, r)
		return
	}

	parts := strings.Split(path, "/")

	// GET /v1/conversations/search?q=... — full-text search (runs:read)
	if len(parts) == 1 && parts[0] == "search" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleSearchConversations(w, r)
		return
	}

	// DELETE /v1/conversations/{id} — requires runs:write
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleDeleteConversation(w, r, parts[0])
		return
	}

	// GET /v1/conversations/{id}/messages (runs:read)
	if len(parts) == 2 && parts[1] == "messages" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		convID := parts[0]
		if s.blockConversationCrossTenant(w, r, convID) {
			return
		}
		msgs, ok := s.runner.ConversationMessages(convID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
		return
	}

	// GET /v1/conversations/{id}/runs — list runs for a conversation (runs:read)
	if len(parts) == 2 && parts[1] == "runs" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleListConversationRuns(w, r, parts[0])
		return
	}

	// GET /v1/conversations/{id}/export — JSONL export (runs:read)
	if len(parts) == 2 && parts[1] == "export" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleExportConversation(w, r, parts[0])
		return
	}

	// GET /v1/conversations/{id}/rewind-points — list file snapshots (runs:read).
	if len(parts) == 2 && parts[1] == "rewind-points" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleListRewindPoints(w, r, parts[0])
		return
	}
	// POST /v1/conversations/{id}/rewind — destructive file/conversation restore (runs:write).
	if len(parts) == 2 && parts[1] == "rewind" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleRestoreRewind(w, r, parts[0])
		return
	}

	// POST /v1/conversations/{id}/compact — context compaction (runs:write) (Issue #33)
	if len(parts) == 2 && parts[1] == "compact" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleCompactConversation(w, r, parts[0])
		return
	}

	// POST /v1/conversations/{id}/undo — drop recent prompts from the active context (runs:write) (Issue #805)
	if len(parts) == 2 && parts[1] == "undo" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleUndoConversation(w, r, parts[0])
		return
	}

	// POST /v1/conversations/{id}/fork — duplicate the conversation with its full
	// message history under a new ID (runs:write) (epic #816)
	if len(parts) == 2 && parts[1] == "fork" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockConversationCrossTenant(w, r, parts[0]) {
			return
		}
		s.handleForkConversation(w, r, parts[0])
		return
	}

	// POST /v1/conversations/cleanup — retention-based bulk delete (admin) (Issue #34)
	//
	// Gated to admin scope rather than runs:write: DeleteOldConversations has no
	// tenant parameter (internal/harness/conversation_store.go), so this endpoint
	// deletes across ALL tenants. Restricting to admin is the safe interim until
	// the store interface gains a tenant-scoped variant.
	if len(parts) == 1 && parts[0] == "cleanup" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !hasScope(r.Context(), store.ScopeAdmin) {
			writeScopeError(w, store.ScopeAdmin)
			return
		}
		s.handleConversationsCleanup(w, r)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleListRewindPoints(w http.ResponseWriter, r *http.Request, convID string) {
	rewind, ok := s.runner.GetConversationStore().(harness.RewindStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "not_implemented", "rewind persistence is not configured")
		return
	}
	points, err := rewind.ListRewindPoints(r.Context(), convID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": points})
}

func (s *Server) handleRestoreRewind(w http.ResponseWriter, r *http.Request, convID string) {
	var req struct {
		PointID string `json:"point_id"`
		Force   bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PointID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "point_id is required")
		return
	}
	store := s.runner.GetConversationStore()
	rewind, ok := store.(harness.RewindStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "not_implemented", "rewind persistence is not configured")
		return
	}
	owner, err := store.GetConversationOwner(r.Context(), convID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if owner == nil || owner.Workspace == "" {
		writeError(w, http.StatusNotFound, "not_found", "conversation workspace not found")
		return
	}
	result, err := rewind.RestoreRewindPoint(r.Context(), convID, req.PointID, owner.Workspace, req.Force)
	if err != nil {
		code := http.StatusConflict
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		writeError(w, code, "rewind_refused", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSearchConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query parameter \"q\" is required")
		return
	}

	// Enforce tenant isolation: full-text search must only return the caller's own
	// messages. effectiveTenantID resolves the authenticated tenant (and rejects a
	// conflicting ?tenant_id=). When auth is disabled it returns the request value
	// (empty by default), which disables the filter and preserves prior behavior.
	//
	// Tenant consistency note: conversations are stored with the LITERAL tenant ID
	// from the run (runner.go normalises "default" → "" before writing to the
	// store, so the default/no-tenant case is stored as ""). effectiveTenantID
	// likewise returns "" for auth-disabled requests, causing SearchMessages to
	// skip the tenant filter entirely — which is the correct legacy behaviour.
	// Named tenants (e.g. "tenant-foo") are stored and queried as the same literal
	// string, so stamping and querying are self-consistent for all named tenants.
	//
	// This differs from run-ownership resolution (http_runs.go runTenantMismatch),
	// which normalises "" → "default" for comparison. The asymmetry is intentional:
	// conversation rows use "" as the no-tenant sentinel (matching the DB default),
	// while run records keep "default" as an explicit value. There is no case where
	// a named tenant's own conversations are hidden from that tenant.
	requestTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	effectiveTenant, err := s.effectiveTenantID(r, requestTenantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			limit = n
		}
	}

	results, err := store.SearchMessages(r.Context(), effectiveTenant, q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleExportConversation(w http.ResponseWriter, r *http.Request, convID string) {
	// Try in-memory first (active run), fall back to store.
	msgs, ok := s.runner.ConversationMessages(convID)
	if !ok {
		store := s.runner.GetConversationStore()
		if store == nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
			return
		}
		loaded, err := store.LoadMessages(r.Context(), convID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if len(loaded) == 0 {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
			return
		}
		msgs = loaded
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			return
		}
	}
}

// handleCompactConversation handles POST /v1/conversations/{id}/compact.
// It replaces early messages with a summary (Issue #33).
func (s *Server) handleCompactConversation(w http.ResponseWriter, r *http.Request, convID string) {
	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}

	var req struct {
		KeepFromStep int    `json:"keep_from_step"`
		Summary      string `json:"summary"`
		Role         string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.Summary) == "" {
		// Auto-generate summary via LLM when none provided.
		msgs, err := store.LoadMessages(r.Context(), convID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if len(msgs) == 0 {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
			return
		}
		generated, err := s.runner.SummarizeMessages(r.Context(), msgs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("auto-summary failed: %s", err.Error()))
			return
		}
		req.Summary = generated
	}
	if req.KeepFromStep < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "keep_from_step must be >= 0")
		return
	}

	role := req.Role
	if role == "" {
		role = "system"
	}

	summaryMsg := harness.Message{
		Role:             role,
		Content:          req.Summary,
		IsCompactSummary: true,
	}

	if err := store.CompactConversation(r.Context(), convID, req.KeepFromStep, summaryMsg); err != nil {
		// Distinguish "not found" from other errors by checking the error message.
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
			return
		}
		if strings.Contains(err.Error(), "keepFromStep must be") {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Return the new message count.
	msgs, err := store.LoadMessages(r.Context(), convID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"compacted":     true,
		"message_count": len(msgs),
	})
}

// handleUndoConversation handles POST /v1/conversations/{id}/undo.
// It removes the last N user prompts (default 1) and every message after them
// from the persisted conversation (Issue #805). The body accepts either
// {"count": N} or {"to_step": S} (undo back to the prompt at step S); an empty
// or field-less body undoes the single most recent prompt.
//
// Like the compact route, this mutates only the persisted conversation; a
// caller with an in-flight run (the TUI) refetches messages after success.
func (s *Server) handleUndoConversation(w http.ResponseWriter, r *http.Request, convID string) {
	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}

	var req struct {
		Count  *int `json:"count"`
		ToStep *int `json:"to_step"`
	}
	// io.EOF (empty body) is accepted as an all-defaults request.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Count != nil && req.ToStep != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "provide count or to_step, not both")
		return
	}

	count := 1
	if req.Count != nil {
		count = *req.Count
	}
	if req.ToStep != nil {
		resolved, err := undoCountForStep(r, store, convID, *req.ToStep)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		count = resolved
	}

	removedFromStep, err := store.UndoPrompts(r.Context(), convID, count)
	if err != nil {
		switch {
		case errors.Is(err, harness.ErrUndoCrossesCompaction):
			writeError(w, http.StatusConflict, "undo_crosses_compaction", err.Error())
		case errors.Is(err, harness.ErrUndoCountOutOfRange):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case strings.Contains(err.Error(), "not found"):
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	msgs, err := store.LoadMessages(r.Context(), convID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"undone":             true,
		"removed_from_step":  removedFromStep,
		"remaining_messages": len(msgs),
	})
}

// undoCountForStep converts a to_step undo request into a prompt count for
// ConversationStore.UndoPrompts: the referenced step must exist and point at a
// non-meta user prompt, and the count is the number of non-meta user prompts
// from that step through the newest. The store's own range and compaction
// guards still apply to the resolved count.
func undoCountForStep(r *http.Request, store harness.ConversationStore, convID string, step int) (int, error) {
	if step < 0 {
		return 0, fmt.Errorf("to_step must be >= 0, got %d", step)
	}
	msgs, err := store.LoadMessages(r.Context(), convID)
	if err != nil {
		return 0, fmt.Errorf("load messages: %w", err)
	}
	if len(msgs) == 0 {
		return 0, fmt.Errorf("conversation %q not found", convID)
	}
	if step >= len(msgs) {
		return 0, fmt.Errorf("to_step %d is beyond the conversation history (%d messages)", step, len(msgs))
	}
	if msgs[step].Role != "user" || msgs[step].IsMeta {
		return 0, fmt.Errorf("to_step %d does not reference a user prompt", step)
	}
	count := 0
	for i := step; i < len(msgs); i++ {
		if msgs[i].Role == "user" && !msgs[i].IsMeta {
			count++
		}
	}
	return count, nil
}

// handleForkConversation handles POST /v1/conversations/{id}/fork (epic #816).
// It duplicates the source conversation — full message history included — under
// a freshly minted conversation ID; afterwards the two conversations diverge
// independently. Store-backed sources are forked at the store level so metadata
// (workspace, tenant) is inherited; sources that exist only in the runner's
// in-memory mirror are persisted via SaveConversation and stamped with the
// source run's tenant. Response: {"conversation_id", "forked_from",
// "message_count"}; 404 for an unknown source, 501 when no store is configured.
func (s *Server) handleForkConversation(w http.ResponseWriter, r *http.Request, convID string) {
	convStore := s.runner.GetConversationStore()
	if convStore == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}
	ctx := r.Context()
	newID := uuid.New().String()

	owner, err := convStore.GetConversationOwner(ctx, convID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if owner != nil {
		// Store-backed source: fork at the store level to inherit metadata.
		if _, err := convStore.ForkConversation(ctx, convID, newID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		count := owner.MsgCount
		// A live run may have mirrored newer turns than the store has
		// persisted; refresh the fork with the in-memory view when it is
		// ahead so a mid-run fork captures the latest turn.
		if msgs, ok := s.runner.ConversationMessages(convID); ok && len(msgs) > count {
			if err := convStore.SaveConversation(ctx, newID, msgs); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
				return
			}
			count = len(msgs)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"conversation_id": newID,
			"forked_from":     convID,
			"message_count":   count,
		})
		return
	}

	// In-memory-only source (resolved via the runner's conversation mirror).
	msgs, ok := s.runner.ConversationMessages(convID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
		return
	}
	if err := convStore.SaveConversation(ctx, newID, msgs); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.inheritConversationTenant(r, convID, newID)

	writeJSON(w, http.StatusOK, map[string]any{
		"conversation_id": newID,
		"forked_from":     convID,
		"message_count":   len(msgs),
	})
}

// inheritConversationTenant copies the tenant stamp from the source
// conversation's owning run onto a freshly forked conversation row, so the
// copy of a tenant-owned conversation is not world-readable until the next
// run on the fork stamps it. Best effort: the next persist re-stamps anyway.
func (s *Server) inheritConversationTenant(r *http.Request, srcID, newID string) {
	if s.runStore == nil {
		return
	}
	runs, err := s.runStore.ListRuns(r.Context(), store.RunFilter{ConversationID: srcID})
	if err != nil || len(runs) == 0 {
		return
	}
	tenantID := runs[0].TenantID
	if tenantID == "default" {
		// Match the runner's normalisation: "default" is stored as "".
		tenantID = ""
	}
	if tenantID == "" {
		return
	}
	if convStore := s.runner.GetConversationStore(); convStore != nil {
		_ = convStore.UpdateConversationMeta(r.Context(), newID, "", tenantID)
	}
}

// handleConversationsCleanup handles POST /v1/conversations/cleanup.
// It deletes non-pinned conversations older than max_age_days (default 30).
// Response: {"deleted": N}
func (s *Server) handleConversationsCleanup(w http.ResponseWriter, r *http.Request) {
	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}

	var req struct {
		MaxAgeDays *int `json:"max_age_days"`
	}
	// Body is optional — ignore decode errors for empty body.
	_ = json.NewDecoder(r.Body).Decode(&req)

	maxAgeDays := 30
	if req.MaxAgeDays != nil {
		maxAgeDays = *req.MaxAgeDays
	}

	if maxAgeDays <= 0 {
		// 0 means disabled — nothing to delete.
		writeJSON(w, http.StatusOK, map[string]any{"deleted": 0})
		return
	}

	threshold := s.timeNow().UTC().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	n, err := store.DeleteOldConversations(r.Context(), threshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	// Delegate to search handler when ?q= is present.
	if q := r.URL.Query().Get("q"); q != "" {
		s.handleSearchConversations(w, r)
		return
	}

	// Enforce tenant isolation: validate ?tenant_id= against auth context before
	// even checking whether a conversation store is configured. A cross-tenant
	// enumeration attempt should be rejected with 400, not 501.
	requestTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	effectiveTenant, err := s.effectiveTenantID(r, requestTenantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			offset = n
		}
	}

	filter := harness.ConversationFilter{
		Workspace: strings.TrimSpace(r.URL.Query().Get("workspace")),
		TenantID:  effectiveTenant,
	}

	convs, err := store.ListConversations(r.Context(), filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": convs})
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request, convID string) {
	store := s.runner.GetConversationStore()
	if store == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "conversation persistence is not configured")
		return
	}
	if err := store.DeleteConversation(r.Context(), convID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
