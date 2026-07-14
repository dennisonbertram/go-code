package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"unicode/utf8"

	"go-agent-harness/internal/forensics/audittrail"
	"go-agent-harness/internal/forensics/contextwindow"
	"go-agent-harness/internal/forensics/errorchain"
	"go-agent-harness/internal/forensics/redaction"
	"go-agent-harness/internal/forensics/tooldecision"
	htools "go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/profiles"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/rollout"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/systemprompt"
	"go-agent-harness/internal/workspace"
)

type runState struct {
	run                Run
	staticSystemPrompt string
	promptResolved     *systemprompt.ResolvedPrompt
	usageTotals        usageTotalsAccumulator
	costTotals         RunCostTotals
	messages           []Message
	events             []Event
	subscribers        map[chan Event]struct{}
	nextEventSeq       uint64
	steeringCh         chan string // buffered channel for user steering messages
	// maxCostUSD is the per-run spending ceiling (0 = unlimited).
	maxCostUSD float64
	// allowedTools is the per-run base tool filter from RunRequest.AllowedTools.
	// When non-empty, only these tools (plus AlwaysAvailableTools) are offered
	// to the LLM. Skill constraints override this during skill execution.
	// Nil or empty means no per-run restriction.
	allowedTools []string
	// permissions is the effective two-axis permission configuration for this run.
	permissions PermissionConfig
	// permissionWorkspaceRoot is the workspace used to resolve path rules.
	permissionWorkspaceRoot string
	// recorderCh is the input channel for the per-run recorder goroutine.
	// Events sent here are written to the JSONL rollout file in order.
	// Nil when no RolloutDir is configured.  The channel is closed (and
	// recorderDone closed by the goroutine) exactly once, on the terminal event.
	recorderCh chan rollout.RecordableEvent
	// recorderDone is closed by the recorder goroutine after it has drained
	// recorderCh and called recorder.Close().  Terminal-event callers wait on
	// this channel to ensure the JSONL file is fully flushed before returning.
	recorderDone chan struct{}
	// closeRecorderOnce closes recorderCh exactly once using sync.Once, ensuring
	// the recorder goroutine is always cleaned up even on non-terminal exits
	// (e.g. double panics where failRun itself panics and execute() exits
	// without emitting a terminal event).  The terminal-event path in emit()
	// also calls this function so that both paths share exactly-once semantics.
	closeRecorderOnce func()
	// auditWriter is the append-only hash-chained audit log writer.
	// Non-nil only when AuditTrailEnabled is set in RunnerConfig and RolloutDir is set.
	auditWriter *audittrail.AuditWriter
	// previousRunID is set when this run was created via ContinueRun.
	previousRunID string
	// continuationPolicyNotice is an optional hidden system/meta message injected
	// at the start of a continued run when the operator changed the continuation's
	// tool or permission policy relative to the source run.
	continuationPolicyNotice string
	// currentStep tracks the current step number during execution.
	currentStep int
	// continued is set to true once ContinueRun has been called on this run,
	// preventing a second continuation without mutating the run's terminal Status.
	continued bool
	// snapshotBuilder collects a rolling window of tool calls and messages for
	// error context snapshots. Non-nil only when ErrorChainEnabled is set in
	// RunnerConfig.
	snapshotBuilder *errorchain.SnapshotBuilder
	// terminated is set to true once the terminal event (run.completed or
	// run.failed) has been emitted. Any subsequent emit() call returns
	// immediately to prevent post-terminal streaming callbacks from appending
	// events after the forensic record is closed.
	terminated bool
	// compactMu serializes auto-compact and manual CompactRun calls.
	compactMu sync.Mutex
	// resetIndex increments each time the agent calls reset_context.
	// 0 means no reset has occurred yet for this run.
	resetIndex int
	// scopedMCPRegistry is the per-run MCP registry created when
	// RunRequest.MCPServers is non-empty. It is closed when the run completes.
	// Nil when no per-run MCP servers are configured.
	scopedMCPRegistry *ScopedMCPRegistry
	// firedOnceRules tracks the IDs of DynamicRules with FireOnce=true that
	// have already fired at least once during this run. Rules present in this
	// set are not re-injected on subsequent steps.
	firedOnceRules map[string]bool
	// dynamicRules is the merged list of active dynamic rules for this run
	// (runner-level config + per-request rules). Stored here so execute()
	// can access them without re-merging each step.
	dynamicRules []DynamicRule
	// profileName is the profile name from RunRequest.ProfileName, stored so
	// that forked sub-runs inherit the parent's profile (MCP servers, etc.).
	profileName string
	// resolvedRoleModels is the fully-merged role model configuration for this
	// run (per-request overrides merged on top of runner-level config). It is
	// set once at the start of execute() and read by autoCompactMessages so
	// that the per-request Summarizer override is honoured during compaction.
	resolvedRoleModels RoleModels
	// storedMsgCount tracks how many messages have already been persisted to
	// the store via AppendMessage. Used by storeAppendNewMessages to append
	// only messages that are new since the last persistence call.
	storedMsgCount int
	// workspaceCleanup is called once before the terminal event is emitted to
	// destroy the per-run workspace and emit workspace.destroyed. It is set in
	// execute() when workspace_type is non-empty, and called by runWorkspaceCleanup.
	// Nil when no per-run workspace was provisioned.
	workspaceCleanup func()
	// perRunTools is a tool registry rooted at the per-run provisioned workspace
	// path. It is populated when workspace_type is non-empty and provisioning
	// succeeds, so that filesystem and shell tools (read/write/bash/grep/...)
	// resolve paths against the provisioned workspace instead of the harnessd's
	// startup workspace. Nil when no provisioned workspace exists, in which case
	// the global Runner.tools is used.
	perRunTools *Registry
	// forkDepth is the recursive nesting depth for this run. 0 = root agent,
	// 1 = first child spawned by spawn_agent, etc. Used to gate task_complete
	// visibility and inject step-budget pressure messages for subagents.
	forkDepth int
}

// Runner concurrency/lifecycle invariants
//
// Event ledger:
//  1. emit() is the only writer of state.events and state.nextEventSeq; the
//     sequence assigned under r.mu is the canonical per-run event order.
//  2. state.events is the source of truth; the rollout recorder is an ordered
//     mirror that must drain exactly that ledger before a terminal emit returns.
//  3. state.terminated is armed before terminal redaction/fanout so no
//     post-terminal goroutine can append to the sealed forensic record.
//
// Message lifecycle:
//  1. state.messages is the only source of truth for run context.
//  2. execute() works on per-step snapshots only and must re-read via
//     messagesForStep() at step boundaries so CompactRun/setMessages changes win.
//  3. Every exported or persisted message slice is deep-cloned so callers,
//     stores, and subscribers never alias runner-owned ToolCalls.
//
// Payload ownership:
//  1. emit() deep-clones caller payloads before mutation/redaction.
//  2. Stored history, subscribers, and the recorder each receive independent
//     payload copies, so no consumer can mutate another boundary's view.
//  3. Structs with pointer fields must be normalized before entering payloads
//     (for example recordAccounting -> completionUsageToMap).
type usageTotalsAccumulator struct {
	promptTokensTotal     int
	completionTokensTotal int
	totalTokens           int
	lastTurnTokens        int
	cachedPromptTokens    int
	hasCachedPromptTokens bool
	reasoningTokens       int
	hasReasoningTokens    bool
	inputAudioTokens      int
	hasInputAudioTokens   bool
	outputAudioTokens     int
	hasOutputAudioTokens  bool
}

var (
	ErrRunNotFound     = errors.New("run not found")
	ErrNoPendingInput  = errors.New("no pending input")
	ErrInvalidRunInput = errors.New("invalid run input")
	// ErrRunNotCompleted is returned by ContinueRun when the target run has not
	// reached a completed status (e.g. it is still running or has failed).
	ErrRunNotCompleted = errors.New("run is not completed")
	// ErrRunNotActive is returned by SteerRun when the target run is not in an
	// active state (running or waiting for user).
	ErrRunNotActive = errors.New("run is not active")
	// ErrSteeringBufferFull is returned by SteerRun when the run's steering
	// channel is at capacity.
	ErrSteeringBufferFull = errors.New("steering buffer full")
	// ErrConversationAccessDenied is returned by StartRun when the caller
	// supplies a ConversationID that exists but belongs to a different
	// tenant or agent (cross-tenant/cross-agent disclosure prevention).
	ErrConversationAccessDenied = errors.New("conversation access denied")
	// ErrRunnerClosed is returned by StartRun and ContinueRun when Shutdown
	// has already been called on the runner.
	ErrRunnerClosed = errors.New("runner is closed")
)

// steeringBufferSize is the capacity of the per-run steering message channel.
const steeringBufferSize = 10

// recorderChannelSize is the capacity of the per-run recorder goroutine's
// input channel.  256 slots provides headroom for bursty event emission
// (tool-call fans with many parallel results) without unbounded memory growth.
const recorderChannelSize = 256

// recorderDrainTimeout is the maximum time emit() will wait for the recorder
// goroutine to drain and close after the terminal event is sent.  A stalled
// disk or slow filesystem should not hang the run goroutine indefinitely.
const recorderDrainTimeout = 30 * time.Second

// maxEmptyRetries is the maximum number of consecutive empty LLM responses
// (no text content, no tool calls) before the runner stops retrying and
// fails the run explicitly. Handles Gemini 2.5 Flash thinking mode where
// the model returns 0 completion_tokens with empty content.
const maxEmptyRetries = 3

const (
	defaultMaxCompletedRetention    = 32
	defaultMaxConversationRetention = 256
)

// conversationOwner records the tenant and agent that own a conversation.
// This is used to enforce conversation scoping: a caller-supplied ConversationID
// must match the requesting tenant + agent before its history is loaded.
type conversationOwner struct {
	tenantID string
	agentID  string
}

// queuedRun holds a (runID, req) pair waiting for a worker slot.
type queuedRun struct {
	runID string
	req   RunRequest
}

type auditBucket struct {
	writer *audittrail.AuditWriter
}

type Runner struct {
	provider         Provider
	tools            *Registry
	config           RunnerConfig
	providerRegistry *catalog.ProviderRegistry
	activations      *ActivationTracker
	skillConstraints *SkillConstraintTracker
	envInfo          systemprompt.EnvironmentInfo

	mu            sync.RWMutex
	runs          map[string]*runState
	conversations map[string][]Message
	// subscriberMu serializes terminal fanout with subscriber cancellation.
	// Terminal fanout intentionally runs outside mu so slow persistence cannot
	// block run queries, while cancellation may still close subscriber channels.
	subscriberMu      sync.Mutex
	closedSubscribers map[chan Event]struct{}
	// conversationTouched tracks recency for the in-memory conversation mirror.
	// It deliberately mirrors r.conversations only; durable retention is owned
	// by ConversationStore implementations.
	conversationTouched map[string]time.Time
	// conversationOwners maps conversation_id -> owner (tenantID + agentID).
	// It is populated when a run completes and its conversation is saved to the
	// in-memory conversations map. Used to validate caller-supplied conversation IDs.
	conversationOwners map[string]conversationOwner
	// cancelFuncs maps runID → context.CancelFunc for cooperative cancellation.
	// An entry is present while the run's execute() goroutine is active.
	// CancelRun looks up and calls the function to interrupt provider and tool
	// calls. The entry is deleted when execute() returns (via deferred cleanup).
	cancelFuncs sync.Map

	// workerSem is a counting semaphore that bounds concurrent run execution.
	// A non-nil channel means the pool is bounded (WorkerPoolSize > 0). A token
	// is consumed by sending to the channel before execute() starts and released
	// (by the deferred releaseWorker closure) when execute() returns. When the
	// channel is nil the runner operates in the legacy unbounded mode.
	workerSem chan struct{}
	// poolDispatchHook is a test seam invoked after a queued item acquires a
	// worker token and before execute() is launched.
	poolDispatchHook func(queuedRun)
	// runQueue is a FIFO channel of pending (runID, req) pairs waiting for a
	// worker slot. It is only used when workerSem is non-nil.
	runQueue chan queuedRun

	// done is closed by Shutdown to signal poolDispatcher and enqueueRun to stop.
	// It is allocated in NewRunner so it is always non-nil; closing it is the
	// shutdown trigger.
	done chan struct{}
	// shutdownOnce ensures close(done) happens exactly once even if Shutdown is
	// called concurrently from multiple goroutines.
	shutdownOnce sync.Once
	// toolShutdownOnce ensures registry shutdown hooks run at most once.
	toolShutdownOnce sync.Once
	toolShutdownErr  error
	// auditBuckets holds shared audit writers keyed by UTC date (YYYY-MM-DD).
	auditMu           sync.Mutex
	auditBuckets      map[string]*auditBucket
	auditShutdownOnce sync.Once
	auditShutdownErr  error
	// inflight counts goroutines currently inside execute() (or executeWithRelease).
	// Shutdown waits until this reaches zero before returning.
	inflight sync.WaitGroup
}

func NewRunner(provider Provider, tools *Registry, config RunnerConfig) *Runner {
	if config.DefaultModel == "" {
		config.DefaultModel = "gpt-4.1-mini"
	}
	if config.DefaultAgentIntent == "" {
		config.DefaultAgentIntent = "general"
	}
	// MaxSteps <= 0 means unlimited; no default cap is applied here.
	if config.AskUserTimeout <= 0 {
		config.AskUserTimeout = 5 * time.Minute
	}
	if config.HookFailureMode == "" {
		config.HookFailureMode = HookFailureModeFailClosed
	}
	if config.AutoCompactMode == "" {
		config.AutoCompactMode = "hybrid"
	}
	if config.AutoCompactThreshold == 0 {
		config.AutoCompactThreshold = 0.80
	}
	if config.AutoCompactKeepLast <= 0 {
		config.AutoCompactKeepLast = 8
	}
	if config.ModelContextWindow == 0 {
		config.ModelContextWindow = 128000
	}
	if config.MaxCompletedRetention <= 0 {
		config.MaxCompletedRetention = defaultMaxCompletedRetention
	}
	if config.MaxConversationRetention <= 0 {
		config.MaxConversationRetention = defaultMaxConversationRetention
	}
	if tools == nil {
		tools = NewRegistry()
	}
	activations := config.Activations
	if activations == nil {
		activations = NewActivationTracker()
	}
	skillConstraints := config.SkillConstraints
	if skillConstraints == nil {
		skillConstraints = NewSkillConstraintTracker()
	}
	envInfo := systemprompt.EnvironmentInfo{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Shell:     os.Getenv("SHELL"),
	}
	if h, err := os.Hostname(); err == nil {
		envInfo.Hostname = h
	}
	if u, err := osuser.Current(); err == nil {
		envInfo.Username = u.Username
	}
	if wd, err := os.Getwd(); err == nil {
		envInfo.WorkingDir = wd
	}

	r := &Runner{
		provider:            provider,
		tools:               tools,
		config:              config,
		providerRegistry:    config.ProviderRegistry,
		activations:         activations,
		skillConstraints:    skillConstraints,
		envInfo:             envInfo,
		runs:                make(map[string]*runState),
		conversations:       make(map[string][]Message),
		closedSubscribers:   make(map[chan Event]struct{}),
		conversationTouched: make(map[string]time.Time),
		conversationOwners:  make(map[string]conversationOwner),
		auditBuckets:        make(map[string]*auditBucket),
		done:                make(chan struct{}),
	}
	if config.WorkerPoolSize > 0 {
		// Bounded pool: workerSem acts as a counting semaphore.
		// The capacity of runQueue is generous but bounded to prevent
		// unbounded memory growth when callers fire many runs quickly.
		// 4096 slots is far larger than any reasonable burst.
		r.workerSem = make(chan struct{}, config.WorkerPoolSize)
		r.runQueue = make(chan queuedRun, 4096)
		go r.poolDispatcher()
	}
	return r
}

type retainedRunCandidate struct {
	id        string
	updatedAt time.Time
}

func (r *Runner) pruneCompletedRuns() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneCompletedRunsLocked()
}

func (r *Runner) pruneCompletedRunsLocked() {
	if r.config.Store == nil {
		return
	}

	limit := r.config.MaxCompletedRetention
	if limit <= 0 {
		limit = defaultMaxCompletedRetention
	}

	terminalCount := 0
	candidates := make([]retainedRunCandidate, 0)
	for runID, state := range r.runs {
		if state == nil || !isTerminalRunStatus(state.run.Status) {
			continue
		}
		terminalCount++
		if len(state.subscribers) == 0 {
			candidates = append(candidates, retainedRunCandidate{
				id:        runID,
				updatedAt: state.run.UpdatedAt,
			})
		}
	}
	if terminalCount <= limit || len(candidates) == 0 {
		return
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].updatedAt.Equal(candidates[j].updatedAt) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].updatedAt.Before(candidates[j].updatedAt)
	})

	toDelete := terminalCount - limit
	if toDelete > len(candidates) {
		toDelete = len(candidates)
	}
	for i := 0; i < toDelete; i++ {
		delete(r.runs, candidates[i].id)
	}
}

type retainedConversationCandidate struct {
	id        string
	touchedAt time.Time
}

func (r *Runner) pruneConversationMirror() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneConversationMirrorLocked()
}

func (r *Runner) pruneConversationMirrorLocked() {
	limit := r.config.MaxConversationRetention
	if limit <= 0 {
		limit = defaultMaxConversationRetention
	}
	if len(r.conversations) <= limit {
		return
	}

	candidates := make([]retainedConversationCandidate, 0, len(r.conversations))
	for convID := range r.conversations {
		touchedAt := r.conversationTouched[convID]
		candidates = append(candidates, retainedConversationCandidate{
			id:        convID,
			touchedAt: touchedAt,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].touchedAt.Equal(candidates[j].touchedAt) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].touchedAt.Before(candidates[j].touchedAt)
	})

	toDelete := len(r.conversations) - limit
	for i := 0; i < toDelete; i++ {
		convID := candidates[i].id
		delete(r.conversations, convID)
		delete(r.conversationOwners, convID)
		delete(r.conversationTouched, convID)
	}
}

func isTerminalRunStatus(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled
}

// GetProviderRegistry returns the provider registry, if configured.
func (r *Runner) GetProviderRegistry() *catalog.ProviderRegistry {
	return r.providerRegistry
}

// toolsForRun returns the tool registry that should be used for this run.
// When a per-run workspace was provisioned (workspace_type != ""), a fresh
// registry rooted at the provisioned path is built and stashed on runState
// — this is what file/shell tools should consult so paths resolve against
// the worktree (or container/vm path) instead of the harnessd's startup
// workspace. When no provisioning happened, the global Runner.tools registry
// is returned unchanged.
//
// Callers must treat the returned *Registry as read-only with respect to
// global registration; it is owned by the runState (or by Runner for the
// fallback path) and should not be mutated except via the registered MCP
// flow which handles its own concurrency.
func (r *Runner) toolsForRun(runID string) *Registry {
	r.mu.RLock()
	st, ok := r.runs[runID]
	r.mu.RUnlock()
	if ok && st != nil && st.perRunTools != nil {
		return st.perRunTools
	}
	return r.tools
}

// poolDispatcher is the long-running goroutine that drains runQueue and
// dispatches work as worker slots become available. It exits when r.done is
// closed (via Shutdown) or when runQueue delivers its zero value with ok==false
// (defensive; runQueue is never closed in practice — use r.done to stop).
//
// Each iteration acquires a worker slot (workerSem), reads the next item from
// runQueue, and starts execute() in a goroutine. execute() defers
// releaseWorker, which returns the token when it finishes.
//
// On shutdown, poolDispatcher drains any items that were enqueued into the
// buffered runQueue after r.done was closed (the enqueueRun select is not
// atomic, so a small number of items may race in). Each such item had
// r.inflight.Add(1) called by dispatchRun before enqueue, so we must call
// r.inflight.Done() once per drained item to allow Shutdown's Wait to complete.
func (r *Runner) poolDispatcher() {
	for {
		if !r.poolDispatcherStep() {
			return
		}
	}
}

func (r *Runner) poolDispatcherStep() (keepGoing bool) {
	keepGoing = true
	var item queuedRun
	haveItem := false
	acquiredWorker := false
	defer func() {
		if p := recover(); p != nil {
			if acquiredWorker {
				<-r.workerSem
			}
			if haveItem {
				r.failRun(item.runID, fmt.Errorf("pool dispatcher panic: %v", p))
				r.inflight.Done()
			}
			if r.config.Logger != nil {
				r.config.Logger.Error("runner: recovered panic in pool dispatcher",
					"run_id", item.runID,
					"panic", p,
				)
			}
			keepGoing = true
		}
	}()

	select {
	case <-r.done:
		// Drain any items that raced into the buffer after done was closed.
		// Each was counted in r.inflight by dispatchRun; account for them now.
		for {
			select {
			case _, ok := <-r.runQueue:
				if !ok {
					return false
				}
				r.inflight.Done()
			default:
				return false
			}
		}
	case queued, ok := <-r.runQueue:
		if !ok {
			return false
		}
		item = queued
		haveItem = true
		// Acquire a worker slot. This blocks when all slots are occupied,
		// naturally serializing pending items until a slot frees up.
		// Check done again while waiting for a slot so Shutdown can
		// interrupt a poolDispatcher that is blocked on a full semaphore.
		select {
		case <-r.done:
			// Also drain here: we dequeued one item and are about to exit.
			r.inflight.Done()
			for {
				select {
				case _, ok := <-r.runQueue:
					if !ok {
						return false
					}
					r.inflight.Done()
				default:
					return false
				}
			}
		case r.workerSem <- struct{}{}:
			acquiredWorker = true
		}
		if r.poolDispatchHook != nil {
			r.poolDispatchHook(item)
		}
		// Mark transition from queued → running before launching the goroutine
		// so that the status is accurate by the time the caller's goroutine
		// observes it.
		go r.executeWithRelease(item.runID, item.req)
		return true
	}
}

