package training

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultBaseURL       = "https://api.anthropic.com"
	defaultModel         = "claude-opus-4-6"
	anthropicAPIVersion  = "2023-06-01"
	maxTokensDefault     = 4096
)

// ClaudeTrainer uses the Anthropic API to analyze run traces.
type ClaudeTrainer struct {
	apiKey   string
	baseURL  string
	model    string
	client   *http.Client
}

// ClaudeTrainerOption configures a ClaudeTrainer.
type ClaudeTrainerOption func(*ClaudeTrainer)

// WithBaseURL overrides the API base URL (useful for testing).
func WithBaseURL(url string) ClaudeTrainerOption {
	return func(ct *ClaudeTrainer) {
		ct.baseURL = url
	}
}

// WithModel overrides the model used for analysis.
func WithModel(model string) ClaudeTrainerOption {
	return func(ct *ClaudeTrainer) {
		ct.model = model
	}
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(c *http.Client) ClaudeTrainerOption {
	return func(ct *ClaudeTrainer) {
		ct.client = c
	}
}

// NewClaudeTrainer creates a new ClaudeTrainer with the given API key.
func NewClaudeTrainer(apiKey string, opts ...ClaudeTrainerOption) *ClaudeTrainer {
	ct := &ClaudeTrainer{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		model:   defaultModel,
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(ct)
	}
	return ct
}

// Analyze sends a single trace bundle to Claude for analysis.
func (ct *ClaudeTrainer) Analyze(ctx context.Context, bundle TraceBundle) (*TrainerReport, error) {
	prompt := buildAnalyzePrompt(bundle)
	text, err := ct.callAPI(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("claude analyze: %w", err)
	}

	var report TrainerReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		return nil, fmt.Errorf("parse trainer report: %w (response: %.200s)", err, text)
	}
	return &report, nil
}

// AnalyzeBatch sends multiple trace bundles to Claude for batch analysis.
func (ct *ClaudeTrainer) AnalyzeBatch(ctx context.Context, bundles []TraceBundle) (*BatchReport, error) {
	prompt := buildBatchPrompt(bundles)
	text, err := ct.callAPI(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("claude analyze batch: %w", err)
	}

	var report BatchReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		return nil, fmt.Errorf("parse batch report: %w (response: %.200s)", err, text)
	}
	return &report, nil
}

// apiRequest is the Anthropic Messages API request body.
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// apiResponse is a minimal representation of the Anthropic Messages API response.
type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// callAPI sends a prompt to the Anthropic API and returns the text response.
func (ct *ClaudeTrainer) callAPI(ctx context.Context, userPrompt string) (string, error) {
	reqBody := apiRequest{
		Model:     ct.model,
		MaxTokens: maxTokensDefault,
		Messages: []apiMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := ct.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", ct.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := ct.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("parse API response: %w", err)
	}

	for _, block := range apiResp.Content {
		if block.Type == "text" {
			// Strip markdown code fences if present
			text := strings.TrimSpace(block.Text)
			text = strings.TrimPrefix(text, "```json")
			text = strings.TrimPrefix(text, "```")
			text = strings.TrimSuffix(text, "```")
			return strings.TrimSpace(text), nil
		}
	}

	return "", fmt.Errorf("no text content in API response")
}

