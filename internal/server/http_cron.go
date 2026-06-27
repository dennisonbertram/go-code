package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/store"
)

// handleCronJobsRoot handles GET /v1/cron/jobs and POST /v1/cron/jobs.
func (s *Server) handleCronJobsRoot(w http.ResponseWriter, r *http.Request) {
	if s.cronClient == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cron not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		// GET /v1/cron/jobs — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleCronListJobs(w, r)
	case http.MethodPost:
		// POST /v1/cron/jobs — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleCronCreateJob(w, r)
	default:
		writeMethodNotAllowed(w, "GET, POST")
	}
}

// handleCronJobByID handles all /v1/cron/jobs/{id} and sub-path requests.
func (s *Server) handleCronJobByID(w http.ResponseWriter, r *http.Request) {
	if s.cronClient == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "cron not configured")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/cron/jobs/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	if len(parts) == 2 {
		switch parts[1] {
		case "pause":
			// POST /v1/cron/jobs/{id}/pause — requires runs:write
			if !hasScope(r.Context(), store.ScopeRunsWrite) {
				writeScopeError(w, store.ScopeRunsWrite)
				return
			}
			s.handleCronPauseJob(w, r, id)
		case "resume":
			// POST /v1/cron/jobs/{id}/resume — requires runs:write
			if !hasScope(r.Context(), store.ScopeRunsWrite) {
				writeScopeError(w, store.ScopeRunsWrite)
				return
			}
			s.handleCronResumeJob(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		// GET /v1/cron/jobs/{id} — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleCronGetJob(w, r, id)
	case http.MethodPatch:
		// PATCH /v1/cron/jobs/{id} — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleCronUpdateJob(w, r, id)
	case http.MethodDelete:
		// DELETE /v1/cron/jobs/{id} — requires runs:write
		if !hasScope(r.Context(), store.ScopeRunsWrite) {
			writeScopeError(w, store.ScopeRunsWrite)
			return
		}
		s.handleCronDeleteJob(w, r, id)
	default:
		writeMethodNotAllowed(w, "GET, PATCH, DELETE")
	}
}

// handleCronListJobs handles GET /v1/cron/jobs.
func (s *Server) handleCronListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.cronClient.ListJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	jobs = filterCronJobsByTenant(jobs, TenantIDFromContext(r.Context()))
	if jobs == nil {
		jobs = []tools.CronJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handleCronCreateJob handles POST /v1/cron/jobs.
func (s *Server) handleCronCreateJob(w http.ResponseWriter, r *http.Request) {
	var req tools.CronCreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if req.Schedule == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "schedule is required")
		return
	}
	req.TenantID = TenantIDFromContext(r.Context())

	job, err := s.cronClient.CreateJob(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

// handleCronGetJob handles GET /v1/cron/jobs/{id}.
func (s *Server) handleCronGetJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := s.cronJobForTenant(r.Context(), id)
	if err != nil {
		writeCronJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleCronUpdateJob handles PATCH /v1/cron/jobs/{id}.
func (s *Server) handleCronUpdateJob(w http.ResponseWriter, r *http.Request, id string) {
	var req tools.CronUpdateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if _, err := s.cronJobForTenant(r.Context(), id); err != nil {
		writeCronJobError(w, err)
		return
	}
	job, err := s.cronClient.UpdateJob(r.Context(), id, req)
	if err != nil {
		writeCronJobError(w, err)
		return
	}
	if !cronJobVisibleToTenant(job, TenantIDFromContext(r.Context())) {
		writeCronJobError(w, tools.ErrCronJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleCronDeleteJob handles DELETE /v1/cron/jobs/{id}.
func (s *Server) handleCronDeleteJob(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.cronJobForTenant(r.Context(), id); err != nil {
		writeCronJobError(w, err)
		return
	}
	if err := s.cronClient.DeleteJob(r.Context(), id); err != nil {
		writeCronJobError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCronPauseJob handles POST /v1/cron/jobs/{id}/pause.
func (s *Server) handleCronPauseJob(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if _, err := s.cronJobForTenant(r.Context(), id); err != nil {
		writeCronJobError(w, err)
		return
	}
	paused := "paused"
	job, err := s.cronClient.UpdateJob(r.Context(), id, tools.CronUpdateJobRequest{
		Status: &paused,
	})
	if err != nil {
		writeCronJobError(w, err)
		return
	}
	if !cronJobVisibleToTenant(job, TenantIDFromContext(r.Context())) {
		writeCronJobError(w, tools.ErrCronJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleCronResumeJob handles POST /v1/cron/jobs/{id}/resume.
func (s *Server) handleCronResumeJob(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if _, err := s.cronJobForTenant(r.Context(), id); err != nil {
		writeCronJobError(w, err)
		return
	}
	active := "active"
	job, err := s.cronClient.UpdateJob(r.Context(), id, tools.CronUpdateJobRequest{
		Status: &active,
	})
	if err != nil {
		writeCronJobError(w, err)
		return
	}
	if !cronJobVisibleToTenant(job, TenantIDFromContext(r.Context())) {
		writeCronJobError(w, tools.ErrCronJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) cronJobForTenant(ctx context.Context, id string) (tools.CronJob, error) {
	job, err := s.cronClient.GetJob(ctx, id)
	if err != nil {
		return tools.CronJob{}, err
	}
	if !cronJobVisibleToTenant(job, TenantIDFromContext(ctx)) {
		return tools.CronJob{}, tools.ErrCronJobNotFound
	}
	return job, nil
}

func filterCronJobsByTenant(jobs []tools.CronJob, tenantID string) []tools.CronJob {
	if tenantID == "" {
		return jobs
	}
	filtered := make([]tools.CronJob, 0, len(jobs))
	for _, job := range jobs {
		if job.TenantID == tenantID {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func cronJobVisibleToTenant(job tools.CronJob, tenantID string) bool {
	return tenantID == "" || job.TenantID == tenantID
}

func writeCronJobError(w http.ResponseWriter, err error) {
	if errors.Is(err, tools.ErrCronJobNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}
