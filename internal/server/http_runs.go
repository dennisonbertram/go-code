package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		writeJSONDecodeError(w, err)
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
	// Enforce tenant isolation: only return runs that belong to the caller's
	// authenticated tenant. This prevents cross-tenant enumeration of a
	// conversation's run history via a known (or guessed) conversation ID.
	effectiveTenant, err := s.effectiveTenantID(r, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	filter := store.RunFilter{
		ConversationID: conversationID,
		TenantID:       effectiveTenant,
	}
	runs, err := s.runStore.ListRuns(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// runTenantMismatch reports whether a per-run by-ID request must be blocked
// because the run belongs to a tenant other than the caller's.
//
// It enforces tenant ownership on the by-ID routes (GET /v1/runs/{id} and the
// /{id}/{cancel,steer,continue,compact,input,approve,deny,events,summary,context,
// todos} sub-routes), which already enforce scope but not ownership.
//
// Resolution order mirrors handleGetRun: the runner's in-memory state first
// (active / recently completed runs), then the persistent store (historical
// runs). The run's stored TenantID is compared to the caller's authenticated
// tenant.
//
//   - Auth disabled or no store configured → never gates (returns false),
//     preserving unauthenticated / no-persistence behavior.
//   - Run found and its (normalized) stored tenant differs from the caller's
//     (normalized) tenant → gate (returns true). This is the real cross-tenant
//     attack surface: under auth, every HTTP-created run is stamped with its
//     creator's tenant by handlePostRun, so a mismatch always means the run
//     belongs to a different tenant.
//   - Run not found in either source → returns false so the downstream handler
//     produces its own (also-404) not-found response unchanged.
//
// Tenant values are normalized the same way the runner normalizes them ("" →
// "default", see Runner.checkConversationOwnership): the runner rewrites an
// empty request tenant to "default" on StartRun, so an empty-tenant caller (or
// an out-of-band run with no tenant) compares equal to "default" rather than
// being spuriously blocked. This secure default still blocks every genuine
// cross-tenant access because the two distinct tenants normalize to distinct,
// non-equal values.
func (s *Server) runTenantMismatch(r *http.Request, runID string) bool {
	// When auth is disabled or there is no store, there is no tenant to enforce.
	if s.authDisabled || s.runStore == nil {
		return false
	}

	caller := normalizeTenant(TenantIDFromContext(r.Context()))

	// In-memory state first: covers active and recently completed runs.
	if run, ok := s.runner.GetRun(runID); ok {
		return normalizeTenant(run.TenantID) != caller
	}

	// Fall back to the persistent store for historical runs.
	storeRun, err := s.runStore.GetRun(r.Context(), runID)
	if err == nil && storeRun != nil {
		return normalizeTenant(storeRun.TenantID) != caller
	}

	// Unknown run: do not gate here — let the handler return its own not-found.
	return false
}

// normalizeTenant maps the empty tenant to "default", matching the runner's
// tenant normalization (Runner.checkConversationOwnership). This keeps "" and
// "default" interchangeable when comparing run ownership.
func normalizeTenant(t string) string {
	if t == "" {
		return "default"
	}
	return t
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
			return
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
			return
		}
		s.handleRunTodos(w, r, runID)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
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
		if s.blockCrossTenant(w, r, runID) {
			return
		}
		s.handleDenyRun(w, r, runID)
		return
	}

	http.NotFound(w, r)
}

// blockCrossTenant enforces tenant ownership on per-run by-ID routes. It is
// called AFTER the per-route scope check (so an under-scoped caller still sees
// 403, not 404). When the run belongs to a different tenant it writes a 404
// not_found response — matching the unknown-id contract so the existence of
// another tenant's run is never revealed — and returns true to signal the
// caller to stop. Returns false (no response written) when the request may
// proceed. See runTenantMismatch for the ownership semantics.
func (s *Server) blockCrossTenant(w http.ResponseWriter, r *http.Request, runID string) bool {
	if s.runTenantMismatch(r, runID) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("run %q not found", runID))
		return true
	}
	return false
}

