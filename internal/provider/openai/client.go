package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/pricing"
)

// ModelAPILookupFn returns the value of the "api" field for a model in the catalog,
// or empty string if the model is not found or has no "api" field.
// Used to route models to the correct endpoint (e.g. "responses" → /v1/responses).
type ModelAPILookupFn func(providerName, modelID string) string

type Config struct {
	APIKey            string
	BaseURL           string
	Model             string
	Client            *http.Client
	PricingResolver   pricing.Resolver
	ProviderName      string           // e.g. "openai", "deepseek" — used for pricing resolution
	ModelAPILookup    ModelAPILookupFn // optional — routes models to the correct endpoint
	NoParallelTools   bool             // when true, sets parallel_tool_calls: false in requests (workaround for Gemini streaming bug)
	ForceNonStreaming bool             // when true, always uses non-streaming HTTP requests regardless of req.Stream (workaround for Gemini parallel tool call index bug)
	ModelIDPrefix     string           // when non-empty, prepended to model ID in API requests (e.g., "models/" for Gemini's OpenAI-compat API)
	// Quirks is the list of provider-level quirk identifiers from the catalog.
	// Recognized values:
	//   "reasoning_content_passback" — replay prior assistant Reasoning back to the
	//   API on follow-up turns (required by DeepSeek V4-Pro and OpenRouter-routed
	//   DeepSeek models for multi-turn tool use).
	Quirks            []string
	OpenRouterReferer string // when non-empty and providerName == "openrouter", sent as HTTP-Referer header
	OpenRouterTitle   string // when non-empty and providerName == "openrouter", sent as X-Title header
}

type Client struct {
	apiKey              string
	baseURL             string
	model               string
	client              *http.Client
	pricingResolver     pricing.Resolver
	providerName        string
	modelAPILookup      ModelAPILookupFn
	noParallelTools     bool
	forceNonStreaming   bool
	modelIDPrefix       string
	quirks              []string
	openRouterReferer   string
	openRouterTitle     string
}

func NewClient(config Config) (*Client, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("openai api key is required")
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	model := config.Model
	if model == "" {
		model = "gpt-4.1-mini"
	}
	httpClient := config.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	providerName := config.ProviderName
	if providerName == "" {
		providerName = "openai"
	}
	// Normalize base URL: strip trailing slash and any /v1 suffix so that
	// callers can pass either "https://api.openai.com" or "https://api.openai.com/v1"
	// and get the same behavior — path segments (/v1/chat/completions etc.) are
	// always appended by this client.
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return &Client{
		apiKey:            config.APIKey,
		baseURL:           baseURL,
		model:             model,
		client:            httpClient,
		pricingResolver:   config.PricingResolver,
		providerName:      providerName,
		modelAPILookup:    config.ModelAPILookup,
		noParallelTools:   config.NoParallelTools,
		forceNonStreaming: config.ForceNonStreaming,
		modelIDPrefix:     config.ModelIDPrefix,
		quirks:            append([]string(nil), config.Quirks...),
		openRouterReferer: config.OpenRouterReferer,
		openRouterTitle:   config.OpenRouterTitle,
	}, nil
}

// hasQuirk returns true if the named quirk is present in the client's quirk list.
func (c *Client) hasQuirk(name string) bool {
	return slices.Contains(c.quirks, name)
}

// usesResponsesAPI returns true if the given model requires the Responses API (/v1/responses)
// instead of the standard Chat Completions API (/v1/chat/completions).
func (c *Client) usesResponsesAPI(model string) bool {
	if c.modelAPILookup == nil {
		return false
	}
	return c.modelAPILookup(c.providerName, model) == "responses"
}

