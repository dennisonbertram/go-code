package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

// driftFixture wires an auth-DISABLED server with a real rollout recorder, so a
// real recorded rollout can be produced and then replayed with drift detection.
// Auth-disabled keeps the focus on the drift pipeline (tenant gating has its own
// suite in http_replay_tenant_test.go).
type driftFixture struct {
	ts         *httptest.Server
	rolloutDir string
	runner     *harness.Runner
}

// newDriftFixture builds a server backed by a fakeprovider whose scripted turn
// makes the run complete in a single no-tool turn. A single-turn, no-tool run
// produces the simplest possible rollout, which the drift re-run (recorded
// provider) reproduces exactly.
func newDriftFixture(t *testing.T) *driftFixture {
	t.Helper()

	rolloutDir := t.TempDir()

	// A single no-tool turn that STREAMS its content and REPORTS usage, mirroring
	// what a real provider (and the recorded-response provider in the drift
	// re-run) produces. Matching streaming + usage_status keeps the re-run's
	// rollout structurally identical to the original.
	answer := "the one and only answer"
	prov := fakeprovider.New([]fakeprovider.Turn{
		{
			Content: answer,
			Deltas:  []harness.CompletionDelta{{Content: answer}},
			Usage: &harness.CompletionUsage{
				PromptTokens:     7,
				CompletionTokens: 5,
				TotalTokens:      12,
			},
			UsageStatus: harness.UsageStatusProviderReported,
		},
	})
	runner := harness.NewRunner(
		prov,
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:        "test-model",
			DefaultSystemPrompt: "test",
			MaxSteps:            4,
			RolloutDir:          rolloutDir,
		},
	)

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

	return &driftFixture{ts: ts, rolloutDir: rolloutDir, runner: runner}
}

// recordRollout drives a real run through the HTTP API, waits for completion,
// and returns the on-disk path of the recorded rollout file.
func (f *driftFixture) recordRollout(t *testing.T, prompt string) string {
	t.Helper()

	b, _ := json.Marshal(map[string]any{"prompt": prompt})
	resp, err := http.Post(f.ts.URL+"/v1/runs", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.RunID == "" {
		t.Fatalf("POST /v1/runs returned no run_id")
	}

	f.waitForDone(t, created.RunID)

	// Poll until the rollout file both exists AND contains a terminal event
	// (run.completed or run.failed). The recorder writes asynchronously, so
	// the HTTP status reaching "completed" does not guarantee the terminal
	// line has been flushed to disk yet. Reading the file before the terminal
	// line is written causes injectPhantomStep to see no run.completed, leave
	// terminalStep=0, and inject a phantom llm.turn.completed with step=0,
	// which the loader then rejects as invalid.
	var found string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_ = filepath.Walk(f.rolloutDir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if filepath.Base(p) == created.RunID+".jsonl" {
				found = p
			}
			return nil
		})
		if found != "" && rolloutFileHasTerminal(found) {
			break
		}
		found = "" // reset: file exists but terminal not yet written
		time.Sleep(10 * time.Millisecond)
	}
	if found == "" {
		t.Fatalf("recorded rollout file for run %s not found (or has no terminal event) under %s", created.RunID, f.rolloutDir)
	}
	return found
}

func (f *driftFixture) waitForDone(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		resp, err := http.Get(f.ts.URL + "/v1/runs/" + runID)
		if err != nil {
			t.Fatalf("GET /v1/runs/%s: %v", runID, err)
		}
		var state struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&state)
		_ = resp.Body.Close()
		if state.Status == "completed" || state.Status == "failed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run %s (last status %q)", runID, state.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rolloutFileHasTerminal reports whether the JSONL rollout file at path
// contains at least one run.completed or run.failed event. It is used by
// recordRollout to confirm the recorder has flushed the terminal line to disk
// before returning the path for subsequent manipulation by injectPhantomStep.
func rolloutFileHasTerminal(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			if ev.Type == "run.completed" || ev.Type == "run.failed" {
				return true
			}
		}
	}
	return false
}

