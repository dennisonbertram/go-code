package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/trigger"
)

// handleExternalTrigger handles POST /v1/external/trigger.
//
// It accepts a normalized ExternalTriggerEnvelope, validates the source-specific
// HMAC signature, derives a deterministic ExternalThreadID, looks up any existing
// run associated with that thread, and routes to SteerRun, ContinueRun, or
// StartRun based on the requested action and the current run state.
//
// Response codes:
//
//	202 — request accepted (steer/continue/start succeeded)
//	400 — invalid JSON or missing required fields
//	401 — signature validation failed or no validator configured for source
//	404 — action is "steer" or "continue" but no existing run found for thread
//	409 — run state mismatch (e.g. "steer" on completed run, "continue" on active run)
//	501 — runStore not configured (required for thread lookup)
func (s *Server) handleExternalTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	// 1. Read the raw body so we can validate the HMAC signature.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
		return
	}

	// 2. Decode the JSON envelope.
	var env trigger.ExternalTriggerEnvelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	env.RawBody = rawBody

	// Prefer X-Trigger-Signature header over the JSON body field when both are
	// present; this avoids the circular dependency where the HMAC would need to
	// cover the field that carries the HMAC itself.
	if hdrSig := strings.TrimSpace(r.Header.Get("X-Trigger-Signature")); hdrSig != "" {
		env.Signature = hdrSig
	}

	// Validate required fields.
	if strings.TrimSpace(env.Source) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "source is required")
		return
	}
	if strings.TrimSpace(env.Action) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "action is required")
		return
	}
	if strings.TrimSpace(env.ThreadID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "thread_id is required")
		return
	}
	if strings.TrimSpace(env.Message) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "message is required")
		return
	}

	// 3. Validate the webhook signature.
	if s.validators == nil {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "no validator registry configured")
		return
	}
	validator, ok := s.validators.Get(env.Source)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "no validator configured for source: "+env.Source)
		return
	}
	if err := validator.ValidateSignature(r.Context(), env); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}

	// 4–6. Derive thread ID, look up store, route by action.
	s.dispatchTriggerEnvelope(w, r, &env)
}

