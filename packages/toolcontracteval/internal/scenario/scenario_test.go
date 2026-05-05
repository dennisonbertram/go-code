package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSuiteValidatesShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(`{"id":"x","scenarios":[{"id":"s","prompt":"do it"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if suite.MaxTurns != 4 {
		t.Fatalf("max turns = %d, want default 4", suite.MaxTurns)
	}
}
