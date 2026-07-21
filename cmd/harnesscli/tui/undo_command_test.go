package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// collectCmdMsgs runs cmd (unwrapping tea.BatchMsg) and records the last
// produced message for which keep returns true into *out. Sub-commands that
// do not return promptly (e.g. status-expiry ticks) are skipped after a
// short grace period so picker-flow tests stay fast.
func collectCmdMsgs(cmd tea.Cmd, out *tea.Msg, keep func(tea.Msg) bool) {
	if cmd == nil {
		return
	}
	result := cmd()
	if result == nil {
		return
	}
	if batch, ok := result.(tea.BatchMsg); ok {
		for _, sub := range batch {
			collectSubCmdMsgs(sub, out, keep)
		}
		return
	}
	if keep(result) {
		*out = result
	}
}

func collectSubCmdMsgs(cmd tea.Cmd, out *tea.Msg, keep func(tea.Msg) bool) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case r := <-ch:
		if r == nil {
			return
		}
		if batch, ok := r.(tea.BatchMsg); ok {
			for _, sub := range batch {
				collectSubCmdMsgs(sub, out, keep)
			}
			return
		}
		if keep(r) {
			*out = r
		}
	case <-time.After(500 * time.Millisecond):
		// Delayed commands (status-expiry ticks) are irrelevant to these tests.
	}
}

// ---------------------------------------------------------------------------
// Epic #805 Slice 3: /undo command tests
// ---------------------------------------------------------------------------

// undoTestServer returns an httptest.Server that serves the undo endpoint and
// the messages refetch, recording the last undo request body.
func undoTestServer(t *testing.T, status int, undoBody string, messagesBody string, got *struct {
	method string
	path   string
	body   string
}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/undo") {
			got.method = r.Method
			got.path = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			got.body = string(b)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(undoBody))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(messagesBody))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// TestUndoConversationCmd_Success verifies undoConversationCmd POSTs the count
// to /v1/conversations/{id}/undo, decodes the result, and refetches the
// trimmed history for the viewport rebuild.
func TestUndoConversationCmd_Success(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true,"removed_from_step":2,"remaining_messages":3}`,
		`{"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"system","content":"undo boundary: removed 1 prompt(s)","is_meta":true}]}`,
		&got)
	defer ts.Close()

	msg := undoConversationCmd(ts.URL, "conv-1", 1, "")()

	if got.method != http.MethodPost {
		t.Errorf("method: want POST, got %q", got.method)
	}
	if got.path != "/v1/conversations/conv-1/undo" {
		t.Errorf("path: want /v1/conversations/conv-1/undo, got %q", got.path)
	}
	if !strings.Contains(got.body, `"count":1`) {
		t.Errorf("body missing count: %q", got.body)
	}

	res, ok := msg.(UndoResultMsg)
	if !ok {
		t.Fatalf("expected UndoResultMsg, got %T", msg)
	}
	if res.Err != "" || res.Conflict {
		t.Fatalf("unexpected error state: %+v", res)
	}
	if res.RemovedFromStep != 2 || res.RemainingMessages != 3 {
		t.Errorf("result = %+v, want removed_from_step=2 remaining=3", res)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 refetched messages, got %d: %+v", len(res.Messages), res.Messages)
	}
	if res.Messages[0].Role != "user" || res.Messages[0].Content != "q1" {
		t.Errorf("message[0] = %+v, want user q1", res.Messages[0])
	}
}

// TestUndoConversationCmd_Conflict verifies a 409 (compaction boundary) maps
// to Conflict=true with the server's explanation.
func TestUndoConversationCmd_Conflict(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusConflict,
		`{"error":{"code":"undo_crosses_compaction","message":"undo crosses compaction boundary: target prompt at step 0 is at or below compaction summary at step 2"}}`,
		`{"messages":[]}`,
		&got)
	defer ts.Close()

	msg := undoConversationCmd(ts.URL, "conv-1", 2, "")()
	res, ok := msg.(UndoResultMsg)
	if !ok {
		t.Fatalf("expected UndoResultMsg, got %T", msg)
	}
	if !res.Conflict {
		t.Errorf("expected Conflict=true for 409, got %+v", res)
	}
	if !strings.Contains(res.Err, "compaction") {
		t.Errorf("expected the server's compaction explanation, got %q", res.Err)
	}
}

