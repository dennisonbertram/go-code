package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/workingmemory"
)

type Action string

const (
	ActionRead     Action = "read"
	ActionWrite    Action = "write"
	ActionList     Action = "list"
	ActionExecute  Action = "execute"
	ActionFetch    Action = "fetch"
	ActionDownload Action = "download"
)

type ApprovalMode string

const (
	ApprovalModeFullAuto    ApprovalMode = "full_auto"
	ApprovalModePermissions ApprovalMode = "permissions"
	// ApprovalModeAll requires policy approval for every tool call, including reads.
	ApprovalModeAll ApprovalMode = "all"
)

// SandboxScope mirrors harness.SandboxScope at the tools layer so the tools
// package does not import the harness package (which would create a cycle).
type SandboxScope string

const (
	SandboxScopeWorkspace    SandboxScope = "workspace"
	SandboxScopeLocal        SandboxScope = "local"
	SandboxScopeUnrestricted SandboxScope = "unrestricted"
)

type PolicyInput struct {
	ToolName  string          `json:"tool_name"`
	Action    Action          `json:"action"`
	Path      string          `json:"path,omitempty"`
	Arguments json.RawMessage `json:"arguments"`
	Mutating  bool            `json:"mutating"`
}

type PolicyDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

type Policy interface {
	Allow(ctx context.Context, in PolicyInput) (PolicyDecision, error)
}

// ToolTier classifies a tool as core (always visible) or deferred (hidden until activated).
type ToolTier string

const (
	// TierCore tools are always sent to the LLM.
	TierCore ToolTier = "core"
	// TierDeferred tools are hidden until activated via find_tool.
	TierDeferred ToolTier = "deferred"
)

type Definition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Parameters   map[string]any `json:"parameters"`
	Action       Action         `json:"-"`
	Mutating     bool           `json:"-"`
	ParallelSafe bool           `json:"-"`
	Tags         []string       `json:"-"` // search tags for tool discovery
	Tier         ToolTier       `json:"-"` // core or deferred
}

// ActivationTrackerInterface tracks which deferred tools have been activated per run.
type ActivationTrackerInterface interface {
	Activate(runID string, toolNames ...string)
	IsActive(runID string, toolName string) bool
}

type Handler func(ctx context.Context, args json.RawMessage) (string, error)

type Tool struct {
	Definition Definition
	Handler    Handler
}

// PromptExtensionDirs holds the resolved absolute paths to the prompt extension directories.
type PromptExtensionDirs struct {
	BehaviorsDir string
	TalentsDir   string
}

type BuildOptions struct {
	WorkspaceRoot      string
	ApprovalMode       ApprovalMode
	Policy             Policy
	SandboxScope       SandboxScope // controls filesystem/network restrictions
	HTTPClient         *http.Client
	Now                func() time.Time
	AskUserBroker      AskUserQuestionBroker
	AskUserTimeout     time.Duration
	MemoryManager      om.Manager
	WorkingMemoryStore workingmemory.Store

	MCPRegistry         MCPRegistry
	AgentRunner         AgentRunner
	WebFetcher          WebFetcher
	Sourcegraph         SourcegraphConfig
	EnableTodos         bool
	EnableLSP           bool
	EnableMCP           bool
	EnableAgent         bool
	EnableWebOps        bool
	EnableSkills        bool
	SkillLister         SkillLister
	SkillVerifier       SkillVerifier
	ModelCatalog        *catalog.Catalog
	EnableCron          bool
	CronClient          CronClient
	CallbackManager     *CallbackManager
	EnableCallbacks     bool
	EnableRecipes       bool
	RecipesDir          string
	ConversationStore   ConversationReader
	MessageSummarizer   MessageSummarizer
	EnableConversations bool

	// PromptExtensionDirs provides the extension directories for the create_prompt_extension tool.
	// If empty, that tool returns an error indicating it is not configured.
	PromptExtensionDirs PromptExtensionDirs

	// SubagentManager provides the interface for creating and polling subagents.
	// When non-nil, the run_agent tool is registered and available to the LLM.
	SubagentManager SubagentManager

	// ProfilesDir is the directory to search for user-global profile TOML files.
	// Used by run_agent and list_profiles. Defaults to ~/.harness/profiles/.
	ProfilesDir string
}

