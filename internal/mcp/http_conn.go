package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpConn implements Conn over HTTP using JSON-RPC 2.0.
// It supports both application/json and text/event-stream (SSE) responses.
type httpConn struct {
	name     string
	endpoint string
	client   *http.Client

	idCounter         atomic.Int64
	negotiatedVersion string

	mu     sync.Mutex
	closed bool
}

// dialHTTP creates a new httpConn from a ServerConfig.
func dialHTTP(cfg ServerConfig) (Conn, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: http transport requires a URL")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp: invalid URL for server %q: %w", cfg.Name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("mcp: server %q URL must use http or https scheme (got %q)", cfg.Name, u.Scheme)
	}
	return &httpConn{
		name:     cfg.Name,
		endpoint: cfg.URL,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// NextID returns the next unique request ID.
func (c *httpConn) NextID() int64 {
	return c.idCounter.Add(1)
}

// Initialize performs the MCP protocol handshake with version negotiation.
// It first tries protocolVersion "2025-11-25", and if the server rejects it
// with error code -32602 or -32600, retries with "2024-11-05".
func (c *httpConn) Initialize(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "go-agent-harness",
			"version": "1.0",
		},
	}

	result, err := c.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		// Check if this is a version negotiation error (-32602 or -32600).
		if isVersionNegotiationError(err) {
			// Retry with older protocol version.
			initParams["protocolVersion"] = "2024-11-05"
			result, err = c.sendRequest(ctx, "initialize", initParams)
			if err != nil {
				return fmt.Errorf("mcp: initialize %q (retry): %w", c.name, err)
			}
			c.negotiatedVersion = "2024-11-05"
			_ = result
			return nil
		}
		return fmt.Errorf("mcp: initialize %q: %w", c.name, err)
	}

	// Parse result to get the negotiated version.
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initResult); err == nil && initResult.ProtocolVersion != "" {
		c.negotiatedVersion = initResult.ProtocolVersion
	} else {
		c.negotiatedVersion = "2025-11-25"
	}
	return nil
}

// ListTools queries the server for its tool list.
func (c *httpConn) ListTools(ctx context.Context) ([]ToolDef, error) {
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
func (c *httpConn) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
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
func (c *httpConn) ListResources(ctx context.Context) ([]ResourceDef, error) {
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
func (c *httpConn) ReadResource(ctx context.Context, uri string) (string, error) {
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

// Close releases resources. It is idempotent and safe for concurrent use.
func (c *httpConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// sendRequest sends a JSON-RPC 2.0 request over HTTP and returns the result.
// It handles both application/json and text/event-stream responses.
func (c *httpConn) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: connection to %q is closed", c.name)
	}
	c.mu.Unlock()

	id := c.NextID()

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		reqBody["params"] = params
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp: request to %q: %w", c.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp: server %q returned HTTP %d %s", c.name, resp.StatusCode, resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "text/event-stream") {
		return c.parseSSEResponse(resp.Body)
	}

	// Default: application/json
	return c.parseJSONResponse(resp.Body)
}

// parseJSONResponse reads a standard JSON-RPC response from the body.
func (c *httpConn) parseJSONResponse(body io.Reader) (json.RawMessage, error) {
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("mcp: decode response from %q: %w", c.name, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp: server %q returned error: %s (code %d)", c.name, rpcResp.Error.Message, rpcResp.Error.Code)
	}
	return rpcResp.Result, nil
}

// parseSSEResponse reads a text/event-stream body and extracts the JSON-RPC
// response from data: lines.
func (c *httpConn) parseSSEResponse(body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data:")
		dataStr = strings.TrimSpace(dataStr)
		if dataStr == "" {
			continue
		}

		var rpcResp jsonRPCResponse
		if err := json.Unmarshal([]byte(dataStr), &rpcResp); err != nil {
			continue // skip non-JSON data lines
		}
		if rpcResp.Error != nil {
			return nil, fmt.Errorf("mcp: server %q returned error: %s (code %d)", c.name, rpcResp.Error.Message, rpcResp.Error.Code)
		}
		return rpcResp.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp: read SSE from %q: %w", c.name, err)
	}
	return nil, fmt.Errorf("mcp: no data line found in SSE response from %q", c.name)
}

// isVersionNegotiationError checks if an error from sendRequest indicates
// a JSON-RPC error with code -32602 or -32600, which means the server
// rejected the protocol version.
func isVersionNegotiationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "code -32602") || strings.Contains(msg, "code -32600")
}
