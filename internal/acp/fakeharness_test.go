package acp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeEvent is one SSE event the fake harnessd emits on a run's stream.
type fakeEvent struct {
	typ     string
	payload string
}

// fakeRun is one run inside the fake harnessd. Events are pushed through the
// events channel; the SSE handler forwards them until it sees a terminal
// event, mirroring harnessd's real stream-then-close behavior.
type fakeRun struct {
	id     string
	prompt string
	events chan fakeEvent
}

// emit queues a non-terminal event. It blocks until the SSE reader consumes
// it, which synchronizes the test with the subscriber.
func (r *fakeRun) emit(typ, payload string) { r.events <- fakeEvent{typ: typ, payload: payload} }

// finish emits a terminal event and closes the stream.
func (r *fakeRun) finish(typ, payload string) {
	r.events <- fakeEvent{typ: typ, payload: payload}
	close(r.events)
}

// fakeHarness is an httptest double for harnessd's runs API:
// POST /v1/runs, GET /v1/runs/{id}/events, POST /v1/runs/{id}/cancel.
type fakeHarness struct {
	t   *testing.T
	mux *http.ServeMux
	*httptest.Server

	mu          sync.Mutex
	runs        map[string]*fakeRun
	bodies      map[string]string // run id -> raw POST /v1/runs body
	runAuths    map[string]string // run id -> Authorization header on POST /v1/runs
	cancels     []string          // run ids that received POST cancel
	cancelAuths map[string]string
	decisions   []string // "runID:approve" / "runID:deny" in arrival order
	noBroker    bool     // when true, approve/deny answer 501 (no approval broker)
	nextID      int
}

func newFakeHarness(t *testing.T) *fakeHarness {
	t.Helper()
	fh := &fakeHarness{
		t:           t,
		mux:         http.NewServeMux(),
		runs:        map[string]*fakeRun{},
		bodies:      map[string]string{},
		runAuths:    map[string]string{},
		cancelAuths: map[string]string{},
	}
	fh.mux.HandleFunc("POST /v1/runs", fh.handlePostRun)
	fh.mux.HandleFunc("/v1/runs/", fh.handleRunByID)
	fh.Server = httptest.NewServer(fh.mux)
	t.Cleanup(fh.Server.Close)
	return fh
}

func (fh *fakeHarness) handlePostRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if p, ok := raw["prompt"].(string); ok {
		body.Prompt = p
	}
	rawBody, _ := json.Marshal(raw)

	fh.mu.Lock()
	fh.nextID++
	id := fmt.Sprintf("run-%d", fh.nextID)
	run := &fakeRun{id: id, prompt: body.Prompt, events: make(chan fakeEvent)}
	fh.runs[id] = run
	fh.bodies[id] = string(rawBody)
	fh.runAuths[id] = r.Header.Get("Authorization")
	fh.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"run_id":%q,"status":"running"}`, id)
}

func (fh *fakeHarness) handleRunByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	runID, sub := parts[0], parts[1]
	fh.mu.Lock()
	run, ok := fh.runs[runID]
	fh.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":{"code":"not_found","message":"run not found"}}`, http.StatusNotFound)
		return
	}
	switch sub {
	case "events":
		fh.serveEvents(w, r, run)
	case "cancel":
		fh.mu.Lock()
		fh.cancels = append(fh.cancels, runID)
		fh.cancelAuths[runID] = r.Header.Get("Authorization")
		fh.mu.Unlock()
		// Mirror real harnessd: a cancel terminates the run with run.cancelled
		// on the event stream.
		go run.finish("run.cancelled", `{}`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"cancelling"}`)
	case "approve", "deny":
		fh.mu.Lock()
		noBroker := fh.noBroker
		if !noBroker {
			fh.decisions = append(fh.decisions, runID+":"+sub)
		}
		fh.mu.Unlock()
		if noBroker {
			// Mirror internal/server: approve/deny return 501 when no
			// approval broker is configured.
			http.Error(w, `{"error":{"code":"not_implemented","message":"approval broker is not configured"}}`, http.StatusNotImplemented)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":%q}`, sub+"d")
	default:
		http.NotFound(w, r)
	}
}

// serveEvents streams the run's queued events as SSE in harnessd's wire
// format (id/retry/event/data lines, blank-line terminated), closing the
// stream after a terminal event.
func (fh *fakeHarness) serveEvents(w http.ResponseWriter, r *http.Request, run *fakeRun) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	seq := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-run.events:
			if !ok {
				return
			}
			seq++
			payload := ev.payload
			if payload == "" {
				payload = "{}"
			}
			fmt.Fprintf(w, "id: %s:%d\n", run.id, seq)
			fmt.Fprint(w, "retry: 3000\n")
			fmt.Fprintf(w, "event: %s\n", ev.typ)
			fmt.Fprintf(w, "data: {\"id\":%q,\"run_id\":%q,\"type\":%q,\"payload\":%s}\n\n", fmt.Sprintf("%s:%d", run.id, seq), run.id, ev.typ, payload)
			if flusher != nil {
				flusher.Flush()
			}
			if ev.typ == "run.completed" || ev.typ == "run.failed" || ev.typ == "run.cancelled" {
				return
			}
		}
	}
}

// run returns the fake run with the given id (or the only run, when id is "").
func (fh *fakeHarness) run(id string) *fakeRun {
	fh.t.Helper()
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if id == "" && len(fh.runs) == 1 {
		for _, r := range fh.runs {
			return r
		}
	}
	r, ok := fh.runs[id]
	if !ok {
		fh.t.Fatalf("fake harness: no run %q (have %v)", id, fh.runs)
	}
	return r
}

func (fh *fakeHarness) promptOf(id string) string {
	fh.t.Helper()
	fh.mu.Lock()
	defer fh.mu.Unlock()
	return fh.runs[id].prompt
}

func (fh *fakeHarness) cancelled(id string) bool {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	for _, c := range fh.cancels {
		if c == id {
			return true
		}
	}
	return false
}

// decision returns "approve", "deny", or "" depending on what was POSTed for
// the given run.
func (fh *fakeHarness) decision(id string) string {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	for _, d := range fh.decisions {
		if strings.HasPrefix(d, id+":") {
			return strings.TrimPrefix(d, id+":")
		}
	}
	return ""
}
