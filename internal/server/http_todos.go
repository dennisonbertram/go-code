package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/harness/tools/deferred"
)

// handleRunTodos handles GET/PUT /v1/runs/{id}/todos.
func (s *Server) handleRunTodos(w http.ResponseWriter, r *http.Request, runID string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetRunTodos(w, runID)
	case http.MethodPut:
		s.handlePutRunTodos(w, r, runID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleGetRunTodos(w http.ResponseWriter, runID string) {
	if s.todos == nil {
		writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "todos": []deferred.TodoItem{}})
		return
	}
	todos := s.todos.GetTodos(runID)
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "todos": todos})
}

func (s *Server) handlePutRunTodos(w http.ResponseWriter, r *http.Request, runID string) {
	if s.todos == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "todos not configured")
		return
	}

	var req struct {
		Todos []deferred.TodoItem `json:"todos"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Todos == nil {
		req.Todos = []deferred.TodoItem{}
	}

	if err := s.todos.SetTodos(runID, req.Todos); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	todos := s.todos.GetTodos(runID)
	s.runner.EmitEvent(runID, harness.EventTodosUpdated, map[string]any{"todos": todos})
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "todos": todos})
}

// extractRunID extracts the run ID from a path of the form /v1/runs/{id}/...
// It returns the run ID and the remaining suffix after the run ID segment.
func extractRunID(path string) (runID, suffix string) {
	trimmed := strings.TrimPrefix(path, "/v1/runs/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}
