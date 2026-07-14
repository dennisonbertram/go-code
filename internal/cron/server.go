package cron

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	robfigcron "github.com/robfig/cron/v3"
)

// Server provides REST API handlers for cron job management.
type Server struct {
	store     Store
	scheduler *Scheduler
	clock     Clock
}

// NewServer creates an http.Handler with cron API routes.
func NewServer(store Store, scheduler *Scheduler, clock Clock) http.Handler {
	s := &Server{store: store, scheduler: scheduler, clock: clock}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/jobs", s.handleJobs)
	mux.HandleFunc("/v1/jobs/", s.handleJobByID)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListJobs(w, r)
	case http.MethodPost:
		s.handleCreateJob(w, r)
	default:
		writeMethodNotAllowed(w, "GET, POST")
	}
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	var req CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "name is required")
		return
	}

	if req.Schedule == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "schedule is required")
		return
	}

	nextRun, err := NextRunTime(req.Schedule, s.clock.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", fmt.Sprintf("invalid schedule: %v", err))
		return
	}

	if req.ExecType != ExecTypeShell && req.ExecType != ExecTypeHarness {
		writeError(w, http.StatusBadRequest, "validation_error", "execution_type must be \"shell\" or \"harness\"")
		return
	}

	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 30
	}

	now := s.clock.Now()
	job := Job{
		ID:         uuid.New().String(),
		TenantID:   req.TenantID,
		Name:       req.Name,
		Schedule:   req.Schedule,
		ExecType:   req.ExecType,
		ExecConfig: req.ExecConfig,
		Status:     StatusActive,
		TimeoutSec: req.TimeoutSec,
		Tags:       req.Tags,
		NextRunAt:  nextRun,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	job, err = s.store.CreateJob(r.Context(), job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	if addErr := s.scheduler.AddJob(job); addErr != nil {
		writeError(w, http.StatusInternalServerError, "scheduler_error", addErr.Error())
		return
	}

	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if jobs == nil {
		jobs = []Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/jobs/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 2 && parts[1] == "history" {
		s.handleHistory(w, r, id)
		return
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetJob(w, r, id)
	case http.MethodPatch:
		s.handleUpdateJob(w, r, id)
	case http.MethodDelete:
		s.handleDeleteJob(w, r, id)
	default:
		writeMethodNotAllowed(w, "GET, PATCH, DELETE")
	}
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if !IsJobNotFound(err) {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		// Try by name.
		job, err = s.store.GetJobByName(r.Context(), id)
		if err != nil {
			if !IsJobNotFound(err) {
				writeError(w, http.StatusInternalServerError, "store_error", err.Error())
				return
			}
			writeError(w, http.StatusNotFound, "not_found", "job not found")
			return
		}
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleUpdateJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if !IsJobNotFound(err) {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	var req UpdateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if req.Schedule != nil {
		trimmed := strings.TrimSpace(*req.Schedule)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "validation_error", "schedule must not be empty")
			return
		}
		nextRun, err := NextRunTime(*req.Schedule, s.clock.Now())
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", fmt.Sprintf("invalid schedule: %v", err))
			return
		}
		job.Schedule = *req.Schedule
		job.NextRunAt = nextRun
	}
	if req.ExecConfig != nil {
		job.ExecConfig = *req.ExecConfig
	}
	if req.TimeoutSec != nil {
		job.TimeoutSec = *req.TimeoutSec
	}
	if req.Tags != nil {
		job.Tags = *req.Tags
	}

	if req.Status != nil {
		if *req.Status != StatusActive && *req.Status != StatusPaused {
			writeError(w, http.StatusBadRequest, "validation_error", "status must be \"active\" or \"paused\"")
			return
		}
		oldStatus := job.Status
		job.Status = *req.Status

		if *req.Status == StatusPaused && oldStatus != StatusPaused {
			s.scheduler.RemoveJob(job.ID)
		}
		if *req.Status == StatusActive && oldStatus != StatusActive {
			if addErr := s.scheduler.AddJob(job); addErr != nil {
				writeError(w, http.StatusInternalServerError, "scheduler_error", addErr.Error())
				return
			}
		}
	}

	// Gate on job.Status (the EFFECTIVE post-update status), not on
	// req.Status (the raw request field). A schedule-only PATCH
	// (req.Status == nil) must not re-arm a job whose stored status is
	// paused: job.Status already reflects that live status in that case.
	// For a resume+schedule PATCH, the status block above already set
	// job.Status = StatusActive, so this still correctly re-arms
	// genuinely-active jobs.
	if req.Schedule != nil && job.Status == StatusActive {
		if err := s.scheduler.UpdateJobSchedule(job); err != nil {
			writeError(w, http.StatusInternalServerError, "scheduler_error", err.Error())
			return
		}
	}

	job.UpdatedAt = s.clock.Now()
	if err := s.store.UpdateJob(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.DeleteJob(r.Context(), id); err != nil {
		if IsJobNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	s.scheduler.RemoveJob(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "GET")
		return
	}

	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	execs, err := s.store.ListExecutions(r.Context(), jobID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if execs == nil {
		execs = []Execution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": execs})
}

func NextRunTime(schedule string, from time.Time) (time.Time, error) {
	parser := robfigcron.NewParser(robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow)
	sched, err := parser.Parse(schedule)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
