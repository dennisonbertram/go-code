package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

type multiEdit struct {
	OldText    string `json:"old_text"`
	OldString  string `json:"old_string"`
	NewText    string `json:"new_text"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type unifiedPatchFile struct {
	Path  string
	Kind  string
	Hunks []unifiedPatchHunk
}

type unifiedPatchHunk struct {
	OldText string
	NewText string
}

// ApplyPatchTool returns a core tool that applies a find/replace patch to a file.
func ApplyPatchTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "apply_patch",
		Description:  descriptions.Load("apply_patch"),
		Action:       tools.ActionWrite,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "relative file path inside workspace"},
				"file_path":   map[string]any{"type": "string", "description": "alias of path"},
				"find":        map[string]any{"type": "string"},
				"replace":     map[string]any{"type": "string"},
				"replace_all": map[string]any{"type": "boolean"},
				"patch":       map[string]any{"type": "string", "description": "unified diff patch payload"},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_text":    map[string]any{"type": "string"},
							"old_string":  map[string]any{"type": "string"},
							"new_text":    map[string]any{"type": "string"},
							"new_string":  map[string]any{"type": "string"},
							"replace_all": map[string]any{"type": "boolean"},
						},
					},
				},
				"expected_version": map[string]any{"type": "string"},
			},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path            string      `json:"path"`
			FilePath        string      `json:"file_path"`
			Find            string      `json:"find"`
			Replace         string      `json:"replace"`
			ReplaceAll      bool        `json:"replace_all"`
			Patch           string      `json:"patch"`
			Edits           []multiEdit `json:"edits"`
			ExpectedVersion string      `json:"expected_version"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse apply_patch args: %w", err)
		}
		if strings.TrimSpace(args.Patch) != "" {
			return applyUnifiedPatch(ctx, workspaceRoot, opts.SandboxScope, args.Patch)
		}
		if args.Path == "" {
			args.Path = args.FilePath
		}
		if args.Path == "" {
			return "", fmt.Errorf("path is required")
		}

		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("read patch file: %w", err)
		}
		original := string(content)
		if args.ExpectedVersion != "" {
			actual := tools.FileVersionFromBytes(content)
			if actual != args.ExpectedVersion {
				return tools.MarshalToolResult(map[string]any{
					"error": map[string]any{
						"code":             "stale_write",
						"path":             args.Path,
						"expected_version": args.ExpectedVersion,
						"actual_version":   actual,
					},
				})
			}
		}

		updated := original
		totalReplacements := 0

		if len(args.Edits) > 0 {
			failed := make([]map[string]any, 0)
			applied := 0
			for i, e := range args.Edits {
				oldText := e.OldText
				if oldText == "" {
					oldText = e.OldString
				}
				newText := e.NewText
				if e.NewString != "" || (e.NewString == "" && e.NewText == "") {
					newText = e.NewString
				}

				if oldText == "" {
					failed = append(failed, map[string]any{"index": i, "error": "old_text is required"})
					continue
				}
				replacements := 0
				if e.ReplaceAll {
					replacements = strings.Count(updated, oldText)
					updated = strings.ReplaceAll(updated, oldText, newText)
				} else {
					if strings.Contains(updated, oldText) {
						replacements = 1
						updated = strings.Replace(updated, oldText, newText, 1)
					}
				}
				if replacements == 0 {
					failed = append(failed, map[string]any{"index": i, "error": "old_text not found"})
					continue
				}
				applied++
				totalReplacements += replacements
			}
			if totalReplacements > 0 {
				if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
					return "", fmt.Errorf("write patched file: %w", err)
				}
			}
			result := map[string]any{
				"path":          tools.NormalizeRelPath(workspaceRoot, absPath),
				"replacements":  totalReplacements,
				"applied_edits": applied,
				"failed_edits":  failed,
				"partial":       len(failed) > 0,
				"version":       tools.FileVersionFromBytes([]byte(updated)),
				"diff":          map[string]any{"before_bytes": len(original), "after_bytes": len(updated), "changed": original != updated},
			}
			return tools.MarshalToolResult(result)
		}

		if args.Find == "" {
			return "", fmt.Errorf("find is required")
		}
		if args.ReplaceAll {
			totalReplacements = strings.Count(updated, args.Find)
			updated = strings.ReplaceAll(updated, args.Find, args.Replace)
		} else {
			if strings.Contains(updated, args.Find) {
				totalReplacements = 1
				updated = strings.Replace(updated, args.Find, args.Replace, 1)
			}
		}
		if totalReplacements == 0 {
			return "", fmt.Errorf("find text not present in %s", args.Path)
		}

		if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
			return "", fmt.Errorf("write patched file: %w", err)
		}

		result := map[string]any{
			"path":         tools.NormalizeRelPath(workspaceRoot, absPath),
			"replacements": totalReplacements,
			"version":      tools.FileVersionFromBytes([]byte(updated)),
			"diff": map[string]any{
				"before_bytes": len(original),
				"after_bytes":  len(updated),
				"changed":      original != updated,
			},
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// isStandardUnifiedDiff reports whether patch looks like standard unified diff
// format (--- a/file / +++ b/file) as opposed to the custom *** Begin Patch format.
func isStandardUnifiedDiff(patch string) bool {
	trimmed := strings.TrimLeft(patch, " \t\r\n")
	return strings.HasPrefix(trimmed, "--- ")
}

func applyUnifiedPatch(ctx context.Context, workspaceRoot string, sandboxScope tools.SandboxScope, patch string) (string, error) {
	var files []unifiedPatchFile
	var err error
	if isStandardUnifiedDiff(patch) {
		files, err = parseStandardUnifiedDiff(patch)
	} else {
		files, err = parseUnifiedPatch(patch)
	}
	if err != nil {
		return "", err
	}

	results := make([]map[string]any, 0, len(files))
	for _, filePatch := range files {
		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, filePatch.Path, sandboxScope)
		if err != nil {
			return "", err
		}

		switch filePatch.Kind {
		case "delete":
			content, err := os.ReadFile(absPath)
			if err != nil {
				return "", fmt.Errorf("read patch file: %w", err)
			}
			if err := os.Remove(absPath); err != nil {
				return "", fmt.Errorf("delete patched file: %w", err)
			}
			results = append(results, map[string]any{
				"path":    tools.NormalizeRelPath(workspaceRoot, absPath),
				"action":  "delete",
				"version": tools.FileVersionFromBytes(nil),
				"diff": map[string]any{
					"before_bytes": len(content),
					"after_bytes":  0,
					"changed":      true,
				},
			})
		case "add":
			updated := ""
			for _, hunk := range filePatch.Hunks {
				updated += hunk.NewText
			}
			if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
				return "", fmt.Errorf("write patched file: %w", err)
			}
			results = append(results, map[string]any{
				"path":    tools.NormalizeRelPath(workspaceRoot, absPath),
				"action":  "add",
				"version": tools.FileVersionFromBytes([]byte(updated)),
				"diff": map[string]any{
					"before_bytes": 0,
					"after_bytes":  len(updated),
					"changed":      true,
				},
			})
		case "update":
			content, err := os.ReadFile(absPath)
			if err != nil {
				return "", fmt.Errorf("read patch file: %w", err)
			}
			original := string(content)
			updated := original
			replacements := 0
			for _, hunk := range filePatch.Hunks {
				if hunk.OldText == "" {
					return "", fmt.Errorf("patch hunk for %s is missing old text", filePatch.Path)
				}
				if !strings.Contains(updated, hunk.OldText) {
					return "", fmt.Errorf("patch hunk not present in %s", filePatch.Path)
				}
				updated = strings.Replace(updated, hunk.OldText, hunk.NewText, 1)
				replacements++
			}
			if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
				return "", fmt.Errorf("write patched file: %w", err)
			}
			results = append(results, map[string]any{
				"path":         tools.NormalizeRelPath(workspaceRoot, absPath),
				"action":       "update",
				"replacements": replacements,
				"version":      tools.FileVersionFromBytes([]byte(updated)),
				"diff": map[string]any{
					"before_bytes": len(original),
					"after_bytes":  len(updated),
					"changed":      original != updated,
				},
			})
		default:
			return "", fmt.Errorf("unsupported patch action %q", filePatch.Kind)
		}
	}

	return tools.MarshalToolResult(map[string]any{"files": results})
}