func (f *driftFixture) simulate(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(f.ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/runs/replay: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// TestReplaySimulate_DetectDrift_StableFixtureMatches is T-F3a: POST simulate with
// detect_drift:true on a real recorded rollout returns a drift block, and an
// identical re-run (recorded provider + recorded tool outputs) reports
// matched=true with zero added/removed/changed steps.
func TestReplaySimulate_DetectDrift_StableFixtureMatches(t *testing.T) {
	t.Parallel()

	f := newDriftFixture(t)
	path := f.recordRollout(t, "what is the answer")

	code, out := f.simulate(t, map[string]any{
		"rollout_path": path,
		"mode":         "simulate",
		"detect_drift": true,
	})
	if code != http.StatusOK {
		t.Fatalf("detect_drift simulate: got %d, want 200; body %+v", code, out)
	}

	if out["mode"] != "simulate" {
		t.Errorf("mode: got %v, want simulate", out["mode"])
	}

	// Integrity block is still present (the existing offline replay result).
	integ, ok := out["integrity"].(map[string]any)
	if !ok {
		t.Fatalf("expected integrity block, got %+v", out["integrity"])
	}
	if integ["matched"] != true {
		t.Errorf("integrity.matched: got %v, want true", integ["matched"])
	}

	// Drift block must be present with the contract's fields.
	drift, ok := out["drift"].(map[string]any)
	if !ok {
		t.Fatalf("expected drift block, got %+v", out["drift"])
	}
	if drift["matched"] != true {
		t.Errorf("drift.matched: got %v, want true (identical re-run). full drift: %+v", drift["matched"], drift)
	}
	if drift["outcome_diff"] != "identical" {
		t.Errorf("drift.outcome_diff: got %v, want identical", drift["outcome_diff"])
	}
	for _, key := range []string{"added_steps", "removed_steps", "changed_steps"} {
		if v, present := drift[key]; present && v != nil {
			if arr, isArr := v.([]any); isArr && len(arr) != 0 {
				t.Errorf("drift.%s: expected empty, got %v", key, arr)
			}
		}
	}
	// cost_delta_usd and score must be present.
	if _, present := drift["cost_delta_usd"]; !present {
		t.Errorf("drift.cost_delta_usd missing")
	}
	if _, present := drift["score"]; !present {
		t.Errorf("drift.score missing")
	}

	// Top-level matched == integrity.matched AND drift.matched.
	if out["matched"] != true {
		t.Errorf("top-level matched: got %v, want true", out["matched"])
	}
}

// TestReplaySimulate_DefaultUnchanged is T-F3b: a default simulate request (no
// detect_drift) returns the SAME shape as before the drift feature: top-level
// integrity fields, NO drift block, NO integrity block.
func TestReplaySimulate_DefaultUnchanged(t *testing.T) {
	t.Parallel()

	f := newDriftFixture(t)
	path := f.recordRollout(t, "the default path")

	code, out := f.simulate(t, map[string]any{
		"rollout_path": path,
		"mode":         "simulate",
	})
	if code != http.StatusOK {
		t.Fatalf("default simulate: got %d, want 200; body %+v", code, out)
	}

	if out["mode"] != "simulate" {
		t.Errorf("mode: got %v, want simulate", out["mode"])
	}
	// The original integrity-only fields must remain at the top level.
	if out["matched"] != true {
		t.Errorf("matched: got %v, want true", out["matched"])
	}
	if _, present := out["events_replayed"]; !present {
		t.Errorf("events_replayed missing (default shape regressed)")
	}
	if _, present := out["step_count"]; !present {
		t.Errorf("step_count missing (default shape regressed)")
	}
	if _, present := out["mismatches"]; !present {
		t.Errorf("mismatches missing (default shape regressed)")
	}

	// No drift / integrity sub-block must be added when detect_drift is absent.
	if _, present := out["drift"]; present {
		t.Errorf("default request must NOT include a drift block, got %v", out["drift"])
	}
	if _, present := out["integrity"]; present {
		t.Errorf("default request must NOT include an integrity block, got %v", out["integrity"])
	}
}

// injectPhantomStep reads the JSONL rollout file at path, inserts a phantom
// "llm.turn.completed" event at an extra step number just before the terminal
// run.completed/run.failed line, and writes the modified file back to disk.
// This makes the "original" appear to have an extra LLM turn that the drift
// re-run (which replays only the real scripted turns) will NOT produce, so
// drift detection must report matched=false with non-empty removed_steps.
//
// The on-disk JSONL format is {"ts":"...","seq":N,"type":"...","data":{...}}.
func injectPhantomStep(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("injectPhantomStep: read %s: %v", path, err)
	}

	// Parse lines into raw JSON objects to find the terminal event and the
	// highest step number seen so far.
	type rawLine struct {
		Ts   string         `json:"ts"`
		Seq  uint64         `json:"seq"`
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}

	var lines []rawLine
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var rl rawLine
		if err := json.Unmarshal([]byte(text), &rl); err != nil {
			t.Fatalf("injectPhantomStep: parse line %q: %v", text, err)
		}
		lines = append(lines, rl)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("injectPhantomStep: scan: %v", err)
	}

	// Find the terminal event (run.completed or run.failed) and its step number.
	insertIdx := len(lines) // default: append (shouldn't happen on valid rollout)
	terminalStep := 0
	for i, l := range lines {
		if l.Type == "run.completed" || l.Type == "run.failed" {
			insertIdx = i
			if s, ok := l.Data["step"].(float64); ok {
				terminalStep = int(s)
			}
			break
		}
	}
	// Use the terminal step so the injected line is non-decreasing with
	// the lines before it (the loader rejects decreasing step numbers).
	phantomStep := terminalStep

	// Build the phantom line: an llm.turn.completed event at phantomStep.
	// Inserting it before run.completed at the same step is valid (non-decreasing)
	// and adds an LLM turn the re-run will not produce, causing drift.
	phantom := rawLine{
		Ts:   time.Now().UTC().Format(time.RFC3339Nano),
		Seq:  lines[len(lines)-1].Seq + 1,
		Type: "llm.turn.completed",
		Data: map[string]any{
			"step":       float64(phantomStep),
			"tool_calls": float64(0),
			"content":    "phantom turn injected for drift test",
		},
	}

	// Insert the phantom line at insertIdx.
	lines = append(lines[:insertIdx], append([]rawLine{phantom}, lines[insertIdx:]...)...)

	// Marshal back to JSONL.
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("injectPhantomStep: marshal line: %v", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("injectPhantomStep: write %s: %v", path, err)
	}
}

