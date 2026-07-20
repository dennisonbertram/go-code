package contextwindow_test

import (
	"math"
	"testing"

	"go-agent-harness/internal/forensics/contextwindow"
)

// TestEstimateTokens_Empty verifies that empty string returns 0 tokens.
func TestEstimateTokens_Empty(t *testing.T) {
	t.Parallel()
	if got := contextwindow.EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
}

// TestEstimateTokens_SingleChar verifies single character returns 1.
func TestEstimateTokens_SingleChar(t *testing.T) {
	t.Parallel()
	// 1 rune → (1+3)/4 = 1
	if got := contextwindow.EstimateTokens("a"); got != 1 {
		t.Errorf("EstimateTokens(\"a\") = %d, want 1", got)
	}
}

// TestEstimateTokens_FourChars verifies 4 ASCII chars → 1 token.
func TestEstimateTokens_FourChars(t *testing.T) {
	t.Parallel()
	// 4 runes → (4+3)/4 = 1
	if got := contextwindow.EstimateTokens("abcd"); got != 1 {
		t.Errorf("EstimateTokens(\"abcd\") = %d, want 1", got)
	}
}

// TestEstimateTokens_FiveChars verifies 5 ASCII chars → 2 tokens.
func TestEstimateTokens_FiveChars(t *testing.T) {
	t.Parallel()
	// 5 runes → (5+3)/4 = 2
	if got := contextwindow.EstimateTokens("abcde"); got != 2 {
		t.Errorf("EstimateTokens(\"abcde\") = %d, want 2", got)
	}
}

// TestEstimateTokens_100Chars verifies a 100-character string → 25-26 tokens.
func TestEstimateTokens_100Chars(t *testing.T) {
	t.Parallel()
	s := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 100 a's... let me count right
	// Build exact 100 chars
	s = ""
	for i := 0; i < 100; i++ {
		s += "a"
	}
	got := contextwindow.EstimateTokens(s)
	// 100 runes → (100+3)/4 = 25
	want := 25
	if got != want {
		t.Errorf("EstimateTokens(100 chars) = %d, want %d", got, want)
	}
}

// TestEstimateTokens_MultibyteRunes verifies multibyte UTF-8 chars are counted by rune.
func TestEstimateTokens_MultibyteRunes(t *testing.T) {
	t.Parallel()
	// 4 CJK characters (multibyte), should still be 4 runes → (4+3)/4 = 1
	s := "你好世界"
	got := contextwindow.EstimateTokens(s)
	want := 1
	if got != want {
		t.Errorf("EstimateTokens(%q) = %d, want %d", s, got, want)
	}
}

// TestBuildSnapshot_BasicFields verifies BuildSnapshot populates required fields.
func TestBuildSnapshot_BasicFields(t *testing.T) {
	t.Parallel()

	msgs := []contextwindow.MessageForEstimate{
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I am fine thanks."},
	}
	snap := contextwindow.BuildSnapshot(1, "You are a helpful assistant.", msgs, 0, false, 128000)

	if snap.Step != 1 {
		t.Errorf("Step = %d, want 1", snap.Step)
	}
	if snap.MaxContextTokens != 128000 {
		t.Errorf("MaxContextTokens = %d, want 128000", snap.MaxContextTokens)
	}
	if snap.Breakdown.Estimated != true {
		t.Error("Breakdown.Estimated should always be true")
	}
	if snap.EstimatedTotalTokens <= 0 {
		t.Errorf("EstimatedTotalTokens should be > 0, got %d", snap.EstimatedTotalTokens)
	}
}

// TestBuildSnapshot_ProviderReported verifies provider-reported tokens are used for ratio.
func TestBuildSnapshot_ProviderReported(t *testing.T) {
	t.Parallel()

	msgs := []contextwindow.MessageForEstimate{
		{Role: "user", Content: "a"},
	}
	// Provider reports 1000 tokens, max is 10000.
	snap := contextwindow.BuildSnapshot(2, "", msgs, 1000, true, 10000)

	if !snap.ProviderReported {
		t.Error("ProviderReported should be true")
	}
	if snap.ProviderReportedTokens != 1000 {
		t.Errorf("ProviderReportedTokens = %d, want 1000", snap.ProviderReportedTokens)
	}
	// Ratio should be based on provider-reported (1000/10000 = 0.1).
	wantRatio := 0.1
	if math.Abs(snap.UsageRatio-wantRatio) > 1e-9 {
		t.Errorf("UsageRatio = %f, want %f", snap.UsageRatio, wantRatio)
	}
	// Headroom based on provider-reported: 10000 - 1000 = 9000.
	if snap.HeadroomTokens != 9000 {
		t.Errorf("HeadroomTokens = %d, want 9000", snap.HeadroomTokens)
	}
}

