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
)

func (s *Server) registerRunRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("/v1/runs", auth(http.HandlerFunc(s.handleRuns)))
	mux.Handle("/v1/runs/", auth(http.HandlerFunc(s.handleRunByID)))
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// POST /v1/runs — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handlePostRun(w, r)
	case http.MethodGet:
		// GET /v1/runs — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleListRuns(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// handlePostRun handles POST /v1/runs — starts a new run.
func (s *Server) handlePostRun(w http.ResponseWriter, r *http.Request) {
	var req harness.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	// Enforce tenant isolation: derive the effective tenant from the auth context.
	// When auth is enabled, the caller-supplied tenant_id is validated against the
	// authenticated API key's tenant. A mismatch is rejected to prevent cross-tenant
	// run creation.
	effective, err := s.effectiveTenantID(r, req.TenantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.TenantID = effective

	// Populate InitiatorAPIKeyPrefix from auth context for audit trail provenance.
	req.InitiatorAPIKeyPrefix = APIKeyPrefixFromContext(r.Context())

	run, err := s.runner.StartRun(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id": run.ID,
		"status": run.Status,
	})
}

// handleListRuns handles GET /v1/runs?conversation_id=X&status=Y&tenant_id=Z.
// Requires a runStore; returns 501 if store is not configured.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "run persistence is not configured")
		return
	}

	// Enforce tenant isolation: derive the effective tenant from the auth context.
	// When auth is enabled, callers cannot enumerate another tenant's runs by
	// supplying a different ?tenant_id= query parameter.
	requestTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	effectiveTenant, err := s.effectiveTenantID(r, requestTenantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	filter := store.RunFilter{
		ConversationID: strings.TrimSpace(r.URL.Query().Get("conversation_id")),
		TenantID:       effectiveTenant,
		Status:         store.RunStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
	}
	runs, err := s.runStore.ListRuns(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// handleListConversationRuns handles GET /v1/conversations/{id}/runs.
// Returns all runs associated with the given conversation ID, ordered newest first.
// Requires a runStore; returns 501 if store is not configured.
func (s *Server) handleListConversationRuns(w http.ResponseWriter, r *http.Request, conversationID string) {
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "run persistence is not configured")
		return
	}
	if strings.TrimSpace(conversationID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "conversation ID is required")
		return
	}
	filter := store.RunFilter{
		ConversationID: conversationID,
	}
	runs, err := s.runStore.ListRuns(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleRunByID(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/runs/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(path, "/")
	runID := parts[0]

	// Intercept /v1/runs/replay before treating "replay" as a run ID.
	// POST /v1/runs/replay — requires runs:write.
	if runID == "replay" && len(parts) == 1 {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRunReplay(w, r)
		return
	}

	// GET /v1/runs/{id} — requires runs:read.
	if len(parts) == 1 {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleGetRun(w, r, runID)
		return
	}

	// GET /v1/runs/{id}/events — requires runs:read.
	if len(parts) == 2 && parts[1] == "events" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleRunEvents(w, r, runID)
		return
	}

	// GET/POST /v1/runs/{id}/input — GET requires runs:read; POST requires runs:write.
	if len(parts) == 2 && parts[1] == "input" {
		if r.Method == http.MethodPost {
			if !hasScope(r.Context(), store.ScopeRunsWrite) {
				writeScopeError(w, store.ScopeRunsWrite)
				return
			}
		} else {
			if !hasScope(r.Context(), store.ScopeRunsRead) {
				writeScopeError(w, store.ScopeRunsRead)
				return
			}
		}
		s.handleRunInput(w, r, runID)
		return
	}

	// GET /v1/runs/{id}/summary — requires runs:read.
	if len(parts) == 2 && parts[1] == "summary" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleRunSummary(w, r, runID)
		return
	}

	// POST /v1/runs/{id}/continue — requires runs:write.
	if len(parts) == 2 && parts[1] == "continue" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRunContinue(w, r, runID)
		return
	}

	// POST /v1/runs/{id}/steer — requires runs:write.
	if len(parts) == 2 && parts[1] == "steer" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRunSteer(w, r, runID)
		return
	}

	// GET /v1/runs/{id}/context — requires runs:read.
	if len(parts) == 2 && parts[1] == "context" {
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleRunContext(w, r, runID)
		return
	}

	// POST /v1/runs/{id}/compact — requires runs:write.
	if len(parts) == 2 && parts[1] == "compact" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRunCompact(w, r, runID)
		return
	}

	// GET/PUT /v1/runs/{id}/todos — GET requires runs:read; PUT requires runs:write.
	if len(parts) == 2 && parts[1] == "todos" {
		if r.Method == http.MethodPut {
			if !hasScope(r.Context(), store.ScopeRunsWrite) {
				writeScopeError(w, store.ScopeRunsWrite)
				return
			}
		} else {
			if !hasScope(r.Context(), store.ScopeRunsRead) {
				writeScopeError(w, store.ScopeRunsRead)
				return
			}
		}
		s.handleRunTodos(w, r, runID)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleCancelRun(w, r, runID)
		return
	}

	// POST /v1/runs/{id}/approve — requires runs:write.
	if len(parts) == 2 && parts[1] == "approve" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleApproveRun(w, r, runID)
		return
	}

	// POST /v1/runs/{id}/deny — requires runs:write.
	if len(parts) == 2 && parts[1] == "deny" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleDenyRun(w, r, runID)
		return
	}

	http.NotFound(w, r)
}

