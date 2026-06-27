package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go-agent-harness/internal/forensics/audittrail"
)

var errAuditTest = errors.New("audit test provider error")

type blockingAuditProvider struct {
	release <-chan struct{}
}

func (p *blockingAuditProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	<-p.release
	return CompletionResult{Content: "done"}, nil
}

// readAuditEntries reads all entries from an audit.jsonl file.
func readAuditEntries(t *testing.T, path string) []audittrail.AuditEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log %s: %v", path, err)
	}
	defer f.Close()

	var entries []audittrail.AuditEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry audittrail.AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("unmarshal audit entry: %v", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return entries
}

// findAuditLog finds the first audit.jsonl file under rolloutDir.
func findAuditLog(t *testing.T, rolloutDir string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(rolloutDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "audit.jsonl" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk rolloutDir: %v", err)
	}
	return found
}

// TestAuditTrail_RunStarted_WrittenOnEnable verifies that when AuditTrailEnabled
// is set, the run.started event is written to the audit log with provenance fields.
func TestAuditTrail_RunStarted_WrittenOnEnable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:                "audit this",
		InitiatorAPIKeyPrefix: "sk_test",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 audit entries (run.started + run.completed), got %d", len(entries))
	}

	// First entry should be run.started
	first := entries[0]
	if first.EventType != "run.started" {
		t.Errorf("first entry EventType = %q, want %q", first.EventType, "run.started")
	}
	if first.RunID != run.ID {
		t.Errorf("first entry RunID = %q, want %q", first.RunID, run.ID)
	}
	if first.PrevHash != "genesis" {
		t.Errorf("first entry PrevHash = %q, want %q", first.PrevHash, "genesis")
	}
	if first.EntryHash == "" {
		t.Error("first entry EntryHash is empty")
	}
	// Provenance fields
	if v, ok := first.Payload["model"]; !ok || v == "" {
		t.Errorf("run.started missing model field, payload: %v", first.Payload)
	}
	if v, ok := first.Payload["initiator_api_key_prefix"]; !ok {
		t.Errorf("run.started missing initiator_api_key_prefix, payload: %v", first.Payload)
	} else if v != "sk_test" {
		t.Errorf("initiator_api_key_prefix = %q, want %q", v, "sk_test")
	}
}

// TestAuditTrail_RunCompleted_Written verifies that run.completed is written.
func TestAuditTrail_RunCompleted_Written(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}

	last := entries[len(entries)-1]
	if last.EventType != "run.completed" && last.EventType != "run.failed" {
		t.Errorf("last entry EventType = %q, want run.completed or run.failed", last.EventType)
	}
}

func TestAuditTrail_ActiveRunsShareDateBucketWriter(t *testing.T) {
	dir := t.TempDir()
	provider := newHangingProvider()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:      "test-model",
		RolloutDir:        dir,
		AuditTrailEnabled: true,
	})

	run1, err := runner.StartRun(RunRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("StartRun first: %v", err)
	}
	run2, err := runner.StartRun(RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("StartRun second: %v", err)
	}
	waitForStatus(t, runner, run1.ID, RunStatusRunning)
	waitForStatus(t, runner, run2.ID, RunStatusRunning)

	runner.mu.RLock()
	writer1 := runner.runs[run1.ID].auditWriter
	writer2 := runner.runs[run2.ID].auditWriter
	runner.mu.RUnlock()
	if writer1 == nil || writer2 == nil {
		t.Fatalf("expected both runs to have audit writers, got writer1=%v writer2=%v", writer1, writer2)
	}
	if writer1 != writer2 {
		t.Fatalf("same-day active runs should share one audit writer, got %p and %p", writer1, writer2)
	}

	provider.Release()
	waitForStatus(t, runner, run1.ID, RunStatusCompleted, RunStatusFailed)
	waitForStatus(t, runner, run2.ID, RunStatusCompleted, RunStatusFailed)
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestAuditTrail_HashChainValid verifies hash chain integrity across entries.
func TestAuditTrail_HashChainValid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Register a state-modifying tool so audit.action events are generated.
	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "run bash",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "output", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            3,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "run bash"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) < 3 {
		t.Fatalf("expected >= 3 audit entries (started + action + completed), got %d", len(entries))
	}

	// Verify hash chain integrity.
	if entries[0].PrevHash != "genesis" {
		t.Errorf("entries[0].PrevHash = %q, want %q", entries[0].PrevHash, "genesis")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].PrevHash != entries[i-1].EntryHash {
			t.Errorf("chain broken at %d: PrevHash=%q != entries[%d].EntryHash=%q",
				i, entries[i].PrevHash, i-1, entries[i-1].EntryHash)
		}
	}
}

