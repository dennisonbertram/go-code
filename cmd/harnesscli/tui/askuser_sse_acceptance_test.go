package tui_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestAskUser_WaitingEnvelopeThroughBridge_ShowsSubmitsAndContinues crosses
// the production SSE decoder, Bubble Tea model, pending-input API, visible
// overlay, answer API, resume event, and later assistant output. In particular,
// run_id exists only at the event-envelope level, matching harness.Event.
func TestAskUser_WaitingEnvelopeThroughBridge_ShowsSubmitsAndContinues(t *testing.T) {
	const (
		runID  = "run-waiting-envelope"
		callID = "call-waiting-envelope"
	)

	answerAccepted := make(chan struct{})
	var answerOnce sync.Once
	releaseAnswer := func() { answerOnce.Do(func() { close(answerAccepted) }) }
	submittedBody := make(chan []byte, 1)
	var eventRequests atomic.Int32
	var inputGets atomic.Int32
	var inputPosts atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs/"+runID+"/events", func(w http.ResponseWriter, r *http.Request) {
		eventRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "id: %s:7\n", runID)
		fmt.Fprint(w, "event: run.waiting_for_user\n")
		fmt.Fprintf(
			w,
			"data: {\"id\":%q,\"run_id\":%q,\"type\":\"run.waiting_for_user\",\"payload\":{\"call_id\":%q}}\n\n",
			runID+":7",
			runID,
			callID,
		)
		w.(http.Flusher).Flush()

		select {
		case <-answerAccepted:
		case <-r.Context().Done():
			return
		}

		fmt.Fprintf(
			w,
			"id: %s:8\nevent: run.resumed\ndata: {\"id\":%q,\"run_id\":%q,\"type\":\"run.resumed\",\"payload\":{\"call_id\":%q}}\n\n",
			runID,
			runID+":8",
			runID,
			callID,
		)
		fmt.Fprintf(
			w,
			"id: %s:9\nevent: assistant.message.delta\ndata: {\"id\":%q,\"run_id\":%q,\"type\":\"assistant.message.delta\",\"payload\":{\"content\":\"Continuation after answer\"}}\n\n",
			runID,
			runID+":9",
			runID,
		)
		fmt.Fprintf(
			w,
			"id: %s:10\nevent: run.completed\ndata: {\"id\":%q,\"run_id\":%q,\"type\":\"run.completed\",\"payload\":{}}\n\n",
			runID,
			runID+":10",
			runID,
		)
		w.(http.Flusher).Flush()
	})
	mux.HandleFunc("/v1/runs/"+runID+"/input", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			inputGets.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				w,
				`{"run_id":%q,"call_id":%q,"tool":"AskUserQuestion","questions":[{"question":"Continue this conversation?","header":"Continue","options":[{"label":"Proceed","description":"Resume the run"}],"multiSelect":false}],"deadline_at":"2099-01-01T00:00:00Z"}`,
				runID,
				callID,
			)
		case http.MethodPost:
			inputPosts.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read submitted answer: %v", err)
			}
			submittedBody <- body
			releaseAnswer()
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer releaseAnswer()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	model := tui.New(cfg)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = next.(tui.Model)
	next, cmd := model.Update(tui.RunStartedMsg{RunID: runID})
	model = next.(tui.Model)

	msgCh := make(chan tea.Msg, 32)
	deadline := time.Now().Add(5 * time.Second)
	var dispatch func(tea.Cmd)
	dispatch = func(pending tea.Cmd) {
		if pending == nil {
			return
		}
		go func() {
			msg := pending()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, child := range batch {
					dispatch(child)
				}
				return
			}
			if msg == nil {
				return
			}
			select {
			case msgCh <- msg:
			case <-time.After(time.Until(deadline) + time.Second):
			}
		}()
	}
	dispatch(cmd)

	overlayObserved := false
	for time.Now().Before(deadline) {
		select {
		case msg := <-msgCh:
			next, followup := model.Update(msg)
			model = next.(tui.Model)
			dispatch(followup)

			if !overlayObserved && model.AskUserActive() &&
				strings.Contains(model.View(), "Continue this conversation?") &&
				strings.Contains(model.View(), "Proceed") {
				overlayObserved = true
				next, submit := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
				model = next.(tui.Model)
				dispatch(submit)
			}

			if overlayObserved && !model.RunActive() &&
				strings.Contains(model.View(), "Continuation after answer") {
				goto verified
			}
		case <-time.After(time.Until(deadline)):
		}
	}
	t.Fatalf(
		"waiting conversation did not complete: overlay=%t input_gets=%d input_posts=%d view=%q",
		overlayObserved,
		inputGets.Load(),
		inputPosts.Load(),
		model.View(),
	)

verified:
	if got := eventRequests.Load(); got != 1 {
		t.Errorf("event stream requests = %d, want 1", got)
	}
	if got := inputGets.Load(); got != 1 {
		t.Errorf("pending input GETs = %d, want 1", got)
	}
	if got := inputPosts.Load(); got != 1 {
		t.Errorf("answer POSTs = %d, want 1", got)
	}
	select {
	case raw := <-submittedBody:
		var submitted struct {
			Answers map[string]string `json:"answers"`
		}
		if err := json.Unmarshal(raw, &submitted); err != nil {
			t.Fatalf("decode submitted answer: %v", err)
		}
		if got := submitted.Answers["Continue this conversation?"]; got != "Proceed" {
			t.Errorf("submitted answer = %q, want Proceed", got)
		}
	default:
		t.Fatal("answer request body was not captured")
	}
}

func TestAskUser_WaitingEnvelopeReconnectRetainsCanonicalRunID(t *testing.T) {
	const (
		runID        = "run-waiting-replay"
		lastEventID  = "run-waiting-replay:6"
		waitingEvent = "run-waiting-replay:7"
	)

	headerSeen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerSeen <- r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(
			w,
			"id: %s\nevent: run.waiting_for_user\ndata: {\"id\":%q,\"run_id\":%q,\"type\":\"run.waiting_for_user\",\"payload\":{\"call_id\":\"call-replay\"}}\n\n",
			waitingEvent,
			waitingEvent,
			runID,
		)
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	ch, stop := tui.StartSSEBridgeWithOptions(
		t.Context(),
		srv.URL,
		tui.SSEBridgeOptions{LastEventID: lastEventID},
	)
	defer stop()

	select {
	case msg := <-ch:
		event, ok := msg.(tui.SSEEventMsg)
		if !ok {
			t.Fatalf("bridge message = %T, want SSEEventMsg", msg)
		}
		if event.RunID != runID {
			t.Errorf("decoded run id = %q, want %q", event.RunID, runID)
		}
		if event.ID != waitingEvent {
			t.Errorf("decoded event id = %q, want %q", event.ID, waitingEvent)
		}
		if strings.Contains(string(event.Raw), "run_id") {
			t.Errorf("payload unexpectedly duplicates envelope run_id: %s", event.Raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for replayed waiting event")
	}

	select {
	case got := <-headerSeen:
		if got != lastEventID {
			t.Errorf("Last-Event-ID = %q, want %q", got, lastEventID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay request")
	}
}
