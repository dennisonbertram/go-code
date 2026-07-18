package deferred

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// commitRecord is a structured representation of a single git commit.
type commitRecord struct {
	Hash        string `json:"hash"`
	ShortHash   string `json:"short_hash"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	Date        string `json:"date"`
	Subject     string `json:"subject"`
	Body        string `json:"body,omitempty"`
	MatchType   string `json:"match_type,omitempty"`
	Diff        string `json:"diff,omitempty"`
}

// gitLogFormat is the --pretty=format string used to parse git log output.
// Fields are separated by the unit separator character (0x1F) which never
// appears in commit metadata, and records are separated by the record
// separator character (0x1E).
const gitLogFormat = `--pretty=format:%x1E%H%x1F%h%x1F%aN%x1F%aE%x1F%aI%x1F%s%x1F%b%x1E`

// parseCommitLog parses the output of git log using gitLogFormat.
// It returns a slice of commitRecords in the order they appear in the output.
func parseCommitLog(output string) []commitRecord {
	var records []commitRecord
	// Split on record separator
	parts := strings.Split(output, "\x1E")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "\x1F")
		if len(fields) < 6 {
			continue
		}
		body := ""
		if len(fields) >= 7 {
			body = strings.TrimSpace(fields[6])
			// Truncate body at 500 bytes for token efficiency
			if len(body) > 500 {
				body = body[:500] + "..."
			}
		}
		records = append(records, commitRecord{
			Hash:        strings.TrimSpace(fields[0]),
			ShortHash:   strings.TrimSpace(fields[1]),
			AuthorName:  strings.TrimSpace(fields[2]),
			AuthorEmail: strings.TrimSpace(fields[3]),
			Date:        strings.TrimSpace(fields[4]),
			Subject:     strings.TrimSpace(fields[5]),
			Body:        body,
		})
	}
	return records
}

// GitLogSearchTool returns a deferred tool that searches commit history by
// keyword in commit messages and/or diff content.
func GitLogSearchTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_log_search",
		Description:  descriptions.Load("git_log_search"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"git", "history", "search", "commits", "log"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Literal string to search for"},
				"mode":        map[string]any{"type": "string", "enum": []string{"message", "pickaxe", "both"}, "description": "Search scope: message, pickaxe, or both (default)"},
				"path":        map[string]any{"type": "string", "description": "Limit to a file or directory (optional)"},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Max commits to return (default 20)"},
				"since":       map[string]any{"type": "string", "description": "Limit to commits after this date or ref (optional)"},
			},
			"required": []string{"query"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Query      string `json:"query"`
			Mode       string `json:"mode"`
			Path       string `json:"path"`
			MaxResults int    `json:"max_results"`
			Since      string `json:"since"`
		}
		args.Mode = "both"
		args.MaxResults = 20
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_log_search args: %w", err)
			}
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		if args.Mode == "" {
			args.Mode = "both"
		}
		if args.MaxResults <= 0 {
			args.MaxResults = 20
		}
		if args.MaxResults > 100 {
			args.MaxResults = 100
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}

		// Build optional path suffix
		var pathSuffix []string
		if strings.TrimSpace(args.Path) != "" {
			absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			rel := tools.NormalizeRelPath(workspaceRoot, absPath)
			pathSuffix = []string{"--", filepath.FromSlash(rel)}
		}

		seen := make(map[string]bool)
		var results []commitRecord

		baseArgs := []string{"-C", absRoot, "log", "--all"}
		if strings.TrimSpace(args.Since) != "" {
			baseArgs = append(baseArgs, "--since="+args.Since)
		}

		runSearch := func(extraArgs []string, matchType string) error {
			cmdArgs := append(baseArgs, gitLogFormat) //nolint:gocritic
			cmdArgs = append(cmdArgs, extraArgs...)
			cmdArgs = append(cmdArgs, pathSuffix...)
			output, _, _, err := tools.RunCommand(ctx, 30*time.Second, "git", cmdArgs...)
			if err != nil {
				return fmt.Errorf("git log search (%s): %w", matchType, err)
			}
			commits := parseCommitLog(output)
			for _, c := range commits {
				if !seen[c.Hash] {
					seen[c.Hash] = true
					c.MatchType = matchType
					results = append(results, c)
				}
			}
			return nil
		}

		switch args.Mode {
		case "message":
			if err := runSearch([]string{"--grep=" + args.Query}, "message"); err != nil {
				return "", err
			}
		case "pickaxe":
			if err := runSearch([]string{"-S", args.Query}, "pickaxe"); err != nil {
				return "", err
			}
		default: // "both"
			// Run message search first, then pickaxe; deduplicate by hash
			if err := runSearch([]string{"--grep=" + args.Query}, "message"); err != nil {
				return "", err
			}
			if err := runSearch([]string{"-S", args.Query}, "pickaxe"); err != nil {
				return "", err
			}
		}

		totalFound := len(results)
		truncated := false
		if len(results) > args.MaxResults {
			results = results[:args.MaxResults]
			truncated = true
		}

		return tools.MarshalToolResult(map[string]any{
			"commits":     results,
			"total_found": totalFound,
			"truncated":   truncated,
			"query":       args.Query,
			"mode":        args.Mode,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// GitFileHistoryTool returns a deferred tool that shows the commit timeline for
// a specific file or directory.
func GitFileHistoryTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_file_history",
		Description:  descriptions.Load("git_file_history"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"git", "history", "file", "evolution", "timeline"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "File or directory path relative to workspace root"},
				"max_commits": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum commits to return (default 20)"},
				"follow":      map[string]any{"type": "boolean", "description": "Follow file across renames (default true)"},
				"show_diffs":  map[string]any{"type": "boolean", "description": "Include diff for each commit (default false)"},
				"since":       map[string]any{"type": "string", "description": "Limit to commits after this date or ref (optional)"},
			},
			"required": []string{"path"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path       string `json:"path"`
			MaxCommits int    `json:"max_commits"`
			Follow     *bool  `json:"follow"`
			ShowDiffs  bool   `json:"show_diffs"`
			Since      string `json:"since"`
		}
		args.MaxCommits = 20
		followDefault := true
		args.Follow = &followDefault
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_file_history args: %w", err)
			}
		}
		if strings.TrimSpace(args.Path) == "" {
			return "", fmt.Errorf("path is required")
		}
		if args.MaxCommits <= 0 {
			args.MaxCommits = 20
		}
		if args.MaxCommits > 200 {
			args.MaxCommits = 200
		}
		follow := true
		if args.Follow != nil {
			follow = *args.Follow
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}
		rel := tools.NormalizeRelPath(workspaceRoot, absPath)

		cmdArgs := []string{"-C", absRoot, "log"}
		if follow {
			cmdArgs = append(cmdArgs, "--follow")
		}
		if strings.TrimSpace(args.Since) != "" {
			cmdArgs = append(cmdArgs, "--since="+args.Since)
		}
		cmdArgs = append(cmdArgs, fmt.Sprintf("-n%d", args.MaxCommits))
		cmdArgs = append(cmdArgs, gitLogFormat)
		if args.ShowDiffs {
			cmdArgs = append(cmdArgs, "-p", "--unified=3")
		}
		cmdArgs = append(cmdArgs, "--", filepath.FromSlash(rel))

		output, _, _, err := tools.RunCommand(ctx, 30*time.Second, "git", cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("git file history: %w", err)
		}

		var commits []commitRecord
		if args.ShowDiffs {
			// When -p is active, the format interleaves the log format with patch data.
			// We need to split on record separators first, then extract diffs.
			commits = parseCommitLogWithDiffs(output)
		} else {
			commits = parseCommitLog(output)
		}

		totalCommits := len(commits)
		truncated := totalCommits >= args.MaxCommits

		return tools.MarshalToolResult(map[string]any{
			"file":          rel,
			"follow":        follow,
			"commits":       commits,
			"total_commits": totalCommits,
			"truncated":     truncated,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// parseCommitLogWithDiffs parses git log -p output where each commit block
// contains the formatted header followed by the diff patch.
func parseCommitLogWithDiffs(output string) []commitRecord {
	const maxDiffBytes = 4096

	var records []commitRecord
	// Split on record separator
	parts := strings.Split(output, "\x1E")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Find the first \x1F which marks the start of the structured data,
		// but the record separator (\x1E) delineates records. The format
		// puts the separator before the hash, so within each part the first
		// non-whitespace content is the structured header up until the next
		// \x1E. Any content after the last \x1F field and before the next
		// \x1E record separator is the diff.
		//
		// The gitLogFormat ends with %b%x1E, meaning body then record separator.
		// With -p, git appends the diff after the record separator. However,
		// since we split on \x1E, the diff appears as a separate "part".
		// We handle this by detecting that a part does not contain \x1F
		// (i.e., it's a diff block) and attaching it to the last record.
		if !strings.Contains(part, "\x1F") {
			// This is a diff block — attach to last record if present
			if len(records) > 0 {
				diff := strings.TrimSpace(part)
				if len(diff) > maxDiffBytes {
					diff = diff[:maxDiffBytes] + "\n... (truncated)"
				}
				records[len(records)-1].Diff = diff
			}
			continue
		}

		fields := strings.Split(part, "\x1F")
		if len(fields) < 6 {
			continue
		}
		body := ""
		if len(fields) >= 7 {
			body = strings.TrimSpace(fields[6])
			if len(body) > 500 {
				body = body[:500] + "..."
			}
		}
		records = append(records, commitRecord{
			Hash:        strings.TrimSpace(fields[0]),
			ShortHash:   strings.TrimSpace(fields[1]),
			AuthorName:  strings.TrimSpace(fields[2]),
			AuthorEmail: strings.TrimSpace(fields[3]),
			Date:        strings.TrimSpace(fields[4]),
			Subject:     strings.TrimSpace(fields[5]),
			Body:        body,
		})
	}
	return records
}

// blameLineRecord is a single line from git blame --porcelain output.
type blameLineRecord struct {
	LineNumber    int    `json:"line_number"`
	Content       string `json:"content"`
	CommitHash    string `json:"commit_hash"`
	ShortHash     string `json:"short_hash"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
	Date          string `json:"date"`
	CommitSubject string `json:"commit_subject"`
	CommitBody    string `json:"commit_body,omitempty"`
}

