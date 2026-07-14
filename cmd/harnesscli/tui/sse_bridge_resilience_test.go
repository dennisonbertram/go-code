package tui_test

// sse_bridge_resilience_test.go — TASK: fix/sse-bridge-resilience
//
// Reproduces three real, user-reported failure modes of the harnesscli TUI's
// SSE client (cmd/harnesscli/tui/bridge.go) against local httptest SSE
// servers:
//
//   - BUG 1 (P0): an SSE data: line larger than bufio.Scanner's default 64KB
//     token limit kills the scanner permanently (bufio.ErrTooLong), so the
//     bridge goroutine returns and every subsequent event on that run is
//     lost.
//   - BUG 2 (P1): the client never reconnects when the connection drops
//     mid-run, even though the server supports resuming via the
//     Last-Event-ID header (internal/server/http_runs.go +
//     harness.ParseEventID).
//   - BUG 3 (P1): the bridge's non-blocking trySend silently drops real
//     events (including tool.call.completed) once its 256-slot channel
//     fills up under a burst, which is why tool cards stayed stuck
//     "in-progress" for the user.
//
// These tests are written FIRST (TDD "red" step) and are expected to FAIL
// against the current (unfixed) implementation.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

// driveModel feeds messages produced by cmd (and any tea.Batch sub-commands
// it contains, recursively) back into m.Update() until stop reports true or
// timeout elapses. It mirrors — in a minimal, deterministic way — how the
// real bubbletea runtime dispatches each returned tea.Cmd on its own
// goroutine, which matters here because RunStartedMsg returns a batch
// containing both a spinner tick command and the SSE poll command; a naive
// synchronous driver would stall on whichever command is invoked first.
func driveModel(t *testing.T, m tui.Model, cmd tea.Cmd, timeout time.Duration, stop func(tui.Model, tea.Msg) bool) tui.Model {
	t.Helper()

	msgCh := make(chan tea.Msg, 256)
	deadline := time.Now().Add(timeout)

	var dispatch func(tea.Cmd)
	dispatch = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					dispatch(sub)
				}
				return
			}
			select {
			case msgCh <- msg:
			case <-time.After(time.Until(deadline) + time.Second):
			}
		}()
	}
	dispatch(cmd)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("driveModel: timed out after %s waiting for stop condition", timeout)
			return m
		}
		select {
		case msg := <-msgCh:
			model2, next := m.Update(msg)
			m = model2.(tui.Model)
			if stop(m, msg) {
				return m
			}
			dispatch(next)
		case <-time.After(remaining):
			t.Fatalf("driveModel: timed out after %s waiting for stop condition", timeout)
			return m
		}
	}
}

// burstEvent describes one synthetic SSE event used by the BUG 3 backpressure
// test. deltaChunk / markerID are set (mutually exclusive with each other and
// with neither) so the test can independently verify content reconstruction
// for the coalesced tool.output.delta stream and zero loss for the
// non-coalesced tool.call.completed markers, without racing on shared state
// between the httptest handler goroutine and the test goroutine.
type burstEvent struct {
	eventType  string
	payload    string
	deltaChunk string
	markerID   string
}

// buildBurstEvents constructs a deterministic sequence of tool.output.delta
// chunks for a single call_id, interleaved with tool.call.completed markers
// for distinct call_ids every markerEvery events, terminated by run.completed.
// It is pure (no shared mutable state) so both the httptest handler and the
// assertion code can safely range over the same slice concurrently.
func buildBurstEvents(n, markerEvery int, callID string) []burstEvent {
	events := make([]burstEvent, 0, n+n/markerEvery+1)
	for i := 0; i < n; i++ {
		chunk := fmt.Sprintf("[%05d]", i)
		chunkJSON, _ := json.Marshal(chunk)
		events = append(events, burstEvent{
			eventType:  "tool.output.delta",
			payload:    fmt.Sprintf(`{"call_id":%q,"content":%s}`, callID, chunkJSON),
			deltaChunk: chunk,
		})
		if i%markerEvery == 0 {
			markerID := fmt.Sprintf("call-marker-%d", i)
			events = append(events, burstEvent{
				eventType: "tool.call.completed",
				payload:   fmt.Sprintf(`{"call_id":%q,"tool":"bash","output":"ok"}`, markerID),
				markerID:  markerID,
			})
		}
	}
	events = append(events, burstEvent{eventType: "run.completed", payload: "{}"})
	return events
}

