package subagents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

const (
	// SwarmItemPlaceholder is the placeholder inside a swarm prompt template
	// that is replaced with each item to produce the member prompts.
	SwarmItemPlaceholder = "{{item}}"
	// SwarmMaxMembers is the hard cap on swarm members (items plus, in later
	// slices, resumed subagents), matching kimi-code's AgentSwarm limit.
	SwarmMaxMembers = 128
	// SwarmMaxConcurrencyEnv caps how many swarm members run concurrently.
	// Read once when the Swarm is constructed; values above SwarmMaxMembers
	// are clamped and invalid values fall back to SwarmMaxMembers.
	SwarmMaxConcurrencyEnv = "HARNESS_SWARM_MAX_CONCURRENCY"

	// defaultSwarmInitialConcurrency is the number of members started
	// immediately before the ramp begins (kimi-code parity: 5).
	defaultSwarmInitialConcurrency = 5
	// defaultSwarmRampInterval is how often the concurrency allowance grows
	// by one member (kimi-code parity: +1 every 700ms).
	defaultSwarmRampInterval = 700 * time.Millisecond

	swarmMemberStatusPending = "pending"
)

// ErrInvalidSwarmRequest marks swarm requests that fail validation
// (bad template, bad item count, duplicate expansions, unsupported options).
var ErrInvalidSwarmRequest = errors.New("invalid swarm request")

// SwarmRequest fans one prompt template out over a set of items. Each item
// expands PromptTemplate into one member prompt; every member runs as an
// ordinary subagent through the tools.SubagentManager the Swarm was built
// with. The profile/model fields are per-member overrides mirroring
// tools.SubagentRequest; zero values inherit the runner defaults.
type SwarmRequest struct {
	// PromptTemplate must contain SwarmItemPlaceholder at least once.
	PromptTemplate string `json:"prompt_template"`
	// Items holds 1..SwarmMaxMembers entries; expanded prompts must be distinct.
	Items []string `json:"items"`
	// ResumeAgentIDs reuses existing subagents instead of creating new ones:
	// entry i is paired with Items[i], and the item's expanded prompt is
	// delivered to that subagent through the message_subagent steering path
	// (RunSteerer.SteerRun on the resolved run ID). Resumed members are
	// scheduled before new items. Requires len(ResumeAgentIDs) <= len(Items),
	// distinct IDs, and each target in a running or waiting-for-user state.
	ResumeAgentIDs []string `json:"resume_agent_ids,omitempty"`

	Model                string                      `json:"model,omitempty"`
	SystemPrompt         string                      `json:"system_prompt,omitempty"`
	MaxSteps             int                         `json:"max_steps,omitempty"`
	MaxCostUSD           float64                     `json:"max_cost_usd,omitempty"`
	ReasoningEffort      string                      `json:"reasoning_effort,omitempty"`
	AllowedTools         []string                    `json:"allowed_tools,omitempty"`
	ProfileName          string                      `json:"profile,omitempty"`
	IsolationMode        string                      `json:"isolation,omitempty"`
	CleanupPolicy        string                      `json:"cleanup_policy,omitempty"`
	BaseRef              string                      `json:"base_ref,omitempty"`
	ResultMode           string                      `json:"result_mode,omitempty"`
	ParentContextHandoff *tools.ParentContextHandoff `json:"parent_context_handoff,omitempty"`
}

// SwarmMemberReport is the per-member outcome of a swarm run.
type SwarmMemberReport struct {
	ID      string `json:"id,omitempty"`
	Item    string `json:"item"`
	Prompt  string `json:"prompt"`
	Status  string `json:"status"` // "pending" | "completed" | "failed" | "cancelled" | ...
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Resumed bool   `json:"resumed,omitempty"` // true when the member resumed an existing subagent
}

