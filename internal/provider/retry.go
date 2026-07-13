package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RetryConfig controls the bounded retry loop used by DoWithRetry.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxTotal    time.Duration
	Jitter      bool
}

// DefaultRetryConfig returns the production retry settings.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		MaxTotal:    60 * time.Second,
		Jitter:      true,
	}
}

func mergeRetryConfig(cfg *RetryConfig) RetryConfig {
	if cfg == nil {
		return DefaultRetryConfig()
	}
	c := *cfg
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultRetryConfig().MaxAttempts
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = DefaultRetryConfig().BaseDelay
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = DefaultRetryConfig().MaxDelay
	}
	if c.MaxTotal <= 0 {
		c.MaxTotal = DefaultRetryConfig().MaxTotal
	}
	return c
}

// DoWithRetry executes req with bounded retries for transient HTTP failures.
// It returns the successful response, a non-retryable error response, or the
// last error after exhausting retries. The request body is buffered so it can
// be replayed across attempts. The caller is responsible for closing the
// returned response body.
func DoWithRetry(ctx context.Context, client *http.Client, req *http.Request, cfg *RetryConfig) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	c := mergeRetryConfig(cfg)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	body, err := snapshotBody(req)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if req.GetBody == nil {
		req.GetBody = func() (io.ReadCloser, error) {
			return newBodyReader(body), nil
		}
	}
	req.Body = newBodyReader(body)

	start := time.Now()
	var lastErr error

	for attempt := 1; attempt <= c.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if attempt > 1 && time.Since(start) >= c.MaxTotal {
			if lastErr != nil {
				return nil, fmt.Errorf("retry budget exhausted: %w", lastErr)
			}
			return nil, errors.New("retry budget exhausted")
		}

		attemptReq := req.Clone(ctx)
		if req.GetBody != nil {
			b, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("reset request body: %w", err)
			}
			attemptReq.Body = b
			attemptReq.GetBody = req.GetBody
		}

		resp, err := client.Do(attemptReq)
		if err != nil {
			if !isRetryableError(err, ctx) {
				return nil, err
			}
			lastErr = err
			if attempt == c.MaxAttempts {
				return nil, lastErr
			}
			wait := retryDelay(nil, attempt, c)
			if err := sleepCtx(ctx, wait, c.MaxTotal-time.Since(start)); err != nil {
				return nil, err
			}
			continue
		}

		if resp.StatusCode < 300 {
			return resp, nil
		}

		if !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}

		if attempt == c.MaxAttempts {
			return resp, nil
		}

		// Drain and close the response body so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastErr = fmt.Errorf("server returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))

		wait := retryDelay(resp, attempt, c)
		if err := sleepCtx(ctx, wait, c.MaxTotal-time.Since(start)); err != nil {
			return nil, err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("exhausted retries")
}

func snapshotBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

func newBodyReader(data []byte) io.ReadCloser {
	if data == nil {
		return nil
	}
	return io.NopCloser(bytes.NewReader(data))
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		529:
		return true
	}
	return false
}

func isRetryableError(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}

	// Never retry explicit context cancellation.
	if errors.Is(err, context.Canceled) {
		return false
	}

	// If the parent context is still valid, a deadline-exceeded error is
	// likely an HTTP client timeout rather than user cancellation.
	if errors.Is(err, context.DeadlineExceeded) {
		return ctx.Err() == nil
	}

	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	if isConnectionReset(err) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}

func isConnectionReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "forcibly closed")
}

func retryDelay(resp *http.Response, attempt int, cfg RetryConfig) time.Duration {
	if resp != nil {
		if d := parseRetryAfter(resp.Header.Get("Retry-After")); d > 0 {
			return clampDuration(d, cfg.MaxDelay)
		}
	}

	delay := cfg.BaseDelay
	if attempt > 1 {
		factor := uint(attempt - 1)
		if factor > 30 {
			factor = 30
		}
		delay = cfg.BaseDelay * (1 << factor)
	}
	delay = clampDuration(delay, cfg.MaxDelay)

	if cfg.Jitter && delay > 0 {
		jitter := time.Duration(rand.Int63n(int64(delay)/2 + 1))
		delay += jitter
		delay = clampDuration(delay, cfg.MaxDelay)
	}

	return delay
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		return d
	}

	return 0
}

func clampDuration(d, max time.Duration) time.Duration {
	if max > 0 && d > max {
		return max
	}
	return d
}

func sleepCtx(ctx context.Context, wait, remaining time.Duration) error {
	if remaining <= 0 {
		return ctx.Err()
	}
	if wait > remaining {
		wait = remaining
	}
	if wait <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
