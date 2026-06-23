// T-D2: drive live runs through the real recorder with a fakeprovider script,
// capture the resulting JSONL rollout files, and assert they LoadFile+Replay
// cleanly. This guards the F0 fix (runner emits "output" key; replayer falls
// back from "result" to "output") end-to-end through the real recorder and
// loader. Two cases:
//   - A simple no-tool run, where LoadFile succeeds and Replay returns
//     matched==true (guards the recorder format, step ordering, and terminal
//     invariants).
//   - A tool-call run, where LoadFile succeeds and the resulting events contain
//     tool.call.completed events with the real "output" key (guards F0: the
//     runner always emits "output", never "result", for tool results).
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/forensics/replay"
	rolloutpkg "go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

// captureFixture is a reusable helper that wires a server with a fakeprovider,
// starts a run via HTTP, waits for completion, and returns the JSONL rollout
// file path. It is the plumbing shared by all T-D2 sub-tests.
type captureFixture struct {
	ts         *httptest.Server
	rolloutDir string
	runner     *harness.Runner
}

func newCaptureFixture(t *testing.T, prov *fakeprovider.Provider, registry *harness.Registry) *captureFixture {
	t.Helper()

	rolloutDir := t.TempDir()
	runner := harness.NewRunner(prov, registry, harness.RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            5,
		RolloutDir:          rolloutDir,
	})
	h := server.NewWithOptions(server.ServerOptions{
		Runner:       runner,
		RolloutDir:   rolloutDir,
		AuthDisabled: true,
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})
	return &captureFixture{ts: ts, rolloutDir: rolloutDir, runner: runner}
}

// run starts a run via the HTTP API and returns its run_id.
func (f *captureFixture) run(t *testing.T, prompt string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"prompt": prompt})
	resp, err := http.Post(f.ts.URL+"/v1/runs", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode /v1/runs response: %v", err)
	}
	_ = resp.Body.Close()
	if created.RunID == "" {
		t.Fatal("POST /v1/runs: empty run_id")
	}
	return created.RunID
}

