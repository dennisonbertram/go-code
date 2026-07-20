package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/training"
)

// writeTestJSONL writes a minimal rollout JSONL file for testing.
func writeTestJSONL(t *testing.T, dir, runID string) string {
	t.Helper()

	// Create date subdirectory matching rollout convention.
	dateDir := filepath.Join(dir, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dateDir, runID+".jsonl")

	type entry struct {
		Ts   string         `json:"ts"`
		Seq  int            `json:"seq"`
		Type string         `json:"type"`
		Data map[string]any `json:"data,omitempty"`
	}

	events := []entry{
		{Ts: "2026-03-14T12:00:00Z", Seq: 1, Type: "run.started", Data: map[string]any{
			"run_id": runID,
			"prompt": "fix the bug",
		}},
		{Ts: "2026-03-14T12:00:01Z", Seq: 2, Type: "tool.call", Data: map[string]any{
			"name":    "read_file",
			"call_id": "call_1",
			"step":    1,
			"args":    map[string]any{"path": "/app/main.go"},
		}},
		{Ts: "2026-03-14T12:00:02Z", Seq: 3, Type: "tool.result", Data: map[string]any{
			"name":    "read_file",
			"call_id": "call_1",
			"step":    1,
			"output":  "package main\n",
			"success": true,
		}},
		{Ts: "2026-03-14T12:00:03Z", Seq: 4, Type: "llm.completion.finished", Data: map[string]any{
			"content":  "I found the issue.",
			"usage":    map[string]any{"total_tokens": 500.0},
			"cost_usd": 0.01,
		}},
		{Ts: "2026-03-14T12:00:04Z", Seq: 5, Type: "run.completed", Data: map[string]any{
			"steps": 2,
		}},
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}

	return path
}

func TestScoreCommand(t *testing.T) {
	dir := t.TempDir()
	runID := "test_run_1"
	writeTestJSONL(t, dir, runID)

	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runScore(runID, dir, dbPath)
	if err != nil {
		t.Fatalf("runScore failed: %v", err)
	}
}

func TestScoreCommand_MissingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runScore("nonexistent_run", dir, dbPath)
	if err == nil {
		t.Fatal("expected error for missing rollout file")
	}
}

func TestStatusCommand(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Create a store and add some test data.
	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	bundle := training.TraceBundle{RunID: "r1", TaskID: "t1", Outcome: "pass", Steps: 3}
	score := training.ScoreResult{RunID: "r1", ToolQuality: 0.9, Efficiency: 0.8}
	if err := store.SaveTrace(bundle, score); err != nil {
		t.Fatal(err)
	}
	store.Close()

	err = runStatus(dbPath)
	if err != nil {
		t.Fatalf("runStatus failed: %v", err)
	}
}

func TestStatusCommand_EmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Ensure the database directory exists.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}

	err := runStatus(dbPath)
	if err != nil {
		t.Fatalf("runStatus on empty db failed: %v", err)
	}
}

func TestHistoryCommand_NoChanges(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Create empty store.
	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	err = runHistory("2026-01-01", dbPath)
	if err != nil {
		t.Fatalf("runHistory failed: %v", err)
	}
}

func TestHistoryCommand_WithChanges(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAppliedChange("abc123def", 1, "Fixed retry loop in bash tool"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	err = runHistory("2026-01-01", dbPath)
	if err != nil {
		t.Fatalf("runHistory failed: %v", err)
	}
}

func TestHistoryCommand_InvalidDate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runHistory("not-a-date", dbPath)
	if err == nil {
		t.Fatal("expected error for invalid date format")
	}
}

func TestFindRolloutFile(t *testing.T) {
	dir := t.TempDir()
	runID := "test_find"
	writeTestJSONL(t, dir, runID)

	path, err := findRolloutFile(dir, runID)
	if err != nil {
		t.Fatalf("findRolloutFile failed: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got: %s", path)
	}
}

