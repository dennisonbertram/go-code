package harness

import (
	"testing"
)

func TestEventTypeConstants(t *testing.T) {
	// Spot-check that constants match their string values
	tests := []struct {
		got  EventType
		want string
	}{
		{EventRunStarted, "run.started"},
		{EventRunCompleted, "run.completed"},
		{EventRunFailed, "run.failed"},
		{EventToolCallStarted, "tool.call.started"},
		{EventAssistantMessageDelta, "assistant.message.delta"},
		{EventUsageDelta, "usage.delta"},
		{EventHookStarted, "hook.started"},
		{EventMemoryObserveStarted, "memory.observe.started"},
		{EventAssistantThinkingDelta, "assistant.thinking.delta"},
		{EventProviderResolved, "provider.resolved"},
		{EventAuditAction, "audit.action"},
	}
	for _, tt := range tests {
		if string(tt.got) != tt.want {
			t.Errorf("EventType %q != %q", tt.got, tt.want)
		}
	}
}

func TestIsTerminalEvent(t *testing.T) {
	tests := []struct {
		event EventType
		want  bool
	}{
		{EventRunCompleted, true},
		{EventRunFailed, true},
		{EventRunStarted, false},
		{EventLLMTurnRequested, false},
		{EventType(""), false},
		{EventType("unknown.event"), false},
	}
	for _, tt := range tests {
		got := IsTerminalEvent(tt.event)
		if got != tt.want {
			t.Errorf("IsTerminalEvent(%q) = %v, want %v", tt.event, got, tt.want)
		}
	}
}

func TestAllEventTypes_Count(t *testing.T) {
	all := AllEventTypes()
	if len(all) != 77 {
		t.Errorf("AllEventTypes() returned %d events, want 77", len(all))
	}
	// Verify no duplicates
	seen := make(map[EventType]bool)
	for _, et := range all {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestEventRunCostLimitReachedType(t *testing.T) {
	if string(EventRunCostLimitReached) != "run.cost_limit_reached" {
		t.Errorf("EventRunCostLimitReached = %q, want %q", EventRunCostLimitReached, "run.cost_limit_reached")
	}
	// Cost limit reached is NOT a terminal event (run.completed follows it).
	if IsTerminalEvent(EventRunCostLimitReached) {
		t.Error("IsTerminalEvent(EventRunCostLimitReached) = true, want false")
	}
	// Verify it is included in AllEventTypes.
	found := false
	for _, et := range AllEventTypes() {
		if et == EventRunCostLimitReached {
			found = true
			break
		}
	}
	if !found {
		t.Error("EventRunCostLimitReached not found in AllEventTypes()")
	}
}

func TestEventToolHookTypes(t *testing.T) {
	tests := []struct {
		got  EventType
		want string
	}{
		{EventToolHookStarted, "tool_hook.started"},
		{EventToolHookFailed, "tool_hook.failed"},
		{EventToolHookCompleted, "tool_hook.completed"},
	}
	for _, tt := range tests {
		if string(tt.got) != tt.want {
			t.Errorf("EventType %q != %q", tt.got, tt.want)
		}
	}
}

func TestEventSkillForkTypes(t *testing.T) {
	tests := []struct {
		got  EventType
		want string
	}{
		{EventSkillForkStarted, "skill.fork.started"},
		{EventSkillForkCompleted, "skill.fork.completed"},
		{EventSkillForkFailed, "skill.fork.failed"},
	}
	for _, tt := range tests {
		if string(tt.got) != tt.want {
			t.Errorf("EventType %q != %q", tt.got, tt.want)
		}
	}
}

func TestIsTerminalEvent_ForkEvents(t *testing.T) {
	// Fork events are NOT terminal events
	forkEvents := []EventType{
		EventSkillForkStarted,
		EventSkillForkCompleted,
		EventSkillForkFailed,
	}
	for _, et := range forkEvents {
		if IsTerminalEvent(et) {
			t.Errorf("IsTerminalEvent(%q) = true, want false (fork events are not terminal)", et)
		}
	}
}

func TestRunCompletedPayload_RoundTrip(t *testing.T) {
	orig := RunCompletedPayload{
		Output:      "done",
		UsageTotals: map[string]any{"prompt_tokens": float64(100)},
		CostTotals:  map[string]any{"total_usd": float64(0.01)},
	}
	payload := orig.ToPayload()
	parsed, err := ParseRunCompletedPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Output != orig.Output {
		t.Errorf("Output = %q, want %q", parsed.Output, orig.Output)
	}
}

func TestRunFailedPayload_RoundTrip(t *testing.T) {
	orig := RunFailedPayload{
		Error:       "something broke",
		UsageTotals: map[string]any{"prompt_tokens": float64(50)},
	}
	payload := orig.ToPayload()
	parsed, err := ParseRunFailedPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Error != orig.Error {
		t.Errorf("Error = %q, want %q", parsed.Error, orig.Error)
	}
}

func TestParseEventID(t *testing.T) {
	tests := []struct {
		id      string
		wantRun string
		wantSeq uint64
		wantErr bool
	}{
		{"run_1:0", "run_1", 0, false},
		{"run_1:7", "run_1", 7, false},
		{"run_99:123", "run_99", 123, false},
		{"", "", 0, true},
		{"no-colon", "", 0, true},
		{"trailing:", "", 0, true},
		{"run_1:abc", "", 0, true},
		{"run_1:-1", "", 0, true},
		{":0", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			runID, seq, err := ParseEventID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEventID(%q) expected error, got runID=%q seq=%d", tt.id, runID, seq)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEventID(%q) unexpected error: %v", tt.id, err)
			}
			if runID != tt.wantRun {
				t.Errorf("ParseEventID(%q) runID = %q, want %q", tt.id, runID, tt.wantRun)
			}
			if seq != tt.wantSeq {
				t.Errorf("ParseEventID(%q) seq = %d, want %d", tt.id, seq, tt.wantSeq)
			}
		})
	}
}