// TestUndoConversationCmd_ErrorStatus verifies a non-200, non-409 response
// (e.g. 400 out-of-range) surfaces as Err, not Conflict.
func TestUndoConversationCmd_ErrorStatus(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusBadRequest,
		`{"error":{"code":"invalid_request","message":"undo count out of range"}}`,
		`{"messages":[]}`,
		&got)
	defer ts.Close()

	msg := undoConversationCmd(ts.URL, "conv-1", 99, "")()
	res, ok := msg.(UndoResultMsg)
	if !ok {
		t.Fatalf("expected UndoResultMsg, got %T", msg)
	}
	if res.Conflict {
		t.Errorf("400 must not set Conflict: %+v", res)
	}
	if res.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestUndoConversationCmd_NetworkError verifies an unreachable server yields
// Err rather than a panic.
func TestUndoConversationCmd_NetworkError(t *testing.T) {
	t.Parallel()

	msg := undoConversationCmd("http://127.0.0.1:1", "conv-err", 1, "")()
	res, ok := msg.(UndoResultMsg)
	if !ok {
		t.Fatalf("expected UndoResultMsg, got %T", msg)
	}
	if res.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestExecuteUndoCommand_BareOpensPicker verifies that bare /undo (epic #805
// slice 4) fetches the conversation history and opens the prompt picker
// overlay instead of immediately undoing (the slice-3 numeric path is
// unchanged; see TestExecuteUndoCommand_NumericArg).
func TestExecuteUndoCommand_BareOpensPicker(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true,"removed_from_step":2,"remaining_messages":3}`,
		`{"messages":[
			{"role":"user","content":"first question"},
			{"role":"assistant","content":"first answer"},
			{"role":"user","content":"second question"},
			{"role":"assistant","content":"second answer"},
			{"role":"user","content":"third question"},
			{"role":"assistant","content":"third answer"}
		]}`,
		&got)
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"
	cmds, quit := executeUndoCommand(&m, Command{Name: "undo"})
	if quit {
		t.Fatal("/undo must not quit")
	}
	msg := lastCmd(t, cmds)()

	loaded, ok := msg.(UndoCandidatesLoadedMsg)
	if !ok {
		t.Fatalf("expected UndoCandidatesLoadedMsg, got %T", msg)
	}
	if loaded.Err != "" {
		t.Fatalf("unexpected fetch error: %s", loaded.Err)
	}
	// Bare /undo must not undo anything yet: no POST to /undo may have happened.
	if got.method != "" {
		t.Fatalf("bare /undo issued %s %s before a selection was made", got.method, got.path)
	}

	m2, _ := m.Update(loaded)
	m = m2.(Model)
	if !m.overlayActive || m.activeOverlay != "undo" {
		t.Fatalf("picker overlay not open: overlayActive=%v activeOverlay=%q", m.overlayActive, m.activeOverlay)
	}
	if !m.undoPicker.IsOpen() {
		t.Fatal("undoPicker is not open")
	}
	entries := m.undoPicker.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d picker entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].Count != 1 || entries[0].Preview != "third question" {
		t.Errorf("entries[0] = %+v, want count=1 'third question' (newest first)", entries[0])
	}
	if entries[2].Count != 3 || entries[2].Preview != "first question" {
		t.Errorf("entries[2] = %+v, want count=3 'first question'", entries[2])
	}
}

// TestUndoPickerFlow_ConfirmIssuesUndoRequest drives the full picker flow:
// bare /undo → candidates loaded → picker opens → Down + Enter → the server
// receives POST /undo with the selected entry's count (epic #805 slice 4
// integration test).
func TestUndoPickerFlow_ConfirmIssuesUndoRequest(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true,"removed_from_step":2,"remaining_messages":3}`,
		`{"messages":[
			{"role":"user","content":"first question"},
			{"role":"assistant","content":"first answer"},
			{"role":"user","content":"second question"},
			{"role":"assistant","content":"second answer"},
			{"role":"user","content":"third question"},
			{"role":"assistant","content":"third answer"}
		]}`,
		&got)
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"

	// Open the picker.
	cmds, _ := executeUndoCommand(&m, Command{Name: "undo"})
	m2, _ := m.Update(lastCmd(t, cmds)())
	m = m2.(Model)
	if !m.undoPicker.IsOpen() {
		t.Fatal("picker did not open")
	}

	// Move to the 2nd-newest prompt and confirm.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(Model)
	m4, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(Model)
	if enterCmd == nil {
		t.Fatal("Enter on an enabled picker row must produce a command")
	}
	// Unwrap the (possibly batched) command to find the translated selection msg.
	var selMsg tea.Msg
	collectCmdMsgs(enterCmd, &selMsg, func(msg tea.Msg) bool {
		_, ok := msg.(UndoPickerSelectedMsg)
		return ok
	})
	if selMsg == nil {
		t.Fatal("no UndoPickerSelectedMsg produced by Enter")
	}

	// The selection closes the overlay and dispatches the undo call.
	m5, undoCmd := m.Update(selMsg)
	m = m5.(Model)
	if m.overlayActive || m.undoPicker.IsOpen() {
		t.Errorf("overlay still open after selection: overlayActive=%v pickerOpen=%v", m.overlayActive, m.undoPicker.IsOpen())
	}
	if undoCmd == nil {
		t.Fatal("selection did not dispatch the undo command")
	}
	collectCmdMsgs(undoCmd, &selMsg, func(msg tea.Msg) bool { return true })
	if got.method != http.MethodPost || got.path != "/v1/conversations/conv-undo/undo" {
		t.Fatalf("undo request: got %s %s, want POST /v1/conversations/conv-undo/undo", got.method, got.path)
	}
	if !strings.Contains(got.body, `"count":2`) {
		t.Errorf("undo body: got %q, want count=2 (2nd-newest prompt)", got.body)
	}
}

