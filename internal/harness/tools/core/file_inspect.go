package core

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// FileInspectTool returns a core tool that inspects file metadata, type, and content preview.
func FileInspectTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "file_inspect",
		Description:  descriptions.Load("file_inspect"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Mutating:     false,
		Tier:         tools.TierCore,
		Tags:         []string{"file", "inspect", "metadata", "mime", "type", "binary", "preview"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "description": "relative file path inside workspace"},
				"preview_lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "number of text lines to preview (default 20)"},
				"hex_bytes":     map[string]any{"type": "integer", "minimum": 1, "maximum": 1024, "description": "number of binary bytes to hex-dump (default 256)"},
			},
			"required": []string{"path"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path         string `json:"path"`
			PreviewLines int    `json:"preview_lines"`
			HexBytes     int    `json:"hex_bytes"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse file_inspect args: %w", err)
		}
		if args.Path == "" {
			return "", fmt.Errorf("path is required")
		}

		// Apply defaults and clamp.
		if args.PreviewLines <= 0 {
			args.PreviewLines = 20
		}
		if args.PreviewLines > 100 {
			args.PreviewLines = 100
		}
		if args.HexBytes <= 0 {
			args.HexBytes = 256
		}
		if args.HexBytes > 1024 {
			args.HexBytes = 1024
		}

		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return "", fmt.Errorf("stat file: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory, not a file", args.Path)
		}

		// Read first 512 bytes for MIME detection.
		f, err := os.Open(absPath)
		if err != nil {
			return "", fmt.Errorf("open file: %w", err)
		}
		defer f.Close()

		header := make([]byte, 512)
		n, err := io.ReadFull(f, header)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return "", fmt.Errorf("read file header: %w", err)
		}
		header = header[:n]

		mimeType := http.DetectContentType(header)
		// DetectContentType may include charset; keep only the media type for cleanliness
		// when the charset portion is not useful (e.g. "text/plain; charset=utf-8" -> keep as-is
		// since it is informative).

		// A file is text if it is valid UTF-8 AND contains no null bytes.
		// Null bytes are common in binary formats (ELF, PNG, PDF, etc.) but
		// rare in legitimate text files.
		hasNull := false
		for _, b := range header {
			if b == 0 {
				hasNull = true
				break
			}
		}
		isText := utf8.Valid(header) && !hasNull
		encoding := "binary"
		if isText {
			encoding = "utf-8"
		}

		result := map[string]any{
			"path":       tools.NormalizeRelPath(workspaceRoot, absPath),
			"size_bytes": info.Size(),
			"size_human": humanSize(info.Size()),
			"mime_type":  mimeType,
			"encoding":   encoding,
			"extension":  filepath.Ext(absPath),
		}

		if isText {
			// Re-open for line-based reading.
			tf, err := os.Open(absPath)
			if err != nil {
				return "", fmt.Errorf("open file for preview: %w", err)
			}
			defer tf.Close()

			scanner := bufio.NewScanner(tf)
			var previewLines []string
			totalLines := 0
			for scanner.Scan() {
				totalLines++
				if len(previewLines) < args.PreviewLines {
					previewLines = append(previewLines, scanner.Text())
				}
			}
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("scan file lines: %w", err)
			}

			result["preview"] = strings.Join(previewLines, "\n")
			result["preview_lines"] = len(previewLines)
			result["total_lines"] = totalLines
			if len(previewLines) < totalLines {
				result["truncation_warning"] = fmt.Sprintf(
					"showing %d of %d lines; use the read tool for full content",
					len(previewLines), totalLines,
				)
			}
		} else {
			// Binary: hex dump of first N bytes.
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return "", fmt.Errorf("seek file: %w", err)
			}
			buf := make([]byte, args.HexBytes)
			n, err := io.ReadFull(f, buf)
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				return "", fmt.Errorf("read binary preview: %w", err)
			}
			buf = buf[:n]

			result["hex_preview"] = hex.Dump(buf)
			if int64(n) < info.Size() {
				result["truncation_warning"] = fmt.Sprintf(
					"showing hex dump of first %d of %d bytes",
					n, info.Size(),
				)
			}
		}

		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// humanSize formats a byte count into a human-readable string (B, KB, MB, GB).
func humanSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
