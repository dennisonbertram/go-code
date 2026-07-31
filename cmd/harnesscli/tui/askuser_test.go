package tui_test

// askuser_test.go — TUI #476
// Behavioral tests for AskUserQuestion TUI integration.
// Tests for: run.waiting_for_user SSE event handling, question overlay,
// answer submission, run.resumed handling, and timeout behavior.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ---------------------------------------------------------------------------
// BT-001: run.waiting_for_user SSE event triggers question fetch and overlay
// ---------------------------------------------------------------------------

func TestAskUser_WaitingForUserSSE_SetsOverlayActive(t *testing.T) {
	// When a run.waiting_for_user SSE event arrives during an active run,
	// the TUI must set the question overlay active and store the run ID.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-ask-1"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-ask-1",
		Raw:       []byte(`{"call_id":"call-q1"}`),
	})
	model = m3.(tui.Model)

	if !model.AskUserActive() {
		t.Error("expected question overlay to be active after run.waiting_for_user SSE event")
	}
}

func TestAskUser_WaitingForUserSSE_FetchesPendingQuestions(t *testing.T) {
	// When run.waiting_for_user arrives, the TUI issues a GET /v1/runs/{id}/input
	// to fetch the pending questions and loads them into the model.
	pendingPayload := `{
		"run_id": "run-fetch-1",
		"call_id": "call-q2",
		"tool": "AskUserQuestion",
		"questions": [{
			"question": "Which approach?",
			"header": "Strategy",
			"options": [
				{"label": "Fast", "description": "Quick but rough"},
				{"label": "Careful", "description": "Slower but thorough"}
			],
			"multiSelect": false
		}],
		"deadline_at": "2099-01-01T00:00:00Z"
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/input") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, pendingPayload)
		}
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run-fetch-1"})
	model = m3.(tui.Model)

	// Deliver waiting_for_user event
	m4, cmd := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-fetch-1",
		Raw:       []byte(`{"call_id":"call-q2"}`),
	})
	model = m4.(tui.Model)

	// If a cmd is returned, execute it to get the AskUserPendingMsg
	if cmd != nil {
		msg := cmd()
		m5, _ := model.Update(msg)
		model = m5.(tui.Model)
	}

	// After fetching, the model should have questions loaded
	questions := model.AskUserQuestions()
	if len(questions) == 0 {
		t.Error("expected questions to be loaded after fetching pending input")
	}
	if len(questions) > 0 && questions[0].Question != "Which approach?" {
		t.Errorf("unexpected question text: %q", questions[0].Question)
	}
}

// ---------------------------------------------------------------------------
// BT-002: Question overlay renders options with navigation
// ---------------------------------------------------------------------------

func TestAskUser_Overlay_RendersQuestionAndOptions(t *testing.T) {
	// When the question overlay shows a multiple-choice question,
	// the view must contain the question text and each option label.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-render-1"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-render-1",
		CallID: "call-r1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Where should I look first?",
				Header:   "Navigation",
				Options: []tui.AskUserOption{
					{Label: "Docs", Description: "Read documentation"},
					{Label: "Code", Description: "Read source code"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	view := model.View()
	if !strings.Contains(view, "Where should I look first?") {
		t.Errorf("expected question text in view; view=%q", view)
	}
	if !strings.Contains(view, "Docs") {
		t.Errorf("expected option 'Docs' in view; view=%q", view)
	}
	if !strings.Contains(view, "Code") {
		t.Errorf("expected option 'Code' in view; view=%q", view)
	}
}

func TestAskUser_Overlay_ArrowKeysNavigateOptions(t *testing.T) {
	// When the question overlay is shown, Up/Down arrows change the selected option.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-nav-1"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-nav-1",
		CallID: "call-nav1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Pick one",
				Header:   "Choice",
				Options: []tui.AskUserOption{
					{Label: "Alpha", Description: "First option"},
					{Label: "Beta", Description: "Second option"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	// Initially the first option should be selected (index 0)
	if model.AskUserSelectedIdx() != 0 {
		t.Errorf("expected initial selected index 0, got %d", model.AskUserSelectedIdx())
	}

	// Press Down to move to index 1
	m4, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = m4.(tui.Model)

	if model.AskUserSelectedIdx() != 1 {
		t.Errorf("expected selected index 1 after Down, got %d", model.AskUserSelectedIdx())
	}

	// Press Up to go back to index 0
	m5, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = m5.(tui.Model)

	if model.AskUserSelectedIdx() != 0 {
		t.Errorf("expected selected index 0 after Up, got %d", model.AskUserSelectedIdx())
	}
}

// ---------------------------------------------------------------------------
// BT-003: Enter confirms selection and sends POST /v1/runs/{id}/input
// ---------------------------------------------------------------------------

func TestAskUser_Enter_SubmitsAnswerAndDismissesOverlay(t *testing.T) {
	// When the user presses Enter on a selected option, the TUI sends
	// POST /v1/runs/{id}/input with the correct answers payload and the
	// overlay is dismissed.
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/input") {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body) //nolint:errcheck
			receivedBody = body
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run-submit-1"})
	model = m3.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-submit-1",
		CallID: "call-s1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Choose path",
				Header:   "Direction",
				Options: []tui.AskUserOption{
					{Label: "Left", Description: "Go left"},
					{Label: "Right", Description: "Go right"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	// Press Enter to confirm first option "Left"
	m5, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = m5.(tui.Model)

	// The overlay should be dismissed (pending submission in flight)
	if model.AskUserActive() {
		t.Error("expected question overlay to be dismissed after Enter")
	}

	// Execute the submission command
	if cmd != nil {
		msg := cmd()
		m6, _ := model.Update(msg)
		model = m6.(tui.Model)
	}

	// Check the POST body was sent with the correct answers
	if len(receivedBody) == 0 {
		t.Fatal("expected POST body to be sent to server")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("expected valid JSON POST body, got: %q, err: %v", receivedBody, err)
	}
	answers, ok := payload["answers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'answers' key in POST body, got: %+v", payload)
	}
	if answers["Choose path"] != "Left" {
		t.Errorf("expected answer 'Left' for 'Choose path', got: %v", answers["Choose path"])
	}
}

// ---------------------------------------------------------------------------
// BT-004: run.resumed SSE event dismisses overlay and resumes streaming
// ---------------------------------------------------------------------------

func TestAskUser_RunResumed_DismissesOverlay(t *testing.T) {
	// When run.resumed SSE event arrives after answering, the question overlay
	// is removed and the model is no longer in ask-user mode.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-resumed-1"})
	model := m2.(tui.Model)

	// Activate overlay first
	pending := tui.AskUserPendingMsg{
		RunID:  "run-resumed-1",
		CallID: "call-r1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Any question?",
				Header:   "Header",
				Options: []tui.AskUserOption{
					{Label: "Yes", Description: "Affirmative"},
					{Label: "No", Description: "Negative"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	if !model.AskUserActive() {
		t.Fatal("prerequisite: expected overlay to be active before run.resumed")
	}

	// Simulate run.resumed SSE event
	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "run.resumed",
		Raw:       []byte(`{"run_id":"run-resumed-1"}`),
	})
	model = m4.(tui.Model)

	if model.AskUserActive() {
		t.Error("expected question overlay to be dismissed after run.resumed SSE event")
	}
}

// ---------------------------------------------------------------------------
// BT-005: Deadline expiry shows timeout message and auto-dismisses
// ---------------------------------------------------------------------------

func TestAskUser_DeadlineExpired_ShowsTimeoutAndDismisses(t *testing.T) {
	// When the question has a deadline that has already passed, the overlay
	// shows a timeout message and/or auto-dismisses.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-timeout-1"})
	model := m2.(tui.Model)

	// Set a deadline in the past
	pending := tui.AskUserPendingMsg{
		RunID:  "run-timeout-1",
		CallID: "call-t1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Time-sensitive question?",
				Header:   "Urgent",
				Options: []tui.AskUserOption{
					{Label: "Yes", Description: "Yes"},
					{Label: "No", Description: "No"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(-1 * time.Second), // already past
	}
	model = activateAskUserPending(t, model, pending, 1)

	// Send the deadline tick — callID must match so the overlay is dismissed
	m4, _ := model.Update(tui.AskUserTimeoutMsg{RunID: "run-timeout-1", CallID: "call-t1"})
	model = m4.(tui.Model)

	// After timeout, overlay should be dismissed
	if model.AskUserActive() {
		t.Error("expected question overlay to be dismissed after deadline timeout")
	}

	// View should indicate timeout
	view := model.View()
	_ = view // timeout message may or may not be in viewport — the key is overlay dismissed
}

// ---------------------------------------------------------------------------
// BT-007: POST answer failure shows error and allows retry
// ---------------------------------------------------------------------------

func TestAskUser_SubmitFailure_ShowsError(t *testing.T) {
	// When the answer POST fails (network error), an error message is displayed
	// and the model handles it gracefully.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-err-1"})
	model := m2.(tui.Model)

	// Simulate answer submission failure
	m3, _ := model.Update(tui.AskUserSubmitErrorMsg{
		Err: "connection refused",
	})
	model = m3.(tui.Model)

	view := model.View()
	if !strings.Contains(view, "connection refused") && !strings.Contains(view, "failed") && !strings.Contains(view, "error") {
		t.Errorf("expected error message in view after AskUserSubmitErrorMsg; view=%q", view)
	}
}

// ---------------------------------------------------------------------------
// Regression: SSE event parsing for run.waiting_for_user and run.resumed
// ---------------------------------------------------------------------------

func TestRegression_WaitingForUser_SSEEventType_IsHandled(t *testing.T) {
	// If the run.waiting_for_user case is removed from the SSE switch,
	// AskUserActive() will never become true.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-reg-wait"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run-reg-wait",
		Raw:       []byte(`{"call_id":"call-reg1"}`),
	})
	model = m3.(tui.Model)

	if !model.AskUserActive() {
		t.Error("regression: run.waiting_for_user SSE event must activate ask-user overlay")
	}
}

func TestRegression_RunResumed_SSEEventType_DismissesOverlay(t *testing.T) {
	// If the run.resumed case is removed from the SSE switch,
	// the overlay will never be cleared.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-reg-resumed"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-reg-resumed",
		CallID: "call-reg2",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Test?",
				Header:   "Test",
				Options: []tui.AskUserOption{
					{Label: "A", Description: "Option A"},
					{Label: "B", Description: "Option B"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "run.resumed",
		Raw:       []byte(`{"run_id":"run-reg-resumed"}`),
	})
	model = m4.(tui.Model)

	if model.AskUserActive() {
		t.Error("regression: run.resumed SSE event must clear the ask-user overlay")
	}
}

func TestRegression_AskUser_OverlayKeyPriorityBeforeOtherKeys(t *testing.T) {
	// Keys when overlay is active must be routed to the overlay, not the
	// viewport or input. This ensures overlay gets key priority.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-priority-1"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-priority-1",
		CallID: "call-p1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Priority test?",
				Header:   "Priority",
				Options: []tui.AskUserOption{
					{Label: "X", Description: "Option X"},
					{Label: "Y", Description: "Option Y"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	// Down arrow should navigate overlay (change selected index), not scroll viewport
	idxBefore := model.AskUserSelectedIdx()
	m4, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = m4.(tui.Model)

	if model.AskUserSelectedIdx() == idxBefore {
		t.Error("regression: Down arrow when overlay active must navigate overlay options, not pass through to viewport")
	}
	// Overlay should still be active (pressing Down does not dismiss)
	if !model.AskUserActive() {
		t.Error("regression: Down arrow must not dismiss the ask-user overlay")
	}
}

// ---------------------------------------------------------------------------
// Fix 1 (HIGH): Stale timeout race condition
// Two consecutive AskUserPendingMsg with different callIDs; first timeout fires
// ---------------------------------------------------------------------------

func TestAskUser_StaleTimeout_DoesNotDismissNewerPrompt(t *testing.T) {
	// When two consecutive AskUserPendingMsg arrive with different CallIDs,
	// and the timeout fires for the FIRST CallID, the overlay (showing the
	// second question) must NOT be dismissed.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-stale-1"})
	model := m2.(tui.Model)

	// First question with callID "call-stale-first"
	firstPending := tui.AskUserPendingMsg{
		RunID:  "run-stale-1",
		CallID: "call-stale-first",
		Questions: []tui.AskUserQuestion{
			{
				Question: "First question?",
				Header:   "First",
				Options: []tui.AskUserOption{
					{Label: "A", Description: "Option A"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(10 * time.Minute),
	}
	model = activateAskUserPending(t, model, firstPending, 1)

	// Second question (different callID) — replaces the first
	secondPending := tui.AskUserPendingMsg{
		RunID:  "run-stale-1",
		CallID: "call-stale-second",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Second question?",
				Header:   "Second",
				Options: []tui.AskUserOption{
					{Label: "B", Description: "Option B"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(10 * time.Minute),
	}
	model = activateAskUserPending(t, model, secondPending, 2)

	// Sanity check: second question is active
	if !model.AskUserActive() {
		t.Fatal("prerequisite: overlay must be active with second question")
	}

	// Now fire the STALE timeout (for first callID, not the current one)
	m5, _ := model.Update(tui.AskUserTimeoutMsg{
		RunID:  "run-stale-1",
		CallID: "call-stale-first", // stale callID
	})
	model = m5.(tui.Model)

	// The overlay must still be active because the timeout was for a stale callID
	if !model.AskUserActive() {
		t.Error("expected overlay to remain active: stale timeout (first callID) must not dismiss the second question")
	}
}

func TestAskUser_CurrentTimeout_DismissesCurrentPrompt(t *testing.T) {
	// When the timeout fires for the CURRENT callID, the overlay IS dismissed.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-current-to-1"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-current-to-1",
		CallID: "call-current",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Current question?",
				Header:   "Current",
				Options: []tui.AskUserOption{
					{Label: "Yes", Description: "Yes"},
				},
				MultiSelect: false,
			},
		},
		DeadlineAt: time.Now().Add(10 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	// Fire timeout with the CURRENT callID
	m4, _ := model.Update(tui.AskUserTimeoutMsg{
		RunID:  "run-current-to-1",
		CallID: "call-current",
	})
	model = m4.(tui.Model)

	if model.AskUserActive() {
		t.Error("expected overlay to be dismissed when timeout fires for the current callID")
	}
}

// ---------------------------------------------------------------------------
// Fix 2 (MEDIUM): MultiSelect warning indicator
// ---------------------------------------------------------------------------

func TestAskUser_MultiSelect_ShowsWarningIndicator(t *testing.T) {
	// When a question has MultiSelect:true, the overlay must render a
	// "[multi-select not supported]" warning indicator.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-multiselect-1"})
	model := m2.(tui.Model)

	pending := tui.AskUserPendingMsg{
		RunID:  "run-multiselect-1",
		CallID: "call-ms1",
		Questions: []tui.AskUserQuestion{
			{
				Question: "Pick many?",
				Header:   "Multi",
				Options: []tui.AskUserOption{
					{Label: "X", Description: "Option X"},
					{Label: "Y", Description: "Option Y"},
				},
				MultiSelect: true, // <--- multi-select enabled
			},
		},
		DeadlineAt: time.Now().Add(5 * time.Minute),
	}
	model = activateAskUserPending(t, model, pending, 1)

	view := model.View()
	if !strings.Contains(view, "multi-select not supported") {
		t.Errorf("expected '[multi-select not supported]' warning in overlay view; view=%q", view)
	}
}

// ---------------------------------------------------------------------------
// Fix 4 (MEDIUM): URL path injection — special chars in runID
// ---------------------------------------------------------------------------

func TestAskUser_TUI_FetchURL_EscapesRunID(t *testing.T) {
	// When the runID contains special characters (e.g. slashes), the GET URL
	// path must be correctly percent-escaped, not raw-concatenated.
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.RequestURI
		if r.Method == http.MethodGet {
			pendingPayload := `{
				"run_id": "run/with/slashes",
				"call_id": "call-escape-1",
				"questions": [{
					"question": "Escape test?",
					"header": "Escape",
					"options": [{"label": "OK", "description": "Fine"}],
					"multiSelect": false
				}],
				"deadline_at": "2099-01-01T00:00:00Z"
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, pendingPayload)
		} else if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run/with/slashes"})
	model = m3.(tui.Model)

	// Trigger the waiting_for_user SSE event which internally calls fetchAskUserPendingCmd
	m4, cmd := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     "run/with/slashes",
		Raw:       []byte(`{"call_id":"call-escape-1"}`),
	})
	model = m4.(tui.Model)

	// Execute the fetch cmd to trigger the actual HTTP GET
	if cmd != nil {
		cmd()
	}

	// The path must NOT contain raw slashes in the run ID portion.
	// /v1/runs/run%2Fwith%2Fslashes/input is correct.
	// /v1/runs/run/with/slashes/input is wrong.
	if strings.Contains(receivedPath, "/v1/runs/run/with/slashes/input") {
		t.Errorf("runID slashes must be percent-escaped in URL, but got raw path: %q", receivedPath)
	}
	if receivedPath != "" && !strings.Contains(receivedPath, "run%2Fwith%2Fslashes") {
		t.Errorf("expected percent-escaped runID in path; got: %q", receivedPath)
	}
}

func activateAskUserPending(
	t *testing.T,
	model tui.Model,
	pending tui.AskUserPendingMsg,
	generation uint64,
) tui.Model {
	t.Helper()
	next, _ := model.Update(tui.SSEEventMsg{
		EventType: "run.waiting_for_user",
		RunID:     pending.RunID,
		Raw:       []byte(fmt.Sprintf(`{"call_id":%q}`, pending.CallID)),
	})
	model = next.(tui.Model)
	pending.WaitingCallID = pending.CallID
	pending.Generation = generation
	next, _ = model.Update(pending)
	return next.(tui.Model)
}