// executeWithRelease wraps execute() to return the worker slot on exit and
// to decrement r.inflight (which was incremented by the caller before launch).
func (r *Runner) executeWithRelease(runID string, req RunRequest) {
	defer r.inflight.Done()
	defer func() { <-r.workerSem }()
	r.execute(runID, req)
}

// enqueueRun places a run in the queue and emits run.queued.
// Called from dispatchRun when the pool is bounded and no slot is immediately
// available. Returns ErrRunnerClosed if Shutdown has been called.
//
// The non-blocking done check before the send narrows (but cannot fully
// eliminate) the window where a send races with Shutdown. The load-bearing
// fix for the residual race is poolDispatcher's drain-on-exit logic, which
// accounts for any items that slip through.
func (r *Runner) enqueueRun(runID string, req RunRequest) error {
	// Prioritized fast-path: if done is already closed, reject immediately
	// before emitting run.queued or touching the buffered channel.
	select {
	case <-r.done:
		return ErrRunnerClosed
	default:
	}
	r.emit(runID, EventRunQueued, map[string]any{"prompt": req.Prompt})
	select {
	case <-r.done:
		return ErrRunnerClosed
	case r.runQueue <- queuedRun{runID: runID, req: req}:
		return nil
	}
}

// dispatchRun either launches execute() directly (unbounded mode) or enqueues
// the run for the pool dispatcher (bounded mode). It is the single call site
// that replaces the bare "go r.execute(...)" pattern.
// Returns ErrRunnerClosed if Shutdown has been called.
func (r *Runner) dispatchRun(runID string, req RunRequest) error {
	// Reject immediately if the runner has been shut down.
	select {
	case <-r.done:
		return ErrRunnerClosed
	default:
	}

	if r.workerSem == nil {
		// Unbounded mode: legacy behaviour — start immediately.
		r.inflight.Add(1)
		go func() {
			defer r.inflight.Done()
			r.execute(runID, req)
		}()
		return nil
	}
	// Bounded mode: try to acquire a slot without blocking.
	select {
	case <-r.done:
		return ErrRunnerClosed
	case r.workerSem <- struct{}{}:
		// Slot available — start immediately without going through the queue.
		r.inflight.Add(1)
		go r.executeWithRelease(runID, req)
		return nil
	default:
		// No slot available — enqueue and let poolDispatcher pick it up.
		// inflight is incremented here; executeWithRelease will decrement it.
		r.inflight.Add(1)
		if err := r.enqueueRun(runID, req); err != nil {
			r.inflight.Done()
			return err
		}
		return nil
	}
}

func (r *Runner) StartRun(req RunRequest) (Run, error) {
	// Fast path: reject immediately if the runner has been shut down.
	select {
	case <-r.done:
		return Run{}, ErrRunnerClosed
	default:
	}

	if r.provider == nil {
		return Run{}, fmt.Errorf("provider is required")
	}
	if req.Prompt == "" {
		return Run{}, fmt.Errorf("prompt is required")
	}
	if req.MaxSteps < 0 {
		return Run{}, fmt.Errorf("max_steps must be >= 0 (0 means use runner default)")
	}
	if req.MaxTurns < 0 {
		return Run{}, fmt.Errorf("max_turns must be >= 0 (0 means unlimited)")
	}
	if req.MaxCostUSD < 0 {
		return Run{}, fmt.Errorf("max_cost_usd must be >= 0 (0 means unlimited)")
	}
	if req.Permissions != nil {
		if err := ValidatePermissionConfig(*req.Permissions); err != nil {
			return Run{}, fmt.Errorf("invalid permissions: %w", err)
		}
	}
	if err := ValidatePermissionRules(req.Rules); err != nil {
		return Run{}, fmt.Errorf("invalid rules: %w", err)
	}
	if len(req.MCPServers) > 0 {
		if err := validateMCPServerConfigs(req.MCPServers); err != nil {
			return Run{}, fmt.Errorf("invalid mcp_servers: %w", err)
		}
	}

	// Bounds validation for DynamicRules to prevent memory exhaustion attacks.
	const (
		maxDynamicRules       = 50
		maxDynamicRuleContent = 64 * 1024 // 64KB per rule
	)
	if len(req.DynamicRules) > maxDynamicRules {
		return Run{}, fmt.Errorf("too many dynamic rules: %d exceeds limit of %d", len(req.DynamicRules), maxDynamicRules)
	}
	for i, rule := range req.DynamicRules {
		if len(rule.Content) > maxDynamicRuleContent {
			return Run{}, fmt.Errorf("dynamic rule %d content too large: %d bytes exceeds limit of %d", i, len(rule.Content), maxDynamicRuleContent)
		}
	}

	// Validate workspace_type early to fail fast before any state is created.
	// Beyond the name check, also enforce the deterministic provisioning
	// preconditions (registered backend, worktree repo path) so an HTTP caller
	// gets a synchronous 400 instead of a queued run that dies in provisioning.
	if req.WorkspaceType != "" {
		if err := validateWorkspaceType(req.WorkspaceType); err != nil {
			return Run{}, err
		}
		if err := validateWorkspaceProvisionPreconditions(req.WorkspaceType, r.config.WorkspaceBaseOptions); err != nil {
			return Run{}, err
		}
	}

	model := req.Model
	if model == "" {
		model = r.config.DefaultModel
	}
	// Use RepoPath as the initial workspace path so that AGENTS.md from the
	// base repository is loaded for all runs. For workspace-type runs, the
	// system prompt is re-resolved after provisioning with the actual workspace
	// path (see execute()).
	systemPrompt, resolvedPrompt, err := r.resolveSystemPrompt(req, model, r.config.WorkspaceBaseOptions.RepoPath)
	if err != nil {
		return Run{}, err
	}

	now := time.Now().UTC()
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "default"
	}
	run := Run{
		ID:                   r.nextID("run"),
		Prompt:               req.Prompt,
		Model:                model,
		Status:               RunStatusQueued,
		UsageTotals:          &RunUsageTotals{},
		CostTotals:           &RunCostTotals{CostStatus: CostStatusPending},
		TenantID:             tenantID,
		AgentID:              agentID,
		ParentContextHandoff: req.ParentContextHandoff,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	run.ConversationID = strings.TrimSpace(req.ConversationID)
	if run.ConversationID == "" {
		run.ConversationID = run.ID
	}

	// Validate caller-supplied ConversationID against tenant/agent ownership.
	// Only applies when the caller explicitly passed a ConversationID (as
	// opposed to the auto-assigned case where run.ConversationID == run.ID).
	if strings.TrimSpace(req.ConversationID) != "" {
		if err := r.checkConversationOwnership(run.ConversationID, tenantID, agentID); err != nil {
			return Run{}, err
		}
	}

	// Create rollout recorder before acquiring the run lock so that any
	// filesystem error is surfaced at start time rather than mid-run.
	var rec *rollout.Recorder
	if r.config.RolloutDir != "" {
		var recErr error
		rec, recErr = rollout.NewRecorder(rollout.RecorderConfig{
			Dir:   r.config.RolloutDir,
			RunID: run.ID,
		})
		if recErr != nil && r.config.Logger != nil {
			r.config.Logger.Error("rollout recorder: failed to create", "run_id", run.ID, "error", recErr)
		}
	}

	// Attach the shared audit writer for today's UTC date when enabled.
	// The audit log is written to <RolloutDir>/<YYYY-MM-DD>/audit.jsonl, a single
	// shared file (not per-run) since it captures all runs in the session.
	var aw *audittrail.AuditWriter
	if r.config.AuditTrailEnabled && r.config.RolloutDir != "" {
		var awErr error
		aw, awErr = r.auditWriterFor(time.Now().UTC())
		if awErr != nil && r.config.Logger != nil {
			r.config.Logger.Error("audit trail: failed to create writer", "run_id", run.ID, "error", awErr)
		}
	}

	// Resolve effective permissions: use request value or fall back to default.
	effectivePerms := DefaultPermissionConfig()
	if req.Permissions != nil {
		effectivePerms = normalizePermissionConfig(*req.Permissions)
	}
	mergedPermissionRules := append(copyPermissionRules(permissionRulesFromSet(effectivePerms.Rules)), req.Rules...)
	effectivePerms.Rules = NewPermissionRuleSet(mergedPermissionRules)

	var sb *errorchain.SnapshotBuilder
	if r.config.ErrorChainEnabled {
		sb = errorchain.NewSnapshotBuilder(r.config.ErrorContextDepth)
	}
	// Merge runner-level dynamic rules with per-run dynamic rules.
	// Runner-level rules come first; per-run rules are appended.
	mergedRules := mergeDynamicRules(r.config.DynamicRules, req.DynamicRules)

	state := &runState{
		run:                     run,
		staticSystemPrompt:      systemPrompt,
		promptResolved:          resolvedPrompt,
		usageTotals:             usageTotalsAccumulator{},
		costTotals:              RunCostTotals{CostStatus: CostStatusPending},
		messages:                make([]Message, 0, 16),
		events:                  make([]Event, 0, 32),
		subscribers:             make(map[chan Event]struct{}),
		steeringCh:              make(chan string, steeringBufferSize),
		maxCostUSD:              req.MaxCostUSD,
		allowedTools:            req.AllowedTools,
		permissions:             effectivePerms,
		permissionWorkspaceRoot: r.defaultPermissionWorkspaceRoot(),
		snapshotBuilder:         sb,
		auditWriter:             aw,
		profileName:             req.ProfileName,
		dynamicRules:            mergedRules,
		firedOnceRules:          make(map[string]bool),
		forkDepth:               req.ForkDepth,
	}
	if rec != nil {
		startRecorderGoroutine(state, rec)
	}
	r.mu.Lock()
	r.runs[run.ID] = state
	r.mu.Unlock()

	// Persist the initial run record to the configured store (non-fatal).
	r.storeCreateRun(run)

	if err := r.dispatchRun(run.ID, req); err != nil {
		// Runner was shut down between the early check and here.  Remove the
		// half-created run state so callers never see an orphan run.
		// Before deleting, clean up any resources that were attached to the
		// half-created state. If we skip this, the recorder goroutine blocks
		// forever on its channel.
		if state.closeRecorderOnce != nil {
			state.closeRecorderOnce()
		}
		r.mu.Lock()
		// Zero out auditWriter so that closeAuditWriter on a concurrent path is
		// a no-op. The shared date-bucket writer is closed by Runner.Shutdown.
		if s, ok := r.runs[run.ID]; ok {
			s.auditWriter = nil
		}
		delete(r.runs, run.ID)
		r.mu.Unlock()
		return Run{}, err
	}

	return run, nil
}

// checkConversationOwnership validates that a caller-supplied ConversationID
// belongs to the requesting tenant + agent before its history is loaded.
//
// The check is two-phase:
//  1. In-memory: if r.conversationOwners has an entry for convID, both
//     tenantID and agentID must match (strict check, both axes enforced).
//  2. Persistent store: if not found in memory but a ConversationStore is
//     configured, the store's tenant_id column is checked (agent_id is not
//     stored in the schema, so only the tenant axis is enforced here).
//
// Returns nil if the conversation does not exist yet (new conversation
// allowed), or if the caller matches the recorded owner.
// Returns ErrConversationAccessDenied if a mismatch is detected.
//
// tenantID normalization: the runner normalises "" → "default" on input, and
// the SQLite layer stores "" for "default" tenant rows. Both sides are
// normalised before comparison so "default" and "" compare equal.
func (r *Runner) checkConversationOwnership(convID, tenantID, agentID string) error {
	// Normalise: "" and "default" are the same tenant value.
	normTenant := func(t string) string {
		if t == "" {
			return "default"
		}
		return t
	}

	callerTenant := normTenant(tenantID)
	callerAgent := agentID

	// Phase 1: in-memory map (strongest check — tenant + agent both enforced).
	r.mu.RLock()
	owner, found := r.conversationOwners[convID]
	r.mu.RUnlock()

	if found {
		if normTenant(owner.tenantID) != callerTenant || owner.agentID != callerAgent {
			return ErrConversationAccessDenied
		}
		return nil
	}

	// Phase 2: persistent store (tenant-only check — schema has no agent_id).
	if r.config.ConversationStore == nil {
		// No store configured and not in memory — brand-new conversation, allow.
		return nil
	}
	conv, err := r.config.ConversationStore.GetConversationOwner(context.Background(), convID)
	if err != nil {
		// Treat store errors as a hard failure to prevent silent bypass.
		return fmt.Errorf("conversation ownership check: %w", err)
	}
	if conv == nil {
		// Not found in store either — brand-new conversation, allow.
		return nil
	}
	// Found in store: check tenant match only (no agent_id column in schema).
	storedTenant := normTenant(conv.TenantID)
	if storedTenant != callerTenant {
		return ErrConversationAccessDenied
	}
	return nil
}

func (r *Runner) resolveSystemPrompt(req RunRequest, model, workspacePath string) (string, *systemprompt.ResolvedPrompt, error) {
	if strings.TrimSpace(req.SystemPrompt) != "" {
		return req.SystemPrompt, nil, nil
	}
	if r.config.PromptEngine == nil {
		return r.config.DefaultSystemPrompt, nil, nil
	}
	extensions := mapPromptExtensions(req.PromptExtensions)
	resolved, err := r.config.PromptEngine.Resolve(systemprompt.ResolveRequest{
		Model:              model,
		AgentIntent:        req.AgentIntent,
		DefaultAgentIntent: r.config.DefaultAgentIntent,
		PromptProfile:      req.PromptProfile,
		TaskContext:        req.TaskContext,
		Extensions:         extensions,
		WorkspacePath:      workspacePath,
	})
	if err != nil {
		return "", nil, err
	}
	return resolved.StaticPrompt, &resolved, nil
}

// providerCandidate is a resolved provider that may be attempted during a run.
// Index 0 is always the primary (what resolveProvider returns today); indices
// 1..n are fallback candidates populated when AllowFallback is true.
type providerCandidate struct {
	Provider Provider
	Name     string
}

type runPreflightResult struct {
	model          string
	primaryModel   string
	activeProvider Provider
	providerName   string
	// providerCandidates is the ordered list of providers to attempt for each
	// LLM turn.  Index 0 is the primary (identical to activeProvider/providerName).
	// Subsequent entries are fallbacks, populated only when AllowFallback is true.
	providerCandidates     []providerCandidate
	systemPrompt           string
	resolvedPrompt         *systemprompt.ResolvedPrompt
	runStartedAt           time.Time
	messages               []Message
	effectiveWorkspaceType string
}

func (r *Runner) runPreflight(ctx context.Context, runID string, req RunRequest) (*runPreflightResult, error) {
	// Per-run workspace provisioning (issue #324, extended by issue #414).
	// Resolve the effective workspace type: RunRequest.WorkspaceType takes
	// precedence; if unset, Profile.IsolationMode provides the fallback.
	// Load the full profile now (if a profile is named) so we can extract
	// IsolationMode. Profile loading errors are non-fatal for this field —
	// a missing or unparseable profile falls back to no provisioning rather
	// than failing the run at this early stage (the MCP loading path below
	// already handles the authoritative profile error reporting).
	var resolvedProfile *profiles.Profile
	if req.ProfileName != "" {
		profilesDir := r.config.ProfilesDir
		if profilesDir == "" {
			profilesDir = defaultProfilesDir()
		}
		if p, loadErr := profiles.LoadProfileFromUserDir(req.ProfileName, profilesDir); loadErr == nil && p != nil {
			resolvedProfile = p
		}
	}
	effectiveWorkspaceType := resolveWorkspaceType(req.WorkspaceType, resolvedProfile)

	// If workspace_type is set (explicitly or via profile), provision it now
	// and register cleanup in runState.
	// The cleanup function is called by runWorkspaceCleanup() before each terminal
	// event, ensuring workspace.destroyed is emitted before run.completed/failed.
	if effectiveWorkspaceType != "" {
		ws, provisionErr := provisionRunWorkspace(ctx, runID, effectiveWorkspaceType, r.config.WorkspaceBaseOptions)
		if provisionErr != nil {
			r.emit(runID, EventWorkspaceProvisionFailed, map[string]any{
				"workspace_type": effectiveWorkspaceType,
				"error":          provisionErr.Error(),
			})
			return nil, fmt.Errorf("workspace provisioning failed: %w", provisionErr)
		}
		wsPath := ws.WorkspacePath()
		r.emit(runID, EventWorkspaceProvisioned, map[string]any{
			"workspace_type": effectiveWorkspaceType,
			"workspace_path": wsPath,
		})
		// Re-resolve system prompt with the provisioned workspace path so that
		// AGENTS.md from the workspace is injected (overriding any AGENTS.md
		// loaded from WorkspaceBaseOptions.RepoPath during StartRun). This is a
		// no-op when the workspace path matches the repo path.
		wsModel := req.Model
		if wsModel == "" {
			wsModel = r.config.DefaultModel
		}
		if wsPath != "" && r.config.PromptEngine != nil {
			if wsSP, wsRP, wsErr := r.resolveSystemPrompt(req, wsModel, wsPath); wsErr == nil {
				r.mu.Lock()
				if st, ok := r.runs[runID]; ok {
					st.staticSystemPrompt = wsSP
					st.promptResolved = wsRP
				}
				r.mu.Unlock()
			} else if r.config.Logger != nil {
				r.config.Logger.Error("failed to re-resolve system prompt with workspace path",
					"run_id", runID, "workspace_path", wsPath, "error", wsErr)
			}
		}
		// Store cleanup function so completeRun/failRun/cancelledRun can call it
		// before the terminal event, keeping workspace.destroyed before run.completed.
		wsType := effectiveWorkspaceType
		logger := r.config.Logger
		cleanupFn := func() {
			destroyErr := ws.Destroy(context.Background())
			payload := map[string]any{
				"workspace_type": wsType,
				"workspace_path": wsPath,
			}
			if destroyErr != nil {
				payload["error"] = destroyErr.Error()
				if logger != nil {
					logger.Error("workspace destroy failed", "run_id", runID, "error", destroyErr)
				}
			}
			r.emit(runID, EventWorkspaceDestroyed, payload)
		}
		r.mu.Lock()
		if st, ok := r.runs[runID]; ok {
			st.workspaceCleanup = cleanupFn
		}
		r.mu.Unlock()

		// Build a per-run tool registry rooted at the provisioned workspace path
		// so that file/shell tools resolve relative paths against the worktree
		// (or container/vm path) instead of the harnessd's startup workspace.
		// Without this, the workspace.provisioned event is cosmetic — only AGENTS.md
		// loading respected the new path.
		if wsPath != "" && effectiveWorkspaceType != "vm" {
			perRun := NewDefaultRegistryWithOptions(wsPath, r.config.BaseRegistryOptions)
			r.mu.Lock()
			if st, ok := r.runs[runID]; ok {
				st.perRunTools = perRun
				st.permissionWorkspaceRoot = wsPath
			}
			r.mu.Unlock()
		}
		if wsPath != "" && effectiveWorkspaceType == "vm" {
			r.emit(runID, EventPromptWarning, map[string]any{
				"code":    "vm_workspace_tool_routing",
				"message": fmt.Sprintf("VM workspace detected: tool execution runs on host, not inside the guest VM. Filesystem tools (write, edit, bash) operate on the host workspace. Full VM tool routing is tracked in issue #564."),
			})
		}
	}

	model := req.Model
	if model == "" {
		model = r.config.DefaultModel
	}

	// Canonicalize the model for the target provider: when a non-OpenRouter
	// provider is explicitly requested, strip any OpenRouter-qualified prefix
	// from the model slug (e.g. "deepseek/deepseek-v4-flash" -> "deepseek-v4-flash").
	if req.ProviderName != "" && !strings.EqualFold(req.ProviderName, "openrouter") && r.providerRegistry != nil {
		canonical := r.providerRegistry.CanonicalModelForProvider(model, req.ProviderName)
		if canonical != model {
			r.mu.Lock()
			if state, ok := r.runs[runID]; ok {
				state.run.Model = canonical
			}
			r.mu.Unlock()
			model = canonical
		}
	}

	// Resolve per-role model overrides. primaryModel is used in CompletionRequests
	// for the main step loop. An empty Primary falls back to the base model.
	roleModels := r.resolveRoleModels(req)
	primaryModel := model
	if roleModels.Primary != "" {
		primaryModel = roleModels.Primary
	}

	candidates, err := r.resolveProviderCandidates(runID, model, req.ProviderName, req.AllowFallback, req.FallbackProviders)
	if err != nil {
		return nil, err
	}
	activeProvider := candidates[0].Provider
	providerName := candidates[0].Name

	// Set provider name and resolved role models on run state.
	// resolvedRoleModels is stored so that autoCompactMessages can honour the
	// per-request Summarizer override without needing the original RunRequest.
	r.mu.Lock()
	if state, ok := r.runs[runID]; ok {
		state.run.ProviderName = providerName
		state.resolvedRoleModels = roleModels
	}
	r.mu.Unlock()

	r.emit(runID, EventProviderResolved, map[string]any{
		"model":    model,
		"provider": providerName,
	})

	systemPrompt, resolvedPrompt, runStartedAt := r.promptContext(runID)
	if resolvedPrompt != nil {
		r.emit(runID, EventPromptResolved, map[string]any{
			"intent":            resolvedPrompt.ResolvedIntent,
			"model_profile":     resolvedPrompt.ResolvedModelProfile,
			"model_fallback":    resolvedPrompt.ModelFallback,
			"applied_behaviors": append([]string(nil), resolvedPrompt.Behaviors...),
			"applied_talents":   append([]string(nil), resolvedPrompt.Talents...),
			"applied_skills":    append([]string(nil), resolvedPrompt.Skills...),
			"has_warnings":      len(resolvedPrompt.Warnings) > 0,
		})
		for _, warning := range resolvedPrompt.Warnings {
			r.emit(runID, EventPromptWarning, map[string]any{
				"code":    warning.Code,
				"message": warning.Message,
			})
		}
	}

	priorMessages := r.loadConversationHistory(runID)
	r.mu.RLock()
	continuationPolicyNotice := ""
	if state, ok := r.runs[runID]; ok {
		continuationPolicyNotice = strings.TrimSpace(state.continuationPolicyNotice)
	}
	r.mu.RUnlock()
	if continuationPolicyNotice != "" {
		priorMessages = append(priorMessages, Message{
			Role:    "system",
			Content: continuationPolicyNotice,
			IsMeta:  true,
		})
	}
	messages := make([]Message, 0, len(priorMessages)+16)
	messages = append(messages, priorMessages...)
	messages = append(messages, Message{Role: "user", Content: req.Prompt})
	r.snapshotRecordMessage(runID, "user", req.Prompt)

	if len(priorMessages) > 0 {
		r.emit(runID, EventConversationContinued, map[string]any{
			"conversation_id":     r.conversationID(runID),
			"prior_message_count": len(priorMessages),
		})
	}
	r.setMessages(runID, messages)

	// Build per-run MCP registry when profile and/or run-level MCP servers are configured.
	// Profile servers shadow global server names (no error); run-level servers error on collision.
	if req.ProfileName != "" || len(req.MCPServers) > 0 {
		var profileMCPServers []MCPServerConfig

		if req.ProfileName != "" {
			// Resolve the profiles directory: use runner config value or fall back to default.
			profilesDir := r.config.ProfilesDir
			if profilesDir == "" {
				profilesDir = defaultProfilesDir()
			}
			profileCfg, profileErr := loadProfileMCPServers(profilesDir, req.ProfileName)
			if profileErr != nil {
				// Non-fatal: log and continue without profile servers.
				if r.config.Logger != nil {
					r.config.Logger.Error("failed to load profile MCP servers",
						"run_id", runID,
						"profile", req.ProfileName,
						"error", profileErr)
				}
			} else {
				for name, srv := range profileCfg {
					// Harness MCPServerConfig infers transport from Command/URL;
					// the config.MCPServerConfig Transport field is not forwarded here.
					profileMCPServers = append(profileMCPServers, MCPServerConfig{
						Name:    name,
						Command: srv.Command,
						Args:    srv.Args,
						URL:     srv.URL,
					})
				}
			}
		}

		scopedReg, mcpErr := buildPerRunMCPRegistry(
			r.config.GlobalMCPRegistry,
			r.config.GlobalMCPServerNames,
			profileMCPServers,
			req.MCPServers,
		)
		if mcpErr != nil {
			return nil, fmt.Errorf("build per-run MCP registry: %w", mcpErr)
		}
		r.mu.Lock()
		storedReg := false
		if state, ok := r.runs[runID]; ok {
			state.scopedMCPRegistry = scopedReg
			storedReg = true
		} else {
			// Run was cancelled before we could store the registry; clean up.
			_ = scopedReg.Close()
		}
		r.mu.Unlock()

		// Register per-run MCP tools into the global tool registry so that the
		// agent can discover and call them. We iterate over each server in the
		// scoped registry and register its tools individually so that the correct
		// server name is used as the tool name prefix.
		if storedReg {
			byServer, listErr := scopedReg.ListPerRunTools(ctx)
			if listErr != nil {
				if r.config.Logger != nil {
					r.config.Logger.Error("failed to list per-run MCP tools for registration",
						"run_id", runID,
						"error", listErr)
				}
			} else {
				for serverName, toolDefs := range byServer {
					// Register MCP tools onto the per-run registry when a per-run
					// workspace was provisioned, so the agent sees them in the same
					// registry that holds the workspace-rooted file/shell tools.
					// Otherwise register onto the global registry (the legacy path).
					registered, regErr := r.toolsForRun(runID).RegisterMCPTools(serverName, toolDefs, scopedReg)
					if regErr != nil {
						// "already connected" is expected when a global server is shadowed
						// by a profile server — log at warn level but do not fail the run.
						if r.config.Logger != nil {
							r.config.Logger.Error("failed to register per-run MCP tools",
								"run_id", runID,
								"server", serverName,
								"error", regErr)
						}
					} else if len(registered) > 0 {
						// Activate the newly registered tools for this run so they
						// appear in DefinitionsForRun (deferred tools are only visible
						// when activated).
						r.activations.Activate(runID, registered...)
					}
				}
			}
		}
	}

	return &runPreflightResult{
		model:                  model,
		primaryModel:           primaryModel,
		activeProvider:         activeProvider,
		providerName:           providerName,
		providerCandidates:     candidates,
		systemPrompt:           systemPrompt,
		resolvedPrompt:         resolvedPrompt,
		runStartedAt:           runStartedAt,
		messages:               messages,
		effectiveWorkspaceType: effectiveWorkspaceType,
	}, nil
}

