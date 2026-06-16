package training

import "context"

// Confidence represents how confident a finding is.
type Confidence string

const (
	ConfidenceCertain   Confidence = "CERTAIN"
	ConfidenceProbable  Confidence = "PROBABLE"
	ConfidenceTentative Confidence = "TENTATIVE"
)

// Finding represents a discovered issue or improvement opportunity.
type Finding struct {
	Type          string     `json:"type"`           // "system_prompt" | "tool_description" | "behavior" | "anti_pattern"
	Priority      string     `json:"priority"`       // "low" | "medium" | "high" | "critical"
	Target        string     `json:"target"`
	Issue         string     `json:"issue"`
	Proposed      string     `json:"proposed"`
	Rationale     string     `json:"rationale"`
	Confidence    Confidence `json:"confidence"`
	EvidenceCount int        `json:"evidence_count"`
	PatternFreq   int        `json:"pattern_freq"`
}

// ToolCallTrace captures a single tool invocation during a run.
type ToolCallTrace struct {
	Name    string         `json:"name"`
	Args    map[string]any `json:"args"`
	Output  string         `json:"output"`
	Success bool           `json:"success"`
	Retried bool           `json:"retried"`
	StepIdx int            `json:"step_idx"`
}

// ContextSnapshot captures context window usage at a point in time.
type ContextSnapshot struct {
	StepIdx     int     `json:"step_idx"`
	TotalTokens int     `json:"total_tokens"`
	UsedTokens  int     `json:"used_tokens"`
	Ratio       float64 `json:"ratio"`
}

// AntiPatternAlert records a detected anti-pattern during a run.
type AntiPatternAlert struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Evidence string `json:"evidence,omitempty"`
	StepIdx  int    `json:"step_idx"`
}

// TraceBundle is the complete trace of a single run, ready for analysis.
type TraceBundle struct {
	RunID              string             `json:"run_id"`
	TaskID             string             `json:"task_id"`
	Outcome            string             `json:"outcome"` // "pass" | "fail" | "unknown"
	Steps              int                `json:"steps"`
	CostUSD            float64            `json:"cost_usd"`
	EfficiencyScore    float64            `json:"efficiency_score"`
	ToolCalls          []ToolCallTrace    `json:"tool_calls"`
	FirstTryRate       float64            `json:"first_try_rate"`
	AntiPatterns       []AntiPatternAlert `json:"anti_patterns"`
	ContextSnapshots   []ContextSnapshot  `json:"context_snapshots"`
	MaxContextRatio    float64            `json:"max_context_ratio"`
	Messages           []Message          `json:"messages"`
	SystemPrompt       string             `json:"system_prompt"`
	TokenCount         int                `json:"token_count"`
	Truncated          bool               `json:"truncated"`
	TruncatedTokens    int                `json:"truncated_tokens"`
	TruncationStrategy string             `json:"truncation_strategy"`
}

// Message is a simplified representation of a conversation message.
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// TrainerReport is the result of analyzing a single run.
type TrainerReport struct {
	RunID  string `json:"run_id"`
	Scores struct {
		ToolQuality   float64 `json:"tool_quality"`
		Efficiency    float64 `json:"efficiency"`
		GoalAdherence float64 `json:"goal_adherence"`
		ErrorRecovery float64 `json:"error_recovery"`
	} `json:"scores"`
	Findings       []Finding `json:"findings"`
	TrainingLabels struct {
		PreferredSteps []int `json:"preferred_steps"`
		RejectedSteps  []int `json:"rejected_steps"`
	} `json:"training_labels"`
}

// BatchReport is the result of analyzing multiple runs.
type BatchReport struct {
	BatchID  string    `json:"batch_id"`
	RunIDs   []string  `json:"run_ids"`
	Findings []Finding `json:"findings"`
	Patterns []Pattern `json:"patterns"`
}

// Pattern is a recurring failure mode across runs.
type Pattern struct {
	FailureMode string `json:"failure_mode"`
	Frequency   int    `json:"frequency"`
	LastSeen    string `json:"last_seen"`
	Description string `json:"description"`
}

// Trainer analyzes run traces and produces reports.
type Trainer interface {
	Analyze(ctx context.Context, bundle TraceBundle) (*TrainerReport, error)
	AnalyzeBatch(ctx context.Context, bundles []TraceBundle) (*BatchReport, error)
}
