package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression test for issue #789: an option-like target must be rejected
// before git runs. git parses e.g. --output=/abs/path as an option even in a
// non-repository directory, giving an arbitrary file write from a
// read-classified tool. No repo is needed here: validation precedes exec.
func TestGitDiffTool_RejectsOptionLikeTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pwn := filepath.Join(dir, "pwn")
	tool := gitDiffTool(dir, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"target":"--output=`+pwn+`"}`))
	if err == nil {
		t.Fatal("expected error for option-like target, got nil")
	}
	if !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected error to contain %q, got %q", "must not begin with '-'", err.Error())
	}
	if _, statErr := os.Stat(pwn); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (git must not run with an injected option)", pwn)
	}
}
