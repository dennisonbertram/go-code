package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go-agent-harness/internal/forensics/redaction"
	htools "go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/store/s3backup"
	"go-agent-harness/internal/systemprompt"
	"go-agent-harness/internal/workingmemory"
)

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// ParallelSafe indicates that this tool can safely execute concurrently
	// with other parallel-safe tool calls within a single runner step.
	// Tools that mutate shared state, interact with the user, or modify the
	// transcript (e.g. compact_history, reset_context, ask_user_question) must
	// leave this false (the zero value).
	ParallelSafe bool `json:"-"`
	// Mutating indicates that this tool modifies external state (writes files,
	// executes commands, etc.). When ApprovalPolicyDestructive is set, only
	// mutating tools require operator approval before execution.
	Mutating bool `json:"-"`
}

// Clone returns a deep copy of the tool definition, including the schema map.
func (d ToolDefinition) Clone() ToolDefinition {
	d.Parameters = deepClonePayload(d.Parameters)
	return d
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Clone returns a copy of tc. ToolCall fields are all value types (strings),
// so this is equivalent to a struct copy — no heap aliasing is possible.
// Clone exists to make the immutability contract explicit and to future-proof
// against reference-typed fields being added later.
func (tc ToolCall) Clone() ToolCall {
	return tc // strings are immutable value types in Go
}

type Message struct {
	MessageID        string     `json:"message_id,omitempty"`
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	IsMeta           bool       `json:"is_meta,omitempty"`
	IsCompactSummary bool       `json:"is_compact_summary,omitempty"`
	// CorrelationID links messages across turns within a conversation.
	CorrelationID string `json:"correlation_id,omitempty"`
	// ConversationID is stable across ContinueRun restarts.
	ConversationID string `json:"conversation_id,omitempty"`
	// Reasoning holds the aggregated thinking/reasoning text produced by the
	// LLM for this message. It is only populated when CaptureReasoning is
	// enabled in RunnerConfig. Stored with omitempty so it is omitted from
	// JSON when empty, keeping wire format backward-compatible.
	Reasoning string `json:"reasoning,omitempty"`
}

// Clone returns a deep copy of m with an independent ToolCalls slice.
func (m Message) Clone() Message {
	if m.ToolCalls != nil {
		tc := make([]ToolCall, len(m.ToolCalls))
		for i, t := range m.ToolCalls {
			tc[i] = t.Clone()
		}
		m.ToolCalls = tc
	}
	return m
}

type CompletionRequest struct {
	Model    string                `json:"model"`
	Messages []Message             `json:"messages"`
	Tools    []ToolDefinition      `json:"tools,omitempty"`
	Stream   func(CompletionDelta) `json:"-"`
	// ReasoningEffort controls the thinking budget for reasoning models.
	// For OpenAI o-series, valid values are "low", "medium", "high".
	// Empty means the provider default (field omitted from the API request).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type CompletionResult struct {
	Content     string            `json:"content"`
	ToolCalls   []ToolCall        `json:"tool_calls,omitempty"`
	Deltas      []CompletionDelta `json:"-"`
	Usage       *CompletionUsage  `json:"usage,omitempty"`
	CostUSD     *float64          `json:"cost_usd,omitempty"`
	Cost        *CompletionCost   `json:"cost,omitempty"`
	UsageStatus UsageStatus       `json:"usage_status,omitempty"`
	CostStatus  CostStatus        `json:"cost_status,omitempty"`
	// Latency fields set by the provider. All values are integer milliseconds.
	// TTFTMs is the time from request start to first token received (streaming only;
	// zero for non-streaming calls).
	TTFTMs int64 `json:"ttft_ms,omitempty"`
	// TotalDurationMs is the wall-clock time from request start to full response.
	TotalDurationMs int64 `json:"total_duration_ms,omitempty"`
	// ReasoningText holds the aggregated thinking/reasoning text produced by
	// the LLM during this completion. It is populated by providers that support
	// reasoning blocks (e.g. OpenAI o-series via reasoning_content stream deltas).
	// Empty when the model does not emit reasoning tokens.
	ReasoningText string `json:"reasoning_text,omitempty"`
	// ReasoningTokens is the count of tokens in ReasoningText. Populated when
	// the provider reports reasoning token counts separately. Zero when
	// reasoning is absent or the count is unavailable.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// ModelVersion is the specific model version string returned by the provider,
	// if available (e.g. "gpt-4.1-2025-04-14"). Empty when the provider does not
	// report this field. Used by the forensics request envelope feature.
	ModelVersion string `json:"model_version,omitempty"`
	// FinishReason is a normalized signal for why the completion stopped,
	// unifying OpenAI's finish_reason and Anthropic's stop_reason into a
	// single vocabulary — see FinishReason's doc comment for the exact
	// per-provider/per-API coverage, which is NOT uniform: it is empty for
	// every completion routed through OpenAI's Responses API (/v1/responses),
	// even a truncated one, not just when the provider genuinely reported
	// nothing.
	//
	// This field is purely additive: it does not change Complete()'s error
	// contract. A response truncated by the model's max-tokens limit
	// (FinishReasonLength) is still returned as a successful CompletionResult
	// with usable partial content, not an error — callers that care about
	// truncation should inspect this field and decide policy themselves.
	FinishReason FinishReason `json:"finish_reason,omitempty"`
}

// FinishReason is a normalized representation of why a completion stopped,
// unifying provider-specific vocabularies into a single set of values so
// callers don't need to branch on provider. The empty string means the
// provider did not report a finish reason (distinct from FinishReasonOther,
// which means the provider reported one but it did not map onto a
// recognized value).
//
// Coverage (deliberately incomplete — do not assume this is populated for
// every provider/API path):
//   - anthropic: populated for both streaming and non-streaming completions,
//     from response.stop_reason (message_delta's stop_reason when streaming).
//   - openai Chat Completions (/v1/chat/completions): populated for both
//     streaming and non-streaming completions, from choices[].finish_reason.
//   - openai Responses API (/v1/responses, used by o3/gpt-5-family models):
//     NOT populated for either mode. The Responses API reports completion/
//     truncation status via a different schema (response.status and
//     response.incomplete_details.reason) that is not modeled or mapped
//     onto FinishReason yet. A Responses-API completion always has
//     FinishReason == "" ("provider did not report"), even when the
//     response was in fact truncated by max_output_tokens.
type FinishReason string

const (
	// FinishReasonStop indicates the model reached a natural stopping point.
	// Maps from OpenAI's "stop" and Anthropic's "end_turn" / "stop_sequence".
	FinishReasonStop FinishReason = "stop"
	// FinishReasonLength indicates the completion was truncated because it
	// hit the configured max-tokens limit. Maps from OpenAI's "length" and
	// Anthropic's "max_tokens". Content up to this point is usable but
	// incomplete.
	FinishReasonLength FinishReason = "length"
	// FinishReasonToolCalls indicates the model stopped in order to invoke
	// one or more tools. Maps from OpenAI's "tool_calls" (and the deprecated
	// "function_call") and Anthropic's "tool_use".
	FinishReasonToolCalls FinishReason = "tool_calls"
	// FinishReasonContentFilter indicates the provider's content filter
	// suppressed or blocked the completion. Maps from OpenAI's
	// "content_filter" and Anthropic's "refusal".
	FinishReasonContentFilter FinishReason = "content_filter"
	// FinishReasonOther is used when the provider reported a finish reason
	// that does not map onto any of the above (e.g. Anthropic's
	// "pause_turn"). This constant exists so "provider reported something
	// unrecognized" is distinguishable from "provider reported nothing"
	// (empty string).
	FinishReasonOther FinishReason = "other"
)

type CompletionDelta struct {
	Content   string        `json:"content,omitempty"`
	Reasoning string        `json:"reasoning,omitempty"`
	ToolCall  ToolCallDelta `json:"tool_call,omitempty"`
}

type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type CompletionUsage struct {
	PromptTokens       int  `json:"prompt_tokens"`
	CompletionTokens   int  `json:"completion_tokens"`
	TotalTokens        int  `json:"total_tokens"`
	CachedPromptTokens *int `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    *int `json:"reasoning_tokens,omitempty"`
	InputAudioTokens   *int `json:"input_audio_tokens,omitempty"`
	OutputAudioTokens  *int `json:"output_audio_tokens,omitempty"`
}

type CompletionCost struct {
	InputUSD       float64 `json:"input_usd"`
	OutputUSD      float64 `json:"output_usd"`
	CacheReadUSD   float64 `json:"cache_read_usd"`
	CacheWriteUSD  float64 `json:"cache_write_usd"`
	TotalUSD       float64 `json:"total_usd"`
	PricingVersion string  `json:"pricing_version,omitempty"`
	Estimated      bool    `json:"estimated"`
}

type UsageStatus string

const (
	UsageStatusProviderReported   UsageStatus = "provider_reported"
	UsageStatusProviderUnreported UsageStatus = "provider_unreported"
)

type CostStatus string

const (
	CostStatusAvailable          CostStatus = "available"
	CostStatusUnpricedModel      CostStatus = "unpriced_model"
	CostStatusProviderUnreported CostStatus = "provider_unreported"
	CostStatusPending            CostStatus = "pending"
)

type RunUsageTotals struct {
	PromptTokensTotal     int `json:"prompt_tokens_total"`
	CompletionTokensTotal int `json:"completion_tokens_total"`
	TotalTokens           int `json:"total_tokens"`
	LastTurnTokens        int `json:"last_turn_tokens"`
}

type RunCostTotals struct {
	CostUSDTotal    float64    `json:"cost_usd_total"`
	LastTurnCostUSD float64    `json:"last_turn_cost_usd"`
	CostStatus      CostStatus `json:"cost_status"`
	PricingVersion  string     `json:"pricing_version,omitempty"`
}

// RunSummary contains post-run telemetry for benchmarking and analysis.
type RunSummary struct {
	RunID                 string            `json:"run_id"`
	Status                RunStatus         `json:"status"`
	StepsTaken            int               `json:"steps_taken"`
	TotalPromptTokens     int               `json:"total_prompt_tokens"`
	TotalCompletionTokens int               `json:"total_completion_tokens"`
	TotalCostUSD          float64           `json:"total_cost_usd"`
	CostStatus            CostStatus        `json:"cost_status"`
	ToolCalls             []ToolCallSummary `json:"tool_calls"`
	CacheHitRate          float64           `json:"cache_hit_rate"`
	Error                 string            `json:"error,omitempty"`
}

// ToolCallSummary records a single tool invocation within a run.
type ToolCallSummary struct {
	ToolName string `json:"tool_name"`
	Step     int    `json:"step"`
}

// ProviderHTTPError is returned by provider clients when the upstream API
// responds with a non-2xx HTTP status code. Its Error() string is
// byte-identical to the fmt.Errorf messages the OpenAI and Anthropic clients
// previously produced, so any existing string-based assertions continue to
// pass. The structured fields allow the fallback machinery to inspect the
// status code without string parsing.
//
// Example errors:
//
//	"openai request failed (429): <body>"
//	"anthropic request failed (503): <body>"
type ProviderHTTPError struct {
	// Provider is the lowercase provider name (e.g. "openai", "anthropic").
	Provider string
	// StatusCode is the HTTP status code returned by the upstream API.
	StatusCode int
	// Body is the trimmed response body text returned by the upstream API.
	Body string
}

// Error returns the provider error string in the canonical format:
// "<provider> request failed (<status>): <body>"
// This matches the exact text previously produced by fmt.Errorf in each
// provider client, ensuring backward compatibility with string assertions.
func (e *ProviderHTTPError) Error() string {
	return fmt.Sprintf("%s request failed (%d): %s", e.Provider, e.StatusCode, e.Body)
}

// isFallbackEligible reports whether err is a *ProviderHTTPError with a status
// code that warrants trying a fallback provider.  Eligible codes are transient
// server-side failures: 429, 500, 502, 503, 504.  Client-side errors (400,
// 401, 403, 404, 422) are NOT eligible because retrying with a different
// provider will not fix a malformed or unauthorised request.
func isFallbackEligible(err error) bool {
	var phe *ProviderHTTPError
	if !errors.As(err, &phe) {
		return false
	}
	switch phe.StatusCode {
	case 429, 500, 502, 503, 504:
		return true
	}
	return false
}

type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

type Event struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type RunStatus string

const (
	RunStatusQueued             RunStatus = "queued"
	RunStatusRunning            RunStatus = "running"
	RunStatusWaitingForUser     RunStatus = "waiting_for_user"
	RunStatusWaitingForApproval RunStatus = "waiting_for_approval"
	RunStatusCompleted          RunStatus = "completed"
	RunStatusFailed             RunStatus = "failed"
	RunStatusCancelled          RunStatus = "cancelled"
)

type Run struct {
	ID                   string                       `json:"id"`
	Prompt               string                       `json:"prompt"`
	Model                string                       `json:"model"`
	ProviderName         string                       `json:"provider_name,omitempty"`
	Status               RunStatus                    `json:"status"`
	Output               string                       `json:"output,omitempty"`
	Error                string                       `json:"error,omitempty"`
	ParentContextHandoff *htools.ParentContextHandoff `json:"parent_context_handoff,omitempty"`
	UsageTotals          *RunUsageTotals              `json:"usage_totals,omitempty"`
	CostTotals           *RunCostTotals               `json:"cost_totals,omitempty"`
	Recap                *store.WorkflowRecap         `json:"recap,omitempty"`
	TenantID             string                       `json:"tenant_id,omitempty"`
	ConversationID       string                       `json:"conversation_id,omitempty"`
	AgentID              string                       `json:"agent_id,omitempty"`
	CreatedAt            time.Time                    `json:"created_at"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

// WorkspaceProvisionOptions holds parameters for per-run workspace provisioning.
// It is embedded in RunnerConfig to provide defaults for all runs.
// Fields map directly to workspace.Options: see that type for semantics.
type WorkspaceProvisionOptions struct {
	// RepoPath is the git repository path used by worktree-backed workspaces.
	RepoPath string
	// WorktreeRootDir is the parent directory under which worktree paths are created.
	// Used only for workspace_type="worktree". Defaults to a sibling directory of RepoPath.
	WorktreeRootDir string
	// BaseDir is the base directory for local workspace subdirectories.
	BaseDir string
	// ConfigTOML is an optional serialized TOML configuration string written to
	// harness.toml in the workspace root after provisioning. When non-empty it is
	// forwarded to workspace.Options.ConfigTOML. Never include secrets here.
	ConfigTOML string
}

type RunRequest struct {
	Prompt string `json:"prompt"`
	// PlanMode starts the run in the read-only planning state. While active,
	// mutation is limited to PlanFile until the operator approves the plan.
	PlanMode bool `json:"plan_mode,omitempty"`
	// PlanFile is the workspace-relative plan artifact allowed during PlanMode.
	// Empty uses the stable default `.harness/plan.md`.
	PlanFile string `json:"plan_file,omitempty"`
	Model    string `json:"model,omitempty"`
	// WorkspaceType selects the workspace backend for this run.
	// When set, the runner provisions an isolated workspace and cleans it up
	// on run completion (success, failure, or cancellation).
	//
	// Supported values:
	//   ""          — use the server default (local process, no provisioning).
	//   "local"     — provision a local directory workspace (same host, no isolation).
	//   "worktree"  — provision a git worktree (requires WorkspaceBaseOptions.RepoPath).
	//   "container" — provision a container workspace (requires orchestrator config).
	//   "vm"        — provision a VM workspace (requires orchestrator config).
	//
	// Unknown values are rejected at StartRun time with a validation error.
	// When empty, the runner falls back to Profile.IsolationMode if a profile is
	// named in ProfileName. Explicit WorkspaceType always takes precedence over
	// the profile's IsolationMode setting.
	WorkspaceType string `json:"workspace_type,omitempty"`
	// ProviderName explicitly selects which catalog provider to use for this run.
	// When set, overrides the automatic provider resolution from the model name.
	// Must match a provider key in the model catalog (e.g. "openai", "anthropic").
	ProviderName  string `json:"provider_name,omitempty"`
	AllowFallback bool   `json:"allow_fallback,omitempty"`
	// FallbackProviders is an ordered list of provider names to try when
	// allow_fallback is true and the primary provider returns a
	// fallback-eligible runtime error (e.g. HTTP 429, 500, 502, 503, 504).
	// Providers are attempted in order; the first successful response wins.
	// When empty and allow_fallback is true, the runner falls back to the
	// runner-level default provider (if different from the primary).
	//
	// Model constraint: the same CompletionRequest.Model (i.e. the primary
	// model name) is sent verbatim to every fallback provider.  Fallback
	// providers must therefore serve that exact model ID.  No model
	// translation or remapping is performed between candidates.
	FallbackProviders []string          `json:"fallback_providers,omitempty"`
	SystemPrompt      string            `json:"system_prompt,omitempty"`
	TenantID          string            `json:"tenant_id,omitempty"`
	ConversationID    string            `json:"conversation_id,omitempty"`
	AgentID           string            `json:"agent_id,omitempty"`
	AgentIntent       string            `json:"agent_intent,omitempty"`
	TaskContext       string            `json:"task_context,omitempty"`
	PromptProfile     string            `json:"prompt_profile,omitempty"`
	PromptExtensions  *PromptExtensions `json:"prompt_extensions,omitempty"`
	// MaxSteps caps the number of LLM turns for this run.
	// 0 means use the runner's config default (which may itself be 0 = unlimited).
	// Negative values are rejected at StartRun time.
	MaxSteps int `json:"max_steps,omitempty"`
	// MaxTurns caps the number of assistant turns for this run.
	// 0 means use the runner's config default (which may itself be 0 = unlimited).
	// Negative values are rejected at StartRun time.
	MaxTurns int `json:"max_turns,omitempty"`
	// MaxCostUSD is a per-run spending ceiling in US dollars.
	// After each LLM turn, if the cumulative cost (when pricing is available) is
	// >= MaxCostUSD the run is terminated with a run.cost_limit_reached event and
	// completed status (not failed — the run did work, it just hit its budget).
	// 0 means no ceiling (unlimited). Negative values are rejected at StartRun time.
	// The ceiling is only enforced when cost data is available (CostStatusAvailable);
	// unpriced models are never terminated by this check.
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	// ReasoningEffort controls the thinking budget forwarded to the provider.
	// For OpenAI o-series models, valid values are "low", "medium", "high".
	// Empty string means no preference (provider default).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// AllowedTools restricts which tools are available for this run.
	// When non-empty, only the listed tool names (plus always-available tools
	// such as AskUserQuestion, find_tool, and skill) are offered to the LLM.
	// An empty or nil slice means no restriction — all registered tools are
	// available. Skill constraints activated during execution override this
	// base filter for the duration of the skill.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DeniedTools lists tool names that must never be offered to or callable
	// from this run, even when activated or granted by AllowedTools. Used to
	// keep agent_swarm out of swarm-member runs (no nested swarms).
	DeniedTools []string `json:"denied_tools,omitempty"`
	// MCPServers specifies per-run MCP server configurations. Each entry
	// describes an MCP server to connect for this run only. The per-run servers
	// shadow any global MCP servers with the same name. When the run completes,
	// the per-run connections are torn down automatically.
	// Nil or empty means use global MCP configuration unchanged.
	MCPServers []MCPServerConfig `json:"mcp_servers,omitempty"`
	// DynamicRules is a list of pattern-triggered rules to inject into the
	// system prompt only when their trigger fires. Zero token cost until the
	// trigger fires. An empty or nil slice means no dynamic rules are active.
	DynamicRules []DynamicRule `json:"dynamic_rules,omitempty"`
	// ProfileName is the name of the TOML profile to activate for this run.
	// Profile mcp_servers shadow global servers with the same name.
	// Profile files are read from the runner's ProfilesDir (default: ~/.harness/profiles/).
	// An empty string means no profile is applied.
	ProfileName string `json:"profile,omitempty"`
	// ParentContextHandoff carries a bounded parent-to-child context summary.
	ParentContextHandoff *htools.ParentContextHandoff `json:"parent_context_handoff,omitempty"`
	// Permissions configures the two-axis permission model for this run.
	// If nil, DefaultPermissionConfig() is used (unrestricted sandbox, no approval).
	Permissions *PermissionConfig `json:"permissions,omitempty"`
	// InitiatorAPIKeyPrefix is the first 8 characters of the API key used to
	// authenticate the run request. It is populated by the server from the
	// auth context and written to the audit log for accountability. Never
	// set this field to the full API key; only the prefix is stored.
	// This field is intentionally excluded from JSON deserialization (server-set only).
	InitiatorAPIKeyPrefix string `json:"-"`
	// RoleModels optionally specifies per-role model overrides for this run.
	// When set, overrides the corresponding runner-level RoleModels config.
	// Empty fields fall back to the runner config RoleModels, then to Model.
	RoleModels *RoleModels `json:"role_models,omitempty"`
	// ForkDepth is the nesting depth when this run is a child spawned by
	// spawn_agent. 0 means root agent, 1 means first-level child, etc.
	// Used to gate task_complete visibility and enforce DefaultMaxForkDepth.
	// Populated automatically by RunForkedSkill; callers should not set this.
	ForkDepth int `json:"fork_depth,omitempty"`
	// Rules applies fine-grained allow, ask, or deny effects to tool calls.
	// Rules in Permissions and this field are evaluated together; the
	// RunRequest rules are appended after PermissionConfig.Rules.
	Rules []PermissionRule `json:"rules,omitempty"`
}

// ContinueRunRequest defines the continuation-specific inputs accepted when a
// completed run is resumed as a new run in the same conversation.
//
// Omitted fields inherit from the source run. When AllowedTools is provided,
// its value replaces the source run's tool filter; a provided empty slice means
// "unrestricted" (matching RunRequest.AllowedTools semantics).
type ContinueRunRequest struct {
	Prompt string `json:"prompt"`
	// AllowedTools overrides the source run's base tool filter when non-nil.
	AllowedTools *[]string `json:"allowed_tools,omitempty"`
	// Permissions overrides the source run's permission configuration when non-nil.
	Permissions *PermissionConfig `json:"permissions,omitempty"`
}

type PromptExtensions struct {
	Behaviors []string `json:"behaviors,omitempty"`
	Talents   []string `json:"talents,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Custom    string   `json:"custom,omitempty"`
}

// RoleModels configures per-role model overrides. Empty strings fall back to
// the run's primary Model field.
type RoleModels struct {
	Primary    string `json:"primary,omitempty"`    // override for the main step loop LLM calls
	Summarizer string `json:"summarizer,omitempty"` // override for context compaction / SummarizeMessages()
}

type RunnerConfig struct {
	DefaultModel string
	// DefaultProviderName is applied when a run does not explicitly select a
	// provider, allowing startup configuration to disambiguate mirrored models.
	DefaultProviderName string
	// RoleModels optionally overrides the model used for specific roles within
	// a run. Empty fields fall back to the run's Model (or DefaultModel).
	RoleModels          RoleModels
	DefaultSystemPrompt string
	DefaultAgentIntent  string
	MaxSteps            int
	// MaxTurns caps the number of assistant turns per run at the runner level.
	// 0 means unlimited. Per-run RunRequest.MaxTurns takes precedence.
	MaxTurns int
	// WorkerPoolSize caps the number of runs that execute concurrently.
	// When > 0, at most WorkerPoolSize runs are in RunStatusRunning at any
	// time; additional runs are placed in RunStatusQueued and started as
	// slots free up. When 0 (the default), there is no cap — all runs start
	// immediately (the legacy unbounded behaviour).
	WorkerPoolSize int
	// MaxCompletedRetention caps completed/failed/cancelled run states retained
	// in memory after terminal events are persisted and subscribers drain.
	// 0 uses the default retention window.
	MaxCompletedRetention int
	// MaxConversationRetention caps the in-memory conversation transcript mirror.
	// Persistent ConversationStore history, when configured, is left untouched.
	// 0 uses the default retention window.
	MaxConversationRetention int
	AskUserTimeout           time.Duration
	AskUserBroker            htools.AskUserQuestionBroker
	// ApprovalBroker is the broker used to pause/resume tool calls that require
	// operator approval. When nil, no approval pausing occurs even if
	// PermissionConfig.Approval is set to ApprovalPolicyDestructive or
	// ApprovalPolicyAll. Providing an InMemoryApprovalBroker wires up the full
	// pause/approve/deny lifecycle.
	ApprovalBroker     ApprovalBroker
	MemoryManager      om.Manager
	WorkingMemoryStore workingmemory.Store
	PromptEngine       systemprompt.Engine
	PreMessageHooks    []PreMessageHook
	PostMessageHooks   []PostMessageHook
	PreToolUseHooks    []PreToolUseHook
	PostToolUseHooks   []PostToolUseHook
	HookFailureMode    HookFailureMode
	ToolApprovalMode   ToolApprovalMode
	ToolPolicy         ToolPolicy
	ProviderRegistry   *catalog.ProviderRegistry `json:"-"`
	ConversationStore  ConversationStore         `json:"-"`
	ContextResetStore  ContextResetStore         `json:"-"`
	// Store is the optional run persistence store. When set, the runner calls
	// CreateRun, UpdateRun, AppendMessage, and AppendEvent to durably record
	// run state. Store errors are non-fatal: the runner logs them (when Logger
	// is configured) but continues execution. A nil Store disables persistence.
	Store store.Store `json:"-"`
	// ProfileRunStore is the optional profile run history store. When set, the
	// runner persists a ProfileRunRecord for each run that has a non-empty
	// ProfileName at completion time (both success and failure paths).
	// Errors are non-fatal: logged but never propagated to the caller.
	// A nil ProfileRunStore disables profile run history persistence.
	ProfileRunStore store.ProfileRunStoreIface `json:"-"`
	// S3Uploader is the optional S3 backup uploader. When set, the runner
	// calls UploadRun on each terminal event (run.completed or run.failed) to
	// stream the run's JSONL events to S3. Errors are non-fatal: the runner
	// logs them but continues. A nil uploader disables S3 backup silently.
	S3Uploader       s3backup.RunUploader    `json:"-"`
	Logger           Logger                  `json:"-"`
	Activations      *ActivationTracker      `json:"-"` // shared tracker for deferred tools
	SkillConstraints *SkillConstraintTracker `json:"-"` // shared tracker for skill tool constraints
	// RolloutDir is the root directory for JSONL rollout files. When set, every
	// run's events are recorded to <RolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl.
	// Leave empty to disable rollout recording.
	RolloutDir string
	// RedactionPipeline is an optional PII/secret redaction pipeline. When set,
	// every event payload is filtered through the pipeline before being appended
	// to the run's event list and before being written to JSONL rollouts.
	// A nil pipeline means no redaction is applied.
	RedactionPipeline *redaction.Pipeline `json:"-"`
	// ErrorChainEnabled enables error context snapshots and chain tracing.
	// When true, the runner emits an error.context SSE event immediately
	// before run.failed, containing an error classification, a rolling
	// snapshot of recent tool calls and messages, and an optional cause chain.
	ErrorChainEnabled bool
	// ErrorContextDepth controls the rolling window size for the error context
	// snapshot (number of tool calls and messages retained). Defaults to
	// errorchain.DefaultSnapshotDepth (10) when ErrorChainEnabled is true and
	// this field is <= 0.
	ErrorContextDepth int
	// CaptureReasoning controls whether reasoning/thinking text emitted by
	// the LLM provider is captured and exposed. When true, the runner:
	//   - Stores reasoning in Message.Reasoning for each assistant turn.
	//   - Emits a reasoning.complete SSE event with the text and token count.
	//   - Applies the RedactionPipeline to reasoning text if set.
	// Default is false (off) to preserve backward compatibility.
	CaptureReasoning bool
	// AutoCompactEnabled enables proactive context auto-compaction. When true,
	// the runner estimates token usage before each LLM call and triggers
	// compaction if the ratio exceeds AutoCompactThreshold.
	AutoCompactEnabled bool
	// AutoCompactMode is the compaction strategy ("strip", "summarize", or
	// "hybrid"). Default is "hybrid".
	AutoCompactMode string
	// AutoCompactThreshold is the fraction of ModelContextWindow that triggers
	// compaction (e.g. 0.80 means 80%). Default is 0.80.
	AutoCompactThreshold float64
	// AutoCompactKeepLast is the number of recent turns to preserve during
	// auto-compaction. Default is 8.
	AutoCompactKeepLast int
	// ModelContextWindow is the model's context window size in tokens.
	// Default is 128000.
	ModelContextWindow int
	// TraceToolDecisions enables forensic tool-decision tracing. When true,
	// a tool.decision event is emitted after each LLM turn that contains
	// tool calls, listing which tools were available and which were selected.
	TraceToolDecisions bool
	// DetectAntiPatterns enables detection of repetitive tool call patterns.
	// When true, the runner tracks (tool_name, args) pairs and emits a
	// tool.antipattern event the first time a pair is seen 3 or more times
	// in a single run.
	DetectAntiPatterns bool
	// TraceHookMutations enables before/after snapshots for pre-tool-use hooks.
	// When true, a tool.hook.mutation event is emitted whenever a hook modifies
	// or blocks a tool call's arguments.
	TraceHookMutations bool
	// CaptureRequestEnvelope enables forensic capture of the LLM request and
	// response envelope for each provider call. When true, the runner emits:
	//   - llm.request.snapshot BEFORE each provider call: SHA-256 hash of the
	//     prompt content, list of tool names, memory snippet, and step number.
	//   - llm.response.meta AFTER each provider call: wall-clock latency and
	//     the model version string returned by the provider (if any).
	// Default is false (off) to preserve backward compatibility and avoid
	// extra event volume when not needed.
	CaptureRequestEnvelope bool
	// SnapshotMemorySnippet controls whether the memory snippet text is
	// included verbatim in the llm.request.snapshot event payload. It
	// defaults to false so that PII or sensitive context stored in memory
	// is not written to forensic logs unless the operator explicitly opts
	// in. When false, the memory_snippet field is omitted from the event
	// even if a snippet is present (#229).
	SnapshotMemorySnippet       bool
	CostAnomalyDetectionEnabled bool
	CostAnomalyStepMultiplier   float64
	// AuditTrailEnabled enables the append-only compliance audit log.
	// When true, writes audit.jsonl alongside rollout.jsonl.
	AuditTrailEnabled             bool
	ContextWindowSnapshotEnabled  bool
	ContextWindowWarningThreshold float64
	// CausalGraphEnabled enables causal event graph construction. When true,
	// the runner builds a causal dependency graph during the run (tracking
	// Tier 1 context dependencies and Tier 2 data-flow heuristics) and emits
	// a causal.graph.snapshot event at run end.
	CausalGraphEnabled bool
	// ProfilesDir is the directory containing named profile TOML files.
	// Defaults to ~/.harness/profiles/ if empty.
	// Used to load mcp_servers from a named profile when RunRequest.ProfileName is set.
	ProfilesDir string
	// GlobalMCPRegistry is the global MCPRegistry used to route tool calls to
	// globally registered MCP servers. When set alongside GlobalMCPServerNames,
	// it is passed to buildPerRunMCPRegistry so per-run servers can shadow globals.
	// A nil registry means no global MCP servers are configured.
	GlobalMCPRegistry htools.MCPRegistry `json:"-"`
	// GlobalMCPServerNames is the set of server names already registered in
	// GlobalMCPRegistry. Per-run MCP servers with the same name cause an error
	// unless they come from a profile (profile servers shadow without error).
	GlobalMCPServerNames []string
	// DynamicRules is the default list of pattern-triggered rules to inject into
	// the system prompt when their trigger fires. Per-run RunRequest.DynamicRules
	// are appended to (not replacing) this list when both are set.
	// An empty or nil slice means no dynamic rules are active by default.
	DynamicRules []DynamicRule
	// WorkspaceBaseOptions provides defaults for per-run workspace provisioning.
	// Used when a RunRequest specifies a non-empty WorkspaceType.
	// For WorkspaceType="worktree", RepoPath must be set here unless the caller
	// embeds it in a custom workspace registry.
	WorkspaceBaseOptions WorkspaceProvisionOptions
	// BaseRegistryOptions are the options used to construct the runner's
	// default tool registry. They are stored here so that when a per-run
	// workspace is provisioned (any WorkspaceType), the runner can rebuild
	// a fresh tool registry rooted at the provisioned path. Without this,
	// filesystem and shell tools resolve paths against the harnessd's
	// startup workspace regardless of provisioning, defeating isolation.
	BaseRegistryOptions DefaultRegistryOptions
}

// ContextReset records a single context reset event for a run.
type ContextReset struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	ResetIndex int             `json:"reset_index"`
	AtStep     int             `json:"at_step"`
	Persist    json.RawMessage `json:"persist,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ContextResetStore persists context reset events for a run.
// It is an optional dependency injected via RunnerConfig.
type ContextResetStore interface {
	RecordContextReset(ctx context.Context, runID string, resetIndex, atStep int, persist json.RawMessage) error
	GetContextResets(ctx context.Context, runID string) ([]ContextReset, error)
}

// Logger is a minimal logging interface for the runner.
type Logger interface {
	Error(msg string, keysAndValues ...any)
}

type ToolApprovalMode string

const (
	ToolApprovalModeFullAuto    ToolApprovalMode = "full_auto"
	ToolApprovalModePermissions ToolApprovalMode = "permissions"
	// ToolApprovalModeAll requires policy approval for every tool call, including reads.
	ToolApprovalModeAll ToolApprovalMode = "all"
)

// SandboxScope controls what the agent is allowed to access.
type SandboxScope string

const (
	// SandboxScopeWorkspace: agent can only access within the workspace directory.
	SandboxScopeWorkspace SandboxScope = "workspace"
	// SandboxScopeLocal: agent can access the local filesystem without the
	// workspace-only path restriction, but the current bash sandbox still blocks
	// outbound network commands as a defense-in-depth policy.
	SandboxScopeLocal SandboxScope = "local"
	// SandboxScopeUnrestricted: no filesystem restrictions (existing behavior).
	SandboxScopeUnrestricted SandboxScope = "unrestricted"
)

// ApprovalPolicy controls when the agent must ask for approval.
type ApprovalPolicy string

const (
	// ApprovalPolicyNone: never ask for approval (full auto).
	ApprovalPolicyNone ApprovalPolicy = "none"
	// ApprovalPolicyDestructive: ask before destructive/mutating operations.
	ApprovalPolicyDestructive ApprovalPolicy = "destructive"
	// ApprovalPolicyAll: ask before every tool call.
	ApprovalPolicyAll ApprovalPolicy = "all"
)

// PermissionConfig combines sandbox scope and approval policy.
type PermissionConfig struct {
	Sandbox  SandboxScope   `json:"sandbox"`
	Approval ApprovalPolicy `json:"approval"`
	// Rules applies fine-grained effects to matching tool calls. A nil or empty
	// rule set leaves the legacy two-axis permission behavior unchanged.
	Rules *PermissionRuleSet `json:"rules,omitempty"`
}

// DefaultPermissionConfig returns the default (workspace-confined, no
// approval required) permission configuration.
//
// SAFETY DEFAULT: Sandbox defaults to SandboxScopeWorkspace, not
// SandboxScopeUnrestricted. The harness is used both as a local coding tool
// and to run multi-agent/multi-tenant workloads; a caller that does not
// explicitly opt out must not be able to read/write arbitrary absolute host
// paths (e.g. ~/.ssh/id_rsa) via the file tools. Callers that legitimately
// need broader filesystem access must explicitly request
// SandboxScopeLocal or SandboxScopeUnrestricted via PermissionConfig.Sandbox
// on the run request.
func DefaultPermissionConfig() PermissionConfig {
	return PermissionConfig{
		Sandbox:  SandboxScopeWorkspace,
		Approval: ApprovalPolicyNone,
	}
}

// ToLegacy converts PermissionConfig to the legacy approval mode.
// This preserves backward compatibility with existing ToolApprovalMode usage.
func (p PermissionConfig) ToLegacy() ToolApprovalMode {
	switch p.Approval {
	case ApprovalPolicyNone:
		return ToolApprovalModeFullAuto
	case ApprovalPolicyDestructive:
		return ToolApprovalModePermissions
	case ApprovalPolicyAll:
		return ToolApprovalModeAll
	default:
		return ToolApprovalModeFullAuto
	}
}

// ValidatePermissionConfig checks that all fields in PermissionConfig are valid.
func ValidatePermissionConfig(p PermissionConfig) error {
	switch p.Sandbox {
	case SandboxScopeWorkspace, SandboxScopeLocal, SandboxScopeUnrestricted:
		// valid
	case "":
		// empty defaults to workspace (the safety-biased default; see
		// DefaultPermissionConfig and normalizePermissionConfig) — also valid
		// at validation time
	default:
		return fmt.Errorf("invalid sandbox scope %q: must be one of workspace, local, unrestricted", p.Sandbox)
	}
	switch p.Approval {
	case ApprovalPolicyNone, ApprovalPolicyDestructive, ApprovalPolicyAll:
		// valid
	case "":
		// empty defaults to none — also valid at validation time
	default:
		return fmt.Errorf("invalid approval policy %q: must be one of none, destructive, all", p.Approval)
	}
	if err := ValidatePermissionRules(permissionRulesFromSet(p.Rules)); err != nil {
		return err
	}
	return nil
}

// MCPServerConfig describes an MCP server to connect for a single run.
// Exactly one of Command or URL must be set. Command launches a stdio
// subprocess; URL connects via HTTP/SSE.
type MCPServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// RuleTrigger describes the condition under which a DynamicRule fires.
// Currently only ToolNames is supported (MVP): the rule fires when the
// previous step's tool calls include any of the listed tool names.
type RuleTrigger struct {
	// ToolNames is a list of tool names. The rule fires when any of these
	// tool names was called in the previous step.
	ToolNames []string `json:"tool_names,omitempty"`
}

// DynamicRule is a pattern-triggered rule that is injected into the system
// prompt only when its trigger fires. Zero token cost until the trigger fires.
type DynamicRule struct {
	// ID is a unique identifier for this rule within a run.
	ID string `json:"id"`
	// Trigger defines the condition that causes this rule to be injected.
	Trigger RuleTrigger `json:"trigger"`
	// Content is the text appended to the system prompt when the rule fires.
	Content string `json:"content"`
	// FireOnce, when true, injects the rule content only the first time the
	// trigger fires per run. Subsequent matching steps do not re-inject it.
	FireOnce bool `json:"fire_once,omitempty"`
}

type ToolPolicyInput struct {
	ToolName  string          `json:"tool_name"`
	Action    string          `json:"action"`
	Path      string          `json:"path,omitempty"`
	Arguments json.RawMessage `json:"arguments"`
	Mutating  bool            `json:"mutating"`
}

type ToolPolicyDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

type ToolPolicy interface {
	Allow(ctx context.Context, in ToolPolicyInput) (ToolPolicyDecision, error)
}

type HookAction string

const (
	HookActionContinue HookAction = "continue"
	HookActionBlock    HookAction = "block"
)

type HookFailureMode string

const (
	HookFailureModeFailClosed HookFailureMode = "fail_closed"
	HookFailureModeFailOpen   HookFailureMode = "fail_open"
)

type PreMessageHookInput struct {
	RunID   string
	Step    int
	Request CompletionRequest
}

type PreMessageHookResult struct {
	Action         HookAction
	Reason         string
	MutatedRequest *CompletionRequest
}

type PostMessageHookInput struct {
	RunID     string
	Step      int
	Request   CompletionRequest
	Response  CompletionResult
	ToolCalls []ToolCall
}

type PostMessageHookResult struct {
	Action          HookAction
	Reason          string
	MutatedResponse *CompletionResult
}

type PreMessageHook interface {
	Name() string
	BeforeMessage(ctx context.Context, in PreMessageHookInput) (PreMessageHookResult, error)
}

type PostMessageHook interface {
	Name() string
	AfterMessage(ctx context.Context, in PostMessageHookInput) (PostMessageHookResult, error)
}

// ToolHookDecision controls whether a tool call proceeds.
type ToolHookDecision int

const (
	// ToolHookAllow permits tool execution (zero value = allow by default).
	ToolHookAllow ToolHookDecision = iota
	// ToolHookDeny blocks tool execution and returns an error result to the LLM.
	ToolHookDeny
)

// PreToolUseEvent is passed to PreToolUseHooks before a tool executes.
type PreToolUseEvent struct {
	// ToolName is the name of the tool about to execute.
	ToolName string
	// CallID is the tool_call_id from the LLM response.
	CallID string
	// Args is the raw JSON arguments as provided by the LLM (possibly
	// modified by an earlier hook in the chain).
	Args json.RawMessage
	// RunID is the active run identifier.
	RunID string
}

// PreToolUseResult is returned from PreToolUseHooks.
type PreToolUseResult struct {
	// Decision controls whether the tool is allowed to execute.
	// A zero value (ToolHookAllow) permits execution.
	Decision ToolHookDecision
	// Reason is a human-readable explanation used when Decision is Deny
	// or when emitting hook events.
	Reason string
	// ModifiedArgs replaces the LLM-provided args passed to the tool handler.
	// If nil, the previous args (original or from a prior hook) are used.
	ModifiedArgs json.RawMessage
}

// PostToolUseEvent is passed to PostToolUseHooks after a tool executes.
type PostToolUseEvent struct {
	// ToolName is the name of the tool that executed.
	ToolName string
	// CallID is the tool_call_id from the LLM response.
	CallID string
	// Args is the raw JSON arguments that were passed to the tool handler
	// (after any pre-tool-use hook modifications).
	Args json.RawMessage
	// Result is the output string returned by the tool handler.
	// Empty when Error is non-nil.
	Result string
	// Duration is the wall-clock time the tool handler took to execute.
	Duration time.Duration
	// Error is non-nil if the tool handler returned an error.
	Error error
	// RunID is the active run identifier.
	RunID string
}

// PostToolUseResult is returned from PostToolUseHooks.
type PostToolUseResult struct {
	// ModifiedResult replaces the tool output passed to the LLM.
	// If empty, the original tool result is used unchanged.
	ModifiedResult string
}

// PreToolUseHook intercepts tool calls before execution.
type PreToolUseHook interface {
	Name() string
	// PreToolUse is called before the tool handler executes.
	// Return nil result (with nil error) to allow with no modification.
	PreToolUse(ctx context.Context, ev PreToolUseEvent) (*PreToolUseResult, error)
}

// PostToolUseHook intercepts tool calls after execution.
type PostToolUseHook interface {
	Name() string
	// PostToolUse is called after the tool handler executes (even on error).
	PostToolUse(ctx context.Context, ev PostToolUseEvent) (*PostToolUseResult, error)
}
