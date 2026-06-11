// Package safety screens incoming messages for harmful content using Llama Guard
package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Safe     bool
	Category string
}

type Checker interface {
	Check(ctx context.Context, message string) (*Result, error)
}

const RefusalText = "I'm sorry, but I can't help with that request. If you have a different question, feel free to ask."

type LlamaGuardConfig struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

type LlamaGuardChecker struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

func NewLlamaGuardChecker(cfg LlamaGuardConfig) *LlamaGuardChecker {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &LlamaGuardChecker{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *LlamaGuardChecker) Check(ctx context.Context, message string) (*Result, error) {
	prompt := fmt.Sprintf(
		"<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\n"+
			"%s<|eot_id|><|start_header_id|>assistant<|end_header_id|>\n\n",
		message,
	)
	reqBody := ollamaGenerateRequest{Model: c.model, Prompt: prompt, Stream: false, Options: ollamaOptions{Temperature: 0}}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("safety: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("safety: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("safety: llama guard unreachable: %v", err)
		return &Result{Safe: false, Category: "safety_unavailable"}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("safety: llama guard returned %d: %s", resp.StatusCode, string(body))
		return &Result{Safe: false, Category: "safety_error"}, nil
	}
	var ollamaResp ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("safety: decode response: %w", err)
	}
	return parseLlamaGuardResponse(ollamaResp.Response), nil
}

func parseLlamaGuardResponse(response string) *Result {
	trimmed := strings.TrimSpace(response)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "safe") {
		return &Result{Safe: true}
	}
	category := ""
	parts := strings.SplitN(trimmed, "\n", 2)
	if len(parts) > 1 {
		category = strings.TrimSpace(parts[1])
	}
	if category == "" {
		category = "unsafe"
	}
	return &Result{Safe: false, Category: category}
}

type ollamaGenerateRequest struct {
	Model   string        `json:"model"`
	Prompt  string        `json:"prompt"`
	Stream  bool          `json:"stream"`
	Options ollamaOptions `json:"options"`
}
type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
}
type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func Check(ctx context.Context, checker Checker, message string) (safe bool, category string, err error) {
	result, err := checker.Check(ctx, message)
	if err != nil {
		return false, "", err
	}
	return result.Safe, result.Category, nil
}
