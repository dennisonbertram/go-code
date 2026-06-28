package fakeprovider

import (
	"context"
	"sync"
	"time"

	"go-agent-harness/internal/harness"
)

// ExhaustedBehavior controls what Provider.Complete returns when all scripted
// turns have been consumed.
type ExhaustedBehavior int

const (
	// ExhaustEmpty returns a zero CompletionResult with a nil error (default).
	ExhaustEmpty ExhaustedBehavior = iota
	// ExhaustError returns a GenericError stating the provider is exhausted.
	ExhaustError
	// ExhaustRepeatLast repeats the last scripted turn indefinitely.
	ExhaustRepeatLast
)

// Turn describes a single scripted response that Provider will return for one
// Complete call.
type Turn struct {
	// Content is returned in CompletionResult.Content (non-streaming path).
	Content string
	// ToolCalls is returned in CompletionResult.ToolCalls.
	ToolCalls []harness.ToolCall
	// Deltas are emitted via req.Stream when the request is streaming.
	Deltas []harness.CompletionDelta
	// Usage is returned verbatim in CompletionResult.Usage.
	Usage *harness.CompletionUsage
	// Cost is returned verbatim in CompletionResult.Cost.
	Cost *harness.CompletionCost
	// CostUSD is returned verbatim in CompletionResult.CostUSD.
	CostUSD *float64
	// UsageStatus is returned verbatim in CompletionResult.UsageStatus.
	UsageStatus harness.UsageStatus
	// CostStatus is returned verbatim in CompletionResult.CostStatus.
	CostStatus harness.CostStatus
	// Error is returned as the Complete error for this turn.
	Error error
	// Delay is the duration to wait before returning.  Context-aware: if the
	// context is cancelled during the delay, ctx.Err() is returned instead.
	Delay time.Duration
	// InterDeltaDelay is the pause inserted between streaming deltas.
	// Context-aware: cancellation aborts the stream.
	InterDeltaDelay time.Duration
	// Hang causes Complete to block indefinitely (until Release() is called or
	// the context is cancelled).
	Hang bool
}

// Invocation records a single call to Complete for post-hoc assertions.
type Invocation struct {
	// Index is the zero-based turn index that was served (or -1 when exhausted).
	Index int
	// Request is the CompletionRequest received by Complete.
	Request harness.CompletionRequest
	// Streamed is true when req.Stream was non-nil.
	Streamed bool
	// StartedAt is when Complete was entered.
	StartedAt time.Time
	// ReturnedAt is when Complete returned.
	ReturnedAt time.Time
	// Err is the error returned by Complete (including ctx.Err() on cancel).
	Err error
}

// Option is a functional option for Provider.
type Option func(*Provider)

// WithDefaultDelay sets a default delay applied to every turn that does not
// specify its own Delay (i.e. Turn.Delay == 0).
func WithDefaultDelay(d time.Duration) Option {
	return func(p *Provider) { p.defaultDelay = d }
}

// WithExhaustedBehavior sets what happens when all scripted turns are consumed.
func WithExhaustedBehavior(b ExhaustedBehavior) Option {
	return func(p *Provider) { p.exhausted = b }
}

// WithName sets the provider name used in errors emitted by the fake.
func WithName(name string) Option {
	return func(p *Provider) { p.name = name }
}

// Provider is a fake implementation of harness.Provider backed by a
// pre-scripted sequence of turns.  It is safe for concurrent use.
type Provider struct {
	mu           sync.Mutex
	name         string
	turns        []Turn
	callIndex    int
	invocations  []Invocation
	defaultDelay time.Duration
	exhausted    ExhaustedBehavior

	// release is used to unblock Hang turns.  Allocated once and closed
	// (idempotently via releaseOnce) on the first Release() call.
	release     chan struct{}
	releaseOnce sync.Once
}