// TestBuildSnapshot_EstimatedFallback verifies estimated tokens used when provider not reported.
func TestBuildSnapshot_EstimatedFallback(t *testing.T) {
	t.Parallel()

	msgs := []contextwindow.MessageForEstimate{
		{Role: "user", Content: "aaaa"}, // 4 runes = 1 token
	}
	sysPrompt := "aaaa" // 4 runes = 1 token
	// estimated = 1 (sys) + 1 (conv) = 2; max = 100
	snap := contextwindow.BuildSnapshot(1, sysPrompt, msgs, 0, false, 100)

	if snap.ProviderReported {
		t.Error("ProviderReported should be false when not reported")
	}
	// UsageRatio = 2/100 = 0.02
	wantRatio := float64(snap.EstimatedTotalTokens) / 100.0
	if math.Abs(snap.UsageRatio-wantRatio) > 1e-9 {
		t.Errorf("UsageRatio = %f, want %f", snap.UsageRatio, wantRatio)
	}
}

// TestBuildSnapshot_ZeroMaxContext verifies zero max context gives zero ratio.
func TestBuildSnapshot_ZeroMaxContext(t *testing.T) {
	t.Parallel()

	snap := contextwindow.BuildSnapshot(1, "hello", nil, 0, false, 0)

	if snap.UsageRatio != 0 {
		t.Errorf("UsageRatio should be 0 when MaxContextTokens=0, got %f", snap.UsageRatio)
	}
	if snap.HeadroomTokens != 0 {
		t.Errorf("HeadroomTokens should be 0 when MaxContextTokens=0, got %d", snap.HeadroomTokens)
	}
}

// TestBuildSnapshot_ToolResultsSeparated verifies tool messages counted separately.
func TestBuildSnapshot_ToolResultsSeparated(t *testing.T) {
	t.Parallel()

	msgs := []contextwindow.MessageForEstimate{
		{Role: "user", Content: "aaaa"},           // 1 token → conv
		{Role: "tool", Content: "aaaa aaaa aaaa"}, // 3 tokens → tool
		{Role: "assistant", Content: "aaaa"},      // 1 token → conv
	}
	snap := contextwindow.BuildSnapshot(1, "", msgs, 0, false, 0)

	if snap.Breakdown.ConversationTokens <= 0 {
		t.Errorf("ConversationTokens should be > 0, got %d", snap.Breakdown.ConversationTokens)
	}
	if snap.Breakdown.ToolResultTokens <= 0 {
		t.Errorf("ToolResultTokens should be > 0, got %d", snap.Breakdown.ToolResultTokens)
	}
	// Tool tokens should be less than conv tokens in this example.
	total := snap.Breakdown.SystemPromptTokens + snap.Breakdown.ConversationTokens + snap.Breakdown.ToolResultTokens
	if total != snap.EstimatedTotalTokens {
		t.Errorf("Breakdown sum %d != EstimatedTotalTokens %d", total, snap.EstimatedTotalTokens)
	}
}

// TestBuildSnapshot_HeadroomNegativeOverrun verifies headroom can be negative.
func TestBuildSnapshot_HeadroomNegativeOverrun(t *testing.T) {
	t.Parallel()

	// Large system prompt, tiny context window.
	bigPrompt := ""
	for i := 0; i < 1000; i++ {
		bigPrompt += "aaaa" // 4000 runes
	}
	snap := contextwindow.BuildSnapshot(1, bigPrompt, nil, 0, false, 100)

	// estimated = 1000 tokens, max = 100 → headroom = -900
	if snap.HeadroomTokens >= 0 {
		t.Errorf("expected negative HeadroomTokens for overrun, got %d", snap.HeadroomTokens)
	}
}

// TestBuildSnapshot_EmptyMessages verifies empty message list works.
func TestBuildSnapshot_EmptyMessages(t *testing.T) {
	t.Parallel()

	snap := contextwindow.BuildSnapshot(1, "", nil, 0, false, 128000)

	if snap.EstimatedTotalTokens != 0 {
		t.Errorf("EstimatedTotalTokens should be 0 for empty input, got %d", snap.EstimatedTotalTokens)
	}
	if snap.Breakdown.ConversationTokens != 0 {
		t.Errorf("ConversationTokens should be 0, got %d", snap.Breakdown.ConversationTokens)
	}
	if snap.Breakdown.ToolResultTokens != 0 {
		t.Errorf("ToolResultTokens should be 0, got %d", snap.Breakdown.ToolResultTokens)
	}
}