func parseUnifiedPatch(patch string) ([]unifiedPatchFile, error) {
	lines := strings.Split(patch, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("patch must start with *** Begin Patch")
	}

	files := make([]unifiedPatchFile, 0)
	for i := 1; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case line == "*** End Patch":
			return files, nil
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			filePatch, next, err := parseUnifiedPatchFile(lines, i+1, path, "update")
			if err != nil {
				return nil, err
			}
			files = append(files, filePatch)
			i = next
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			filePatch, next, err := parseUnifiedPatchFile(lines, i+1, path, "add")
			if err != nil {
				return nil, err
			}
			files = append(files, filePatch)
			i = next
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			files = append(files, unifiedPatchFile{Path: path, Kind: "delete"})
			i++
		default:
			return nil, fmt.Errorf("unsupported patch line: %s", line)
		}
	}

	return nil, fmt.Errorf("patch missing *** End Patch")
}

func parseUnifiedPatchFile(lines []string, start int, path, kind string) (unifiedPatchFile, int, error) {
	filePatch := unifiedPatchFile{Path: path, Kind: kind}
	i := start
	for i < len(lines) {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** "):
			return filePatch, i, nil
		case strings.HasPrefix(line, "@@"):
			hunk, next, err := parseUnifiedPatchHunk(lines, i+1)
			if err != nil {
				return unifiedPatchFile{}, 0, err
			}
			filePatch.Hunks = append(filePatch.Hunks, hunk)
			i = next
		case kind == "add" && strings.HasPrefix(line, "+"):
			hunk, next, err := parseUnifiedPatchHunk(lines, i)
			if err != nil {
				return unifiedPatchFile{}, 0, err
			}
			filePatch.Hunks = append(filePatch.Hunks, hunk)
			i = next
		case strings.TrimSpace(line) == "":
			i++
		default:
			return unifiedPatchFile{}, 0, fmt.Errorf("unexpected patch content for %s: %s", path, line)
		}
	}
	return unifiedPatchFile{}, 0, fmt.Errorf("patch for %s missing terminator", path)
}

