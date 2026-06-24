package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/pricing"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	defaultMaxTokens = 4096
	anthropicVersion = "2023-06-01"
)

// Config holds configuration for the Anthropic client.
type Config struct {
	APIKey          string
	BaseURL         string
	Model           string
	Client          *http.Client
	PricingResolver pricing.Resolver
	ProviderName    string
}

// Client is an Anthropic API client implementing harness.Provider.
type Client struct {
	apiKey          string
	baseURL         string
	model           string
	client          *http.Client
	pricingResolver pricing.Resolver
	providerName    string
}

// NewClient creates a new Anthropic provider client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = "anthropic"
	}
	return &Client{
		apiKey:          cfg.APIKey,
		baseURL:         strings.TrimRight(baseURL, "/"),
		model:           model,
		client:          httpClient,
		pricingResolver: cfg.PricingResolver,
		providerName:    providerName,
	}, nil
}

// Complete implements harness.Provider.
func (c *Client) Complete(ctx context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	// Split system message out from conversation messages.
	systemPrompt, conversationMsgs := extractSystem(req.Messages)

	// Convert harness messages to Anthropic format.
	anthropicMsgs, err := mapMessages(conversationMsgs)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("map messages: %w", err)
	}

	payload := messageRequest{
		Model:     model,
		MaxTokens: defaultMaxTokens,
		System:    systemPrompt,
		Messages:  anthropicMsgs,
		Tools:     mapTools(req.Tools),
		Stream:    req.Stream != nil,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	if payload.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	httpRes, err := c.client.Do(httpReq)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer httpRes.Body.Close()

	if payload.Stream {
		if httpRes.StatusCode >= 300 {
			responseBody, readErr := io.ReadAll(httpRes.Body)
			if readErr != nil {
				return harness.CompletionResult{}, fmt.Errorf("read error response body: %w", readErr)
			}
			return harness.CompletionResult{}, &harness.ProviderHTTPError{
				Provider:   "anthropic",
				StatusCode: httpRes.StatusCode,
				Body:       strings.TrimSpace(string(responseBody)),
			}
		}
		return c.decodeStreamingResponse(model, httpRes.Body, req.Stream)
	}

	responseBody, err := io.ReadAll(httpRes.Body)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read response body: %w", err)
	}

	if httpRes.StatusCode >= 300 {
		return harness.CompletionResult{}, &harness.ProviderHTTPError{
			Provider:   "anthropic",
			StatusCode: httpRes.StatusCode,
			Body:       strings.TrimSpace(string(responseBody)),
		}
	}

	return c.decodeResponse(model, responseBody)
}

func (c *Client) decodeResponse(model string, responseBody []byte) (harness.CompletionResult, error) {
	var response messageResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("decode response: %w", err)
	}
	return c.resultFromResponse(model, response)
}

func (c *Client) decodeStreamingResponse(model string, body io.Reader, streamFn func(harness.CompletionDelta)) (harness.CompletionResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	state := &streamState{}
	receivedStop := false

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			// Event type line — we handle by data line type field instead
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		done, err := processStreamEvent(data, state, streamFn)
		if err != nil {
			return harness.CompletionResult{}, err
		}
		if done {
			receivedStop = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read stream: %w", err)
	}
	if !receivedStop {
		return harness.CompletionResult{}, fmt.Errorf("stream ended before message_stop")
	}

	// Build a messageResponse from the streamed state.
	response := state.toMessageResponse()
	return c.resultFromResponse(model, response)
}