// SwarmReport aggregates every member outcome in deterministic order:
// non-resumed item members first (in item order), then resumed members (in
// resume_agent_ids order).
type SwarmReport struct {
	Members   []SwarmMemberReport `json:"members"`
	Total     int                 `json:"total"`
	Completed int                 `json:"completed"`
	Failed    int                 `json:"failed"`
	Cancelled int                 `json:"cancelled"`
}

// finalize computes the summary counts from the member statuses.
func (r *SwarmReport) finalize() {
	r.Total = len(r.Members)
	r.Completed, r.Failed, r.Cancelled = 0, 0, 0
	for _, m := range r.Members {
		switch m.Status {
		case "completed":
			r.Completed++
		case "failed":
			r.Failed++
		case "cancelled":
			r.Cancelled++
		}
	}
}

// swarmTicker abstracts time.Ticker so tests can drive the ramp manually.
type swarmTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type realSwarmTicker struct{ t *time.Ticker }

func (r realSwarmTicker) Chan() <-chan time.Time { return r.t.C }
func (r realSwarmTicker) Stop()                  { r.t.Stop() }

// Swarm is the agent_swarm orchestrator: it validates a template fan-out,
// starts members through a tools.SubagentManager under a ramping concurrency
// allowance (resuming existing subagents through a tools.RunSteerer when
// resume_agent_ids is set), propagates caller cancellation to every member,
// and aggregates the per-member results into one report.
type Swarm struct {
	manager        tools.SubagentManager
	steerer        tools.RunSteerer
	maxConcurrency int
	newTicker      func(time.Duration) swarmTicker
}

// SwarmOption customizes a Swarm; options are applied after the environment
// is read, so an explicit option always wins over HARNESS_SWARM_MAX_CONCURRENCY.
type SwarmOption func(*Swarm)

// WithSwarmMaxConcurrency overrides the concurrency cap from the environment.
// Values < 1 are ignored.
func WithSwarmMaxConcurrency(n int) SwarmOption {
	return func(s *Swarm) {
		if n >= 1 {
			s.maxConcurrency = n
		}
	}
}

// WithSwarmSteerer sets the RunSteerer used to deliver prompts to resumed
// subagents (the same messaging path message_subagent uses). Required when a
// request carries resume_agent_ids.
func WithSwarmSteerer(steerer tools.RunSteerer) SwarmOption {
	return func(s *Swarm) {
		if steerer != nil {
			s.steerer = steerer
		}
	}
}

// withSwarmTickerFactory replaces the ticker used by the ramp scheduler.
// It exists for deterministic timing tests.
func withSwarmTickerFactory(f func(time.Duration) swarmTicker) SwarmOption {
	return func(s *Swarm) {
		if f != nil {
			s.newTicker = f
		}
	}
}

