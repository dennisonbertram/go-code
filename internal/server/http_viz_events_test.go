package server_test

// http_viz_events_test.go — pins the GET /v1/runs/{id}/events contract that
// the /viz session visualizer's timeline view depends on (epic #812, slice 4):
//
//   - A client connecting mid-flight first receives the run's stored history
//     (replay), then live events as the run progresses.
//   - Event ids are "<runID>:<seq>" with seq strictly increasing by 1 across
//     the whole stream (replay and live phases concatenated), so the UI can
//     render in order and reconnect with Last-Event-ID without duplicates.
//   - The server closes the stream after the terminal event.
//
// Adjacent contracts are already covered elsewhere and intentionally not
// duplicated: Last-Event-ID skip (TestLastEventIDSkipsSeenEvents,
// TestRegression_C1_InRangeLastEventID_StillReplaysOnlyUnseenEvents),
// adversarial Last-Event-ID (TestLastEventID_AdversarialValuesDoNotPanic),
// SSE framing (TestWriteSSE_IncludesIDAndRetry), keepalive pings
// (TestSSEKeepalivePingsInEventStream), and from-start streaming to a
// terminal event (test/e2e TestE2E_HappyPathRunCompletes).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

// signalingProvider blocks inside Complete until released, and closes
// called on entry so tests can synchronize on "the provider was invoked"
// (which implies run.started and llm.turn.requested are already in the
// run's event history).
type signalingProvider struct {
	called chan struct{}
	done   chan struct{}
	once   sync.Once
}

func (p *signalingProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	p.once.Do(func() { close(p.called) })
	select {
	case <-ctx.Done():
		return harness.CompletionResult{}, ctx.Err()
	case <-p.done:
		return harness.CompletionResult{Content: "signaling provider done"}, nil
	}
}

// sseFrame is one parsed SSE frame from the events stream.
type sseFrame struct {
	id    string
	event string
	data  string
}

// readSSEFrame reads one frame (terminated by a blank line) from r,
// skipping keepalive comment lines (": ping"). It returns the frame's id,
// event, and data fields.
func readSSEFrame(r *bufio.Reader) (sseFrame, error) {
	var f sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return sseFrame{}, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "":
			// Blank line ends the frame; a frame with no data was a comment.
			if f.event == "" && f.data == "" {
				continue
			}
			return f, nil
		case strings.HasPrefix(trimmed, ":"):
			// SSE comment (keepalive ping) — ignore.
		case strings.HasPrefix(trimmed, "id: "):
			f.id = strings.TrimPrefix(trimmed, "id: ")
		case strings.HasPrefix(trimmed, "event: "):
			f.event = strings.TrimPrefix(trimmed, "event: ")
		case strings.HasPrefix(trimmed, "data: "):
			if f.data != "" {
				f.data += "\n"
			}
			f.data += strings.TrimPrefix(trimmed, "data: ")
		}
	}
}

func TestRunEvents_HistoryThenLiveOrdering(t *testing.T) {
	t.Parallel()

	sp := &signalingProvider{called: make(chan struct{}), done: make(chan struct{})}
	runner := harness.NewRunner(
		sp,
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "gpt-4.1-mini", MaxSteps: 1},
	)

	handler := server.New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(`{"prompt":"timeline contract"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// Wait until the provider is invoked: at that point run.started and
	// llm.turn.requested are guaranteed to be in the run's history, so a
	// client connecting now must receive them as replayed history.
	select {
	case <-sp.called:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not invoked within 5s")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/runs/"+created.RunID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect to events stream: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)

	// Phase 1: read the replayed history frames (before the run is released).
	// Everything the run emitted up to the provider call must arrive first.
	var frames []sseFrame
	first, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read first frame (history): %v", err)
	}
	frames = append(frames, first)
	second, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read second frame (history): %v", err)
	}
	frames = append(frames, second)

	if frames[0].event != string(harness.EventRunStarted) {
		t.Fatalf("first replayed frame = %q, want %q (history must replay from the beginning)", frames[0].event, harness.EventRunStarted)
	}

	// Phase 2: release the provider and read the live continuation to the
	// terminal event.
	close(sp.done)
	for {
		f, err := readSSEFrame(reader)
		if err != nil {
			t.Fatalf("read live frame: %v", err)
		}
		frames = append(frames, f)
		if harness.IsTerminalEvent(harness.EventType(f.event)) {
			break
		}
	}

	// The whole stream (replay + live) must carry ids "<runID>:<seq>" with
	// seq strictly increasing by 1 from 0 — the UI renders in this order and
	// resumes with Last-Event-ID without duplicates.
	for i, f := range frames {
		runID, seq, err := harness.ParseEventID(f.id)
		if err != nil {
			t.Fatalf("frame %d: unparseable event id %q: %v", i, f.id, err)
		}
		if runID != created.RunID {
			t.Fatalf("frame %d: id run %q, want %q", i, runID, created.RunID)
		}
		if seq != uint64(i) {
			t.Fatalf("frame %d: seq = %d, want %d (ids must be strictly increasing without gaps)", i, seq, i)
		}
		// The data payload must be the full event JSON with a matching type.
		var payload struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(f.data), &payload); err != nil {
			t.Fatalf("frame %d: data is not event JSON: %v", i, err)
		}
		if payload.Type != f.event {
			t.Fatalf("frame %d: event line %q but data type %q", i, f.event, payload.Type)
		}
		if payload.ID != f.id {
			t.Fatalf("frame %d: id line %q but data id %q", i, f.id, payload.ID)
		}
	}

	last := frames[len(frames)-1]
	if last.event != string(harness.EventRunCompleted) {
		t.Fatalf("terminal frame = %q, want %q", last.event, harness.EventRunCompleted)
	}

	// The live phase must actually contain the events emitted after the
	// provider was released (llm.turn.completed arrives after Complete
	// returns), proving the stream continued past the replayed prefix.
	var sawTurnCompleted bool
	for _, f := range frames {
		if f.event == string(harness.EventLLMTurnCompleted) {
			sawTurnCompleted = true
			break
		}
	}
	if !sawTurnCompleted {
		t.Fatal("stream missing llm.turn.completed after the provider was released (live continuation did not arrive)")
	}

	// The server must close the stream after the terminal event (the UI
	// treats EOF as "run finished", not as a reconnect signal).
	if _, err := readSSEFrame(reader); err != io.EOF && err == nil {
		t.Fatal("expected stream to close after the terminal event")
	} else if err != io.EOF {
		// Any read error other than a clean EOF after the terminal event is
		// acceptable only if it is the context deadline racing the close; fail
		// otherwise to keep the contract strict.
		if ctx.Err() == nil {
			t.Fatalf("unexpected stream error after terminal event: %v", err)
		}
	}
}