// ConversationSummary holds lightweight metadata about a conversation.
type ConversationSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	MsgCount  int    `json:"message_count"`
}

// ConversationSearchResult is a single result from a full-text search over conversations.
type ConversationSearchResult struct {
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
	Snippet        string `json:"snippet"`
}

// ConversationReader provides read-only access to conversation history.
// Implementations must be safe for concurrent use.
type ConversationReader interface {
	ListConversations(ctx context.Context, limit, offset int) ([]ConversationSummary, error)
	SearchConversations(ctx context.Context, query string, limit int) ([]ConversationSearchResult, error)
}

type SourcegraphConfig struct {
	Endpoint string
	Token    string
}

type MCPResource struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type MCPToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type MCPRegistry interface {
	ListResources(ctx context.Context, server string) ([]MCPResource, error)
	ReadResource(ctx context.Context, server, uri string) (string, error)
	ListTools(ctx context.Context) (map[string][]MCPToolDefinition, error)
	CallTool(ctx context.Context, server, tool string, args json.RawMessage) (string, error)
}

type AgentRunner interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

// ConstrainedAgentRunner extends AgentRunner with support for preserving
// per-run allowed-tools restrictions on prompt-based fallback paths.
type ConstrainedAgentRunner interface {
	AgentRunner
	RunPromptWithAllowedTools(ctx context.Context, prompt string, allowedTools []string) (string, error)
}

// ForkConfig holds configuration for a forked skill execution.
type ForkConfig struct {
	Prompt       string            // the interpolated skill body
	SkillName    string            // name of the skill being forked
	Agent        string            // agent type hint (e.g., "Explore")
	AllowedTools []string          // tool restrictions for the subagent
	Metadata     map[string]string // arbitrary metadata (parent run ID, etc.)
	// ParentContextHandoff carries serialized context from the parent run.
	ParentContextHandoff *ParentContextHandoff
	// Model optionally overrides the runner's default model for the child run.
	// An empty string means use the runner's configured default.
	Model string
	// MaxSteps optionally caps the number of LLM turns for the child run.
	// 0 or negative means inherit from parent run or use runner default.
	MaxSteps int
	// MaxTurns optionally caps the number of assistant turns for the child run.
	// 0 means unlimited (no turn budget). Overrides MaxSteps when both are set.
	MaxTurns int
}

// ForkResult holds the output from a forked skill execution.
type ForkResult struct {
	Output  string // the subagent's final output
	Summary string // optional summarized output
	Error   string // error message if the subagent failed
}

// ForkedAgentRunner extends AgentRunner with support for forked skill execution.
// Implementations that only support basic RunPrompt need not implement this.
type ForkedAgentRunner interface {
	AgentRunner
	RunForkedSkill(ctx context.Context, config ForkConfig) (ForkResult, error)
}

// ModelAgentRunner extends AgentRunner with support for model-overriding sub-agent execution.
// When the agent tool receives a non-empty "model" parameter and the runner implements this
// interface, RunPromptWithModel is called instead of RunPrompt. Implementations that only
// support the default model need not implement this interface.
type ModelAgentRunner interface {
	AgentRunner
	// RunPromptWithModel runs a sub-agent prompt on a specific model. An empty model
	// string should be treated the same as calling RunPrompt (use runner default).
	RunPromptWithModel(ctx context.Context, prompt, model string) (string, error)
}

