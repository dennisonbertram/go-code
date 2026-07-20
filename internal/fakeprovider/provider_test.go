package fakeprovider_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func simpleRequest() harness.CompletionRequest {
	return harness.CompletionRequest{Model: "fake-model"}
}

func streamingRequest(cb func(harness.CompletionDelta)) harness.CompletionRequest {
	return harness.CompletionRequest{Model: "fake-model", Stream: cb}
}

// ---------------------------------------------------------------------------
// Basic scripted turns
// ---------------------------------------------------------------------------

func TestProvider_SingleTurn_Content(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{
		{Content: "hello world"},
	})
	result, err := p.Complete(context.Background(), simpleRequest())
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Content)
	assert.Equal(t, 1, p.Calls())
}

func TestProvider_SingleTurn_ToolCalls(t *testing.T) {
	t.Parallel()
	tc := harness.ToolCall{ID: "tc-1", Name: "my_tool", Arguments: `{"x":1}`}
	p := fakeprovider.New([]fakeprovider.Turn{
		{ToolCalls: []harness.ToolCall{tc}},
	})
	result, err := p.Complete(context.Background(), simpleRequest())
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, tc, result.ToolCalls[0])
}

func TestProvider_MultipleTurns_ServedInOrder(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{
		{Content: "first"},
		{Content: "second"},
		{Content: "third"},
	})
	ctx := context.Background()
	for _, want := range []string{"first", "second", "third"} {
		res, err := p.Complete(ctx, simpleRequest())
		require.NoError(t, err)
		assert.Equal(t, want, res.Content)
	}
	assert.Equal(t, 3, p.Calls())
}

// ---------------------------------------------------------------------------
// Usage / Cost pass-through
// ---------------------------------------------------------------------------

func TestProvider_UsageAndCostPassThrough(t *testing.T) {
	t.Parallel()
	usd := 0.0042
	p := fakeprovider.New([]fakeprovider.Turn{{
		Content: "done",
		Usage: &harness.CompletionUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		Cost: &harness.CompletionCost{
			TotalUSD: usd,
		},
		CostUSD:     &usd,
		UsageStatus: harness.UsageStatusProviderReported,
		CostStatus:  harness.CostStatusAvailable,
	}})
	res, err := p.Complete(context.Background(), simpleRequest())
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 100, res.Usage.PromptTokens)
	assert.Equal(t, 50, res.Usage.CompletionTokens)
	assert.Equal(t, 150, res.Usage.TotalTokens)
	require.NotNil(t, res.Cost)
	assert.InDelta(t, 0.0042, res.Cost.TotalUSD, 1e-9)
	require.NotNil(t, res.CostUSD)
	assert.InDelta(t, 0.0042, *res.CostUSD, 1e-9)
	assert.Equal(t, harness.UsageStatusProviderReported, res.UsageStatus)
	assert.Equal(t, harness.CostStatusAvailable, res.CostStatus)
}

// ---------------------------------------------------------------------------
// Turn error
// ---------------------------------------------------------------------------

func TestProvider_TurnError_IsReturned(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("something went wrong")
	p := fakeprovider.New([]fakeprovider.Turn{
		{Error: sentinel},
	})
	_, err := p.Complete(context.Background(), simpleRequest())
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, p.Calls())
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

func TestProvider_Streaming_DeltasEmitted(t *testing.T) {
	t.Parallel()
	deltas := []harness.CompletionDelta{
		{Content: "part1"},
		{Content: "part2"},
		{Content: "part3"},
	}
	p := fakeprovider.New([]fakeprovider.Turn{
		{Deltas: deltas, Content: "full"},
	})

	var got []harness.CompletionDelta
	req := streamingRequest(func(d harness.CompletionDelta) {
		got = append(got, d)
	})
	res, err := p.Complete(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "full", res.Content)
	require.Len(t, got, 3)
	for i, d := range deltas {
		assert.Equal(t, d, got[i])
	}
}