func TestUsageDeltaPayload_RoundTrip(t *testing.T) {
	orig := UsageDeltaPayload{
		Step:              1,
		UsageStatus:       "provider_reported",
		CostStatus:        "available",
		TurnCostUSD:       0.001,
		CumulativeCostUSD: 0.003,
	}
	payload := orig.ToPayload()
	parsed, err := ParseUsageDeltaPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Step != orig.Step {
		t.Errorf("Step = %d, want %d", parsed.Step, orig.Step)
	}
	if parsed.CostStatus != orig.CostStatus {
		t.Errorf("CostStatus = %q, want %q", parsed.CostStatus, orig.CostStatus)
	}
}

func TestToolOutputDeltaPayload_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := ToolOutputDeltaPayload{
		CallID:      "call_abc123",
		Tool:        "bash",
		StreamIndex: 3,
		Content:     "line output\n",
	}
	payload := orig.ToPayload()

	// Verify the map has expected keys.
	if payload["call_id"] != "call_abc123" {
		t.Errorf("payload call_id = %v, want %q", payload["call_id"], "call_abc123")
	}
	if payload["tool"] != "bash" {
		t.Errorf("payload tool = %v, want %q", payload["tool"], "bash")
	}
	if payload["content"] != "line output\n" {
		t.Errorf("payload content = %v, want %q", payload["content"], "line output\n")
	}

	// stream_index is encoded as float64 after JSON round-trip.
	if payload["stream_index"] != float64(3) {
		t.Errorf("payload stream_index = %v, want %v", payload["stream_index"], float64(3))
	}

	parsed, err := ParseToolOutputDeltaPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CallID != orig.CallID {
		t.Errorf("CallID = %q, want %q", parsed.CallID, orig.CallID)
	}
	if parsed.Tool != orig.Tool {
		t.Errorf("Tool = %q, want %q", parsed.Tool, orig.Tool)
	}
	if parsed.StreamIndex != orig.StreamIndex {
		t.Errorf("StreamIndex = %d, want %d", parsed.StreamIndex, orig.StreamIndex)
	}
	if parsed.Content != orig.Content {
		t.Errorf("Content = %q, want %q", parsed.Content, orig.Content)
	}
}