// SubagentRequest is a tool-layer subagent request, mirroring subagents.Request
// without importing the subagents package (which would create a cycle).
type SubagentRequest struct {
	Prompt       string
	Model        string
	SystemPrompt string
	MaxSteps     int
	MaxCostUSD   float64
	AllowedTools []string
	ProfileName  string
	// ParentContextHandoff carries serialized context from the parent run.
	ParentContextHandoff *ParentContextHandoff

	// New runtime and safety policy fields sourced from profile.ApplyValues().
	// All fields are backward-compatible: zero value = inherit from defaults.

	// ReasoningEffort is the reasoning effort hint ("low", "medium", "high").
	// Empty means provider default.
	ReasoningEffort string
	// IsolationMode selects the workspace isolation backend.
	// Empty means inline (no isolation).
	IsolationMode string
	// CleanupPolicy controls workspace lifecycle after the run completes.
	// Empty means inherit from manager defaults.
	CleanupPolicy string
	// BaseRef is the git ref to use as base for worktree-backed runs.
	// Empty means inherit from manager defaults.
	BaseRef string
	// ResultMode controls how subagent output is formatted.
	// Empty means inherit from defaults.
	ResultMode string
}

// SubagentResult is a tool-layer subagent result, mirroring subagents.Subagent
// without importing the subagents package.
type SubagentResult struct {
	ID     string
	RunID  string
	Status string // "queued" | "running" | "completed" | "failed" | "cancelled"
	Output string
	Error  string
}