func TestProvider_Streaming_InterDeltaDelay_ContextCancel(t *testing.T) {
	t.Parallel()
	deltas := []harness.CompletionDelta{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	p := fakeprovider.New([]fakeprovider.Turn{
		{Deltas: deltas, InterDeltaDelay: 200 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var mu sync.Mutex
	var received []harness.CompletionDelta
	req := streamingRequest(func(d harness.CompletionDelta) {
		mu.Lock()
		received = append(received, d)
		mu.Unlock()
	})
	_, err := p.Complete(ctx, req)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	// Only the first delta (before first inter-delta delay) may have been emitted.
	mu.Lock()
	n := len(received)
	mu.Unlock()
	assert.LessOrEqual(t, n, 1, "should not have streamed many deltas before cancel")
}

// ---------------------------------------------------------------------------
// Per-turn delay
// ---------------------------------------------------------------------------

func TestProvider_TurnDelay_ContextCancel(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{
		{Content: "slow", Delay: 500 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Complete(ctx, simpleRequest())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestProvider_DefaultDelay_Applied(t *testing.T) {
	t.Parallel()
	// Use a very short default delay and confirm the call takes at least that long.
	d := 20 * time.Millisecond
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "x"}}, fakeprovider.WithDefaultDelay(d))
	start := time.Now()
	_, err := p.Complete(context.Background(), simpleRequest())
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, d)
}

func TestProvider_DefaultDelay_ContextCancel(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "x"}},
		fakeprovider.WithDefaultDelay(500*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := p.Complete(ctx, simpleRequest())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// ---------------------------------------------------------------------------
// Hang / Release
// ---------------------------------------------------------------------------

func TestProvider_Hang_BlocksUntilRelease(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Hang: true, Content: "released"}})

	done := make(chan harness.CompletionResult, 1)
	go func() {
		res, err := p.Complete(context.Background(), simpleRequest())
		if err == nil {
			done <- res
		}
	}()

	// Give the goroutine time to block.
	time.Sleep(30 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("expected Complete to be blocking")
	default:
	}

	p.Release()

	select {
	case res := <-done:
		assert.Equal(t, "released", res.Content)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Complete did not return after Release()")
	}
}

func TestProvider_Hang_ContextCancel(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Hang: true}})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Complete(ctx, simpleRequest())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestProvider_Release_Idempotent(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Hang: true}})
	// Multiple Release calls must not panic (would double-close the channel).
	p.Release()
	p.Release()
	p.Release()
}

// ---------------------------------------------------------------------------
// ExhaustedBehavior
// ---------------------------------------------------------------------------

func TestProvider_ExhaustEmpty_ReturnsZero(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "only"}})
	_, _ = p.Complete(context.Background(), simpleRequest())
	// Second call — exhausted, default ExhaustEmpty
	res, err := p.Complete(context.Background(), simpleRequest())
	require.NoError(t, err)
	assert.Equal(t, "", res.Content)
	assert.Nil(t, res.ToolCalls)
}

func TestProvider_ExhaustError_ReturnsError(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "only"}},
		fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustError))
	_, _ = p.Complete(context.Background(), simpleRequest())
	_, err := p.Complete(context.Background(), simpleRequest())
	require.Error(t, err)
}

func TestProvider_ExhaustRepeatLast_RepeatsLastTurn(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{
		{Content: "first"},
		{Content: "last"},
	}, fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustRepeatLast))
	ctx := context.Background()
	_, _ = p.Complete(ctx, simpleRequest())      // "first"
	_, _ = p.Complete(ctx, simpleRequest())      // "last"
	res, err := p.Complete(ctx, simpleRequest()) // repeat "last"
	require.NoError(t, err)
	assert.Equal(t, "last", res.Content)
	res2, _ := p.Complete(ctx, simpleRequest()) // repeat again
	assert.Equal(t, "last", res2.Content)
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestProvider_Reset_RewindsCallsAndInvocations(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "a"}, {Content: "b"}})
	ctx := context.Background()
	_, _ = p.Complete(ctx, simpleRequest())
	_, _ = p.Complete(ctx, simpleRequest())
	assert.Equal(t, 2, p.Calls())

	p.Reset()
	assert.Equal(t, 0, p.Calls())
	assert.Empty(t, p.Invocations())

	// Serves from beginning again.
	res, err := p.Complete(ctx, simpleRequest())
	require.NoError(t, err)
	assert.Equal(t, "a", res.Content)
}

func TestProvider_Reset_RestoresHang(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Hang: true}})
	// Release without hanging.
	p.Release()
	// Reset and confirm hang works again.
	p.Reset()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Complete(ctx, simpleRequest())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// ---------------------------------------------------------------------------
// Invocations / LastRequest
// ---------------------------------------------------------------------------

func TestProvider_Invocations_RecordedCorrectly(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "one"}, {Content: "two"}})
	ctx := context.Background()
	req1 := harness.CompletionRequest{Model: "m1"}
	req2 := harness.CompletionRequest{Model: "m2"}
	_, _ = p.Complete(ctx, req1)
	_, _ = p.Complete(ctx, req2)

	invs := p.Invocations()
	require.Len(t, invs, 2)
	assert.Equal(t, 0, invs[0].Index)
	assert.Equal(t, "m1", invs[0].Request.Model)
	assert.False(t, invs[0].Streamed)
	assert.False(t, invs[0].StartedAt.IsZero())
	assert.False(t, invs[0].ReturnedAt.IsZero())
	assert.Nil(t, invs[0].Err)

	assert.Equal(t, 1, invs[1].Index)
	assert.Equal(t, "m2", invs[1].Request.Model)
}

func TestProvider_LastRequest_NotOkWhenNoCalls(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New(nil)
	_, ok := p.LastRequest()
	assert.False(t, ok)
}

