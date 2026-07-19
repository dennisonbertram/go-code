package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
