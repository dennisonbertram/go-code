package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func testRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		MaxTotal:    100 * time.Millisecond,
		Jitter:      false,
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestRetryOn503(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	defer resp.Body.Close()

	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRetryHonorsRetryAfterHeaderSeconds(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestRetryHonorsRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	// HTTP-date Retry-After has one-second granularity, so a date two seconds
	// out parses to somewhere in (1s, 2s]. BaseDelay is kept tiny so that any
	// delay over a second can only have come from the Retry-After header.
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			retryAt := time.Now().UTC().Add(2 * time.Second).Format(http.TimeFormat)
			w.Header().Set("Retry-After", retryAt)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	start := time.Now()
	resp, err := DoWithRetry(context.Background(), srv.Client(), req, &RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxTotal:    10 * time.Second,
		Jitter:      false,
	})
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start)
	if elapsed < time.Second {
		t.Fatalf("expected Retry-After to drive a delay over 1s, got %v", elapsed)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestRetryDoesNotRetry400(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt for 400, got %d", attempts.Load())
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRetryDoesNotRetryUnauthorizedOrNotFound(t *testing.T) {
	t.Parallel()

	codes := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity}
	for _, code := range codes {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
			if err != nil {
				t.Fatalf("DoWithRetry: %v", err)
			}
			defer resp.Body.Close()

			if attempts.Load() != 1 {
				t.Fatalf("expected 1 attempt for %d, got %d", code, attempts.Load())
			}
			if resp.StatusCode != code {
				t.Fatalf("expected %d, got %d", code, resp.StatusCode)
			}
		})
	}
}

func TestRetryContextCancellationAbortsImmediately(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			cancel()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = DoWithRetry(ctx, srv.Client(), req, &RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Second,
		MaxDelay:    10 * time.Second,
		MaxTotal:    10 * time.Second,
		Jitter:      false,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts.Load())
	}
}

func TestRetryContextDeadlineNotRetried(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = DoWithRetry(ctx, srv.Client(), req, testRetryConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts.Load())
	}
}

func TestRetryOnUnexpectedEOF(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &failNTimesTransport{
			n:       1,
			err:     io.ErrUnexpectedEOF,
			wrapped: true,
			base:    srv.Client().Transport,
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), client, req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 1 {
		t.Fatalf("expected 1 server request, got %d", attempts.Load())
	}
}

func TestRetryOnConnectionReset(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &failNTimesTransport{
			n:       1,
			err:     errors.New("read tcp: connection reset by peer"),
			wrapped: true,
			base:    srv.Client().Transport,
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), client, req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 1 {
		t.Fatalf("expected 1 server request, got %d", attempts.Load())
	}
}

func TestRetryOnSyscallConnectionReset(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &failNTimesTransport{
			n:       1,
			err:     syscall.ECONNRESET,
			wrapped: false,
			base:    srv.Client().Transport,
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), client, req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 1 {
		t.Fatalf("expected 1 server request, got %d", attempts.Load())
	}
}

func TestRetryOnNetTimeout(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &failNTimesTransport{
			n:    1,
			err:  &timeoutError{},
			base: srv.Client().Transport,
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), client, req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 1 {
		t.Fatalf("expected 1 server request, got %d", attempts.Load())
	}
}

func TestRetryDoesNotRetryPermanentNetworkError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &failNTimesTransport{
		n:       10,
		err:     errors.New("no such host"),
		wrapped: true,
		base:    srv.Client().Transport,
	}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = DoWithRetry(context.Background(), client, req, testRetryConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("expected 1 attempt, got %d", transport.calls.Load())
	}
}

func TestRetryRequestBodyReplayable(t *testing.T) {
	t.Parallel()

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, testRetryConfig())
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("request bodies differ: %q vs %q", bodies[0], bodies[1])
	}
}

func TestRetryBudgetReturnsFinalResponse(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "attempt %d", attempts.Load())
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := DoWithRetry(context.Background(), srv.Client(), req, &RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		MaxTotal:    50 * time.Millisecond,
		Jitter:      false,
	})
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 final response, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "attempt 2" {
		t.Fatalf("unexpected final body: %q", string(body))
	}
}

// failNTimesTransport returns err for the first n requests, then delegates to base.
type failNTimesTransport struct {
	n       int
	err     error
	wrapped bool
	base    http.RoundTripper
	calls   atomic.Int64
}

func (t *failNTimesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	if t.n > 0 {
		t.n--
		if t.wrapped {
			return nil, fmt.Errorf("wrapped: %w", t.err)
		}
		return nil, t.err
	}
	if t.base == nil {
		return nil, errors.New("no base transport")
	}
	return t.base.RoundTrip(req)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

var _ net.Error = timeoutError{}
