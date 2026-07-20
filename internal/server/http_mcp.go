package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"go-agent-harness/internal/store"
)

// MCPConnector opens a connection to a remote MCP server and returns the
// tool names available on that server.
// This is a server-local interface mirroring deferred.MCPConnector but
// returning the connected tool names directly so the server does not need
// to call ListTools itself.
type MCPConnector interface {
	// Connect establishes a connection to the given HTTP/SSE MCP server URL.
	// serverName is the logical name to use when identifying the server.
	// Returns the list of tool names the server exposes.
	Connect(ctx context.Context, serverURL, serverName string) ([]string, error)
}

// connectedMCPServer holds metadata about a server that has been connected.
type connectedMCPServer struct {
	Name  string   `json:"name"`
	URL   string   `json:"url"`
	Tools []string `json:"tools"`
}

// handleMCPServers routes /v1/mcp/servers requests.
// GET requires runs:read; POST (connect) requires admin.
func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// GET /v1/mcp/servers — requires runs:read
		if !hasScope(r.Context(), store.ScopeRunsRead) {
			writeScopeError(w, store.ScopeRunsRead)
			return
		}
		s.handleListMCPServers(w, r)
	case http.MethodPost:
		// POST /v1/mcp/servers — requires admin (management operation)
		if !hasScope(r.Context(), store.ScopeAdmin) {
			writeScopeError(w, store.ScopeAdmin)
			return
		}
		s.handleConnectMCPServer(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleListMCPServers(w http.ResponseWriter, _ *http.Request) {
	s.mcpMu.RLock()
	servers := make([]connectedMCPServer, 0, len(s.mcpServers))
	for _, srv := range s.mcpServers {
		servers = append(servers, srv)
	}
	s.mcpMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

func (s *Server) handleConnectMCPServer(w http.ResponseWriter, r *http.Request) {
	if s.mcpConnector == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "MCP connector not configured")
		return
	}

	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "url is required")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		// derive from URL — use hostname
		name = deriveNameFromURL(rawURL)
	}

	toolNames, err := s.mcpConnector.Connect(r.Context(), rawURL, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connect_failed", err.Error())
		return
	}
	if toolNames == nil {
		toolNames = []string{}
	}

	srv := connectedMCPServer{
		Name:  name,
		URL:   rawURL,
		Tools: toolNames,
	}

	s.mcpMu.Lock()
	s.mcpServers[name] = srv
	s.mcpMu.Unlock()

	writeJSON(w, http.StatusCreated, srv)
}

// deriveNameFromURL extracts a simple name from an MCP server URL.
func deriveNameFromURL(rawURL string) string {
	// Strip scheme
	s := rawURL
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	// Strip path
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	// Strip port
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	// Replace dots with underscores
	s = strings.ReplaceAll(s, ".", "_")
	if s == "" {
		return "mcp_server"
	}
	return s
}
