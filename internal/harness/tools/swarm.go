package tools

import "context"

// AgentSwarmToolName is the name of the swarm fan-out tool. It lives here so
// the runner's sole-call rule, the deferred tool, and the subagents package
// (member tool-set exclusion) all share one constant without importing each
// other.
const AgentSwarmToolName = "agent_swarm"

// SwarmRequest mirrors subagents.SwarmRequest without importing the subagents
// package (which would create an import cycle: harness -> deferred ->
// subagents -> harness). Field semantics are defined by subagents.SwarmRequest.
type SwarmRequest struct {
	PromptTemplate string   `json:"prompt_template"`
	Items          []string `json:"items"`
	ResumeAgentIDs []string `json:"resume_agent_ids,omitempty"`

	Model                string                `json:"model,omitempty"`
	SystemPrompt         string                `json:"system_prompt,omitempty"`
	MaxSteps             int                   `json:"max_steps,omitempty"`
	MaxCostUSD           float64               `json:"max_cost_usd,omitempty"`
	ReasoningEffort      string                `json:"reasoning_effort,omitempty"`
	AllowedTools         []string              `json:"allowed_tools,omitempty"`
	ProfileName          string                `json:"profile,omitempty"`
	IsolationMode        string                `json:"isolation,omitempty"`
	CleanupPolicy        string                `json:"cleanup_policy,omitempty"`
	BaseRef              string                `json:"base_ref,omitempty"`
	ResultMode           string                `json:"result_mode,omitempty"`
	ParentContextHandoff *ParentContextHandoff `json:"parent_context_handoff,omitempty"`
}

// SwarmMemberReport mirrors subagents.SwarmMemberReport.
type SwarmMemberReport struct {
	ID      string `json:"id,omitempty"`
	Item    string `json:"item"`
	Prompt  string `json:"prompt"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Resumed bool   `json:"resumed,omitempty"`
}

// SwarmReport mirrors subagents.SwarmReport: one aggregated result for the
// whole cohort, in deterministic order (new item members first, then resumed).
type SwarmReport struct {
	Members   []SwarmMemberReport `json:"members"`
	Total     int                 `json:"total"`
	Completed int                 `json:"completed"`
	Failed    int                 `json:"failed"`
	Cancelled int                 `json:"cancelled"`
}

// SwarmRunner fans a prompt template out over items into concurrent
// subagents and returns the aggregated report. It is implemented by an
// adapter in the subagents package; the interface lives here to avoid the
// import cycle SubagentManager avoids.
type SwarmRunner interface {
	RunSwarm(ctx context.Context, req SwarmRequest) (SwarmReport, error)
}