func TestToolOutputDeltaPayload_ZeroValues(t *testing.T) {
	t.Parallel()
	var p ToolOutputDeltaPayload
	payload := p.ToPayload()
	parsed, err := ParseToolOutputDeltaPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.StreamIndex != 0 {
		t.Errorf("StreamIndex = %d, want 0", parsed.StreamIndex)
	}
	if parsed.Content != "" {
		t.Errorf("Content = %q, want empty", parsed.Content)
	}
}

func TestParseToolOutputDeltaPayload_InvalidInput(t *testing.T) {
	t.Parallel()
	// Passing a nil map should produce zero-value struct, not an error.
	parsed, err := ParseToolOutputDeltaPayload(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil payload: %v", err)
	}
	if parsed.StreamIndex != 0 || parsed.Content != "" {
		t.Errorf("expected zero-value struct for nil input, got %+v", parsed)
	}
}

func TestEventToolOutputDeltaConstant(t *testing.T) {
	t.Parallel()
	if string(EventToolOutputDelta) != "tool.output.delta" {
		t.Errorf("EventToolOutputDelta = %q, want %q", EventToolOutputDelta, "tool.output.delta")
	}
}

func TestContextWindowEventTypeConstants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		got  EventType
		want string
	}{
		{EventContextWindowSnapshot, "context.window.snapshot"},
		{EventContextWindowWarning, "context.window.warning"},
	}
	for _, tt := range tests {
		if string(tt.got) != tt.want {
			t.Errorf("EventType %q != %q", tt.got, tt.want)
		}
	}
}

func TestContextWindowEventsNotTerminal(t *testing.T) {
	t.Parallel()
	events := []EventType{
		EventContextWindowSnapshot,
		EventContextWindowWarning,
	}
	for _, et := range events {
		if IsTerminalEvent(et) {
			t.Errorf("IsTerminalEvent(%q) = true, want false", et)
		}
	}
}

func TestContextWindowEventsInAllEventTypes(t *testing.T) {
	t.Parallel()
	all := AllEventTypes()
	wantPresent := []EventType{
		EventContextWindowSnapshot,
		EventContextWindowWarning,
	}
	for _, want := range wantPresent {
		found := false
		for _, et := range all {
			if et == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q not found in AllEventTypes()", want)
		}
	}
}

func TestContextWindowSnapshotPayload_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := ContextWindowSnapshotPayload{
		Step:                   3,
		ProviderReportedTokens: 2500,
		ProviderReported:       true,
		EstimatedTotalTokens:   2200,
		MaxContextTokens:       128000,
		UsageRatio:             0.0195,
		HeadroomTokens:         125500,
		Breakdown: ContextWindowSnapshotBreakdown{
			SystemPromptTokens: 300,
			ConversationTokens: 1800,
			ToolResultTokens:   100,
			Estimated:          true,
		},
	}
	payload := orig.ToPayload()
	parsed, err := ParseContextWindowSnapshotPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Step != orig.Step {
		t.Errorf("Step = %d, want %d", parsed.Step, orig.Step)
	}
	if parsed.ProviderReportedTokens != orig.ProviderReportedTokens {
		t.Errorf("ProviderReportedTokens = %d, want %d", parsed.ProviderReportedTokens, orig.ProviderReportedTokens)
	}
	if parsed.ProviderReported != orig.ProviderReported {
		t.Errorf("ProviderReported = %v, want %v", parsed.ProviderReported, orig.ProviderReported)
	}
	if parsed.MaxContextTokens != orig.MaxContextTokens {
		t.Errorf("MaxContextTokens = %d, want %d", parsed.MaxContextTokens, orig.MaxContextTokens)
	}
	if !parsed.Breakdown.Estimated {
		t.Error("Breakdown.Estimated should always be true")
	}
}

