package rollout

import (
	"testing"
	"time"
)

func TestCanonicalize_DefaultOptions(t *testing.T) {
	ts1 := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 3, 12, 10, 0, 1, 0, time.UTC)

	events := []RolloutEvent{
		{
			ID:        "1",
			Type:      "run.started",
			Step:      0,
			Timestamp: ts1,
			Payload:   map[string]any{"run_id": "r1", "step": float64(0)},
		},
		{
			ID:        "2",
			Type:      "tool.call.started",
			Step:      1,
			Timestamp: ts2,
			Payload:   map[string]any{"run_id": "r1", "step": float64(1), "tool": "bash"},
		},
	}

	result := Canonicalize(events, DefaultOptions)

	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}

	// IDs should be stripped.
	for i, ev := range result {
		if ev.ID != "" {
			t.Errorf("event %d: expected empty ID, got %q", i, ev.ID)
		}
	}

	// Timestamps should be zero.
	for i, ev := range result {
		if !ev.Timestamp.IsZero() {
			t.Errorf("event %d: expected zero timestamp, got %v", i, ev.Timestamp)
		}
	}

	// run_id should be stripped from payload.
	for i, ev := range result {
		if _, ok := ev.Payload["run_id"]; ok {
			t.Errorf("event %d: expected run_id stripped from payload", i)
		}
	}

	// Other payload fields should be preserved.
	if result[1].Payload["tool"] != "bash" {
		t.Errorf("expected tool=bash preserved, got %v", result[1].Payload["tool"])
	}
}

func TestCanonicalize_NoStrip(t *testing.T) {
	ts := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	events := []RolloutEvent{
		{
			ID:        "1",
			Type:      "run.started",
			Step:      0,
			Timestamp: ts,
			Payload:   map[string]any{"run_id": "r1"},
		},
	}

	opts := CanonicalizationOptions{}
	result := Canonicalize(events, opts)

	if result[0].ID != "1" {
		t.Errorf("expected ID preserved, got %q", result[0].ID)
	}
	if !result[0].Timestamp.Equal(ts) {
		t.Errorf("expected timestamp preserved, got %v", result[0].Timestamp)
	}
	if result[0].Payload["run_id"] != "r1" {
		t.Errorf("expected run_id preserved, got %v", result[0].Payload["run_id"])
	}
}

func TestCanonicalize_SortsByStep(t *testing.T) {
	events := []RolloutEvent{
		{Type: "tool.call.completed", Step: 2},
		{Type: "run.started", Step: 0},
		{Type: "tool.call.started", Step: 1},
	}

	result := Canonicalize(events, DefaultOptions)

	expectedSteps := []int{0, 1, 2}
	for i, ev := range result {
		if ev.Step != expectedSteps[i] {
			t.Errorf("event %d: expected step %d, got %d", i, expectedSteps[i], ev.Step)
		}
	}
}

func TestCanonicalize_StableSortWithinStep(t *testing.T) {
	events := []RolloutEvent{
		{Type: "llm.turn.requested", Step: 1},
		{Type: "llm.turn.completed", Step: 1},
		{Type: "tool.call.started", Step: 1},
	}

	result := Canonicalize(events, DefaultOptions)

	// Order within step 1 should be preserved.
	expectedTypes := []string{"llm.turn.requested", "llm.turn.completed", "tool.call.started"}
	for i, ev := range result {
		if ev.Type != expectedTypes[i] {
			t.Errorf("event %d: expected type %s, got %s", i, expectedTypes[i], ev.Type)
		}
	}
}

func TestCanonicalize_NilPayload(t *testing.T) {
	events := []RolloutEvent{
		{Type: "run.started", Step: 0, Payload: nil},
	}

	result := Canonicalize(events, DefaultOptions)
	if result[0].Payload != nil {
		t.Errorf("expected nil payload preserved, got %v", result[0].Payload)
	}
}

func TestCanonicalize_DoesNotMutateOriginal(t *testing.T) {
	original := []RolloutEvent{
		{
			ID:        "1",
			Type:      "run.started",
			Timestamp: time.Now(),
			Payload:   map[string]any{"run_id": "r1", "tool": "bash"},
		},
	}

	_ = Canonicalize(original, DefaultOptions)

	// Original should be unchanged.
	if original[0].ID != "1" {
		t.Error("original ID was mutated")
	}
	if _, ok := original[0].Payload["run_id"]; !ok {
		t.Error("original payload was mutated")
	}
}

