package repostructure

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the repository root directory derived from this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

// TestRegressionScriptDefaultsToFullSuite verifies that the regression script
// defaults its test package scope to `./...` (full module), matching the
// documented full-suite policy in docs/runbooks/testing.md.
func TestRegressionScriptDefaultsToFullSuite(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "test-regression.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("cannot read regression script: %v", err)
	}
	content := string(data)

	// The default PKG_PATTERNS must be ./... to match the documented full-suite policy.
	if !strings.Contains(content, `PKG_PATTERNS="${PKG_PATTERNS:-./...}"`) {
		t.Errorf("regression script PKG_PATTERNS default is not ./...: script may have drifted from documented full-suite policy")
	}

	// PKGS must be derived from PKG_PATTERNS, not hardcoded to a narrower scope.
	if strings.Contains(content, `go list ./internal/... ./cmd/...`) {
		t.Errorf("regression script has hardcoded narrow scope instead of using PKG_PATTERNS variable")
	}
}

// TestRegressionScriptCoverageScope verifies that the coverage scope
// (COVER_PKG_PATTERNS) defaults to the core packages, keeping the
// coverage gate practical while the test scope covers the full suite.
func TestRegressionScriptCoverageScope(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "test-regression.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("cannot read regression script: %v", err)
	}
	content := string(data)

	// COVER_PKG_PATTERNS must default to the core package set.
	if !strings.Contains(content, `COVER_PKG_PATTERNS="${COVER_PKG_PATTERNS:-./internal/... ./cmd/...}"`) {
		t.Errorf("regression script COVER_PKG_PATTERNS default is not ./internal/... ./cmd/...")
	}

	// COVERPKGS must be derived from COVER_PKG_PATTERNS.
	if !strings.Contains(content, `COVERPKGS="$(go list ${COVER_PKG_PATTERNS})"`) {
		t.Errorf("regression script COVERPKGS is not derived from COVER_PKG_PATTERNS")
	}
}

// TestDocsClaimFullSuiteCoverage verifies that the testing runbook documents the
// regression gate as enforcing `go test ./...` (the full suite), consistent with
// the regression script's default test scope.
func TestDocsClaimFullSuiteCoverage(t *testing.T) {
	root := repoRoot(t)
	docsPath := filepath.Join(root, "docs", "runbooks", "testing.md")
	data, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("cannot read testing runbook: %v", err)
	}
	content := string(data)

	// The Regression Gate section must reference `go test ./...` as the enforced command.
	if !strings.Contains(content, "go test ./...") {
		t.Errorf("testing runbook does not document `go test ./...` as the regression gate command")
	}
}

// TestRegressionScriptDocsScopeMatch verifies that the regression script's
// default test scope and the documented policy are consistent. This is the
// drift check: if either side changes independently, this test fails.
func TestRegressionScriptDocsScopeMatch(t *testing.T) {
	root := repoRoot(t)

	scriptPath := filepath.Join(root, "scripts", "test-regression.sh")
	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("cannot read regression script: %v", err)
	}
	scriptContent := string(scriptData)

	docsPath := filepath.Join(root, "docs", "runbooks", "testing.md")
	docsData, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("cannot read testing runbook: %v", err)
	}
	docsContent := string(docsData)

	scriptHasFullSuite := strings.Contains(scriptContent, `PKG_PATTERNS="${PKG_PATTERNS:-./...}"`)
	docsHasFullSuite := strings.Contains(docsContent, "go test ./...")

	if scriptHasFullSuite != docsHasFullSuite {
		t.Errorf("scope mismatch: script defaults to full suite=%v, docs claim full suite=%v",
			scriptHasFullSuite, docsHasFullSuite)
	}
}