func (r *Runner) runStepEngine(ctx context.Context, runID string, req RunRequest, preflight *runPreflightResult, effectiveMaxSteps int, effectiveMaxTurns int, runForkDepth int, effectiveApprovalPolicy ApprovalPolicy, effectiveSandboxScope htools.SandboxScope) {
	newStepEngine(r, ctx, runID, req, preflight, effectiveMaxSteps, effectiveMaxTurns, runForkDepth, effectiveApprovalPolicy, effectiveSandboxScope).run()
}

func mapPromptExtensions(input *PromptExtensions) systemprompt.Extensions {
	if input == nil {
		return systemprompt.Extensions{}
	}
	return systemprompt.Extensions{
		Behaviors: append([]string(nil), input.Behaviors...),
		Talents:   append([]string(nil), input.Talents...),
		Skills:    append([]string(nil), input.Skills...),
		Custom:    input.Custom,
	}
}

func (r *Runner) GetRun(runID string) (Run, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.runs[runID]
	if !ok {
		return Run{}, false
	}
	out := state.run
	if state.run.UsageTotals != nil {
		usage := *state.run.UsageTotals
		out.UsageTotals = &usage
	}
	if state.run.CostTotals != nil {
		cost := *state.run.CostTotals
		out.CostTotals = &cost
	}
	if state.run.Recap != nil {
		out.Recap = cloneWorkflowRecap(state.run.Recap)
	}
	return out, true
}

// ContinueRun appends a follow-up user message to a completed run and starts a
// new execution under the same conversation_id. The original run state is kept
// intact. The new run shares the conversation history so the LLM sees the full
// transcript.
//
// Errors:
//   - ErrRunNotFound     — the source run does not exist.
//   - ErrRunNotCompleted — the source run has not reached RunStatusCompleted
//     (it is still running, queued, waiting for user, or has failed).
//   - validation error   — message is empty.
//
// The method is safe for concurrent use. Only one goroutine can successfully
// continue a given completed run: the first to acquire the lock transitions
// the source run's status away from RunStatusCompleted, so subsequent callers
// see ErrRunNotCompleted and fail.
func (r *Runner) ContinueRun(runID, message string) (Run, error) {
	return r.ContinueRunWithOptions(runID, ContinueRunRequest{Prompt: message})
}

// ContinueRunWithOptions creates a new run in the same conversation as a
// completed source run, optionally overriding the source run's tool and
// permission policy for the continuation.
func (r *Runner) ContinueRunWithOptions(runID string, req ContinueRunRequest) (Run, error) {
	// Fast path: reject immediately if the runner has been shut down.
	// This prevents mutating the source run's state (state.continued) when
	// the runner is already closed and dispatch would always fail.
	select {
	case <-r.done:
		return Run{}, ErrRunnerClosed
	default:
	}

	if strings.TrimSpace(req.Prompt) == "" {
		return Run{}, fmt.Errorf("message is required")
	}
	if req.Permissions != nil {
		if err := ValidatePermissionConfig(*req.Permissions); err != nil {
			return Run{}, fmt.Errorf("invalid permissions: %w", err)
		}
	}

	// Atomically check that the run exists and is completed, then immediately
	// stamp it with RunStatusRunning to prevent any other goroutine from also
	// starting a continuation. All snapshot values are read under the same
	// lock so we never release it between check and mutation.
	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return Run{}, ErrRunNotFound
	}
	if state.run.Status != RunStatusCompleted {
		r.mu.Unlock()
		return Run{}, ErrRunNotCompleted
	}
	if state.continued {
		r.mu.Unlock()
		return Run{}, fmt.Errorf("run %q has already been continued", runID)
	}

	// Snapshot before we release.
	convID := state.run.ConversationID
	existingModel := state.run.Model
	existingTenantID := state.run.TenantID
	existingAgentID := state.run.AgentID
	systemPrompt := state.staticSystemPrompt
	promptResolved := state.promptResolved
	// Snapshot security controls so the continuation inherits the same budget
	// ceiling and permission constraints as the source run unless explicitly
	// overridden by the caller.
	srcMaxCostUSD := state.maxCostUSD
	srcPermissions := state.permissions
	// Snapshot resolvedRoleModels so the continuation honours any per-request
	// RoleModels overrides that were active on the source run. Without this,
	// the continuation's execute() call re-resolves from req.RoleModels (nil)
	// and falls back to runner-level config only, silently dropping any
	// per-request Primary or Summarizer overrides.
	srcResolvedRoleModels := state.resolvedRoleModels
	// Snapshot allowedTools so the continuation enforces the same per-run tool
	// filter as the source run unless explicitly overridden by the caller.
	srcAllowedTools := copyStringSlice(state.allowedTools)
	effectiveAllowedTools := srcAllowedTools
	if req.AllowedTools != nil {
		effectiveAllowedTools = copyStringSlice(*req.AllowedTools)
	}
	effectivePermissions := srcPermissions
	if req.Permissions != nil {
		effectivePermissions = normalizePermissionConfig(*req.Permissions)
	}
	policyNotice := buildContinuationPolicyNotice(srcAllowedTools, effectiveAllowedTools, srcPermissions, effectivePermissions)

	// Mark the source run as continued so no second goroutine can also
	// continue it. We do NOT mutate run.Status — it stays Completed.
	state.continued = true

	now := time.Now().UTC()
	newRun := Run{
		ID:             r.nextID("run"),
		Prompt:         req.Prompt,
		Model:          existingModel,
		Status:         RunStatusQueued,
		UsageTotals:    &RunUsageTotals{},
		CostTotals:     &RunCostTotals{CostStatus: CostStatusPending},
		TenantID:       existingTenantID,
		ConversationID: convID,
		AgentID:        existingAgentID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// Create rollout recorder for the continuation run (while lock is held,
	// but recorder creation doesn't need it — release lock before creating).
	r.mu.Unlock()

	var contRec *rollout.Recorder
	if r.config.RolloutDir != "" {
		var recErr error
		contRec, recErr = rollout.NewRecorder(rollout.RecorderConfig{
			Dir:   r.config.RolloutDir,
			RunID: newRun.ID,
		})
		if recErr != nil && r.config.Logger != nil {
			r.config.Logger.Error("rollout recorder: failed to create for continuation", "run_id", newRun.ID, "error", recErr)
		}
	}

	var contSB *errorchain.SnapshotBuilder
	if r.config.ErrorChainEnabled {
		contSB = errorchain.NewSnapshotBuilder(r.config.ErrorContextDepth)
	}
	contState := &runState{
		run:                      newRun,
		staticSystemPrompt:       systemPrompt,
		promptResolved:           promptResolved,
		usageTotals:              usageTotalsAccumulator{},
		costTotals:               RunCostTotals{CostStatus: CostStatusPending},
		messages:                 make([]Message, 0, 16),
		events:                   make([]Event, 0, 32),
		subscribers:              make(map[chan Event]struct{}),
		steeringCh:               make(chan string, steeringBufferSize),
		maxCostUSD:               srcMaxCostUSD,
		permissions:              effectivePermissions,
		permissionWorkspaceRoot:  r.defaultPermissionWorkspaceRoot(),
		resolvedRoleModels:       srcResolvedRoleModels,
		allowedTools:             effectiveAllowedTools,
		previousRunID:            runID,
		continuationPolicyNotice: policyNotice,
		snapshotBuilder:          contSB,
	}
	if contRec != nil {
		startRecorderGoroutine(contState, contRec)
	}
	r.mu.Lock()
	r.runs[newRun.ID] = contState
	r.mu.Unlock()

	// Persist the continuation run record to the configured store (non-fatal).
	r.storeCreateRun(newRun)

	// Build the request after the lock is released.
	// Propagate resolvedRoleModels into the RunRequest so that execute()'s
	// resolveRoleModels() call re-applies the same per-request overrides
	// (Primary, Summarizer) rather than silently falling back to runner-level
	// config when the originating request had per-request RoleModels set.
	var contRoleModels *RoleModels
	if srcResolvedRoleModels.Primary != "" || srcResolvedRoleModels.Summarizer != "" {
		rm := srcResolvedRoleModels
		contRoleModels = &rm
	}
	runReq := RunRequest{
		Prompt:         req.Prompt,
		Model:          existingModel,
		ConversationID: convID,
		TenantID:       existingTenantID,
		AgentID:        existingAgentID,
		RoleModels:     contRoleModels,
		AllowedTools:   copyStringSlice(effectiveAllowedTools),
	}
	perms := effectivePermissions
	runReq.Permissions = &perms
	if systemPrompt != "" {
		runReq.SystemPrompt = systemPrompt
	}

	if err := r.dispatchRun(newRun.ID, runReq); err != nil {
		// Runner was shut down between validation and dispatch.  Remove the
		// half-created continuation run so callers never see an orphan.
		//
		// Three cleanup actions are required:
		// 1. Close the recorder goroutine so it doesn't block forever.
		// 2. Detach any audit writer from the orphan continuation state.
		// 3. Revert state.continued on the SOURCE run so it can be continued
		//    again (the continuation never actually started).
		if contState.closeRecorderOnce != nil {
			contState.closeRecorderOnce()
		}
		r.mu.Lock()
		// Detach any audit writer on the continuation state (forward-compat
		// guard; ContinueRunWithOptions does not create one today). Shared
		// date-bucket writers are closed by Runner.Shutdown.
		if s, ok := r.runs[newRun.ID]; ok {
			s.auditWriter = nil
		}
		delete(r.runs, newRun.ID)
		// Revert state.continued on the source run so it remains continuable.
		// The continuation never actually started, so the source run should not
		// be permanently marked as continued.
		if srcState, ok := r.runs[runID]; ok {
			srcState.continued = false
		}
		r.mu.Unlock()
		return Run{}, err
	}

	return newRun, nil
}

// GetRunSummary computes a telemetry summary for a completed (or failed) run
// by scanning the run's event history. Returns ErrRunNotFound if the run does
// not exist, or an error if the run is still in progress.
func (r *Runner) GetRunSummary(runID string) (RunSummary, error) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return RunSummary{}, ErrRunNotFound
	}
	run := state.run
	events := append([]Event(nil), state.events...)
	acc := state.usageTotals
	costTotals := state.costTotals
	r.mu.RUnlock()

	if run.Status != RunStatusCompleted && run.Status != RunStatusFailed {
		return RunSummary{}, fmt.Errorf("run %q is still %s", runID, run.Status)
	}

	stepsTaken := 0
	var toolCalls []ToolCallSummary
	currentStep := 0
	for _, evt := range events {
		switch evt.Type {
		case EventLLMTurnRequested:
			if stepVal, ok := evt.Payload["step"]; ok {
				if s, ok := stepVal.(float64); ok {
					currentStep = int(s)
				} else if s, ok := stepVal.(int); ok {
					currentStep = s
				}
			}
			stepsTaken++
		case EventToolCallStarted:
			name, _ := evt.Payload["tool"].(string)
			toolCalls = append(toolCalls, ToolCallSummary{
				ToolName: name,
				Step:     currentStep,
			})
		}
	}

	var cacheHitRate float64
	if acc.hasCachedPromptTokens && acc.promptTokensTotal > 0 {
		cacheHitRate = float64(acc.cachedPromptTokens) / float64(acc.promptTokensTotal)
	}

	summary := RunSummary{
		RunID:                 runID,
		Status:                run.Status,
		StepsTaken:            stepsTaken,
		TotalPromptTokens:     acc.promptTokensTotal,
		TotalCompletionTokens: acc.completionTokensTotal,
		TotalCostUSD:          costTotals.CostUSDTotal,
		CostStatus:            costTotals.CostStatus,
		ToolCalls:             toolCalls,
		CacheHitRate:          cacheHitRate,
		Error:                 run.Error,
	}
	if summary.ToolCalls == nil {
		summary.ToolCalls = []ToolCallSummary{}
	}
	return summary, nil
}

func (r *Runner) PendingInput(runID string) (htools.AskUserQuestionPending, error) {
	r.mu.RLock()
	_, ok := r.runs[runID]
	r.mu.RUnlock()
	if !ok {
		return htools.AskUserQuestionPending{}, ErrRunNotFound
	}
	if r.config.AskUserBroker == nil {
		return htools.AskUserQuestionPending{}, ErrNoPendingInput
	}
	pending, ok := r.config.AskUserBroker.Pending(runID)
	if !ok {
		return htools.AskUserQuestionPending{}, ErrNoPendingInput
	}
	return pending, nil
}

func (r *Runner) SubmitInput(runID string, answers map[string]string) error {
	r.mu.RLock()
	_, ok := r.runs[runID]
	r.mu.RUnlock()
	if !ok {
		return ErrRunNotFound
	}
	if r.config.AskUserBroker == nil {
		return ErrNoPendingInput
	}
	if err := r.config.AskUserBroker.Submit(runID, answers); err != nil {
		if errors.Is(err, ErrNoPendingUserQuestion) {
			return ErrNoPendingInput
		}
		if errors.Is(err, ErrInvalidUserQuestionInput) {
			return ErrInvalidRunInput
		}
		return err
	}
	return nil
}

// SteerRun injects a guidance message into a running run. The message is
// appended to the transcript as a user message before the next LLM call.
//
// Errors:
//   - ErrRunNotFound        — the run does not exist.
//   - ErrRunNotActive       — the run is not currently active (already completed or failed).
//   - ErrSteeringBufferFull — the steering channel is at capacity; try again later.
//   - validation error      — message is empty.
func (r *Runner) SteerRun(runID, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is required")
	}

	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return ErrRunNotFound
	}
	status := state.run.Status
	steeringCh := state.steeringCh
	r.mu.RUnlock()

	if status != RunStatusRunning && status != RunStatusWaitingForUser {
		return ErrRunNotActive
	}

	select {
	case steeringCh <- message:
		return nil
	default:
		return ErrSteeringBufferFull
	}
}

func (r *Runner) Subscribe(runID string) ([]Event, <-chan Event, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.runs[runID]
	if !ok {
		return nil, nil, nil, fmt.Errorf("run %q not found", runID)
	}

	// Deep-clone each historical event's payload so callers cannot mutate
	// the stored forensic history by modifying nested structures.
	history := make([]Event, len(state.events))
	for i, ev := range state.events {
		history[i] = ev
		history[i].Payload = deepClonePayload(ev.Payload)
	}
	ch := make(chan Event, 64)
	state.subscribers[ch] = struct{}{}

	cancel := func() {
		r.mu.Lock()
		state, ok := r.runs[runID]
		if !ok {
			r.mu.Unlock()
			return
		}
		shouldClose := false
		if _, exists := state.subscribers[ch]; exists {
			delete(state.subscribers, ch)
			shouldClose = true
			r.pruneCompletedRunsLocked()
		}
		r.mu.Unlock()
		if shouldClose {
			r.closeSubscriber(ch)
		}
	}
	return history, ch, cancel, nil
}

func (r *Runner) closeSubscriber(ch chan Event) {
	r.subscriberMu.Lock()
	defer r.subscriberMu.Unlock()
	if _, closed := r.closedSubscribers[ch]; closed {
		return
	}
	r.closedSubscribers[ch] = struct{}{}
	close(ch)
}

func (r *Runner) sendTerminalSubscriberEvent(ch chan Event, ev Event) {
	r.subscriberMu.Lock()
	defer r.subscriberMu.Unlock()
	if _, closed := r.closedSubscribers[ch]; closed {
		return
	}
	select {
	case ch <- ev:
	default:
		// Drop if subscriber is too slow; event is still persisted in run history.
	}
}

