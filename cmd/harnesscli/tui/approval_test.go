package tui_test

// approval_test.go — TUI tool.approval_required regression tests.
// Verifies the tool-approval overlay activates on the tool.approval_required
// SSE event, gates keys while active, and drives approve/deny HTTP calls.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// ---------------------------------------------------------------------------
// tool.approval_required SSE event activates the overlay
// ---------------------------------------------------------------------------

func TestToolApproval_ApprovalRequiredSSE_SetsOverlayActive(t *testing.T) {
	// When a tool.approval_required SSE event arrives during an active run,
	// the TUI must set the tool-approval overlay active with the tool name.
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-approval-1"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-a1","tool":"bash","arguments":"{\"command\":\"rm -rf /\"}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m3.(tui.Model)

	if !model.ToolApprovalActive() {
		t.Fatal("expected tool-approval overlay to be active after tool.approval_required SSE event")
	}
	if model.ToolApprovalTool() != "bash" {
		t.Errorf("expected tool name 'bash', got %q", model.ToolApprovalTool())
	}
	if model.ToolApprovalCallID() != "call-a1" {
		t.Errorf("expected call ID 'call-a1', got %q", model.ToolApprovalCallID())
	}
	if !strings.Contains(model.ToolApprovalArguments(), "rm -rf /") {
		t.Errorf("expected formatted arguments to contain command text, got %q", model.ToolApprovalArguments())
	}
}

// TestRegression_ToolApprovalRequired_SSEEventType_IsHandled fails if the
// tool.approval_required case is removed from the SSE dispatch switch —
// without it the overlay never activates and an approval-required run hangs
// forever in the TUI.
func TestRegression_ToolApprovalRequired_SSEEventType_IsHandled(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-reg-approval"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-reg1","tool":"write_file","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m3.(tui.Model)

	if !model.ToolApprovalActive() {
		t.Error("regression: tool.approval_required SSE event must activate the tool-approval overlay")
	}
}

// ---------------------------------------------------------------------------
// Overlay renders the tool name and prompt
// ---------------------------------------------------------------------------

func TestToolApproval_Overlay_RendersToolAndPrompt(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-approval-render"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-r1","tool":"delete_database","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m3.(tui.Model)

	view := model.View()
	if !strings.Contains(view, "delete_database") {
		t.Errorf("expected tool name in view; view=%q", view)
	}
	if !strings.Contains(view, "[a]pprove") || !strings.Contains(view, "[d]eny") {
		t.Errorf("expected approve/deny prompt in view; view=%q", view)
	}
}

// ---------------------------------------------------------------------------
// Key priority: overlay swallows keys, does not leak into the input box
// ---------------------------------------------------------------------------

func TestRegression_ToolApproval_OverlayKeyPriorityBeforeOtherKeys(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-approval-priority"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-p1","tool":"exec","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m3.(tui.Model)

	if !model.ToolApprovalActive() {
		t.Fatal("prerequisite: expected overlay to be active")
	}

	// An unrelated key (not a/y/d/n/enter/esc) must be swallowed: overlay
	// stays active and nothing leaks into the input box.
	m4, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	model = m4.(tui.Model)

	if !model.ToolApprovalActive() {
		t.Error("regression: unrelated key must not dismiss the tool-approval overlay")
	}
	if model.Input() != "" {
		t.Errorf("regression: key must not leak into input box while overlay active, got %q", model.Input())
	}
}

// ---------------------------------------------------------------------------
// Pressing 'a' approves: POST /v1/runs/{id}/approve
// ---------------------------------------------------------------------------

func TestToolApproval_ApproveKey_PostsApprove(t *testing.T) {
	var receivedPath, receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run-approve-1"})
	model = m3.(tui.Model)

	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-ap1","tool":"bash","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m4.(tui.Model)

	m5, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	model = m5.(tui.Model)

	if model.ToolApprovalActive() {
		t.Error("expected tool-approval overlay to be dismissed immediately after pressing 'a'")
	}
	if cmd == nil {
		t.Fatal("expected an approve command to be produced after pressing 'a'")
	}

	msg := cmd()
	if _, ok := msg.(tui.ToolApprovalDecidedMsg); !ok {
		t.Fatalf("expected ToolApprovalDecidedMsg from approve command, got %T", msg)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST method, got %q", receivedMethod)
	}
	if !strings.HasSuffix(receivedPath, "/v1/runs/run-approve-1/approve") {
		t.Errorf("expected POST to .../approve, got path %q", receivedPath)
	}
}