// TestReplaySimulate_DetectDrift_DivergentFixtureReportsMatchedFalse is T-F3c:
// a NEGATIVE end-to-end test for the drift layer. A real rollout is recorded,
// then a phantom extra step is injected into the on-disk file so the "original"
// has an LLM turn that the re-run (which follows only the real scripted provider
// turns) will NOT produce. Drift detection must therefore report matched=false
// with non-empty divergence (removed_steps or changed_steps).
func TestReplaySimulate_DetectDrift_DivergentFixtureReportsMatchedFalse(t *testing.T) {
	t.Parallel()

	f := newDriftFixture(t)
	path := f.recordRollout(t, "diverge me")

	// Mutate the rollout to introduce a phantom step.
	injectPhantomStep(t, path)

	code, out := f.simulate(t, map[string]any{
		"rollout_path": path,
		"mode":         "simulate",
		"detect_drift": true,
	})
	if code != http.StatusOK {
		t.Fatalf("detect_drift diverge: got %d, want 200; body %+v", code, out)
	}

	drift, ok := out["drift"].(map[string]any)
	if !ok {
		t.Fatalf("expected drift block in response, got %+v", out["drift"])
	}

	// The drift block must report matched=false.
	if drift["matched"] != false {
		t.Errorf("drift.matched: got %v, want false (divergent fixture must not match)", drift["matched"])
	}

	// At least one of removed_steps or changed_steps must be non-empty,
	// reflecting the phantom step the re-run did not produce.
	removed, _ := drift["removed_steps"].([]any)
	changed, _ := drift["changed_steps"].([]any)
	if len(removed) == 0 && len(changed) == 0 {
		t.Errorf("expected non-empty removed_steps or changed_steps; got removed=%v changed=%v (full drift: %+v)",
			removed, changed, drift)
	}

	// Top-level matched must also be false.
	if out["matched"] != false {
		t.Errorf("top-level matched: got %v, want false", out["matched"])
	}
}