// conversationTenantMismatch reports whether a conversation sub-resource request
// must be blocked because the conversation belongs to a tenant other than the
// caller's.
//
// Resolution order (fail-closed when auth is enabled):
//  1. Run store: list runs for the conversation; the run's TenantID is
//     authoritative. Same-tenant → proceed. Cross-tenant → block.
//  2. Conversation store: when no runs are found, fall back to the conversation
//     store's own tenant_id column. This handles conversations that exist in the
//     store but have no persisted run (e.g. conversations loaded from history
//     before run persistence was enabled, or conversations stored directly).
//     If the conversation record exists with a non-empty tenant that differs
//     from the caller → block (fail-closed). If the conversation record has an
//     empty tenant (legacy row without tenant stamp) → treat as same-tenant
//     (preserve prior behavior for pre-tenant-stamp rows).
//  3. Conversation not found in either source → let the downstream handler
//     produce its own not-found response (return false, no gate).
//
// Auth disabled or no store → never gates (returns false).
func (s *Server) conversationTenantMismatch(r *http.Request, conversationID string) bool {
	if s.authDisabled || s.runStore == nil {
		return false
	}
	caller := normalizeTenant(TenantIDFromContext(r.Context()))

	// 1. Try the run store: list runs for this conversation without a tenant
	// filter so we can read the stored owner unconditionally.
	runs, err := s.runStore.ListRuns(r.Context(), store.RunFilter{ConversationID: conversationID})
	if err == nil && len(runs) > 0 {
		return normalizeTenant(runs[0].TenantID) != caller
	}

	// 2. No runs found (or store error). Fall back to the conversation store's
	// own tenant_id column so a runless-but-stored conversation is not ungated.
	if convStore := s.runner.GetConversationStore(); convStore != nil {
		owner, err := convStore.GetConversationOwner(r.Context(), conversationID)
		if err == nil && owner != nil {
			// A row exists in the conversation store.
			if owner.TenantID == "" {
				// Legacy row with no tenant stamp: do not block (preserve prior behavior).
				return false
			}
			return normalizeTenant(owner.TenantID) != caller
		}
	}

	// 3. Conversation not found in any source: let the downstream handler
	// produce its own not-found response.
	return false
}

// blockConversationCrossTenant enforces tenant ownership on per-conversation
// sub-resource routes (messages, export, compact, delete). It writes a 404
// response and returns true when the conversation belongs to a different tenant.
// Returns false (no response written) when the request may proceed.
func (s *Server) blockConversationCrossTenant(w http.ResponseWriter, r *http.Request, convID string) bool {
	if s.conversationTenantMismatch(r, convID) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("conversation %q not found", convID))
		return true
	}
	return false
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
// The optional JSON body {"option": "<id>"} records the operator's selected
// plan approach option for plan-exit approvals; an absent or unknown option
// ID falls back to a plain approve.
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
	var body struct {
		Option string `json:"option"`
	}
	if r.Body != nil {
		// An empty body is a plain approve; a malformed body is a client error.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("invalid approve body: %v", err))
			return
		}
	}
	approveErr := error(nil)
	if option := strings.TrimSpace(body.Option); option != "" {
		// Validate the requested option against the pending approval's options;
		// an unknown ID falls back to a plain approve.
		valid := false
		if pending, ok := s.approvalBroker.Pending(runID); ok {
			for _, opt := range pending.Options {
				if opt.ID == option {
					valid = true
					break
				}
			}
		}
		if valid {
			approveErr = s.approvalBroker.ApproveWithOption(runID, option)
		} else {
			approveErr = s.approvalBroker.Approve(runID)
		}
	} else {
		approveErr = s.approvalBroker.Approve(runID)
	}
	if approveErr != nil {
		if errors.Is(approveErr, harness.ErrNoPendingApproval) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no pending approval for run %q", runID))
			return
		}
		writeError(w, http.StatusInternalServerError, "approve_failed", approveErr.Error())
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
		Mode        string `json:"mode"`
		KeepLast    int    `json:"keep_last"`
		Instruction string `json:"instruction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	result, err := s.runner.CompactRun(r.Context(), runID, harness.CompactRunRequest{
		Mode:        req.Mode,
		KeepLast:    req.KeepLast,
		Instruction: req.Instruction,
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
		"mode":             result.Mode,
		"summary":          result.Summary,
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
	//
	// seq is a uint64 (harness.ParseEventID parses it via strconv.ParseUint),
	// so a crafted huge value (e.g. near math.MaxInt64 or math.MaxUint64) must
	// never be used to compute seq+1 and slice history directly — converting
	// such a value to int can wrap to a negative number, and slicing with the
	// raw uint64 value can panic with "slice bounds out of range" (a one-header
	// remote DoS). We therefore only ever slice when seq is already known to be
	// a valid, in-range index (seq < len(history)), which guarantees seq+1 fits
	// safely within len(history) and cannot overflow.
	//
	// Any out-of-range sequence (seq >= len(history), including adversarially
	// huge values) is treated the same as an unparseable Last-Event-ID: fall
	// back to a full replay rather than guessing, silently dropping events, or
	// panicking. This also avoids a separate hang: nil-ing out history for an
	// out-of-range seq on an already-completed run would skip the terminal
	// event in the replay loop below and fall through to the live-event wait,
	// which never fires for a run that has already finished.
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if _, seq, err := harness.ParseEventID(lastID); err == nil {
			historyLen := uint64(len(history))
			if seq < historyLen {
				history = history[seq+1:]
			}
			// seq >= historyLen: leave history as the full replay (no-op).
		}
		// Unparseable Last-Event-ID: leave history as the full replay (no-op).
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
		"recap":           r.Recap,
		"created_at":      r.CreatedAt,
		"updated_at":      r.UpdatedAt,
	}
}