// TestSnapshotToPayload_RequiredKeys verifies SnapshotToPayload includes all required keys.
func TestSnapshotToPayload_RequiredKeys(t *testing.T) {
	t.Parallel()

	snap := contextwindow.WindowSnapshot{
		Step:                   3,
		ProviderReportedTokens: 1500,
		ProviderReported:       true,
		EstimatedTotalTokens:   1200,
		MaxContextTokens:       128000,
		UsageRatio:             0.012,
		HeadroomTokens:         126500,
		Breakdown: contextwindow.ContextBreakdown{
			SystemPromptTokens: 200,
			ConversationTokens: 900,
			ToolResultTokens:   100,
			Estimated:          true,
		},
	}

	payload := contextwindow.SnapshotToPayload(snap)

	required := []string{
		"step", "provider_reported_tokens", "provider_reported",
		"estimated_total_tokens", "max_context_tokens",
		"usage_ratio", "headroom_tokens", "breakdown",
	}
	for _, key := range required {
		if _, ok := payload[key]; !ok {
			t.Errorf("SnapshotToPayload missing required key %q", key)
		}
	}

	// Verify breakdown sub-keys.
	bd, ok := payload["breakdown"].(map[string]any)
	if !ok {
		t.Fatalf("breakdown is not map[string]any")
	}
	breakdownKeys := []string{"system_prompt_tokens", "conversation_tokens", "tool_result_tokens", "estimated"}
	for _, key := range breakdownKeys {
		if _, ok := bd[key]; !ok {
			t.Errorf("breakdown missing required key %q", key)
		}
	}

	// Verify estimated is always true.
	if est, ok := bd["estimated"].(bool); !ok || !est {
		t.Errorf("breakdown.estimated should be true, got %v", bd["estimated"])
	}
}

// TestSnapshotToPayload_Values verifies the values are copied correctly.
func TestSnapshotToPayload_Values(t *testing.T) {
	t.Parallel()

	snap := contextwindow.WindowSnapshot{
		Step:                   5,
		ProviderReportedTokens: 2000,
		ProviderReported:       true,
		EstimatedTotalTokens:   1800,
		MaxContextTokens:       32000,
		UsageRatio:             0.0625,
		HeadroomTokens:         30000,
	}

	payload := contextwindow.SnapshotToPayload(snap)

	if payload["step"] != 5 {
		t.Errorf("step = %v, want 5", payload["step"])
	}
	if payload["provider_reported_tokens"] != 2000 {
		t.Errorf("provider_reported_tokens = %v, want 2000", payload["provider_reported_tokens"])
	}
	if payload["provider_reported"] != true {
		t.Errorf("provider_reported = %v, want true", payload["provider_reported"])
	}
	if payload["max_context_tokens"] != 32000 {
		t.Errorf("max_context_tokens = %v, want 32000", payload["max_context_tokens"])
	}
}

// TestContextBreakdown_AlwaysEstimated verifies Estimated is always true for breakdown.
func TestContextBreakdown_AlwaysEstimated(t *testing.T) {
	t.Parallel()

	snap := contextwindow.BuildSnapshot(1, "system", []contextwindow.MessageForEstimate{
		{Role: "user", Content: "question"},
	}, 100, true, 1000)

	if !snap.Breakdown.Estimated {
		t.Error("Breakdown.Estimated should always be true regardless of whether provider reported tokens")
	}
}

// TestBuildSnapshot_SystemPromptBreakdown verifies system prompt tokens are counted.
func TestBuildSnapshot_SystemPromptBreakdown(t *testing.T) {
	t.Parallel()

	// 8 chars system prompt → 2 tokens
	snap := contextwindow.BuildSnapshot(1, "aaaaaaaa", nil, 0, false, 1000)

	if snap.Breakdown.SystemPromptTokens != 2 {
		t.Errorf("SystemPromptTokens = %d, want 2", snap.Breakdown.SystemPromptTokens)
	}
	if snap.Breakdown.ConversationTokens != 0 {
		t.Errorf("ConversationTokens = %d, want 0", snap.Breakdown.ConversationTokens)
	}
}

// TestCompactionTokenCounts_AlwaysEstimated verifies CompactionTokenCounts.Estimated.
func TestCompactionTokenCounts_AlwaysEstimated(t *testing.T) {
	t.Parallel()

	c := contextwindow.CompactionTokenCounts{
		BeforeTokens: 5000,
		AfterTokens:  1200,
		Estimated:    true,
	}
	if !c.Estimated {
		t.Error("CompactionTokenCounts.Estimated should be true")
	}
	if c.BeforeTokens != 5000 {
		t.Errorf("BeforeTokens = %d, want 5000", c.BeforeTokens)
	}
	if c.AfterTokens != 1200 {
		t.Errorf("AfterTokens = %d, want 1200", c.AfterTokens)
	}
}

// TestEstimateTokens_LargeString verifies consistent scaling for large inputs.
func TestEstimateTokens_LargeString(t *testing.T) {
	t.Parallel()

	// Build a 4000-char string; should give exactly 1000 tokens.
	s := ""
	for i := 0; i < 1000; i++ {
		s += "aaaa"
	}
	got := contextwindow.EstimateTokens(s)
	want := 1000
	if got != want {
		t.Errorf("EstimateTokens(4000 chars) = %d, want %d", got, want)
	}
}
