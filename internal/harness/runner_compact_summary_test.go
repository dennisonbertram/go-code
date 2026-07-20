package harness

import (
	"context"
	"strings"
	"testing"
)

// Tests in this file cover epic #817 slice 2: CompactRunResult carries the
// compaction mode and (for summarize/hybrid) the produced summary.
// Helpers (compactInstructionProvider, startThreeStepGatedRun,
// newGatedInstructionProvider, threeStepToolCallResults) live in
// runner_compact_instruction_test.go.

// TestCompactRun_SummarizeModeReturnsSummary verifies that summarize mode
// returns the summary text produced by the summarizer along with the mode.
func TestCompactRun_SummarizeModeReturnsSummary(t *testing.T) {
	t.Parallel()

	gateRelease := make(chan struct{})
	prov := newGatedInstructionProvider(gateRelease)
	runner, runID := startThreeStepGatedRun(t, prov, `{"echo":"ok"}`, gateRelease)

	result, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
		Mode:     "summarize",
		KeepLast: 1,
	})
	if err != nil {
		t.Fatalf("CompactRun summarize: %v", err)
	}
	close(gateRelease)

	if result.Mode != "summarize" {
		t.Errorf("expected Mode=%q, got %q", "summarize", result.Mode)
	}
	// threeStepToolCallResults scripts the summarization response.
	if result.Summary != "a compact summary" {
		t.Errorf("expected Summary=%q, got %q", "a compact summary", result.Summary)
	}
	if result.MessagesRemoved <= 0 {
		t.Errorf("expected MessagesRemoved > 0, got %d", result.MessagesRemoved)
	}
}

// TestCompactRun_HybridModeReturnsSummary verifies that hybrid mode surfaces
// the summary of the removed large tool outputs.
func TestCompactRun_HybridModeReturnsSummary(t *testing.T) {
	t.Parallel()

	gateRelease := make(chan struct{})
	prov := newGatedInstructionProvider(gateRelease)

	// Tool output above the 500-token hybrid threshold (~2000 runes).
	largeOutput := strings.Repeat("x", 2400)
	runner, runID := startThreeStepGatedRun(t, prov, largeOutput, gateRelease)

	result, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
		Mode:     "hybrid",
		KeepLast: 1,
	})
	if err != nil {
		t.Fatalf("CompactRun hybrid: %v", err)
	}
	close(gateRelease)

	if result.Mode != "hybrid" {
		t.Errorf("expected Mode=%q, got %q", "hybrid", result.Mode)
	}
	if result.Summary != "a compact summary" {
		t.Errorf("expected Summary=%q, got %q", "a compact summary", result.Summary)
	}
}

// TestCompactRun_StripModeReturnsEmptySummary verifies strip mode returns an
// empty summary (nothing is summarized) while MessagesRemoved is populated.
// An omitted mode resolves to "strip" and must report the resolved mode.
func TestCompactRun_StripModeReturnsEmptySummary(t *testing.T) {
	t.Parallel()

	for _, reqMode := range []string{"strip", ""} {
		reqMode := reqMode
		t.Run("mode="+reqMode, func(t *testing.T) {
			t.Parallel()

			gateRelease := make(chan struct{})
			prov := newGatedInstructionProvider(gateRelease)
			runner, runID := startThreeStepGatedRun(t, prov, `{"echo":"ok"}`, gateRelease)

			result, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
				Mode:     reqMode,
				KeepLast: 1,
			})
			if err != nil {
				t.Fatalf("CompactRun strip: %v", err)
			}
			close(gateRelease)

			if result.Mode != "strip" {
				t.Errorf("expected resolved Mode=%q, got %q", "strip", result.Mode)
			}
			if result.Summary != "" {
				t.Errorf("strip mode must return an empty summary, got %q", result.Summary)
			}
			if result.MessagesRemoved <= 0 {
				t.Errorf("expected MessagesRemoved > 0, got %d", result.MessagesRemoved)
			}
		})
	}
}