func TestAuditTrail_RunCompletedStatusWaitsForAuditPersistence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	release := make(chan struct{})
	runner := NewRunner(&blockingAuditProvider{release: release}, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            1,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "complete once"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	auditPath := auditLogPath(dir)
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries := readAuditEntries(t, auditPath)
		if len(entries) >= 1 && entries[0].EventType == string(EventRunStarted) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run.started audit entry")
		}
		time.Sleep(10 * time.Millisecond)
	}

	lockFile, err := os.OpenFile(auditPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", auditPath, err)
	}
	locked := false
	defer func() {
		if lockFile == nil {
			return
		}
		if locked {
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatalf("flock unlock: %v", err)
			}
		}
		if err := lockFile.Close(); err != nil {
			t.Fatalf("close audit lock file: %v", err)
		}
	}()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock exclusive: %v", err)
	}
	locked = true

	close(release)
	time.Sleep(50 * time.Millisecond)
	current, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("GetRun(%q): not found", run.ID)
	}
	if current.Status == RunStatusCompleted {
		t.Fatalf("run reached completed status before terminal audit entry could be persisted")
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("flock unlock: %v", err)
	}
	locked = false
	if err := lockFile.Close(); err != nil {
		t.Fatalf("close audit lock file: %v", err)
	}
	lockFile = nil

	waitForStatus(t, runner, run.ID, RunStatusCompleted)

	entries := readAuditEntries(t, auditPath)
	if got := entries[len(entries)-1].EventType; got != string(EventRunCompleted) {
		t.Fatalf("last entry = %q, want %q", got, EventRunCompleted)
	}
}

// TestAuditTrail_StateModifyingToolEmitsAuditAction verifies that calling a
// state-modifying tool emits audit.action events.
func TestAuditTrail_StateModifyingToolEmitsAuditAction(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "run bash",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "output", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            3,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "run bash"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Verify audit.action event appears in the run events (SSE stream).
	events := collectEvents(t, runner, run.ID)
	var found bool
	for _, evt := range events {
		if evt.Type == EventAuditAction {
			found = true
			if v, ok := evt.Payload["tool"]; !ok || v != "bash" {
				t.Errorf("audit.action payload tool = %v, want %q", v, "bash")
			}
			break
		}
	}
	if !found {
		t.Error("no audit.action event emitted for bash tool call")
	}

	// Also verify it appears in the audit log file.
	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}
	auditEntries := readAuditEntries(t, auditPath)
	var foundAuditAction bool
	for _, e := range auditEntries {
		if e.EventType == "audit.action" {
			foundAuditAction = true
			break
		}
	}
	if !foundAuditAction {
		t.Error("no audit.action entry in audit.jsonl")
	}
}

// TestAuditTrail_ReadOnlyToolNoAuditAction verifies that read-only tools
// do NOT emit audit.action events.
func TestAuditTrail_ReadOnlyToolNoAuditAction(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "grep",
		Description: "search files",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"pattern": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "results", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "grep", Arguments: `{"pattern":"foo"}`}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            3,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "search"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Verify no audit.action in SSE events.
	events := collectEvents(t, runner, run.ID)
	for _, evt := range events {
		if evt.Type == EventAuditAction {
			t.Errorf("unexpected audit.action event for read-only tool grep: %+v", evt)
		}
	}

	// Verify no audit.action in audit log.
	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}
	auditEntries := readAuditEntries(t, auditPath)
	for _, e := range auditEntries {
		if e.EventType == "audit.action" {
			t.Errorf("unexpected audit.action entry in audit.jsonl for grep: %+v", e)
		}
	}
}

// TestAuditTrail_DisabledByDefault verifies that without AuditTrailEnabled,
// no audit.jsonl is written and no audit.action events appear.
func TestAuditTrail_DisabledByDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "run bash",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "output", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            3,
		RolloutDir:          dir,
		// AuditTrailEnabled: false (default)
	})

	run, err := runner.StartRun(RunRequest{Prompt: "run bash"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// No audit.jsonl should be created.
	auditPath := findAuditLog(t, dir)
	if auditPath != "" {
		t.Errorf("audit.jsonl created when AuditTrailEnabled=false: %s", auditPath)
	}

	// No audit.action events in SSE stream.
	events := collectEvents(t, runner, run.ID)
	for _, evt := range events {
		if evt.Type == EventAuditAction {
			t.Errorf("unexpected audit.action event when AuditTrailEnabled=false: %+v", evt)
		}
	}
}

// TestAuditTrail_RunFailed_Written verifies that run.failed is written to audit log.
func TestAuditTrail_RunFailed_Written(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prov := &errorProvider{err: errAuditTest}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}

	last := entries[len(entries)-1]
	if last.EventType != "run.failed" {
		t.Errorf("last entry EventType = %q, want %q", last.EventType, "run.failed")
	}
}
