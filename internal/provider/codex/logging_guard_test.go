package codex

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCredentialPathsHaveNoTokenLogging is a grep-based guard against adding
// credential-bearing log/print calls to this provider's auth paths.
func TestCredentialPathsHaveNoTokenLogging(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)(?:log\.|fmt\.(?:print|printf|sprint)).{0,120}(?:access[_ -]?token|refresh[_ -]?token|id[_ -]?token)`)
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if match := pattern.Find(contents); match != nil {
			t.Fatalf("credential-related logging call found in %s: %s", path, match)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan credential auth paths: %v", err)
	}
}
