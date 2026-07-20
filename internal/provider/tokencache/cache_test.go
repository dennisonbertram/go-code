package tokencache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheReusesCredentialOutsideSafetyMargin(t *testing.T) {
	t.Parallel()

	var refreshCalls atomic.Int32
	cache := New("fake-cached-credential", "fake-refresh-handle", time.Now().Add(time.Hour), time.Minute,
		func(context.Context, string) (string, string, time.Time, error) {
			refreshCalls.Add(1)
			return "", "", time.Time{}, errors.New("refresh should not be called")
		})

	got, err := cache.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if got != "fake-cached-credential" {
		t.Fatal("Token() did not return the cached credential")
	}
	if refreshCalls.Load() != 0 {
		t.Fatalf("refresh calls = %d, want 0", refreshCalls.Load())
	}
}

func TestCacheSingleFlightsConcurrentRefresh(t *testing.T) {
	t.Parallel()

	var refreshCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	cache := New("fake-expiring-credential", "fake-refresh-handle", time.Now().Add(time.Second), time.Minute,
		func(context.Context, string) (string, string, time.Time, error) {
			refreshCalls.Add(1)
			close(started)
			<-release
			return "fake-refreshed-credential", "fake-next-refresh-handle", time.Now().Add(time.Hour), nil
		})

	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	values := make(chan string, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.Token(context.Background())
			values <- value
			errs <- err
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(values)
	close(errs)

	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("Token() error: %v", err)
		}
	}
	for value := range values {
		if value != "fake-refreshed-credential" {
			t.Fatal("concurrent caller did not receive refreshed credential")
		}
	}
}

func TestCacheReturnsStillValidCredentialWhenMarginRefreshFails(t *testing.T) {
	t.Parallel()

	cache := New("fake-still-valid-credential", "fake-refresh-handle", time.Now().Add(time.Minute), 2*time.Minute,
		func(context.Context, string) (string, string, time.Time, error) {
			return "", "", time.Time{}, errors.New("refresh unavailable")
		})

	got, err := cache.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if got != "fake-still-valid-credential" {
		t.Fatal("Token() did not retain the still-valid cached credential")
	}
}
