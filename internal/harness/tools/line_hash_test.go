package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// lineHash unit tests
// ---------------------------------------------------------------------------

func TestLineHashReturns12HexChars(t *testing.T) {
	t.Parallel()
	h := lineHash("hello world")
	if len(h) != 12 {
		t.Errorf("expected 12-char hash, got %q (len=%d)", h, len(h))
	}
	for _, c := range h {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("non-hex char %q in hash %q", c, h)
		}
	}
}

func TestLineHashIsDeterministic(t *testing.T) {
	t.Parallel()
	h1 := lineHash("foo bar")
	h2 := lineHash("foo bar")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
}

func TestLineHashTrimsTrailingWhitespace(t *testing.T) {
	t.Parallel()
	h1 := lineHash("hello")
	h2 := lineHash("hello   ")
	h3 := lineHash("hello\t")
	h4 := lineHash("hello\r")
	if h1 != h2 {
		t.Errorf("trailing spaces should not affect hash: %q != %q", h1, h2)
	}
	if h1 != h3 {
		t.Errorf("trailing tab should not affect hash: %q != %q", h1, h3)
	}
	if h1 != h4 {
		t.Errorf("trailing CR should not affect hash: %q != %q", h1, h4)
	}
}

func TestLineHashDifferentForDifferentContent(t *testing.T) {
	t.Parallel()
	h1 := lineHash("line one")
	h2 := lineHash("line two")
	if h1 == h2 {
		t.Errorf("expected different hashes for different content, got same: %q", h1)
	}
}

func TestLineHashEmptyString(t *testing.T) {
	t.Parallel()
	h := lineHash("")
	if len(h) != 12 {
		t.Errorf("expected 12-char hash for empty string, got %q (len=%d)", h, len(h))
	}
}

// ---------------------------------------------------------------------------
// read tool: hash_lines parameter
// ---------------------------------------------------------------------------

func buildReadTool(t *testing.T, workspace string) Tool {
	t.Helper()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	return findToolByName(t, list, "read")
}

func buildEditTool(t *testing.T, workspace string) Tool {
	t.Helper()
	list, err := BuildCatalog(BuildOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	return findToolByName(t, list, "edit")
}

func TestReadHashLinesAddsHashPrefix(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(workspace, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := buildReadTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":       "test.txt",
		"hash_lines": true,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("read with hash_lines: %v", err)
	}

	// The content field should contain hash prefixes like [abc123] 1→alpha
	if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
		t.Errorf("expected hash brackets in output, got: %s", out)
	}

	// Each line hash should be 12 hex chars.
	// Verify for "alpha" — compute its hash and look for it in the output.
	alphaHash := lineHash("alpha")
	if !strings.Contains(out, "["+alphaHash+"]") {
		t.Errorf("expected hash [%s] for 'alpha' in output, got: %s", alphaHash, out)
	}
}