func TestContextWindowSnapshotPayload_BreakdownEstimatedAlwaysTrue(t *testing.T) {
	t.Parallel()
	// Verify that even when Estimated is false in the struct, we can set it.
	p := ContextWindowSnapshotPayload{
		Breakdown: ContextWindowSnapshotBreakdown{Estimated: true},
	}
	payload := p.ToPayload()
	bd, ok := payload["breakdown"].(map[string]any)
	if !ok {
		t.Fatal("breakdown not a map")
	}
	if est, ok := bd["estimated"].(bool); !ok || !est {
		t.Errorf("breakdown.estimated should be true, got %v", bd["estimated"])
	}
}

func TestContextWindowWarningPayload_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := ContextWindowWarningPayload{
		Step:             5,
		UsageRatio:       0.85,
		Threshold:        0.80,
		ProviderReported: true,
		TokensUsed:       108800,
		MaxContextTokens: 128000,
	}
	payload := orig.ToPayload()
	parsed, err := ParseContextWindowWarningPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Step != orig.Step {
		t.Errorf("Step = %d, want %d", parsed.Step, orig.Step)
	}
	if parsed.UsageRatio != orig.UsageRatio {
		t.Errorf("UsageRatio = %f, want %f", parsed.UsageRatio, orig.UsageRatio)
	}
	if parsed.Threshold != orig.Threshold {
		t.Errorf("Threshold = %f, want %f", parsed.Threshold, orig.Threshold)
	}
	if parsed.ProviderReported != orig.ProviderReported {
		t.Errorf("ProviderReported = %v, want %v", parsed.ProviderReported, orig.ProviderReported)
	}
	if parsed.TokensUsed != orig.TokensUsed {
		t.Errorf("TokensUsed = %d, want %d", parsed.TokensUsed, orig.TokensUsed)
	}
	if parsed.MaxContextTokens != orig.MaxContextTokens {
		t.Errorf("MaxContextTokens = %d, want %d", parsed.MaxContextTokens, orig.MaxContextTokens)
	}
}

func TestContextWindowWarningPayload_ZeroValues(t *testing.T) {
	t.Parallel()
	var p ContextWindowWarningPayload
	payload := p.ToPayload()
	parsed, err := ParseContextWindowWarningPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Step != 0 || parsed.UsageRatio != 0 || parsed.MaxContextTokens != 0 {
		t.Errorf("expected zero-value struct, got %+v", parsed)
	}
}

func TestContextWindowSnapshotPayload_ZeroValues(t *testing.T) {
	t.Parallel()
	var p ContextWindowSnapshotPayload
	payload := p.ToPayload()
	parsed, err := ParseContextWindowSnapshotPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Step != 0 || parsed.MaxContextTokens != 0 {
		t.Errorf("expected zero-value struct, got %+v", parsed)
	}
}

func TestProfileEfficiencySuggestionEvent(t *testing.T) {
	t.Parallel()
	// Verify the constant has the correct string value.
	if string(EventProfileEfficiencySuggestion) != "profile.efficiency_suggestion" {
		t.Errorf("EventProfileEfficiencySuggestion = %q, want %q",
			EventProfileEfficiencySuggestion, "profile.efficiency_suggestion")
	}
	// It is not a terminal event.
	if IsTerminalEvent(EventProfileEfficiencySuggestion) {
		t.Error("IsTerminalEvent(EventProfileEfficiencySuggestion) = true, want false")
	}
	// It is included in AllEventTypes.
	found := false
	for _, et := range AllEventTypes() {
		if et == EventProfileEfficiencySuggestion {
			found = true
			break
		}
	}
	if !found {
		t.Error("EventProfileEfficiencySuggestion not found in AllEventTypes()")
	}
}
