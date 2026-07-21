// Package mcp implements an MCP (Model Context Protocol) client manager.
//
// It supports connecting to external MCP servers over stdio (subprocess) or
// HTTP transports, discovering their tools, and executing tool calls.
//
// The ClientManager manages multiple named server connections. Each connection
// uses JSON-RPC 2.0 over the transport (stdio or HTTP). Concurrent requests on
// the same connection are multiplexed by request ID; each caller waits on its
// own response channel.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// ToolDef describes a tool exposed by an MCP server.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ResourceDef describes a resource exposed by an MCP server.
type ResourceDef struct {
	URI         string
	Name        string
	Description string
	MimeType    string
}

// ServerConfig holds the configuration for a single MCP server connection.
type ServerConfig struct {
	// Name is the logical identifier for this server. Required, must be unique.
	Name string

	// Transport must be "stdio" or "http".
	Transport string

	// Stdio transport fields.
	Command string   // path or name of the subprocess to launch
	Args    []string // arguments for the subprocess

	// HTTP transport field.
	URL string // HTTP endpoint, e.g. "http://localhost:3000/mcp"

	// Headers are static HTTP headers sent with every request on the http
	// transport (e.g. "Authorization": "Bearer <token>"). They are applied
	// after the transport's own protocol headers, so an explicitly configured
	// header wins on collision. Ignored on the stdio transport.
	Headers map[string]string

	// TokenProvider, when set, resolves a bearer token for this server at
	// request time (e.g. from the OAuth token store with silent refresh).
	// It is consulted on the http transport only when no static
	// Authorization header is configured; returning ("", nil) sends the
	// request without credentials. Not serialized — set programmatically.
	TokenProvider TokenProviderFunc `json:"-"`
}

// TokenProviderFunc resolves a bearer token for the named server at request
// time. Returning ("", nil) means no credentials are available and the
// request is sent unauthenticated.
type TokenProviderFunc func(ctx context.Context, serverName string) (string, error)

// Conn represents an active connection to an MCP server.
type Conn interface {
	// Initialize performs the MCP protocol handshake.
	Initialize(ctx context.Context) error

	// ListTools queries the server for available tools.
	ListTools(ctx context.Context) ([]ToolDef, error)

	// CallTool invokes a named tool with the given JSON arguments.
	CallTool(ctx context.Context, name string, args json.RawMessage) (string, error)

	// NextID returns the next unique request ID. Exposed for testing.
	NextID() int64

	// Close releases resources associated with this connection.
	Close() error
}

// ResourceCapableConn is an optional extension of Conn implemented by
// connections that support the MCP resources capability (resources/list and
// resources/read). Not every Conn implementation supports resources — callers
// should type-assert and treat a failed assertion as "server does not support
// resources" rather than an error.
type ResourceCapableConn interface {
	// ListResources queries the server for available resources.
	ListResources(ctx context.Context) ([]ResourceDef, error)

	// ReadResource reads the content of a resource by URI.
	ReadResource(ctx context.Context, uri string) (string, error)
}

// ConnFactory is a function that creates a new Conn.
type ConnFactory func() (Conn, error)

// serverEntry holds per-server state inside the ClientManager.
type serverEntry struct {
	config  ServerConfig
	factory ConnFactory // nil for config-based entries before first connect

	mu   sync.Mutex
	conn Conn // non-nil after first successful connect
}

// ClientManager manages connections to external MCP servers.
//
// Servers can be added with either AddServer (config-based, lazy connect) or
// AddServerWithConn (test helper, eager connect factory).
//
// ClientManager is safe for concurrent use.
type ClientManager struct {
	mu      sync.RWMutex
	servers map[string]*serverEntry

	tokenProvider TokenProviderFunc // default for servers registered without one
}

// NewClientManager returns a new, empty ClientManager.
func NewClientManager() *ClientManager {
	return &ClientManager{
		servers: make(map[string]*serverEntry),
	}
}

// SetTokenProvider sets the default token provider applied to servers
// registered without an explicit ServerConfig.TokenProvider. Call it before
// the first connect (the provider is captured at dial time).
func (m *ClientManager) SetTokenProvider(p TokenProviderFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenProvider = p
}

