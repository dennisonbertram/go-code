package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/store"
)

// This file exposes the self-contained relay control plane over HTTP: placement
// routing, contract composition, capability policy evaluation, worker capability
// inventories, and operator visibility. Every handler:
//   - returns 501 not_configured when the control plane is not wired,
//   - enforces scope per-method (reads: runs:read, writes: runs:write),
//   - forces the caller's tenant via effectiveTenantID where a tenant applies.
//
// The transport/command/cloud/handoff pieces of internal/relay are intentionally
// NOT exposed: they presuppose a remote worker runtime this repo does not yet
// provide, so routing them would advertise a non-functional API.

// relayControlConfigured guards every control-plane handler.
func (s *Server) relayControlConfigured(w http.ResponseWriter) bool {
	if s.relayControl == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "relay control plane not configured")
		return false
	}
	return true
}

// handleRelayPlacements handles POST /v1/relay/placements: score and select a
// worker for a placement request, persisting the resulting record so it can be
// explained later.
func (s *Server) handleRelayPlacements(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, "POST")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsWrite) {
		writeScopeError(w, store.ScopeRunsWrite)
		return
	}

	var req relay.PlacementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tenantID, err := s.effectiveTenantID(r, strings.TrimSpace(req.TenantID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.TenantID = tenantID

	record, err := s.relayControl.Router.Place(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "placement_failed", err.Error())
		return
	}
	// Persist the decision for later explanation (GetPlacementExplanation).
	if err := s.relayControl.Events.SavePlacementRecord(r.Context(), record); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// handleRelayContracts handles POST /v1/relay/contracts: compose a validated run
// contract from a compose request.
func (s *Server) handleRelayContracts(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, "POST")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsWrite) {
		writeScopeError(w, store.ScopeRunsWrite)
		return
	}

	var req relay.ComposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tenantID, err := s.effectiveTenantID(r, strings.TrimSpace(req.TenantID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.TenantID = tenantID

	contract, err := s.relayControl.Composer.Compose(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, contract)
}

// relayPolicyRequest is the body for the policy check/filter endpoints.
type relayPolicyRequest struct {
	Pack    relay.CapabilityPack `json:"pack"`
	Context relay.PolicyContext  `json:"policy_context"`
}

// handleRelayPolicyCheck handles POST /v1/relay/policy/check: evaluate a
// capability pack against policy. Pure evaluation over the supplied inputs.
func (s *Server) handleRelayPolicyCheck(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, "POST")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}

	req, ok := s.decodeRelayPolicyRequest(w, r)
	if !ok {
		return
	}
	result := s.relayControl.Policy.Check(r.Context(), &req.Pack, req.Context)
	writeJSON(w, http.StatusOK, result)
}

// handleRelayPolicyFilter handles POST /v1/relay/policy/filter: return a copy of
// the pack with policy-denied capabilities removed, plus the decision detail.
func (s *Server) handleRelayPolicyFilter(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, "POST")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}

	req, ok := s.decodeRelayPolicyRequest(w, r)
	if !ok {
		return
	}
	filtered, result := s.relayControl.Policy.FilterPack(r.Context(), &req.Pack, req.Context)
	writeJSON(w, http.StatusOK, map[string]any{
		"filtered_pack": filtered,
		"result":        result,
	})
}

// decodeRelayPolicyRequest decodes the shared policy request body and forces the
// caller's tenant onto the policy context. Returns ok=false after writing an
// error response if decoding or tenant resolution fails.
func (s *Server) decodeRelayPolicyRequest(w http.ResponseWriter, r *http.Request) (relayPolicyRequest, bool) {
	var req relayPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return req, false
	}
	tenantID, err := s.effectiveTenantID(r, strings.TrimSpace(req.Context.TenantID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return req, false
	}
	req.Context.TenantID = tenantID
	return req, true
}

// handleRelayOperatorWorkers handles GET /v1/relay/operator/workers: a
// security-redacted operator view of workers for the caller's tenant.
func (s *Server) handleRelayOperatorWorkers(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "GET")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}

	tenantID, err := s.effectiveTenantID(r, r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	summaries, err := s.relayControl.Operator.ListWorkerSummaries(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": summaries})
}

// handleRelayCapabilitiesByWorker handles GET/PUT/DELETE
// /v1/relay/capabilities/{workerID}: read (sanitized), replace, or delete a
// worker's capability inventory. Access is gated by worker tenant visibility.
func (s *Server) handleRelayCapabilitiesByWorker(w http.ResponseWriter, r *http.Request) {
	if !s.relayControlConfigured(w) {
		return
	}
	if s.relayWorkerStore == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "relay worker store not configured")
		return
	}

	workerID := strings.TrimPrefix(r.URL.Path, "/v1/relay/capabilities/")
	if workerID == "" || strings.Contains(workerID, "/") {
		http.NotFound(w, r)
		return
	}

	// Confirm the worker exists and is visible to the caller's tenant before
	// exposing or mutating its capabilities (relayWorkerVisibleToRequest writes
	// a 404 on mismatch to avoid leaking existence).
	worker, err := s.relayWorkerStore.GetWorker(r.Context(), workerID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "worker not found")
		return
	}
	if !s.relayWorkerVisibleToRequest(w, r, worker, workerID) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		inv, err := s.relayControl.Operator.GetWorkerCapabilities(r.Context(), workerID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, inv)

	case http.MethodPut:
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		var inv relay.CapabilityInventory
		if err := json.NewDecoder(r.Body).Decode(&inv); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		inv.WorkerID = workerID // the path is authoritative
		if err := s.relayControl.Capabilities.SetInventory(r.Context(), &inv); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "worker_id": workerID})

	case http.MethodDelete:
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		if err := s.relayControl.Capabilities.DeleteInventory(r.Context(), workerID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "worker_id": workerID})

	default:
		writeMethodNotAllowed(w, "GET, PUT, DELETE")
	}
}
