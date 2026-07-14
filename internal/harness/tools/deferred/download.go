package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// DownloadTool returns a deferred tool for downloading URL content into a workspace file.
func DownloadTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "download",
		Description:  descriptions.Load("download"),
		Action:       tools.ActionDownload,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierCore,
		Tags:         []string{"http", "download", "file"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":             map[string]any{"type": "string"},
				"file_path":       map[string]any{"type": "string"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
				"max_bytes":       map[string]any{"type": "integer", "minimum": 1},
			},
			"required": []string{"url", "file_path"},
		},
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			URL            string `json:"url"`
			FilePath       string `json:"file_path"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			MaxBytes       int    `json:"max_bytes"`
		}{TimeoutSeconds: 20, MaxBytes: 50 * 1024 * 1024}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse download args: %w", err)
		}
		if strings.TrimSpace(args.URL) == "" {
			return "", fmt.Errorf("url is required")
		}
		if strings.TrimSpace(args.FilePath) == "" {
			return "", fmt.Errorf("file_path is required")
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
			args.MaxBytes = 1024 * 1024
		}
		if args.MaxBytes > 100*1024*1024 {
			args.MaxBytes = 100 * 1024 * 1024
		}

		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.FilePath, opts.SandboxScope)
		if err != nil {
			return "", err
		}

		tctx, cancel := context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(tctx, http.MethodGet, args.URL, nil)
		if err != nil {
			return "", fmt.Errorf("build download request: %w", err)
		}
		res, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("download request failed: %w", err)
		}
		defer res.Body.Close()

		body, err := io.ReadAll(io.LimitReader(res.Body, int64(args.MaxBytes+1)))
		if err != nil {
			return "", fmt.Errorf("read download body: %w", err)
		}
		truncated := len(body) > args.MaxBytes
		if truncated {
			body = body[:args.MaxBytes]
		}

		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", fmt.Errorf("create parent dir: %w", err)
		}
		if err := os.WriteFile(absPath, body, 0o644); err != nil {
			return "", fmt.Errorf("write downloaded file: %w", err)
		}
		version := tools.FileVersionFromBytes(body)
		result := map[string]any{
			"url":           args.URL,
			"file_path":     tools.NormalizeRelPath(workspaceRoot, absPath),
			"bytes_written": len(body),
			"status_code":   res.StatusCode,
			"content_type":  res.Header.Get("Content-Type"),
			"truncated":     truncated,
			"version":       version,
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
