package server

// http_config.go — POST /v1/config/reload (epic #815 slice 3).
//
// The endpoint triggers a daemon config reload through the wired
// ConfigReloadFunc and reports the outcome: hot-swappable fields applied for
// subsequent runs, and restart-only fields that changed but require a daemon
// restart. Admin scope, matching PUT /v1/providers/{name}/key.

import (
	"context"
	"net/http"

	"go-agent-harness/internal/config"
)

// ConfigReloadFunc reloads the daemon configuration and reports the outcome.
// On error the previous configuration remains active (last-known-good
// semantics); the error text is surfaced to the caller as a 400.
type ConfigReloadFunc func(ctx context.Context) (config.ReloadReport, error)

// configReloadResponse is the JSON body returned by POST /v1/config/reload.
type configReloadResponse struct {
	Applied         []string `json:"applied"`
	RestartRequired []string `json:"restart_required"`
}

// handleConfigReload handles POST /v1/config/reload.
func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.configReload == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "config reload is not enabled on this server",
		})
		return
	}
	report, err := s.configReload(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}
	resp := configReloadResponse{
		Applied:         report.Applied,
		RestartRequired: report.RestartRequired,
	}
	if resp.Applied == nil {
		resp.Applied = []string{}
	}
	if resp.RestartRequired == nil {
		resp.RestartRequired = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}
