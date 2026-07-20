package transcriptexport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptEntry represents a single entry in the conversation transcript.
type TranscriptEntry struct {
	Role      string // "user", "assistant", "tool"
	Content   string
	Timestamp time.Time
	ToolName  string // only for role=="tool"
}

// Exporter writes conversation transcripts to markdown files.
// It is a pure value type — safe to copy and use from multiple goroutines
// as long as each call to Export uses a distinct receiver (value semantics).
type Exporter struct {
	OutputDir string // default: runtime-safe transcript directory
}

// DefaultOutputDir returns a runtime-safe directory for transcript exports.
// It prefers the OS cache directory, then falls back to ~/.harness/transcripts,
// then finally the OS temp directory.
func DefaultOutputDir() string {
	var candidates []string
	if cacheDir, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, filepath.Join(cacheDir, "harness", "transcripts"))
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(homeDir, ".harness", "transcripts"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "harness", "transcripts"))
	return selectRuntimeSafeOutputDir(candidates)
}

// NewExporter creates an Exporter that writes files to outputDir.
// If outputDir is empty, DefaultOutputDir() is used.
func NewExporter(outputDir string) Exporter {
	if outputDir == "" {
		outputDir = DefaultOutputDir()
	}
	return Exporter{OutputDir: outputDir}
}

func selectRuntimeSafeOutputDir(candidates []string) string {
	fallback := filepath.Join(os.TempDir(), "harness", "transcripts")
	for _, candidate := range candidates {
		if usableDir, ok := ensureWritableDir(candidate); ok {
			return usableDir
		}
	}
	if usableDir, ok := ensureWritableDir(fallback); ok {
		return usableDir
	}
	return fallback
}

func ensureWritableDir(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	usableDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", false
	}
	if err := os.MkdirAll(usableDir, 0o755); err != nil {
		return "", false
	}
	probe, err := os.CreateTemp(usableDir, ".transcript-export-probe-*")
	if err != nil {
		return "", false
	}
	probePath := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return "", false
	}
	if err := os.Remove(probePath); err != nil {
		return "", false
	}
	return usableDir, true
}

// Export writes entries to a markdown file in OutputDir.
// The filename is transcript-YYYYMMDD-HHMMSS.md using the current local time.
// It returns the absolute path of the written file, or an error.
func (e Exporter) Export(entries []TranscriptEntry) (string, error) {
	now := time.Now()
	filename := fmt.Sprintf("transcript-%s.md", now.Format("20060102-150405"))

	// Resolve OutputDir to an absolute, clean path to prevent path traversal.
	outputDir, err := filepath.Abs(filepath.Clean(e.OutputDir))
	if err != nil {
		return "", fmt.Errorf("transcriptexport: resolve output directory: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("transcriptexport: create output directory: %w", err)
	}

	path := filepath.Join(outputDir, filename)

	var sb strings.Builder
	sb.WriteString("# Conversation Transcript\n")
	sb.WriteString(fmt.Sprintf("Exported: %s\n", now.Format("2006-01-02 15:04:05")))

	for _, entry := range entries {
		sb.WriteString("\n---\n\n")
		timeStr := entry.Timestamp.Format("3:04 PM")
		switch entry.Role {
		case "tool":
			name := entry.ToolName
			if name == "" {
				name = "tool"
			}
			sb.WriteString(fmt.Sprintf("## Tool: %s [%s]\n", name, timeStr))
		case "user":
			sb.WriteString(fmt.Sprintf("## User [%s]\n", timeStr))
		case "assistant":
			sb.WriteString(fmt.Sprintf("## Assistant [%s]\n", timeStr))
		default:
			sb.WriteString(fmt.Sprintf("## %s [%s]\n", entry.Role, timeStr))
		}
		sb.WriteString(entry.Content)
		sb.WriteString("\n")
	}

	if len(entries) > 0 {
		sb.WriteString("\n---\n")
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("transcriptexport: write file: %w", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}
