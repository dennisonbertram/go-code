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
	// ResumeAgentIDs reuses existing subagents instead of creating new ones.
	// Reserved for Slice 2 of epic #808: any non-empty value is rejected.
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
	ID     string `json:"id,omitempty"`
	Item   string `json:"item"`
	Prompt string `json:"prompt"`
	Status string `json:"status"` // "pending" | "completed" | "failed" | "cancelled" | ...
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SwarmReport aggregates every member outcome in deterministic order
// (items first; resumed members would follow in later slices).
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
// allowance, propagates caller cancellation to every member, and aggregates
// the per-member results into one report.
type Swarm struct {
	manager            tools.SubagentManager
	maxConcurrency     int
	initialConcurrency int
	rampInterval       time.Duration
	newTicker          func(time.Duration) swarmTicker
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

// WithSwarmRamp overrides the initial burst size and the ramp interval.
// Non-positive values are ignored.
func WithSwarmRamp(initial int, interval time.Duration) SwarmOption {
	return func(s *Swarm) {
		if initial >= 1 {
			s.initialConcurrency = initial
		}
		if interval > 0 {
			s.rampInterval = interval
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
		manager:            manager,
		maxConcurrency:     resolveSwarmMaxConcurrency(os.Getenv),
		initialConcurrency: defaultSwarmInitialConcurrency,
		rampInterval:       defaultSwarmRampInterval,
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
	if s.initialConcurrency < 1 {
		s.initialConcurrency = 1
	}
	if s.rampInterval <= 0 {
		s.rampInterval = defaultSwarmRampInterval
	}
	if s.newTicker == nil {
		s.newTicker = func(d time.Duration) swarmTicker {
			return realSwarmTicker{t: time.NewTicker(d)}
		}
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
	if len(r.ResumeAgentIDs) > 0 {
		return nil, fmt.Errorf("%w: resume_agent_ids is not supported yet", ErrInvalidSwarmRequest)
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

// Run validates the request, fans the template out over the items, and blocks
// until every member reaches a terminal state. Member failures are captured
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

	n := len(req.Items)
	report := SwarmReport{Members: make([]SwarmMemberReport, n)}
	for i := range req.Items {
		report.Members[i] = SwarmMemberReport{
			Item:   req.Items[i],
			Prompt: prompts[i],
			Status: swarmMemberStatusPending,
		}
	}

	// swarmCtx scopes member waits: it is independent of the caller ctx so
	// that, after a caller cancellation, member waits keep polling until the
	// manager reports the terminal (cancelled) state we just asked for.
	swarmCtx, stopSwarm := context.WithCancel(context.Background())
	defer stopSwarm()

	ticker := s.newTicker(s.rampInterval)
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
	allowance := s.initialConcurrency
	if allowance > s.maxConcurrency {
		allowance = s.maxConcurrency
	}
	if allowance > n {
		allowance = n
	}
	if allowance < 1 {
		allowance = 1
	}

	launch := func(idx int) {
		inFlight++
		go func() {
			res, err := s.manager.Start(ctx, req.memberRequest(prompts[idx]))
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
			recordSwarmOutcome(&report.Members[oc.idx], oc.res, oc.err)
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