func (c *Client) resultFromResponse(model string, response messageResponse) (harness.CompletionResult, error) {
	result := harness.CompletionResult{}

	// Extract text content and tool_use blocks.
	var textParts []string
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			// Marshal Input back to JSON string for Arguments.
			argsJSON, err := json.Marshal(block.Input)
			if err != nil {
				return harness.CompletionResult{}, fmt.Errorf("marshal tool input: %w", err)
			}
			result.ToolCalls = append(result.ToolCalls, harness.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	result.Content = strings.TrimSpace(strings.Join(textParts, ""))

	usage, usageStatus := normalizeUsage(response.Usage)
	result.Usage = &usage
	result.UsageStatus = usageStatus

	cost, costStatus, totalCostUSD := c.computeCost(model, usage, usageStatus)
	result.Cost = &cost
	result.CostStatus = costStatus
	result.CostUSD = &totalCostUSD

	return result, nil
}

// --- Anthropic API types ---

type messageRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Tools     []toolDef `json:"tools,omitempty"`
	Messages  []message `json:"messages"`
	Stream    bool      `json:"stream"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string OR []contentBlock
}

type contentBlock struct {
	Type      string          `json:"type"` // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"` // for tool_result
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type messageResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      *anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Streaming state ---

type streamState struct {
	blocks    []*streamBlock
	stopReason string
	inputTokens  int
	outputTokens int
}

type streamBlock struct {
	blockType string // "text" or "tool_use"
	text      strings.Builder
	toolID    string
	toolName  string
	inputJSON strings.Builder
}

func (s *streamState) ensureBlock(index int) {
	for len(s.blocks) <= index {
		s.blocks = append(s.blocks, &streamBlock{})
	}
}

func (s *streamState) toMessageResponse() messageResponse {
	resp := messageResponse{
		StopReason: s.stopReason,
	}
	if s.inputTokens > 0 || s.outputTokens > 0 {
		resp.Usage = &anthropicUsage{
			InputTokens:  s.inputTokens,
			OutputTokens: s.outputTokens,
		}
	}
	for _, b := range s.blocks {
		if b == nil {
			continue
		}
		switch b.blockType {
		case "text":
			resp.Content = append(resp.Content, contentBlock{
				Type: "text",
				Text: b.text.String(),
			})
		case "tool_use":
			var input json.RawMessage
			if raw := b.inputJSON.String(); raw != "" {
				input = json.RawMessage(raw)
			} else {
				input = json.RawMessage("{}")
			}
			resp.Content = append(resp.Content, contentBlock{
				Type:  "tool_use",
				ID:    b.toolID,
				Name:  b.toolName,
				Input: input,
			})
		}
	}
	return resp
}

// Streaming event types
type streamEvent struct {
	Type string `json:"type"`

	// message_start
	Message *messageResponse `json:"message,omitempty"`

	// content_block_start
	Index        int           `json:"index"`
	ContentBlock *contentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *streamDelta `json:"delta,omitempty"`

	// message_delta
	Usage *streamUsage `json:"usage,omitempty"`
}

type streamDelta struct {
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type streamUsage struct {
	OutputTokens int `json:"output_tokens"`
}

func processStreamEvent(data string, state *streamState, streamFn func(harness.CompletionDelta)) (bool, error) {
	var ev streamEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false, fmt.Errorf("decode stream event: %w", err)
	}

	switch ev.Type {
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			state.inputTokens = ev.Message.Usage.InputTokens
			state.outputTokens = ev.Message.Usage.OutputTokens
		}

	case "content_block_start":
		state.ensureBlock(ev.Index)
		b := state.blocks[ev.Index]
		if ev.ContentBlock != nil {
			b.blockType = ev.ContentBlock.Type
			if ev.ContentBlock.Type == "tool_use" {
				b.toolID = ev.ContentBlock.ID
				b.toolName = ev.ContentBlock.Name
				// Emit a tool call delta with name/id
				if streamFn != nil {
					streamFn(harness.CompletionDelta{
						ToolCall: harness.ToolCallDelta{
							Index: ev.Index,
							ID:    ev.ContentBlock.ID,
							Name:  ev.ContentBlock.Name,
						},
					})
				}
			}
		}

	case "content_block_delta":
		if ev.Index < 0 || ev.Index >= len(state.blocks) {
			return false, fmt.Errorf("invalid content block index %d", ev.Index)
		}
		b := state.blocks[ev.Index]
		if ev.Delta != nil {
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if streamFn != nil && ev.Delta.Text != "" {
					streamFn(harness.CompletionDelta{Content: ev.Delta.Text})
				}
			case "input_json_delta":
				b.inputJSON.WriteString(ev.Delta.PartialJSON)
				if streamFn != nil && ev.Delta.PartialJSON != "" {
					streamFn(harness.CompletionDelta{
						ToolCall: harness.ToolCallDelta{
							Index:     ev.Index,
							Arguments: ev.Delta.PartialJSON,
						},
					})
				}
			}
		}

	case "content_block_stop":
		// Nothing to do; block is complete.

	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			state.stopReason = ev.Delta.StopReason
		}
		if ev.Usage != nil {
			state.outputTokens = ev.Usage.OutputTokens
		}

	case "message_stop":
		return true, nil

	case "error":
		// The event itself is an error from Anthropic
		return false, fmt.Errorf("anthropic stream error: %s", data)
	}

	return false, nil
}

// --- Message conversion ---

// extractSystem splits the first system message from harness messages.
// Anthropic uses a top-level "system" field rather than a message role.
func extractSystem(messages []harness.Message) (string, []harness.Message) {
	var system string
	remaining := make([]harness.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			if system != "" {
				system += "\n" + msg.Content
			} else {
				system = msg.Content
			}
		} else {
			remaining = append(remaining, msg)
		}
	}
	return system, remaining
}

