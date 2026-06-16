package training

import (
	"strings"
	"testing"
)

func TestScorer_PerfectRun(t *testing.T) {
	s := &Scorer{}
	bundle := TraceBundle{
		RunID:   "run_perfect",
		Steps:   2,
		CostUSD: 0.01,
		ToolCalls: []ToolCallTrace{
			{Name: "read_file", Success: true, Retried: false},
			{Name: "write_file", Success: true, Retried: false},
		},
		FirstTryRate: 1.0,
		AntiPatterns: nil,
		ContextSnapshots: []ContextSnapshot{
			{StepIdx: 1, Ratio: 0.3},
		},
		MaxContextRatio: 0.3,
	}

	result := s.Score(bundle)
	if result.RunID != "run_perfect" {
		t.Errorf("RunID = %q, want run_perfect", result.RunID)
	}
	if result.ToolQuality < 0.9 {
		t.Errorf("ToolQuality = %f, want >= 0.9 (no retries, no anti-patterns)", result.ToolQuality)
	}
	if result.Efficiency <= 0 || result.Efficiency > 1.0 {
		t.Errorf("Efficiency = %f, want in (0,1]", result.Efficiency)
	}
	if result.FirstTryRate != 1.0 {
		t.Errorf("FirstTryRate = %f, want 1.0", result.FirstTryRate)
	}
	if result.AntiPatternCount != 0 {
		t.Errorf("AntiPatternCount = %d, want 0", result.AntiPatternCount)
	}
	if result.MaxContextRatio != 0.3 {
		t.Errorf("MaxContextRatio = %f, want 0.3", result.MaxContextRatio)
	}
}

func TestScorer_WithAntiPatterns(t *testing.T) {
	s := &Scorer{}
	bundle := TraceBundle{
		RunID:        "run_ap",
		Steps:        5,
		CostUSD:      0.10,
		FirstTryRate: 0.5,
		AntiPatterns: []AntiPatternAlert{
			{Type: "retry_loop", StepIdx: 2},
			{Type: "retry_loop", StepIdx: 3},
			{Type: "retry_loop", StepIdx: 4},
		},
		MaxContextRatio: 0.8,
	}

	result := s.Score(bundle)
	// weightedSum = 3 * 0.5 (retry_loop weight) = 1.5
	// penalty = min(1, 1.5/5) = 0.3
	// ToolQuality = 0.5 * (1 - 0.3) = 0.35
	if result.ToolQuality < 0.34 || result.ToolQuality > 0.36 {
		t.Errorf("ToolQuality = %f, want ~0.35", result.ToolQuality)
	}
	if result.AntiPatternCount != 3 {
		t.Errorf("AntiPatternCount = %d, want 3", result.AntiPatternCount)
	}
}

func TestScorer_MaxAntiPatternPenalty(t *testing.T) {
	s := &Scorer{}
	bundle := TraceBundle{
		RunID:        "run_max",
		Steps:        10,
		CostUSD:      1.0,
		FirstTryRate: 0.8,
		AntiPatterns: make([]AntiPatternAlert, 10), // 10 anti-patterns, penalty capped at 1.0
	}

	result := s.Score(bundle)
	// penalty = min(1, 10/5) = 1.0, so ToolQuality = 0.8 * (1-1) = 0
	if result.ToolQuality != 0.0 {
		t.Errorf("ToolQuality = %f, want 0.0 (max penalty)", result.ToolQuality)
	}
}

func TestScorer_EfficiencyScaling(t *testing.T) {
	s := &Scorer{}

	// Low cost, few steps = high efficiency
	low := s.Score(TraceBundle{RunID: "lo", Steps: 1, CostUSD: 0.001, FirstTryRate: 1.0})
	// High cost, many steps = low efficiency
	high := s.Score(TraceBundle{RunID: "hi", Steps: 20, CostUSD: 5.0, FirstTryRate: 1.0})

	if low.Efficiency <= high.Efficiency {
		t.Errorf("low-cost efficiency (%f) should be > high-cost efficiency (%f)", low.Efficiency, high.Efficiency)
	}
	if low.Efficiency > 1.0 || low.Efficiency < 0 {
		t.Errorf("Efficiency = %f, want in [0,1]", low.Efficiency)
	}
	if high.Efficiency > 1.0 || high.Efficiency < 0 {
		t.Errorf("Efficiency = %f, want in [0,1]", high.Efficiency)
	}
}

func TestScorer_ZeroToolCalls(t *testing.T) {
	s := &Scorer{}
	result := s.Score(TraceBundle{RunID: "empty", Steps: 1, CostUSD: 0.01})
	// No tool calls => FirstTryRate stays as-is from bundle (0.0)
	if result.FirstTryRate != 0.0 {
		t.Errorf("FirstTryRate = %f, want 0.0", result.FirstTryRate)
	}
}