// ParentContextMessage is a single transcript message included in a handoff bundle.
type ParentContextMessage struct {
	Index      int64  `json:"index"`
	Role       string `json:"role"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Content    string `json:"content"`
}

// ParentContextHandoff contains a bounded, typed summary of parent-run context.
type ParentContextHandoff struct {
	ParentRunID          string                 `json:"parent_run_id"`
	ParentTenantID       string                 `json:"parent_tenant_id,omitempty"`
	ParentConversationID string                 `json:"parent_conversation_id,omitempty"`
	ParentAgentID        string                 `json:"parent_agent_id,omitempty"`
	Messages             []ParentContextMessage `json:"messages"`
}

const (
	defaultParentContextMaxMessages     = 16
	defaultParentContextMaxMessageRunes = 400
	defaultParentContextHandoffMaxBytes = 8192
	parentContextTaskHeader             = "# Task"
	parentContextHandoffHeader          = "# Parent context handoff"
)

// BuildParentContextHandoffFromContext extracts a bounded parent handoff bundle
// from context keys (RunMetadata and TranscriptReader).
func BuildParentContextHandoffFromContext(ctx context.Context) (ParentContextHandoff, bool) {
	handoff := ParentContextHandoff{}
	meta, hasMeta := RunMetadataFromContext(ctx)
	hasAny := false
	if hasMeta {
		handoff.ParentRunID = strings.TrimSpace(meta.RunID)
		handoff.ParentTenantID = strings.TrimSpace(meta.TenantID)
		handoff.ParentConversationID = strings.TrimSpace(meta.ConversationID)
		handoff.ParentAgentID = strings.TrimSpace(meta.AgentID)
		hasAny = strings.TrimSpace(meta.RunID) != ""
	}

	reader, ok := TranscriptReaderFromContext(ctx)
	if ok {
		snapshot := reader.Snapshot(0, true)
		handoff.Messages = append([]ParentContextMessage{}, reduceAndTruncateMessages(snapshot.Messages)...)
		hasAny = hasAny || len(handoff.Messages) > 0
	}

	boundParentContextHandoff(&handoff)
	if !hasAny {
		return ParentContextHandoff{}, false
	}
	return handoff, true
}

func boundParentContextHandoff(handoff *ParentContextHandoff) {
	if handoff == nil {
		return
	}
	msgs := handoff.Messages
	if len(msgs) > defaultParentContextMaxMessages {
		msgs = append([]ParentContextMessage{}, msgs[len(msgs)-defaultParentContextMaxMessages:]...)
	}

	bounded := make([]ParentContextMessage, 0, len(msgs))
	for _, msg := range msgs {
		bounded = append(bounded, ParentContextMessage{
			Index:      msg.Index,
			Role:       strings.TrimSpace(msg.Role),
			Name:       strings.TrimSpace(msg.Name),
			ToolCallID: strings.TrimSpace(msg.ToolCallID),
			Content:    truncateRunes(msg.Content, defaultParentContextMaxMessageRunes),
		})
	}
	handoff.Messages = bounded

	for {
		payload := mustJSONPayload(handoff)
		if len(payload) <= defaultParentContextHandoffMaxBytes {
			return
		}
		if len(handoff.Messages) == 0 {
			return
		}
		handoff.Messages = append([]ParentContextMessage{}, handoff.Messages[1:]...)
	}
}

func reduceAndTruncateMessages(messages []TranscriptMessage) []ParentContextMessage {
	if len(messages) == 0 {
		return nil
	}
	bounded := make([]ParentContextMessage, 0, len(messages))
	for _, msg := range messages {
		bounded = append(bounded, ParentContextMessage{
			Index:      msg.Index,
			Role:       msg.Role,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Content:    truncateRunes(msg.Content, defaultParentContextMaxMessageRunes),
		})
	}
	return bounded
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 1 {
		return "…"
	}
	truncated := append([]rune{}, runes[:maxRunes-1]...)
	truncated = append(truncated, '…')
	return string(truncated)
}

// RenderParentContextHandoffBlock renders a markdown block for prompt insertion.
func RenderParentContextHandoffBlock(handoff ParentContextHandoff) string {
	if len(handoff.Messages) == 0 && strings.TrimSpace(handoff.ParentRunID) == "" {
		return ""
	}
	payload := mustJSONPayload(handoff)
	return parentContextHandoffHeader + "\n```json\n" + string(payload) + "\n```"
}

// RenderPromptWithParentContext prepends the handoff block before the task.
func RenderPromptWithParentContext(task string, handoff ParentContextHandoff) string {
	section := RenderParentContextHandoffBlock(handoff)
	if section == "" {
		return strings.TrimSpace(task)
	}
	return strings.TrimSpace(section) + "\n\n" + parentContextTaskHeader + "\n\n" + strings.TrimSpace(task)
}

func mustJSONPayload(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// SubagentManager is the interface for creating and polling subagents.
// Implementations are provided by the subagents package; this interface lives
// here to avoid an import cycle between tools/deferred and subagents.
type SubagentManager interface {
	// CreateAndWait creates a subagent, waits for it to complete, and returns
	// the result. The subagent runs inline (no worktree isolation).
	CreateAndWait(ctx context.Context, req SubagentRequest) (SubagentResult, error)
	// StartSubagent creates a subagent and returns immediately without waiting
	// for completion.
	Start(ctx context.Context, req SubagentRequest) (SubagentResult, error)
	// GetSubagent fetches the latest status for a running/finished subagent.
	Get(ctx context.Context, id string) (SubagentResult, error)
	// Wait blocks until the subagent reaches a terminal state and returns the
	// terminal result.
	Wait(ctx context.Context, id string) (SubagentResult, error)
	// Cancel requests cancellation for a running subagent.
	Cancel(ctx context.Context, id string) error
}

// SkillInfo holds read-only skill metadata for the tool layer.
type SkillInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argument_hint,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Source       string   `json:"source"`
	Context      string   `json:"context,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	Verified     bool     `json:"verified,omitempty"`
	VerifiedAt   string   `json:"verified_at,omitempty"`
	VerifiedBy   string   `json:"verified_by,omitempty"`
	FilePath     string   `json:"file_path,omitempty"` // needed for verify action
}

// SkillLister provides skill lookup and listing for the skill tool.
type SkillLister interface {
	GetSkill(name string) (SkillInfo, bool)
	ListSkills() []SkillInfo
	ResolveSkill(ctx context.Context, name, args, workspace string) (string, error)
}

