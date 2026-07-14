package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go-agent-harness/internal/store"
	"go-agent-harness/internal/workflow"
)

// scriptWorkflowManager is the interface for the new script-based workflow engine.
type scriptWorkflowManager interface {
	List() []workflow.Meta
	Start(ctx context.Context, name string, args any) (*workflow.Run, error)
	GetRun(runID string) (*workflow.Run, error)
	Subscribe(runID string) ([]workflow.Event, <-chan workflow.Event, func(), error)
	Resume(ctx context.Context, runID string, args any) (*workflow.Run, error)
}

func (s *Server) registerScriptWorkflowRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("/v1/script-workflows", auth(http.HandlerFunc(s.handleScriptWorkflows)))
	mux.Handle("/v1/script-workflows/", auth(http.HandlerFunc(s.handleScriptWorkflowByName)))
	mux.Handle("/v1/script-workflow-runs/", auth(http.HandlerFunc(s.handleScriptWorkflowRunByID)))
}

// recordScriptWorkflowTenant stamps runID with the tenant that started it
// (S3). Safe to call with an empty tenant (no-auth mode) — harmless, since
// scriptWorkflowTenantMismatch never gates when auth is disabled.
func (s *Server) recordScriptWorkflowTenant(runID, tenantID string) {
	s.scriptWorkflowMu.Lock()
	defer s.scriptWorkflowMu.Unlock()
	if s.scriptWorkflowTenants == nil {
		s.scriptWorkflowTenants = make(map[string]string)
	}
	s.scriptWorkflowTenants[runID] = tenantID
}

// getScriptWorkflowTenant returns the tenant that started runID, if recorded.
func (s *Server) getScriptWorkflowTenant(runID string) (string, bool) {
	s.scriptWorkflowMu.Lock()
	defer s.scriptWorkflowMu.Unlock()
	t, ok := s.scriptWorkflowTenants[runID]
	return t, ok
}

// scriptWorkflowTenantMismatch reports whether a script-workflow run request
// must be blocked because the run belongs to a tenant other than the
// caller's. Mirrors runTenantMismatch / conversationTenantMismatch
// (http_runs.go):
//
//   - Auth disabled or no store configured → never gates (returns false),
//     preserving unauthenticated / no-persistence behavior.
//   - Ownership never recorded (unknown run ID, or a run started before this
//     process tracked ownership) → returns false so the downstream call
//     produces its own not-found response unchanged.
//   - Ownership recorded and differs from the caller's (normalized) tenant →
//     gate (returns true).
func (s *Server) scriptWorkflowTenantMismatch(r *http.Request, runID string) bool {
	if s.authDisabled || s.runStore == nil {
		return false
	}
	caller := normalizeTenant(TenantIDFromContext(r.Context()))
	owner, ok := s.getScriptWorkflowTenant(runID)
	if !ok {
		return false
	}
	return normalizeTenant(owner) != caller
}

// blockScriptWorkflowCrossTenant writes a 404 not_found response and returns
// true when runID belongs to a tenant other than the caller's (S3). 404 (not
// 403) so that another tenant's run ID is never distinguishable from an
// unknown one — matching blockCrossTenant / blockConversationCrossTenant.
func (s *Server) blockScriptWorkflowCrossTenant(w http.ResponseWriter, r *http.Request, runID string) bool {
	if s.scriptWorkflowTenantMismatch(r, runID) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("script workflow run %q not found", runID))
		return true
	}
	return false
}

func (s *Server) handleScriptWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.scriptWorkflows == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "script workflow service is not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflows": s.scriptWorkflows.List()})
	default:
		writeMethodNotAllowed(w, http.MethodGet)
	}
}

func (s *Server) handleScriptWorkflowByName(w http.ResponseWriter, r *http.Request) {
	if s.scriptWorkflows == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "script workflow service is not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/script-workflows/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]

	if len(parts) == 1 {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		// Return workflow metadata if available in the list
		for _, m := range s.scriptWorkflows.List() {
			if m.Name == name {
				writeJSON(w, http.StatusOK, m)
				return
			}
		}
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("script workflow %q not found", name))
		return
	}

	if len(parts) == 2 && parts[1] == "runs" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		var req struct {
			Args map[string]any `json:"args"`
		}
		if r.Body != nil {
			// io.EOF means an empty body, which legitimately means "no args";
			// any other decode error means the body is malformed and must be
			// rejected rather than silently proceeding with nil args.
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				writeJSONDecodeError(w, err)
				return
			}
		}
		run, err := s.scriptWorkflows.Start(r.Context(), name, req.Args)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		// Stamp ownership with the caller's authenticated tenant (S3) so later
		// reads/resumes/event-streams of this run can be tenant-gated.
		s.recordScriptWorkflowTenant(run.ID, TenantIDFromContext(r.Context()))
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id":        run.ID,
			"status":        run.Status,
			"workflow_name": run.WorkflowName,
		})
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleScriptWorkflowRunByID(w http.ResponseWriter, r *http.Request) {
	if s.scriptWorkflows == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "script workflow service is not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/script-workflow-runs/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]

	if len(parts) == 1 {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if s.blockScriptWorkflowCrossTenant(w, r, runID) {
			return
		}
		run, err := s.scriptWorkflows.GetRun(runID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("script workflow run %q not found", runID))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":            run.ID,
			"workflow_name": run.WorkflowName,
			"status":        run.Status,
			"result_json":   run.ResultJSON,
			"error":         run.Error,
			"created_at":    run.CreatedAt,
			"updated_at":    run.UpdatedAt,
		})
		return
	}

	if len(parts) == 2 && parts[1] == "resume" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if s.blockScriptWorkflowCrossTenant(w, r, runID) {
			return
		}
		var req struct {
			Args map[string]any `json:"args"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				writeJSONDecodeError(w, err)
				return
			}
		}
		run, err := s.scriptWorkflows.Resume(r.Context(), runID, req.Args)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id": run.ID,
			"status": run.Status,
		})
		return
	}

	if len(parts) == 2 && parts[1] == "events" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if s.blockScriptWorkflowCrossTenant(w, r, runID) {
			return
		}
		// C2: verify the run actually exists before subscribing. Subscribe
		// itself does not error for an unknown run ID (the underlying event
		// store returns an empty-but-nil-error result for any unrecognized
		// key), so without this check the handler would fall straight into
		// the live-event wait loop below and block forever — a client hang
		// and a leaked goroutine for every request to a bad or stale run ID.
		if _, err := s.scriptWorkflows.GetRun(runID); err != nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("script workflow run %q not found", runID))
			return
		}
		history, stream, cancel, err := s.scriptWorkflows.Subscribe(runID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("script workflow run %q not found", runID))
			return
		}
		defer cancel()

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "stream_unsupported", "response writer does not support streaming")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")

		// Emit historical events; short-circuit if a terminal event is already in history.
		for _, ev := range history {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, mustJSON(ev.Payload))
			flusher.Flush()
			if ev.Type == workflow.EventWorkflowCompleted || ev.Type == workflow.EventWorkflowFailed {
				return
			}
		}
		// Stream live events
		for {
			select {
			case <-r.Context().Done():
				return
			case ev, ok := <-stream:
				if !ok {
					return
				}
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, mustJSON(ev.Payload))
				flusher.Flush()
				if ev.Type == workflow.EventWorkflowCompleted || ev.Type == workflow.EventWorkflowFailed {
					return
				}
			}
		}
	}

	http.NotFound(w, r)
}