// TestUndoPickerFlow_EscapeCancelsWithoutHTTP verifies Esc closes the picker
// without issuing any undo request.
func TestUndoPickerFlow_EscapeCancelsWithoutHTTP(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true}`,
		`{"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"}]}`,
		&got)
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"
	cmds, _ := executeUndoCommand(&m, Command{Name: "undo"})
	m2, _ := m.Update(lastCmd(t, cmds)())
	m = m2.(Model)
	if !m.undoPicker.IsOpen() {
		t.Fatal("picker did not open")
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m3.(Model)
	if m.overlayActive || m.undoPicker.IsOpen() {
		t.Errorf("Esc did not close the picker: overlayActive=%v pickerOpen=%v", m.overlayActive, m.undoPicker.IsOpen())
	}
	if got.method != "" {
		t.Errorf("Esc issued %s %s — no request may be sent on cancel", got.method, got.path)
	}
}

// TestUndoPickerFlow_FetchError verifies a failed candidate fetch lands in the
// status bar without opening the picker.
func TestUndoPickerFlow_FetchError(t *testing.T) {
	t.Parallel()

	m := testRunControlModel("http://127.0.0.1:1") // unreachable
	m.conversationID = "conv-undo"
	cmds, _ := executeUndoCommand(&m, Command{Name: "undo"})
	msg := lastCmd(t, cmds)()

	loaded, ok := msg.(UndoCandidatesLoadedMsg)
	if !ok {
		t.Fatalf("expected UndoCandidatesLoadedMsg, got %T", msg)
	}
	if loaded.Err == "" {
		t.Fatal("expected a fetch error for an unreachable server")
	}
	m2, _ := m.Update(loaded)
	m = m2.(Model)
	if m.overlayActive || m.undoPicker.IsOpen() {
		t.Error("picker opened despite a failed fetch")
	}
	if !strings.Contains(m.statusMsg, "Undo failed") {
		t.Errorf("expected an undo failure status, got %q", m.statusMsg)
	}
}

// TestUndoPickerFlow_DisabledRowStaysUnselectable verifies that a prompt at or
// below the compaction boundary is skipped by navigation and cannot be
// confirmed, end to end at the model level.
func TestUndoPickerFlow_DisabledRowStaysUnselectable(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true}`,
		`{"messages":[
			{"role":"user","content":"old compacted question"},
			{"role":"system","content":"summary","is_compact_summary":true},
			{"role":"user","content":"new question"},
			{"role":"assistant","content":"new answer"}
		]}`,
		&got)
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"
	cmds, _ := executeUndoCommand(&m, Command{Name: "undo"})
	m2, _ := m.Update(lastCmd(t, cmds)())
	m = m2.(Model)

	entries := m.undoPicker.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if !entries[1].Disabled || entries[1].Preview != "old compacted question" {
		t.Errorf("entries[1] = %+v, want DISABLED 'old compacted question'", entries[1])
	}

	// Navigation can never land on the disabled row: Down from the only
	// enabled row wraps back to itself.
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(Model)
	if sel, _ := m.undoPicker.Selected(); sel.Count != 1 || sel.Disabled {
		t.Fatalf("selection landed on a disabled row: %+v", sel)
	}
}