// RunPrompt implements htools.AgentRunner. It starts a new run with the given
// prompt (using the runner's default model and config) and waits for it to
// complete, returning the run's final output. This satisfies the AgentRunner
// interface required by the skill tool for plain (non-forked) sub-runs.
func (r *Runner) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return r.runPromptWithRequest(ctx, RunRequest{
		Prompt:    prompt,
		ForkDepth: htools.ForkDepthFromContext(ctx),
	}, "RunPrompt")
}

// RunPromptWithAllowedTools starts a new run like RunPrompt while preserving
// the provided AllowedTools filter for fallback execution paths.
func (r *Runner) RunPromptWithAllowedTools(ctx context.Context, prompt string, allowedTools []string) (string, error) {
	return r.runPromptWithRequest(ctx, RunRequest{
		Prompt:       prompt,
		AllowedTools: append([]string(nil), allowedTools...),
		ForkDepth:    htools.ForkDepthFromContext(ctx),
	}, "RunPromptWithAllowedTools")
}

func (r *Runner) runPromptWithRequest(ctx context.Context, req RunRequest, op string) (string, error) {
	run, err := r.StartRun(req)
	if err != nil {
		return "", fmt.Errorf("%s: start run: %w", op, err)
	}

	history, stream, cancel, err := r.Subscribe(run.ID)
	if err != nil {
		return "", fmt.Errorf("%s: subscribe: %w", op, err)
	}
	defer cancel()

	result, err := r.waitForTerminalResult(ctx, run.ID, history, stream)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// RunForkedSkill implements htools.ForkedAgentRunner. It starts a new sub-run
// for the given ForkConfig and waits for it to complete. The sub-run inherits
// the parent run's SystemPrompt and Permissions (looked up via the run ID
// embedded in ctx). AllowedTools from ForkConfig is forwarded as RunRequest.AllowedTools.
// The fork depth from ctx is propagated to the child run via RunRequest.ForkDepth.
func (r *Runner) RunForkedSkill(ctx context.Context, config htools.ForkConfig) (htools.ForkResult, error) {
	requestHandoff := config.ParentContextHandoff
	if requestHandoff == nil {
		if fromContext, ok := htools.BuildParentContextHandoffFromContext(ctx); ok {
			copied := fromContext
			requestHandoff = &copied
		}
	}

	// Build the sub-run request, forwarding AllowedTools from the fork config.
	// Propagate fork depth from context so the child knows its nesting level.
	req := RunRequest{
		Prompt:               config.Prompt,
		AllowedTools:         config.AllowedTools,
		ForkDepth:            htools.ForkDepthFromContext(ctx),
		ParentContextHandoff: requestHandoff,
	}
	// Apply optional model and max_steps overrides from ForkConfig.
	// Empty/zero values mean "use runner defaults" (inherit from parent run).
	if config.Model != "" {
		req.Model = config.Model
	}
	if config.MaxSteps > 0 {
		req.MaxSteps = config.MaxSteps
	}
	if config.MaxTurns > 0 {
		req.MaxTurns = config.MaxTurns
	}

	// Inherit SystemPrompt, Permissions, and ProfileName from the parent run when possible.
	if meta, ok := htools.RunMetadataFromContext(ctx); ok && meta.RunID != "" {
		r.mu.RLock()
		parentState, parentOK := r.runs[meta.RunID]
		if parentOK {
			req.SystemPrompt = parentState.staticSystemPrompt
			perms := parentState.permissions
			req.Permissions = &perms
			req.ProfileName = parentState.profileName
		}
		r.mu.RUnlock()
	}

	run, err := r.StartRun(req)
	if err != nil {
		return htools.ForkResult{}, fmt.Errorf("RunForkedSkill: start sub-run: %w", err)
	}

	history, stream, cancel, err := r.Subscribe(run.ID)
	if err != nil {
		return htools.ForkResult{}, fmt.Errorf("RunForkedSkill: subscribe: %w", err)
	}
	defer cancel()

	return r.waitForTerminalResult(ctx, run.ID, history, stream)
}

func (r *Runner) waitForTerminalResult(ctx context.Context, runID string, history []Event, stream <-chan Event) (htools.ForkResult, error) {
	for _, ev := range history {
		if IsTerminalEvent(ev.Type) {
			return r.forkResultFromRun(runID), nil
		}
	}

	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return r.forkResultFromRun(runID), nil
			}
			if IsTerminalEvent(ev.Type) {
				return r.forkResultFromRun(runID), nil
			}
		case <-ctx.Done():
			if result, terminal := r.terminalForkResult(runID); terminal {
				return result, nil
			}
			_ = r.CancelRun(runID)
			return htools.ForkResult{Error: ctx.Err().Error()}, ctx.Err()
		}
	}
}

func (r *Runner) terminalForkResult(runID string) (htools.ForkResult, bool) {
	run, ok := r.GetRun(runID)
	if !ok {
		return htools.ForkResult{}, false
	}
	if run.Status != RunStatusCompleted && run.Status != RunStatusFailed && run.Status != RunStatusCancelled {
		return htools.ForkResult{}, false
	}
	return forkResultFromSnapshot(run), true
}

// forkResultFromRun extracts a ForkResult from a completed run state.
func (r *Runner) forkResultFromRun(runID string) htools.ForkResult {
	run, ok := r.GetRun(runID)
	if !ok {
		return htools.ForkResult{Error: "run not found"}
	}
	return forkResultFromSnapshot(run)
}

func forkResultFromSnapshot(run Run) htools.ForkResult {
	if run.Error != "" {
		return htools.ForkResult{Error: run.Error}
	}
	return htools.ForkResult{Output: run.Output}
}

// resolveProvider determines which Provider to use for a run.
// Returns the provider, its name, and any error.
func (r *Runner) resolveProvider(runID, model, preferredProvider string, allowFallback bool) (Provider, string, error) {
	if r.providerRegistry == nil {
		return r.provider, "default", nil
	}

	// If caller explicitly specified a provider, try it first.
	if preferredProvider != "" {
		client, err := r.providerRegistry.GetClient(preferredProvider)
		if err == nil {
			if p, ok := client.(Provider); ok {
				return p, preferredProvider, nil
			}
		}
		// Preferred provider unavailable — emit warning and fall through to auto-detection if allowed.
		if !allowFallback {
			return nil, "", fmt.Errorf("requested provider %q: unavailable or does not implement Provider interface", preferredProvider)
		}
		r.emit(runID, EventPromptWarning, map[string]any{
			"code":    "provider_fallback",
			"message": fmt.Sprintf("requested provider %q unavailable, falling back to auto-detection", preferredProvider),
		})
	}

	client, providerName, err := r.providerRegistry.GetClientForModel(model)
	if err != nil {
		// Model not found or client creation failed
		if allowFallback {
			r.emit(runID, EventPromptWarning, map[string]any{
				"code":    "provider_fallback",
				"message": fmt.Sprintf("model %q provider unavailable (%v), falling back to default provider", model, err),
			})
			return r.provider, "default", nil
		}
		return nil, "", fmt.Errorf("model %q: %w", model, err)
	}

	// Type-assert ProviderClient to Provider interface
	p, ok := client.(Provider)
	if !ok {
		if allowFallback {
			r.emit(runID, EventPromptWarning, map[string]any{
				"code":    "provider_fallback",
				"message": fmt.Sprintf("provider %q client does not implement Provider interface, falling back to default", providerName),
			})
			return r.provider, "default", nil
		}
		return nil, "", fmt.Errorf("provider %q client does not implement Provider interface", providerName)
	}

	return p, providerName, nil
}

// resolveProviderCandidates builds the ordered list of providers to attempt
// for a run.  Index 0 is the primary (identical to what resolveProvider
// returns today — behaviour is bit-identical).  Indices 1..n are fallback
// candidates and are only populated when allowFallback is true.
//
// When allowFallback is true and fallbackProviders is non-empty each name is
// resolved via the registry; unresolvable names are silently skipped.  When
// fallbackProviders is empty and allowFallback is true, r.provider (the
// runner's default provider) is appended as an implicit fallback unless it is
// already the primary.  Duplicates (same Provider pointer or same name) are
// deduplicated against the primary.
func (r *Runner) resolveProviderCandidates(runID, model, preferredProvider string, allowFallback bool, fallbackProviders []string) ([]providerCandidate, error) {
	primary, primaryName, err := r.resolveProvider(runID, model, preferredProvider, allowFallback)
	if err != nil {
		return nil, err
	}

	candidates := []providerCandidate{{Provider: primary, Name: primaryName}}

	if !allowFallback || r.providerRegistry == nil {
		return candidates, nil
	}

	seen := map[string]struct{}{primaryName: {}}

	if len(fallbackProviders) > 0 {
		for _, name := range fallbackProviders {
			if _, dup := seen[name]; dup {
				continue
			}
			client, err := r.providerRegistry.GetClient(name)
			if err != nil {
				continue // unresolvable — skip silently
			}
			p, ok := client.(Provider)
			if !ok {
				continue // client doesn't implement Provider — skip
			}
			seen[name] = struct{}{}
			candidates = append(candidates, providerCandidate{Provider: p, Name: name})
		}
	} else {
		// No explicit fallback list: append the runner's default provider if
		// it is different from the primary.
		if r.provider != nil {
			const defaultName = "default"
			if _, dup := seen[defaultName]; !dup {
				seen[defaultName] = struct{}{}
				candidates = append(candidates, providerCandidate{Provider: r.provider, Name: defaultName})
			}
		}
	}

	return candidates, nil
}

func (r *Runner) execute(runID string, req RunRequest) {
	// Create a cancellable context for this run. The cancel function is stored
	// in cancelFuncs so that CancelRun() can interrupt any in-flight provider
	// call or tool execution cooperatively via context cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFuncs.Store(runID, cancel)
	defer func() {
		cancel()
		r.cancelFuncs.Delete(runID)
	}()

	// Safety net: ensure the recorder goroutine is always cleaned up when
	// execute() exits, even if no terminal event was ever emitted (e.g. a
	// double panic where failRun itself panics, leaving recorderCh open
	// forever).  closeRecorderOnce uses sync.Once so calling it here is
	// harmless when the terminal-event path in emit() already closed it.
	defer func() {
		r.mu.RLock()
		state, ok := r.runs[runID]
		var closeFn func()
		if ok {
			closeFn = state.closeRecorderOnce
		}
		r.mu.RUnlock()
		if closeFn != nil {
			closeFn()
		}
	}()
	defer r.closeScopedMCP(runID)

	// Recover from any panic inside the step loop so that a misbehaving tool
	// handler or internal bug does not crash the entire server process.
	// The panic value is converted to a descriptive error, emitted as a
	// run.failed event, and logged with a stack trace.
	defer func() {
		if p := recover(); p != nil {
			stack := debug.Stack()
			errMsg := fmt.Sprintf("internal panic: %v", p)
			if r.config.Logger != nil {
				r.config.Logger.Error("runner: recovered panic in execute",
					"run_id", runID,
					"panic", p,
					"stack", string(stack),
				)
			}
			r.failRun(runID, fmt.Errorf("%s", errMsg))
		}
	}()

	r.setStatus(runID, RunStatusRunning, "", "")

	// Snapshot the effective permissions for this run once at the start of
	// execute() so tool approval and sandbox scope are both sourced from the
	// live run state rather than from registry startup defaults.
	var effectivePermissions PermissionConfig
	{
		r.mu.RLock()
		if st, ok := r.runs[runID]; ok {
			effectivePermissions = st.permissions
		}
		r.mu.RUnlock()
	}
	effectivePermissions = normalizePermissionConfig(effectivePermissions)
	effectiveApprovalPolicy := effectivePermissions.Approval

	// Build run.started payload with optional previous_run_id for continuations.
	// tenant_id records the run's owning tenant in the rollout so that replay
	// can verify tenant ownership from the recorded content (cross-tenant replay
	// of a recorded rollout is rejected by comparing this value to the caller).
	// The effective tenant is read from the run record, which has already
	// normalized an empty request tenant to "default".
	startPayload := map[string]any{"prompt": req.Prompt}
	r.mu.RLock()
	if state, ok := r.runs[runID]; ok {
		if state.previousRunID != "" {
			startPayload["previous_run_id"] = state.previousRunID
		}
		if tid := strings.TrimSpace(state.run.TenantID); tid != "" {
			startPayload["tenant_id"] = tid
		}
	}
	r.mu.RUnlock()
	r.emit(runID, EventRunStarted, startPayload)

	// Audit trail: write run.started with provenance (model, initiator prefix).
	if r.config.AuditTrailEnabled {
		auditModel := req.Model
		if auditModel == "" {
			auditModel = r.config.DefaultModel
		}
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunStarted),
			Payload: map[string]any{
				"prompt":                   req.Prompt,
				"model":                    auditModel,
				"initiator_api_key_prefix": req.InitiatorAPIKeyPrefix,
			},
		})
	}

	preflight, err := r.runPreflight(ctx, runID, req)
	if err != nil {
		r.failRun(runID, err)
		return
	}

	// Resolve the effective step limit for this run.
	// Priority: per-run request > runner config.
	// 0 in either position means "no limit" once chosen.
	effectiveMaxSteps := r.config.MaxSteps
	if req.MaxSteps > 0 {
		effectiveMaxSteps = req.MaxSteps
	}
	// effectiveMaxSteps == 0 means unlimited.

	// Resolve the effective turns limit for this run.
	// Priority: per-run request > runner config.
	// 0 means "no limit" once chosen. MaxTurns counts assistant LLM turns.
	effectiveMaxTurns := r.config.MaxTurns
	if req.MaxTurns > 0 {
		effectiveMaxTurns = req.MaxTurns
	}
	// effectiveMaxTurns == 0 means unlimited.

	// runForkDepth is the nesting depth for this run. 0 = root, >0 = subagent.
	// Captured once from req to avoid repeated lock acquisitions in the step loop.
	runForkDepth := req.ForkDepth

	r.runStepEngine(ctx, runID, req, preflight, effectiveMaxSteps, effectiveMaxTurns, runForkDepth, effectiveApprovalPolicy, htools.SandboxScope(effectivePermissions.Sandbox))
	return
}

type hookBlock struct {
	hookName string
	reason   string
}

func (r *Runner) applyPreHooks(ctx context.Context, runID string, step int, req CompletionRequest) (CompletionRequest, *hookBlock, error) {
	current := req
	for _, hook := range r.config.PreMessageHooks {
		hookName := normalizeHookName(hook.Name())
		r.emit(runID, EventHookStarted, map[string]any{
			"stage": "pre_message",
			"hook":  hookName,
			"step":  step,
		})

		hookStart := time.Now()
		result, err := hook.BeforeMessage(ctx, PreMessageHookInput{
			RunID:   runID,
			Step:    step,
			Request: current,
		})
		if err != nil {
			ignored := r.config.HookFailureMode == HookFailureModeFailOpen
			r.emit(runID, EventHookFailed, map[string]any{
				"stage":   "pre_message",
				"hook":    hookName,
				"step":    step,
				"error":   err.Error(),
				"mode":    r.config.HookFailureMode,
				"ignored": ignored,
			})
			if ignored {
				continue
			}
			return current, nil, fmt.Errorf("pre-message hook %s failed: %w", hookName, err)
		}

		action := result.Action
		if action == "" {
			action = HookActionContinue
		}
		mutated := false
		if result.MutatedRequest != nil {
			current = *result.MutatedRequest
			mutated = true
		}

		r.emit(runID, EventHookCompleted, map[string]any{
			"stage":       "pre_message",
			"hook":        hookName,
			"step":        step,
			"action":      action,
			"mutated":     mutated,
			"reason":      result.Reason,
			"duration_ms": time.Since(hookStart).Milliseconds(),
		})

		if action == HookActionBlock {
			return current, &hookBlock{hookName: hookName, reason: result.Reason}, nil
		}
	}
	return current, nil, nil
}

func (r *Runner) applyPostHooks(ctx context.Context, runID string, step int, req CompletionRequest, res CompletionResult) (CompletionResult, *hookBlock, error) {
	current := res
	for _, hook := range r.config.PostMessageHooks {
		hookName := normalizeHookName(hook.Name())
		r.emit(runID, EventHookStarted, map[string]any{
			"stage": "post_message",
			"hook":  hookName,
			"step":  step,
		})

		hookStart := time.Now()
		result, err := hook.AfterMessage(ctx, PostMessageHookInput{
			RunID:     runID,
			Step:      step,
			Request:   req,
			Response:  current,
			ToolCalls: current.ToolCalls,
		})
		if err != nil {
			ignored := r.config.HookFailureMode == HookFailureModeFailOpen
			r.emit(runID, EventHookFailed, map[string]any{
				"stage":   "post_message",
				"hook":    hookName,
				"step":    step,
				"error":   err.Error(),
				"mode":    r.config.HookFailureMode,
				"ignored": ignored,
			})
			if ignored {
				continue
			}
			return current, nil, fmt.Errorf("post-message hook %s failed: %w", hookName, err)
		}

		action := result.Action
		if action == "" {
			action = HookActionContinue
		}
		mutated := false
		if result.MutatedResponse != nil {
			current = *result.MutatedResponse
			mutated = true
		}

		r.emit(runID, EventHookCompleted, map[string]any{
			"stage":       "post_message",
			"hook":        hookName,
			"step":        step,
			"action":      action,
			"mutated":     mutated,
			"reason":      result.Reason,
			"duration_ms": time.Since(hookStart).Milliseconds(),
		})

		if action == HookActionBlock {
			return current, &hookBlock{hookName: hookName, reason: result.Reason}, nil
		}
	}
	return current, nil, nil
}

func normalizeHookName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "unnamed_hook"
	}
	return trimmed
}

// applyPreToolUseHooks runs all registered PreToolUseHooks for a given tool call.
//
// It returns (denied=true, errorOutput) if any hook denies execution.
// If denied=false, callArgs may have been modified in place by a hook.
//
// Panic recovery: a panicking hook is caught and treated as a hook error.
// - fail_open:   panic is recovered, hook is skipped, execution continues.
// - fail_closed: panic is recovered, tool is denied with an error output.
func (r *Runner) applyPreToolUseHooks(ctx context.Context, runID string, call ToolCall, callArgs *json.RawMessage) (denied bool, denialOutput string) {
	if len(r.config.PreToolUseHooks) == 0 {
		return false, ""
	}

	for _, hook := range r.config.PreToolUseHooks {
		hookName := normalizeHookName(hook.Name())
		r.emit(runID, EventToolHookStarted, map[string]any{
			"stage":   "pre_tool_use",
			"hook":    hookName,
			"tool":    call.Name,
			"call_id": call.ID,
		})

		// Forensics Part 3: capture args before hook runs for mutation tracing.
		argsBefore := ""
		if r.config.TraceHookMutations {
			argsBefore = string(*callArgs)
		}

		result, err := safeCallPreToolUseHook(hook, ctx, PreToolUseEvent{
			ToolName: call.Name,
			CallID:   call.ID,
			Args:     append(json.RawMessage(nil), *callArgs...),
			RunID:    runID,
		})

		if err != nil {
			ignored := r.config.HookFailureMode == HookFailureModeFailOpen
			r.emit(runID, EventToolHookFailed, map[string]any{
				"stage":   "pre_tool_use",
				"hook":    hookName,
				"tool":    call.Name,
				"call_id": call.ID,
				"error":   err.Error(),
				"ignored": ignored,
			})
			if r.config.TraceHookMutations {
				// Hook error in fail_closed mode counts as an implicit block.
				if !ignored {
					mutation := tooldecision.HookMutation{
						ToolCallID: call.ID,
						HookName:   hookName,
						Action:     tooldecision.HookActionBlock,
						ArgsBefore: argsBefore,
					}
					r.emit(runID, EventToolHookMutation, map[string]any{
						"tool_call_id": mutation.ToolCallID,
						"hook":         mutation.HookName,
						"action":       string(mutation.Action),
						"args_before":  mutation.ArgsBefore,
						"args_after":   mutation.ArgsAfter,
					})
				}
			}
			if ignored {
				continue
			}
			// fail_closed: deny the tool call with an error result
			return true, mustJSON(map[string]any{
				"error": fmt.Sprintf("pre_tool_use hook %s failed: %v", hookName, err),
			})
		}

		// nil result is treated as allow with no modification
		if result == nil {
			r.emit(runID, EventToolHookCompleted, map[string]any{
				"stage":    "pre_tool_use",
				"hook":     hookName,
				"tool":     call.Name,
				"call_id":  call.ID,
				"decision": "allow",
				"mutated":  false,
			})
			continue
		}

		mutated := false
		if len(result.ModifiedArgs) > 0 {
			*callArgs = append(json.RawMessage(nil), result.ModifiedArgs...)
			mutated = true
		}

		decision := "allow"
		if result.Decision == ToolHookDeny {
			decision = "deny"
		}

		r.emit(runID, EventToolHookCompleted, map[string]any{
			"stage":    "pre_tool_use",
			"hook":     hookName,
			"tool":     call.Name,
			"call_id":  call.ID,
			"decision": decision,
			"reason":   result.Reason,
			"mutated":  mutated,
		})

		// Forensics Part 3: emit hook mutation event when tracing is enabled.
		if r.config.TraceHookMutations {
			argsAfter := string(*callArgs)
			blocked := result.Decision == ToolHookDeny
			action := tooldecision.ClassifyHookAction(blocked, argsBefore, argsAfter)
			// Only emit when something interesting happened (not a plain Allow).
			if action != tooldecision.HookActionAllow {
				mutation := tooldecision.HookMutation{
					ToolCallID: call.ID,
					HookName:   hookName,
					Action:     action,
					ArgsBefore: argsBefore,
					ArgsAfter:  argsAfter,
				}
				r.emit(runID, EventToolHookMutation, map[string]any{
					"tool_call_id": mutation.ToolCallID,
					"hook":         mutation.HookName,
					"action":       string(mutation.Action),
					"args_before":  mutation.ArgsBefore,
					"args_after":   mutation.ArgsAfter,
				})
			}
		}

		if result.Decision == ToolHookDeny {
			reason := result.Reason
			if reason == "" {
				reason = "denied by hook"
			}
			return true, mustJSON(map[string]any{
				"error": fmt.Sprintf("tool %q denied by hook %s: %s", call.Name, hookName, reason),
			})
		}
	}
	return false, ""
}

