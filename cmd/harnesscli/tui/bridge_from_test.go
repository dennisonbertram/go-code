package tui_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestStartSSEBridgeFrom_SendsLastEventID verifies that StartSSEBridgeFrom
// forwards its lastEventID argument as the Last-Event-ID request header and
// still delivers SSE events to the returned channel.
func TestStartSSEBridgeFrom_SendsLastEventID(t *testing.T) {
	var mu sync.Mutex
	var gotLastEventID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotLastEventID = r.Header.Get("Last-Event-ID")
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		flusher.Flush()

		fmt.Fprint(w, "event: message\ndata: {\"type\":\"assistant.message.delta\",\"payload\":{\"content\":\"hello\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message\ndata: {\"type\":\"run.completed\",\"payload\":{}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msgs, stop := tui.StartSSEBridgeFrom(ctx, srv.URL, "resume-id-42")
	defer stop()

	var gotEvent bool
	var gotDone bool
	for msg := range msgs {
		switch m := msg.(type) {
		case tui.SSEEventMsg:
			if m.EventType == "assistant.message.delta" {
				gotEvent = true
			}
		case tui.SSEDoneMsg:
			if m.EventType == "run.completed" {
				gotDone = true
			}
		}
	}

	mu.Lock()
	header := gotLastEventID
	mu.Unlock()

	if header != "resume-id-42" {
		t.Errorf("Last-Event-ID header = %q, want %q", header, "resume-id-42")
	}
	if !gotEvent {
		t.Error("expected an SSEEventMsg to be delivered")
	}
	if !gotDone {
		t.Error("expected run.completed SSEDoneMsg")
	}
}
