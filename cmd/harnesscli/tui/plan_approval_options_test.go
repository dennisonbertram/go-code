package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Slice 4 of epic #819: when plan.approval_required carries approach options,
// the plan-approval overlay renders a selectable list (reusing the askuser
// cursor idiom), enter/a approves with the highlighted option ID in the POST
// body, d still requests changes. A no-options payload renders and behaves
// exactly as before.

func TestPlanApprovalOverlayRendersOptionsAndMovesCursor(t *testing.T) {
	m := New(TUIConfig{})
	m.planApproval = planApprovalState{
		active:  true,
		runID:   "r",
		content: "# Plan\n\nBuild it.",
		options: []planApproachOption{
			{ID: "a", Label: "Incremental", Description: "migrate piece by piece"},
			{ID: "b", Label: "Big bang", Description: "rewrite in one pass"},
		},
	}
	view := strings.Join(m.renderPlanApprovalOverlay(), "\n")
	for _, want := range []string{"Plan Approval Required", "Approaches:", "Incremental", "migrate piece by piece", "Big bang", "▶ Incremental"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	st, _ := m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyDown})
	if st.selectedIdx != 1 {
		t.Fatalf("selectedIdx=%d after down, want 1", st.selectedIdx)
	}
	if st.offset != 0 {
		t.Fatalf("offset=%d after down, want 0 (down moves the option cursor, not the scroll)", st.offset)
	}
	m.planApproval = st
	view = strings.Join(m.renderPlanApprovalOverlay(), "\n")
	if !strings.Contains(view, "▶ Big bang") {
		t.Fatalf("cursor did not move to Big bang:\n%s", view)
	}

	st, _ = m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyDown})
	if st.selectedIdx != 1 {
		t.Fatalf("selectedIdx=%d after down at end, want clamped to 1", st.selectedIdx)
	}
	st, _ = m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyUp})
	if st.selectedIdx != 0 {
		t.Fatalf("selectedIdx=%d after up, want 0", st.selectedIdx)
	}
}

// approveRequest captures what the TUI posted to the approve/deny endpoints.
type approveRequest struct {
	path   string
	option string
}

func runPlanApproveKey(t *testing.T, st planApprovalState, key tea.KeyMsg) approveRequest {
	t.Helper()
	received := make(chan approveRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Option string `json:"option"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		received <- approveRequest{path: r.URL.Path, option: body.Option}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := New(TUIConfig{BaseURL: srv.URL})
	m.planApproval = st
	_, cmd := m.handlePlanApprovalKey(key)
	if cmd == nil {
		t.Fatal("expected an HTTP command")
	}
	_ = cmd() // ToolApprovalDecidedMsg / ToolApprovalErrorMsg; the request is what matters
	return <-received
}

func TestPlanApprovalApprovePostsSelectedOption(t *testing.T) {
	st := planApprovalState{
		active: true,
		runID:  "run-1",
		options: []planApproachOption{
			{ID: "a", Label: "Incremental"},
			{ID: "b", Label: "Big bang"},
		},
		selectedIdx: 1,
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("a")}} {
		got := runPlanApproveKey(t, st, key)
		if got.path != "/v1/runs/run-1/approve" {
			t.Fatalf("posted to %q, want /v1/runs/run-1/approve", got.path)
		}
		if got.option != "b" {
			t.Fatalf("approve body option=%q, want %q (highlighted option)", got.option, "b")
		}
	}
}

func TestPlanApprovalDenyIgnoresOptions(t *testing.T) {
	st := planApprovalState{
		active:      true,
		runID:       "run-1",
		options:     []planApproachOption{{ID: "a", Label: "Incremental"}},
		selectedIdx: 0,
	}
	got := runPlanApproveKey(t, st, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if got.path != "/v1/runs/run-1/deny" {
		t.Fatalf("posted to %q, want /v1/runs/run-1/deny", got.path)
	}
	if got.option != "" {
		t.Fatalf("deny body option=%q, want empty", got.option)
	}
}

// TestPlanApprovalNoOptionsKeepsScrollAndPlainApprove is the regression guard:
// a no-options payload scrolls and approves exactly as before slice 4.
func TestPlanApprovalNoOptionsKeepsScrollAndPlainApprove(t *testing.T) {
	m := New(TUIConfig{})
	m.planApproval = planApprovalState{active: true, runID: "run-1", content: strings.Repeat("line\n", 15)}
	view := strings.Join(m.renderPlanApprovalOverlay(), "\n")
	if strings.Contains(view, "Approaches:") || strings.Contains(view, "▶") {
		t.Fatalf("no-option overlay must render as before:\n%s", view)
	}
	st, _ := m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyDown})
	if st.offset != 1 || st.selectedIdx != 0 {
		t.Fatalf("offset=%d selectedIdx=%d after down, want scroll behavior", st.offset, st.selectedIdx)
	}

	got := runPlanApproveKey(t, planApprovalState{active: true, runID: "run-1"}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got.path != "/v1/runs/run-1/approve" || got.option != "" {
		t.Fatalf("plain approve posted path=%q option=%q, want /approve with empty option", got.path, got.option)
	}
}

func TestPlanApprovalRequiredEventParsesOptions(t *testing.T) {
	m := New(TUIConfig{})
	updated, _ := m.Update(SSEEventMsg{
		EventType: "plan.approval_required",
		Raw: []byte(`{"tool":"plan_exit","plan":"# Plan","options":[` +
			`{"id":"a","label":"Incremental","description":"migrate piece by piece"},` +
			`{"id":"b","label":"Big bang","description":"rewrite in one pass"}]}`),
	})
	m = updated.(Model)
	if !m.planApproval.active {
		t.Fatal("overlay not active after plan.approval_required")
	}
	if len(m.planApproval.options) != 2 {
		t.Fatalf("options=%#v, want 2", m.planApproval.options)
	}
	if m.planApproval.options[1].ID != "b" || m.planApproval.options[1].Label != "Big bang" {
		t.Fatalf("option[1]=%#v, want {b Big bang}", m.planApproval.options[1])
	}

	updated, _ = m.Update(SSEEventMsg{
		EventType: "plan.approval_required",
		Raw:       []byte(`{"tool":"plan_exit","plan":"# Plan"}`),
	})
	m = updated.(Model)
	if len(m.planApproval.options) != 0 {
		t.Fatalf("no-option payload left stale options: %#v", m.planApproval.options)
	}
}