func writeBurstEvents(w http.ResponseWriter, events []burstEvent) {
	for i, e := range events {
		fmt.Fprintf(w, "id: run-burst:%d\nevent: message\ndata: {\"type\":%q,\"payload\":%s}\n\n", i, e.eventType, e.payload)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------------------------------------------------------------------------
// BUG 1 (P0): oversized events must not kill the stream permanently.
// ---------------------------------------------------------------------------

func TestBridgeBug1_OversizedEventDoesNotKillStream(t *testing.T) {
	// 200KB comfortably exceeds bufio.Scanner's default 64KB max token size,
	// and is realistic: merged stdout+stderr tool output is capped at ~60KB
	// (internal/harness/tools/head_tail_buffer.go) plus JSON escaping and
	// envelope overhead.
	bigContent := strings.Repeat("A", 200*1024)
	bigContentJSON, err := json.Marshal(bigContent)
	if err != nil {
		t.Fatalf("marshal big content: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "id: run-big:0\nevent: message\ndata: {\"type\":\"assistant.message.delta\",\"payload\":{\"content\":%s}}\n\n", bigContentJSON)
		fmt.Fprintf(w, "id: run-big:1\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridge(ctx, srv.URL)
	defer stop()

	var gotBig bool
	var gotDone bool
	for msg := range msgs {
		switch m := msg.(type) {
		case tui.SSEEventMsg:
			if m.EventType == "assistant.message.delta" {
				var p struct {
					Content string `json:"content"`
				}
				if json.Unmarshal(m.Raw, &p) == nil && len(p.Content) == len(bigContent) {
					gotBig = true
				}
			}
		case tui.SSEDoneMsg:
			if m.EventType == "run.completed" {
				gotDone = true
			}
		}
	}

	if !gotBig {
		t.Error("expected the 200KB event to be delivered intact; the scanner likely died with bufio.ErrTooLong before the fix (no .Buffer() call on the scanner)")
	}
	if !gotDone {
		t.Error("expected the stream to survive the oversized event and still deliver the subsequent run.completed event; a dead scanner drops the rest of the run silently")
	}
}

// ---------------------------------------------------------------------------
// BUG 2 (P1): the client must reconnect using Last-Event-ID when the
// connection drops mid-run, and must not duplicate or skip any event.
// ---------------------------------------------------------------------------

func TestSSEBridgeBug2_ReconnectsAndResumesWithLastEventID(t *testing.T) {
	var mu sync.Mutex
	connCount := 0
	var secondReqLastEventID string
	var secondReqSeen bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connCount++
		n := connCount
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		if n == 1 {
			// First connection: deliver two events, then close the connection
			// abruptly without ever sending run.completed/run.failed — this
			// simulates the real-world "server dropped mid-stream" failure.
			fmt.Fprint(w, "id: run-bug2:0\nevent: message\ndata: {\"type\":\"assistant.message.delta\",\"payload\":{\"content\":\"Hello\"}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			fmt.Fprint(w, "id: run-bug2:1\nevent: message\ndata: {\"type\":\"assistant.message.delta\",\"payload\":{\"content\":\", world\"}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}

		// Reconnect attempt: record the Last-Event-ID header the client sent,
		// then deliver the remainder of the stream plus the terminal event.
		mu.Lock()
		secondReqLastEventID = r.Header.Get("Last-Event-ID")
		secondReqSeen = true
		mu.Unlock()

		fmt.Fprint(w, "id: run-bug2:2\nevent: message\ndata: {\"type\":\"assistant.message.delta\",\"payload\":{\"content\":\"!\"}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "id: run-bug2:3\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)

	model2, cmd := model.Update(tui.RunStartedMsg{RunID: "run-bug2"})
	model = model2.(tui.Model)

	final := driveModel(t, model, cmd, 15*time.Second, func(m tui.Model, msg tea.Msg) bool {
		if done, ok := msg.(tui.SSEDoneMsg); ok && done.EventType == "run.completed" {
			return true
		}
		// The unfixed client marks the run inactive as soon as the first
		// (non-terminal) SSEDoneMsg arrives and never reconnects — stop as
		// soon as that happens instead of waiting out the full timeout.
		return !m.RunActive()
	})

	if final.LastAssistantText() != "Hello, world!" {
		t.Errorf("expected assembled text to survive the reconnect intact, got %q, want %q (a naive reconnect without resume would re-deliver history and corrupt/duplicate this; no reconnect at all would truncate it)", final.LastAssistantText(), "Hello, world!")
	}

	mu.Lock()
	gotSecondReq := secondReqSeen
	gotHeader := secondReqLastEventID
	mu.Unlock()

	if !gotSecondReq {
		t.Fatal("expected the bridge to automatically reconnect after the connection dropped mid-stream, but no second request was ever made")
	}
	if gotHeader != "run-bug2:1" {
		t.Errorf("expected reconnect request to carry Last-Event-ID %q (the ID of the last event actually delivered), got %q", "run-bug2:1", gotHeader)
	}
	if final.RunActive() {
		t.Error("expected the run to be inactive after run.completed arrives via the reconnected stream")
	}
}

// ---------------------------------------------------------------------------
// BUG 3 (P1): real events must never be silently dropped under backpressure,
// including tool.call.completed (which is why tool cards stayed stuck
// in-progress) and tool.output.delta content (which corrupts accumulated
// tool output).
// ---------------------------------------------------------------------------

func TestBridgeBug3_BurstDoesNotDropRealEventsUnderBackpressure(t *testing.T) {
	const numDeltas = 900
	const markerEvery = 150
	const callID = "call-burst"

	events := buildBurstEvents(numDeltas, markerEvery, callID)

	var expectedContent strings.Builder
	var expectedMarkers []string
	for _, e := range events {
		if e.deltaChunk != "" {
			expectedContent.WriteString(e.deltaChunk)
		}
		if e.markerID != "" {
			expectedMarkers = append(expectedMarkers, e.markerID)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeBurstEvents(w, events)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridge(ctx, srv.URL)
	defer stop()

	var gotContent strings.Builder
	gotMarkers := map[string]bool{}
	var gotDone bool
	var corruptionWarnings int

	for msg := range msgs {
		// Simulate a slow consumer (e.g. heavy rendering) so the bridge's
		// channel builds up backpressure, which is exactly the condition
		// under which the non-blocking trySend used to drop messages.
		time.Sleep(time.Millisecond)

		switch m := msg.(type) {
		case tui.SSEEventMsg:
			switch m.EventType {
			case "tool.output.delta":
				var p struct {
					CallID  string `json:"call_id"`
					Content string `json:"content"`
				}
				if json.Unmarshal(m.Raw, &p) == nil && p.CallID == callID {
					gotContent.WriteString(p.Content)
				}
			case "tool.call.completed":
				var p struct {
					CallID string `json:"call_id"`
				}
				if json.Unmarshal(m.Raw, &p) == nil {
					gotMarkers[p.CallID] = true
				}
			}
		case tui.SSEErrorMsg:
			if strings.Contains(m.Err.Error(), "dropped") || strings.Contains(m.Err.Error(), "corrupt") {
				corruptionWarnings++
			}
		case tui.SSEDoneMsg:
			if m.EventType == "run.completed" {
				gotDone = true
			}
		}
	}

	if gotContent.String() != expectedContent.String() {
		t.Errorf("tool.output.delta content was lost or corrupted under backpressure: got %d bytes, want %d bytes", gotContent.Len(), expectedContent.Len())
	}
	missing := 0
	for _, id := range expectedMarkers {
		if !gotMarkers[id] {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d/%d tool.call.completed markers were dropped under backpressure — this is exactly what leaves tool cards stuck in-progress", missing, len(expectedMarkers))
	}
	if !gotDone {
		t.Error("the terminal run.completed event was dropped under backpressure")
	}
	if corruptionWarnings > 0 {
		t.Errorf("bridge reported %d 'dropped messages / stream corrupt' warning(s) under a burst; real events must never be dropped in the first place", corruptionWarnings)
	}
}

// ---------------------------------------------------------------------------
// Regression tests — each of these would fail if the corresponding fix above
// were reverted or subtly broken, even though they exercise a different edge
// than the primary behavioral test for that bug.
// ---------------------------------------------------------------------------

// TestRegression_SSEBridgeSurvives1MBEvent pins the scanner buffer at the
// documented real-world upper bound: internal/harness/tools/bash_manager.go's
// defaultMaxStreamLineBytes caps a single tool.output.delta line at 1MB. If
// the scanner buffer ever regresses back toward bufio.Scanner's 64KB default
// (or anything below 1MB), this line alone would kill the stream exactly as
// BUG 1 originally described.
func TestRegression_SSEBridgeSurvives1MBEvent(t *testing.T) {
	bigContent := strings.Repeat("B", 1024*1024)
	bigContentJSON, err := json.Marshal(bigContent)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "id: run-1mb:0\nevent: message\ndata: {\"type\":\"tool.output.delta\",\"payload\":{\"call_id\":\"call-1mb\",\"content\":%s}}\n\n", bigContentJSON)
		fmt.Fprintf(w, "id: run-1mb:1\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridge(ctx, srv.URL)
	defer stop()

	var gotLen int
	var gotDone bool
	for msg := range msgs {
		switch m := msg.(type) {
		case tui.SSEEventMsg:
			if m.EventType == "tool.output.delta" {
				var p struct {
					Content string `json:"content"`
				}
				if json.Unmarshal(m.Raw, &p) == nil {
					gotLen += len(p.Content)
				}
			}
		case tui.SSEDoneMsg:
			if m.EventType == "run.completed" {
				gotDone = true
			}
		}
	}
	if gotLen != len(bigContent) {
		t.Errorf("expected the full 1MB event content to survive, got %d bytes, want %d", gotLen, len(bigContent))
	}
	if !gotDone {
		t.Error("expected run.completed to still arrive after a 1MB event")
	}
}

// TestRegression_SSEBridgeReconnectGivesUpGracefullyAfterBoundedAttempts
// pins the bound + single-message-on-abandonment contract: if a server
// connection can never succeed (every attempt closes immediately), the
// client must stop after exactly maxSSEReconnectAttempts retries, mark the
// run inactive, and surface exactly one "could not be re-established"
// message — not the repeated "stream error" storm the user originally
// reported, and not an infinite reconnect loop.
func TestRegression_SSEBridgeReconnectGivesUpGracefullyAfterBoundedAttempts(t *testing.T) {
	var mu sync.Mutex
	connCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Close immediately without ever delivering an event or a terminal
		// signal — every connection attempt (initial + every reconnect)
		// fails the same way, forcing the retry budget to be exhausted.
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)

	model2, cmd := model.Update(tui.RunStartedMsg{RunID: "run-give-up"})
	model = model2.(tui.Model)

	final := driveModel(t, model, cmd, 20*time.Second, func(m tui.Model, msg tea.Msg) bool {
		return !m.RunActive()
	})

	if final.RunActive() {
		t.Fatal("expected the run to eventually be marked inactive once reconnect attempts are exhausted")
	}

	view := final.View()
	abandonCount := strings.Count(view, "could not be re-established")
	if abandonCount != 1 {
		t.Errorf("expected exactly one 'could not be re-established' message in the viewport, got %d — repeated messages would reproduce the original 'too many dropped messages' storm", abandonCount)
	}

	mu.Lock()
	gotConns := connCount
	mu.Unlock()
	const wantConns = 1 + 5 // initial attempt + maxSSEReconnectAttempts
	if gotConns != wantConns {
		t.Errorf("expected exactly %d total connection attempts (1 initial + bounded reconnects), got %d — reconnects must be bounded, not infinite", wantConns, gotConns)
	}
}

// TestRegression_SSEBridgeCoalescesPerCallIDWithoutCrossContamination
// interleaves two distinct call_ids on every other event, forcing the
// coalescer in bridge.go to flush and switch accumulators on almost every
// message. This would fail if the merge logic ever concatenated content
// across call_ids or dropped the pending accumulator's content when
// switching, corrupting one or both call_ids' output.
func TestRegression_SSEBridgeCoalescesPerCallIDWithoutCrossContamination(t *testing.T) {
	const n = 200
	const callA, callB = "call-a", "call-b"

	var expectedA, expectedB strings.Builder
	var events []burstEvent
	for i := 0; i < n; i++ {
		chunkA := fmt.Sprintf("A%03d", i)
		chunkAJSON, _ := json.Marshal(chunkA)
		expectedA.WriteString(chunkA)
		events = append(events, burstEvent{
			eventType: "tool.output.delta",
			payload:   fmt.Sprintf(`{"call_id":%q,"content":%s}`, callA, chunkAJSON),
		})

		chunkB := fmt.Sprintf("B%03d", i)
		chunkBJSON, _ := json.Marshal(chunkB)
		expectedB.WriteString(chunkB)
		events = append(events, burstEvent{
			eventType: "tool.output.delta",
			payload:   fmt.Sprintf(`{"call_id":%q,"content":%s}`, callB, chunkBJSON),
		})
	}
	events = append(events, burstEvent{eventType: "run.completed", payload: "{}"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeBurstEvents(w, events)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridge(ctx, srv.URL)
	defer stop()

	var gotA, gotB strings.Builder
	for msg := range msgs {
		evt, ok := msg.(tui.SSEEventMsg)
		if !ok || evt.EventType != "tool.output.delta" {
			continue
		}
		var p struct {
			CallID  string `json:"call_id"`
			Content string `json:"content"`
		}
		if json.Unmarshal(evt.Raw, &p) != nil {
			continue
		}
		switch p.CallID {
		case callA:
			gotA.WriteString(p.Content)
		case callB:
			gotB.WriteString(p.Content)
		}
	}

	if gotA.String() != expectedA.String() {
		t.Errorf("call_id %q content corrupted by coalescing: got %d bytes, want %d bytes", callA, gotA.Len(), expectedA.Len())
	}
	if gotB.String() != expectedB.String() {
		t.Errorf("call_id %q content corrupted by coalescing: got %d bytes, want %d bytes", callB, gotB.Len(), expectedB.Len())
	}
}

// ---------------------------------------------------------------------------
// FOLLOW-UP: the SSE request must authenticate like the rest of the CLI, and
// a rejected/unknown-run status must not burn the bounded reconnect budget.
//
// The events endpoint sends no Authorization header at all today (verified:
// none of api.go's calls to the harnessd BaseURL set one; the only existing
// Authorization-setting code, fetchOpenRouterModelsFromURL, authenticates to
// the *external* openrouter.ai API with a provider key, not to harnessd
// itself). The actual harnessd auth pattern lives in cmd/harnesscli/auth.go's
// newAuthedRequest, which is only wired into the plain (non-TUI) harnesscli
// commands. See the accompanying report for the minimal plumbing added to
// reach the TUI (TUIConfig.APIKey + newTUIConfig loading
// ~/.harness/config.json via the existing auth.go:loadConfig()).
// ---------------------------------------------------------------------------

// TestSSEBridgeAuth_InitialRequestCarriesAuthorizationWhenConfigured asserts
// the bridge sends "Authorization: Bearer <key>" when a key is supplied via
// SSEBridgeOptions.APIKey.
func TestSSEBridgeAuth_InitialRequestCarriesAuthorizationWhenConfigured(t *testing.T) {
	var gotAuth string
	var sawRequest bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: run-auth:0\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridgeWithOptions(ctx, srv.URL, tui.SSEBridgeOptions{APIKey: "secret-key"})
	defer stop()
	for range msgs {
	}

	if !sawRequest {
		t.Fatal("expected the server to receive a request")
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("expected Authorization header %q, got %q — the SSE request must authenticate the same way the rest of the CLI does", "Bearer secret-key", gotAuth)
	}
}

// TestSSEBridgeAuth_NoAuthorizationHeaderWhenKeyEmpty asserts that when no
// API key is configured, the request carries no Authorization header at
// all — preserving today's unauthenticated-local behavior.
func TestSSEBridgeAuth_NoAuthorizationHeaderWhenKeyEmpty(t *testing.T) {
	var sawHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["Authorization"]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: run-noauth:0\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridge(ctx, srv.URL) // no options => unauthenticated
	defer stop()
	for range msgs {
	}

	if sawHeader {
		t.Error("expected no Authorization header when no API key is configured")
	}
}

// TestSSEBridgeAuth_ReconnectCarriesAuthorizationAndLastEventID asserts a
// single connection attempt that sets both LastEventID and APIKey (exactly
// what a reconnect does — see reconnectSSECmd/startSSEForRunFrom in api.go)
// carries both headers together, not just one or the other.
func TestSSEBridgeAuth_ReconnectCarriesAuthorizationAndLastEventID(t *testing.T) {
	var gotAuth, gotLastEventID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLastEventID = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: run-reconnect-auth:1\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridgeWithOptions(ctx, srv.URL, tui.SSEBridgeOptions{
		LastEventID: "run-reconnect-auth:0",
		APIKey:      "secret-key",
	})
	defer stop()
	for range msgs {
	}

	if gotAuth != "Bearer secret-key" {
		t.Errorf("expected reconnect request Authorization = %q, got %q", "Bearer secret-key", gotAuth)
	}
	if gotLastEventID != "run-reconnect-auth:0" {
		t.Errorf("expected reconnect request Last-Event-ID = %q, got %q", "run-reconnect-auth:0", gotLastEventID)
	}
}

// TestSSEBridgeAuth_401IsNonRetryable asserts that a 401 response produces
// exactly one connection attempt (no bounded-reconnect retries burned
// against a permanent auth rejection) and a single clear error.
func TestSSEBridgeAuth_401IsNonRetryable(t *testing.T) {
	var mu sync.Mutex
	connCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connCount++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"missing or invalid api key"}}`)
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)

	model2, cmd := model.Update(tui.RunStartedMsg{RunID: "run-401"})
	model = model2.(tui.Model)

	final := driveModel(t, model, cmd, 10*time.Second, func(m tui.Model, msg tea.Msg) bool {
		return !m.RunActive()
	})

	mu.Lock()
	gotConns := connCount
	mu.Unlock()
	if gotConns != 1 {
		t.Errorf("expected exactly 1 connection attempt on a 401 (non-retryable), got %d — burning the reconnect budget against a permanent auth rejection wastes time and confuses the user", gotConns)
	}
	if final.RunActive() {
		t.Error("expected the run to be marked inactive after a non-retryable 401")
	}

	view := final.View()
	errCount := strings.Count(view, "stream error")
	if errCount != 1 {
		t.Errorf("expected exactly one clear stream error message for a non-retryable 401, got %d", errCount)
	}
}

// TestSSEBridgeAuth_5xxStillRetries is a regression guard proving the 401/403
// non-retryable fix did not make everything non-retryable: a transient 5xx
// (or a network-level failure — already covered by the reconnect tests
// above) must still be retried per the existing bounded/backoff behavior.
func TestSSEBridgeAuth_5xxStillRetries(t *testing.T) {
	var mu sync.Mutex
	connCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connCount++
		n := connCount
		mu.Unlock()

		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal"}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: run-5xx:0\nevent: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)

	model2, cmd := model.Update(tui.RunStartedMsg{RunID: "run-5xx"})
	model = model2.(tui.Model)

	final := driveModel(t, model, cmd, 15*time.Second, func(m tui.Model, msg tea.Msg) bool {
		if done, ok := msg.(tui.SSEDoneMsg); ok && done.EventType == "run.completed" {
			return true
		}
		return !m.RunActive()
	})

	mu.Lock()
	gotConns := connCount
	mu.Unlock()
	if gotConns != 3 {
		t.Errorf("expected exactly 3 connection attempts (2 transient 5xx failures + 1 success), got %d", gotConns)
	}
	if final.RunActive() {
		t.Error("expected run inactive after run.completed")
	}
}
