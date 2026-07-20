package main

// viz_test.go — tests for "harnesscli viz [--open]" (epic #812, slice 2).
//
// Behavior under test:
//   - viz prints the visualizer URL (<base>/viz/) for the configured daemon.
//   - -base-url resolves the daemon address the same way runStatus/runList do,
//     with trailing slashes trimmed.
//   - --open launches the OS browser via an injected opener (tests never exec
//     a real browser); on opener failure the URL is still printed as fallback.
//   - The opener command is selected by platform: open on darwin, xdg-open
//     elsewhere.
//   - dispatch routes the "viz" subcommand.

import (
	"errors"
	"strings"
	"testing"
)

func TestRunVizPrintsDefaultURL(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := runViz(nil)
	if code != 0 {
		t.Fatalf("runViz returned %d, stderr=%s", code, errBuf.String())
	}
	got := strings.TrimSpace(outBuf.String())
	if got != "http://localhost:8080/viz/" {
		t.Fatalf("runViz printed %q, want %q", got, "http://localhost:8080/viz/")
	}
}

func TestRunVizTrimsTrailingSlashOnBaseURL(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := runViz([]string{"-base-url", "http://example.com:9000/"})
	if code != 0 {
		t.Fatalf("runViz returned %d, stderr=%s", code, errBuf.String())
	}
	got := strings.TrimSpace(outBuf.String())
	if got != "http://example.com:9000/viz/" {
		t.Fatalf("runViz printed %q, want %q", got, "http://example.com:9000/viz/")
	}
}

func TestRunVizRejectsPositionalArgs(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := runViz([]string{"extra"})
	if code == 0 {
		t.Fatalf("runViz with positional arg returned 0, want non-zero; stdout=%s", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "harnesscli viz") {
		t.Fatalf("stderr missing command prefix: %q", errBuf.String())
	}
}

func TestRunVizOpenInvokesOpener(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	var opened []string
	origOpen := vizOpenURL
	vizOpenURL = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	defer func() { vizOpenURL = origOpen }()

	code := runViz([]string{"--open", "-base-url", "http://example.com:1234"})
	if code != 0 {
		t.Fatalf("runViz --open returned %d, stderr=%s", code, errBuf.String())
	}
	if len(opened) != 1 || opened[0] != "http://example.com:1234/viz/" {
		t.Fatalf("opener invoked with %v, want [http://example.com:1234/viz/]", opened)
	}
	if !strings.Contains(outBuf.String(), "http://example.com:1234/viz/") {
		t.Fatalf("stdout missing URL even on successful open: %q", outBuf.String())
	}
}

func TestRunVizOpenFailureFallsBackToPrintingURL(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origOpen := vizOpenURL
	vizOpenURL = func(url string) error {
		return errors.New("no browser available")
	}
	defer func() { vizOpenURL = origOpen }()

	code := runViz([]string{"--open"})
	if code == 0 {
		t.Fatalf("runViz --open with failing opener returned 0, want non-zero")
	}
	if !strings.Contains(outBuf.String(), "http://localhost:8080/viz/") {
		t.Fatalf("stdout missing fallback URL after opener failure: %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "no browser available") {
		t.Fatalf("stderr missing opener error: %q", errBuf.String())
	}
}

func TestRunVizWithoutOpenDoesNotInvokeOpener(t *testing.T) {
	_, _, restore := captureOutput(t)
	defer restore()

	called := false
	origOpen := vizOpenURL
	vizOpenURL = func(url string) error {
		called = true
		return nil
	}
	defer func() { vizOpenURL = origOpen }()

	if code := runViz(nil); code != 0 {
		t.Fatalf("runViz returned %d", code)
	}
	if called {
		t.Fatal("opener invoked without --open")
	}
}

func TestVizOpenerNameSelectsPlatformCommand(t *testing.T) {
	if got := vizOpenerName("darwin"); got != "open" {
		t.Errorf("vizOpenerName(darwin) = %q, want %q", got, "open")
	}
	for _, platform := range []string{"linux", "freebsd"} {
		if got := vizOpenerName(platform); got != "xdg-open" {
			t.Errorf("vizOpenerName(%s) = %q, want %q", platform, got, "xdg-open")
		}
	}
}

func TestDispatchRoutesViz(t *testing.T) {
	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	code := dispatch([]string{"viz", "-base-url", "http://example.com:5555"})
	if code != 0 {
		t.Fatalf("dispatch viz returned %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "http://example.com:5555/viz/") {
		t.Fatalf("dispatch viz output missing URL: %q", outBuf.String())
	}
}