// dispatchTriggerEnvelope is the shared inner routing logic used by both
// handleExternalTrigger and handleGitHubWebhook. It assumes that signature
// validation has already been performed by the caller.
//
// It derives the deterministic ExternalThreadID, looks up existing runs in the
// store, and routes to StartRun, SteerRun, or ContinueRun based on the action
// and current run state.
func (s *Server) dispatchTriggerEnvelope(w http.ResponseWriter, r *http.Request, env *trigger.ExternalTriggerEnvelope) {
	// Derive the deterministic thread ID.
	threadID := trigger.DeriveExternalThreadID(env.Source, env.RepoOwner, env.RepoName, env.ThreadID)

	// Look up existing runs by conversation ID (requires a store).
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "run persistence is not configured")
		return
	}

	// SECURITY (S1/S2): never trust the caller-supplied tenant_id verbatim.
	// These routes authenticate via a single shared per-source HMAC secret,
	// not a per-caller API key, so the request body is not a trustworthy
	// source of tenancy — see ServerOptions.WebhookTenantIDs for the full
	// threat model and resolution rules.
	tenantID, err := s.resolveWebhookTenantID(env.Source, env.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "tenant_mismatch", err.Error())
		return
	}

	runs, err := s.runStore.ListRuns(r.Context(), store.RunFilter{
		ConversationID: threadID.String(),
		TenantID:       tenantID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	action := strings.ToLower(strings.TrimSpace(env.Action))

	// Route by action and run state.
	switch action {
	case "start":
		// Start a new run regardless of existing runs.
		req := harness.RunRequest{
			Prompt:         env.Message,
			ConversationID: threadID.String(),
			TenantID:       tenantID,
		}
		if strings.TrimSpace(env.AgentID) != "" {
			req.AgentID = env.AgentID
		}
		run, err := s.runner.StartRun(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id": run.ID,
			"status": run.Status,
		})

	case "steer":
		if len(runs) == 0 {
			writeError(w, http.StatusNotFound, "no_thread_found", "no existing run found for this thread")
			return
		}
		// Find the most recent active run (first in list since ListRuns returns newest first).
		latestRun := runs[0]
		if latestRun.Status != store.RunStatusRunning && latestRun.Status != store.RunStatusQueued &&
			latestRun.Status != store.RunStatusWaitingForUser {
			writeError(w, http.StatusConflict, "run_state_mismatch",
				"cannot steer a run with status: "+string(latestRun.Status))
			return
		}
		if err := s.runner.SteerRun(latestRun.ID, env.Message); err != nil {
			if errors.Is(err, harness.ErrRunNotFound) {
				writeError(w, http.StatusNotFound, "no_thread_found", "run not found in runner")
				return
			}
			if errors.Is(err, harness.ErrRunNotActive) {
				writeError(w, http.StatusConflict, "run_state_mismatch", err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})

	case "continue":
		if len(runs) == 0 {
			writeError(w, http.StatusNotFound, "no_thread_found", "no existing run found for this thread")
			return
		}
		// Find the most recent completed run.
		latestRun := runs[0]
		if latestRun.Status != store.RunStatusCompleted && latestRun.Status != store.RunStatusFailed {
			writeError(w, http.StatusConflict, "run_state_mismatch",
				"cannot continue a run with status: "+string(latestRun.Status))
			return
		}
		newRun, err := s.runner.ContinueRun(latestRun.ID, env.Message)
		if err != nil {
			if errors.Is(err, harness.ErrRunNotFound) {
				writeError(w, http.StatusNotFound, "no_thread_found", "run not found in runner")
				return
			}
			if errors.Is(err, harness.ErrRunNotCompleted) {
				writeError(w, http.StatusConflict, "run_state_mismatch", err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id": newRun.ID,
			"status": newRun.Status,
		})

	default:
		writeError(w, http.StatusBadRequest, "invalid_request",
			"unknown action: "+env.Action+"; valid actions are: start, steer, continue")
	}
}

// resolveWebhookTenantID resolves the authoritative tenant for a
// webhook/trigger-initiated run (S1/S2 hardening).
//
// These routes (POST /v1/external/trigger and the source-specific webhook
// endpoints) authenticate via a single shared per-source HMAC secret, not a
// per-caller API key, so there is no authenticated principal in the request
// context the way effectiveTenantID relies on for every other endpoint (see
// buildMux: these routes intentionally bypass authMiddleware). The
// caller-supplied tenant_id on the envelope is therefore untrustworthy on
// its own — anyone who knows the shared secret could otherwise inject a run
// into an arbitrary tenant just by naming it.
//
// Resolution:
//   - source has a configured tenant in ServerOptions.WebhookTenantIDs: that
//     tenant is authoritative. An empty or matching bodyTenantID is
//     accepted; a non-matching bodyTenantID is rejected (cross-tenant
//     injection attempt).
//   - source has no configured tenant (the zero-config default): bodyTenantID
//     is ignored outright and "default" is always used. This is the secure
//     default — an unconfigured deployment can never be tricked into
//     cross-tenant injection via the request body.
func (s *Server) resolveWebhookTenantID(source, bodyTenantID string) (string, error) {
	configured, hasConfig := s.webhookTenantIDs[strings.ToLower(strings.TrimSpace(source))]
	bodyTenantID = strings.TrimSpace(bodyTenantID)

	if !hasConfig {
		// No per-source tenant configured: never trust the body's tenant_id.
		return "default", nil
	}

	configured = normalizeTenant(configured)
	if bodyTenantID == "" || normalizeTenant(bodyTenantID) == configured {
		return configured, nil
	}
	return "", fmt.Errorf("tenant_id %q does not match the configured tenant for source %q", bodyTenantID, source)
}