func (c *Client) Complete(ctx context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	// Apply provider-specific model ID prefix (e.g. Gemini's OpenAI-compat API requires
	// "models/" prefix: "gemini-2.5-flash" → "models/gemini-2.5-flash").
	if c.modelIDPrefix != "" && !strings.HasPrefix(model, c.modelIDPrefix) {
		model = c.modelIDPrefix + model
	}

	if c.usesResponsesAPI(model) {
		return c.completeWithResponsesAPI(ctx, req, model)
	}

	tools := mapTools(req.Tools)
	toolChoice := ""
	if len(tools) > 0 {
		toolChoice = "auto"
	}
	payload := completionRequest{
		Model:           model,
		Messages:        mapMessages(req.Messages, c.hasQuirk("reasoning_content_passback")),
		Tools:           tools,
		ToolChoice:      toolChoice,
		Stream:          req.Stream != nil && !c.forceNonStreaming,
		StreamOptions:   &streamOptions{IncludeUsage: true},
		ReasoningEffort: req.ReasoningEffort,
	}
	if !payload.Stream {
		payload.StreamOptions = nil
	}
	if c.noParallelTools {
		f := false
		payload.ParallelToolCalls = &f
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if c.providerName == "openrouter" {
		if c.openRouterReferer != "" {
			httpReq.Header.Set("HTTP-Referer", c.openRouterReferer)
		}
		if c.openRouterTitle != "" {
			httpReq.Header.Set("X-Title", c.openRouterTitle)
		}
	}

	requestStart := time.Now()

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
				Provider:   "openai",
				StatusCode: httpRes.StatusCode,
				Body:       strings.TrimSpace(string(responseBody)),
			}
		}
		// Wrap the stream function to capture TTFT timing.
		var ttftMs int64
		var ttftRecorded bool
		origStream := req.Stream
		timedStream := func(delta harness.CompletionDelta) {
			if !ttftRecorded {
				ttftMs = time.Since(requestStart).Milliseconds()
				ttftRecorded = true
			}
			origStream(delta)
		}
		result, err := c.decodeStreamingResponse(model, httpRes.Body, timedStream)
		if err != nil {
			return result, err
		}
		result.TTFTMs = ttftMs
		result.TotalDurationMs = time.Since(requestStart).Milliseconds()
		return result, nil
	}

	responseBody, err := io.ReadAll(httpRes.Body)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read response body: %w", err)
	}

	if httpRes.StatusCode >= 300 {
		return harness.CompletionResult{}, &harness.ProviderHTTPError{
			Provider:   "openai",
			StatusCode: httpRes.StatusCode,
			Body:       strings.TrimSpace(string(responseBody)),
		}
	}

	result, err := c.decodeCompletionResponse(model, responseBody)
	if err != nil {
		return result, err
	}
	result.TotalDurationMs = time.Since(requestStart).Milliseconds()
	// If stream callback was provided but we used non-streaming (forceNonStreaming),
	// emit the full content as a single delta so callers receive text output.
	if c.forceNonStreaming && req.Stream != nil && result.Content != "" {
		req.Stream(harness.CompletionDelta{Content: result.Content})
	}
	return result, nil
}

func (c *Client) decodeCompletionResponse(model string, responseBody []byte) (harness.CompletionResult, error) {
	var response completionResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("decode response: %w", err)
	}
	return c.resultFromCompletionResponse(model, response)
}

func (c *Client) decodeStreamingResponse(model string, body io.Reader, streamFn func(harness.CompletionDelta)) (harness.CompletionResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var lines []string
	state := streamedCompletionState{}
	receivedDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := processStreamBlock(strings.Join(lines, "\n"), &state, streamFn)
			if err != nil {
				return harness.CompletionResult{}, err
			}
			if done {
				receivedDone = true
				break
			}
			lines = lines[:0]
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read stream: %w", err)
	}
	if !receivedDone {
		done, err := processStreamBlock(strings.Join(lines, "\n"), &state, streamFn)
		if err != nil {
			return harness.CompletionResult{}, err
		}
		receivedDone = done
	}
	if !receivedDone {
		return harness.CompletionResult{}, fmt.Errorf("stream ended before [DONE]")
	}

	response := completionResponse{
		Choices: []choice{{
			Message: chatCompletionMessage{
				Content:   state.content.String(),
				ToolCalls: state.toolCalls(),
			},
		}},
		Usage: state.usage,
	}
	result, err := c.resultFromCompletionResponse(model, response)
	if err != nil {
		return result, err
	}
	// Populate reasoning fields from accumulated streaming reasoning content.
	result.ReasoningText = state.reasoning.String()
	if result.ReasoningText != "" && result.Usage != nil && result.Usage.ReasoningTokens != nil {
		result.ReasoningTokens = *result.Usage.ReasoningTokens
	}
	return result, nil
}

func (c *Client) resultFromCompletionResponse(model string, response completionResponse) (harness.CompletionResult, error) {
	if len(response.Choices) == 0 {
		return harness.CompletionResult{}, fmt.Errorf("openai response had no choices")
	}

	choice := response.Choices[0]
	result := harness.CompletionResult{
		Content: strings.TrimSpace(choice.Message.Content),
	}
	usage, usageStatus := normalizeUsage(response.Usage)
	result.Usage = &usage
	result.UsageStatus = usageStatus
	cost, costStatus, totalCostUSD := c.computeCost(model, usage, usageStatus, response)
	result.Cost = &cost
	result.CostStatus = costStatus
	result.CostUSD = &totalCostUSD

	if len(choice.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]harness.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, call := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, harness.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	}
	// Capture reasoning from non-streaming responses. Providers like DeepSeek
	// (and OpenRouter when routing to a reasoning model) include a
	// `reasoning_content` field on the response message; without this, the
	// assistant's thinking is lost and the reasoning_content_passback quirk
	// has nothing to replay on follow-up turns.
	if choice.Message.ReasoningContent != "" {
		result.ReasoningText = choice.Message.ReasoningContent
	} else if len(choice.Message.ReasoningDetails) > 0 {
		var b strings.Builder
		for _, d := range choice.Message.ReasoningDetails {
			b.WriteString(d.Text)
		}
		result.ReasoningText = b.String()
	}
	return result, nil
}

type completionRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []toolSpec     `json:"tools,omitempty"`
	ToolChoice      string         `json:"tool_choice,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	// ReasoningEffort controls the thinking budget for o-series models.
	// Valid values: "low", "medium", "high". Omitted when empty.
	ReasoningEffort   string `json:"reasoning_effort,omitempty"`
	ParallelToolCalls *bool  `json:"parallel_tool_calls,omitempty"` // nil = omit (use provider default); false = disable (workaround for Gemini streaming bug)
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	// ReasoningContent is the legacy DeepSeek-style passback field.
	// Emitted only when the "reasoning_content_passback" quirk is active and
	// the prior assistant turn carried non-empty Reasoning text.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ReasoningDetails is the V4-Pro/OpenRouter structured passback array.
	// Shape: [{type: "reasoning.text", text: "..."}]
	// Emitted alongside ReasoningContent when the quirk is active.
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
}

// reasoningDetail is one element of the reasoning_details passback array used
// by DeepSeek V4-Pro and OpenRouter-routed DeepSeek models.
type reasoningDetail struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolSpec struct {
	Type     string          `json:"type"`
	Function toolSpecDetails `json:"function"`
}

type toolSpecDetails struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type completionResponse struct {
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

type completionChunk struct {
	Choices []chunkChoice `json:"choices"`
	Usage   *usage        `json:"usage,omitempty"`
}

type choice struct {
	Message chatCompletionMessage `json:"message"`
}

type chunkChoice struct {
	Delta        chatCompletionMessageDelta `json:"delta"`
	FinishReason *string                    `json:"finish_reason,omitempty"`
}

type chatCompletionMessage struct {
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
	// ReasoningContent is the assistant's thinking/reasoning text returned by
	// providers that emit it on non-streaming responses (DeepSeek, OpenRouter
	// when routing to a reasoning model). The streaming path accumulates the
	// equivalent stream of `reasoning_content` deltas separately.
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
}

type chatCompletionMessageDelta struct {
	Content          string              `json:"content,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCallDelta `json:"tool_calls,omitempty"`
}

type chatToolCallDelta struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function chatToolCallDeltaField `json:"function,omitempty"`
}

type chatToolCallDeltaField struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamedCompletionState struct {
	content   strings.Builder
	reasoning strings.Builder
	usage     *usage
	toolCall  []*streamedToolCall
}

type streamedToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

func processStreamBlock(raw string, state *streamedCompletionState, streamFn func(harness.CompletionDelta)) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}

	dataLines := make([]string, 0, 4)
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return false, nil
	}

	data := strings.Join(dataLines, "\n")
	if data == "[DONE]" {
		return true, nil
	}

	var chunk completionChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, fmt.Errorf("decode stream chunk: %w", err)
	}
	if chunk.Usage != nil {
		state.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			state.content.WriteString(choice.Delta.Content)
			if streamFn != nil {
				streamFn(harness.CompletionDelta{Content: choice.Delta.Content})
			}
		}
		if choice.Delta.ReasoningContent != "" {
			state.reasoning.WriteString(choice.Delta.ReasoningContent)
			if streamFn != nil {
				streamFn(harness.CompletionDelta{Reasoning: choice.Delta.ReasoningContent})
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			if delta.Index < 0 {
				return false, fmt.Errorf("invalid stream tool call index %d", delta.Index)
			}
			state.ensureToolCall(delta.Index)
			call := state.toolCall[delta.Index]
			if delta.ID != "" {
				call.ID = delta.ID
			}
			if delta.Type != "" {
				call.Type = delta.Type
			}
			if delta.Function.Name != "" {
				call.Name = delta.Function.Name
			}
			if delta.Function.Arguments != "" {
				call.Arguments.WriteString(delta.Function.Arguments)
			}
			if streamFn != nil {
				streamFn(harness.CompletionDelta{
					ToolCall: harness.ToolCallDelta{
						Index:     delta.Index,
						ID:        delta.ID,
						Name:      delta.Function.Name,
						Arguments: delta.Function.Arguments,
					},
				})
			}
		}
	}
	return false, nil
}

func (s *streamedCompletionState) ensureToolCall(index int) {
	for len(s.toolCall) <= index {
		s.toolCall = append(s.toolCall, &streamedToolCall{})
	}
}

func (s *streamedCompletionState) toolCalls() []chatToolCall {
	if len(s.toolCall) == 0 {
		return nil
	}
	out := make([]chatToolCall, 0, len(s.toolCall))
	for index, call := range s.toolCall {
		if call == nil {
			continue
		}
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		id := call.ID
		if id == "" {
			id = "call_" + strconv.Itoa(index)
		}
		out = append(out, chatToolCall{
			ID:   id,
			Type: callType,
			Function: chatToolCallFunction{
				Name:      call.Name,
				Arguments: call.Arguments.String(),
			},
		})
	}
	return slices.Clip(out)
}