func TestReadHashLinesFormat(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "first line\nsecond line\n"
	if err := os.WriteFile(filepath.Join(workspace, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := buildReadTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":       "f.txt",
		"hash_lines": true,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	h1 := lineHash("first line")
	h2 := lineHash("second line")

	// Format: [hash] linenum→content
	if !strings.Contains(out, "["+h1+"] 1\u2192first line") {
		t.Errorf("expected formatted first line with hash [%s], got: %s", h1, out)
	}
	if !strings.Contains(out, "["+h2+"] 2\u2192second line") {
		t.Errorf("expected formatted second line with hash [%s], got: %s", h2, out)
	}
}

func TestReadHashLinesFalseOrAbsentNoPrefix(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "hello\nworld\n"
	if err := os.WriteFile(filepath.Join(workspace, "hw.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := buildReadTool(t, workspace)

	// Without hash_lines — should not contain square brackets in content
	args, _ := json.Marshal(map[string]any{"path": "hw.txt"})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// content field value should just be plain text — no [hash] prefixes
	helloHash := lineHash("hello")
	if strings.Contains(out, "["+helloHash+"]") {
		t.Errorf("hash prefix should not appear when hash_lines is absent: %s", out)
	}
}

func TestReadHashLinesWithOffsetAndLimit(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(filepath.Join(workspace, "multi.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := buildReadTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":       "multi.txt",
		"hash_lines": true,
		"offset":     1,
		"limit":      2,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("read with offset+limit+hash_lines: %v", err)
	}

	h2 := lineHash("line2")
	h3 := lineHash("line3")
	if !strings.Contains(out, "["+h2+"]") {
		t.Errorf("expected hash for line2, got: %s", out)
	}
	if !strings.Contains(out, "["+h3+"]") {
		t.Errorf("expected hash for line3, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// edit tool: start_line_hash / end_line_hash
// ---------------------------------------------------------------------------

func TestEditStartLineHashReplacesCorrectLine(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(filepath.Join(workspace, "e.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Hash of "line two"
	h := lineHash("line two")

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "e.txt",
		"old_text":        "line two",
		"new_text":        "LINE TWO",
		"start_line_hash": h,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit with start_line_hash: %v", err)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected error in output: %s", out)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "e.txt"))
	if !strings.Contains(string(got), "LINE TWO") {
		t.Errorf("replacement not applied; got: %q", string(got))
	}
}

func TestEditStartLineHashNotFound(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "alpha\nbeta\n"
	if err := os.WriteFile(filepath.Join(workspace, "e2.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "e2.txt",
		"old_text":        "alpha",
		"new_text":        "ALPHA",
		"start_line_hash": "deadbeef0000", // nonexistent hash
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for nonexistent start_line_hash")
	}
	if !strings.Contains(err.Error(), "start_line_hash") {
		t.Errorf("error should mention 'start_line_hash', got: %v", err)
	}
}

func TestEditEndLineHashNotFound(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "alpha\nbeta\n"
	if err := os.WriteFile(filepath.Join(workspace, "e3.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":          "e3.txt",
		"old_text":      "alpha",
		"new_text":      "ALPHA",
		"end_line_hash": "deadbeef0000", // nonexistent hash
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error for nonexistent end_line_hash")
	}
	if !strings.Contains(err.Error(), "end_line_hash") {
		t.Errorf("error should mention 'end_line_hash', got: %v", err)
	}
}

func TestEditWithoutHashFieldsPreservesExistingBehavior(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "foo\nbar\nbaz\n"
	if err := os.WriteFile(filepath.Join(workspace, "legacy.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":     "legacy.txt",
		"old_text": "bar",
		"new_text": "BAR",
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("legacy edit failed: %v", err)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected error in legacy edit: %s", out)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "legacy.txt"))
	if !strings.Contains(string(got), "BAR") {
		t.Errorf("legacy replacement not applied; got: %q", got)
	}
}

func TestEditEndLineHashReplacesBlockCorrectly(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	// Multiline content where end_line_hash points to the end of old_text
	content := "header\nstart here\nmiddle content\nend here\nfooter\n"
	if err := os.WriteFile(filepath.Join(workspace, "block.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	endHash := lineHash("end here")

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":          "block.txt",
		"old_text":      "start here\nmiddle content\nend here",
		"new_text":      "REPLACED BLOCK",
		"end_line_hash": endHash,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit with end_line_hash: %v", err)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected error: %s", out)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "block.txt"))
	if !strings.Contains(string(got), "REPLACED BLOCK") {
		t.Errorf("block replacement not applied; got: %q", got)
	}
	if strings.Contains(string(got), "middle content") {
		t.Errorf("old content still present; got: %q", got)
	}
}

func TestEditHashVerifiesOldTextMatchesHashedLine(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(workspace, "verify.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Correct hash of "alpha" but old_text also contains "alpha" → should succeed
	alphaHash := lineHash("alpha")
	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "verify.txt",
		"old_text":        "alpha",
		"new_text":        "ALPHA",
		"start_line_hash": alphaHash,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit with correct hash: %v", err)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected error: %s", out)
	}
	got, _ := os.ReadFile(filepath.Join(workspace, "verify.txt"))
	if !strings.Contains(string(got), "ALPHA") {
		t.Errorf("replacement not applied; got: %q", got)
	}
}

func TestEditStartLineHashMismatchReturnsError(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(workspace, "mismatch.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Hash of "beta" but old_text is "alpha" — hash found but old_text doesn't start at that line
	betaHash := lineHash("beta")
	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "mismatch.txt",
		"old_text":        "alpha",
		"new_text":        "ALPHA",
		"start_line_hash": betaHash,
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error when start_line_hash points to wrong line for old_text")
	}
}

func TestEditEndLineHashMismatchWithLastLineOfOldText(t *testing.T) {
	t.Parallel()
	// Regression test: end_line_hash exists in the file but does NOT match the
	// last line of old_text. The validator must reject this — not silently allow it.
	workspace := t.TempDir()
	// File has three distinct lines. "gamma" is in the file.
	// old_text covers "alpha\nbeta" so its last line is "beta", not "gamma".
	// Providing end_line_hash = hash("gamma") should fail even though "gamma"
	// exists in the file.
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(workspace, "endmismatch.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	gammaHash := lineHash("gamma") // exists in file, but not the last line of old_text

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":          "endmismatch.txt",
		"old_text":      "alpha\nbeta", // last line is "beta", not "gamma"
		"new_text":      "REPLACED",
		"end_line_hash": gammaHash,
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error: end_line_hash points to 'gamma' but last line of old_text is 'beta'")
	}
	if !strings.Contains(err.Error(), "end_line_hash") {
		t.Errorf("error should mention 'end_line_hash', got: %v", err)
	}
	if !strings.Contains(err.Error(), "does not match last line of old_text") {
		t.Errorf("error should describe mismatch with last line of old_text, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Regression tests for position-aware replacement (issue #21 fix)
// ---------------------------------------------------------------------------

func TestEditStartLineHashTargetsSecondOccurrenceOfDuplicateLine(t *testing.T) {
	t.Parallel()
	// Regression: a file with duplicate lines (e.g. closing braces).
	// Without the fix, strings.Replace replaces the FIRST occurrence regardless
	// of which line the hash points to. With the fix, the replacement is anchored
	// to the byte offset of the matched line.
	workspace := t.TempDir()
	// Two identical lines "}" — hash of "}" appears twice. We target the second.
	content := "func foo() {\n}\nfunc bar() {\n}\n"
	if err := os.WriteFile(filepath.Join(workspace, "dup.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Build hash for "}" (both occurrences share the same hash).
	// The first match in the file is line index 1 (0-based), which is the closing
	// brace of foo(). We want to replace the SECOND "}" (line index 3, closing bar()).
	// To target the second occurrence we must use a unique anchor. Simulate this by
	// creating a file where the second occurrence has a unique neighbouring context
	// but the line itself is "}" — use old_text spanning two lines so it is unique,
	// anchored by start_line_hash on the first line.
	//
	// File layout:
	//   0: func foo() {
	//   1: }                ← first "}"
	//   2: func bar() {
	//   3: }                ← second "}" — we target this via start_line_hash on line 2
	//
	// old_text = "func bar() {\n}" (lines 2–3), start_line_hash = hash("func bar() {")
	startHash := lineHash("func bar() {")

	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "dup.go",
		"old_text":        "func bar() {\n}",
		"new_text":        "func bar() {\n\treturn\n}",
		"start_line_hash": startHash,
	})
	out, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit with start_line_hash on second block: %v", err)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("unexpected error in output: %s", out)
	}

	got, _ := os.ReadFile(filepath.Join(workspace, "dup.go"))
	gotStr := string(got)
	// foo() block must be untouched.
	if !strings.Contains(gotStr, "func foo() {\n}") {
		t.Errorf("foo() block was incorrectly modified; got:\n%s", gotStr)
	}
	// bar() block must have the inserted return.
	if !strings.Contains(gotStr, "func bar() {\n\treturn\n}") {
		t.Errorf("bar() block replacement not applied; got:\n%s", gotStr)
	}
}

func TestEditStartLineHashAnchorPositionMismatchReturnsError(t *testing.T) {
	t.Parallel()
	// Regression: start_line_hash points to a line that IS in the file, but
	// old_text does not begin at that byte offset. The position-aware check must
	// catch this and return a descriptive error rather than silently replacing a
	// wrong occurrence.
	workspace := t.TempDir()
	// File: "}\n}\n" — two identical closing braces.
	// start_line_hash = hash("}") → matches line 0 (anchorIdx=0, byteOffset=0).
	// old_text = "something_else" which is NOT present at byte offset 0.
	// Expected: error mentioning "anchor found at line 1 but old_text does not match".
	content := "}\n}\n"
	if err := os.WriteFile(filepath.Join(workspace, "anchor.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Hash of "}" is the same for both lines; anchorIdx will be 0.
	// We supply old_text whose first line matches the hash but the full text
	// does not sit at the anchor position.
	closingHash := lineHash("}")
	tool := buildEditTool(t, workspace)
	args, _ := json.Marshal(map[string]any{
		"path":            "anchor.go",
		"old_text":        "}\nNOT_IN_FILE",
		"new_text":        "REPLACED",
		"start_line_hash": closingHash,
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(args))
	if err == nil {
		t.Fatal("expected error: old_text does not match at anchor position")
	}
	if !strings.Contains(err.Error(), "anchor found at line") {
		t.Errorf("error should mention 'anchor found at line', got: %v", err)
	}
	if !strings.Contains(err.Error(), "old_text does not match at that position") {
		t.Errorf("error should mention 'old_text does not match at that position', got: %v", err)
	}
}