// applyPostToolUseHooks runs all registered PostToolUseHooks after a tool executes.
//
// It returns the (possibly modified) tool output. If toolErr is non-nil,
// the output passed to hooks will be the empty string and toolErr will be set
// in the event; the original error output is still returned to the LLM
// (hooks can override it via ModifiedResult).
//
// Panic recovery mirrors pre-tool-use hook behaviour.
func (r *Runner) applyPostToolUseHooks(ctx context.Context, runID string, call ToolCall, callArgs json.RawMessage, output string, duration time.Duration, toolErr error) string {
	if len(r.config.PostToolUseHooks) == 0 {
		return output
	}

	// For error results, pass the empty string as result (the JSON error
	// output is constructed after hooks run).
	rawResult := output
	if toolErr != nil {
		rawResult = ""
	}

	current := output
	for _, hook := range r.config.PostToolUseHooks {
		hookName := normalizeHookName(hook.Name())
		r.emit(runID, EventToolHookStarted, map[string]any{
			"stage":   "post_tool_use",
			"hook":    hookName,
			"tool":    call.Name,
			"call_id": call.ID,
		})

		result, err := safeCallPostToolUseHook(hook, ctx, PostToolUseEvent{
			ToolName: call.Name,
			CallID:   call.ID,
			Args:     append(json.RawMessage(nil), callArgs...),
			Result:   rawResult,
			Duration: duration,
			Error:    toolErr,
			RunID:    runID,
		})

		if err != nil {
			ignored := r.config.HookFailureMode == HookFailureModeFailOpen
			r.emit(runID, EventToolHookFailed, map[string]any{
				"stage":   "post_tool_use",
				"hook":    hookName,
				"tool":    call.Name,
				"call_id": call.ID,
				"error":   err.Error(),
				"ignored": ignored,
			})
			if !ignored {
				// fail_closed: stop the chain and return current output unchanged
				return current
			}
			continue
		}

		mutated := false
		if result != nil && result.ModifiedResult != "" {
			current = result.ModifiedResult
			rawResult = result.ModifiedResult
			mutated = true
		}

		r.emit(runID, EventToolHookCompleted, map[string]any{
			"stage":   "post_tool_use",
			"hook":    hookName,
			"tool":    call.Name,
			"call_id": call.ID,
			"mutated": mutated,
		})
	}
	return current
}

// safeCallPreToolUseHook calls hook.PreToolUse and recovers from panics,
// returning the panic as an error.
func safeCallPreToolUseHook(hook PreToolUseHook, ctx context.Context, ev PreToolUseEvent) (result *PreToolUseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook panic: %v", r)
		}
	}()
	return hook.PreToolUse(ctx, ev)
}

// safeCallPostToolUseHook calls hook.PostToolUse and recovers from panics,
// returning the panic as an error.
func safeCallPostToolUseHook(hook PostToolUseHook, ctx context.Context, ev PostToolUseEvent) (result *PostToolUseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook panic: %v", r)
		}
	}()
	return hook.PostToolUse(ctx, ev)
}