// AddServer registers a server from a config. The connection is not established
// until the first call to DiscoverTools or ExecuteTool.
func (m *ClientManager) AddServer(cfg ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("mcp: server name must not be empty")
	}
	switch cfg.Transport {
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("mcp: stdio transport requires a command")
		}
	case "http":
		if cfg.URL == "" {
			return fmt.Errorf("mcp: http transport requires a URL")
		}
	default:
		return fmt.Errorf("mcp: unsupported transport %q: must be \"stdio\" or \"http\"", cfg.Transport)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[cfg.Name]; exists {
		return fmt.Errorf("mcp: server %q already registered", cfg.Name)
	}
	m.servers[cfg.Name] = &serverEntry{config: cfg}
	return nil
}

// AddServerWithConn registers a server using a factory function that creates
// the connection on demand. Primarily useful for testing with in-process
// pipe-based connections.
func (m *ClientManager) AddServerWithConn(name string, factory ConnFactory) error {
	if name == "" {
		return fmt.Errorf("mcp: server name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("mcp: factory must not be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("mcp: server %q already registered", name)
	}
	m.servers[name] = &serverEntry{
		config:  ServerConfig{Name: name},
		factory: factory,
	}
	return nil
}

// ListServers returns the names of all registered servers.
func (m *ClientManager) ListServers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for n := range m.servers {
		names = append(names, n)
	}
	return names
}

// DiscoverTools connects to the named server if not already connected and
// returns its tool list.
func (m *ClientManager) DiscoverTools(ctx context.Context, serverName string) ([]ToolDef, error) {
	conn, err := m.getConn(ctx, serverName)
	if err != nil {
		return nil, err
	}
	return conn.ListTools(ctx)
}

// ExecuteTool connects to the named server if not already connected and
// executes the named tool with the given arguments.
func (m *ClientManager) ExecuteTool(ctx context.Context, serverName, toolName string, args json.RawMessage) (string, error) {
	conn, err := m.getConn(ctx, serverName)
	if err != nil {
		return "", err
	}
	return conn.CallTool(ctx, toolName, args)
}

// ListResources connects to the named server if not already connected and
// returns its resource list. If the server's connection does not support the
// MCP resources capability, it returns a clean error.
func (m *ClientManager) ListResources(ctx context.Context, serverName string) ([]ResourceDef, error) {
	conn, err := m.getConn(ctx, serverName)
	if err != nil {
		return nil, err
	}
	rc, ok := conn.(ResourceCapableConn)
	if !ok {
		return nil, fmt.Errorf("mcp: server %q does not support resources", serverName)
	}
	return rc.ListResources(ctx)
}

// ReadResource connects to the named server if not already connected and
// reads the resource at uri. If the server's connection does not support the
// MCP resources capability, it returns a clean error.
func (m *ClientManager) ReadResource(ctx context.Context, serverName, uri string) (string, error) {
	conn, err := m.getConn(ctx, serverName)
	if err != nil {
		return "", err
	}
	rc, ok := conn.(ResourceCapableConn)
	if !ok {
		return "", fmt.Errorf("mcp: server %q does not support resources", serverName)
	}
	return rc.ReadResource(ctx, uri)
}

// Close closes all open server connections and releases resources.
func (m *ClientManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for _, entry := range m.servers {
		entry.mu.Lock()
		if entry.conn != nil {
			if err := entry.conn.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("mcp: close %q: %w", entry.config.Name, err)
			}
			entry.conn = nil
		}
		entry.mu.Unlock()
	}
	return firstErr
}

// getConn retrieves (or lazily creates) the connection for the named server.
func (m *ClientManager) getConn(ctx context.Context, serverName string) (Conn, error) {
	m.mu.RLock()
	entry, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %q not found", serverName)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.conn != nil {
		return entry.conn, nil
	}

	// Establish the connection.
	var conn Conn
	var err error

	if entry.factory != nil {
		conn, err = entry.factory()
	} else {
		cfg := entry.config
		if cfg.TokenProvider == nil {
			m.mu.RLock()
			cfg.TokenProvider = m.tokenProvider
			m.mu.RUnlock()
		}
		conn, err = dialServer(cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %q: %w", serverName, err)
	}

	if err := conn.Initialize(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: initialize %q: %w", serverName, err)
	}

	entry.conn = conn
	return conn, nil
}

