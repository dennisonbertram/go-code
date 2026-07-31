package tui_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
)

func TestAskUser_LatePendingAfterResumeIsDiscarded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writePendingQuestion(w, "run-resume-race", "call-resume-race", "Already answered?")
	}))
	defer srv.Close()

	model := newAskUserRaceModel(t, srv.URL, "run-resume-race")
	next, fetchCmd := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-resume-race",
		Raw:       []byte(`{"call_id":"call-resume-race"}`),
	})
	model = next.(tui.Model)
	if fetchCmd == nil {
		t.Fatal("expected waiting event to start pending-input fetch")
	}

	fetched := make(chan tea.Msg, 1)
	go func() { fetched <- fetchCmd() }()
	waitForAskUserRaceSignal(t, started, "pending GET to start")

	next, _ = model.Update(tui.SSEEventMsg{
		EventType: "run.resumed",
		RunID:     "run-resume-race",
		Raw:       []byte(`{"call_id":"call-resume-race"}`),
	})
	model = next.(tui.Model)
	close(release)

	select {
	case msg := <-fetched:
		next, _ = model.Update(msg)
		model = next.(tui.Model)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for released pending GET")
	}

	if model.AskUserActive() {
		t.Fatal("late pending result resurrected overlay after run.resumed")
	}
	if strings.Contains(model.View(), "Already answered?") {
		t.Fatal("late pending result rendered an already-answered question")
	}
}

func TestAskUser_SupersededPendingFetchIsDiscarded(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch requests.Add(1) {
		case 1:
			close(firstStarted)
			<-releaseFirst
			writePendingQuestion(w, "run-superseded", "call-old", "Old question?")
		case 2:
			writePendingQuestion(w, "run-superseded", "call-new", "New question?")
		default:
			http.Error(w, "unexpected pending fetch", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	model := newAskUserRaceModel(t, srv.URL, "run-superseded")
	next, oldFetchCmd := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-superseded",
		Raw:       []byte(`{"call_id":"call-old"}`),
	})
	model = next.(tui.Model)
	if oldFetchCmd == nil {
		t.Fatal("expected first waiting event to start pending-input fetch")
	}
	oldFetched := make(chan tea.Msg, 1)
	go func() { oldFetched <- oldFetchCmd() }()
	waitForAskUserRaceSignal(t, firstStarted, "first pending GET to start")

	next, newFetchCmd := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-superseded",
		Raw:       []byte(`{"call_id":"call-new"}`),
	})
	model = next.(tui.Model)
	if newFetchCmd == nil {
		t.Fatal("expected newer waiting event to start pending-input fetch")
	}
	next, _ = model.Update(newFetchCmd())
	model = next.(tui.Model)
	if !strings.Contains(model.View(), "New question?") {
		t.Fatalf("newer wait did not render before old GET completed; view=%q", model.View())
	}

	close(releaseFirst)
	select {
	case msg := <-oldFetched:
		next, _ = model.Update(msg)
		model = next.(tui.Model)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for old pending GET")
	}

	view := model.View()
	if !strings.Contains(view, "New question?") {
		t.Fatalf("late old fetch overwrote the newer question; view=%q", view)
	}
	if strings.Contains(view, "Old question?") {
		t.Fatalf("late old fetch rendered superseded question; view=%q", view)
	}
}

func newAskUserRaceModel(t *testing.T, baseURL, runID string) tui.Model {
	t.Helper()
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = baseURL
	model := tui.New(cfg)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = next.(tui.Model).WithCancelRun(func() {})
	next, _ = model.Update(tui.RunStartedMsg{RunID: runID})
	return next.(tui.Model)
}

func waitForAskUserRaceSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func writePendingQuestion(w http.ResponseWriter, runID, callID, question string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(
		w,
		`{"run_id":%q,"call_id":%q,"tool":"AskUserQuestion","questions":[{"question":%q,"header":"Race","options":[{"label":"Answer","description":"Continue"}],"multiSelect":false}],"deadline_at":"2099-01-01T00:00:00Z"}`,
		runID,
		callID,
		question,
	)
}
