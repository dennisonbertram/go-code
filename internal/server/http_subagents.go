package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
)

// subagentTenantMismatch reports whether the given subagent's stored tenant
// differs from the caller's authenticated tenant. Mirrors runTenantMismatch
// (http_runs.go): no-ops when auth is disabled or no run store is configured,
// and normalizes "" to "default" so untenanted records and the default tenant
// compare equal.
func (s *Server) subagentTenantMismatch(r *http.Request, subagentTenantID string) bool {
	if s.authDisabled || s.runStore == nil {
		return false
	}
	caller := normalizeTenant(TenantIDFromContext(r.Context()))
	return normalizeTenant(subagentTenantID) != caller
}

// filterSubagentsByTenant restricts a subagent list to the caller's own
// tenant. When auth is disabled or no run store is configured, the list is
// returned unfiltered, preserving prior behavior.
func (s *Server) filterSubagentsByTenant(r *http.Request, items []subagents.Subagent) []subagents.Subagent {
	if s.authDisabled || s.runStore == nil {
		return items
	}
	caller := normalizeTenant(TenantIDFromContext(r.Context()))
	filtered := make([]subagents.Subagent, 0, len(items))
	for _, item := range items {
		if normalizeTenant(item.TenantID) == caller {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *Server) handleSubagents(w http.ResponseWriter, r *http.Request) {
	if s.subagentManager == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "subagent manager is not configured")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// GET /v1/subagents — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		items, err := s.subagentManager.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		items = s.filterSubagentsByTenant(r, items)
		writeJSON(w, http.StatusOK, map[string]any{"subagents": items})
	case http.MethodPost:
		// POST /v1/subagents — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		var req subagents.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		// Enforce tenant isolation: derive the effective tenant from the auth
		// context, mirroring handlePostRun (http_runs.go).
		effective, err := s.effectiveTenantID(r, req.TenantID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		req.TenantID = effective
		item, err := s.subagentManager.Create(r.Context(), req)
		if err != nil {
			switch {
			case errors.Is(err, subagents.ErrInvalidConfig):
				writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		writeMethodNotAllowed(w, http.MethodGet)
	}
}

func (s *Server) handleSubagentByID(w http.ResponseWriter, r *http.Request) {
	if s.subagentManager == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "subagent manager is not configured")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/subagents/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "subagent not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		switch parts[1] {
		case "wait":
			s.handleSubagentWait(w, r, id)
		case "cancel":
			if !hasScope(r.Context(), store.ScopeRunsWrite) {
				writeScopeError(w, store.ScopeRunsWrite)
				return
			}
			s.handleSubagentCancel(w, r, id)
		default:
			writeError(w, http.StatusNotFound, "not_found", "subagent action not found")
		}
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found", "subagent not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// GET /v1/subagents/{id} — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		item, err := s.subagentManager.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, subagents.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		if s.subagentTenantMismatch(r, item.TenantID) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("subagent %q not found", id))
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		// DELETE /v1/subagents/{id} — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		item, err := s.subagentManager.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, subagents.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		if s.subagentTenantMismatch(r, item.TenantID) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("subagent %q not found", id))
			return
		}
		err = s.subagentManager.Delete(r.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, subagents.ErrNotFound):
				writeError(w, http.StatusNotFound, "not_found", err.Error())
			case errors.Is(err, subagents.ErrActive):
				writeError(w, http.StatusConflict, "subagent_active", err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w, http.MethodGet)
	}
}

func (s *Server) handleSubagentWait(w http.ResponseWriter, r *http.Request, subagentID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}

	// POST /v1/subagents/{id}/wait blocks until terminal state.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, "wait_cancelled", r.Context().Err().Error())
			return
		case <-ticker.C:
			item, err := s.subagentManager.Get(r.Context(), subagentID)
			if err != nil {
				if errors.Is(err, subagents.ErrNotFound) {
					writeError(w, http.StatusNotFound, "not_found", err.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
				return
			}
			if s.subagentTenantMismatch(r, item.TenantID) {
				writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("subagent %q not found", subagentID))
				return
			}
			if item.Status == harness.RunStatusCompleted || item.Status == harness.RunStatusFailed || item.Status == harness.RunStatusCancelled {
				writeJSON(w, http.StatusOK, item)
				return
			}
		}
	}
}

func (s *Server) handleSubagentCancel(w http.ResponseWriter, r *http.Request, subagentID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	item, err := s.subagentManager.Get(r.Context(), subagentID)
	if err != nil {
		if errors.Is(err, subagents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if s.subagentTenantMismatch(r, item.TenantID) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("subagent %q not found", subagentID))
		return
	}
	if err := s.subagentManager.Cancel(r.Context(), subagentID); err != nil {
		if errors.Is(err, subagents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": subagentID, "status": "cancelling"})
}