// filteredToolsForRun returns tool definitions for a run, applying skill
// constraints on top of the deferred-tool activation filter. If no skill
// constraint is active, or if the constraint has nil AllowedTools, all
// definitions from DefinitionsForRun are returned.
func (r *Runner) filteredToolsForRun(runID string) []ToolDefinition {
	defs := r.toolsForRun(runID).DefinitionsForRun(runID, r.activations)

	// Skill constraints (activated by the skill tool) take precedence over the
	// per-run base filter. If a skill constraint is active with a non-nil
	// AllowedTools list, apply it exclusively.
	constraint, active := r.skillConstraints.Active(runID)
	if active && constraint.AllowedTools != nil {
		allowed := make(map[string]bool, len(constraint.AllowedTools)+len(AlwaysAvailableTools))
		for _, name := range constraint.AllowedTools {
			allowed[name] = true
		}
		for name := range AlwaysAvailableTools {
			allowed[name] = true
		}
		filtered := make([]ToolDefinition, 0, len(allowed))
		for _, def := range defs {
			if allowed[def.Name] {
				filtered = append(filtered, def)
			}
		}
		return filtered
	}

	// No active skill constraint (or skill constraint with nil AllowedTools =
	// unrestricted). Apply the per-run base allowed-tools list from RunRequest.
	r.mu.RLock()
	state, stateOK := r.runs[runID]
	var baseAllowed []string
	if stateOK {
		baseAllowed = state.allowedTools
	}
	r.mu.RUnlock()

	if len(baseAllowed) == 0 {
		return defs // no per-run restriction either
	}

	allowed := make(map[string]bool, len(baseAllowed)+len(AlwaysAvailableTools))
	for _, name := range baseAllowed {
		allowed[name] = true
	}
	for name := range AlwaysAvailableTools {
		// When a run explicitly restricts its tools via allowed_tools, only
		// AskUserQuestion is truly unconditional infrastructure. find_tool and
		// skill must NOT be silently force-granted: find_tool can surface
		// deferred tools and skill can activate a skill constraint whose own
		// allowlist replaces this base filter — both would let a restricted run
		// reach tools outside its allowed_tools boundary (issue #527). They are
		// available only when the caller lists them explicitly.
		if name == "AskUserQuestion" || allowed[name] {
			allowed[name] = true
		}
	}
	filtered := make([]ToolDefinition, 0, len(allowed))
	for _, def := range defs {
		if allowed[def.Name] {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// maybeActivateSkillConstraint inspects a skill tool result and activates
// a constraint if the result contains allowed_tools.
func (r *Runner) maybeActivateSkillConstraint(runID, resultJSON string) {
	var result struct {
		Skill        string   `json:"skill"`
		AllowedTools []string `json:"allowed_tools"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return // not a valid skill result
	}
	if result.Skill == "" {
		return // was a "list" action, not "apply"
	}

	// Check if there was a previous constraint
	if prev, active := r.skillConstraints.Active(runID); active {
		r.emit(runID, EventSkillConstraintDeactivated, map[string]any{
			"skill":  prev.SkillName,
			"reason": "replaced_by_new_skill",
		})
	}

	constraint := SkillConstraint{
		SkillName:    result.Skill,
		AllowedTools: result.AllowedTools,
	}
	r.skillConstraints.Activate(runID, constraint)
	r.emit(runID, EventSkillConstraintActivated, map[string]any{
		"skill":         result.Skill,
		"allowed_tools": result.AllowedTools,
		"unrestricted":  result.AllowedTools == nil,
	})
}

// drainSteering reads all pending steering messages from the run's steeringCh
// and appends them as user messages to the transcript. A steering.received event
// is emitted for each injected message. This is called at the top of each step
// before the next LLM call.
func (r *Runner) drainSteering(runID string, messages *[]Message) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	r.mu.RUnlock()
	if !ok {
		return
	}
	for {
		select {
		case msg := <-state.steeringCh:
			*messages = append(*messages, Message{Role: "user", Content: msg})
			r.snapshotRecordMessage(runID, "user", msg)
			r.setMessages(runID, *messages)
			r.emit(runID, EventSteeringReceived, map[string]any{"message": msg})
		default:
			return
		}
	}
}

func (r *Runner) completeRun(runID, output string) {
	// Clean up per-run workspace before terminal event (issue #324).
	r.runWorkspaceCleanup(runID)

	// Clean up deferred tool activations for this run
	r.activations.Cleanup(runID)

	// Clean up skill constraints for this run
	r.skillConstraints.Cleanup(runID)

	// Clean up per-run MCP servers
	r.closeScopedMCP(runID)

	// Store conversation messages for multi-turn support
	r.mu.RLock()
	state, ok := r.runs[runID]
	if ok {
		convID := state.run.ConversationID
		tenantID := state.run.TenantID
		agentID := state.run.AgentID
		msgs := copyMessages(state.messages)
		r.mu.RUnlock()

		r.mu.Lock()
		touchedAt := time.Now().UTC()
		r.conversations[convID] = msgs
		r.conversationTouched[convID] = touchedAt
		// Record ownership so that future StartRun callers with the same
		// ConversationID can be validated against the originating tenant+agent
		// (cross-tenant/cross-agent disclosure prevention, issue #221).
		r.conversationOwners[convID] = conversationOwner{
			tenantID: tenantID,
			agentID:  agentID,
		}
		r.pruneConversationMirrorLocked()
		r.mu.Unlock()

		// Persist to SQLite store if configured
		if r.config.ConversationStore != nil {
			storeMsgs := copyMessages(msgs) // defensive clone for untrusted store boundary
			usageTotals, costTotals := r.accountingTotals(runID)
			tokenCost := ConversationTokenCost{
				PromptTokens:     usageTotals.PromptTokensTotal,
				CompletionTokens: usageTotals.CompletionTokensTotal,
				CostUSD:          costTotals.CostUSDTotal,
			}
			if err := r.config.ConversationStore.SaveConversationWithCost(context.Background(), convID, storeMsgs, tokenCost); err != nil {
				if r.config.Logger != nil {
					r.config.Logger.Error("failed to persist conversation", "conv_id", convID, "error", err)
				}
			} else {
				// Wire tenant scoping: set workspace and tenant_id on the conversation row.
				if tenantID == "default" {
					tenantID = ""
				}
				if tenantID != "" {
					if err := r.config.ConversationStore.UpdateConversationMeta(context.Background(), convID, "", tenantID); err != nil {
						if r.config.Logger != nil {
							r.config.Logger.Error("failed to update conversation meta", "conv_id", convID, "error", err)
						}
					}
				}
			}
		}
		r.pruneConversationMirror()
	} else {
		r.mu.RUnlock()
	}

	// Audit trail: write run.completed and close the writer.
	if r.config.AuditTrailEnabled {
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunCompleted),
			Payload:   map[string]any{"status": "completed"},
		})
		r.closeAuditWriter(runID)
	}

	r.setStatus(runID, RunStatusCompleted, output, "")

	usageTotals, costTotals := r.accountingTotals(runID)

	// Efficiency suggestion: if the run used a named profile and the
	// efficiency score is below the threshold, emit a suggestion event.
	// This is suggest-only — no profile changes are applied automatically.
	r.maybeEmitProfileEfficiencySuggestion(runID, costTotals.CostUSDTotal)

	// Profile run history: persist completion record for analysis.
	r.persistProfileRun(runID, "completed", costTotals.CostUSDTotal)

	r.emit(runID, EventRunCompleted, map[string]any{
		"output":       output,
		"usage_totals": usageTotals,
		"cost_totals":  costTotals,
	})

	// S3 backup: upload JSONL after the terminal event is emitted and the
	// store has been updated. Runs in a goroutine; errors are non-fatal.
	r.backupRunToS3(runID)
	r.pruneCompletedRuns()
}

// maybeEmitProfileEfficiencySuggestion emits a profile.efficiency_suggestion
// event if the run used a named profile and the efficiency score is below the
// threshold. This is suggest-only — no profile changes are applied.
func (r *Runner) maybeEmitProfileEfficiencySuggestion(runID string, costUSD float64) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return
	}
	profileName := state.profileName
	steps := state.currentStep
	r.mu.RUnlock()

	if profileName == "" {
		return // No profile — nothing to suggest.
	}

	score := profiles.ScoreEfficiency(steps, costUSD)
	if !profiles.ShouldEmitSuggestion(score) {
		return // Score is fine — no suggestion needed.
	}

	r.emit(runID, EventProfileEfficiencySuggestion, map[string]any{
		"profile_name":     profileName,
		"run_id":           runID,
		"efficiency_score": score,
		"steps":            steps,
		"cost_usd":         costUSD,
	})
}

// persistProfileRun persists a profile run record to the ProfileRunStore when
// the run has a non-empty profile name. It is a no-op when ProfileRunStore is
// nil or the run has no profile name.  Errors are non-fatal: logged but never
// propagated so that persistence failures never affect the run outcome.
func (r *Runner) persistProfileRun(runID string, runStatus string, costUSD float64) {
	if r.config.ProfileRunStore == nil {
		return
	}

	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return
	}
	profileName := state.profileName
	steps := state.currentStep
	startedAt := state.run.CreatedAt
	// Collect used tool names from the event log (EventToolCallStarted payloads).
	var usedTools []string
	for _, evt := range state.events {
		if evt.Type == EventToolCallStarted {
			if name, ok := evt.Payload["tool"].(string); ok && name != "" {
				usedTools = append(usedTools, name)
			}
		}
	}
	r.mu.RUnlock()

	if profileName == "" {
		return
	}

	finishedAt := time.Now().UTC()
	recordID := fmt.Sprintf("%s:%s", profileName, runID)

	stats := profiles.RunStats{
		RunID:       runID,
		ProfileName: profileName,
		Steps:       steps,
		CostUSD:     costUSD,
		UsedTools:   usedTools,
	}
	completion := profiles.RunCompletionData{
		RecordID:   recordID,
		Status:     runStatus,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	rec := profiles.BuildProfileRunRecord(stats, completion)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.config.ProfileRunStore.RecordProfileRun(ctx, rec); err != nil {
		if r.config.Logger != nil {
			r.config.Logger.Error("failed to persist profile run",
				"run_id", runID,
				"profile", profileName,
				"error", err,
			)
		}
	}
}

// backupRunToS3 uploads the run's events as JSONL to S3 via the configured
// S3Uploader. The upload is performed synchronously and errors are logged but
// never propagated to callers — backup failures must never block the run loop.
// This is a no-op when S3Uploader is nil or when the run store is not set.
func (r *Runner) backupRunToS3(runID string) {
	if r.config.S3Uploader == nil || r.config.Store == nil {
		return
	}

	// Read convID under the lock.
	r.mu.RLock()
	state, ok := r.runs[runID]
	var convID string
	if ok {
		convID = state.run.ConversationID
	}
	r.mu.RUnlock()

	if !ok || convID == "" {
		return
	}

	// Upload in a goroutine so the terminal-event path is never blocked by
	// network I/O. Errors are logged (non-fatal).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := r.config.S3Uploader.UploadRun(ctx, r.config.Store, convID, runID); err != nil {
			if r.config.Logger != nil {
				r.config.Logger.Error("s3 backup failed", "run_id", runID, "conv_id", convID, "error", err)
			}
		}
	}()
}

// runWorkspaceCleanup calls the per-run workspace cleanup function exactly once
// before the terminal event is emitted. It is idempotent: if no cleanup is
// registered (no workspace was provisioned) this is a no-op.
func (r *Runner) runWorkspaceCleanup(runID string) {
	r.mu.Lock()
	var cleanupFn func()
	if st, ok := r.runs[runID]; ok {
		cleanupFn = st.workspaceCleanup
		st.workspaceCleanup = nil // clear to prevent double-call
	}
	r.mu.Unlock()
	if cleanupFn != nil {
		cleanupFn()
	}
}

// closeScopedMCP closes the per-run scoped MCP registry if one was configured
// for this run, and removes its tools from the global tool registry so the
// same server name can be re-registered on subsequent runs. It is a no-op when
// no scoped registry exists.
func (r *Runner) closeScopedMCP(runID string) {
	r.mu.Lock()
	state, ok := r.runs[runID]
	var scoped *ScopedMCPRegistry
	if ok {
		scoped = state.scopedMCPRegistry
		state.scopedMCPRegistry = nil
	}
	r.mu.Unlock()
	if scoped != nil {
		// Deregister per-run tools from whichever registry they were registered
		// into (per-run when provisioning happened, otherwise the global registry)
		// BEFORE closing the scoped registry, so subsequent runs can register the
		// same server names without hitting the "already connected" error.
		toolReg := r.toolsForRun(runID)
		for _, serverName := range scoped.PerRunServerNames() {
			toolReg.UnregisterMCPServer(serverName)
		}
		_ = scoped.Close()
	}
}

func (r *Runner) closeAllScopedMCP() {
	r.mu.RLock()
	runIDs := make([]string, 0, len(r.runs))
	for runID, state := range r.runs {
		if state != nil && state.scopedMCPRegistry != nil {
			runIDs = append(runIDs, runID)
		}
	}
	r.mu.RUnlock()

	for _, runID := range runIDs {
		r.closeScopedMCP(runID)
	}
}

// snapshotRecordToolCall records a tool call in the run's snapshot builder when
// ErrorChainEnabled is set. It is a no-op when ErrorChainEnabled is false.
func (r *Runner) snapshotRecordToolCall(runID, name, callID, args, errMsg string) {
	if !r.config.ErrorChainEnabled {
		return
	}
	r.mu.RLock()
	state, ok := r.runs[runID]
	var sb *errorchain.SnapshotBuilder
	if ok {
		sb = state.snapshotBuilder
	}
	r.mu.RUnlock()
	if sb != nil {
		sb.RecordToolCall(name, callID, args, errMsg)
	}
}

// snapshotRecordMessage records a message in the run's snapshot builder when
// ErrorChainEnabled is set. It is a no-op when ErrorChainEnabled is false.
func (r *Runner) snapshotRecordMessage(runID, role, content string) {
	if !r.config.ErrorChainEnabled {
		return
	}
	r.mu.RLock()
	state, ok := r.runs[runID]
	var sb *errorchain.SnapshotBuilder
	if ok {
		sb = state.snapshotBuilder
	}
	r.mu.RUnlock()
	if sb != nil {
		sb.RecordMessage(role, content)
	}
}

// emitContextWindowSnapshot emits a context.window.snapshot event after an LLM
// turn. It builds a token breakdown using the rune/4 estimation heuristic for
// all fields except the provider-reported prompt token count (when available).
//
// The maxContextTokens is resolved in priority order:
//  1. Provider catalog (via providerRegistry.MaxContextTokens) if the registry is set.
//  2. RunnerConfig.ModelContextWindow fallback.
//
// A context.window.warning event is also emitted when ContextWindowWarningThreshold
// is non-zero and the usage ratio exceeds the threshold.
func (r *Runner) emitContextWindowSnapshot(
	runID string,
	step int,
	model string,
	systemPromptText string,
	turnMessages []Message,
	result CompletionResult,
) {
	// Determine max context tokens: prefer catalog, fall back to config.
	maxCtxTokens := r.config.ModelContextWindow
	if r.providerRegistry != nil {
		if catalogMax, ok := r.providerRegistry.MaxContextTokens(model); ok && catalogMax > 0 {
			maxCtxTokens = catalogMax
		}
	}

	// Extract provider-reported prompt token count from the result.
	providerPromptTokens := 0
	providerReported := false
	if result.Usage != nil && result.Usage.PromptTokens > 0 {
		providerPromptTokens = result.Usage.PromptTokens
		providerReported = result.UsageStatus == UsageStatusProviderReported || result.UsageStatus == ""
	}

	// Build message list for estimation (exclude system messages from turnMessages
	// since we handle systemPromptText separately).
	msgs := make([]contextwindow.MessageForEstimate, 0, len(turnMessages))
	for _, m := range turnMessages {
		if m.Role == "system" {
			// System messages (including memory snippets injected as system)
			// are counted in system prompt tokens.
			systemPromptText += " " + m.Content
			continue
		}
		msgs = append(msgs, contextwindow.MessageForEstimate{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	snap := contextwindow.BuildSnapshot(
		step,
		systemPromptText,
		msgs,
		providerPromptTokens,
		providerReported,
		maxCtxTokens,
	)

	r.emit(runID, EventContextWindowSnapshot, contextwindow.SnapshotToPayload(snap))

	// Emit warning when threshold is configured and usage exceeds it.
	if r.config.ContextWindowWarningThreshold > 0 && snap.UsageRatio >= r.config.ContextWindowWarningThreshold {
		tokensUsed := snap.EstimatedTotalTokens
		if snap.ProviderReported {
			tokensUsed = snap.ProviderReportedTokens
		}
		r.emit(runID, EventContextWindowWarning, map[string]any{
			"step":               step,
			"usage_ratio":        snap.UsageRatio,
			"threshold":          r.config.ContextWindowWarningThreshold,
			"provider_reported":  snap.ProviderReported,
			"tokens_used":        tokensUsed,
			"max_context_tokens": maxCtxTokens,
		})
	}
}

func (r *Runner) failRun(runID string, err error) {
	if err == nil {
		err = errors.New("run failed")
	}

	// Clean up per-run workspace before terminal event (issue #324).
	r.runWorkspaceCleanup(runID)

	// Clean up deferred tool activations for this run
	r.activations.Cleanup(runID)

	// Clean up skill constraints for this run
	r.skillConstraints.Cleanup(runID)

	// Clean up per-run MCP servers
	r.closeScopedMCP(runID)

	// Emit error.context before run.failed when ErrorChainEnabled.
	if r.config.ErrorChainEnabled {
		r.mu.RLock()
		state, ok := r.runs[runID]
		var sb *errorchain.SnapshotBuilder
		if ok {
			sb = state.snapshotBuilder
		}
		r.mu.RUnlock()
		if sb != nil {
			ce := errorchain.NewChainedError(errorchain.ClassProvider, err.Error(), nil)
			payload := errorchain.BuildErrorContextPayload(ce, sb)
			r.emit(runID, EventErrorContext, payload)
		}
	}

	// Audit trail: write run.failed and close the writer.
	if r.config.AuditTrailEnabled {
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunFailed),
			Payload:   map[string]any{"error": err.Error(), "status": "failed"},
		})
		r.closeAuditWriter(runID)
	}

	r.setStatus(runID, RunStatusFailed, "", err.Error())

	usageTotals, costTotals := r.accountingTotals(runID)

	// Profile run history: persist failure record for analysis.
	r.persistProfileRun(runID, "failed", costTotals.CostUSDTotal)

	r.emit(runID, EventRunFailed, map[string]any{
		"error":        err.Error(),
		"usage_totals": usageTotals,
		"cost_totals":  costTotals,
	})

	// S3 backup: upload JSONL after the terminal event is emitted.
	// Runs in a goroutine; errors are non-fatal.
	r.backupRunToS3(runID)
	r.pruneCompletedRuns()
}

// failRunMaxSteps is a specialisation of failRun used when the step loop
// exhausts its budget.  The run.failed event carries a structured
// reason="max_steps_reached" and max_steps field so clients can distinguish
// this terminal state from other failures without parsing the error string.
func (r *Runner) failRunMaxSteps(runID string, maxSteps int) {
	err := fmt.Errorf("max steps (%d) reached", maxSteps)

	// Clean up per-run workspace before terminal event (issue #324).
	r.runWorkspaceCleanup(runID)

	// Clean up deferred tool activations for this run
	r.activations.Cleanup(runID)

	// Clean up skill constraints for this run
	r.skillConstraints.Cleanup(runID)

	// Clean up per-run MCP servers
	r.closeScopedMCP(runID)
	// Audit trail: write run.failed and close the writer.
	if r.config.AuditTrailEnabled {
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunFailed),
			Payload: map[string]any{
				"error":  err.Error(),
				"reason": "max_steps_reached",
				"status": "failed",
			},
		})
		r.closeAuditWriter(runID)
	}

	r.setStatus(runID, RunStatusFailed, "", err.Error())

	usageTotals, costTotals := r.accountingTotals(runID)

	// Profile run history: persist partial record (max steps reached) for analysis.
	r.persistProfileRun(runID, "partial", costTotals.CostUSDTotal)

	r.emit(runID, EventRunFailed, map[string]any{
		"error":        err.Error(),
		"reason":       "max_steps_reached",
		"max_steps":    maxSteps,
		"usage_totals": usageTotals,
		"cost_totals":  costTotals,
	})

	// S3 backup: upload JSONL after the terminal event is emitted.
	// Runs in a goroutine; errors are non-fatal.
	r.backupRunToS3(runID)
	r.pruneCompletedRuns()
}

// failRunMaxTurns is a specialisation of failRun used when the step loop
// exhausts its MaxTurns budget. The run.failed event carries a structured
// reason="max_turns_exhausted" and max_turns field so clients can distinguish
// this terminal state from other failures without parsing the error string.
func (r *Runner) failRunMaxTurns(runID string, maxTurns int) {
	err := fmt.Errorf("max turns (%d) reached", maxTurns)

	// Clean up per-run workspace before terminal event (issue #324).
	r.runWorkspaceCleanup(runID)

	// Clean up deferred tool activations for this run
	r.activations.Cleanup(runID)

	// Clean up skill constraints for this run
	r.skillConstraints.Cleanup(runID)

	// Clean up per-run MCP servers
	r.closeScopedMCP(runID)
	// Audit trail: write run.failed and close the writer.
	if r.config.AuditTrailEnabled {
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunFailed),
			Payload: map[string]any{
				"error":  err.Error(),
				"reason": "max_turns_exhausted",
				"status": "failed",
			},
		})
		r.closeAuditWriter(runID)
	}

	r.setStatus(runID, RunStatusFailed, "", err.Error())

	usageTotals, costTotals := r.accountingTotals(runID)

	// Profile run history: persist partial record (max turns exhausted) for analysis.
	r.persistProfileRun(runID, "partial", costTotals.CostUSDTotal)

	r.emit(runID, EventRunFailed, map[string]any{
		"error":        err.Error(),
		"reason":       "max_turns_exhausted",
		"max_turns":    maxTurns,
		"usage_totals": usageTotals,
		"cost_totals":  costTotals,
	})

	// S3 backup: upload JSONL after the terminal event is emitted.
	// Runs in a goroutine; errors are non-fatal.
	r.backupRunToS3(runID)
	r.pruneCompletedRuns()
}

// cancelledRun emits the run.cancelled terminal event and sets the run's status
// to RunStatusCancelled. It mirrors the structure of failRun but uses the
// dedicated cancelled event and status rather than failed.
func (r *Runner) cancelledRun(runID string) {
	// Clean up per-run workspace before terminal event (issue #324).
	r.runWorkspaceCleanup(runID)

	// Clean up deferred tool activations for this run.
	r.activations.Cleanup(runID)

	// Clean up skill constraints for this run.
	r.skillConstraints.Cleanup(runID)

	// Clean up per-run MCP servers.
	r.closeScopedMCP(runID)

	// Audit trail: write run.cancelled and close the writer.
	if r.config.AuditTrailEnabled {
		r.writeAudit(runID, audittrail.AuditRecord{
			RunID:     runID,
			EventType: string(EventRunCancelled),
			Payload:   map[string]any{"status": "cancelled"},
		})
		r.closeAuditWriter(runID)
	}

	r.setStatus(runID, RunStatusCancelled, "", "")

	usageTotals, costTotals := r.accountingTotals(runID)
	r.emit(runID, EventRunCancelled, map[string]any{
		"usage_totals": usageTotals,
		"cost_totals":  costTotals,
	})
	r.pruneCompletedRuns()
}

// CancelRun requests cooperative cancellation of the run identified by runID.
// The run's in-flight provider call or tool execution is interrupted via
// context cancellation. The run will transition to RunStatusCancelled and emit
// a run.cancelled event asynchronously.
//
// Errors:
//   - ErrRunNotFound — the run does not exist.
//
// Idempotency: if the run is already terminal (completed, failed, or cancelled),
// or if CancelRun is called multiple times, the call is a no-op and returns nil.
func (r *Runner) CancelRun(runID string) error {
	r.mu.RLock()
	state, ok := r.runs[runID]
	var status RunStatus
	if ok {
		status = state.run.Status
	}
	r.mu.RUnlock()

	if !ok {
		return ErrRunNotFound
	}

	// If the run is already in a terminal state, cancellation is a no-op.
	if status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled {
		return nil
	}

	// Look up and call the cancel function. If the run just finished and its
	// cancel function was already deleted, this is a safe no-op.
	if cancelFn, loaded := r.cancelFuncs.Load(runID); loaded {
		cancelFn.(context.CancelFunc)()
	}
	return nil
}

// Shutdown gracefully stops the runner. It signals the poolDispatcher (if
// running) to exit, then waits until all in-flight execute() goroutines have
// returned. Shutdown is idempotent: subsequent calls are no-ops and return nil.
// The ctx controls how long Shutdown will wait for in-flight runs; on
// ctx.Done(), all active runs are cooperatively cancelled and ctx.Err() is
// returned.
//
// After Shutdown returns, calls to StartRun and ContinueRun will return
// ErrRunnerClosed.
func (r *Runner) Shutdown(ctx context.Context) error {
	// Signal poolDispatcher and enqueueRun to stop accepting new work.
	r.shutdownOnce.Do(func() { close(r.done) })

	// Wait for all in-flight goroutines to finish.
	inflightDone := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(inflightDone)
	}()

	select {
	case <-inflightDone:
		r.closeAllScopedMCP()
		return errors.Join(r.shutdownToolRegistriesOnce(ctx), r.closeAuditBucketsOnce())
	case <-ctx.Done():
		// Context expired — cancel all remaining in-flight runs cooperatively.
		r.cancelFuncs.Range(func(_, v any) bool {
			if cancel, ok := v.(context.CancelFunc); ok {
				cancel()
			}
			return true
		})
		r.closeAllScopedMCP()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		return errors.Join(ctx.Err(), r.shutdownToolRegistriesOnce(cleanupCtx), r.closeAuditBucketsOnce())
	}
}

func (r *Runner) shutdownToolRegistriesOnce(ctx context.Context) error {
	r.toolShutdownOnce.Do(func() {
		r.toolShutdownErr = r.shutdownToolRegistries(ctx)
	})
	return r.toolShutdownErr
}

func (r *Runner) shutdownToolRegistries(ctx context.Context) error {
	registries := map[*Registry]struct{}{}
	if r.tools != nil {
		registries[r.tools] = struct{}{}
	}
	r.mu.RLock()
	for _, state := range r.runs {
		if state != nil && state.perRunTools != nil {
			registries[state.perRunTools] = struct{}{}
		}
	}
	r.mu.RUnlock()

	var joined error
	for registry := range registries {
		if err := registry.Shutdown(ctx); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (r *Runner) recordAccounting(runID string, result CompletionResult, step int) map[string]any {
	turnUsage, usageStatus := normalizeTurnUsage(result)
	turnCostUSD, costStatus, pricingVersion := normalizeTurnCost(result, usageStatus)

	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return map[string]any{
			"step":                step,
			"usage_status":        usageStatus,
			"cost_status":         costStatus,
			"turn_usage":          completionUsageToMap(turnUsage),
			"turn_cost_usd":       turnCostUSD,
			"cumulative_usage":    completionUsageToMap(CompletionUsage{}),
			"cumulative_cost_usd": 0.0,
			"pricing_version":     pricingVersion,
		}
	}
	state.usageTotals.add(turnUsage)
	state.costTotals.CostUSDTotal += turnCostUSD
	state.costTotals.LastTurnCostUSD = turnCostUSD
	state.costTotals.CostStatus = costStatus
	if trimmedVersion := strings.TrimSpace(pricingVersion); trimmedVersion != "" {
		state.costTotals.PricingVersion = trimmedVersion
	}
	usageTotals := state.usageTotals.runTotals()
	costTotals := state.costTotals
	cumulativeUsage := state.usageTotals.completionUsage()
	state.run.UsageTotals = &usageTotals
	state.run.CostTotals = &costTotals
	r.mu.Unlock()

	return map[string]any{
		"step":                step,
		"usage_status":        usageStatus,
		"cost_status":         costStatus,
		"turn_usage":          completionUsageToMap(turnUsage),
		"turn_cost_usd":       turnCostUSD,
		"cumulative_usage":    completionUsageToMap(cumulativeUsage),
		"cumulative_cost_usd": costTotals.CostUSDTotal,
		"pricing_version":     costTotals.PricingVersion,
	}
}

// completionUsageToMap converts a CompletionUsage struct into a map[string]any
// using its JSON representation. This breaks all pointer aliases: the resulting
// map contains only scalar values (float64 for JSON numbers) that are safe for
// insertion into event payloads distributed to multiple subscribers.
// CompletionUsage contains only numeric types so marshal cannot fail in practice.
func completionUsageToMap(u CompletionUsage) map[string]any {
	b, err := json.Marshal(u)
	if err != nil {
		return map[string]any{
			"prompt_tokens":     u.PromptTokens,
			"completion_tokens": u.CompletionTokens,
			"total_tokens":      u.TotalTokens,
		}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{
			"prompt_tokens":     u.PromptTokens,
			"completion_tokens": u.CompletionTokens,
			"total_tokens":      u.TotalTokens,
		}
	}
	return m
}

func normalizeTurnUsage(result CompletionResult) (CompletionUsage, UsageStatus) {
	if result.Usage == nil {
		return CompletionUsage{}, UsageStatusProviderUnreported
	}
	usage := *result.Usage
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	status := result.UsageStatus
	if status == "" {
		status = UsageStatusProviderReported
	}
	return usage, status
}

func normalizeTurnCost(result CompletionResult, usageStatus UsageStatus) (float64, CostStatus, string) {
	status := result.CostStatus
	if status == "" {
		if usageStatus == UsageStatusProviderUnreported {
			status = CostStatusProviderUnreported
		} else if result.Cost != nil || result.CostUSD != nil {
			status = CostStatusAvailable
		} else {
			status = CostStatusUnpricedModel
		}
	}
	if status != CostStatusAvailable {
		return 0, status, pricingVersionFromResult(result)
	}

	total := 0.0
	if result.Cost != nil {
		total = result.Cost.TotalUSD
	}
	if result.CostUSD != nil {
		total = *result.CostUSD
	}
	return total, status, pricingVersionFromResult(result)
}

func pricingVersionFromResult(result CompletionResult) string {
	if result.Cost == nil {
		return ""
	}
	return strings.TrimSpace(result.Cost.PricingVersion)
}

func (r *Runner) accountingTotals(runID string) (RunUsageTotals, RunCostTotals) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.runs[runID]
	if !ok {
		return RunUsageTotals{}, RunCostTotals{CostStatus: CostStatusPending}
	}
	usage := state.usageTotals.runTotals()
	cost := state.costTotals
	if cost.CostStatus == "" {
		cost.CostStatus = CostStatusPending
	}
	return usage, cost
}

// exceedsCostCeiling reports whether the cumulative cost for the given run has
// reached or exceeded the per-run cost ceiling (max_cost_usd). Returns false
// when no ceiling is set (maxCostUSD == 0) or when cost data is unavailable
// (unpriced model or provider-unreported cost).
func (r *Runner) exceedsCostCeiling(runID string) bool {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return false
	}
	maxCost := state.maxCostUSD
	total := state.costTotals.CostUSDTotal
	status := state.costTotals.CostStatus
	r.mu.RUnlock()

	if maxCost <= 0 {
		return false // no ceiling configured
	}
	if status != CostStatusAvailable {
		return false // cost data unavailable; don't halt on unknown costs
	}
	return total >= maxCost
}

func (a *usageTotalsAccumulator) add(turn CompletionUsage) {
	a.promptTokensTotal += turn.PromptTokens
	a.completionTokensTotal += turn.CompletionTokens
	a.totalTokens += turn.TotalTokens
	a.lastTurnTokens = turn.TotalTokens
	if turn.CachedPromptTokens != nil {
		a.cachedPromptTokens += *turn.CachedPromptTokens
		a.hasCachedPromptTokens = true
	}
	if turn.ReasoningTokens != nil {
		a.reasoningTokens += *turn.ReasoningTokens
		a.hasReasoningTokens = true
	}
	if turn.InputAudioTokens != nil {
		a.inputAudioTokens += *turn.InputAudioTokens
		a.hasInputAudioTokens = true
	}
	if turn.OutputAudioTokens != nil {
		a.outputAudioTokens += *turn.OutputAudioTokens
		a.hasOutputAudioTokens = true
	}
}

func (a usageTotalsAccumulator) runTotals() RunUsageTotals {
	return RunUsageTotals{
		PromptTokensTotal:     a.promptTokensTotal,
		CompletionTokensTotal: a.completionTokensTotal,
		TotalTokens:           a.totalTokens,
		LastTurnTokens:        a.lastTurnTokens,
	}
}

func (a usageTotalsAccumulator) completionUsage() CompletionUsage {
	out := CompletionUsage{
		PromptTokens:     a.promptTokensTotal,
		CompletionTokens: a.completionTokensTotal,
		TotalTokens:      a.totalTokens,
	}
	if a.hasCachedPromptTokens {
		n := a.cachedPromptTokens
		out.CachedPromptTokens = &n
	}
	if a.hasReasoningTokens {
		n := a.reasoningTokens
		out.ReasoningTokens = &n
	}
	if a.hasInputAudioTokens {
		n := a.inputAudioTokens
		out.InputAudioTokens = &n
	}
	if a.hasOutputAudioTokens {
		n := a.outputAudioTokens
		out.OutputAudioTokens = &n
	}
	return out
}

func (r *Runner) setStatus(runID string, status RunStatus, output, runErr string) {
	r.mu.Lock()

	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return
	}
	state.run.Status = status
	state.run.Output = output
	state.run.Error = runErr
	state.run.UpdatedAt = time.Now().UTC()
	if shouldPersistWorkflowRecap(status) {
		state.run.Recap = buildWorkflowRecap(state.run, state.messages, state.events)
	} else {
		state.run.Recap = nil
	}
	r.mu.Unlock()

	// Persist the updated run state to the store (non-fatal, called after unlock).
	r.storeUpdateRun(runID)
}

func (r *Runner) setMessages(runID string, messages []Message) {
	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return
	}
	// setMessages replaces the canonical run context. We deep-clone at the
	// write boundary so callers can never retain ownership of state.messages.
	state.messages = copyMessages(messages)
	r.mu.Unlock()

	// Persist any new messages to the store (non-fatal, outside lock).
	r.storeAppendNewMessages(runID)
}

// GetRunMessages returns a snapshot of the messages for the given run.
// Returns nil when the run does not exist. The returned slice is a copy
// so callers cannot mutate the stored state.
func (r *Runner) GetRunMessages(runID string) []Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return nil
	}
	return copyMessages(state.messages)
}

// mergeDynamicRules merges runner-level and per-run dynamic rules into a single
// slice. Runner-level rules are returned first, per-run rules appended after.
// Both slices are read-only; a new slice is always allocated.
func mergeDynamicRules(runnerRules, reqRules []DynamicRule) []DynamicRule {
	if len(runnerRules) == 0 && len(reqRules) == 0 {
		return nil
	}
	merged := make([]DynamicRule, 0, len(runnerRules)+len(reqRules))
	merged = append(merged, runnerRules...)
	merged = append(merged, reqRules...)
	return merged
}

// evaluateDynamicRules examines the previous step's tool calls in messages,
// determines which DynamicRules have their trigger satisfied, and appends
// the fired rules' Content to out. It also emits a rule.injected event for
// each rule that fires.
//
// messages is the current conversation transcript at the start of this step.
// The method scans the last assistant message with ToolCalls to find which
// tool names were called in the previous step.
//
// Thread-safety: the method acquires r.mu exclusively for the brief window
// where it reads and mutates state.firedOnceRules.
func (r *Runner) evaluateDynamicRules(runID string, step int, messages []Message, out *strings.Builder) {
	// Read the active rules and already-fired FireOnce set under the lock.
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return
	}
	rules := state.dynamicRules
	if len(rules) == 0 {
		r.mu.RUnlock()
		return
	}
	// Snapshot the firedOnceRules set for read-only use (we will update under write lock below).
	firedOnce := make(map[string]bool, len(state.firedOnceRules))
	for id, v := range state.firedOnceRules {
		firedOnce[id] = v
	}
	r.mu.RUnlock()

	// Collect tool names from the last assistant message that has tool calls.
	// This represents the previous step's tool calls (or empty on step 1).
	prevToolNames := make(map[string]bool)
	prevTriggerTool := ""
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				prevToolNames[tc.Name] = true
				if prevTriggerTool == "" {
					prevTriggerTool = tc.Name // first match used in event payload
				}
			}
			break
		}
	}

	if len(prevToolNames) == 0 {
		// No tool calls in the previous step — no rule can fire yet.
		return
	}

	// Evaluate rules in order. Collect IDs of FireOnce rules that fire this step.
	var newFiredOnce []string
	for _, rule := range rules {
		if rule.ID == "" || rule.Content == "" {
			continue
		}
		// Skip FireOnce rules that have already fired.
		if rule.FireOnce && firedOnce[rule.ID] {
			continue
		}
		// Check if any trigger tool name matches a previous step tool call.
		triggerTool := ""
		for _, name := range rule.Trigger.ToolNames {
			if prevToolNames[name] {
				triggerTool = name
				break
			}
		}
		if triggerTool == "" {
			continue
		}
		// Rule fires: append its content.
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(rule.Content)
		// Track FireOnce rules.
		if rule.FireOnce {
			newFiredOnce = append(newFiredOnce, rule.ID)
		}
		// Emit observability event.
		r.emit(runID, EventRuleInjected, map[string]any{
			"rule_id":      rule.ID,
			"step":         step,
			"trigger_tool": triggerTool,
		})
	}

	// Update firedOnceRules under write lock.
	if len(newFiredOnce) > 0 {
		r.mu.Lock()
		if st, ok := r.runs[runID]; ok {
			for _, id := range newFiredOnce {
				st.firedOnceRules[id] = true
			}
		}
		r.mu.Unlock()
	}
}

func (r *Runner) promptContext(runID string) (string, *systemprompt.ResolvedPrompt, time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return "", nil, time.Now().UTC()
	}
	return state.staticSystemPrompt, state.promptResolved, state.run.CreatedAt
}

func (r *Runner) scopeKey(runID string) om.ScopeKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return om.ScopeKey{TenantID: "default", ConversationID: runID, AgentID: "default"}
	}
	// Normalize empty fields to the same defaults used by workingMemoryScopeFromContext
	// (internal/harness/tools/core/working_memory.go). Without this alignment a
	// tool-written entry (scope: tenant="default", conv=runID, agent="default") would
	// never be found by the runner's working-memory READ injection (scope: tenant="",
	// conv="", agent="") when the run has no explicit tenant/conversation/agent.
	tenantID := strings.TrimSpace(state.run.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}
	conversationID := strings.TrimSpace(state.run.ConversationID)
	if conversationID == "" {
		conversationID = runID
	}
	agentID := strings.TrimSpace(state.run.AgentID)
	if agentID == "" {
		agentID = "default"
	}
	return om.ScopeKey{
		TenantID:       tenantID,
		ConversationID: conversationID,
		AgentID:        agentID,
	}
}

func (r *Runner) runMetadata(runID string) htools.RunMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return htools.RunMetadata{RunID: runID, TenantID: "default", ConversationID: runID, AgentID: "default"}
	}
	return htools.RunMetadata{
		RunID:          state.run.ID,
		TenantID:       state.run.TenantID,
		ConversationID: state.run.ConversationID,
		AgentID:        state.run.AgentID,
	}
}

func (r *Runner) transcriptSnapshot(runID string, limit int, includeTools bool) htools.TranscriptSnapshot {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return htools.TranscriptSnapshot{
			RunID:          runID,
			TenantID:       "default",
			ConversationID: runID,
			AgentID:        "default",
			Messages:       []htools.TranscriptMessage{},
			GeneratedAt:    time.Now().UTC(),
		}
	}
	run := state.run
	messages := copyMessages(state.messages)
	r.mu.RUnlock()

	items := make([]htools.TranscriptMessage, 0, len(messages))
	for i, msg := range messages {
		if msg.IsMeta {
			continue // meta-messages are not visible in transcripts
		}
		if !includeTools && msg.Role == "tool" {
			continue
		}
		items = append(items, htools.TranscriptMessage{
			Index:      int64(i),
			Role:       msg.Role,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Content:    msg.Content,
		})
	}
	if limit > 0 && len(items) > limit {
		items = append([]htools.TranscriptMessage(nil), items[len(items)-limit:]...)
	}
	return htools.TranscriptSnapshot{
		RunID:          run.ID,
		TenantID:       run.TenantID,
		ConversationID: run.ConversationID,
		AgentID:        run.AgentID,
		Messages:       items,
		GeneratedAt:    time.Now().UTC(),
	}
}

func (r *Runner) loadConversationHistory(runID string) []Message {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return nil
	}
	convID := state.run.ConversationID
	msgs, found := r.conversations[convID]
	if found {
		r.mu.RUnlock()
		return copyMessages(msgs)
	}
	r.mu.RUnlock()

	// Fall through to persistent store
	if r.config.ConversationStore != nil {
		loaded, err := r.config.ConversationStore.LoadMessages(context.Background(), convID)
		if err != nil {
			if r.config.Logger != nil {
				r.config.Logger.Error("failed to load conversation from store", "conv_id", convID, "error", err)
			}
			return nil
		}
		if len(loaded) > 0 {
			return copyMessages(loaded)
		}
		owner, ownerErr := r.config.ConversationStore.GetConversationOwner(context.Background(), convID)
		if ownerErr != nil {
			if r.config.Logger != nil {
				r.config.Logger.Error("failed to load conversation owner from store", "conv_id", convID, "error", ownerErr)
			}
			return nil
		}
		if owner != nil {
			return copyMessages(loaded)
		}
	}
	return nil
}

func (r *Runner) conversationID(runID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return ""
	}
	return state.run.ConversationID
}

func (r *Runner) ConversationMessages(conversationID string) ([]Message, bool) {
	r.mu.RLock()
	msgs, ok := r.conversations[conversationID]
	if ok {
		r.mu.RUnlock()
		return copyMessages(msgs), true
	}
	r.mu.RUnlock()

	// Fall through to persistent store
	if r.config.ConversationStore != nil {
		loaded, err := r.config.ConversationStore.LoadMessages(context.Background(), conversationID)
		if err != nil {
			return nil, false
		}
		if len(loaded) > 0 {
			return copyMessages(loaded), true
		}
		owner, err := r.config.ConversationStore.GetConversationOwner(context.Background(), conversationID)
		if err != nil {
			return nil, false
		}
		if owner != nil {
			return copyMessages(loaded), true
		}
	}
	return nil, false
}

// GetConversationStore returns the configured conversation store, or nil.
func (r *Runner) GetConversationStore() ConversationStore {
	return r.config.ConversationStore
}

// RunContextStatus holds the context window status for a run.
type RunContextStatus struct {
	MessageCount    int    `json:"message_count"`
	EstimatedTokens int    `json:"estimated_tokens"`
	ContextPressure string `json:"context_pressure"`
}

// GetRunContextStatus returns the current context status for the given run.
// Returns ErrRunNotFound if the run does not exist.
func (r *Runner) GetRunContextStatus(runID string) (RunContextStatus, error) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return RunContextStatus{}, ErrRunNotFound
	}
	messages := copyMessages(state.messages)
	r.mu.RUnlock()

	totalTokens := 0
	for _, msg := range messages {
		runes := utf8.RuneCountInString(msg.Content)
		if runes > 0 {
			totalTokens += (runes + 3) / 4
		}
	}

	pressure := contextPressureLevel(totalTokens)
	return RunContextStatus{
		MessageCount:    len(messages),
		EstimatedTokens: totalTokens,
		ContextPressure: pressure,
	}, nil
}

func contextPressureLevel(estimatedTokens int) string {
	switch {
	case estimatedTokens > 60000:
		return "high"
	case estimatedTokens > 30000:
		return "medium"
	default:
		return "low"
	}
}

// CompactRunRequest holds the parameters for a CompactRun call.
type CompactRunRequest struct {
	// Mode must be one of "strip", "summarize", or "hybrid". Defaults to "strip".
	Mode     string
	KeepLast int
}

// CompactRunResult holds the result of a CompactRun call.
type CompactRunResult struct {
	MessagesRemoved int `json:"messages_removed"`
}

// CompactRun triggers in-memory context compaction on an active run.
// Returns ErrRunNotFound if the run does not exist, ErrRunNotActive if the
// run is not currently active (running or waiting for user input).
func (r *Runner) CompactRun(ctx context.Context, runID string, req CompactRunRequest) (CompactRunResult, error) {
	mode := req.Mode
	if mode == "" {
		mode = "strip"
	}
	if mode != "strip" && mode != "summarize" && mode != "hybrid" {
		return CompactRunResult{}, fmt.Errorf("mode must be one of: strip, summarize, hybrid")
	}

	keepLast := req.KeepLast
	if keepLast <= 0 {
		keepLast = 4
	}

	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return CompactRunResult{}, ErrRunNotFound
	}
	status := state.run.Status
	r.mu.RUnlock()

	if status != RunStatusRunning && status != RunStatusWaitingForUser {
		return CompactRunResult{}, ErrRunNotActive
	}

	// Serialize with auto-compact to prevent concurrent mutations.
	state.compactMu.Lock()
	defer state.compactMu.Unlock()

	// Re-read messages under compactMu.
	r.mu.RLock()
	messages := copyMessages(state.messages)
	r.mu.RUnlock()

	// Convert messages to TranscriptMessages for the compaction logic.
	snap := messagesAsTranscriptSnapshot(messages)
	if len(snap) == 0 {
		return CompactRunResult{}, nil
	}

	// Snapshot the per-request Summarizer model from runState so that manual
	// CompactRun calls honour the RoleModels.Summarizer override, not just
	// the runner-level default (fix for HIGH issue in #25).
	r.mu.RLock()
	summarizerModel := state.resolvedRoleModels.Summarizer
	r.mu.RUnlock()

	beforeCount := len(snap)
	summarizer := r.newMessageSummarizerWithModel(summarizerModel)
	compacted, err := compactMessagesHTTP(ctx, snap, mode, keepLast, summarizer)
	if err != nil {
		return CompactRunResult{}, fmt.Errorf("compaction failed: %w", err)
	}

	// Convert compacted TranscriptMessages back to harness Messages.
	newMessages := transcriptMessagesToHarness(compacted)
	r.setMessages(runID, newMessages)

	removed := beforeCount - len(compacted)
	if removed < 0 {
		removed = 0
	}
	return CompactRunResult{MessagesRemoved: removed}, nil
}

// messagesForStep returns a fresh snapshot of the canonical state.messages
// under compactMu. execute() must call this at step boundaries so CompactRun
// and other message replacement paths remain the single source of truth.
func (r *Runner) messagesForStep(state *runState) []Message {
	state.compactMu.Lock()
	msgs := copyMessages(state.messages)
	state.compactMu.Unlock()
	return msgs
}

// autoCompactMessages performs compaction on the run's messages under compactMu.
// It tries hybrid (or configured) mode first and falls back to strip on error.
// The per-request Summarizer role model override stored in runState is honoured
// so that a per-request RoleModels.Summarizer is not silently ignored.
func (r *Runner) autoCompactMessages(ctx context.Context, runID string, messages []Message) ([]Message, error) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrRunNotFound
	}

	state.compactMu.Lock()
	defer state.compactMu.Unlock()

	snap := messagesAsTranscriptSnapshot(messages)
	if len(snap) == 0 {
		return messages, nil
	}

	mode := r.config.AutoCompactMode
	keepLast := r.config.AutoCompactKeepLast

	// Use the per-request summarizer model if one was resolved for this run.
	// newMessageSummarizerWithModel falls back to runner-level config when the
	// override is empty, preserving existing behaviour.
	summarizer := r.newMessageSummarizerWithModel(state.resolvedRoleModels.Summarizer)

	compacted, err := compactMessagesHTTP(ctx, snap, mode, keepLast, summarizer)
	if err != nil && mode != "strip" {
		// Fallback to strip mode if hybrid/summarize fails.
		compacted, err = compactMessagesHTTP(ctx, snap, "strip", keepLast, nil)
	}
	if err != nil {
		return nil, err
	}

	return transcriptMessagesToHarness(compacted), nil
}

// messagesAsTranscriptSnapshot converts harness Messages to the tool-layer
// TranscriptMessage format used by the compaction logic.
func messagesAsTranscriptSnapshot(msgs []Message) []htools.TranscriptMessage {
	result := make([]htools.TranscriptMessage, 0, len(msgs))
	for i, m := range msgs {
		if m.IsMeta {
			continue
		}
		tm := htools.TranscriptMessage{
			Index:      int64(i),
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		result = append(result, tm)
	}
	return result
}

// transcriptMessagesToHarness converts tool-layer TranscriptMessages back to
// harness Messages suitable for setMessages.
func transcriptMessagesToHarness(msgs []htools.TranscriptMessage) []Message {
	result := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, Message{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		})
	}
	return result
}

// compactMessagesHTTP applies the compaction strategy to transcript messages.
// It mirrors the logic inside the compact_history tool handler but operates
// directly on slices without a context-based reader/replacer.
func compactMessagesHTTP(
	ctx context.Context,
	msgs []htools.TranscriptMessage,
	mode string,
	keepLast int,
	summarizer htools.MessageSummarizer,
) ([]htools.TranscriptMessage, error) {
	turns := parseTurnsHTTP(msgs)
	prefixEnd, compactEnd := findCompactionBoundsHTTP(turns, keepLast)

	if compactEnd <= prefixEnd {
		// Nothing to compact — return the original slice.
		return msgs, nil
	}

	switch mode {
	case "strip":
		return compactStripHTTP(turns, prefixEnd, compactEnd), nil
	case "summarize":
		if summarizer == nil {
			return nil, fmt.Errorf("summarize mode requires a message summarizer (not configured)")
		}
		result, _, err := compactSummarizeHTTP(ctx, turns, prefixEnd, compactEnd, summarizer)
		return result, err
	case "hybrid":
		result, _, err := compactHybridHTTP(ctx, turns, prefixEnd, compactEnd, summarizer)
		return result, err
	default:
		return nil, fmt.Errorf("unknown mode: %s", mode)
	}
}

// httpTurn mirrors the tools-layer turn type for HTTP-based compaction.
type httpTurn struct {
	Messages []htools.TranscriptMessage
	Kind     string
}

func parseTurnsHTTP(msgs []htools.TranscriptMessage) []httpTurn {
	if len(msgs) == 0 {
		return nil
	}

	var turns []httpTurn
	i := 0

	for i < len(msgs) && msgs[i].Role == "system" {
		kind := "system_prefix"
		if msgs[i].Name == "compact_summary" {
			kind = "compact_summary"
		}
		turns = append(turns, httpTurn{
			Messages: []htools.TranscriptMessage{msgs[i]},
			Kind:     kind,
		})
		i++
	}

	for i < len(msgs) {
		msg := msgs[i]

		switch msg.Role {
		case "user":
			turns = append(turns, httpTurn{
				Messages: []htools.TranscriptMessage{msg},
				Kind:     "user",
			})
			i++

		case "assistant":
			t := httpTurn{
				Messages: []htools.TranscriptMessage{msg},
				Kind:     "assistant_text",
			}
			i++

			hasToolResults := false
			for i < len(msgs) && (msgs[i].Role == "tool" || msgs[i].Role == "system") {
				if msgs[i].Role == "tool" {
					hasToolResults = true
					t.Messages = append(t.Messages, msgs[i])
					i++
				} else if msgs[i].Role == "system" {
					t.Messages = append(t.Messages, msgs[i])
					i++
				} else {
					break
				}
			}
			if hasToolResults {
				t.Kind = "assistant_tool"
			}
			turns = append(turns, t)

		case "system":
			kind := "system_prefix"
			if msg.Name == "compact_summary" {
				kind = "compact_summary"
			}
			turns = append(turns, httpTurn{
				Messages: []htools.TranscriptMessage{msg},
				Kind:     kind,
			})
			i++

		case "tool":
			turns = append(turns, httpTurn{
				Messages: []htools.TranscriptMessage{msg},
				Kind:     "assistant_tool",
			})
			i++

		default:
			turns = append(turns, httpTurn{
				Messages: []htools.TranscriptMessage{msg},
				Kind:     "user",
			})
			i++
		}
	}

	return turns
}

func findCompactionBoundsHTTP(turns []httpTurn, keepLast int) (prefixEnd, compactEnd int) {
	for prefixEnd < len(turns) {
		if turns[prefixEnd].Kind != "system_prefix" && turns[prefixEnd].Kind != "compact_summary" {
			break
		}
		prefixEnd++
	}

	nonPrefixCount := len(turns) - prefixEnd
	if nonPrefixCount <= keepLast {
		return prefixEnd, prefixEnd
	}

	compactEnd = len(turns) - keepLast
	return prefixEnd, compactEnd
}

func compactStripHTTP(turns []httpTurn, prefixEnd, compactEnd int) []htools.TranscriptMessage {
	var result []htools.TranscriptMessage

	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	stripped := 0
	for i := prefixEnd; i < compactEnd; i++ {
		t := turns[i]
		switch t.Kind {
		case "assistant_tool":
			if len(t.Messages) > 0 && strings.TrimSpace(t.Messages[0].Content) != "" {
				result = append(result, htools.TranscriptMessage{
					Index:   t.Messages[0].Index,
					Role:    "assistant",
					Content: t.Messages[0].Content,
				})
			}
			for _, m := range t.Messages {
				if m.Role == "tool" {
					stripped++
				}
			}
		default:
			result = append(result, t.Messages...)
		}
	}

	if stripped > 0 {
		result = append(result, htools.TranscriptMessage{
			Role:    "system",
			Name:    "compact_summary",
			Content: fmt.Sprintf("[context compacted: %d tool interactions stripped]", stripped),
		})
	}

	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result
}

func compactSummarizeHTTP(
	ctx context.Context,
	turns []httpTurn,
	prefixEnd, compactEnd int,
	summarizer htools.MessageSummarizer,
) ([]htools.TranscriptMessage, string, error) {
	var result []htools.TranscriptMessage

	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	var zoneMsgs []map[string]any
	for i := prefixEnd; i < compactEnd; i++ {
		for _, m := range turns[i].Messages {
			zoneMsgs = append(zoneMsgs, map[string]any{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}

	summary, err := summarizer.SummarizeMessages(ctx, zoneMsgs)
	if err != nil {
		return nil, "", err
	}

	result = append(result, htools.TranscriptMessage{
		Role:    "system",
		Name:    "compact_summary",
		Content: summary,
	})

	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result, summary, nil
}

func compactHybridHTTP(
	ctx context.Context,
	turns []httpTurn,
	prefixEnd, compactEnd int,
	summarizer htools.MessageSummarizer,
) ([]htools.TranscriptMessage, string, error) {
	var result []htools.TranscriptMessage

	for i := 0; i < prefixEnd; i++ {
		result = append(result, turns[i].Messages...)
	}

	const largeTokenThreshold = 500
	var removedContent []string
	stripped := 0

	for i := prefixEnd; i < compactEnd; i++ {
		t := turns[i]
		switch t.Kind {
		case "assistant_tool":
			if len(t.Messages) > 0 && strings.TrimSpace(t.Messages[0].Content) != "" {
				result = append(result, htools.TranscriptMessage{
					Index:   t.Messages[0].Index,
					Role:    "assistant",
					Content: t.Messages[0].Content,
				})
			}
			for _, m := range t.Messages {
				if m.Role != "tool" {
					continue
				}
				runes := utf8.RuneCountInString(m.Content)
				tokens := 0
				if runes > 0 {
					tokens = (runes + 3) / 4
				}
				if tokens > largeTokenThreshold {
					removedContent = append(removedContent, m.Content)
					stripped++
				} else {
					result = append(result, m)
				}
			}
		default:
			result = append(result, t.Messages...)
		}
	}

	var summary string
	if len(removedContent) > 0 {
		if summarizer != nil {
			var summaryMsgs []map[string]any
			for _, content := range removedContent {
				summaryMsgs = append(summaryMsgs, map[string]any{
					"role":    "tool",
					"content": content,
				})
			}
			var err error
			summary, err = summarizer.SummarizeMessages(ctx, summaryMsgs)
			if err != nil {
				summary = ""
			}
		}

		marker := fmt.Sprintf("[context compacted: %d large tool outputs removed]", stripped)
		if summary != "" {
			marker = fmt.Sprintf("[context compacted: %d large tool outputs summarized]\n%s", stripped, summary)
		}
		result = append(result, htools.TranscriptMessage{
			Role:    "system",
			Name:    "compact_summary",
			Content: marker,
		})
	}

	for i := compactEnd; i < len(turns); i++ {
		result = append(result, turns[i].Messages...)
	}

	return result, summary, nil
}

// SummarizeMessages makes a single LLM call to summarize the given messages.
// Returns a summary string suitable for use as a compact summary.
// The model is resolved from: runner-level config defaults < config RoleModels.Summarizer.
// Use SummarizeMessagesWithModel to supply a per-request override on top of that.
func (r *Runner) SummarizeMessages(ctx context.Context, messages []Message) (string, error) {
	return r.SummarizeMessagesWithModel(ctx, messages, "")
}

// SummarizeMessagesWithModel is like SummarizeMessages but accepts an explicit
// model override. When overrideModel is non-empty it takes precedence over both
// the runner-level DefaultModel and the config-level RoleModels.Summarizer.
// This is used to honour per-request RoleModels.Summarizer overrides during
// auto-compaction, where the resolved model is stored in runState.
func (r *Runner) SummarizeMessagesWithModel(ctx context.Context, messages []Message, overrideModel string) (string, error) {
	if r.provider == nil {
		return "", fmt.Errorf("provider not configured")
	}
	model := r.config.DefaultModel
	if model == "" {
		model = "gpt-4.1-mini"
	}
	// Apply Summarizer role model override when configured.
	if r.config.RoleModels.Summarizer != "" {
		model = r.config.RoleModels.Summarizer
	}
	// Per-request override wins over everything else.
	if overrideModel != "" {
		model = overrideModel
	}
	req := CompletionRequest{
		Model: model,
		Messages: append(copyMessages(messages), Message{
			Role:    "user",
			Content: "Please provide a concise summary of this conversation so far, suitable for use as context in a continuation. Include key facts, decisions, and outputs. Be concise.",
		}),
	}
	result, err := r.provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Content) == "" {
		return "", fmt.Errorf("empty summary from provider")
	}
	return result.Content, nil
}

// runnerMessageSummarizer adapts *Runner to the tools.MessageSummarizer interface.
// overrideModel, when non-empty, is passed to SummarizeMessagesWithModel so that
// per-request Summarizer role model overrides are honoured during compaction.
type runnerMessageSummarizer struct {
	runner        *Runner
	overrideModel string
}

func (s *runnerMessageSummarizer) SummarizeMessages(ctx context.Context, msgs []map[string]any) (string, error) {
	converted := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		msg := Message{}
		if v, ok := m["role"].(string); ok {
			msg.Role = v
		}
		if v, ok := m["content"].(string); ok {
			msg.Content = v
		}
		if v, ok := m["name"].(string); ok {
			msg.Name = v
		}
		if v, ok := m["tool_call_id"].(string); ok {
			msg.ToolCallID = v
		}
		converted = append(converted, msg)
	}
	return s.runner.SummarizeMessagesWithModel(ctx, converted, s.overrideModel)
}

// NewMessageSummarizer returns a tools.MessageSummarizer backed by this runner.
func (r *Runner) NewMessageSummarizer() htools.MessageSummarizer {
	return &runnerMessageSummarizer{runner: r}
}

// newMessageSummarizerWithModel returns a tools.MessageSummarizer that uses
// overrideModel for all summarization calls, taking precedence over the
// runner-level config. Pass "" to use the default resolution order.
func (r *Runner) newMessageSummarizerWithModel(overrideModel string) htools.MessageSummarizer {
	return &runnerMessageSummarizer{runner: r, overrideModel: overrideModel}
}

// GetSummarizer returns a MessageSummarizer backed by this runner, or nil if no
// provider is configured (which means summarization is not available).
func (r *Runner) GetSummarizer() htools.MessageSummarizer {
	if r.provider == nil {
		return nil
	}
	return &runnerMessageSummarizer{runner: r}
}

// ApprovalBroker returns the approval broker configured for this runner, or nil
// if none was set. This allows the HTTP server to share the same broker instance.
func (r *Runner) ApprovalBroker() ApprovalBroker {
	return r.config.ApprovalBroker
}

func (r *Runner) observeMemory(runID string, step int, messages []Message) {
	if r.config.MemoryManager == nil || r.config.MemoryManager.Mode() == om.ModeOff {
		return
	}
	scope := r.scopeKey(runID)
	converted := make([]om.TranscriptMessage, 0, len(messages))
	for i, msg := range messages {
		converted = append(converted, om.TranscriptMessage{
			Index:      int64(i),
			Role:       msg.Role,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Content:    msg.Content,
		})
	}
	r.emit(runID, EventMemoryObserveStarted, map[string]any{"step": step})
	out, err := r.config.MemoryManager.Observe(context.Background(), om.ObserveRequest{
		Scope:    scope,
		RunID:    runID,
		Messages: converted,
	})
	if err != nil {
		r.emit(runID, EventMemoryObserveFailed, map[string]any{"step": step, "error": err.Error()})
		return
	}
	r.emit(runID, EventMemoryObserveCompleted, map[string]any{
		"step":        step,
		"observed":    out.Observed,
		"reflected":   out.Reflected,
		"observation": out.Status.ObservationCount,
	})
	if out.Reflected {
		r.emit(runID, EventMemoryReflectionCompleted, map[string]any{"step": step})
	}
}

type runTranscriptReader struct {
	runner *Runner
	runID  string
}

func (r runTranscriptReader) Snapshot(limit int, includeTools bool) htools.TranscriptSnapshot {
	if r.runner == nil {
		return htools.TranscriptSnapshot{RunID: r.runID, GeneratedAt: time.Now().UTC()}
	}
	return r.runner.transcriptSnapshot(r.runID, limit, includeTools)
}

// emit appends one event to the canonical in-memory ledger and mirrors that
// same event to subscribers and the optional JSONL recorder.
func (r *Runner) emit(runID string, eventType EventType, payload map[string]any) {
	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return
	}

	// Drop post-terminal events to preserve forensic ordering. Provider
	// streaming callbacks and tool output goroutines can fire after
	// run.completed/run.failed; we gate them here so no orphan events are
	// appended to the forensic record after it is sealed.
	if state.terminated {
		r.mu.Unlock()
		return
	}
	journal := newEventJournal(r)
	delivery, deliver := journal.prepareLocked(state, runID, eventType, payload)
	publishTerminal := deliver && !delivery.dropped && IsTerminalEvent(eventType)
	r.mu.Unlock()
	if !deliver {
		return
	}
	if publishTerminal {
		journal.publishTerminal(delivery)
		r.pruneCompletedRuns()
	}
	journal.dispatch(delivery)
}

func (r *Runner) emitCompletionDelta(runID string, step int, delta CompletionDelta) {
	if delta.Content != "" {
		r.emit(runID, EventAssistantMessageDelta, map[string]any{
			"step":    step,
			"content": delta.Content,
		})
	}
	if delta.Reasoning != "" && r.config.CaptureReasoning {
		r.emit(runID, EventAssistantThinkingDelta, map[string]any{
			"step":    step,
			"content": delta.Reasoning,
		})
	}
	if delta.ToolCall.ID == "" && delta.ToolCall.Name == "" && delta.ToolCall.Arguments == "" {
		return
	}
	r.emit(runID, EventToolCallDelta, map[string]any{
		"step":      step,
		"index":     delta.ToolCall.Index,
		"call_id":   delta.ToolCall.ID,
		"tool":      delta.ToolCall.Name,
		"arguments": delta.ToolCall.Arguments,
	})
}

func (r *Runner) nextID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, uuid.New().String())
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"error":"failed to marshal tool error"}`
	}
	return string(data)
}

// auditLogPath returns the path for the audit.jsonl file under the given
// rollout directory, partitioned by the current UTC date.
func auditLogPath(rolloutDir string) string {
	return auditLogPathForDate(rolloutDir, time.Now().UTC().Format("2006-01-02"))
}

func auditLogPathForDate(rolloutDir, dateKey string) string {
	dateDir := rolloutDir + "/" + dateKey
	return dateDir + "/audit.jsonl"
}

func (r *Runner) auditWriterFor(now time.Time) (*audittrail.AuditWriter, error) {
	if !r.config.AuditTrailEnabled || r.config.RolloutDir == "" {
		return nil, nil
	}
	dateKey := now.UTC().Format("2006-01-02")

	r.auditMu.Lock()
	defer r.auditMu.Unlock()
	if bucket := r.auditBuckets[dateKey]; bucket != nil && bucket.writer != nil {
		return bucket.writer, nil
	}

	writer, err := audittrail.NewAuditWriter(auditLogPathForDate(r.config.RolloutDir, dateKey))
	if err != nil {
		return nil, err
	}
	r.auditBuckets[dateKey] = &auditBucket{writer: writer}
	return writer, nil
}

func (r *Runner) closeAuditBucketsOnce() error {
	r.auditShutdownOnce.Do(func() {
		r.auditShutdownErr = r.closeAuditBuckets()
	})
	return r.auditShutdownErr
}

func (r *Runner) closeAuditBuckets() error {
	r.auditMu.Lock()
	buckets := r.auditBuckets
	r.auditBuckets = make(map[string]*auditBucket)
	r.auditMu.Unlock()

	var joined error
	for _, bucket := range buckets {
		if bucket != nil && bucket.writer != nil {
			joined = errors.Join(joined, bucket.writer.Close())
		}
	}
	return joined
}

// writeAudit writes a record to the run's audit writer if audit trail is
// enabled and the writer is available. It never blocks the run loop.
//
// When a RedactionPipeline is configured, rec.Payload is passed through the
// pipeline with StorageModeRedacted semantics applied unconditionally. We
// never skip (drop) an audit entry regardless of the pipeline's keep result:
// dropping would break the hash chain. When the pipeline is nil the payload
// is written verbatim.
func (r *Runner) writeAudit(runID string, rec audittrail.AuditRecord) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return
	}
	aw := state.auditWriter
	r.mu.RUnlock()

	if aw == nil {
		return
	}

	// Apply redaction when the pipeline is configured. StorageModeRedacted is
	// the default mode for event types not listed in the EventClassConfig; the
	// pipeline may return keep=false for StorageModeNone events, but we always
	// write the entry to preserve the hash chain — the payload is cleared to an
	// empty map in that case so no content is leaked.
	if r.config.RedactionPipeline != nil && rec.Payload != nil {
		redacted, keep := redaction.RedactPayload(r.config.RedactionPipeline, rec.EventType, rec.Payload)
		if keep {
			rec.Payload = redacted
		} else {
			// StorageModeNone: write the entry with an empty payload so the
			// hash chain remains intact.
			rec.Payload = map[string]any{}
		}
	}

	// Errors are silently dropped to never impact the run loop.
	_ = aw.Write(rec)
}

// resolveRoleModels merges the per-request RoleModels with the runner-level
// RoleModels configuration. Request-level fields take precedence; empty fields
// fall back to the runner config. The returned RoleModels always reflects the
// highest-priority non-empty override for each role.
func (r *Runner) resolveRoleModels(req RunRequest) RoleModels {
	result := r.config.RoleModels // start from config defaults
	if req.RoleModels != nil {
		if req.RoleModels.Primary != "" {
			result.Primary = req.RoleModels.Primary
		}
		if req.RoleModels.Summarizer != "" {
			result.Summarizer = req.RoleModels.Summarizer
		}
	}
	return result
}

// closeAuditWriter detaches the run from the shared audit writer, if any.
func (r *Runner) closeAuditWriter(runID string) {
	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return
	}
	state.auditWriter = nil
	r.mu.Unlock()
}

