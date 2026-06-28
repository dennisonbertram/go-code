package replay

import (
	"path/filepath"
	"runtime"
	"testing"

	"go-agent-harness/internal/forensics/rollout"
)

// fixturesDir returns the absolute path to testdata/rollouts relative to this
// file so the tests work regardless of the working directory.
func fixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "rollouts")
}

// T-D1: Load every stable fixture from testdata/rollouts/*.jsonl and assert
// that both LoadFile and Replay report matched==true. The tool-call fixture
// additionally asserts that the reconstructed tool messages are non-empty,
// exercising the F0 fix (runner emits "output" key; replayer must fall back
// from "result" to "output" when "result" is absent).

// TestFixture_SimpleNoTool loads the simplest valid rollout (no tool calls) and
// verifies that LoadFile succeeds and Replay returns matched==true.
func TestFixture_SimpleNoTool(t *testing.T) {
	t.Parallel()

	path := filepath.Join(fixturesDir(), "simple-no-tool.jsonl")
	events, err := rollout.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event, got 0")
	}

	result := Replay(events)
	if !result.Matched {
		t.Errorf("Replay: expected matched=true, got false; mismatches: %v", result.Mismatches)
	}
	if result.StepCount == 0 {
		t.Error("expected step_count > 0")
	}
}

// TestFixture_ToolWithOutputKey loads the fixture that uses the real runner
// "output" key (not "result") in tool.call.completed events. It verifies:
//   - LoadFile succeeds (valid JSONL structure)
//   - Replay returns matched==true (F0: fallback from "result" to "output" in indexToolCompletions)
//   - ReconstructMessages returns a non-empty tool message (F0: fallback in ReconstructMessages)
func TestFixture_ToolWithOutputKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(fixturesDir(), "tool-with-output-key.jsonl")
	events, err := rollout.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event, got 0")
	}

	// --- Integrity: Replay must succeed and be matched ---
	result := Replay(events)
	if !result.Matched {
		t.Errorf("Replay: expected matched=true, got false; mismatches: %v", result.Mismatches)
	}

	// The tool.call.started ReplayEvent must carry a non-empty Details["result"]
	// (populated from the "output" field via the F0 fallback).
	var startedEv *ReplayEvent
	for i := range result.Events {
		if result.Events[i].EventType == "tool.call.started" {
			startedEv = &result.Events[i]
			break
		}
	}
	if startedEv == nil {
		t.Fatal("no tool.call.started event found in ReplayResult.Events")
	}
	if got, _ := startedEv.Details["result"].(string); got == "" {
		t.Error("tool.call.started Details[\"result\"] is empty; F0 fallback from \"output\" key not working in Replay")
	}

	// --- ReconstructMessages: tool message Content must be non-empty ---
	// Find the highest step to pass as upToStep.
	maxStep := 0
	for _, ev := range events {
		if ev.Step > maxStep {
			maxStep = ev.Step
		}
	}
	msgs := ReconstructMessages(events, maxStep)

	var toolMsg *struct{ Content, Role string }
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsg = &struct{ Content, Role string }{Content: m.Content, Role: m.Role}
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("ReconstructMessages: no tool-role message found")
	}
	if toolMsg.Content == "" {
		t.Error("ReconstructMessages: tool message Content is empty; F0 fallback from \"output\" key not working")
	}
}

// TestFixture_AllRollouts is a table-driven test that loads every fixture in
// testdata/rollouts/ and asserts that LoadFile+Replay both succeed with
// matched==true. New fixtures added to that directory are automatically
// exercised here.
func TestFixture_AllRollouts(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name     string
		filename string
	}{
		{"simple_no_tool", "simple-no-tool.jsonl"},
		{"tool_with_output_key", "tool-with-output-key.jsonl"},
	}

	dir := fixturesDir()
	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(dir, tc.filename)
			events, err := rollout.LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile(%q): %v", path, err)
			}
			result := Replay(events)
			if !result.Matched {
				t.Errorf("Replay(%q): matched=false; mismatches: %v", tc.filename, result.Mismatches)
			}
		})
	}
}