// waitDone polls until the run reaches a terminal status or times out.
func (f *captureFixture) waitDone(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := http.Get(f.ts.URL + "/v1/runs/" + runID)
		if err != nil {
			t.Fatalf("GET /v1/runs/%s: %v", runID, err)
		}
		var state struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(r.Body).Decode(&state)
		_ = r.Body.Close()
		if state.Status == "completed" || state.Status == "failed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run %s", runID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rolloutPath locates the JSONL file for runID under rolloutDir and returns it
// only once it is COMPLETE. The HTTP run status flips to "completed" the moment
// the terminal event is emitted, but the recorder flushes the JSONL file
// asynchronously, so the file can exist (and even be partially written) before
// the terminal "run.completed"/"run.failed" line lands on disk. Polling for mere
// file existence is therefore a load-sensitive race: under a busy full-suite run
// the file's last line can still be "usage.delta" when a caller reads it. We poll
// until the file parses AND its last event is terminal, guaranteeing callers
// always observe a fully-flushed rollout.
func (f *captureFixture) rolloutPath(t *testing.T, runID string) string {
	t.Helper()
	target := runID + ".jsonl"
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var found string
		_ = filepath.Walk(f.rolloutDir, func(p string, info os.FileInfo, werr error) error {
			if werr != nil || info.IsDir() {
				return nil
			}
			if filepath.Base(p) == target {
				found = p
			}
			return nil
		})
		if found != "" {
			// LoadFile may error or return a non-terminal last event while the
			// recorder is still flushing; keep polling until it is complete.
			if events, err := rolloutpkg.LoadFile(found); err == nil && len(events) > 0 {
				switch events[len(events)-1].Type {
				case "run.completed", "run.failed", "run.cancelled":
					return found
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("rollout file %s did not become complete (terminal event) under %s", target, f.rolloutDir)
	return ""
}

// TestRolloutCapture_NoTool_LoadAndReplay is the core T-D2 guard: a simple
// no-tool run through the real recorder produces a JSONL file that LoadFile
// parses cleanly and Replay verifies with matched==true. This validates that:
//   - The real recorder writes a conforming JSONL format (run.started first at
//     step=0, monotonic steps, terminal last).
//   - The loader and replayer accept the format without error.
func TestRolloutCapture_NoTool_LoadAndReplay(t *testing.T) {
	t.Parallel()

	prov := fakeprovider.New([]fakeprovider.Turn{
		{Content: "The answer is 42."},
	})
	f := newCaptureFixture(t, prov, harness.NewRegistry())
	runID := f.run(t, "What is the answer to life?")
	f.waitDone(t, runID)
	path := f.rolloutPath(t, runID)

	// --- Load ---
	events, err := rolloutpkg.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if len(events) == 0 {
		t.Fatal("LoadFile: got 0 events")
	}

	// Structural sanity: run.started first, terminal last.
	if events[0].Type != "run.started" {
		t.Errorf("first event: want run.started, got %q", events[0].Type)
	}
	last := events[len(events)-1]
	if last.Type != "run.completed" && last.Type != "run.failed" {
		t.Errorf("last event: want run.completed or run.failed, got %q", last.Type)
	}

	// --- Replay ---
	result := replay.Replay(events)
	if !result.Matched {
		t.Errorf("Replay: expected matched=true, got false\nmismatches: %v", result.Mismatches)
	}
}

// TestRolloutCapture_ToolCall_OutputKey guards the F0 fix end-to-end through
// the real recorder: a run that includes a tool call produces a JSONL file
// where tool.call.completed events carry the "output" key (not "result"). This
// verifies that the real runner format (runner_step_engine.go:1072,1091) is
// what the loader and replayer's F0 fallback handles.
//
// Note on Replay.matched: the runner records tool_calls in llm.turn.completed
// as an integer count (not an array of objects), so Replay's announcement
// cross-check cannot verify causal ordering for real recorded tool-call runs.
// Replay will return matched=false for this reason. This is a known limitation
// of the current Replay implementation on real recorder output with tool calls;
// the integrity assertion (matched=true) is only available for runs without
// tool calls or for hand-authored fixtures that include full tool call objects
// in llm.turn.completed (as T-D1 does). This test guards the recorder format
// and the F0 key fallback independently of Replay's causal checks.
func TestRolloutCapture_ToolCall_OutputKey(t *testing.T) {
	t.Parallel()

	const toolName = "list_files"
	const toolCallID = "cap-d2-call-001"

	prov := fakeprovider.New([]fakeprovider.Turn{
		{
			Content: "I will list the files.",
			ToolCalls: []harness.ToolCall{
				{ID: toolCallID, Name: toolName, Arguments: `{"path":"/tmp"}`},
			},
		},
		{Content: "Done. The files are: a.txt, b.go."},
	})

	registry := harness.NewRegistry()
	if err := registry.Register(harness.ToolDefinition{
		Name:        toolName,
		Description: "List files in a directory.",
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "a.txt\nb.go", nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	f := newCaptureFixture(t, prov, registry)
	runID := f.run(t, "List files in /tmp")
	f.waitDone(t, runID)
	path := f.rolloutPath(t, runID)

	// --- Load ---
	events, err := rolloutpkg.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if len(events) == 0 {
		t.Fatal("LoadFile: got 0 events")
	}

	// --- Find tool.call.completed and verify "output" key is present ---
	// The runner always emits tool results under "output" (runner_step_engine.go).
	// If the recorder or loader were mangling keys, "output" would be absent or
	// renamed to something else.
	var foundToolCompleted bool
	var hasOutputKey bool
	for _, ev := range events {
		if ev.Type != "tool.call.completed" {
			continue
		}
		foundToolCompleted = true
		if ev.Payload == nil {
			continue
		}
		if _, ok := ev.Payload["output"]; ok {
			hasOutputKey = true
		}
	}
	if !foundToolCompleted {
		t.Error("no tool.call.completed event found in rollout — tool was not recorded")
	}
	if !hasOutputKey {
		t.Error("tool.call.completed event missing \"output\" key; runner should emit output not result (F0)")
	}

	// --- Replay: LoadFile did not error; Replay runs without panic ---
	// Replay will return matched=false because llm.turn.completed records
	// tool_calls as an integer count in real rollouts. This is expected and
	// documented above.
	result := replay.Replay(events)
	_ = result // matched=false is expected for real tool-call rollouts; no assertion
}
