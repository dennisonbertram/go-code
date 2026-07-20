package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
)

// Task type values returned by GET /v1/tasks.
const (
	TaskTypeSubagent = "subagent"
	TaskTypeCron     = "cron"
	TaskTypeCallback = "callback"
	TaskTypeBashJob  = "bash_job"
)

// Task action values advertise which stop/control operations the client can
// invoke for a row. They map onto existing per-type endpoints
// (POST /v1/subagents/{id}/cancel, DELETE /v1/cron/jobs/{id}, ...).
const (
	TaskActionCancel = "cancel"
	TaskActionDelete = "delete"
	TaskActionPause  = "pause"
	TaskActionResume = "resume"
)

// CallbackLister enumerates delayed callbacks across all conversations for
// the tasks union. *tools.CallbackManager satisfies it via ListAll.
type CallbackLister interface {
	ListAll() []tools.CallbackInfo
}

// Task is the unified DTO returned by GET /v1/tasks. It captures one piece of
// background work — a managed subagent, a cron job, or a pending delayed
// callback — with the fields the /tasks panel needs to render a row.
type Task struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	Label      string    `json:"label"`
	StartedAt  time.Time `json:"started_at"`
	AgeSeconds int64     `json:"age_seconds"`
	Actions    []string  `json:"actions"`
}

// handleTasks serves GET /v1/tasks: a union of every daemon-reachable piece of
// background work (subagents, cron jobs, pending delayed callbacks). Sources
// that are not configured are skipped, so an unconfigured daemon returns an
// empty list rather than an error. Requires runs:read, matching /v1/subagents
// and /v1/cron/jobs.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}

	tasks := make([]Task, 0)
	now := s.timeNow()

	if s.subagentManager != nil {
		items, err := s.subagentManager.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		for _, item := range s.filterSubagentsByTenant(r, items) {
			tasks = append(tasks, taskFromSubagent(item, now))
		}
	}

	if s.cronClient != nil {
		jobs, err := s.cronClient.ListJobs(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		for _, job := range filterCronJobsByTenant(jobs, TenantIDFromContext(r.Context())) {
			tasks = append(tasks, taskFromCronJob(job, now))
		}
	}

	if s.callbackLister != nil {
		caller := TenantIDFromContext(r.Context())
		for _, info := range s.callbackLister.ListAll() {
			// Mirror filterCronJobsByTenant: an empty caller tenant (auth
			// disabled) sees everything; otherwise exact tenant match.
			if caller != "" && info.TenantID != caller {
				continue
			}
			tasks = append(tasks, taskFromCallback(info, now))
		}
	}

	if s.jobTracker != nil {
		caller := TenantIDFromContext(r.Context())
		for _, tj := range s.jobTracker.List() {
			// Same tenant rule as callbacks/cron above.
			if caller != "" && tj.Info.TenantID != caller {
				continue
			}
			tasks = append(tasks, taskFromBashJob(tj, now))
		}
	}

	// Deterministic ordering: oldest first, ties broken by type then id, so
	// clients and tests see a stable list regardless of map iteration order.
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].StartedAt.Equal(tasks[j].StartedAt) {
			return tasks[i].StartedAt.Before(tasks[j].StartedAt)
		}
		if tasks[i].Type != tasks[j].Type {
			return tasks[i].Type < tasks[j].Type
		}
		return tasks[i].ID < tasks[j].ID
	})

	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// taskFromSubagent maps a managed subagent onto the unified DTO. Active
// subagents can be cancelled; terminal ones can be deleted.
func taskFromSubagent(item subagents.Subagent, now time.Time) Task {
	label := item.BranchName
	if label == "" {
		label = item.RunID
	}
	actions := []string{TaskActionDelete}
	switch item.Status {
	case harness.RunStatusQueued, harness.RunStatusRunning, harness.RunStatusWaitingForUser, harness.RunStatusWaitingForApproval:
		actions = []string{TaskActionCancel}
	}
	return Task{
		ID:         item.ID,
		Type:       TaskTypeSubagent,
		Status:     string(item.Status),
		Label:      label,
		StartedAt:  item.CreatedAt,
		AgeSeconds: taskAgeSeconds(item.CreatedAt, now),
		Actions:    actions,
	}
}

// taskFromCronJob maps a cron job onto the unified DTO. Active jobs can be
// paused, paused jobs resumed; every job can be deleted.
func taskFromCronJob(job tools.CronJob, now time.Time) Task {
	actions := []string{TaskActionDelete}
	switch job.Status {
	case "active":
		actions = []string{TaskActionPause, TaskActionDelete}
	case "paused":
		actions = []string{TaskActionResume, TaskActionDelete}
	}
	return Task{
		ID:         job.ID,
		Type:       TaskTypeCron,
		Status:     job.Status,
		Label:      job.Name,
		StartedAt:  job.CreatedAt,
		AgeSeconds: taskAgeSeconds(job.CreatedAt, now),
		Actions:    actions,
	}
}

// taskFromCallback maps a pending delayed callback onto the unified DTO.
// CallbackManager.ListAll only returns pending callbacks, so the action set
// is always [cancel].
func taskFromCallback(info tools.CallbackInfo, now time.Time) Task {
	return Task{
		ID:         info.ID,
		Type:       TaskTypeCallback,
		Status:     string(info.State),
		Label:      info.Prompt,
		StartedAt:  info.CreatedAt,
		AgeSeconds: taskAgeSeconds(info.CreatedAt, now),
		Actions:    []string{TaskActionCancel},
	}
}

// taskFromBashJob maps a tracked background bash job onto the unified DTO.
// Running jobs can be cancelled (via POST /v1/jobs/{id}/kill); finished jobs
// offer no actions — the JobManager TTL reclaims them.
func taskFromBashJob(tj harness.TrackedJob, now time.Time) Task {
	actions := []string{}
	if tj.Info.Running {
		actions = []string{TaskActionCancel}
	}
	return Task{
		ID:         tj.TaskID,
		Type:       TaskTypeBashJob,
		Status:     tj.Info.Status(),
		Label:      tj.Info.Command,
		StartedAt:  tj.Info.StartedAt,
		AgeSeconds: taskAgeSeconds(tj.Info.StartedAt, now),
		Actions:    actions,
	}
}

// handleJobByID serves POST /v1/jobs/{id}/kill, the daemon-side kill path for
// background bash jobs (epic #814 slice 2). The {id} is the namespaced task
// ID from GET /v1/tasks ("<managerRef>:<shellID>"). Requires runs:write,
// matching the mutating subagent/cron routes. Cross-tenant kills are 404
// (not 403), matching subagent cancel semantics.
func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if s.jobTracker == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "job tracker is not configured")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "kill" {
		writeError(w, http.StatusNotFound, "not_found", "job action not found")
		return
	}
	id := parts[0]
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsWrite) {
		writeScopeError(w, store.ScopeRunsWrite)
		return
	}
	// Tenant scoping: when auth is enabled, the caller may only kill their own
	// tenant's jobs. Mirrors the list filter in handleTasks.
	if caller := TenantIDFromContext(r.Context()); caller != "" {
		tj, ok := s.jobTracker.Get(id)
		if !ok || tj.Info.TenantID != caller {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("job %q not found", id))
			return
		}
	}
	if err := s.jobTracker.Kill(id); err != nil {
		if errors.Is(err, harness.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("job %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "kill_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "killed": true})
}

// taskAgeSeconds computes a non-negative age in whole seconds. Clock skew or
// zero timestamps clamp to 0 rather than producing negative ages.
func taskAgeSeconds(started, now time.Time) int64 {
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return int64(now.Sub(started).Seconds())
}
