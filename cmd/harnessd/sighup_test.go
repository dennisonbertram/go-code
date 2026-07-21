package main

// sighup_test.go — SIGHUP-driven config reload (epic #815 slice 4).
//
// awaitServer is the daemon's signal loop: it waits for server errors and
// shutdown signals, dispatching SIGHUP to the injected reload function.
// Reload errors are logged, never fatal; repeated SIGHUPs reload again;
// SIGINT/SIGTERM shutdown semantics are unchanged.

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go-agent-harness/internal/config"
)

// runAwaitServer starts awaitServer in a goroutine and returns the channels
// plus a result channel that receives the return value (or stays empty while
// the loop is still running).
func runAwaitServer(reloadFn func(context.Context) (config.ReloadReport, error)) (chan os.Signal, chan error, chan error) {
	sig := make(chan os.Signal, 4)
	serverErr := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- awaitServer(sig, serverErr, reloadFn)
	}()
	return sig, serverErr, done
}

// assertStillWaiting fails if the loop has returned.
func assertStillWaiting(t *testing.T, done chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s: awaitServer returned early with %v", what, err)
	case <-time.After(50 * time.Millisecond):
	}
}

// assertReturnedWithin fails unless the loop returns within the timeout.
func assertReturnedWithin(t *testing.T, done chan error, d time.Duration, what string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("%s: awaitServer did not return within %s", what, d)
		return nil
	}
}

// TestAwaitServer_SIGHUPTriggersReload verifies SIGHUP invokes the reload
// function exactly once and the loop keeps waiting afterwards.
func TestAwaitServer_SIGHUPTriggersReload(t *testing.T) {
	var calls atomic.Int32
	reloadFn := func(context.Context) (config.ReloadReport, error) {
		calls.Add(1)
		return config.ReloadReport{Applied: []string{"model"}}, nil
	}
	sig, _, done := runAwaitServer(reloadFn)

	sig <- syscall.SIGHUP
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("reload invocations after one SIGHUP: got %d, want 1", got)
	}
	assertStillWaiting(t, done, "after SIGHUP")

	sig <- syscall.SIGTERM
	if err := assertReturnedWithin(t, done, 2*time.Second, "SIGTERM"); err != nil {
		t.Errorf("SIGTERM shutdown: got error %v, want nil", err)
	}
}

// TestAwaitServer_ReloadErrorLoggedNotFatal verifies a failing reload does
// not bring the daemon down, and a subsequent SIGHUP triggers a fresh reload.
func TestAwaitServer_ReloadErrorLoggedNotFatal(t *testing.T) {
	var calls atomic.Int32
	reloadFn := func(context.Context) (config.ReloadReport, error) {
		calls.Add(1)
		return config.ReloadReport{}, errors.New("toml: parse error")
	}
	sig, _, done := runAwaitServer(reloadFn)

	sig <- syscall.SIGHUP
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assertStillWaiting(t, done, "after failing reload")

	// Subsequent SIGHUPs reload again.
	sig <- syscall.SIGHUP
	deadline = time.Now().Add(2 * time.Second)
	for calls.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reload invocations after two SIGHUPs: got %d, want 2", got)
	}

	sig <- syscall.SIGTERM
	if err := assertReturnedWithin(t, done, 2*time.Second, "SIGTERM after reload errors"); err != nil {
		t.Errorf("shutdown after reload errors: got error %v, want nil", err)
	}
}

// TestAwaitServer_ServerErrorReturns verifies a server error takes the daemon
// down with that error regardless of reload wiring.
func TestAwaitServer_ServerErrorReturns(t *testing.T) {
	var calls atomic.Int32
	reloadFn := func(context.Context) (config.ReloadReport, error) {
		calls.Add(1)
		return config.ReloadReport{}, nil
	}
	_, serverErr, done := runAwaitServer(reloadFn)

	want := errors.New("listener died")
	serverErr <- want
	err := assertReturnedWithin(t, done, 2*time.Second, "server error")
	if !errors.Is(err, want) {
		t.Errorf("awaitServer return: got %v, want %v", err, want)
	}
	if calls.Load() != 0 {
		t.Errorf("reload invoked %d times without SIGHUP, want 0", calls.Load())
	}
}

// TestAwaitServer_ShutdownSignalsUnchanged verifies SIGINT and SIGTERM both
// shut down promptly and never trigger a reload.
func TestAwaitServer_ShutdownSignalsUnchanged(t *testing.T) {
	for _, shutdownSig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(shutdownSig.String(), func(t *testing.T) {
			var calls atomic.Int32
			reloadFn := func(context.Context) (config.ReloadReport, error) {
				calls.Add(1)
				return config.ReloadReport{}, nil
			}
			sig, _, done := runAwaitServer(reloadFn)

			sig <- shutdownSig
			if err := assertReturnedWithin(t, done, 2*time.Second, "shutdown"); err != nil {
				t.Errorf("%s: got error %v, want nil", shutdownSig, err)
			}
			if calls.Load() != 0 {
				t.Errorf("%s invoked reload %d times, want 0", shutdownSig, calls.Load())
			}
		})
	}
}

// TestAwaitServer_NilReloadFuncToleratesSIGHUP is the defensive case: a
// SIGHUP with no reload wired must not kill the wait loop.
func TestAwaitServer_NilReloadFuncToleratesSIGHUP(t *testing.T) {
	sig, _, done := runAwaitServer(nil)

	sig <- syscall.SIGHUP
	assertStillWaiting(t, done, "SIGHUP with nil reload func")

	sig <- syscall.SIGTERM
	if err := assertReturnedWithin(t, done, 2*time.Second, "SIGTERM"); err != nil {
		t.Errorf("SIGTERM: got error %v, want nil", err)
	}
}
