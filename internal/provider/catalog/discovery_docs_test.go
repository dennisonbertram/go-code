package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveModelDiscoveryIsDocumentedForOperators(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		filepath.Join("..", "..", "..", "docs", "logs", "engineering-log.md"),
		filepath.Join("..", "..", "..", "docs", "logs", "system-log.md"),
		filepath.Join("..", "..", "..", "CLAUDE.md"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(strings.ToLower(string(contents)), "live model discovery") {
			t.Fatalf("%s does not document live model discovery", path)
		}
	}
}