func TestProvider_LastRequest_ReturnsLastRequest(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "x"}, {Content: "y"}})
	ctx := context.Background()
	req1 := harness.CompletionRequest{Model: "first"}
	req2 := harness.CompletionRequest{Model: "second"}
	_, _ = p.Complete(ctx, req1)
	_, _ = p.Complete(ctx, req2)
	got, ok := p.LastRequest()
	require.True(t, ok)
	assert.Equal(t, "second", got.Model)
}

func TestProvider_StreamedFlag_RecordedInInvocation(t *testing.T) {
	t.Parallel()
	p := fakeprovider.New([]fakeprovider.Turn{{Deltas: []harness.CompletionDelta{{Content: "x"}}}})
	req := streamingRequest(func(harness.CompletionDelta) {})
	_, _ = p.Complete(context.Background(), req)
	invs := p.Invocations()
	require.Len(t, invs, 1)
	assert.True(t, invs[0].Streamed)
}

// ---------------------------------------------------------------------------
// errors.go — error helpers
// ---------------------------------------------------------------------------

func TestRateLimitError_IsRetryable(t *testing.T) {
	t.Parallel()
	err := fakeprovider.RateLimitError("too many requests")
	assert.True(t, fakeprovider.IsRetryable(err))

	var phe *harness.ProviderHTTPError
	require.True(t, errors.As(err, &phe))
	assert.Equal(t, 429, phe.StatusCode)
	assert.Equal(t, "too many requests", phe.Body)
	assert.Equal(t, "fake", phe.Provider)
}

func TestRetryableError_WrapsErr(t *testing.T) {
	t.Parallel()
	base := errors.New("network timeout")
	err := fakeprovider.RetryableError(base)
	assert.True(t, fakeprovider.IsRetryable(err))
}

func TestRetryableError_PassesThroughProviderHTTPError429(t *testing.T) {
	t.Parallel()
	orig := &harness.ProviderHTTPError{Provider: "openai", StatusCode: 429, Body: "rate limit"}
	wrapped := fakeprovider.RetryableError(orig)
	// Should be the same underlying error (already retryable).
	assert.True(t, fakeprovider.IsRetryable(wrapped))
	var phe *harness.ProviderHTTPError
	require.True(t, errors.As(wrapped, &phe))
	assert.Equal(t, 429, phe.StatusCode)
}

func TestGenericError_NotRetryable(t *testing.T) {
	t.Parallel()
	err := fakeprovider.GenericError("bad request")
	assert.False(t, fakeprovider.IsRetryable(err))
	assert.EqualError(t, err, "bad request")
}

func TestIsRetryable_RetryableStatusCodes(t *testing.T) {
	t.Parallel()
	retryCodes := []int{429, 500, 502, 503, 504}
	for _, code := range retryCodes {
		err := &harness.ProviderHTTPError{Provider: "fake", StatusCode: code, Body: "err"}
		assert.True(t, fakeprovider.IsRetryable(err), "code %d should be retryable", code)
	}
}

func TestIsRetryable_NonRetryableStatusCodes(t *testing.T) {
	t.Parallel()
	nonRetryCodes := []int{400, 401, 403, 404, 422}
	for _, code := range nonRetryCodes {
		err := &harness.ProviderHTTPError{Provider: "fake", StatusCode: code, Body: "err"}
		assert.False(t, fakeprovider.IsRetryable(err), "code %d should not be retryable", code)
	}
}

func TestIsRetryable_NilAndPlainErrors(t *testing.T) {
	t.Parallel()
	assert.False(t, fakeprovider.IsRetryable(nil))
	assert.False(t, fakeprovider.IsRetryable(errors.New("plain")))
}

// ---------------------------------------------------------------------------
// WithName option
// ---------------------------------------------------------------------------

func TestProvider_WithName_SetsName(t *testing.T) {
	t.Parallel()
	// Verify the option doesn't panic and the provider still works.
	p := fakeprovider.New([]fakeprovider.Turn{{Content: "ok"}}, fakeprovider.WithName("mytest"))
	res, err := p.Complete(context.Background(), simpleRequest())
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Content)
}

// ---------------------------------------------------------------------------
// Thread safety — 50 concurrent Complete calls
// ---------------------------------------------------------------------------

func TestProvider_ConcurrentComplete_ThreadSafe(t *testing.T) {
	t.Parallel()
	const concurrency = 50

	// Enough turns for all goroutines.
	turns := make([]fakeprovider.Turn, concurrency)
	for i := range turns {
		turns[i] = fakeprovider.Turn{Content: "resp"}
	}
	p := fakeprovider.New(turns)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Complete(context.Background(), simpleRequest())
		}()
	}
	wg.Wait()

	assert.Equal(t, concurrency, p.Calls())
	assert.Len(t, p.Invocations(), concurrency)
}

// ---------------------------------------------------------------------------
// Interface compliance compile-time check
// ---------------------------------------------------------------------------

var _ harness.Provider = (*fakeprovider.Provider)(nil)
