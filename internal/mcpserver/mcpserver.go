// Package mcpserver exposes the agent harness as an MCP server over HTTP.
//
// It implements the Model Context Protocol (MCP) JSON-RPC 2.0 protocol,
// serving requests at the /mcp endpoint. The server exposes ten tools:
//
//   - start_run: submits a new agent run and returns its run ID
//   - get_run_status: retrieves current status and output for a run
//   - list_runs: lists all known runs
//   - steer_run: sends a steering message to a running run
//   - submit_user_input: submits user input to a waiting run
//   - list_conversations: lists conversations with pagination
//   - get_conversation: retrieves messages for a conversation
//   - search_conversations: searches conversations by keyword
//   - compact_conversation: triggers compaction for a conversation
//   - subscribe_run: subscribe to live SSE events for a run
//
// SSE streaming: GET /mcp returns a text/event-stream of JSON-RPC 2.0
// notifications (run/event and run/completed) for subscribed runs.
//
// Usage:
//
//	runner := &myRunner{...}
//	s := mcpserver.NewServer(runner)
//	http.ListenAndServe(":8081", s.Handler())
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// RunnerInterface is the subset of the harness runner that the MCP server needs.
//
// This interface is intentionally narrow to decouple mcpserver from the full
// harness package, avoiding import cycles and simplifying testing.
type RunnerInterface interface {
	// StartRun submits a new run with the given prompt and returns its run ID.
	StartRun(prompt string) (string, error)

	// GetRunStatus returns the current status of a run by ID.
	GetRunStatus(runID string) (RunStatus, error)

	// ListRuns returns all known runs.
	ListRuns() ([]RunStatus, error)

	// SteerRun sends a steering message to a running run.
	SteerRun(runID string, message string) error

	// SubmitUserInput submits a single input string to a run waiting for user input.
	SubmitUserInput(runID string, input string) error

	// ConversationMessages returns the messages for a conversation by ID.
	// Returns (messages, true) if found, (nil, false) if not found.
	ConversationMessages(conversationID string) ([]ConversationMessage, bool)
}

// ConversationInterface provides access to conversation store operations.
// It may be nil on a Server; tools requiring it will return a graceful error.
type ConversationInterface interface {
	// ListConversations returns a paginated list of conversation summaries.
	ListConversations(ctx context.Context, limit, offset int) ([]ConversationSummary, error)

	// SearchConversations searches conversations by the given query string.
	SearchConversations(ctx context.Context, query string) ([]ConversationSearchResult, error)

	// CompactConversation triggers compaction for the given conversation.
	CompactConversation(ctx context.Context, conversationID string) error
}

// ConversationMessage represents a single message in a conversation.
type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ConversationSummary summarizes a conversation for listing.
type ConversationSummary struct {
	ConversationID string    `json:"conversation_id"`
	CreatedAt      time.Time `json:"created_at"`
	MessageCount   int       `json:"message_count"`
}

// ConversationSearchResult is a search match for a conversation.
type ConversationSearchResult struct {
	ConversationID string `json:"conversation_id"`
	Snippet        string `json:"snippet"`
}

// RunStatus holds the observable state of a single run.
type RunStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Server is an MCP HTTP server that exposes the harness runner as MCP tools.
// It is safe for concurrent use.
type Server struct {
	runner       RunnerInterface
	conv         ConversationInterface // may be nil; tools requiring it return graceful error
	broker       *Broker
	poller       *RunPoller
	pollerCancel context.CancelFunc
}

// NewServer creates a new MCP server backed by the given runner.
// It initializes the SSE broker and run poller.
func NewServer(runner RunnerInterface) *Server {
	b := NewBroker()
	p := NewRunPoller(runner, b, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx)
	return &Server{
		runner:       runner,
		broker:       b,
		poller:       p,
		pollerCancel: cancel,
	}
}

// NewServerWithConversations creates a new MCP server with both runner and
// conversation store access.
func NewServerWithConversations(runner RunnerInterface, conv ConversationInterface) *Server {
	s := NewServer(runner)
	s.conv = conv
	return s
}

// Handler returns an http.Handler that serves the /mcp endpoint.
// GET /mcp returns an SSE stream; POST /mcp handles JSON-RPC 2.0 requests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleSSE(w, r)
			return
		}
		s.handleMCP(w, r)
	})
	return mux
}

// Shutdown cancels the poller goroutine and performs cleanup.
func (s *Server) Shutdown(_ context.Context) error {
	if s.pollerCancel != nil {
		s.pollerCancel()
	}
	return nil
}