func TestScorer_Summary(t *testing.T) {
	s := &Scorer{}
	result := s.Score(TraceBundle{
		RunID:        "run_sum",
		Steps:        3,
		CostUSD:      0.05,
		FirstTryRate: 0.75,
	})
	if result.Summary == "" {
		t.Error("Summary should not be empty")
	}
	if !strings.Contains(result.Summary, "run_sum") {
		t.Errorf("Summary should contain run ID, got: %s", result.Summary)
	}
}

func TestScorer_DifferentiatedPenaltyWeights(t *testing.T) {
	s := &Scorer{}

	// Same count of patterns but different types = different penalties.
	t.Run("retry_loop is lighter than named patterns", func(t *testing.T) {
		retryOnly := TraceBundle{
			RunID:        "retry",
			Steps:        4,
			CostUSD:      0.05,
			FirstTryRate: 0.8,
			AntiPatterns: []AntiPatternAlert{
				{Type: "retry_loop", StepIdx: 1},
				{Type: "retry_loop", StepIdx: 2},
			},
		}
		namedOnly := TraceBundle{
			RunID:        "named",
			Steps:        4,
			CostUSD:      0.05,
			FirstTryRate: 0.8,
			AntiPatterns: []AntiPatternAlert{
				{Type: "hedge_assertion", StepIdx: 1},
				{Type: "unverified_file_claim", StepIdx: 2},
			},
		}
		retryResult := s.Score(retryOnly)
		namedResult := s.Score(namedOnly)

		// retry_loop weight 0.5 each = 1.0 total; penalty = 1.0/5 = 0.2
		// named weight 1.0 each = 2.0 total; penalty = 2.0/5 = 0.4
		// So named should have lower ToolQuality than retry-only
		if namedResult.ToolQuality >= retryResult.ToolQuality {
			t.Errorf("named patterns should have lower ToolQuality than retry_loop: named=%f retry=%f",
				namedResult.ToolQuality, retryResult.ToolQuality)
		}
	})

	t.Run("premature_completion has highest weight", func(t *testing.T) {
		premature := TraceBundle{
			RunID:        "premature",
			Steps:        4,
			CostUSD:      0.05,
			FirstTryRate: 0.8,
			AntiPatterns: []AntiPatternAlert{
				{Type: "premature_completion", StepIdx: 1},
			},
		}
		hedge := TraceBundle{
			RunID:        "hedge",
			Steps:        4,
			CostUSD:      0.05,
			FirstTryRate: 0.8,
			AntiPatterns: []AntiPatternAlert{
				{Type: "hedge_assertion", StepIdx: 1},
			},
		}
		prematureResult := s.Score(premature)
		hedgeResult := s.Score(hedge)

		// premature_completion weight 1.25 > hedge_assertion weight 1.0
		// So premature should have lower ToolQuality
		if prematureResult.ToolQuality >= hedgeResult.ToolQuality {
			t.Errorf("premature_completion should have lower ToolQuality than hedge_assertion: premature=%f hedge=%f",
				prematureResult.ToolQuality, hedgeResult.ToolQuality)
		}
	})

	t.Run("unknown type gets default weight", func(t *testing.T) {
		unknown := TraceBundle{
			RunID:        "unknown",
			Steps:        4,
			CostUSD:      0.05,
			FirstTryRate: 0.8,
			AntiPatterns: []AntiPatternAlert{
				{Type: "some_future_pattern", StepIdx: 1},
			},
		}
		result := s.Score(unknown)
		// default weight 1.0; penalty = 1.0/5 = 0.2; ToolQuality = 0.8 * 0.8 = 0.64
		if result.ToolQuality < 0.63 || result.ToolQuality > 0.65 {
			t.Errorf("unknown type ToolQuality = %f, want ~0.64", result.ToolQuality)
		}
	})
}

func TestScorer_ContextRatioFromSnapshots(t *testing.T) {
	s := &Scorer{}
	bundle := TraceBundle{
		RunID:   "run_ctx",
		Steps:   2,
		CostUSD: 0.01,
		ContextSnapshots: []ContextSnapshot{
			{StepIdx: 1, Ratio: 0.4},
			{StepIdx: 2, Ratio: 0.9},
		},
		MaxContextRatio: 0.9,
		FirstTryRate:    1.0,
	}
	result := s.Score(bundle)
	if result.MaxContextRatio != 0.9 {
		t.Errorf("MaxContextRatio = %f, want 0.9", result.MaxContextRatio)
	}
}