// T-F1a: DriftOptions must strip ONLY volatile LLM meta (and the default
// non-deterministic fields), while preserving deterministic content: assistant
// content, tool name/arguments, and outcome-bearing fields.
func TestDriftOptions_StripsVolatileMetaOnly(t *testing.T) {
	events := []RolloutEvent{
		{
			ID:        "e1",
			Type:      "llm.turn.completed",
			Step:      1,
			Timestamp: time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC),
			Payload: map[string]any{
				// Volatile LLM meta — must be stripped.
				"total_duration_ms": float64(1234),
				"ttft_ms":           float64(56),
				"latency_ms":        float64(78),
				"provider":          "openai",
				"prompt_hash":       "abc123",
				"model_version":     "gpt-x-2026",
				// Default non-deterministic — also stripped.
				"run_id": "r1",
				// Deterministic content — must be preserved.
				"content": "hello world",
				"tool":    "bash",
			},
		},
	}

	result := Canonicalize(events, DriftOptions)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	p := result[0].Payload

	for _, k := range []string{"total_duration_ms", "ttft_ms", "latency_ms", "provider", "prompt_hash", "model_version", "run_id"} {
		if _, ok := p[k]; ok {
			t.Errorf("expected volatile key %q stripped under DriftOptions", k)
		}
	}

	if p["content"] != "hello world" {
		t.Errorf("expected content preserved, got %v", p["content"])
	}
	if p["tool"] != "bash" {
		t.Errorf("expected tool preserved, got %v", p["tool"])
	}
}

// TestDefaultOptions_KeepsConversationID asserts that DefaultOptions does NOT
// strip conversation_id. conversation_id can be a stable cross-run identifier
// (multi-turn conversations reuse one), so it must survive DefaultOptions
// canonicalization. Only DriftOptions (StripPerRunConversationID:true) removes
// it, because a drift re-run always receives a fresh conversation id.
func TestDefaultOptions_KeepsConversationID(t *testing.T) {
	events := []RolloutEvent{
		{
			Type: "run.started",
			Step: 0,
			Payload: map[string]any{
				"run_id":          "run-abc",
				"conversation_id": "conv-stable-123",
			},
		},
	}

	result := Canonicalize(events, DefaultOptions)
	p := result[0].Payload

	// run_id must be stripped (StripRunIDs:true).
	if _, ok := p["run_id"]; ok {
		t.Errorf("DefaultOptions must strip run_id")
	}
	// conversation_id must be KEPT (StripPerRunConversationID is false in DefaultOptions).
	if _, ok := p["conversation_id"]; !ok {
		t.Errorf("DefaultOptions must NOT strip conversation_id; it was unexpectedly removed")
	}
	if p["conversation_id"] != "conv-stable-123" {
		t.Errorf("conversation_id value altered: got %v", p["conversation_id"])
	}
}

// TestDriftOptions_StripsConversationID asserts that DriftOptions strips
// conversation_id (StripPerRunConversationID:true), because a drift re-run
// always gets a fresh run/conversation id and stripping is required for the
// re-run to match the original.
func TestDriftOptions_StripsConversationID(t *testing.T) {
	events := []RolloutEvent{
		{
			Type: "run.started",
			Step: 0,
			Payload: map[string]any{
				"run_id":          "run-xyz",
				"conversation_id": "conv-xyz",
			},
		},
	}

	result := Canonicalize(events, DriftOptions)
	p := result[0].Payload

	if _, ok := p["run_id"]; ok {
		t.Errorf("DriftOptions must strip run_id")
	}
	if _, ok := p["conversation_id"]; ok {
		t.Errorf("DriftOptions must strip conversation_id; got %v", p["conversation_id"])
	}
}

// DriftOptions must NOT strip volatile meta unless StripVolatileLLMMeta is set:
// DefaultOptions leaves the volatile LLM-meta keys intact.
func TestDefaultOptions_KeepsVolatileMeta(t *testing.T) {
	events := []RolloutEvent{
		{
			Type: "llm.turn.completed",
			Step: 1,
			Payload: map[string]any{
				"provider":          "openai",
				"total_duration_ms": float64(1234),
			},
		},
	}

	result := Canonicalize(events, DefaultOptions)
	p := result[0].Payload
	if p["provider"] != "openai" {
		t.Errorf("DefaultOptions should keep provider, got %v", p["provider"])
	}
	if p["total_duration_ms"] != float64(1234) {
		t.Errorf("DefaultOptions should keep total_duration_ms, got %v", p["total_duration_ms"])
	}
}