// handleSSE serves the SSE stream. Clients connect via GET /mcp and receive
// JSON-RPC 2.0 notifications for all subscribed runs.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := s.broker.SubscribeAll()
	defer cancel()

	ticker := time.NewTicker(sseKeepaliveInterval())
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			type rpcNotif struct {
				JSONRPC string          `json:"jsonrpc"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			b, err := json.Marshal(rpcNotif{JSONRPC: "2.0", Method: n.Method, Params: n.Params})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// sseKeepaliveInterval reads HARNESS_SSE_KEEPALIVE_SECONDS from the environment
// and returns the duration. Defaults to 15 seconds.
func sseKeepaliveInterval() time.Duration {
	s := os.Getenv("HARNESS_SSE_KEEPALIVE_SECONDS")
	if s == "" {
		return 15 * time.Second
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 15 * time.Second
	}
	return time.Duration(n) * time.Second
}

// --- JSON-RPC 2.0 types ---

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	Method  string           `json:"method"`
	ID      *json.RawMessage `json:"id"` // pointer so we can distinguish missing vs null
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP error codes (subset of JSON-RPC 2.0 standard codes).
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// handleMCP is the main HTTP handler for all MCP requests.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nullID(), errParseError, "parse error: "+err.Error())
		return
	}

	// Notifications (no ID field) are silently acknowledged.
	if req.ID == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	id := *req.ID

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, id, req.Params)
	case "tools/list":
		s.handleToolsList(w, id)
	case "tools/call":
		s.handleToolsCall(w, id, req.Params)
	default:
		writeError(w, id, errMethodNotFound, fmt.Sprintf("method not found: %q", req.Method))
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (s *Server) handleInitialize(w http.ResponseWriter, id json.RawMessage, _ json.RawMessage) {
	result := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "go-agent-harness",
			"version": "1.0",
		},
	}
	writeResult(w, id, result)
}

// handleToolsList responds to tools/list.
func (s *Server) handleToolsList(w http.ResponseWriter, id json.RawMessage) {
	tools := []map[string]any{
		{
			"name":        "start_run",
			"description": "Submit a new agent run with the given prompt. Returns the run ID that can be used to poll status.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The prompt to send to the agent.",
					},
				},
				"required": []string{"prompt"},
			},
		},
		{
			"name":        "get_run_status",
			"description": "Get the current status and output of a run by its run ID.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{
						"type":        "string",
						"description": "The run ID returned by start_run.",
					},
				},
				"required": []string{"run_id"},
			},
		},
		{
			"name":        "list_runs",
			"description": "List all known runs with their current statuses.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "steer_run",
			"description": "Send a steering message to a currently running agent run.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{
						"type":        "string",
						"description": "The run ID of the run to steer.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "The steering message to inject into the running agent.",
					},
				},
				"required": []string{"run_id", "message"},
			},
		},
		{
			"name":        "submit_user_input",
			"description": "Submit user input to an agent run that is waiting for input.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{
						"type":        "string",
						"description": "The run ID of the run waiting for input.",
					},
					"input": map[string]any{
						"type":        "string",
						"description": "The input string to submit.",
					},
				},
				"required": []string{"run_id", "input"},
			},
		},
		{
			"name":        "list_conversations",
			"description": "List conversations with optional pagination.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of conversations to return. Defaults to 20.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Number of conversations to skip. Defaults to 0.",
					},
				},
			},
		},
		{
			"name":        "get_conversation",
			"description": "Retrieve all messages for a conversation by its ID.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "The conversation ID to retrieve.",
					},
				},
				"required": []string{"conversation_id"},
			},
		},
		{
			"name":        "search_conversations",
			"description": "Search conversations by a keyword query.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query string.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "compact_conversation",
			"description": "Trigger context compaction for a conversation to reduce its token footprint.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "The conversation ID to compact.",
					},
				},
				"required": []string{"conversation_id"},
			},
		},
		{
			"name":        "subscribe_run",
			"description": "Subscribe to live events for a run. Returns a stream_id. Connect to GET /mcp with SSE to receive run/event and run/completed notifications. Notifications include run_id for filtering.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{
						"type":        "string",
						"description": "Run ID to subscribe to",
					},
				},
				"required": []string{"run_id"},
			},
		},
	}
	writeResult(w, id, map[string]any{"tools": tools})
}

// handleToolsCall dispatches tools/call requests to the appropriate tool handler.
func (s *Server) handleToolsCall(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		writeError(w, id, errInvalidParams, "invalid params: "+err.Error())
		return
	}

	switch p.Name {
	case "start_run":
		s.toolStartRun(w, id, p.Arguments)
	case "get_run_status":
		s.toolGetRunStatus(w, id, p.Arguments)
	case "list_runs":
		s.toolListRuns(w, id)
	case "steer_run":
		s.toolSteerRun(w, id, p.Arguments)
	case "submit_user_input":
		s.toolSubmitUserInput(w, id, p.Arguments)
	case "list_conversations":
		s.toolListConversations(w, id, p.Arguments)
	case "get_conversation":
		s.toolGetConversation(w, id, p.Arguments)
	case "search_conversations":
		s.toolSearchConversations(w, id, p.Arguments)
	case "compact_conversation":
		s.toolCompactConversation(w, id, p.Arguments)
	case "subscribe_run":
		s.toolSubscribeRun(w, id, p.Arguments)
	default:
		// Return an error as a tool result (isError: true), not a JSON-RPC error,
		// since unknown tool is a tool-level error per MCP spec.
		writeToolError(w, id, fmt.Sprintf("unknown tool: %q", p.Name))
	}
}

// toolStartRun handles the start_run tool.
func (s *Server) toolStartRun(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		Prompt string `json:"prompt"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		writeToolError(w, id, "prompt is required")
		return
	}

	runID, err := s.runner.StartRun(a.Prompt)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("start_run failed: %s", err.Error()))
		return
	}
	writeToolText(w, id, fmt.Sprintf("Run started. run_id=%s status=running", runID))
}