// GitBlameContextTool returns a deferred tool that shows per-line blame for a
// file with full commit context (author, date, commit message).
func GitBlameContextTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_blame_context",
		Description:  descriptions.Load("git_blame_context"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"git", "blame", "authorship", "history", "archaeology"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "File path relative to workspace root"},
				"start_line": map[string]any{"type": "integer", "minimum": 1, "description": "First line to blame (1-indexed, optional)"},
				"end_line":   map[string]any{"type": "integer", "minimum": 1, "description": "Last line to blame inclusive (required when start_line is set)"},
				"rev":        map[string]any{"type": "string", "description": "Revision to blame at (default HEAD)"},
			},
			"required": []string{"path"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
			Rev       string `json:"rev"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_blame_context args: %w", err)
			}
		}
		if strings.TrimSpace(args.Path) == "" {
			return "", fmt.Errorf("path is required")
		}
		if args.Rev == "" {
			args.Rev = "HEAD"
		}
		if err := tools.ValidateGitRef(args.Rev); err != nil {
			return "", err
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}
		rel := tools.NormalizeRelPath(workspaceRoot, absPath)

		cmdArgs := []string{"-C", absRoot, "blame", "--porcelain"}
		if args.StartLine > 0 && args.EndLine > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("-L%d,%d", args.StartLine, args.EndLine))
		}
		cmdArgs = append(cmdArgs, args.Rev, "--", filepath.FromSlash(rel))

		output, _, _, err := tools.RunCommand(ctx, 20*time.Second, "git", cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("git blame: %w", err)
		}

		lines, uniqueHashes := parsePorcelainBlame(output)

		// Fetch commit messages for each unique hash
		commitInfo := make(map[string]struct{ subject, body string })
		for hash := range uniqueHashes {
			if hash == "0000000000000000000000000000000000000000" {
				continue // uncommitted lines
			}
			showArgs := []string{"-C", absRoot, "show", "--format=%s\x1F%b", "--no-patch", hash}
			showOut, _, _, showErr := tools.RunCommand(ctx, 10*time.Second, "git", showArgs...)
			if showErr == nil {
				parts := strings.SplitN(strings.TrimSpace(showOut), "\x1F", 2)
				subject := ""
				body := ""
				if len(parts) >= 1 {
					subject = strings.TrimSpace(parts[0])
				}
				if len(parts) >= 2 {
					body = strings.TrimSpace(parts[1])
					if len(body) > 500 {
						body = body[:500] + "..."
					}
				}
				commitInfo[hash] = struct{ subject, body string }{subject, body}
			}
		}

		// Enrich lines with commit messages
		var result []blameLineRecord
		for _, line := range lines {
			info := commitInfo[line.CommitHash]
			line.CommitSubject = info.subject
			line.CommitBody = info.body
			result = append(result, line)
		}

		return tools.MarshalToolResult(map[string]any{
			"file":           rel,
			"rev":            args.Rev,
			"lines":          result,
			"unique_commits": len(uniqueHashes),
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// parsePorcelainBlame parses the output of `git blame --porcelain`.
//
// The porcelain format for each line is:
//
//	<hash> <orig_line> <final_line> [<num_lines>]
//	author <name>
//	author-mail <email>
//	author-time <unix_timestamp>
//	author-tz <timezone>
//	... (more fields)
//	filename <path>
//	\t<line content>
//
// A hash appears once per first occurrence, then subsequent lines with the
// same commit only emit the hash and line numbers on the header line.
// We accumulate per-hash metadata from the first occurrence.
func parsePorcelainBlame(output string) ([]blameLineRecord, map[string]bool) {
	type commitMeta struct {
		authorName  string
		authorEmail string
		authorDate  string
	}

	knownCommits := make(map[string]*commitMeta)
	uniqueHashes := make(map[string]bool)
	var lines []blameLineRecord

	scanner := bufio.NewScanner(strings.NewReader(output))

	var currentHash string
	var currentFinalLine int

	for scanner.Scan() {
		text := scanner.Text()

		// Header line: <40-char-hash> <orig_line> <final_line> [<num_lines>]
		if len(text) >= 40 && !strings.HasPrefix(text, "\t") && !strings.Contains(text[:1], " ") {
			parts := strings.Fields(text)
			if len(parts) >= 3 {
				hash := parts[0]
				finalLine, _ := strconv.Atoi(parts[2])
				currentHash = hash
				currentFinalLine = finalLine
				uniqueHashes[hash] = true
				if knownCommits[hash] == nil {
					knownCommits[hash] = &commitMeta{}
				}
			}
			continue
		}

		if strings.HasPrefix(text, "author ") {
			if meta := knownCommits[currentHash]; meta != nil && meta.authorName == "" {
				meta.authorName = strings.TrimPrefix(text, "author ")
			}
			continue
		}
		if strings.HasPrefix(text, "author-mail ") {
			if meta := knownCommits[currentHash]; meta != nil && meta.authorEmail == "" {
				email := strings.TrimPrefix(text, "author-mail ")
				email = strings.Trim(email, "<>")
				meta.authorEmail = email
			}
			continue
		}
		if strings.HasPrefix(text, "author-time ") {
			if meta := knownCommits[currentHash]; meta != nil && meta.authorDate == "" {
				ts, _ := strconv.ParseInt(strings.TrimPrefix(text, "author-time "), 10, 64)
				meta.authorDate = time.Unix(ts, 0).UTC().Format(time.RFC3339)
			}
			continue
		}

		// Line content starts with a tab
		if strings.HasPrefix(text, "\t") && currentHash != "" {
			content := text[1:] // strip leading tab
			meta := knownCommits[currentHash]
			rec := blameLineRecord{
				LineNumber: currentFinalLine,
				Content:    content,
				CommitHash: currentHash,
				ShortHash:  currentHash[:7],
			}
			if meta != nil {
				rec.AuthorName = meta.authorName
				rec.AuthorEmail = meta.authorEmail
				rec.Date = meta.authorDate
			}
			lines = append(lines, rec)
			currentHash = ""
			currentFinalLine = 0
		}
	}

	return lines, uniqueHashes
}

// GitDiffRangeTool returns a deferred tool that shows the diff between two
// arbitrary git refs.
func GitDiffRangeTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_diff_range",
		Description:  descriptions.Load("git_diff_range"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"git", "diff", "history", "compare", "range"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from":      map[string]any{"type": "string", "description": "Base ref (commit hash, branch, tag)"},
				"to":        map[string]any{"type": "string", "description": "Target ref (default HEAD)"},
				"path":      map[string]any{"type": "string", "description": "Limit to file or directory (optional)"},
				"stat_only": map[string]any{"type": "boolean", "description": "Return only file change summary, not full diff (default false)"},
				"max_bytes": map[string]any{"type": "integer", "description": "Truncate diff at this byte limit (default 262144)"},
			},
			"required": []string{"from"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Path     string `json:"path"`
			StatOnly bool   `json:"stat_only"`
			MaxBytes int    `json:"max_bytes"`
		}
		args.To = "HEAD"
		args.MaxBytes = 256 * 1024
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_diff_range args: %w", err)
			}
		}
		if strings.TrimSpace(args.From) == "" {
			return "", fmt.Errorf("from is required")
		}
		if args.To == "" {
			args.To = "HEAD"
		}
		if err := tools.ValidateGitRef(args.From); err != nil {
			return "", err
		}
		if err := tools.ValidateGitRef(args.To); err != nil {
			return "", err
		}
		if args.MaxBytes <= 0 {
			args.MaxBytes = 256 * 1024
		}
		if args.MaxBytes > 1024*1024 {
			args.MaxBytes = 1024 * 1024
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}

		rangeSpec := args.From + ".." + args.To

		// Build optional path suffix
		var pathSuffix []string
		if strings.TrimSpace(args.Path) != "" {
			absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			rel := tools.NormalizeRelPath(workspaceRoot, absPath)
			pathSuffix = []string{"--", filepath.FromSlash(rel)}
		}

		// Always get the stat
		statArgs := append([]string{"-C", absRoot, "diff", "--stat", rangeSpec}, pathSuffix...)
		statOutput, _, _, err := tools.RunCommand(ctx, 30*time.Second, "git", statArgs...)
		if err != nil {
			return "", fmt.Errorf("git diff --stat: %w", err)
		}

		// Parse files_changed, insertions, deletions from stat summary line
		filesChanged, insertions, deletions := parseStatSummary(statOutput)

		diffOutput := ""
		truncated := false
		if !args.StatOnly {
			diffArgs := append([]string{"-C", absRoot, "diff", rangeSpec}, pathSuffix...)
			diffOut, _, _, err := tools.RunCommand(ctx, 30*time.Second, "git", diffArgs...)
			if err != nil {
				return "", fmt.Errorf("git diff: %w", err)
			}
			diffOutput = diffOut
			if len(diffOutput) > args.MaxBytes {
				diffOutput = diffOutput[:args.MaxBytes]
				truncated = true
			}
		}

		return tools.MarshalToolResult(map[string]any{
			"from":          args.From,
			"to":            args.To,
			"diff":          diffOutput,
			"stat":          strings.TrimSpace(statOutput),
			"files_changed": filesChanged,
			"insertions":    insertions,
			"deletions":     deletions,
			"truncated":     truncated,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// parseStatSummary extracts files_changed, insertions, and deletions from the
// final summary line of git diff --stat output.
// Example: " 3 files changed, 42 insertions(+), 12 deletions(-)"
func parseStatSummary(stat string) (filesChanged, insertions, deletions int) {
	lines := strings.Split(stat, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "changed") {
			parts := strings.Split(line, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				fields := strings.Fields(part)
				if len(fields) >= 2 {
					n, _ := strconv.Atoi(fields[0])
					switch {
					case strings.Contains(fields[1], "changed"):
						filesChanged = n
					case strings.Contains(fields[1], "insertion"):
						insertions = n
					case strings.Contains(fields[1], "deletion"):
						deletions = n
					}
				}
			}
			break
		}
	}
	return
}

// authorRecord is a single entry in the git_contributor_context output.
type authorRecord struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	CommitCount int    `json:"commit_count"`
}

// GitContributorContextTool returns a deferred tool that shows the top
// contributors for a file or directory.
func GitContributorContextTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_contributor_context",
		Description:  descriptions.Load("git_contributor_context"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"git", "contributors", "authorship", "ownership", "history"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "File or directory (optional; omit for whole repo)"},
				"max_authors": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum number of authors to return (default 10)"},
				"since":       map[string]any{"type": "string", "description": "Limit to commits after this date or ref (optional)"},
			},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path       string `json:"path"`
			MaxAuthors int    `json:"max_authors"`
			Since      string `json:"since"`
		}
		args.MaxAuthors = 10
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_contributor_context args: %w", err)
			}
		}
		if args.MaxAuthors <= 0 {
			args.MaxAuthors = 10
		}
		if args.MaxAuthors > 20 {
			args.MaxAuthors = 20
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}

		// Use git log to get author name and email for every commit, then
		// group by email (names can drift), count, and sort.
		cmdArgs := []string{"-C", absRoot, "log", "--pretty=format:%aN\x1F%aE"}
		if strings.TrimSpace(args.Since) != "" {
			cmdArgs = append(cmdArgs, "--since="+args.Since)
		}
		if strings.TrimSpace(args.Path) != "" {
			absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			rel := tools.NormalizeRelPath(workspaceRoot, absPath)
			cmdArgs = append(cmdArgs, "--", filepath.FromSlash(rel))
		}

		output, _, _, err := tools.RunCommand(ctx, 30*time.Second, "git", cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("git contributor context: %w", err)
		}

		// Aggregate by email
		type authorEntry struct {
			name  string
			count int
		}
		byEmail := make(map[string]*authorEntry)
		scanner := bufio.NewScanner(strings.NewReader(output))
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, "\x1F", 2)
			if len(parts) != 2 {
				continue
			}
			name := strings.TrimSpace(parts[0])
			email := strings.TrimSpace(parts[1])
			if email == "" {
				continue
			}
			if byEmail[email] == nil {
				byEmail[email] = &authorEntry{name: name}
			}
			byEmail[email].count++
			// Keep the most recently seen name
			if byEmail[email].name == "" {
				byEmail[email].name = name
			}
		}

		// Sort by commit count descending
		type sortEntry struct {
			email string
			entry *authorEntry
		}
		sorted := make([]sortEntry, 0, len(byEmail))
		for email, entry := range byEmail {
			sorted = append(sorted, sortEntry{email, entry})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].entry.count > sorted[j].entry.count
		})

		// Truncate to max_authors
		if len(sorted) > args.MaxAuthors {
			sorted = sorted[:args.MaxAuthors]
		}

		authors := make([]authorRecord, 0, len(sorted))
		for _, s := range sorted {
			authors = append(authors, authorRecord{
				Name:        s.entry.name,
				Email:       s.email,
				CommitCount: s.entry.count,
			})
		}

		return tools.MarshalToolResult(map[string]any{
			"path":    args.Path,
			"authors": authors,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}