// handleCancelRun handles POST /v1/runs/{id}/cancel.
// Requests cooperative cancellation of an active run. If the run is already
// in a terminal state the call is idempotent and returns 200. Unknown run IDs
// return 404.
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	if err := s.runner.CancelRun(runID); err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		writeError(w, http.StatusInternalServerError, "cancel_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelling"})
}

// handleApproveRun handles POST /v1/runs/{id}/approve.
// Approves the pending tool call for the given run, allowing it to execute.
// Returns 404 when no pending approval exists for the run.
func (s *Server) handleApproveRun(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.approvalBroker == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "approval broker is not configured")
		return
	}
	if err := s.approvalBroker.Approve(runID); err != nil {
		if errors.Is(err, harness.ErrNoPendingApproval) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no pending approval for run %q", runID))
			return
		}
		writeError(w, http.StatusInternalServerError, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "approved"})
}

// handleDenyRun handles POST /v1/runs/{id}/deny.
// Denies the pending tool call for the given run; the tool returns a
// permission_denied error to the LLM and the run continues.
// Returns 404 when no pending approval exists for the run.
func (s *Server) handleDenyRun(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.approvalBroker == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "approval broker is not configured")
		return
	}
	if err := s.approvalBroker.Deny(runID); err != nil {
		if errors.Is(err, harness.ErrNoPendingApproval) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no pending approval for run %q", runID))
			return
		}
		writeError(w, http.StatusInternalServerError, "deny_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "denied"})
}