func parseUnifiedPatchHunk(lines []string, start int) (unifiedPatchHunk, int, error) {
	var oldBuilder strings.Builder
	var newBuilder strings.Builder

	i := start
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "*** ") {
			break
		}
		if strings.HasPrefix(line, "\\ No newline at end of file") {
			i++
			continue
		}
		if line == "" {
			oldBuilder.WriteByte('\n')
			newBuilder.WriteByte('\n')
			i++
			continue
		}

		prefix := line[0]
		body := line[1:]
		switch prefix {
		case ' ':
			oldBuilder.WriteString(body)
			oldBuilder.WriteByte('\n')
			newBuilder.WriteString(body)
			newBuilder.WriteByte('\n')
		case '-':
			oldBuilder.WriteString(body)
			oldBuilder.WriteByte('\n')
		case '+':
			newBuilder.WriteString(body)
			newBuilder.WriteByte('\n')
		default:
			return unifiedPatchHunk{}, 0, fmt.Errorf("unexpected hunk line: %s", line)
		}
		i++
	}

	return unifiedPatchHunk{
		OldText: oldBuilder.String(),
		NewText: newBuilder.String(),
	}, i, nil
}

// parseStandardUnifiedDiff parses a standard unified diff (as produced by git diff,
// diff -u, or most LLMs) into the internal patch file representation.
//
// The format is:
//
//	--- a/path/to/file       (or --- /dev/null for new files)
//	+++ b/path/to/file       (or +++ /dev/null for deletions)
//	@@ -old_start,old_count +new_start,new_count @@ optional context
//	 context line
//	-removed line
//	+added line
//	...
//
// Multiple file headers may appear sequentially in a single patch string.
func parseStandardUnifiedDiff(patch string) ([]unifiedPatchFile, error) {
	lines := strings.Split(patch, "\n")
	files := make([]unifiedPatchFile, 0)

	i := 0
	for i < len(lines) {
		// Skip blank lines between file entries.
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}

		// Expect "--- ..." header.
		if !strings.HasPrefix(lines[i], "--- ") {
			// Skip unrecognised leading lines (e.g. "diff --git ..." header).
			i++
			continue
		}
		fromPath := parseStdDiffPath(strings.TrimPrefix(lines[i], "--- "))
		i++

		if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return nil, fmt.Errorf("expected +++ header after --- at line %d", i)
		}
		toPath := parseStdDiffPath(strings.TrimPrefix(lines[i], "+++ "))
		i++

		// Determine the kind of change.
		kind := "update"
		path := toPath
		switch {
		case fromPath == "/dev/null":
			kind = "add"
			path = toPath
		case toPath == "/dev/null":
			kind = "delete"
			path = fromPath
		}

		// Collect hunks until the next file header.
		var hunks []unifiedPatchHunk
		for i < len(lines) {
			line := lines[i]
			if strings.HasPrefix(line, "--- ") {
				// Start of the next file entry.
				break
			}
			if strings.HasPrefix(line, "@@ ") {
				// Beginning of a hunk — consume until the next @@ or --- header.
				hunk, next, err := parseStdDiffHunk(lines, i+1)
				if err != nil {
					return nil, fmt.Errorf("hunk in %s: %w", path, err)
				}
				hunks = append(hunks, hunk)
				i = next
				continue
			}
			// Lines before the first @@ (e.g. "diff --git" trailers) — skip.
			i++
		}

		files = append(files, unifiedPatchFile{
			Path:  path,
			Kind:  kind,
			Hunks: hunks,
		})
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no file changes found in standard unified diff")
	}
	return files, nil
}

