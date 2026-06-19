package server

import (
	"context"
	"encoding/json"
	"fmt"
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
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		run, err := s.scriptWorkflows.Start(r.Context(), name, req.Args)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
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
		var req struct {
			Args map[string]any `json:"args"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
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

		// Emit historical events
		for _, ev := range history {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, mustJSON(ev.Payload))
			flusher.Flush()
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
