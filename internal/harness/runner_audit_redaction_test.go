package harness

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"go-agent-harness/internal/forensics/redaction"
)

// secretPattern is a custom regex used across both tests to mark the known secret.
const knownSecret = "SUPERSECRET-abc123xyz789"

// customSecretRegexp matches knownSecret verbatim.
var customSecretRegexp = regexp.MustCompile(regexp.QuoteMeta(knownSecret))

// redactionPipelineWithSecret builds a Pipeline that redacts customSecretRegexp
// (marked as "custom") and applies the default StorageModeRedacted to all event
// types.
func redactionPipelineWithSecret() *redaction.Pipeline {
	r := redaction.NewRedactor([]*regexp.Regexp{customSecretRegexp})
	return redaction.NewPipeline(r, redaction.EventClassConfig{})
}

// TestAuditRedaction_SecretNotInAuditLog (T-PFIX-5) verifies that when a
// RedactionPipeline is configured, secrets that appear in the prompt (run.started)
// and in tool arguments (audit.action) are NOT written verbatim to audit.jsonl.
// The redaction marker ([REDACTED:custom]) must appear instead.
//
// This test FAILS today because writeAudit bypasses the RedactionPipeline.
func TestAuditRedaction_SecretNotInAuditLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Register a state-modifying tool so audit.action entries appear.
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

	// Tool call arguments contain the secret.
	secretArgs := `{"command":"echo ` + knownSecret + `"}`

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: secretArgs}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            3,
		RolloutDir:          dir,
		AuditTrailEnabled:   true,
		RedactionPipeline:   redactionPipelineWithSecret(),
	})

	// Include the secret in the prompt so it appears in run.started audit payload.
	prompt := "use this key: " + knownSecret
	run, err := runner.StartRun(RunRequest{
		Prompt:                prompt,
		InitiatorAPIKeyPrefix: "sk_test1",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	auditPath := findAuditLog(t, dir)
	if auditPath == "" {
		t.Fatal("audit.jsonl not found — AuditTrailEnabled must be true")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 audit entries, got %d", len(entries))
	}

	// Re-marshal each entry to a raw JSON string for easy scanning.
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		rawStr := string(raw)

		if strings.Contains(rawStr, knownSecret) {
			t.Errorf("audit entry %q contains secret verbatim in entry: %s",
				entry.EventType, rawStr)
		}
	}

	// At least one entry must carry a [REDACTED:custom] marker (proving the
	// pipeline was actually applied, not just that the secret was absent for
	// unrelated reasons).
	var foundRedactionMarker bool
	for _, entry := range entries {
		raw, _ := json.Marshal(entry)
		if strings.Contains(string(raw), "[REDACTED:custom]") {
			foundRedactionMarker = true
			break
		}
	}
	if !foundRedactionMarker {
		t.Error("expected at least one audit entry with [REDACTED:custom] marker — pipeline was not applied")
	}

	// Verify the hash chain is still intact (redaction must not break it).
	if entries[0].PrevHash != "genesis" {
		t.Errorf("entries[0].PrevHash = %q, want %q", entries[0].PrevHash, "genesis")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].PrevHash != entries[i-1].EntryHash {
			t.Errorf("hash chain broken at index %d: PrevHash=%q != entries[%d].EntryHash=%q",
				i, entries[i].PrevHash, i-1, entries[i-1].EntryHash)
		}
	}
}

// TestAuditPrefix_OnlyPrefixInPayload (T-E-audit-prefix-only) verifies that
// audit.action and run.started entries contain ONLY the 8-char
// initiator_api_key_prefix, not the full API key or secret.
// This deliverable (E) should also pass once redaction is correctly applied.
func TestAuditPrefix_OnlyPrefixInPayload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A realistic-looking (but fake) full API key — longer than 8 chars.
	fullAPIKey := "sk-testXYZABC123456789fullkey"
	prefix := fullAPIKey[:8] // "sk-testX"

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
		// No RedactionPipeline needed: the key prefix/full key distinction is
		// enforced by the audit construction code, not the pipeline.
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:                "hello",
		InitiatorAPIKeyPrefix: prefix,
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
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}

	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		rawStr := string(raw)

		// The full key must never appear anywhere in any audit entry.
		if strings.Contains(rawStr, fullAPIKey) {
			t.Errorf("audit entry %q contains full API key verbatim: %s",
				entry.EventType, rawStr)
		}
	}

	// The run.started entry must contain exactly the prefix.
	var foundPrefix bool
	for _, entry := range entries {
		if entry.EventType != "run.started" {
			continue
		}
		if v, ok := entry.Payload["initiator_api_key_prefix"]; ok {
			if v == prefix {
				foundPrefix = true
			} else {
				t.Errorf("run.started initiator_api_key_prefix = %q, want %q", v, prefix)
			}
		}
	}
	if !foundPrefix {
		t.Error("run.started entry missing initiator_api_key_prefix field with the correct prefix value")
	}
}