// SkillVerifier extends SkillLister with verification support.
// It provides the file path of a skill's SKILL.md for structural validation,
// and allows marking a skill as verified in the underlying store.
type SkillVerifier interface {
	SkillLister
	// GetSkillFilePath returns the absolute path to the skill's SKILL.md file.
	GetSkillFilePath(name string) (string, bool)
	// UpdateSkillVerification updates the verified status of a skill.
	UpdateSkillVerification(ctx context.Context, name string, verified bool, verifiedAt time.Time, verifiedBy string) error
}

type WebFetcher interface {
	Search(ctx context.Context, query string, maxResults int) ([]map[string]any, error)
	Fetch(ctx context.Context, url string) (string, error)
}

type AskUserQuestionRequest struct {
	RunID     string
	CallID    string
	Questions []AskUserQuestion
	Timeout   time.Duration
}

type AskUserQuestionPending struct {
	RunID      string            `json:"run_id"`
	CallID     string            `json:"call_id"`
	Tool       string            `json:"tool"`
	Questions  []AskUserQuestion `json:"questions"`
	DeadlineAt time.Time         `json:"deadline_at"`
}

type AskUserQuestionBroker interface {
	Ask(ctx context.Context, req AskUserQuestionRequest) (answers map[string]string, answeredAt time.Time, err error)
	Pending(runID string) (AskUserQuestionPending, bool)
	Submit(runID string, answers map[string]string) error
}

type contextKey string

const ContextKeyRunID contextKey = "run_id"
const ContextKeyToolCallID contextKey = "tool_call_id"
const ContextKeyForkedSkill contextKey = "forked_skill"
const ContextKeyRunMetadata contextKey = "run_metadata"
const ContextKeyTranscriptReader contextKey = "transcript_reader"
const ContextKeyOutputStreamer contextKey = "output_streamer"
const ContextKeyMessageReplacer contextKey = "message_replacer"
const ContextKeySandboxScope contextKey = "sandbox_scope"
const contextKeyForkDepth contextKey = "fork_depth"

// DefaultMaxForkDepth is the maximum recursion depth for spawned subagents.
// Agents at depth >= DefaultMaxForkDepth may not spawn further children.
const DefaultMaxForkDepth = 5

// ForkDepthFromContext returns the current agent nesting depth.
// Depth 0 is the root agent; each spawn_agent call increments depth by 1.
func ForkDepthFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(contextKeyForkDepth).(int)
	return v
}

// WithForkDepth returns a context with the given fork depth set.
func WithForkDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, contextKeyForkDepth, depth)
}

// WithSandboxScope returns a context with the given sandbox scope set for the
// current tool execution. Nil contexts are promoted to Background.
func WithSandboxScope(ctx context.Context, scope SandboxScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ContextKeySandboxScope, scope)
}

type RunMetadata struct {
	RunID          string
	TenantID       string
	ConversationID string
	AgentID        string
}