// dialServer creates a new connection based on the server config.
func dialServer(cfg ServerConfig) (Conn, error) {
	switch cfg.Transport {
	case "stdio":
		return dialStdio(cfg)
	case "http":
		return dialHTTP(cfg)
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", cfg.Transport)
	}
}

// dialStdio launches a subprocess and wraps its stdin/stdout as an MCP Conn.
func dialStdio(cfg ServerConfig) (Conn, error) {
	//nolint:gosec // command is from trusted config, not from user input at request time
	cmd := exec.Command(cfg.Command, cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", cfg.Command, err)
	}

	// stdout is io.ReadCloser; pass it as both the reader and the closer so
	// Close() can close it to unblock scanner.Scan() during shutdown.
	return newStdioConnFromPipes(cfg.Name, stdout, stdout, stdin, cmd), nil
}

// stdioConn implements Conn over a reader/writer pair using JSON-RPC 2.0.
//
// Concurrent requests are supported: each request gets a unique integer ID and
// a dedicated buffered response channel. The read loop routes responses back to
// the appropriate channel by ID.
//
// Synchronization design:
//   - stateMu protects `closed` and `pending` (the ID→channel map).
//   - writeMu ensures only one goroutine writes to the wire at a time.
//   - The read loop runs in its own goroutine and does NOT hold stateMu while
//     delivering responses, so there is no deadlock between writers and readers.
//
// Shutdown design:
//   - Close() sets closed=true under stateMu, drains all pending channels so
//     blocked sendRequest goroutines return immediately, closes the writer
//     (stdin), closes the reader (stdout) to unblock scanner.Scan(), kills the
//     subprocess if present, then waits for readLoop to exit via <-done.
//   - This guarantees bounded shutdown even if the peer never closes stdout.
type stdioConn struct {
	name string

	// writeMu protects writes to writer. Separate from stateMu to avoid
	// deadlocking the read loop.
	writeMu sync.Mutex
	writer  io.WriteCloser

	// reader is the closeable stdout of the subprocess (or pipe end in tests).
	// Closing it unblocks scanner.Scan() so readLoop exits promptly on Close().
	reader io.Closer

	cmd *exec.Cmd // may be nil for pipe-based test connections

	stateMu sync.Mutex
	pending map[int64]chan jsonRPCResponse
	closed  bool

	done chan struct{} // closed when the read loop exits

	idCounter atomic.Int64
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewStdioConn creates a Conn from an already-open reader/writer pair.
// This constructor is intended for testing — production code uses dialStdio.
// If r implements io.Closer, it will be closed during shutdown to unblock the
// scanner; otherwise a no-op closer is used.
func NewStdioConn(name string, r io.Reader, w io.WriteCloser) (Conn, error) {
	var rc io.Closer
	if c, ok := r.(io.Closer); ok {
		rc = c
	} else {
		rc = io.NopCloser(r)
	}
	return newStdioConnFromPipes(name, r, rc, w, nil), nil
}

// newStdioConnFromPipes creates a stdioConn and starts the read loop.
// r is the reader passed to the scanner; rc is the closer for r (may be the
// same object when r implements io.ReadCloser, as with cmd.StdoutPipe()).
func newStdioConnFromPipes(name string, r io.Reader, rc io.Closer, w io.WriteCloser, cmd *exec.Cmd) *stdioConn {
	c := &stdioConn{
		name:    name,
		writer:  w,
		reader:  rc,
		cmd:     cmd,
		pending: make(map[int64]chan jsonRPCResponse),
		done:    make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// readLoop reads newline-delimited JSON-RPC responses from the server and
// routes them to the appropriate pending channel.
//
// The read loop acquires stateMu only briefly to look up and remove the
// pending channel, then sends to the channel outside the lock.
func (c *stdioConn) readLoop(r io.Reader) {
	defer close(c.done)

	scanner := bufio.NewScanner(r)
	// Increase buffer for large payloads.
	const maxTokenSize = 4 * 1024 * 1024
	scanner.Buffer(make([]byte, maxTokenSize), maxTokenSize)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		c.stateMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.stateMu.Unlock()

		// Deliver outside the lock.
		if ok {
			select {
			case ch <- resp:
			default:
			}
		}
	}

	// Connection closed or EOF: drain all pending channels with an error.
	c.stateMu.Lock()
	for id, ch := range c.pending {
		errResp := jsonRPCResponse{
			ID:    id,
			Error: &jsonRPCError{Code: -32700, Message: "connection closed"},
		}
		delete(c.pending, id)
		c.stateMu.Unlock()
		// Send outside the lock so we don't hold it while delivering.
		select {
		case ch <- errResp:
		default:
		}
		c.stateMu.Lock()
	}
	c.stateMu.Unlock()
}

// NextID returns the next unique request ID.
func (c *stdioConn) NextID() int64 {
	return c.idCounter.Add(1)
}

// sendRequest sends a JSON-RPC request and waits for the response.
func (c *stdioConn) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.NextID()
	ch := make(chan jsonRPCResponse, 1)

	// Register the pending channel before writing (so the response can arrive
	// before we even finish our select).
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return nil, fmt.Errorf("mcp: connection to %q is closed", c.name)
	}
	c.pending[id] = ch
	c.stateMu.Unlock()

	// Build and marshal the request.
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	// Write to the wire under writeMu only (not stateMu).
	// We do NOT re-read c.closed here under writeMu to avoid a data race with
	// Close() which sets c.closed under stateMu. Instead we attempt the write
	// and handle any write error (Close() closes the writer, so writes will fail
	// if the connection is being closed concurrently).
	c.writeMu.Lock()
	_, writeErr := fmt.Fprintf(c.writer, "%s\n", data)
	c.writeMu.Unlock()

	if writeErr != nil {
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("mcp: write to %q: %w", c.name, writeErr)
	}

	// Wait for the response.
	select {
	case <-ctx.Done():
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("mcp: request to %q timed out: %w", c.name, ctx.Err())
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp: connection to %q closed while waiting for response", c.name)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: server %q returned error: %s (code %d)", c.name, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-c.done:
		// readLoop exited (EOF or reader closed). Clean up pending defensively;
		// the readLoop drain may have already removed this entry but be safe.
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("mcp: connection to %q closed while waiting for response", c.name)
	}
}

