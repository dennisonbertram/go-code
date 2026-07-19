package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RewindRestoreResult describes the destructive restore that completed.
type RewindRestoreResult struct {
	FilesRestored     int `json:"files_restored"`
	MessagesTruncated int `json:"messages_truncated"`
}

func RewindContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum[:])
}

// RewindFileSnapshot is the pre-edit state of one workspace file. Exists=false
// records that the agent created the file, so rewinding removes it.
type RewindFileSnapshot struct {
	Path         string `json:"path"`
	Content      []byte `json:"-"`
	Exists       bool   `json:"exists"`
	Skipped      bool   `json:"skipped,omitempty"`
	SkipReason   string `json:"skip_reason,omitempty"`
	ExpectedHash string `json:"expected_hash,omitempty"`
}

// CaptureRewindPreImage snapshots every addressable target before the tool runs.
// Failures are returned to the caller, which is required to treat them as a warning.
func CaptureRewindPreImage(ctx context.Context, store RewindStore, point RewindPoint, workspace string, raw []byte) error {
	for _, path := range ExtractRewindPaths(point.Tool, raw) {
		file := RewindFileSnapshot{Path: path}
		contents, err := os.ReadFile(filepath.Join(workspace, path))
		if err == nil {
			file.Exists = true
			file.Content = contents
		} else if !os.IsNotExist(err) {
			return err
		}
		point.Files = append(point.Files, file)
	}
	if len(point.Files) == 0 {
		return nil
	}
	return store.SaveRewindPoint(ctx, point)
}

// RewindPoint groups pre-images captured immediately before a mutating tool call.
type RewindPoint struct {
	ID             string               `json:"id"`
	ConversationID string               `json:"conversation_id"`
	Step           int                  `json:"step"`
	Tool           string               `json:"tool"`
	CreatedAt      time.Time            `json:"created_at"`
	Files          []RewindFileSnapshot `json:"files"`
}

// RewindStore is deliberately optional so existing ConversationStore adapters
// remain source compatible.
type RewindStore interface {
	SaveRewindPoint(context.Context, RewindPoint) error
	ListRewindPoints(context.Context, string) ([]RewindPoint, error)
	RestoreRewindPoint(context.Context, string, string, string, bool) (RewindRestoreResult, error)
}

var rewindPatchPath = regexp.MustCompile(`(?m)^\+\+\+\s+(?:[ab]/)?([^\t\n]+)`) // unified diff destination path

// ExtractRewindPaths obtains the target files from the three file-editing
// tools. Other mutating tools have no reliable filesystem target and are not
// snapshotted; their existing mutation classification still controls gating.
func ExtractRewindPaths(tool string, raw []byte) []string {
	var args struct {
		Path        string `json:"path"`
		FilePath    string `json:"file_path"`
		Patch       string `json:"patch"`
		Diff        string `json:"diff"`
		UnifiedDiff string `json:"unified_diff"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil
	}
	paths := []string{}
	if args.Path != "" {
		paths = append(paths, args.Path)
	} else if args.FilePath != "" {
		paths = append(paths, args.FilePath)
	}
	if tool == "apply_patch" {
		patch := args.Patch
		if patch == "" {
			patch = args.Diff
		}
		if patch == "" {
			patch = args.UnifiedDiff
		}
		for _, m := range rewindPatchPath.FindAllStringSubmatch(patch, -1) {
			if len(m) > 1 && m[1] != "/dev/null" {
				paths = append(paths, m[1])
			}
		}
	}
	set := map[string]bool{}
	out := []string{}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "." && !filepath.IsAbs(path) && !strings.HasPrefix(path, "..") && !set[path] {
			set[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