// NewSwarm builds a Swarm that starts members through manager (in production
// an *InlineManager). The concurrency cap is read once from
// HARNESS_SWARM_MAX_CONCURRENCY unless overridden by an option.
func NewSwarm(manager tools.SubagentManager, opts ...SwarmOption) *Swarm {
	s := &Swarm{
		manager:        manager,
		maxConcurrency: resolveSwarmMaxConcurrency(os.Getenv),
		newTicker: func(d time.Duration) swarmTicker {
			return realSwarmTicker{t: time.NewTicker(d)}
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.maxConcurrency > SwarmMaxMembers {
		s.maxConcurrency = SwarmMaxMembers
	}
	if s.maxConcurrency < 1 {
		s.maxConcurrency = 1
	}
	return s
}

// resolveSwarmMaxConcurrency reads the env cap: default SwarmMaxMembers,
// clamped to [1, SwarmMaxMembers], invalid values fall back to the default.
func resolveSwarmMaxConcurrency(getenv func(string) string) int {
	raw := strings.TrimSpace(getenv(SwarmMaxConcurrencyEnv))
	if raw == "" {
		return SwarmMaxMembers
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return SwarmMaxMembers
	}
	if n > SwarmMaxMembers {
		return SwarmMaxMembers
	}
	return n
}

// expandedPrompts validates the request and returns the per-item prompts.
func (r SwarmRequest) expandedPrompts() ([]string, error) {
	if !strings.Contains(r.PromptTemplate, SwarmItemPlaceholder) {
		return nil, fmt.Errorf("%w: prompt_template must contain the %q placeholder", ErrInvalidSwarmRequest, SwarmItemPlaceholder)
	}
	if len(r.Items) == 0 {
		return nil, fmt.Errorf("%w: items must contain at least 1 entry", ErrInvalidSwarmRequest)
	}
	if len(r.Items) > SwarmMaxMembers {
		return nil, fmt.Errorf("%w: items has %d entries, max is %d", ErrInvalidSwarmRequest, len(r.Items), SwarmMaxMembers)
	}
	if len(r.ResumeAgentIDs) > len(r.Items) {
		return nil, fmt.Errorf("%w: resume_agent_ids has %d entries but items has only %d", ErrInvalidSwarmRequest, len(r.ResumeAgentIDs), len(r.Items))
	}
	seenIDs := make(map[string]int, len(r.ResumeAgentIDs))
	for i, id := range r.ResumeAgentIDs {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%w: resume_agent_ids entry %d is empty", ErrInvalidSwarmRequest, i)
		}
		if prev, dup := seenIDs[id]; dup {
			return nil, fmt.Errorf("%w: resume_agent_ids entries %d and %d are duplicate id %q", ErrInvalidSwarmRequest, prev, i, id)
		}
		seenIDs[id] = i
	}

	prompts := make([]string, len(r.Items))
	seen := make(map[string]int, len(r.Items))
	for i, item := range r.Items {
		expanded := strings.ReplaceAll(r.PromptTemplate, SwarmItemPlaceholder, item)
		prompts[i] = expanded
		// Compare trimmed expansions: the manager trims prompts before
		// running, so "a" and "a " would launch duplicate work.
		key := strings.TrimSpace(expanded)
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: items %d and %d expand to the same prompt", ErrInvalidSwarmRequest, prev, i)
		}
		seen[key] = i
	}
	return prompts, nil
}

// memberRequest builds the per-member tool-layer request from the overrides.
func (r SwarmRequest) memberRequest(prompt string) tools.SubagentRequest {
	return tools.SubagentRequest{
		Prompt:               prompt,
		Model:                r.Model,
		SystemPrompt:         r.SystemPrompt,
		MaxSteps:             r.MaxSteps,
		MaxCostUSD:           r.MaxCostUSD,
		AllowedTools:         append([]string(nil), r.AllowedTools...),
		ProfileName:          r.ProfileName,
		ReasoningEffort:      r.ReasoningEffort,
		IsolationMode:        r.IsolationMode,
		CleanupPolicy:        r.CleanupPolicy,
		BaseRef:              r.BaseRef,
		ResultMode:           r.ResultMode,
		ParentContextHandoff: r.ParentContextHandoff,
	}
}

// swarmMemberPlan is one member of the cohort in schedule order: resumes
// first, then new items. reportIdx places the member in the deterministic
// report order (new item members first, resumed members last).
type swarmMemberPlan struct {
	item       string
	prompt     string
	reportIdx  int
	resumed    bool
	subagentID string // resumed members only: canonical subagent ID from Get
	runID      string // resumed members only: run to steer
}

