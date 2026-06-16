package training

import "fmt"

// Scorer performs structural scoring of trace bundles without requiring an LLM.
type Scorer struct{}

// ScoreResult contains the computed scores for a single run.
type ScoreResult struct {
	RunID            string  `json:"run_id"`
	ToolQuality      float64 `json:"tool_quality"`
	Efficiency       float64 `json:"efficiency"`
	FirstTryRate     float64 `json:"first_try_rate"`
	AntiPatternCount int     `json:"anti_pattern_count"`
	MaxContextRatio  float64 `json:"max_context_ratio"`
	Summary          string  `json:"summary"`
}

// antiPatternWeight returns the penalty weight for a given anti-pattern type.
// Named behavioral patterns (conclusion-watching) carry higher weight than
// simple retry loops since they indicate deeper process failures.
func antiPatternWeight(apType string) float64 {
	switch apType {
	case "retry_loop":
		return 0.5 // mechanical issue, often transient
	case "hedge_assertion", "unverified_file_claim":
		return 1.0 // process failure, undermines verification quality
	case "premature_completion":
		return 1.25 // premature task termination is a high-impact failure
	case "skipped_diagnostic", "architecture_assumption":
		return 1.0 // skipping verification steps or assuming architecture
	default:
		return 1.0 // unknown types get standard weight
	}
}

// Score computes structural metrics from a TraceBundle.
//
// Scoring logic:
//   - ToolQuality = FirstTryRate * (1 - antiPatternPenalty) where penalty = min(1, weightedAntiPatternSum/5)
//   - Efficiency = 1.0 / (1.0 + steps*0.1 + costUSD*10) capped at [0,1]
//   - MaxContextRatio from context snapshots
func (s *Scorer) Score(bundle TraceBundle) ScoreResult {
	var weightedSum float64
	for _, ap := range bundle.AntiPatterns {
		weightedSum += antiPatternWeight(ap.Type)
	}
	penalty := weightedSum / 5.0
	if penalty > 1.0 {
		penalty = 1.0
	}
	apCount := len(bundle.AntiPatterns)

	toolQuality := bundle.FirstTryRate * (1.0 - penalty)

	steps := float64(bundle.Steps)
	if steps <= 0 {
		steps = 1
	}
	efficiency := 1.0 / (1.0 + steps*0.1 + bundle.CostUSD*10.0)
	if efficiency > 1.0 {
		efficiency = 1.0
	}
	if efficiency < 0 {
		efficiency = 0
	}

	return ScoreResult{
		RunID:            bundle.RunID,
		ToolQuality:      toolQuality,
		Efficiency:       efficiency,
		FirstTryRate:     bundle.FirstTryRate,
		AntiPatternCount: apCount,
		MaxContextRatio:  bundle.MaxContextRatio,
		Summary: fmt.Sprintf("run=%s quality=%.2f efficiency=%.2f first_try=%.2f anti_patterns=%d ctx_ratio=%.2f",
			bundle.RunID, toolQuality, efficiency, bundle.FirstTryRate, apCount, bundle.MaxContextRatio),
	}
}
