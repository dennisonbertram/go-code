package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/store"
)

// checkpointTenantMismatch reports whether the checkpoint identified by
// checkpointID belongs to a tenant other than the caller's.
//
// It resolves the checkpoint's owning tenant via its RunID: the run's TenantID
// is compared to the caller's authenticated tenant. Resolution order mirrors
// runTenantMismatch: in-memory runner state first, then the persistent store.
//
//   - Auth disabled or no runStore → never gates (returns false).
//   - Checkpoint's RunID is empty (e.g. workflow-only checkpoints) → no run to
//     resolve from; do not gate (treat as unknown ownership).
//   - Run found and its tenant differs from the caller → gate (returns true).
//   - Run not found in either source, caller tenant non-empty → fail CLOSED
//     (return true): a checkpoint that references a run whose tenant cannot be
//     resolved must not be served to an authenticated tenant. This prevents a
//     deleted/missing run from silently becoming a cross-tenant gate bypass.
//   - Run not found, caller tenant empty (auth-disabled path) → do not gate.
func (s *Server) checkpointTenantMismatch(r *http.Request, record checkpoints.Record) bool {
	if s.authDisabled || s.runStore == nil {
		return false
	}
	if record.RunID == "" {
		// No run linkage: cannot enforce ownership. Do not gate.
		return false
	}
	caller := normalizeTenant(TenantIDFromContext(r.Context()))

	// In-memory state first: covers active and recently completed runs.
	if s.runner != nil {
		if run, ok := s.runner.GetRun(record.RunID); ok {
			return normalizeTenant(run.TenantID) != caller
		}
	}

	// Fall back to the persistent store for historical runs.
	storeRun, err := s.runStore.GetRun(r.Context(), record.RunID)
	if err == nil && storeRun != nil {
		return normalizeTenant(storeRun.TenantID) != caller
	}

	// Run not found in either source. When an authenticated (non-empty) tenant
	// is making the request, fail CLOSED: a checkpoint whose owning run cannot
	// be resolved must not be served. An empty caller tenant (normalized to
	// "default") means auth is effectively disabled for this request; do not gate.
	return caller != "default"
}

type checkpointManager interface {
	Get(ctx context.Context, id string) (checkpoints.Record, error)
	Resume(ctx context.Context, id string, payload map[string]any) error
}

func (s *Server) registerCheckpointRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("/v1/checkpoints/", auth(http.HandlerFunc(s.handleCheckpointByID)))
}

func (s *Server) handleCheckpointByID(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/checkpoints/") {
		http.NotFound(w, r)
		return
	}
	if s.checkpoints == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "checkpoint service is not configured")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/checkpoints/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	checkpointID := parts[0]
	if len(parts) == 1 {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleGetCheckpoint(w, r, checkpointID)
		return
	}
	if len(parts) == 2 && parts[1] == "resume" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleResumeCheckpoint(w, r, checkpointID)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleGetCheckpoint(w http.ResponseWriter, r *http.Request, checkpointID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	record, err := s.checkpoints.Get(r.Context(), checkpointID)
	if err != nil {
		if checkpoints.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("checkpoint %q not found", checkpointID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// Enforce tenant ownership: a checkpoint's tenant is resolved via its RunID.
	// Cross-tenant access returns 404 (matching the unknown-id contract).
	if s.checkpointTenantMismatch(r, record) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("checkpoint %q not found", checkpointID))
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleResumeCheckpoint(w http.ResponseWriter, r *http.Request, checkpointID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}

	// Enforce tenant ownership before mutating: resolve the checkpoint's tenant
	// via its RunID. Cross-tenant access returns 404 (matching the unknown-id
	// contract so the existence of another tenant's checkpoint is never revealed).
	existing, err := s.checkpoints.Get(r.Context(), checkpointID)
	if err != nil {
		if checkpoints.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("checkpoint %q not found", checkpointID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if s.checkpointTenantMismatch(r, existing) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("checkpoint %q not found", checkpointID))
		return
	}

	if err := s.checkpoints.Resume(r.Context(), checkpointID, req.Payload); err != nil {
		if checkpoints.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("checkpoint %q not found", checkpointID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "resumed"})
}
