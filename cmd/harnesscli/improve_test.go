package main

import (
	"os"
	"strings"
	"testing"
)

func TestRunImproveDryRunPrintsSelfImprovementPlan(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := runImprove([]string{"--dry-run", "--target", "internal/harness.Runner", "--max-steps", "7"})
	if code != 0 {
		t.Fatalf("runImprove returned %d, stderr=%s", code, errBuf.String())
	}
	out := outBuf.String()
	for _, want := range []string{
		"Self-improvement plan",
		"internal/harness.Runner",
		"scripts/autoresearch-loop.sh",
		"go test ./...",
		"go test ./... -race",
		"./scripts/test-regression.sh",
		"--max-steps 7",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestDispatchRoutesImprove(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := dispatch([]string{"improve", "--dry-run", "--target", "internal/server"})
	if code != 0 {
		t.Fatalf("dispatch improve returned %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "internal/server") {
		t.Fatalf("dispatch improve output missing target:\n%s", outBuf.String())
	}
}

func TestRunScoreCommandsReportsSuccessAndFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		outBuf, errBuf, restore := captureOutput(t)
		defer restore()

		code := runScoreCommands([]string{"printf score-ok"})
		if code != 0 {
			t.Fatalf("runScoreCommands returned %d, stderr=%s", code, errBuf.String())
		}
		output := outBuf.String()
		if !strings.Contains(output, "$ printf score-ok") {
			t.Fatalf("output missing command echo:\n%s", output)
		}
		if !strings.Contains(output, "score-ok") {
			t.Fatalf("output missing command stdout:\n%s", output)
		}
	})

	t.Run("failure", func(t *testing.T) {
		outBuf, errBuf, restore := captureOutput(t)
		defer restore()

		code := runScoreCommands([]string{"printf before-fail", "exit 7", "printf never"})
		if code != 1 {
			t.Fatalf("runScoreCommands returned %d, stderr=%s", code, errBuf.String())
		}
		if !strings.Contains(outBuf.String(), "before-fail") {
			t.Fatalf("output missing successful command stdout:\n%s", outBuf.String())
		}
		if strings.Contains(outBuf.String(), "never") {
			t.Fatalf("runScoreCommands continued after failing command:\n%s", outBuf.String())
		}
		if !strings.Contains(errBuf.String(), "score command failed: exit 7") {
			t.Fatalf("stderr missing failure context:\n%s", errBuf.String())
		}
	})
}

func TestResolveAutoresearchLoopScript(t *testing.T) {
	t.Run("finds repo script", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(tmp+"/scripts", 0o755); err != nil {
			t.Fatalf("mkdir scripts: %v", err)
		}
		if err := os.WriteFile(tmp+"/scripts/autoresearch-loop.sh", []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write script: %v", err)
		}
		t.Chdir(tmp)

		path, err := resolveAutoresearchLoopScript()
		if err != nil {
			t.Fatalf("resolveAutoresearchLoopScript: %v", err)
		}
		if path != "scripts/autoresearch-loop.sh" {
			t.Fatalf("script path = %q, want scripts/autoresearch-loop.sh", path)
		}
	})

	t.Run("errors outside repo", func(t *testing.T) {
		t.Chdir(t.TempDir())

		path, err := resolveAutoresearchLoopScript()
		if err == nil {
			t.Fatalf("expected missing script error, got path %q", path)
		}
		if !strings.Contains(err.Error(), "scripts/autoresearch-loop.sh not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