type usage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *completionTokensDetail `json:"completion_tokens_details,omitempty"`
	CostUSD                 *float64                `json:"cost_usd,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens"`
}

type completionTokensDetail struct {
	ReasoningTokens int `json:"reasoning_tokens"`
	AudioTokens     int `json:"audio_tokens"`
}

func normalizeUsage(in *usage) (harness.CompletionUsage, harness.UsageStatus) {
	if in == nil {
		return harness.CompletionUsage{}, harness.UsageStatusProviderUnreported
	}
	out := harness.CompletionUsage{
		PromptTokens:     in.PromptTokens,
		CompletionTokens: in.CompletionTokens,
		TotalTokens:      in.TotalTokens,
	}
	if out.TotalTokens == 0 && (out.PromptTokens > 0 || out.CompletionTokens > 0) {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if in.PromptTokensDetails != nil {
		out.CachedPromptTokens = intPtr(in.PromptTokensDetails.CachedTokens)
		out.InputAudioTokens = intPtr(in.PromptTokensDetails.AudioTokens)
	}
	if in.CompletionTokensDetails != nil {
		out.ReasoningTokens = intPtr(in.CompletionTokensDetails.ReasoningTokens)
		out.OutputAudioTokens = intPtr(in.CompletionTokensDetails.AudioTokens)
	}
	return out, harness.UsageStatusProviderReported
}

func intPtr(v int) *int {
	n := v
	return &n
}

func (c *Client) computeCost(model string, usage harness.CompletionUsage, usageStatus harness.UsageStatus, response completionResponse) (harness.CompletionCost, harness.CostStatus, float64) {
	cost := harness.CompletionCost{
		Estimated: false,
	}
	if usageStatus == harness.UsageStatusProviderUnreported {
		return cost, harness.CostStatusProviderUnreported, 0
	}
	if explicit, ok := explicitCostUSD(response); ok {
		cost.TotalUSD = explicit
		return cost, harness.CostStatusAvailable, explicit
	}
	if c.pricingResolver == nil {
		return cost, harness.CostStatusUnpricedModel, 0
	}
	resolved, ok := c.pricingResolver.Resolve(c.providerName, model)
	if !ok {
		return cost, harness.CostStatusUnpricedModel, 0
	}
	cost.PricingVersion = resolved.PricingVersion
	cachedPromptTokens := valueOrZero(usage.CachedPromptTokens)
	billablePromptTokens := usage.PromptTokens
	if resolved.Rates.CacheReadPer1MTokensUSD > 0 && cachedPromptTokens > 0 {
		if cachedPromptTokens > billablePromptTokens {
			cachedPromptTokens = billablePromptTokens
		}
		billablePromptTokens -= cachedPromptTokens
		cost.CacheReadUSD = tokensToUSD(cachedPromptTokens, resolved.Rates.CacheReadPer1MTokensUSD)
	}
	cost.InputUSD = tokensToUSD(billablePromptTokens, resolved.Rates.InputPer1MTokensUSD)
	cost.OutputUSD = tokensToUSD(usage.CompletionTokens, resolved.Rates.OutputPer1MTokensUSD)
	cost.CacheWriteUSD = 0
	cost.TotalUSD = cost.InputUSD + cost.OutputUSD + cost.CacheReadUSD + cost.CacheWriteUSD
	return cost, harness.CostStatusAvailable, cost.TotalUSD
}

func explicitCostUSD(response completionResponse) (float64, bool) {
	if response.CostUSD != nil {
		return *response.CostUSD, true
	}
	if response.Usage != nil && response.Usage.CostUSD != nil {
		return *response.Usage.CostUSD, true
	}
	return 0, false
}

func tokensToUSD(tokens int, per1M float64) float64 {
	if tokens <= 0 || per1M <= 0 {
		return 0
	}
	return (float64(tokens) / 1_000_000.0) * per1M
}

func valueOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// mapMessages converts harness messages to the Chat Completions wire format.
// When replayReasoning is true (i.e. the "reasoning_content_passback" quirk is
// active), any non-empty Reasoning on an assistant message is re-emitted as:
//   - reasoning_content  — legacy DeepSeek-style string field
//   - reasoning_details  — V4-Pro/OpenRouter structured array:
//     [{type: "reasoning.text", text: "..."}]
//
// Providers that require this passback (DeepSeek, OpenRouter/DeepSeek models)
// will reject second-turn tool-result messages if the prior assistant turn's
// reasoning is not present.
func mapMessages(messages []harness.Message, replayReasoning bool) []chatMessage {
	mapped := make([]chatMessage, 0, len(messages))
	for _, msg := range messages {
		chatMsg := chatMessage{
			Role:       msg.Role,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
		}
		if msg.Content != "" {
			chatMsg.Content = msg.Content
		}
		if len(msg.ToolCalls) > 0 {
			chatMsg.ToolCalls = make([]chatToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				chatMsg.ToolCalls = append(chatMsg.ToolCalls, chatToolCall{
					ID:   call.ID,
					Type: "function",
					Function: chatToolCallFunction{
						Name:      call.Name,
						Arguments: call.Arguments,
					},
				})
			}
		}
		// When the reasoning_content_passback quirk is active, replay prior
		// assistant reasoning back to the provider so it can continue the chain.
		// Only assistant messages carry reasoning; other roles never have it.
		if replayReasoning && msg.Role == "assistant" && msg.Reasoning != "" {
			chatMsg.ReasoningContent = msg.Reasoning
			chatMsg.ReasoningDetails = []reasoningDetail{
				{Type: "reasoning.text", Text: msg.Reasoning},
			}
		}
		mapped = append(mapped, chatMsg)
	}
	return mapped
}

func mapTools(definitions []harness.ToolDefinition) []toolSpec {
	if len(definitions) == 0 {
		return nil
	}
	mapped := make([]toolSpec, 0, len(definitions))
	for _, def := range definitions {
		mapped = append(mapped, toolSpec{
			Type: "function",
			Function: toolSpecDetails{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		})
	}
	return mapped
}

// ── Responses API (/v1/responses) ──────────────────────────────────────────

// responsesRequest is the wire format for POST /v1/responses.
type responsesRequest struct {
	Model        string               `json:"model"`
	Input        []responsesInputItem `json:"input"`
	Instructions string               `json:"instructions,omitempty"`
	Tools        []responsesToolSpec  `json:"tools,omitempty"`
	Stream       bool                 `json:"stream,omitempty"`
}

// responsesInputItem represents one item in the input[] array.
// It handles user/assistant messages, function calls, and function call outputs.
type responsesInputItem struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"` // string or []responsesContentBlock
	// For type == "function_call"
	CallID string `json:"call_id,omitempty"`
	Name   string `json:"name,omitempty"`
	// Arguments is required by the Responses API for function_call items, even
	// when empty, but it is invalid on message and function_call_output items.
	Arguments *string `json:"arguments,omitempty"`
	// For type == "function_call_output"
	Output string `json:"output,omitempty"`
}

// responsesToolSpec is the flat tool spec used by the Responses API.
// Unlike Chat Completions, there is no nested "function" wrapper.
type responsesToolSpec struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict"`
}

