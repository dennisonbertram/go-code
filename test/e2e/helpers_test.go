// Package e2e drives the real harnessd HTTP server over real HTTP/SSE, the
// same way harnesscli does, using an in-process server and a scripted fake
// harness.Provider so the suite is hermetic (no network egress, no real LLM
// provider, no Docker).
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

// staticProvider always returns the same scripted completion.
type staticProvider struct {
	result harness.CompletionResult
}

func (p *staticProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return p.result, nil
}

// slowProvider returns a scripted completion after a delay, or the context
// error if cancelled first. The delay is used to force at least one SSE
// keepalive ping to be observed while the run is in flight.
type slowProvider struct {
	delay  time.Duration
	result harness.CompletionResult
}

func (p *slowProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	select {
	case <-time.After(p.delay):
		return p.result, nil
	case <-ctx.Done():
		return harness.CompletionResult{}, ctx.Err()
	}
}

// blockingProvider blocks on Complete until its context is cancelled,
// signalling on started the first time it is entered. It is used to drive
// cancellation flows deterministically.
type blockingProvider struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{started: make(chan struct{})}
}

func (p *blockingProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return harness.CompletionResult{}, ctx.Err()
}

// scriptedProvider returns each turn in turns in order, then repeats the
// final turn for any extra calls.
type scriptedProvider struct {
	mu    sync.Mutex
	turns []harness.CompletionResult
	calls int
}

func (p *scriptedProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.calls
	if idx >= len(p.turns) {
		idx = len(p.turns) - 1
	}
	p.calls++
	return p.turns[idx], nil
}

// testServer bundles an in-process httptest.Server together with the runner
// backing it, so tests can start runs and read the resulting SSE stream over
// real HTTP.
type testServer struct {
	*httptest.Server
	runner *harness.Runner
}

// newTestServer builds a Server around the given provider/registry/broker and
// serves it over a real loopback HTTP listener. Auth is disabled so the tests
// exercise run lifecycle mechanics rather than auth plumbing.
func newTestServer(t *testing.T, provider harness.Provider, registry *harness.Registry, broker harness.ApprovalBroker) *testServer {
	t.Helper()
	if registry == nil {
		registry = harness.NewRegistry()
	}
	runner := harness.NewRunner(provider, registry, harness.RunnerConfig{
		DefaultModel:   "test-model",
		MaxSteps:       10,
		ApprovalBroker: broker,
	})
	handler := server.NewWithOptions(server.ServerOptions{
		Runner:         runner,
		AuthDisabled:   true,
		ApprovalBroker: broker,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &testServer{Server: ts, runner: runner}
}

// startRun POSTs /v1/runs with the given raw JSON body and returns the new
// run ID.
func startRun(t *testing.T, ts *testServer, body string) string {
	t.Helper()
	res, err := ts.Client().Post(ts.URL+"/v1/runs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 202 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("POST /v1/runs: expected 202, got %d: %s", res.StatusCode, b)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode POST /v1/runs response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("POST /v1/runs: empty run_id")
	}
	return created.RunID
}

// sseEvent is a single parsed Server-Sent Event record.
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// sseReader parses raw SSE framing from an io.Reader per the WHATWG SSE
// spec: an event is one or more field lines terminated by a blank line;
// lines beginning with ":" are comments (e.g. keepalive pings) and must be
// ignored by conformant clients, but are counted here so tests can assert on
// keepalive framing explicitly.
type sseReader struct {
	scanner *bufio.Scanner
	pings   int
}

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseReader{scanner: sc}
}

// next reads and returns the next SSE event, transparently skipping and
// counting ": ping" comment lines. Returns io.EOF when the stream closes.
func (s *sseReader) next() (sseEvent, error) {
	var ev sseEvent
	haveData := false
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if haveData {
				return ev, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			s.pings++
			continue
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
			haveData = true
		}
	}
	if err := s.scanner.Err(); err != nil {
		return ev, err
	}
	return ev, io.EOF
}

// harnessEvent decodes the sseEvent's data payload into a harness.Event.
func (e sseEvent) harnessEvent(t *testing.T) harness.Event {
	t.Helper()
	var out harness.Event
	if err := json.Unmarshal([]byte(e.Data), &out); err != nil {
		t.Fatalf("decode SSE event data %q: %v", e.Data, err)
	}
	return out
}

// openEventStream issues GET /v1/runs/{id}/events and returns an sseReader
// over the live response body plus a cleanup func the caller must invoke.
func openEventStream(t *testing.T, ts *testServer, runID string) (*sseReader, func()) {
	t.Helper()
	res, err := ts.Client().Get(ts.URL + "/v1/runs/" + runID + "/events")
	if err != nil {
		t.Fatalf("GET /v1/runs/%s/events: %v", runID, err)
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("GET /v1/runs/%s/events: expected 200, got %d: %s", runID, res.StatusCode, b)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		res.Body.Close()
		t.Fatalf("GET /v1/runs/%s/events: expected text/event-stream, got %q", runID, ct)
	}
	return newSSEReader(res.Body), func() { res.Body.Close() }
}

// drainUntilTerminal reads events from r until a terminal harness.Event is
// seen (per harness.IsTerminalEvent) or maxWait elapses, returning the
// sequence of event types observed in order and the terminal event itself.
func drainUntilTerminal(t *testing.T, r *sseReader, maxWait time.Duration) ([]harness.EventType, harness.Event) {
	t.Helper()
	type result struct {
		ev  sseEvent
		err error
	}
	ch := make(chan result, 1)
	done := make(chan struct{})
	defer close(done)

	next := func() {
		ev, err := r.next()
		select {
		case ch <- result{ev, err}:
		case <-done:
		}
	}

	var types []harness.EventType
	deadline := time.After(maxWait)
	for {
		go next()
		select {
		case res := <-ch:
			if res.err != nil {
				t.Fatalf("reading SSE stream: %v (events so far: %v)", res.err, types)
			}
			hev := res.ev.harnessEvent(t)
			types = append(types, hev.Type)
			if harness.IsTerminalEvent(hev.Type) {
				return types, hev
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for terminal event (events so far: %v)", maxWait, types)
		}
	}
}
