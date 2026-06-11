// Package harness provides an HTTP client for the harnessd REST/SSE API.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client talks to a running harnessd instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Client with sensible defaults.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// MCPServer describes a single MCP server to attach to an agent run.
type MCPServer struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// SandboxScope controls what the agent is allowed to access.
type SandboxScope string

const (
	SandboxScopeWorkspace    SandboxScope = "workspace"
	SandboxScopeLocal        SandboxScope = "local"
	SandboxScopeUnrestricted SandboxScope = "unrestricted"
)

// ApprovalPolicy controls when the agent must ask for approval.
type ApprovalPolicy string

const (
	ApprovalPolicyNone        ApprovalPolicy = "none"
	ApprovalPolicyDestructive ApprovalPolicy = "destructive"
	ApprovalPolicyAll         ApprovalPolicy = "all"
)

// PermissionConfig combines sandbox scope and approval policy.
type PermissionConfig struct {
	Sandbox  SandboxScope   `json:"sandbox"`
	Approval ApprovalPolicy `json:"approval"`
}

// RunRequest is the payload sent to POST /v1/runs.
type RunRequest struct {
	Prompt         string            `json:"prompt"`
	ConversationID string            `json:"conversation_id"`
	SystemPrompt   string            `json:"system_prompt,omitempty"`
	TenantID       string            `json:"tenant_id,omitempty"`
	Model          string            `json:"model,omitempty"`
	MCPServers     []MCPServer       `json:"mcp_servers,omitempty"`
	AllowedTools   []string          `json:"allowed_tools,omitempty"`
	Permissions    *PermissionConfig `json:"permissions,omitempty"`
	MaxCostUSD     float64           `json:"max_cost_usd,omitempty"`
}

// RunResponse is the response from POST /v1/runs.
type RunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// RunResult holds the final outcome of an agent run.
type RunResult struct {
	Output string
	Error  string
	RunID  string
}

// StartRun posts a new run to POST /v1/runs.
func (c *Client) StartRun(ctx context.Context, req RunRequest) (*RunResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal run request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("POST /v1/runs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST /v1/runs: unexpected status %d", resp.StatusCode)
	}
	var runResp RunResponse
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		return nil, fmt.Errorf("decode run response: %w", err)
	}
	return &runResp, nil
}

// SendAndWait calls StartRun then StreamEvents, blocking until terminal state.
func (c *Client) SendAndWait(ctx context.Context, req RunRequest) (*RunResult, error) {
	runResp, err := c.StartRun(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("start run: %w", err)
	}
	return c.StreamEvents(ctx, runResp.RunID)
}