// responsesResponse is the non-streaming response from POST /v1/responses.
type responsesResponse struct {
	ID     string                `json:"id"`
	Output []responsesOutputItem `json:"output"`
	Usage  *responsesUsage       `json:"usage,omitempty"`
}

// responsesOutputItem is one item in the output[] array.
type responsesOutputItem struct {
	Type    string                   `json:"type"`               // "message" or "function_call"
	Content []responsesContentBlock  `json:"content,omitempty"`  // for type == "message"
	ID      string                   `json:"id,omitempty"`
	CallID  string                   `json:"call_id,omitempty"`  // for type == "function_call"
	Name    string                   `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
}

// responsesContentBlock is a block inside output[].content[].
type responsesContentBlock struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

// responsesUsage holds token counts as reported by the Responses API.
type responsesUsage struct {
	InputTokens         int                       `json:"input_tokens"`
	OutputTokens        int                       `json:"output_tokens"`
	TotalTokens         int                       `json:"total_tokens"`
	InputTokensDetails  *responsesInputDetails    `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputDetails   `json:"output_tokens_details,omitempty"`
}

type responsesInputDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type responsesOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// injectAdditionalPropertiesFalse recursively adds "additionalProperties": false to all JSON
// schema objects that don't already have it. The Responses API with strict:true requires this
// at every level of the schema hierarchy.
func injectAdditionalPropertiesFalse(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	// Deep-copy so we don't mutate the caller's map.
	out := make(map[string]any, len(schema)+1)
	for k, v := range schema {
		switch k {
		case "properties":
			// Recurse into each property value (which are sub-schemas).
			if props, ok := v.(map[string]any); ok {
				newProps := make(map[string]any, len(props))
				for pk, pv := range props {
					if subSchema, ok := pv.(map[string]any); ok {
						newProps[pk] = injectAdditionalPropertiesFalse(subSchema)
					} else {
						newProps[pk] = pv
					}
				}
				out[k] = newProps
			} else {
				out[k] = v
			}
		case "items":
			// Recurse into array item schema.
			if itemSchema, ok := v.(map[string]any); ok {
				out[k] = injectAdditionalPropertiesFalse(itemSchema)
			} else {
				out[k] = v
			}
		default:
			out[k] = v
		}
	}
	// Only inject on objects (type=="object" or has "properties").
	_, hasProps := out["properties"]
	typeVal, _ := out["type"].(string)
	if hasProps || typeVal == "object" {
		if _, already := out["additionalProperties"]; !already {
			out["additionalProperties"] = false
		}
	}
	return out
}