// buildMemberPlans resolves resume_agent_ids against the manager (rejecting
// unknown IDs and active-incompatible statuses) and lays out the cohort in
// schedule order. Resume entry i consumes items[i]'s expanded prompt.
func (s *Swarm) buildMemberPlans(ctx context.Context, req SwarmRequest, prompts []string) ([]swarmMemberPlan, error) {
	k := len(req.ResumeAgentIDs)
	n := len(req.Items)
	plans := make([]swarmMemberPlan, 0, n)
	for i, id := range req.ResumeAgentIDs {
		res, err := s.manager.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%w: resume_agent_ids entry %q: %v", ErrInvalidSwarmRequest, id, err)
		}
		if !swarmResumeCompatibleStatus(res.Status) {
			return nil, fmt.Errorf("%w: resume_agent_ids entry %q has status %q, want %q or %q",
				ErrInvalidSwarmRequest, id, res.Status, harness.RunStatusRunning, harness.RunStatusWaitingForUser)
		}
		plans = append(plans, swarmMemberPlan{
			item:       req.Items[i],
			prompt:     prompts[i],
			reportIdx:  (n - k) + i,
			resumed:    true,
			subagentID: res.ID,
			runID:      res.RunID,
		})
	}
	for j := k; j < n; j++ {
		plans = append(plans, swarmMemberPlan{
			item:      req.Items[j],
			prompt:    prompts[j],
			reportIdx: j - k,
		})
	}
	return plans, nil
}

// swarmResumeCompatibleStatus reports whether a subagent can accept a steered
// message: the same states RunSteerer.SteerRun accepts.
func swarmResumeCompatibleStatus(status string) bool {
	return status == string(harness.RunStatusRunning) || status == string(harness.RunStatusWaitingForUser)
}

