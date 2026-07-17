package server

import (
	"encoding/json"
	"net/http"

	"go-agent-harness/internal/hooks"
)

// handleHooks serves GET /v1/hooks (epic #737): the startup-computed
// summary of loaded and skipped config-driven hooks. The summary is
// computed once at startup (never re-derived per request) so the listing
// always matches what the runner actually registered. A server built
// without hooks wiring serves empty arrays — not null, not 404.
// The route is read-only: trusting/revoking happens via `harnesscli hooks`.
func (s *Server) handleHooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	summary := s.hooksSummary
	if summary.Hooks == nil {
		summary.Hooks = []hooks.LoadedHook{}
	}
	if summary.Skipped == nil {
		summary.Skipped = []hooks.SkipRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}