// sendNotification sends a JSON-RPC notification (no ID, no response expected).
func (c *stdioConn) sendNotification(method string, params any) error {
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}
	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("mcp: marshal notification: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return fmt.Errorf("mcp: connection to %q is closed", c.name)
	}
	_, err = fmt.Fprintf(c.writer, "%s\n", data)
	return err
}

// Initialize performs the MCP initialize/initialized handshake.
// It first tries protocol version "2025-11-25" and falls back to "2024-11-05"
// if the server rejects it with error code -32602 or -32600.
func (c *stdioConn) Initialize(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "go-agent-harness",
			"version": "1.0",
		},
	}
	_, err := c.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		// Check if this is a version negotiation error; if so, retry with older version.
		if isVersionNegotiationError(err) {
			initParams["protocolVersion"] = "2024-11-05"
			_, err = c.sendRequest(ctx, "initialize", initParams)
			if err != nil {
				return fmt.Errorf("mcp: initialize %q (retry): %w", c.name, err)
			}
			_ = c.sendNotification("notifications/initialized", nil)
			return nil
		}
		return fmt.Errorf("mcp: initialize %q: %w", c.name, err)
	}
	// Best-effort: send initialized notification. Some servers ignore this.
	_ = c.sendNotification("notifications/initialized", nil)
	return nil
}

// ListTools queries the server for its tool list.
func (c *stdioConn) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list response: %w", err)
	}
	out := make([]ToolDef, 0, len(result.Tools))
	for _, t := range result.Tools {
		out = append(out, ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

// CallTool invokes a named tool on the server.
func (c *stdioConn) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	raw, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	return extractToolCallResult(raw)
}

// ListResources queries the server for its resource list.
func (c *stdioConn) ListResources(ctx context.Context) ([]ResourceDef, error) {
	raw, err := c.sendRequest(ctx, "resources/list", nil)
	if err != nil {
		if isMethodNotFoundError(err) {
			return nil, fmt.Errorf("mcp: server %q does not support resources", c.name)
		}
		return nil, err
	}
	return parseResourcesListResult(raw)
}

// ReadResource reads a resource's content by URI.
func (c *stdioConn) ReadResource(ctx context.Context, uri string) (string, error) {
	params := map[string]any{"uri": uri}
	raw, err := c.sendRequest(ctx, "resources/read", params)
	if err != nil {
		if isMethodNotFoundError(err) {
			return "", fmt.Errorf("mcp: server %q does not support resources", c.name)
		}
		return "", err
	}
	return extractResourceReadResult(raw)
}

// extractToolCallResult extracts the text content from a tools/call result.
func extractToolCallResult(raw json.RawMessage) (string, error) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		// Fall back to returning the raw JSON.
		return string(raw), nil
	}
	if result.IsError {
		for _, cont := range result.Content {
			if cont.Type == "text" && cont.Text != "" {
				return "", fmt.Errorf("mcp tool error: %s", cont.Text)
			}
		}
		return "", fmt.Errorf("mcp tool returned an error")
	}
	var sb strings.Builder
	for _, cont := range result.Content {
		if cont.Type == "text" {
			sb.WriteString(cont.Text)
		}
	}
	if sb.Len() == 0 {
		return string(raw), nil
	}
	return sb.String(), nil
}