// mapToResponsesRequest converts a harness.CompletionRequest to the Responses API wire format.
// System messages are extracted to the top-level "instructions" field.
// Tool messages become function_call_output items.
// Assistant messages with tool calls produce both a "message" item and "function_call" items.
func mapToResponsesRequest(req harness.CompletionRequest, model string) responsesRequest {
	rr := responsesRequest{
		Model:  model,
		Stream: req.Stream != nil,
	}

	// Map tools to flat Responses API format (no nested "function" wrapper).
	if len(req.Tools) > 0 {
		rr.Tools = make([]responsesToolSpec, 0, len(req.Tools))
		for _, def := range req.Tools {
			rr.Tools = append(rr.Tools, responsesToolSpec{
				Type:        "function",
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
				Strict:      false,
			})
		}
	}

	// Map messages: system → instructions, others → input items.
	rr.Input = make([]responsesInputItem, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			// System messages map to top-level instructions field.
			if rr.Instructions == "" {
				rr.Instructions = msg.Content
			} else {
				rr.Instructions += "\n" + msg.Content
			}
		case "tool":
			// Tool result messages map to function_call_output items.
			rr.Input = append(rr.Input, responsesInputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		case "assistant":
			// Assistant messages with tool calls produce:
			//   1. A "message" item for any text content.
			//   2. One "function_call" item per tool call.
			if msg.Content != "" {
				rr.Input = append(rr.Input, responsesInputItem{
					Type:    "message",
					Role:    "assistant",
					Content: msg.Content,
				})
			}
			for _, call := range msg.ToolCalls {
				arguments := call.Arguments
				rr.Input = append(rr.Input, responsesInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Name,
					Arguments: &arguments,
				})
			}
			// If no content and no tool calls (edge case), still emit an empty message.
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				rr.Input = append(rr.Input, responsesInputItem{
					Type:    "message",
					Role:    "assistant",
					Content: "",
				})
			}
		default:
			// user and any other roles map to plain message items.
			rr.Input = append(rr.Input, responsesInputItem{
				Type:    "message",
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	return rr
}

// resultFromResponsesResponse converts a responsesResponse to a harness.CompletionResult.
func (c *Client) resultFromResponsesResponse(model string, resp responsesResponse) (harness.CompletionResult, error) {
	result := harness.CompletionResult{}

	// Extract content and tool calls from output[] items.
	var contentParts []string
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, block := range item.Content {
				if block.Type == "output_text" && block.Text != "" {
					contentParts = append(contentParts, block.Text)
				}
			}
		case "function_call":
			result.ToolCalls = append(result.ToolCalls, harness.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
	}
	result.Content = strings.TrimSpace(strings.Join(contentParts, ""))

	// Normalize usage from Responses API field names to harness fields.
	usage, usageStatus := normalizeResponsesUsage(resp.Usage)
	result.Usage = &usage
	result.UsageStatus = usageStatus

	// Compute cost using normalized usage (PromptTokens/CompletionTokens are set by normalizeResponsesUsage).
	cost, costStatus, totalCostUSD := c.computeCostFromUsage(model, usage, usageStatus)
	result.Cost = &cost
	result.CostStatus = costStatus
	result.CostUSD = &totalCostUSD

	return result, nil
}

// normalizeResponsesUsage converts Responses API usage fields to harness.CompletionUsage.
func normalizeResponsesUsage(in *responsesUsage) (harness.CompletionUsage, harness.UsageStatus) {
	if in == nil {
		return harness.CompletionUsage{}, harness.UsageStatusProviderUnreported
	}
	out := harness.CompletionUsage{
		// Map input_tokens → PromptTokens and output_tokens → CompletionTokens
		// so the existing cost computation and display logic works unchanged.
		PromptTokens:     in.InputTokens,
		CompletionTokens: in.OutputTokens,
		TotalTokens:      in.TotalTokens,
	}
	if out.TotalTokens == 0 && (out.PromptTokens > 0 || out.CompletionTokens > 0) {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if in.InputTokensDetails != nil {
		out.CachedPromptTokens = intPtr(in.InputTokensDetails.CachedTokens)
	}
	if in.OutputTokensDetails != nil {
		out.ReasoningTokens = intPtr(in.OutputTokensDetails.ReasoningTokens)
	}
	return out, harness.UsageStatusProviderReported
}

// computeCostFromUsage computes cost from a harness.CompletionUsage value.
// This is a variant of computeCost that doesn't require the full completionResponse.
func (c *Client) computeCostFromUsage(model string, usage harness.CompletionUsage, usageStatus harness.UsageStatus) (harness.CompletionCost, harness.CostStatus, float64) {
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
	cachedPromptTokens := valueOrZero(usage.CachedPromptTokens)
	billablePromptTokens := usage.PromptTokens
	if resolved.Rates.CacheReadPer1MTokensUSD > 0 && cachedPromptTokens > 0 {
		if cachedPromptTokens > billablePromptTokens {
			cachedPromptTokens = billablePromptTokens
		}
		billablePromptTokens -= cachedPromptTokens
		cost.CacheReadUSD = tokensToUSD(cachedPromptTokens, resolved.Rates.CacheReadPer1MTokensUSD)
	}
	cost.InputUSD = tokensToUSD(billablePromptTokens, resolved.Rates.InputPer1MTokensUSD)
	cost.OutputUSD = tokensToUSD(usage.CompletionTokens, resolved.Rates.OutputPer1MTokensUSD)
	cost.TotalUSD = cost.InputUSD + cost.OutputUSD + cost.CacheReadUSD
	return cost, harness.CostStatusAvailable, cost.TotalUSD
}

// completeWithResponsesAPI sends a request to POST /v1/responses and returns the result.
func (c *Client) completeWithResponsesAPI(ctx context.Context, req harness.CompletionRequest, model string) (harness.CompletionResult, error) {
	payload := mapToResponsesRequest(req, model)

	body, err := json.Marshal(payload)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("marshal responses request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("create responses request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if c.providerName == "openrouter" {
		if c.openRouterReferer != "" {
			httpReq.Header.Set("HTTP-Referer", c.openRouterReferer)
		}
		if c.openRouterTitle != "" {
			httpReq.Header.Set("X-Title", c.openRouterTitle)
		}
	}

	requestStart := time.Now()

	httpRes, err := c.client.Do(httpReq)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("responses request failed: %w", err)
	}
	defer httpRes.Body.Close()

	if payload.Stream {
		if httpRes.StatusCode >= 300 {
			responseBody, readErr := io.ReadAll(httpRes.Body)
			if readErr != nil {
				return harness.CompletionResult{}, fmt.Errorf("read error response body: %w", readErr)
			}
			return harness.CompletionResult{}, fmt.Errorf("responses API request failed (%d): %s", httpRes.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		// Wrap the stream function to capture TTFT timing.
		var ttftMs int64
		var ttftRecorded bool
		origStream := req.Stream
		var timedStream func(harness.CompletionDelta)
		if origStream != nil {
			timedStream = func(delta harness.CompletionDelta) {
				if !ttftRecorded {
					ttftMs = time.Since(requestStart).Milliseconds()
					ttftRecorded = true
				}
				origStream(delta)
			}
		}
		result, err := c.decodeResponsesStreamingResponse(model, httpRes.Body, timedStream)
		if err != nil {
			return result, err
		}
		result.TTFTMs = ttftMs
		result.TotalDurationMs = time.Since(requestStart).Milliseconds()
		return result, nil
	}

	responseBody, err := io.ReadAll(httpRes.Body)
	if err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read responses response body: %w", err)
	}
	if httpRes.StatusCode >= 300 {
		return harness.CompletionResult{}, fmt.Errorf("responses API request failed (%d): %s", httpRes.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var response responsesResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("decode responses response: %w", err)
	}
	result, err := c.resultFromResponsesResponse(model, response)
	if err != nil {
		return result, err
	}
	result.TotalDurationMs = time.Since(requestStart).Milliseconds()
	return result, nil
}

// ── Responses API streaming ─────────────────────────────────────────────────

// responsesStreamState accumulates state across typed SSE events from the Responses API.
type responsesStreamState struct {
	content      strings.Builder
	toolCalls    map[string]*responsesStreamedToolCall // keyed by call_id
	toolCallKeys []string                              // preserves insertion order
	usage        *responsesUsage
}

type responsesStreamedToolCall struct {
	CallID    string
	Name      string
	Arguments strings.Builder
}

// decodeResponsesStreamingResponse reads the typed SSE stream from the Responses API
// and returns a CompletionResult when the response.completed event is received.
func (c *Client) decodeResponsesStreamingResponse(model string, body io.Reader, streamFn func(harness.CompletionDelta)) (harness.CompletionResult, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	state := &responsesStreamState{
		toolCalls: make(map[string]*responsesStreamedToolCall),
	}

	var currentEvent string
	var dataLines []string
	receivedCompleted := false

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// End of SSE block — process accumulated event + data.
			if currentEvent != "" && len(dataLines) > 0 {
				done, err := processResponsesSSEBlock(currentEvent, strings.Join(dataLines, "\n"), state, streamFn)
				if err != nil {
					return harness.CompletionResult{}, err
				}
				if done {
					receivedCompleted = true
					break
				}
			}
			currentEvent = ""
			dataLines = dataLines[:0]
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		// Ignore comment lines (":") and other fields.
	}
	if err := scanner.Err(); err != nil {
		return harness.CompletionResult{}, fmt.Errorf("read responses stream: %w", err)
	}

	// Handle any trailing block (stream may end without a blank line).
	if !receivedCompleted && currentEvent != "" && len(dataLines) > 0 {
		done, err := processResponsesSSEBlock(currentEvent, strings.Join(dataLines, "\n"), state, streamFn)
		if err != nil {
			return harness.CompletionResult{}, err
		}
		receivedCompleted = done
	}

	if !receivedCompleted {
		return harness.CompletionResult{}, fmt.Errorf("responses stream ended before response.completed")
	}

	// Build the final response from accumulated state.
	output := make([]responsesOutputItem, 0)
	if state.content.Len() > 0 {
		output = append(output, responsesOutputItem{
			Type: "message",
			Content: []responsesContentBlock{
				{Type: "output_text", Text: state.content.String()},
			},
		})
	}
	for _, key := range state.toolCallKeys {
		tc := state.toolCalls[key]
		output = append(output, responsesOutputItem{
			Type:      "function_call",
			CallID:    tc.CallID,
			Name:      tc.Name,
			Arguments: tc.Arguments.String(),
		})
	}

	finalResp := responsesResponse{
		Output: output,
		Usage:  state.usage,
	}
	return c.resultFromResponsesResponse(model, finalResp)
}

// responsesTextDeltaEvent is the payload of response.output_text.delta events.
type responsesTextDeltaEvent struct {
	Delta string `json:"delta"`
}

// responsesFuncArgsDeltaEvent is the payload of response.function_call_arguments.delta events.
type responsesFuncArgsDeltaEvent struct {
	CallID string `json:"call_id"`
	Delta  string `json:"delta"`
}

// responsesFuncArgsDoneEvent is the payload of response.function_call_arguments.done events.
type responsesFuncArgsDoneEvent struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// responsesOutputItemDoneEvent is the payload of response.output_item.done events.
// Used to capture function_call metadata (name, call_id) for tool calls.
type responsesOutputItemDoneEvent struct {
	Item responsesOutputItem `json:"item"`
}

// responsesCompletedEvent is the payload of the terminal response.completed event.
type responsesCompletedEvent struct {
	Response struct {
		ID     string                `json:"id"`
		Output []responsesOutputItem `json:"output"`
		Usage  *responsesUsage       `json:"usage,omitempty"`
	} `json:"response"`
}

// processResponsesSSEBlock handles one typed SSE event from the Responses API stream.
// Returns (true, nil) when the response.completed event is received.
func processResponsesSSEBlock(event, data string, state *responsesStreamState, streamFn func(harness.CompletionDelta)) (bool, error) {
	switch event {
	case "response.output_text.delta":
		var ev responsesTextDeltaEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("decode response.output_text.delta: %w", err)
		}
		if ev.Delta != "" {
			state.content.WriteString(ev.Delta)
			if streamFn != nil {
				streamFn(harness.CompletionDelta{Content: ev.Delta})
			}
		}

	case "response.function_call_arguments.delta":
		var ev responsesFuncArgsDeltaEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("decode response.function_call_arguments.delta: %w", err)
		}
		if ev.CallID != "" {
			tc := state.ensureToolCall(ev.CallID)
			if ev.Delta != "" {
				tc.Arguments.WriteString(ev.Delta)
				if streamFn != nil {
					streamFn(harness.CompletionDelta{
						ToolCall: harness.ToolCallDelta{
							ID:        ev.CallID,
							Arguments: ev.Delta,
						},
					})
				}
			}
		}

	case "response.function_call_arguments.done":
		var ev responsesFuncArgsDoneEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("decode response.function_call_arguments.done: %w", err)
		}
		if ev.CallID != "" {
			tc := state.ensureToolCall(ev.CallID)
			if ev.Name != "" {
				tc.Name = ev.Name
			}
			// The "done" event carries the full arguments string; use it to set the
			// final accumulated value (replacing any delta-accumulated content).
			if ev.Arguments != "" {
				tc.Arguments.Reset()
				tc.Arguments.WriteString(ev.Arguments)
			}
		}

	case "response.output_item.done":
		// This event carries the full item metadata including name and call_id for function calls.
		var ev responsesOutputItemDoneEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("decode response.output_item.done: %w", err)
		}
		if ev.Item.Type == "function_call" && ev.Item.CallID != "" {
			tc := state.ensureToolCall(ev.Item.CallID)
			if ev.Item.Name != "" {
				tc.Name = ev.Item.Name
			}
			if ev.Item.Arguments != "" {
				tc.Arguments.Reset()
				tc.Arguments.WriteString(ev.Item.Arguments)
			}
		}

	case "response.completed":
		// The completed event carries the full response including usage.
		// We use the usage from here; content/tool calls are already accumulated.
		var ev responsesCompletedEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return false, fmt.Errorf("decode response.completed: %w", err)
		}
		if ev.Response.Usage != nil {
			state.usage = ev.Response.Usage
		}
		return true, nil

	// Ignore events we don't need to handle.
	default:
	}
	return false, nil
}

// ensureToolCall returns the streamedToolCall for the given call_id,
// creating it if it doesn't already exist.
func (s *responsesStreamState) ensureToolCall(callID string) *responsesStreamedToolCall {
	if tc, ok := s.toolCalls[callID]; ok {
		return tc
	}
	tc := &responsesStreamedToolCall{CallID: callID}
	s.toolCalls[callID] = tc
	s.toolCallKeys = append(s.toolCallKeys, callID)
	return tc
}