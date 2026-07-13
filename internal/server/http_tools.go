package server

import (
	"net/http"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
)

// toolCatalog is the read-only view the server needs to enumerate the registered
// LLM tool catalog. *harness.Registry satisfies it.
type toolCatalog interface {
	DefinitionsWithMetadata() []harness.ToolMetadata
}

// toolCatalogEntry is the flattened, cleanly-tagged JSON shape for one tool.
// It decouples the HTTP contract from harness.ToolMetadata (whose fields carry
// no JSON tags).
type toolCatalogEntry struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tier        string         `json:"tier"`
	Tags        []string       `json:"tags,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// handleTools handles GET /v1/tools: enumerate the registered tool catalog
// (core and deferred), so operators and clients can see exactly which tools an
// agent run has available without starting a run.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	if s.toolCatalog == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "tool catalog not configured")
		return
	}
	if !hasScope(r.Context(), store.ScopeRunsRead) {
		writeScopeError(w, store.ScopeRunsRead)
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	metas := s.toolCatalog.DefinitionsWithMetadata()
	entries := make([]toolCatalogEntry, 0, len(metas))
	for _, m := range metas {
		entries = append(entries, toolCatalogEntry{
			Name:        m.Definition.Name,
			Description: m.Definition.Description,
			Tier:        string(m.Tier),
			Tags:        m.Tags,
			Parameters:  m.Definition.Parameters,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": entries,
		"count": len(entries),
	})
}