// TestExecuteUndoCommand_NumericArg verifies /undo 3 sends count=3.
func TestExecuteUndoCommand_NumericArg(t *testing.T) {
	t.Parallel()

	var got struct{ method, path, body string }
	ts := undoTestServer(t, http.StatusOK,
		`{"undone":true,"removed_from_step":0,"remaining_messages":1}`,
		`{"messages":[]}`,
		&got)
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"
	cmds, quit := executeUndoCommand(&m, Command{Name: "undo", Args: []string{"3"}})
	if quit {
		t.Fatal("/undo must not quit")
	}
	lastCmd(t, cmds)()
	if !strings.Contains(got.body, `"count":3`) {
		t.Errorf("/undo 3 body: %q", got.body)
	}
}

// TestExecuteUndoCommand_ParseErrorsNeverCallServer verifies that malformed
// counts (non-numeric, zero, negative) and extra args surface as command
// errors without any HTTP request.
func TestExecuteUndoCommand_ParseErrorsNeverCallServer(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"abc"}, {"0"}, {"-2"}, {"1", "extra"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			requested := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested = true
			}))
			defer ts.Close()

			m := testRunControlModel(ts.URL)
			m.conversationID = "conv-undo"
			cmds, quit := executeUndoCommand(&m, Command{Name: "undo", Args: args})
			if quit {
				t.Fatal("/undo must not quit")
			}
			// The error path must produce exactly one status command — the
			// success path returns two (status + HTTP), so a second command
			// here would mean a request is about to be issued.
			if len(cmds) != 1 {
				t.Fatalf("/undo %v: expected exactly one status command, got %d", args, len(cmds))
			}
			if requested {
				t.Errorf("/undo %v issued an HTTP request despite the parse error", args)
			}
			if !strings.Contains(m.statusMsg, "/undo") {
				t.Errorf("/undo %v: expected a usage status message, got %q", args, m.statusMsg)
			}
		})
	}
}

// TestExecuteUndoCommand_NoConversation verifies /undo before any prompt is a
// command error, not a server call.
func TestExecuteUndoCommand_NoConversation(t *testing.T) {
	t.Parallel()

	requested := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer ts.Close()

	m := testRunControlModel(ts.URL) // conversationID empty
	cmds, quit := executeUndoCommand(&m, Command{Name: "undo"})
	if quit {
		t.Fatal("/undo must not quit")
	}
	// Exactly one status command — a second would mean a request was issued
	// (the success path returns status + HTTP).
	if len(cmds) != 1 {
		t.Fatalf("expected exactly one status command, got %d", len(cmds))
	}
	if requested {
		t.Error("/undo with no conversation issued an HTTP request")
	}
	if m.statusMsg == "" {
		t.Error("expected a status message explaining there is nothing to undo")
	}
}

// TestExecuteUndoCommand_RunActive verifies /undo refuses while a run is
// in-flight (its terminal persistence would clobber the undo).
func TestExecuteUndoCommand_RunActive(t *testing.T) {
	t.Parallel()

	requested := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	m.conversationID = "conv-undo"
	m.runActive = true
	cmds, quit := executeUndoCommand(&m, Command{Name: "undo"})
	if quit {
		t.Fatal("/undo must not quit")
	}
	// Exactly one status command — a second would mean a request was issued.
	if len(cmds) != 1 {
		t.Fatalf("expected exactly one status command, got %d", len(cmds))
	}
	if requested {
		t.Error("/undo during an active run issued an HTTP request")
	}
	if m.statusMsg == "" {
		t.Error("expected a status message explaining the refusal")
	}
}