// mapMessages converts harness messages to Anthropic API format.
// Key constraints:
//   - No "system" role (extracted separately)
//   - OpenAI "tool" role messages become Anthropic "user" role with tool_result blocks
//   - Consecutive "user" messages must be merged (Anthropic alternates user/assistant)
//   - Consecutive tool results must be merged into one user message
func mapMessages(messages []harness.Message) ([]message, error) {
	type pendingMsg struct {
		role   string
		blocks []contentBlock
	}

	var pending []pendingMsg

	flush := func() []message {
		out := make([]message, 0, len(pending))
		for _, p := range pending {
			var contentJSON json.RawMessage
			if len(p.blocks) == 1 && p.blocks[0].Type == "text" {
				// Simple text — can use a plain string for efficiency
				contentJSON, _ = json.Marshal(p.blocks[0].Text)
			} else {
				contentJSON, _ = json.Marshal(p.blocks)
			}
			out = append(out, message{
				Role:    p.role,
				Content: contentJSON,
			})
		}
		return out
	}

	addBlock := func(role string, block contentBlock) {
		if len(pending) > 0 && pending[len(pending)-1].role == role {
			// Merge into existing message of same role
			pending[len(pending)-1].blocks = append(pending[len(pending)-1].blocks, block)
		} else {
			pending = append(pending, pendingMsg{role: role, blocks: []contentBlock{block}})
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			addBlock("user", contentBlock{Type: "text", Text: msg.Content})

		case "assistant":
			// Assistant messages may include text content and/or tool calls.
			var blocks []contentBlock
			if msg.Content != "" {
				blocks = append(blocks, contentBlock{Type: "text", Text: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				// Parse the arguments string back to raw JSON for Input field.
				var input json.RawMessage
				if call.Arguments != "" {
					input = json.RawMessage(call.Arguments)
				} else {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, contentBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Name,
					Input: input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			// Assistant messages should not be merged with previous assistant messages.
			// If there's already a pending assistant, we still add a new one.
			// (Anthropic API requires alternating, but we trust the caller provides valid history.)
			if len(pending) > 0 && pending[len(pending)-1].role == "assistant" {
				pending[len(pending)-1].blocks = append(pending[len(pending)-1].blocks, blocks...)
			} else {
				pending = append(pending, pendingMsg{role: "assistant", blocks: blocks})
			}

		case "tool":
			// OpenAI-style tool result → Anthropic tool_result block in a user message.
			addBlock("user", contentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			})
		}
	}

	return flush(), nil
}

func mapTools(definitions []harness.ToolDefinition) []toolDef {
	if len(definitions) == 0 {
		return nil
	}
	mapped := make([]toolDef, 0, len(definitions))
	for _, def := range definitions {
		schema := def.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		mapped = append(mapped, toolDef{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: schema,
		})
	}
	return mapped
}

// --- Usage and pricing ---

func normalizeUsage(u *anthropicUsage) (harness.CompletionUsage, harness.UsageStatus) {
	if u == nil {
		return harness.CompletionUsage{}, harness.UsageStatusProviderUnreported
	}
	out := harness.CompletionUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
	return out, harness.UsageStatusProviderReported
}

func (c *Client) computeCost(model string, usage harness.CompletionUsage, usageStatus harness.UsageStatus) (harness.CompletionCost, harness.CostStatus, float64) {
	cost := harness.CompletionCost{Estimated: false}
	if usageStatus == harness.UsageStatusProviderUnreported {
		return cost, harness.CostStatusProviderUnreported, 0
	}
	if c.pricingResolver == nil {
		return cost, harness.CostStatusUnpricedModel, 0
	}
	resolved, ok := c.pricingResolver.Resolve(c.providerName, model)
	if !ok {
		return cost, harness.CostStatusUnpricedModel, 0
	}
	cost.PricingVersion = resolved.PricingVersion
	cost.InputUSD = tokensToUSD(usage.PromptTokens, resolved.Rates.InputPer1MTokensUSD)
	cost.OutputUSD = tokensToUSD(usage.CompletionTokens, resolved.Rates.OutputPer1MTokensUSD)
	cost.TotalUSD = cost.InputUSD + cost.OutputUSD
	return cost, harness.CostStatusAvailable, cost.TotalUSD
}

func tokensToUSD(tokens int, per1M float64) float64 {
	if tokens <= 0 || per1M <= 0 {
		return 0
	}
	return (float64(tokens) / 1_000_000.0) * per1M
}
