package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func fetchTool(client *http.Client, networkAllowlist []string) Tool {
	client = NewGuardedHTTPClient(client, networkAllowlist)
	def := Definition{
		Name:         "fetch",
		Description:  descriptions.Load("fetch"),
		Action:       ActionFetch,
		Mutating:     false,
		ParallelSafe: true,
		Tags:         []string{"fetch", "http", "url", "request", "api"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":             map[string]any{"type": "string"},
				"format":          map[string]any{"type": "string"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
				"max_bytes":       map[string]any{"type": "integer", "minimum": 1, "maximum": 1048576},
			},
			"required": []string{"url"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			URL            string `json:"url"`
			Format         string `json:"format"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			MaxBytes       int    `json:"max_bytes"`
		}{TimeoutSeconds: 20, MaxBytes: 128 * 1024}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse fetch args: %w", err)
		}
		if strings.TrimSpace(args.URL) == "" {
			return "", fmt.Errorf("url is required")
		}
		parsed, err := url.Parse(args.URL)
		if err != nil {
			return "", fmt.Errorf("invalid url: %w", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
		}
		if args.TimeoutSeconds <= 0 {
			args.TimeoutSeconds = 20
		}
		if args.TimeoutSeconds > 120 {
			args.TimeoutSeconds = 120
		}
		if args.MaxBytes <= 0 {
			args.MaxBytes = 128 * 1024
		}
		if args.MaxBytes > 1024*1024 {
			args.MaxBytes = 1024 * 1024
		}

		tctx, cancel := context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(tctx, http.MethodGet, args.URL, nil)
		if err != nil {
			return "", fmt.Errorf("build fetch request: %w", err)
		}
		res, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("fetch request failed: %w", err)
		}
		defer res.Body.Close()

		body, err := io.ReadAll(io.LimitReader(res.Body, int64(args.MaxBytes+1)))
		if err != nil {
			return "", fmt.Errorf("read fetch body: %w", err)
		}
		truncated := len(body) > args.MaxBytes
		if truncated {
			body = body[:args.MaxBytes]
		}

		result := map[string]any{
			"url":          args.URL,
			"status_code":  res.StatusCode,
			"content_type": res.Header.Get("Content-Type"),
			"content":      string(body),
			"truncated":    truncated,
		}
		if args.Format != "" {
			result["format"] = args.Format
		}
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}
