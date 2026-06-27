package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/store"
)

// handleRelayWorkersRoot handles GET /v1/relay/workers and POST /v1/relay/workers.
func (s *Server) handleRelayWorkersRoot(w http.ResponseWriter, r *http.Request) {
	if s.relayWorkerStore == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "relay worker store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleRelayListWorkers(w, r)
	case http.MethodPost:
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRelayRegisterWorker(w, r)
	default:
		writeMethodNotAllowed(w, "GET, POST")
	}
}

// handleRelayWorkerByID handles GET/PUT/DELETE /v1/relay/workers/{id}
// and POST /v1/relay/workers/{id}/heartbeat.
func (s *Server) handleRelayWorkerByID(w http.ResponseWriter, r *http.Request) {
	if s.relayWorkerStore == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "relay worker store not configured")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/relay/workers/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	if len(parts) == 2 && parts[1] == "heartbeat" {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, "POST")
			return
		}
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRelayWorkerHeartbeat(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleRelayGetWorker(w, r, id)
	case http.MethodPut:
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRelayUpdateWorker(w, r, id)
	case http.MethodDelete:
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleRelayDeleteWorker(w, r, id)
	default:
		writeMethodNotAllowed(w, "GET, PUT, DELETE")
	}
}

// handleRelayListWorkers handles GET /v1/relay/workers.
func (s *Server) handleRelayListWorkers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := relay.WorkerFilter{
		TenantID:     q.Get("tenant_id"),
		Status:       relay.WorkerStatus(q.Get("status")),
		LocationType: relay.LocationType(q.Get("location_type")),
		TrustTier:    relay.TrustTier(q.Get("trust_tier")),
	}

	workers, err := s.relayWorkerStore.ListWorkers(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workers": workers,
	})
}

// handleRelayRegisterWorker handles POST /v1/relay/workers.
func (s *Server) handleRelayRegisterWorker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID                      string            `json:"id"`
		TenantID                string            `json:"tenant_id"`
		Name                    string            `json:"name"`
		LocationType            string            `json:"location_type"`
		TrustTier               string            `json:"trust_tier"`
		Labels                  map[string]string `json:"labels"`
		SupportedWorkspaceModes []string          `json:"supported_workspace_modes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "id is required")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if req.TenantID == "" {
		req.TenantID = "default"
	}

	locationType := relay.LocationType(req.LocationType)
	if locationType == "" {
		locationType = relay.LocationLocal
	}
	if err := relay.ValidateLocationType(locationType); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	trustTier := relay.TrustTier(req.TrustTier)
	if trustTier == "" {
		trustTier = relay.TrustTierStandard
	}
	if err := relay.ValidateTrustTier(trustTier); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	now := time.Now()
	worker := &relay.Worker{
		ID:                      req.ID,
		TenantID:                req.TenantID,
		Name:                    req.Name,
		LocationType:            locationType,
		Status:                  relay.WorkerStatusOnline,
		TrustTier:               trustTier,
		Load:                    0,
		Labels:                  req.Labels,
		SupportedWorkspaceModes: req.SupportedWorkspaceModes,
		LastHeartbeat:           now,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if worker.Labels == nil {
		worker.Labels = make(map[string]string)
	}
	if worker.SupportedWorkspaceModes == nil {
		worker.SupportedWorkspaceModes = []string{}
	}

	if err := s.relayWorkerStore.RegisterWorker(r.Context(), worker); err != nil {
		if err == relay.ErrWorkerAlreadyExists {
			writeError(w, http.StatusConflict, "worker_exists", "worker already exists: "+req.ID)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, worker)
}

// handleRelayGetWorker handles GET /v1/relay/workers/{id}.
func (s *Server) handleRelayGetWorker(w http.ResponseWriter, r *http.Request, id string) {
	worker, err := s.relayWorkerStore.GetWorker(r.Context(), id)
	if err != nil {
		if err == relay.ErrWorkerNotFound {
			writeError(w, http.StatusNotFound, "worker_not_found", "worker not found: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

// handleRelayUpdateWorker handles PUT /v1/relay/workers/{id}.
func (s *Server) handleRelayUpdateWorker(w http.ResponseWriter, r *http.Request, id string) {
	existing, err := s.relayWorkerStore.GetWorker(r.Context(), id)
	if err != nil {
		if err == relay.ErrWorkerNotFound {
			writeError(w, http.StatusNotFound, "worker_not_found", "worker not found: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	var req struct {
		Name                    *string            `json:"name"`
		LocationType            *string            `json:"location_type"`
		TrustTier               *string            `json:"trust_tier"`
		Status                  *string            `json:"status"`
		Labels                  *map[string]string `json:"labels"`
		SupportedWorkspaceModes *[]string          `json:"supported_workspace_modes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.LocationType != nil {
		lt := relay.LocationType(*req.LocationType)
		if err := relay.ValidateLocationType(lt); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		existing.LocationType = lt
	}
	if req.TrustTier != nil {
		tt := relay.TrustTier(*req.TrustTier)
		if err := relay.ValidateTrustTier(tt); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		existing.TrustTier = tt
	}
	if req.Status != nil {
		st := relay.WorkerStatus(*req.Status)
		switch st {
		case relay.WorkerStatusOnline, relay.WorkerStatusOffline, relay.WorkerStatusStale, relay.WorkerStatusDraining:
			existing.Status = st
		default:
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid status: "+*req.Status)
			return
		}
	}
	if req.Labels != nil {
		existing.Labels = *req.Labels
	}
	if req.SupportedWorkspaceModes != nil {
		existing.SupportedWorkspaceModes = *req.SupportedWorkspaceModes
	}

	existing.UpdatedAt = time.Now()

	if err := s.relayWorkerStore.UpdateWorker(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, existing)
}

// handleRelayDeleteWorker handles DELETE /v1/relay/workers/{id}.
func (s *Server) handleRelayDeleteWorker(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.relayWorkerStore.DeleteWorker(r.Context(), id); err != nil {
		if err == relay.ErrWorkerNotFound {
			writeError(w, http.StatusNotFound, "worker_not_found", "worker not found: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleRelayWorkerHeartbeat handles POST /v1/relay/workers/{id}/heartbeat.
func (s *Server) handleRelayWorkerHeartbeat(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Load   int    `json:"load"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	status := relay.WorkerStatus(req.Status)
	switch status {
	case relay.WorkerStatusOnline, relay.WorkerStatusDraining:
		// valid heartbeat statuses
	case "":
		status = relay.WorkerStatusOnline
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "heartbeat status must be 'online' or 'draining'")
		return
	}

	hb := relay.Heartbeat{
		WorkerID:  id,
		Timestamp: time.Now(),
		Load:      req.Load,
		Status:    status,
	}

	if err := s.relayWorkerStore.RecordHeartbeat(r.Context(), hb); err != nil {
		if err == relay.ErrWorkerNotFound {
			writeError(w, http.StatusNotFound, "worker_not_found", "worker not found: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