// ---------------------------------------------------------------------------
// Pressing 'd' denies: POST /v1/runs/{id}/deny
// ---------------------------------------------------------------------------

func TestToolApproval_DenyKey_PostsDeny(t *testing.T) {
	var receivedPath, receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run-deny-1"})
	model = m3.(tui.Model)

	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-dn1","tool":"bash","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m4.(tui.Model)

	m5, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model = m5.(tui.Model)

	if model.ToolApprovalActive() {
		t.Error("expected tool-approval overlay to be dismissed immediately after pressing 'd'")
	}
	if cmd == nil {
		t.Fatal("expected a deny command to be produced after pressing 'd'")
	}

	msg := cmd()
	decided, ok := msg.(tui.ToolApprovalDecidedMsg)
	if !ok {
		t.Fatalf("expected ToolApprovalDecidedMsg from deny command, got %T", msg)
	}
	if decided.Decision != "denied" {
		t.Errorf("expected decision 'denied', got %q", decided.Decision)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST method, got %q", receivedMethod)
	}
	if !strings.HasSuffix(receivedPath, "/v1/runs/run-deny-1/deny") {
		t.Errorf("expected POST to .../deny, got path %q", receivedPath)
	}
}

// ---------------------------------------------------------------------------
// Approve/deny failure surfaces a status message instead of hanging silently
// ---------------------------------------------------------------------------

func TestToolApproval_DecisionFailure_ShowsError(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-approval-err"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.ToolApprovalErrorMsg{Err: "connection refused"})
	model = m3.(tui.Model)

	view := model.View()
	if !strings.Contains(view, "connection refused") {
		t.Errorf("expected error message in view after ToolApprovalErrorMsg; view=%q", view)
	}
}

// ---------------------------------------------------------------------------
// Esc cancels the run via the existing /cancel plumbing
// ---------------------------------------------------------------------------

func TestToolApproval_Esc_CancelsRun(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = srv.URL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := m2.(tui.Model)
	model = model.WithCancelRun(func() {})
	m3, _ := model.Update(tui.RunStartedMsg{RunID: "run-esc-1"})
	model = m3.(tui.Model)

	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-esc1","tool":"bash","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m4.(tui.Model)

	m5, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = m5.(tui.Model)

	if model.ToolApprovalActive() {
		t.Error("expected tool-approval overlay to be dismissed after esc")
	}
	if cmd == nil {
		t.Fatal("expected a cancel command to be produced after esc")
	}
	cmd()

	if !strings.HasSuffix(receivedPath, "/v1/runs/run-esc-1/cancel") {
		t.Errorf("expected esc to POST to .../cancel, got path %q", receivedPath)
	}
}

// ---------------------------------------------------------------------------
// tool.approval_granted / tool.approval_denied clear a lingering overlay
// ---------------------------------------------------------------------------

func TestToolApproval_GrantedSSE_ClearsOverlay(t *testing.T) {
	m := initModel(t, 80, 24)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-granted-1"})
	model := m2.(tui.Model)

	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_required",
		Raw:       []byte(`{"call_id":"call-g1","tool":"bash","arguments":"{}","deadline_at":"2099-01-01T00:00:00Z"}`),
	})
	model = m3.(tui.Model)

	if !model.ToolApprovalActive() {
		t.Fatal("prerequisite: expected overlay to be active")
	}

	m4, _ := model.Update(tui.SSEEventMsg{
		EventType: "tool.approval_granted",
		Raw:       []byte(`{"call_id":"call-g1","tool":"bash"}`),
	})
	model = m4.(tui.Model)

	if model.ToolApprovalActive() {
		t.Error("expected tool.approval_granted SSE event to clear the overlay")
	}
}