// parseResourcesListResult parses a "resources/list" JSON-RPC result into
// ResourceDefs.
func parseResourcesListResult(raw json.RawMessage) ([]ResourceDef, error) {
	var result struct {
		Resources []struct {
			URI         string `json:"uri"`
			Name        string `json:"name"`
			Description string `json:"description"`
			MimeType    string `json:"mimeType"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse resources/list response: %w", err)
	}
	out := make([]ResourceDef, 0, len(result.Resources))
	for _, r := range result.Resources {
		out = append(out, ResourceDef{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		})
	}
	return out, nil
}

// extractResourceReadResult extracts text content from a "resources/read"
// result. Binary ("blob") contents are not decoded — a short descriptive note
// is returned instead of raw base64/bytes.
func extractResourceReadResult(raw json.RawMessage) (string, error) {
	var result struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Blob     string `json:"blob"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	var parts []string
	for _, cont := range result.Contents {
		switch {
		case cont.Text != "":
			parts = append(parts, cont.Text)
		case cont.Blob != "":
			mt := cont.MimeType
			if mt == "" {
				mt = "application/octet-stream"
			}
			parts = append(parts, fmt.Sprintf("[binary content: %s, %d bytes base64-encoded]", mt, len(cont.Blob)))
		}
	}
	if len(parts) == 0 {
		return string(raw), nil
	}
	return strings.Join(parts, "\n"), nil
}

// isMethodNotFoundError checks if an error from sendRequest indicates a
// JSON-RPC "method not found" error (code -32601), which means the server
// does not implement the requested method (e.g. resources/list).
func isMethodNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "code -32601")
}

// Close closes the connection and any underlying subprocess.
//
// Close is safe to call concurrently and is idempotent. It guarantees a
// bounded shutdown: it immediately unblocks all in-flight sendRequest calls
// (returning errors to them), closes the reader to force the scanner to exit,
// kills the subprocess if one is present, and then waits for readLoop to finish.
func (c *stdioConn) Close() error {
	// Mark closed and steal the current pending map atomically.
	// Any new sendRequest that starts after this point will see closed=true
	// and return immediately without adding to pending.
	c.stateMu.Lock()
	alreadyClosed := c.closed
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan jsonRPCResponse) // fresh map; old entries drained below
	c.stateMu.Unlock()

	if alreadyClosed {
		return nil
	}

	// Unblock all in-flight sendRequest goroutines by delivering error responses.
	// This must happen before we close the writer/reader so that any goroutine
	// racing in sendRequest between the stateMu check and writeMu check will
	// also see closed=true on the second check and clean itself up.
	for id, ch := range pending {
		errResp := jsonRPCResponse{
			ID:    id,
			Error: &jsonRPCError{Code: -32700, Message: "connection closed"},
		}
		select {
		case ch <- errResp:
		default:
		}
	}

	// Close the writer (subprocess stdin) to signal it to exit.
	c.writeMu.Lock()
	_ = c.writer.Close()
	c.writeMu.Unlock()

	// Close the reader (subprocess stdout) to unblock scanner.Scan() in
	// readLoop so it exits promptly rather than blocking forever.
	if c.reader != nil {
		_ = c.reader.Close()
	}

	// Kill the subprocess if it hasn't already exited. We do not wait for a
	// graceful exit — Close() is expected to be immediate.
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}

	// Wait for the read loop to finish. Since we closed the reader above,
	// scanner.Scan() will return false promptly and readLoop will exit.
	<-c.done
	return nil
}