// TestUndoResultMsg_RefreshesViewport verifies a successful undo rebuilds the
// viewport and transcript from the refetched history: the removed prompt's
// bubbles disappear, the kept prompt remains, and the is_meta marker is not
// rendered.
func TestUndoResultMsg_RefreshesViewport(t *testing.T) {
	t.Parallel()

	m := testRunControlModel("http://127.0.0.1:1")
	m.conversationID = "conv-undo"

	// Seed the view with two prompt/response exchanges.
	seeded := []ConversationMessage{
		{Role: "user", Content: "keep-this-prompt"},
		{Role: "assistant", Content: "keep-this-answer"},
		{Role: "user", Content: "drop-this-prompt"},
		{Role: "assistant", Content: "drop-this-answer"},
	}
	for _, e := range seeded {
		m.appendConversationMessages([]ConversationMessage{e})
	}
	if out := m.vp.View(); !strings.Contains(out, "drop-this-prompt") {
		t.Fatalf("seed failed: viewport does not contain the prompt to be dropped")
	}

	m2, _ := m.Update(UndoResultMsg{
		RemovedFromStep:   2,
		RemainingMessages: 3,
		Messages: []ConversationMessage{
			{Role: "user", Content: "keep-this-prompt"},
			{Role: "assistant", Content: "keep-this-answer"},
			{Role: "system", Content: "undo boundary: removed 1 prompt(s); conversation truncated from step 2"},
		},
	})
	m = m2.(Model)

	out := m.vp.View()
	if strings.Contains(out, "drop-this-prompt") || strings.Contains(out, "drop-this-answer") {
		t.Errorf("viewport still shows the removed prompt/answer after undo:\n%s", out)
	}
	if !strings.Contains(out, "keep-this-prompt") || !strings.Contains(out, "keep-this-answer") {
		t.Errorf("viewport lost the kept prompt/answer after undo:\n%s", out)
	}
	if strings.Contains(out, "undo boundary") {
		t.Errorf("is_meta undo marker leaked into the viewport:\n%s", out)
	}
	if len(m.transcript) != 2 {
		t.Errorf("transcript: got %d entries, want 2 (kept prompt + answer)", len(m.transcript))
	}
	if !strings.Contains(m.statusMsg, "Undid") {
		t.Errorf("expected an undo confirmation status, got %q", m.statusMsg)
	}
}

// TestUndoResultMsg_ConflictShowsExplanation verifies a 409 refusal renders
// the compaction-boundary explanation inline and leaves the view intact.
func TestUndoResultMsg_ConflictShowsExplanation(t *testing.T) {
	t.Parallel()

	m := testRunControlModel("http://127.0.0.1:1")
	m.conversationID = "conv-undo"
	m.appendConversationMessages([]ConversationMessage{{Role: "user", Content: "still-here-prompt"}})
	transcriptBefore := len(m.transcript)

	m2, _ := m.Update(UndoResultMsg{
		Conflict: true,
		Err:      "undo crosses compaction boundary: target prompt at step 0 is at or below compaction summary at step 2",
	})
	m = m2.(Model)

	out := m.vp.View()
	if !strings.Contains(out, "compaction") {
		t.Errorf("viewport does not show the compaction-boundary explanation:\n%s", out)
	}
	if !strings.Contains(out, "still-here-prompt") {
		t.Errorf("viewport lost existing content on a refused undo:\n%s", out)
	}
	if len(m.transcript) != transcriptBefore {
		t.Errorf("transcript mutated by a refused undo: got %d entries, want %d", len(m.transcript), transcriptBefore)
	}
}

// TestUndoResultMsg_ErrorKeepsView verifies a generic failure lands in the
// status bar without touching the viewport or transcript.
func TestUndoResultMsg_ErrorKeepsView(t *testing.T) {
	t.Parallel()

	m := testRunControlModel("http://127.0.0.1:1")
	m.conversationID = "conv-undo"
	m.appendConversationMessages([]ConversationMessage{{Role: "user", Content: "still-here-prompt"}})
	transcriptBefore := len(m.transcript)
	viewBefore := m.vp.View()

	m2, _ := m.Update(UndoResultMsg{Err: "HTTP 500: boom"})
	m = m2.(Model)

	if got := m.vp.View(); got != viewBefore {
		t.Errorf("viewport changed on a failed undo:\nbefore:\n%s\nafter:\n%s", viewBefore, got)
	}
	if len(m.transcript) != transcriptBefore {
		t.Errorf("transcript mutated by a failed undo")
	}
	if !strings.Contains(m.statusMsg, "Undo failed") {
		t.Errorf("expected an undo failure status, got %q", m.statusMsg)
	}
}

// TestUndoCommandRegistered verifies /undo resolves through the registry and
// dispatches like every other built-in.
func TestUndoCommandRegistered(t *testing.T) {
	t.Parallel()

	r := NewCommandRegistry()
	entry, ok := r.Lookup("undo")
	if !ok {
		t.Fatal("built-in command \"undo\" not registered")
	}
	if entry.Description == "" {
		t.Error("undo command has empty description")
	}
	cmd, ok := ParseCommand("/undo 2")
	if !ok {
		t.Fatal("ParseCommand(\"/undo 2\") failed")
	}
	if cmd.Name != "undo" || len(cmd.Args) != 1 || cmd.Args[0] != "2" {
		t.Fatalf("parsed command = %+v", cmd)
	}
	if result := entry.Handler(cmd); result.Status != CmdOK {
		t.Errorf("undo handler: expected CmdOK, got %v", result.Status)
	}
}