type TranscriptMessage struct {
	Index      int64  `json:"index"`
	Role       string `json:"role"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Content    string `json:"content,omitempty"`
}

type TranscriptSnapshot struct {
	RunID          string              `json:"run_id"`
	TenantID       string              `json:"tenant_id"`
	ConversationID string              `json:"conversation_id"`
	AgentID        string              `json:"agent_id"`
	Messages       []TranscriptMessage `json:"messages"`
	GeneratedAt    time.Time           `json:"generated_at"`
}

type TranscriptReader interface {
	Snapshot(limit int, includeTools bool) TranscriptSnapshot
}

// MessageSummarizer summarizes a slice of messages into a compact summary string.
// Used by compact_history tool in summarize/hybrid modes.
type MessageSummarizer interface {
	SummarizeMessages(ctx context.Context, messages []map[string]any) (string, error)
}

func RunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if meta, ok := ctx.Value(ContextKeyRunMetadata).(RunMetadata); ok {
		return meta.RunID
	}
	v, _ := ctx.Value(ContextKeyRunID).(string)
	return v
}

func ToolCallIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ContextKeyToolCallID).(string)
	return v
}

func RunMetadataFromContext(ctx context.Context) (RunMetadata, bool) {
	if ctx == nil {
		return RunMetadata{}, false
	}
	v, ok := ctx.Value(ContextKeyRunMetadata).(RunMetadata)
	return v, ok
}

func TranscriptReaderFromContext(ctx context.Context) (TranscriptReader, bool) {
	if ctx == nil {
		return nil, false
	}
	v, ok := ctx.Value(ContextKeyTranscriptReader).(TranscriptReader)
	return v, ok
}

// OutputStreamerFromContext retrieves the output streamer function from the context.
// The streamer, if present, receives incremental output chunks as they are produced
// by a running tool. Callers that do not support streaming may omit it.
func OutputStreamerFromContext(ctx context.Context) (func(chunk string), bool) {
	if ctx == nil {
		return nil, false
	}
	fn, ok := ctx.Value(ContextKeyOutputStreamer).(func(chunk string))
	return fn, ok
}

// MessageReplacerFromContext retrieves the message replacer callback from the context.
// The callback accepts a new message slice that replaces the runner's in-flight messages.
func MessageReplacerFromContext(ctx context.Context) (func([]map[string]any), bool) {
	if ctx == nil {
		return nil, false
	}
	fn, ok := ctx.Value(ContextKeyMessageReplacer).(func([]map[string]any))
	return fn, ok
}

// SandboxScopeFromContext retrieves the effective sandbox scope override from
// the tool execution context.
func SandboxScopeFromContext(ctx context.Context) (SandboxScope, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(ContextKeySandboxScope).(SandboxScope)
	return v, ok
}

// CronClient provides access to the cron scheduler daemon.
type CronClient interface {
	CreateJob(ctx context.Context, req CronCreateJobRequest) (CronJob, error)
	ListJobs(ctx context.Context) ([]CronJob, error)
	GetJob(ctx context.Context, id string) (CronJob, error)
	UpdateJob(ctx context.Context, id string, req CronUpdateJobRequest) (CronJob, error)
	DeleteJob(ctx context.Context, id string) error
	ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]CronExecution, error)
	Health(ctx context.Context) error
}

// CronJob represents a scheduled cron job.
type CronJob struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Schedule   string    `json:"schedule"`
	ExecType   string    `json:"execution_type"`
	ExecConfig string    `json:"execution_config"`
	Status     string    `json:"status"`
	TimeoutSec int       `json:"timeout_seconds"`
	Tags       string    `json:"tags"`
	NextRunAt  time.Time `json:"next_run_at"`
	LastRunAt  time.Time `json:"last_run_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CronExecution represents a single execution of a cron job.
type CronExecution struct {
	ID            string    `json:"id"`
	JobID         string    `json:"job_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Status        string    `json:"status"`
	RunID         string    `json:"run_id,omitempty"`
	OutputSummary string    `json:"output_summary,omitempty"`
	Error         string    `json:"error,omitempty"`
	DurationMs    int64     `json:"duration_ms"`
}

// CronCreateJobRequest is the request for creating a cron job.
type CronCreateJobRequest struct {
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	ExecType   string `json:"execution_type"`
	ExecConfig string `json:"execution_config"`
	TimeoutSec int    `json:"timeout_seconds,omitempty"`
	Tags       string `json:"tags,omitempty"`
}

// CronUpdateJobRequest is the request for updating a cron job.
type CronUpdateJobRequest struct {
	Schedule   *string `json:"schedule,omitempty"`
	ExecConfig *string `json:"execution_config,omitempty"`
	Status     *string `json:"status,omitempty"`
	TimeoutSec *int    `json:"timeout_seconds,omitempty"`
	Tags       *string `json:"tags,omitempty"`
}