// New creates a Provider with the given scripted turns and options.
func New(turns []Turn, opts ...Option) *Provider {
	p := &Provider{
		name:    "fake",
		turns:   turns,
		release: make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Complete implements harness.Provider.  It serves the next scripted turn,
// applying delays and streaming as configured.
func (p *Provider) Complete(ctx context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	started := time.Now()

	// Determine which turn to serve (under lock).
	p.mu.Lock()
	idx := p.callIndex
	var turn Turn
	var turnIdx int

	switch {
	case idx < len(p.turns):
		turn = p.turns[idx]
		turnIdx = idx
		p.callIndex++
	case p.exhausted == ExhaustRepeatLast && len(p.turns) > 0:
		turn = p.turns[len(p.turns)-1]
		turnIdx = len(p.turns) - 1
		p.callIndex++
	case p.exhausted == ExhaustError:
		p.callIndex++
		p.mu.Unlock()
		err := GenericError("fakeprovider: all scripted turns exhausted")
		inv := Invocation{
			Index:      -1,
			Request:    req,
			Streamed:   req.Stream != nil,
			StartedAt:  started,
			ReturnedAt: time.Now(),
			Err:        err,
		}
		p.mu.Lock()
		p.invocations = append(p.invocations, inv)
		p.mu.Unlock()
		return harness.CompletionResult{}, err
	default:
		// ExhaustEmpty — return zero result, nil error.
		p.callIndex++
		p.mu.Unlock()
		inv := Invocation{
			Index:      -1,
			Request:    req,
			Streamed:   req.Stream != nil,
			StartedAt:  started,
			ReturnedAt: time.Now(),
			Err:        nil,
		}
		p.mu.Lock()
		p.invocations = append(p.invocations, inv)
		p.mu.Unlock()
		return harness.CompletionResult{}, nil
	}
	p.mu.Unlock()

	// Hang: block until release channel is closed or context is cancelled.
	if turn.Hang {
		p.mu.Lock()
		relCh := p.release
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			err := ctx.Err()
			p.recordInvocation(Invocation{
				Index:      turnIdx,
				Request:    req,
				Streamed:   req.Stream != nil,
				StartedAt:  started,
				ReturnedAt: time.Now(),
				Err:        err,
			})
			return harness.CompletionResult{}, err
		case <-relCh:
			// Released — fall through to normal return path.
		}
	}

	// Per-turn delay (or default).
	delay := turn.Delay
	if delay == 0 {
		delay = p.defaultDelay
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			p.recordInvocation(Invocation{
				Index:      turnIdx,
				Request:    req,
				Streamed:   req.Stream != nil,
				StartedAt:  started,
				ReturnedAt: time.Now(),
				Err:        err,
			})
			return harness.CompletionResult{}, err
		case <-time.After(delay):
		}
	}

	// Streaming: emit deltas via req.Stream.
	if req.Stream != nil && len(turn.Deltas) > 0 {
		for _, delta := range turn.Deltas {
			if turn.InterDeltaDelay > 0 {
				select {
				case <-ctx.Done():
					err := ctx.Err()
					p.recordInvocation(Invocation{
						Index:      turnIdx,
						Request:    req,
						Streamed:   true,
						StartedAt:  started,
						ReturnedAt: time.Now(),
						Err:        err,
					})
					return harness.CompletionResult{}, err
				case <-time.After(turn.InterDeltaDelay):
				}
			}
			req.Stream(delta)
		}
	}

	// Build result.
	result := harness.CompletionResult{
		Content:     turn.Content,
		ToolCalls:   turn.ToolCalls,
		Usage:       turn.Usage,
		Cost:        turn.Cost,
		CostUSD:     turn.CostUSD,
		UsageStatus: turn.UsageStatus,
		CostStatus:  turn.CostStatus,
	}

	returnedErr := turn.Error
	p.recordInvocation(Invocation{
		Index:      turnIdx,
		Request:    req,
		Streamed:   req.Stream != nil,
		StartedAt:  started,
		ReturnedAt: time.Now(),
		Err:        returnedErr,
	})

	return result, returnedErr
}

// Calls returns the total number of Complete invocations so far.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callIndex
}

// Invocations returns a copy of the recorded invocation log.
func (p *Provider) Invocations() []Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Invocation, len(p.invocations))
	copy(out, p.invocations)
	return out
}

// LastRequest returns the most recent CompletionRequest received, and whether
// any call has been made at all.
func (p *Provider) LastRequest() (harness.CompletionRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.invocations) == 0 {
		return harness.CompletionRequest{}, false
	}
	return p.invocations[len(p.invocations)-1].Request, true
}

// Release unblocks any currently-hanging Complete call (idempotent).
// It closes the internal release channel, so subsequent Hang turns will
// also return immediately unless Reset is called.
func (p *Provider) Release() {
	p.releaseOnce.Do(func() {
		close(p.release)
	})
}

// Reset rewinds the call index and clears the invocation log so the Provider
// can be reused from the beginning.  It also allocates a fresh release channel
// so that Hang turns work again.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callIndex = 0
	p.invocations = nil
	// Replace the release channel and once so Hang turns work again.
	p.release = make(chan struct{})
	p.releaseOnce = sync.Once{}
}

// recordInvocation appends an Invocation to the log under the mutex.
func (p *Provider) recordInvocation(inv Invocation) {
	p.mu.Lock()
	p.invocations = append(p.invocations, inv)
	p.mu.Unlock()
}