func (s *Server) handleRunSteer(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}

	if err := s.runner.SteerRun(runID, req.Prompt); err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		if errors.Is(err, harness.ErrRunNotActive) {
			writeError(w, http.StatusConflict, "run_not_active", err.Error())
			return
		}
		if errors.Is(err, harness.ErrSteeringBufferFull) {
			writeError(w, http.StatusTooManyRequests, "steering_buffer_full", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

// handleRunContext handles GET /v1/runs/{id}/context.
// Returns the current context window status for a run.
func (s *Server) handleRunContext(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	status, err := s.runner.GetRunContextStatus(runID)
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleRunCompact handles POST /v1/runs/{id}/compact.
// Triggers in-memory context compaction on the active run.
func (s *Server) handleRunCompact(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req struct {
		Mode     string `json:"mode"`
		KeepLast int    `json:"keep_last"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	result, err := s.runner.CompactRun(r.Context(), runID, harness.CompactRunRequest{
		Mode:     req.Mode,
		KeepLast: req.KeepLast,
	})
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		if errors.Is(err, harness.ErrRunNotActive) {
			writeError(w, http.StatusConflict, "run_not_active", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"messages_removed": result.MessagesRemoved,
	})
}

func (s *Server) handleRunContinue(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req struct {
		Prompt       string                    `json:"prompt"`
		AllowedTools *[]string                 `json:"allowed_tools,omitempty"`
		Permissions  *harness.PermissionConfig `json:"permissions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}

	newRun, err := s.runner.ContinueRunWithOptions(runID, harness.ContinueRunRequest{
		Prompt:       req.Prompt,
		AllowedTools: req.AllowedTools,
		Permissions:  req.Permissions,
	})
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		if errors.Is(err, harness.ErrRunNotCompleted) {
			writeError(w, http.StatusConflict, "run_not_completed", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id": newRun.ID,
		"status": newRun.Status,
	})
}

func (s *Server) handleRunInput(w http.ResponseWriter, r *http.Request, runID string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetRunInput(w, runID)
		return
	case http.MethodPost:
		s.handlePostRunInput(w, r, runID)
		return
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleGetRunInput(w http.ResponseWriter, runID string) {
	pending, err := s.runner.PendingInput(runID)
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		if errors.Is(err, harness.ErrNoPendingInput) {
			writeError(w, http.StatusConflict, "no_pending_input", "run is not waiting for user input")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pending)
}

func (s *Server) handlePostRunInput(w http.ResponseWriter, r *http.Request, runID string) {
	var req struct {
		Answers map[string]string `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Answers == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "answers is required")
		return
	}

	err := s.runner.SubmitInput(runID, req.Answers)
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		if errors.Is(err, harness.ErrNoPendingInput) {
			writeError(w, http.StatusConflict, "no_pending_input", "run is not waiting for user input")
			return
		}
		if errors.Is(err, harness.ErrInvalidRunInput) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	// Check the runner's in-memory state first (active or recently completed runs).
	if state, ok := s.runner.GetRun(runID); ok {
		writeJSON(w, http.StatusOK, state)
		return
	}
	// Fall back to the persistent store for completed/historical runs.
	if s.runStore != nil {
		storeRun, err := s.runStore.GetRun(r.Context(), runID)
		if err == nil {
			// Convert store.Run back to a minimal harness.Run-compatible response.
			writeJSON(w, http.StatusOK, storeRunToHarness(storeRun))
			return
		}
		if !store.IsNotFound(err) {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
}

func (s *Server) handleRunSummary(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	summary, err := s.runner.GetRunSummary(runID)
	if err != nil {
		if errors.Is(err, harness.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
			return
		}
		writeError(w, http.StatusConflict, "run_not_finished", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	history, stream, cancel, err := s.runner.Subscribe(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
		return
	}
	defer cancel()

	// Support Last-Event-ID reconnection: skip already-seen events.
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if _, seq, err := harness.ParseEventID(lastID); err == nil {
			if int(seq+1) < len(history) {
				history = history[seq+1:]
			} else {
				history = nil
			}
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "response writer does not support streaming")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, event := range history {
		if err := writeSSE(w, event); err != nil {
			return
		}
		flusher.Flush()
		if harness.IsTerminalEvent(event.Type) {
			return
		}
	}

	ticker := time.NewTicker(sseKeepaliveInterval())
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-stream:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				if errors.Is(err, http.ErrHandlerTimeout) {
					return
				}
				return
			}
			flusher.Flush()
			if harness.IsTerminalEvent(event.Type) {
				return
			}
		case <-ticker.C:
			if err := writeSSEPing(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// harnessRunToStore converts a harness.Run to a store.Run for initial persistence.
// Usage/cost JSON fields are left empty; they are updated via UpdateRun later.
func harnessRunToStore(run harness.Run) *store.Run {
	return &store.Run{
		ID:             run.ID,
		ConversationID: run.ConversationID,
		TenantID:       run.TenantID,
		AgentID:        run.AgentID,
		Model:          run.Model,
		ProviderName:   run.ProviderName,
		Prompt:         run.Prompt,
		Status:         store.RunStatus(run.Status),
		Output:         run.Output,
		Error:          run.Error,
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	}
}

// storeRunToHarness converts a store.Run to a map suitable for JSON response.
// This avoids a circular import between server and harness packages.
func storeRunToHarness(r *store.Run) map[string]any {
	return map[string]any{
		"id":              r.ID,
		"conversation_id": r.ConversationID,
		"tenant_id":       r.TenantID,
		"agent_id":        r.AgentID,
		"model":           r.Model,
		"provider_name":   r.ProviderName,
		"prompt":          r.Prompt,
		"status":          r.Status,
		"output":          r.Output,
		"error":           r.Error,
		"created_at":      r.CreatedAt,
		"updated_at":      r.UpdatedAt,
	}
}
