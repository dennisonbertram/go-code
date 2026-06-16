// Package safety provides input screening for user messages before they are
// forwarded to the agent harness. It defines a Screener interface and a
// Llama Guard-backed implementation that calls an external safety classifier
// over HTTP.
//
// By default, no screener is configured and all messages pass through.
// When SAFETY_SCREENER_URL is set, messages are screened before processing.
package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Result holds the outcome of a safety screening call.
type Result struct {
	// Safe is true when the content is safe to process.
	Safe bool
	// Category is the highest-priority violation category, if any.
	// Typical values: "S1", "S2", "S3", "S4", "S5", "S6", "S7", "S8", "S9".
	Category string
	// Reason is a human-readable explanation of the verdict.
	Reason string
}

// Screener screens user input for policy violations.
type Screener interface {
	Screen(ctx context.Context, text string) (*Result, error)
}

// LlamaGuardScreener calls an external Llama Guard HTTP endpoint to classify
// user input against safety categories. It implements fail-open semantics:
// if the external service is unreachable, returns an error, times out, or
// returns a non-200 status, the message is treated as safe to avoid DoSing
// the service when the safety dependency is down.
type LlamaGuardScreener struct {
	endpoint   string
	httpClient *http.Client
}

// NewLlamaGuardScreener creates a LlamaGuardScreener that posts text to the
// given HTTP endpoint. The endpoint is expected to accept a JSON body of the
// form {"text": "..."} and return {"safe": bool, "category": "...",
// "reason": "..."}.
func NewLlamaGuardScreener(endpoint string) *LlamaGuardScreener {
	return &LlamaGuardScreener{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Screen sends text to the Llama Guard endpoint for classification. It
// implements fail-open semantics: any error communicating with the screener
// results in a "safe" result so that the gateway remains available when the
// safety service is down.
func (s *LlamaGuardScreener) Screen(ctx context.Context, text string) (*Result, error) {
	reqBody, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		log.Printf("safety: marshal request: %v", err)
		return &Result{Safe: true}, nil // fail-open
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("safety: create request: %v", err)
		return &Result{Safe: true}, nil // fail-open
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("safety: call screener endpoint: %v", err)
		return &Result{Safe: true}, nil // fail-open
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("safety: screener returned status %d", resp.StatusCode)
		return &Result{Safe: true}, nil // fail-open
	}

	var result struct {
		Safe     bool   `json:"safe"`
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("safety: decode response: %v", err)
		return &Result{Safe: true}, nil // fail-open
	}

	if !result.Safe {
		log.Printf("safety: message flagged as unsafe, category=%s reason=%s", result.Category, result.Reason)
	}

	return &Result{
		Safe:     result.Safe,
		Category: result.Category,
		Reason:   result.Reason,
	}, nil
}

// ParseCategory converts an unsafe result into a human-readable refusal
// message appropriate for sending back to a user.
func ParseCategory(r *Result) string {
	if r == nil || r.Safe {
		return ""
	}
	if r.Reason != "" {
		return fmt.Sprintf("I'm not able to help with that request. (%s)", r.Reason)
	}
	if r.Category != "" {
		return fmt.Sprintf("I'm not able to help with that request. (category: %s)", r.Category)
	}
	return "I'm not able to help with that request."
}