// buildAnalyzePrompt constructs the analysis prompt for a single run.
func buildAnalyzePrompt(bundle TraceBundle) string {
	var b strings.Builder
	b.WriteString("You are reviewing an LLM agent coding task execution. Analyze the trace and provide structured feedback.\n\n")

	b.WriteString("## Task Details\n")
	fmt.Fprintf(&b, "- Run ID: %s\n", bundle.RunID)
	fmt.Fprintf(&b, "- Task ID: %s\n", bundle.TaskID)
	fmt.Fprintf(&b, "- Outcome: %s\n", bundle.Outcome)
	fmt.Fprintf(&b, "- Steps: %d\n", bundle.Steps)
	fmt.Fprintf(&b, "- Cost: $%.4f\n", bundle.CostUSD)
	fmt.Fprintf(&b, "- First-try rate: %.2f\n", bundle.FirstTryRate)
	fmt.Fprintf(&b, "- Max context ratio: %.2f\n", bundle.MaxContextRatio)
	fmt.Fprintf(&b, "- Token count: %d\n", bundle.TokenCount)

	if bundle.SystemPrompt != "" {
		b.WriteString("\n## System Prompt\n")
		b.WriteString(bundle.SystemPrompt)
		b.WriteString("\n")
	}

	if len(bundle.AntiPatterns) > 0 {
		b.WriteString("\n## Anti-Patterns Detected\n")
		b.WriteString("Named anti-pattern types include: retry_loop (mechanical), hedge_assertion, unverified_file_claim, premature_completion, skipped_diagnostic, architecture_assumption (behavioral).\n")
		for _, ap := range bundle.AntiPatterns {
			ev := ""
			if ap.Evidence != "" {
				ev = fmt.Sprintf(" [evidence: %s]", ap.Evidence)
			}
			fmt.Fprintf(&b, "- [step %d] %s: %s%s\n", ap.StepIdx, ap.Type, ap.Message, ev)
		}
	}

	if len(bundle.ToolCalls) > 0 {
		b.WriteString("\n## Tool Call Trace (summary)\n")
		for i, tc := range bundle.ToolCalls {
			status := "ok"
			if !tc.Success {
				status = "FAIL"
			}
			retry := ""
			if tc.Retried {
				retry = " [RETRY]"
			}
			fmt.Fprintf(&b, "%d. [step %d] %s -> %s%s\n", i+1, tc.StepIdx, tc.Name, status, retry)
		}
	}

	b.WriteString("\n## Required Output Format\n")
	b.WriteString("Respond with ONLY a JSON object matching this schema:\n")
	b.WriteString(`{
  "run_id": "string",
  "scores": {
    "tool_quality": 0.0-1.0,
    "efficiency": 0.0-1.0,
    "goal_adherence": 0.0-1.0,
    "error_recovery": 0.0-1.0
  },
  "findings": [
    {
      "type": "system_prompt|tool_description|behavior|anti_pattern",
      "priority": "low|medium|high|critical",
      "target": "what to change",
      "issue": "what's wrong",
      "proposed": "specific fix",
      "rationale": "why",
      "confidence": "CERTAIN|PROBABLE|TENTATIVE",
      "evidence_count": 1,
      "pattern_freq": 1
    }
  ],
  "training_labels": {
    "preferred_steps": [1, 3],
    "rejected_steps": [2]
  }
}`)
	b.WriteString("\n")

	return b.String()
}

// buildBatchPrompt constructs the analysis prompt for multiple runs.
func buildBatchPrompt(bundles []TraceBundle) string {
	var b strings.Builder
	b.WriteString("You are reviewing multiple LLM agent task executions. Identify cross-run patterns and provide batch analysis.\n\n")

	for i, bundle := range bundles {
		fmt.Fprintf(&b, "## Run %d: %s\n", i+1, bundle.RunID)
		fmt.Fprintf(&b, "- Outcome: %s, Steps: %d, Cost: $%.4f\n", bundle.Outcome, bundle.Steps, bundle.CostUSD)
		fmt.Fprintf(&b, "- First-try rate: %.2f, Anti-patterns: %d\n", bundle.FirstTryRate, len(bundle.AntiPatterns))
		b.WriteString("\n")
	}

	b.WriteString("## Required Output Format\n")
	b.WriteString("Respond with ONLY a JSON object matching this schema:\n")
	b.WriteString(`{
  "batch_id": "string",
  "run_ids": ["run_1", "run_2"],
  "findings": [
    {
      "type": "system_prompt|tool_description|behavior|anti_pattern",
      "priority": "low|medium|high|critical",
      "target": "what to change",
      "issue": "what's wrong",
      "proposed": "specific fix",
      "rationale": "why",
      "confidence": "CERTAIN|PROBABLE|TENTATIVE",
      "evidence_count": 1,
      "pattern_freq": 1
    }
  ],
  "patterns": [
    {
      "failure_mode": "descriptive name",
      "frequency": 3,
      "last_seen": "run_id",
      "description": "explanation"
    }
  ]
}`)
	b.WriteString("\n")

	return b.String()
}