func TestFindRolloutFile_FlatDir(t *testing.T) {
	dir := t.TempDir()
	runID := "flat_run"
	path := filepath.Join(dir, runID+".jsonl")
	if err := os.WriteFile(path, []byte(`{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"run_id":"flat_run"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := findRolloutFile(dir, runID)
	if err != nil {
		t.Fatalf("findRolloutFile failed: %v", err)
	}
	if found != path {
		t.Errorf("expected %s, got %s", path, found)
	}
}

func TestFindRolloutFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := findRolloutFile(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFindAllRolloutFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestJSONL(t, dir, "run_a")
	writeTestJSONL(t, dir, "run_b")

	files, err := findAllRolloutFiles(dir)
	if err != nil {
		t.Fatalf("findAllRolloutFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestFindAllRolloutFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	files, err := findAllRolloutFiles(dir)
	if err != nil {
		t.Fatalf("findAllRolloutFiles failed: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestLoopCommand_DryRun(t *testing.T) {
	dir := t.TempDir()
	writeTestJSONL(t, dir, "loop_run_1")

	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runLoop("all", "claude-opus", true, dir, dbPath)
	if err != nil {
		t.Fatalf("runLoop (dry-run) failed: %v", err)
	}
}

func TestLoopCommand_NoFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runLoop("all", "claude-opus", false, dir, dbPath)
	if err != nil {
		t.Fatalf("runLoop with no files failed: %v", err)
	}
}

func TestLoopCommand_SaveToStore(t *testing.T) {
	dir := t.TempDir()
	writeTestJSONL(t, dir, "loop_save_1")

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")

	// Unset ANTHROPIC_API_KEY so loop only scores + saves without Claude analysis.
	origKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", origKey)
		}
	}()

	err := runLoop("all", "claude-opus", false, dir, dbPath)
	if err != nil {
		t.Fatalf("runLoop failed: %v", err)
	}

	// Verify trace was saved.
	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	count, err := store.CountTraces()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 trace, got %d", count)
	}
}

func TestPrintFindings_Text(t *testing.T) {
	findings := []training.Finding{
		{Type: "behavior", Priority: "high", Target: "bash tool", Issue: "retry loop detected"},
	}
	err := printFindings(findings, "text")
	if err != nil {
		t.Fatalf("printFindings text failed: %v", err)
	}
}

func TestPrintFindings_JSON(t *testing.T) {
	findings := []training.Finding{
		{Type: "behavior", Priority: "high", Target: "bash tool", Issue: "retry loop detected"},
	}
	err := printFindings(findings, "json")
	if err != nil {
		t.Fatalf("printFindings json failed: %v", err)
	}
}

func TestPrintFindings_Empty(t *testing.T) {
	err := printFindings(nil, "text")
	if err != nil {
		t.Fatalf("printFindings empty failed: %v", err)
	}
}

func TestRootCmd_Help(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}
}

func TestRootCmd_ScoreHelp(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"score", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("score help failed: %v", err)
	}
}

func TestStoreCounts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Verify empty counts.
	tc, err := store.CountTraces()
	if err != nil {
		t.Fatal(err)
	}
	if tc != 0 {
		t.Errorf("expected 0 traces, got %d", tc)
	}

	fc, err := store.CountFindings()
	if err != nil {
		t.Fatal(err)
	}
	if fc != 0 {
		t.Errorf("expected 0 findings, got %d", fc)
	}

	ac, err := store.CountAppliedChanges()
	if err != nil {
		t.Fatal(err)
	}
	if ac != 0 {
		t.Errorf("expected 0 applied changes, got %d", ac)
	}

	// Add data and re-check.
	bundle := training.TraceBundle{RunID: "c1", Steps: 2}
	score := training.ScoreResult{RunID: "c1"}
	if err := store.SaveTrace(bundle, score); err != nil {
		t.Fatal(err)
	}

	tc, _ = store.CountTraces()
	if tc != 1 {
		t.Errorf("expected 1 trace, got %d", tc)
	}

	if err := store.SaveFindings("c1", []training.Finding{{Type: "behavior"}}); err != nil {
		t.Fatal(err)
	}
	fc, _ = store.CountFindings()
	if fc != 1 {
		t.Errorf("expected 1 finding, got %d", fc)
	}

	if err := store.SaveAppliedChange("abc", 1, "test change"); err != nil {
		t.Fatal(err)
	}
	ac, _ = store.CountAppliedChanges()
	if ac != 1 {
		t.Errorf("expected 1 applied change, got %d", ac)
	}
}

func TestStoreQueryHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := training.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Empty history.
	changes, err := store.QueryHistory(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}

	// Add a change.
	if err := store.SaveAppliedChange("def456", 1, "updated prompt"); err != nil {
		t.Fatal(err)
	}

	// Query should find it.
	changes, err = store.QueryHistory(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].GitCommit != "def456" {
		t.Errorf("expected commit def456, got %s", changes[0].GitCommit)
	}
	if changes[0].Description != "updated prompt" {
		t.Errorf("expected description 'updated prompt', got %s", changes[0].Description)
	}

	// Query with future date should find nothing.
	changes, err = store.QueryHistory(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for future date, got %d", len(changes))
	}
}

func TestRunAnalyze_MissingAPIKey(t *testing.T) {
	origKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", origKey)
		}
	}()

	err := runAnalyze("run_1", t.TempDir(), "text", filepath.Join(t.TempDir(), "test.db"))
	if err == nil {
		t.Fatal("expected error for missing ANTHROPIC_API_KEY")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error should mention ANTHROPIC_API_KEY, got: %v", err)
	}
}

func TestLoadBundles(t *testing.T) {
	dir := t.TempDir()
	writeTestJSONL(t, dir, "lb_run_1")
	writeTestJSONL(t, dir, "lb_run_2")

	bundles, err := loadBundles(dir, []string{"lb_run_1", "lb_run_2"})
	if err != nil {
		t.Fatalf("loadBundles: %v", err)
	}
	if len(bundles) != 2 {
		t.Errorf("expected 2 bundles, got %d", len(bundles))
	}
}

func TestLoadBundles_Missing(t *testing.T) {
	dir := t.TempDir()

	_, err := loadBundles(dir, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing rollout")
	}
}

func TestLoopCommand_TaskSetFilter(t *testing.T) {
	dir := t.TempDir()
	// The JSONL doesn't set task_id, so filtering by a specific task set should skip it.
	writeTestJSONL(t, dir, "filter_run")

	dbPath := filepath.Join(t.TempDir(), "test.db")

	err := runLoop("python", "claude-opus", true, dir, dbPath)
	if err != nil {
		t.Fatalf("runLoop with filter failed: %v", err)
	}
}