// toolGetRunStatus handles the get_run_status tool.
func (s *Server) toolGetRunStatus(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		RunID string `json:"run_id"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.RunID) == "" {
		writeToolError(w, id, "run_id is required")
		return
	}

	status, err := s.runner.GetRunStatus(a.RunID)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("run %q not found: %s", a.RunID, err.Error()))
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "run_id=%s status=%s", status.ID, status.Status)
	if status.Output != "" {
		fmt.Fprintf(&sb, "\noutput=%s", status.Output)
	}
	if status.Error != "" {
		fmt.Fprintf(&sb, "\nerror=%s", status.Error)
	}
	writeToolText(w, id, sb.String())
}

// toolListRuns handles the list_runs tool.
func (s *Server) toolListRuns(w http.ResponseWriter, id json.RawMessage) {
	runs, err := s.runner.ListRuns()
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("list_runs failed: %s", err.Error()))
		return
	}
	if len(runs) == 0 {
		writeToolText(w, id, "No runs found.")
		return
	}

	var sb strings.Builder
	for i, r := range runs {
		if i > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "run_id=%s status=%s", r.ID, r.Status)
		if r.Output != "" {
			fmt.Fprintf(&sb, " output=%s", truncate(r.Output, 80))
		}
		if r.Error != "" {
			fmt.Fprintf(&sb, " error=%s", truncate(r.Error, 80))
		}
	}
	writeToolText(w, id, sb.String())
}

// toolSteerRun handles the steer_run tool.
func (s *Server) toolSteerRun(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		RunID   string `json:"run_id"`
		Message string `json:"message"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.RunID) == "" {
		writeToolError(w, id, "run_id is required")
		return
	}
	if strings.TrimSpace(a.Message) == "" {
		writeToolError(w, id, "message is required")
		return
	}

	if err := s.runner.SteerRun(a.RunID, a.Message); err != nil {
		writeToolError(w, id, err.Error())
		return
	}
	writeToolText(w, id, fmt.Sprintf("steering message accepted for run %s", a.RunID))
}

// toolSubmitUserInput handles the submit_user_input tool.
func (s *Server) toolSubmitUserInput(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		RunID string `json:"run_id"`
		Input string `json:"input"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.RunID) == "" {
		writeToolError(w, id, "run_id is required")
		return
	}
	if strings.TrimSpace(a.Input) == "" {
		writeToolError(w, id, "input is required")
		return
	}

	if err := s.runner.SubmitUserInput(a.RunID, a.Input); err != nil {
		writeToolError(w, id, err.Error())
		return
	}
	writeToolText(w, id, fmt.Sprintf("input submitted for run %s", a.RunID))
}

// toolListConversations handles the list_conversations tool.
func (s *Server) toolListConversations(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if s.conv == nil {
		writeToolError(w, id, "conversations not available")
		return
	}

	var a struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	// Defaults
	a.Limit = 20
	a.Offset = 0

	if args != nil {
		// Unmarshal into a raw map so we can detect which fields were provided.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(args, &raw); err == nil {
			if v, ok := raw["limit"]; ok {
				_ = json.Unmarshal(v, &a.Limit)
			}
			if v, ok := raw["offset"]; ok {
				_ = json.Unmarshal(v, &a.Offset)
			}
		}
	}

	summaries, err := s.conv.ListConversations(context.Background(), a.Limit, a.Offset)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("list_conversations failed: %s", err.Error()))
		return
	}

	data, err := json.Marshal(summaries)
	if err != nil {
		writeToolError(w, id, "failed to encode conversations")
		return
	}
	writeToolText(w, id, string(data))
}

// toolGetConversation handles the get_conversation tool.
func (s *Server) toolGetConversation(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		ConversationID string `json:"conversation_id"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.ConversationID) == "" {
		writeToolError(w, id, "conversation_id is required")
		return
	}

	messages, ok := s.runner.ConversationMessages(a.ConversationID)
	if !ok {
		writeToolError(w, id, fmt.Sprintf("conversation not found: %s", a.ConversationID))
		return
	}

	result := map[string]any{
		"conversation_id": a.ConversationID,
		"messages":        messages,
	}
	data, err := json.Marshal(result)
	if err != nil {
		writeToolError(w, id, "failed to encode conversation")
		return
	}
	writeToolText(w, id, string(data))
}

