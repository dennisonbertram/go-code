package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go-agent-harness/internal/harness"
)

// HTTPHook adapts a kind=http HookDef onto the harness hook interfaces: it
// POSTs the JSON event to the configured URL and reads a JSON decision from
// the response body — the same wire types the command adapter uses.
//
// Semantics: 2xx with a decision body = decision; 2xx with an empty body =
// allow/no-op; non-2xx, network error, timeout, and unparseable body all
// return a non-nil error so the runner's HookFailureMode decides — the
// adapter never maps transport failures to decisions. Retries, auth
// headers, and mTLS are intentionally not supported (see docs).
type HTTPHook struct {
	def    HookDef
	client *http.Client
	// Logger, when non-nil, receives one structured Error call per failed
	// call with hook_name, event, url, status_code, duration_ms, error.
	Logger harness.Logger
}

// Compile-time checks: HTTPHook satisfies all four harness hook interfaces
// with zero changes to internal/harness.
var (
	_ harness.PreToolUseHook  = (*HTTPHook)(nil)
	_ harness.PostToolUseHook = (*HTTPHook)(nil)
	_ harness.PreMessageHook  = (*HTTPHook)(nil)
	_ harness.PostMessageHook = (*HTTPHook)(nil)
)

// NewHTTPHook returns an HTTPHook for def with its own http.Client.
func NewHTTPHook(def HookDef) *HTTPHook {
	return &HTTPHook{def: def, client: &http.Client{}}
}

// Name implements the harness hook interfaces.
func (h *HTTPHook) Name() string { return h.def.Name }

// PreToolUse implements harness.PreToolUseHook. A non-matching tool name
// returns allow (nil result) without making any HTTP call.
func (h *HTTPHook) PreToolUse(ctx context.Context, ev harness.PreToolUseEvent) (*harness.PreToolUseResult, error) {
	if !h.def.MatchesTool(ev.ToolName) {
		return nil, nil
	}
	payload := toolUsePayload{
		Event:    EventPreToolUse,
		RunID:    ev.RunID,
		HookName: h.def.Name,
		ToolName: ev.ToolName,
		CallID:   ev.CallID,
		Args:     normalizeArgs(ev.Args),
	}
	body, err := h.post(ctx, EventPreToolUse, payload)
	if err != nil {
		return nil, err
	}
	return parsePreToolUseBody(body)
}

// PostToolUse implements harness.PostToolUseHook. A non-matching tool name
// returns no modification (nil result) without making any HTTP call.
func (h *HTTPHook) PostToolUse(ctx context.Context, ev harness.PostToolUseEvent) (*harness.PostToolUseResult, error) {
	if !h.def.MatchesTool(ev.ToolName) {
		return nil, nil
	}
	errText := ""
	if ev.Error != nil {
		errText = ev.Error.Error()
	}
	payload := toolUsePayload{
		Event:      EventPostToolUse,
		RunID:      ev.RunID,
		HookName:   h.def.Name,
		ToolName:   ev.ToolName,
		CallID:     ev.CallID,
		Args:       normalizeArgs(ev.Args),
		Result:     ev.Result,
		DurationMS: ev.Duration.Milliseconds(),
		Error:      errText,
	}
	body, err := h.post(ctx, EventPostToolUse, payload)
	if err != nil {
		return nil, err
	}
	return parsePostToolUseBody(body)
}

// BeforeMessage implements harness.PreMessageHook.
func (h *HTTPHook) BeforeMessage(ctx context.Context, in harness.PreMessageHookInput) (harness.PreMessageHookResult, error) {
	payload := buildMessagePayload(EventPreMessage, h.def.Name, messageInput{
		RunID: in.RunID, Step: in.Step, Request: in.Request,
	}, h.def.IncludeMessages)
	body, err := h.post(ctx, EventPreMessage, payload)
	if err != nil {
		return harness.PreMessageHookResult{}, err
	}
	action, reason, err := parseMessageBody(body)
	if err != nil {
		return harness.PreMessageHookResult{}, h.logError(EventPreMessage, 0, 0, err)
	}
	return harness.PreMessageHookResult{Action: action, Reason: reason}, nil
}

// AfterMessage implements harness.PostMessageHook.
func (h *HTTPHook) AfterMessage(ctx context.Context, in harness.PostMessageHookInput) (harness.PostMessageHookResult, error) {
	payload := buildMessagePayload(EventPostMessage, h.def.Name, messageInput{
		RunID: in.RunID, Step: in.Step, Request: in.Request,
		ResponseText: in.Response.Content, ToolCallCount: len(in.ToolCalls),
	}, h.def.IncludeMessages)
	body, err := h.post(ctx, EventPostMessage, payload)
	if err != nil {
		return harness.PostMessageHookResult{}, err
	}
	action, reason, err := parseMessageBody(body)
	if err != nil {
		return harness.PostMessageHookResult{}, h.logError(EventPostMessage, 0, 0, err)
	}
	return harness.PostMessageHookResult{Action: action, Reason: reason}, nil
}

// post sends one JSON POST bounded by the def's timeout and returns the
// response body for 2xx. Everything else is an error.
func (h *HTTPHook) post(ctx context.Context, event string, payload any) ([]byte, error) {
	start := time.Now()
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal hook payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, h.def.Timeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.def.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build hook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, h.logError(event, 0, duration,
				fmt.Errorf("hook timed out after %s", h.def.Timeout()))
		}
		return nil, h.logError(event, 0, duration, fmt.Errorf("hook request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, h.logError(event, resp.StatusCode, duration,
			fmt.Errorf("hook endpoint returned status %d", resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHookOutputBytes+1))
	if err != nil {
		return nil, h.logError(event, resp.StatusCode, duration,
			fmt.Errorf("read hook response: %w", err))
	}
	if len(body) > maxHookOutputBytes {
		return nil, h.logError(event, resp.StatusCode, duration,
			fmt.Errorf("hook response exceeds %d byte limit", maxHookOutputBytes))
	}
	return bytes.TrimSpace(body), nil
}

// logError reports one structured line via the configured Logger (when set)
// and returns the error for the runner's failure-mode handling.
func (h *HTTPHook) logError(event string, statusCode int, duration time.Duration, err error) error {
	if h.Logger != nil {
		h.Logger.Error("config-driven HTTP hook failed",
			"hook_name", h.def.Name,
			"event", event,
			"url", h.def.URL,
			"status_code", statusCode,
			"duration_ms", duration.Milliseconds(),
			"error", err.Error(),
		)
	}
	return err
}