// --- store.Store persistence helpers ---
// All helpers are non-fatal: errors are logged (when Logger is configured) but
// never propagate back to the run loop. A nil store is a no-op for all calls.

// storeCreateRun persists the initial run record to the configured store.
// Called once at the start of StartRun and ContinueRun after the run ID is assigned.
func (r *Runner) storeCreateRun(run Run) {
	if r.config.Store == nil {
		return
	}
	sr := runToStoreRun(run)
	if err := r.config.Store.CreateRun(context.Background(), sr); err != nil {
		if r.config.Logger != nil {
			r.config.Logger.Error("store: CreateRun failed", "run_id", run.ID, "error", err)
		}
	}
}

// storeUpdateRun persists the current run state (status, output, error) to the store.
// Called from setStatus after each status transition.
func (r *Runner) storeUpdateRun(runID string) {
	if r.config.Store == nil {
		return
	}
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return
	}
	run := state.run
	r.mu.RUnlock()

	sr := runToStoreRun(run)
	if err := r.config.Store.UpdateRun(context.Background(), sr); err != nil {
		if r.config.Logger != nil {
			r.config.Logger.Error("store: UpdateRun failed", "run_id", runID, "error", err)
		}
	}
}

func shouldPersistWorkflowRecap(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled
}

// storeAppendEvent persists a single event to the store.
// Called from emit() after the event is appended to state.events.
// Executed outside the lock to avoid increasing lock hold time.
func (r *Runner) storeAppendEvent(ev Event, seq uint64) {
	if r.config.Store == nil {
		return
	}
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		payloadJSON = []byte("{}")
	}
	se := &store.Event{
		Seq:       int(seq),
		RunID:     ev.RunID,
		EventID:   ev.ID,
		EventType: string(ev.Type),
		Payload:   string(payloadJSON),
		Timestamp: ev.Timestamp,
	}
	if err := r.config.Store.AppendEvent(context.Background(), se); err != nil {
		if r.config.Logger != nil {
			r.config.Logger.Error("store: AppendEvent failed",
				"run_id", ev.RunID, "event_type", string(ev.Type), "seq", seq, "error", err)
		}
	}
}