// parseStdDiffPath extracts the file path from a unified diff header line
// value (i.e. the portion after "--- " or "+++ ").
//
// git diff prefixes paths with "a/" or "b/"; we strip those prefixes.
// The special path "/dev/null" is returned as-is.
func parseStdDiffPath(raw string) string {
	// Trim optional tab-separated timestamp appended by some diff tools.
	if idx := strings.IndexByte(raw, '\t'); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return raw
	}
	// Strip "a/" or "b/" git prefix.
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		return raw[2:]
	}
	return raw
}

// parseStdDiffHunk reads lines starting at start until the next hunk header
// (@@ ...) or file header (--- ...) and returns the accumulated hunk content.
func parseStdDiffHunk(lines []string, start int) (unifiedPatchHunk, int, error) {
	var oldBuilder strings.Builder
	var newBuilder strings.Builder

	i := start
	for i < len(lines) {
		line := lines[i]
		// Stop at the next hunk or file header.
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "--- ") {
			break
		}
		// "\ No newline at end of file" is informational; skip it.
		if strings.HasPrefix(line, "\\ ") {
			i++
			continue
		}
		// An empty line: either a blank context line inside the file, or the
		// trailing empty string produced by strings.Split when the patch ends
		// with '\n'. We stop at the end of the array so we don't treat the
		// trailing sentinel as file content.
		if line == "" {
			if i == len(lines)-1 {
				// Sentinel from trailing newline — stop.
				i++
				break
			}
			// A real blank line in the file — treat as context.
			oldBuilder.WriteByte('\n')
			newBuilder.WriteByte('\n')
			i++
			continue
		}

		prefix := line[0]
		body := line[1:]
		switch prefix {
		case ' ':
			oldBuilder.WriteString(body)
			oldBuilder.WriteByte('\n')
			newBuilder.WriteString(body)
			newBuilder.WriteByte('\n')
		case '-':
			oldBuilder.WriteString(body)
			oldBuilder.WriteByte('\n')
		case '+':
			newBuilder.WriteString(body)
			newBuilder.WriteByte('\n')
		default:
			// Unrecognised line inside a hunk — stop the hunk here.
			break
		}
		i++
	}

	return unifiedPatchHunk{
		OldText: oldBuilder.String(),
		NewText: newBuilder.String(),
	}, i, nil
}