// toolSearchConversations handles the search_conversations tool.
func (s *Server) toolSearchConversations(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if s.conv == nil {
		writeToolError(w, id, "conversations not available")
		return
	}

	var a struct {
		Query string `json:"query"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.Query) == "" {
		writeToolError(w, id, "query must not be empty")
		return
	}

	results, err := s.conv.SearchConversations(context.Background(), a.Query)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("search_conversations failed: %s", err.Error()))
		return
	}

	data, err := json.Marshal(results)
	if err != nil {
		writeToolError(w, id, "failed to encode search results")
		return
	}
	writeToolText(w, id, string(data))
}

// toolCompactConversation handles the compact_conversation tool.
func (s *Server) toolCompactConversation(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	if s.conv == nil {
		writeToolError(w, id, "conversations not available")
		return
	}

	var a struct {
		ConversationID string `json:"conversation_id"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.ConversationID) == "" {
		writeToolError(w, id, "conversation_id is required")
		return
	}

	if err := s.conv.CompactConversation(context.Background(), a.ConversationID); err != nil {
		writeToolError(w, id, fmt.Sprintf("compact_conversation failed: %s", err.Error()))
		return
	}
	writeToolText(w, id, `{"ok":true}`)
}

// toolSubscribeRun handles the subscribe_run tool.
// It validates the run exists, registers it for polling, and returns the SSE endpoint info.
// If the run is already in a terminal state, it returns immediately with already_completed:true.
func (s *Server) toolSubscribeRun(w http.ResponseWriter, id json.RawMessage, args json.RawMessage) {
	var a struct {
		RunID string `json:"run_id"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.RunID) == "" {
		writeToolError(w, id, "run_id is required")
		return
	}

	// Verify run exists.
	status, err := s.runner.GetRunStatus(a.RunID)
	if err != nil {
		writeToolError(w, id, fmt.Sprintf("run not found: %s", a.RunID))
		return
	}

	// If already terminal, return immediately.
	if status.Status == "completed" || status.Status == "failed" {
		result := map[string]any{
			"run_id":            a.RunID,
			"status":            status.Status,
			"already_completed": true,
		}
		b, _ := json.Marshal(result)
		writeToolText(w, id, string(b))
		return
	}

	// Register for polling.
	s.poller.Watch(a.RunID)

	result := map[string]any{
		"stream_id":    a.RunID,
		"run_id":       a.RunID,
		"sse_endpoint": "GET /mcp",
	}
	b, _ := json.Marshal(result)
	writeToolText(w, id, string(b))
}

// --- response helpers ---

// writeResult writes a successful JSON-RPC response.
func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		writeError(w, id, errInternal, "internal error: marshal result: "+err.Error())
		return
	}
	writeRaw(w, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  raw,
	})
}

// writeError writes a JSON-RPC error response.
func writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeRaw(w, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

// writeToolText writes a successful tools/call result with a text content item.
func writeToolText(w http.ResponseWriter, id json.RawMessage, text string) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": false,
	}
	writeResult(w, id, result)
}

// writeToolError writes a tools/call result indicating a tool-level error.
func writeToolError(w http.ResponseWriter, id json.RawMessage, msg string) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "Error: " + msg},
		},
		"isError": true,
	}
	writeResult(w, id, result)
}

// writeRaw encodes and writes a JSON-RPC response.
func writeRaw(w http.ResponseWriter, resp rpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"}}`,
			http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// nullID returns the JSON null value, used as the id for parse-error responses
// where we could not determine the request ID.
func nullID() json.RawMessage {
	return json.RawMessage("null")
}

// truncate shortens s to at most n runes, appending "..." if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