// storeAppendNewMessages appends any messages in the current run state that
// have not yet been persisted to the store. It tracks how many messages have
// already been stored via state.storedMsgCount and appends only the new tail.
// Must be called WITHOUT holding r.mu (it acquires its own read lock).
func (r *Runner) storeAppendNewMessages(runID string) {
	if r.config.Store == nil {
		return
	}
	r.mu.Lock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return
	}
	// Snapshot only the new messages (tail not yet stored).
	already := state.storedMsgCount
	current := copyMessages(state.messages)
	r.mu.Unlock()

	if len(current) <= already {
		return // nothing new
	}
	newMsgs := current[already:]
	ctx := context.Background()
	persisted := 0
	for i, m := range newMsgs {
		sm := messageToStoreMessage(m, runID, already+i)
		if err := r.config.Store.AppendMessage(ctx, sm); err != nil {
			if r.config.Logger != nil {
				r.config.Logger.Error("store: AppendMessage failed",
					"run_id", runID, "seq", already+i, "role", m.Role, "error", err)
			}
			// Stop on first error to preserve seq monotonicity.
			break
		}
		persisted++
	}
	if persisted > 0 {
		r.mu.Lock()
		if s, ok := r.runs[runID]; ok {
			s.storedMsgCount += persisted
		}
		r.mu.Unlock()
	}
}

// runToStoreRun converts a harness.Run to a store.Run.
func runToStoreRun(run Run) *store.Run {
	return &store.Run{
		ID:             run.ID,
		ConversationID: run.ConversationID,
		TenantID:       run.TenantID,
		AgentID:        run.AgentID,
		Model:          run.Model,
		ProviderName:   run.ProviderName,
		Prompt:         run.Prompt,
		Status:         store.RunStatus(run.Status),
		Output:         run.Output,
		Error:          run.Error,
		Recap:          cloneWorkflowRecap(run.Recap),
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	}
}

// messageToStoreMessage converts a harness.Message to a store.Message.
func messageToStoreMessage(m Message, runID string, seq int) *store.Message {
	var toolCallsJSON string
	if len(m.ToolCalls) > 0 {
		if data, err := json.Marshal(m.ToolCalls); err == nil {
			toolCallsJSON = string(data)
		}
	}
	return &store.Message{
		Seq:              seq,
		RunID:            runID,
		Role:             m.Role,
		Content:          m.Content,
		ToolCallsJSON:    toolCallsJSON,
		ToolCallID:       m.ToolCallID,
		Name:             m.Name,
		IsMeta:           m.IsMeta,
		IsCompactSummary: m.IsCompactSummary,
	}
}

// startRecorderGoroutine launches the per-run recorder goroutine that owns the
// JSONL write loop for rec. It writes events in Seq order, not channel-arrival
// order, so the on-disk ledger matches the canonical event order assigned by
// emit() even when concurrent goroutines race to enqueue non-terminal events.
// It populates state.recorderCh and state.recorderDone and must be called
// before go r.execute() so the channel is ready before any events are emitted.
func startRecorderGoroutine(state *runState, rec *rollout.Recorder) {
	ch := make(chan rollout.RecordableEvent, recorderChannelSize)
	done := make(chan struct{})
	state.recorderCh = ch
	state.recorderDone = done

	var once sync.Once
	state.closeRecorderOnce = func() { once.Do(func() { close(ch) }) }

	go func() {
		defer close(done)
		nextSeq := uint64(0)
		pending := make(map[uint64]rollout.RecordableEvent)

		record := func(ev rollout.RecordableEvent) {
			defer func() {
				if recover() != nil {
					// Keep recorder failures isolated from the run loop. The
					// canonical state.events ledger remains intact in memory.
				}
			}()
			rec.Record(ev)
		}

		flush := func() {
			for {
				ev, ok := pending[nextSeq]
				if !ok {
					if len(pending) == 0 {
						return
					}
					// We are holding a strictly-later event, so nextSeq can never
					// arrive. The send must have been dropped; skip the gap to keep
					// forward progress.
					minSeq := nextSeq
					first := true
					for seq := range pending {
						if first || seq < minSeq {
							minSeq = seq
							first = false
						}
					}
					nextSeq = minSeq
					continue
				}
				delete(pending, nextSeq)
				record(ev)
				nextSeq++
			}
		}

		for ev := range ch {
			pending[ev.Seq] = ev
			flush()
		}
		flush()
		rec.Close() //nolint:errcheck
	}()
}

// safeRecorderSend sends ev to ch using a non-blocking select, recovering from
// a send-on-closed-channel panic.  This can occur when a concurrent goroutine
// captures state.recorderCh under r.mu before the terminal path closes it.
// Returns true if the event was queued, false if the channel was full or closed.
func safeRecorderSend(ch chan rollout.RecordableEvent, ev rollout.RecordableEvent) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- ev:
		return true
	default:
		return false
	}
}

// knownWorkspaceTypes is the set of workspace types recognised by the per-run
// workspace selection feature. "local" and "worktree" are provisioned entirely
// within the runner process. "container" and "vm" require orchestrator-level
// configuration (symphd) — the runner records their type in events but the
// provisioning path for those types ultimately delegates to the orchestrator.
var knownWorkspaceTypes = map[string]bool{
	"local":     true,
	"worktree":  true,
	"container": true,
	"vm":        true,
}

// validateWorkspaceType returns an error when wsType is not in the known set.
// An empty string is valid and means "use server default" (no provisioning).
func validateWorkspaceType(wsType string) error {
	if wsType == "" {
		return nil
	}
	if knownWorkspaceTypes[wsType] {
		return nil
	}
	return fmt.Errorf("unsupported workspace_type %q: must be one of local, worktree, container, vm", wsType)
}

// resolveWorkspaceType returns the effective workspace type for a run.
// Precedence (highest to lowest):
//  1. RunRequest.WorkspaceType — explicit per-run override wins unconditionally.
//  2. Profile.IsolationMode — when a profile is loaded and IsolationMode is a
//     provisionable value ("worktree", "container", "vm"), that value is used.
//  3. "" (empty) — no provisioning; the run executes in the local process.
//
// Profile IsolationMode values of "none" or "" are treated as no preference
// so that profiles can explicitly opt out of isolation without causing
// provisioning to occur.
func resolveWorkspaceType(reqWorkspaceType string, profile *profiles.Profile) string {
	if reqWorkspaceType != "" {
		return reqWorkspaceType
	}
	if profile != nil {
		switch profile.IsolationMode {
		case "worktree", "container", "vm":
			return profile.IsolationMode
		}
	}
	return ""
}

// copyStringSlice returns a copy of src that preserves nil-vs-empty semantics.
// A nil src returns nil; a non-nil empty src returns a non-nil empty slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func normalizePermissionConfig(p PermissionConfig) PermissionConfig {
	if p.Sandbox == "" {
		// Safety-biased default: see DefaultPermissionConfig in types.go.
		p.Sandbox = SandboxScopeWorkspace
	}
	if p.Approval == "" {
		p.Approval = ApprovalPolicyNone
	}
	p.Rules = copyPermissionRuleSet(p.Rules)
	return p
}

func buildContinuationPolicyNotice(srcAllowed, currentAllowed []string, srcPerms, currentPerms PermissionConfig) string {
	allowedChanged := !stringSlicesEqual(srcAllowed, currentAllowed)
	permsChanged := !permissionConfigsEqual(srcPerms, currentPerms)
	if !allowedChanged && !permsChanged {
		return ""
	}

	lines := []string{
		"SYSTEM: Runtime policy changed for this continuation.",
		"Only the current run's tools and permissions are authoritative.",
		"Ignore any earlier tool usage or permission assumptions from previous turns.",
	}
	if allowedChanged {
		if len(currentAllowed) == 0 {
			lines = append(lines, "Allowed tools for this run: unrestricted current tool catalog.")
		} else {
			lines = append(lines, "Allowed tools for this run: "+strings.Join(currentAllowed, ", ")+".")
		}
	}
	if permsChanged {
		lines = append(lines, fmt.Sprintf("Permissions for this run: sandbox=%s, approval=%s.", currentPerms.Sandbox, currentPerms.Approval))
	}
	return strings.Join(lines, "\n")
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// provisionRunWorkspace provisions a workspace for a run based on wsType and
// the base options from RunnerConfig.WorkspaceBaseOptions.
//
// Provisioning is delegated to the package-level workspace.Registry, which
// the workspace package's init() functions populate with the four built-in
// backends:
//   - "local"     — a LocalWorkspace under BaseDir (defaults to os.TempDir).
//   - "worktree"  — a WorktreeWorkspace off baseOpts.RepoPath. Worktree
//     provisioning fails fast if RepoPath is empty.
//   - "container" — a Docker container running harnessd inside, with a
//     bind-mounted host workspace dir. Requires Docker.
//   - "vm"        — a Hetzner VM workspace. Requires HETZNER_API_KEY.
//
// Each backend ignores fields it doesn't use; the same Options struct is
// passed to all of them.
func provisionRunWorkspace(ctx context.Context, runID, wsType string, baseOpts WorkspaceProvisionOptions) (workspace.Workspace, error) {
	if err := validateWorkspaceProvisionPreconditions(wsType, baseOpts); err != nil {
		return nil, err
	}

	opts := workspace.Options{
		ID:              runID,
		RepoPath:        baseOpts.RepoPath,
		WorktreeRootDir: baseOpts.WorktreeRootDir,
		BaseDir:         baseOpts.BaseDir,
		ConfigTOML:      baseOpts.ConfigTOML,
	}

	ws, err := workspace.New(ctx, wsType, opts)
	if err != nil {
		return nil, fmt.Errorf("provision %s workspace: %w", wsType, err)
	}
	return ws, nil
}

// validateWorkspaceProvisionPreconditions checks the deterministic
// preconditions for provisioning a workspace of the given type, without
// touching the environment: the type must be registered (a clearer error than
// the workspace package's generic ErrNotFound), and worktree can only succeed
// with a real repo path (a remediation-shaped message instead of a deeper
// "repoPath must be set" from inside the workspace package).
//
// StartRun calls this for explicitly requested workspace types so callers get
// a synchronous error at run creation instead of a queued run that fails in
// provisioning; provisionRunWorkspace calls it again as the safety net for
// profile-resolved workspace types. Environment-dependent failures (Docker
// daemon unavailable, missing HETZNER_API_KEY) still surface at provisioning
// time.
func validateWorkspaceProvisionPreconditions(wsType string, baseOpts WorkspaceProvisionOptions) error {
	known := false
	for _, name := range workspace.List() {
		if name == wsType {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown workspace type %q (registered: %v)", wsType, workspace.List())
	}

	if wsType == "worktree" && baseOpts.RepoPath == "" {
		return fmt.Errorf("workspace_type=worktree requires WorkspaceBaseOptions.RepoPath to be configured in RunnerConfig")
	}
	return nil
}