// Run validates the request, fans the template out over the items, and blocks
// until every member reaches a terminal state. Resume entries are resolved
// against the manager up front (unknown IDs and inactive subagents reject the
// whole request before anything launches) and receive their prompts through
// the steering path, scheduled before new items. Member failures are captured
// in the report and never abort the cohort. If ctx is cancelled, every
// started member is cancelled through the manager, members that never started
// are reported as cancelled, and Run returns the partial report together with
// ctx.Err().
func (s *Swarm) Run(ctx context.Context, req SwarmRequest) (SwarmReport, error) {
	if s.manager == nil {
		return SwarmReport{}, fmt.Errorf("subagents: swarm has no subagent manager")
	}
	prompts, err := req.expandedPrompts()
	if err != nil {
		return SwarmReport{}, err
	}
	if len(req.ResumeAgentIDs) > 0 && s.steerer == nil {
		return SwarmReport{}, fmt.Errorf("subagents: swarm has no run steerer for resume_agent_ids")
	}
	plans, err := s.buildMemberPlans(ctx, req, prompts)
	if err != nil {
		return SwarmReport{}, err
	}

	n := len(plans)
	report := SwarmReport{Members: make([]SwarmMemberReport, n)}
	for _, p := range plans {
		entry := &report.Members[p.reportIdx]
		entry.Item = p.item
		entry.Prompt = p.prompt
		entry.Status = swarmMemberStatusPending
		entry.Resumed = p.resumed
		if p.resumed {
			// The subagent already exists, so its ID is known up front.
			entry.ID = p.subagentID
		}
	}

	// swarmCtx scopes member waits: it is independent of the caller ctx so
	// that, after a caller cancellation, member waits keep polling until the
	// manager reports the terminal (cancelled) state we just asked for.
	swarmCtx, stopSwarm := context.WithCancel(context.Background())
	defer stopSwarm()

	ticker := s.newTicker(defaultSwarmRampInterval)
	defer ticker.Stop()

	type outcome struct {
		idx int
		res tools.SubagentResult
		err error
	}
	outcomes := make(chan outcome, n)

	var mu sync.Mutex
	memberIDs := make([]string, n)
	memberDone := make([]bool, n)
	cancelled := atomic.Bool{}

	cancelMembers := func() {
		mu.Lock()
		defer mu.Unlock()
		for i, id := range memberIDs {
			if id != "" && !memberDone[i] {
				// Best effort: a member may already be terminal, in which
				// case the manager's Cancel is a no-op.
				_ = s.manager.Cancel(context.Background(), id)
			}
		}
	}

	inFlight := 0
	started := 0
	finished := 0
	allowance := defaultSwarmInitialConcurrency
	if allowance > s.maxConcurrency {
		allowance = s.maxConcurrency
	}
	if allowance > n {
		allowance = n
	}

	launch := func(idx int) {
		inFlight++
		plan := plans[idx]
		if plan.resumed {
			go func() {
				// Deliver the expanded prompt through the same messaging
				// path message_subagent uses, then wait for the subagent's
				// terminal state like any other member.
				if err := s.steerer.SteerRun(plan.runID, plan.prompt); err != nil {
					outcomes <- outcome{idx: idx, err: fmt.Errorf("resume subagent %q: %w", plan.subagentID, err)}
					return
				}
				mu.Lock()
				memberIDs[idx] = plan.subagentID
				mu.Unlock()
				// Same race as Start below: the swarm may have been
				// cancelled while the steer was in flight.
				if cancelled.Load() {
					_ = s.manager.Cancel(context.Background(), plan.subagentID)
				}
				wres, werr := s.manager.Wait(swarmCtx, plan.subagentID)
				if wres.ID == "" {
					wres.ID = plan.subagentID
				}
				outcomes <- outcome{idx: idx, res: wres, err: werr}
			}()
			return
		}
		go func() {
			res, err := s.manager.Start(ctx, req.memberRequest(plan.prompt))
			if err != nil {
				outcomes <- outcome{idx: idx, err: err}
				return
			}
			mu.Lock()
			memberIDs[idx] = res.ID
			mu.Unlock()
			// If the swarm was cancelled while Start was in flight, the
			// cancel sweep already ran without this id — cancel it here so
			// no member escapes cancellation.
			if cancelled.Load() {
				_ = s.manager.Cancel(context.Background(), res.ID)
			}
			wres, werr := s.manager.Wait(swarmCtx, res.ID)
			if wres.ID == "" {
				wres.ID = res.ID
			}
			outcomes <- outcome{idx: idx, res: wres, err: werr}
		}()
	}

	canLaunch := func() bool {
		return !cancelled.Load() && ctx.Err() == nil && started < n && inFlight < allowance
	}
	for canLaunch() {
		launch(started)
		started++
	}

	ctxDone := ctx.Done()
	for finished < n {
		// After cancellation there is nothing to launch; leave the loop as
		// soon as every started member has reported a terminal outcome.
		if cancelled.Load() && finished >= started {
			break
		}
		select {
		case <-ctxDone:
			cancelled.Store(true)
			cancelMembers()
			ctxDone = nil // fire once; a closed channel would spin the select
		case oc := <-outcomes:
			finished++
			inFlight--
			mu.Lock()
			memberDone[oc.idx] = true
			mu.Unlock()
			recordSwarmOutcome(&report.Members[plans[oc.idx].reportIdx], oc.res, oc.err)
			for canLaunch() {
				launch(started)
				started++
			}
		case <-ticker.Chan():
			maxAllowance := s.maxConcurrency
			if maxAllowance > n {
				maxAllowance = n
			}
			if allowance < maxAllowance {
				allowance++
			}
			for canLaunch() {
				launch(started)
				started++
			}
		}
	}

	if cancelled.Load() {
		for i := range report.Members {
			if report.Members[i].Status == swarmMemberStatusPending {
				report.Members[i].Status = "cancelled"
				report.Members[i].Error = ctx.Err().Error()
			}
		}
		report.finalize()
		return report, ctx.Err()
	}
	report.finalize()
	return report, nil
}

// recordSwarmOutcome folds one member outcome into its report entry. The
// member index never changes, so item order in the report is deterministic.
func recordSwarmOutcome(m *SwarmMemberReport, res tools.SubagentResult, err error) {
	if res.ID != "" {
		m.ID = res.ID
	}
	switch {
	case err != nil && res.Status == "":
		// Start failed or the wait errored without a status: the member
		// never produced a terminal state of its own.
		m.Status = "failed"
		m.Error = err.Error()
	case err != nil:
		m.Status = res.Status
		m.Output = res.Output
		m.Error = res.Error
		if m.Error == "" {
			m.Error = err.Error()
		}
	default:
		m.Status = res.Status
		m.Output = res.Output
		m.Error = res.Error
	}
}
