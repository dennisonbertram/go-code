package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchRewindPointsCmd_Success verifies fetchRewindPointsCmd hits
// GET /v1/conversations/{id}/rewind-points and decodes the point list.
func TestFetchRewindPointsCmd_Success(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"points":[{"id":"p1","step":2,"tool":"write"}]}`))
	}))
	defer ts.Close()

	msg := fetchRewindPointsCmd(ts.URL, "conv-1", "")()

	if gotMethod != http.MethodGet {
		t.Errorf("method: want GET, got %q", gotMethod)
	}
	if gotPath != "/v1/conversations/conv-1/rewind-points" {
		t.Errorf("path: want /v1/conversations/conv-1/rewind-points, got %q", gotPath)
	}

	got, ok := msg.(RewindPointsLoadedMsg)
	if !ok {
		t.Fatalf("expected RewindPointsLoadedMsg, got %T", msg)
	}
	if len(got.Points) != 1 || got.Points[0].ID != "p1" || got.Points[0].Tool != "write" {
		t.Fatalf("Points = %+v", got.Points)
	}
}

// TestFetchRewindPointsCmd_ErrorStatus verifies a non-200 response yields
// RewindResultMsg.Err instead of a decode panic.
func TestFetchRewindPointsCmd_ErrorStatus(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	msg := fetchRewindPointsCmd(ts.URL, "conv-missing", "")()
	got, ok := msg.(RewindResultMsg)
	if !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestFetchRewindPointsCmd_NetworkError verifies an unreachable server yields
// RewindResultMsg.Err.
func TestFetchRewindPointsCmd_NetworkError(t *testing.T) {
	t.Parallel()

	msg := fetchRewindPointsCmd("http://127.0.0.1:1", "conv-err", "")()
	got, ok := msg.(RewindResultMsg)
	if !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestRestoreRewindCmd_Success verifies restoreRewindCmd POSTs the point id
// to /v1/conversations/{id}/rewind and decodes the restore result.
func TestRestoreRewindCmd_Success(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"FilesRestored":2,"MessagesTruncated":3}`))
	}))
	defer ts.Close()

	msg := restoreRewindCmd(ts.URL, "conv-2", "point-9", "")()

	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %q", gotMethod)
	}
	if gotPath != "/v1/conversations/conv-2/rewind" {
		t.Errorf("path: want /v1/conversations/conv-2/rewind, got %q", gotPath)
	}
	if !strings.Contains(gotBody, `"point_id":"point-9"`) {
		t.Errorf("body missing point_id: %q", gotBody)
	}

	got, ok := msg.(RewindResultMsg)
	if !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
	if got.FilesRestored != 2 || got.MessagesTruncated != 3 {
		t.Fatalf("result = %+v", got)
	}
}

// TestRestoreRewindCmd_ErrorStatus verifies a non-200 response (e.g. the
// server refusing an externally-modified file) surfaces as RewindResultMsg.Err.
func TestRestoreRewindCmd_ErrorStatus(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rewind refused", http.StatusConflict)
	}))
	defer ts.Close()

	msg := restoreRewindCmd(ts.URL, "conv-3", "point-1", "")()
	got, ok := msg.(RewindResultMsg)
	if !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestRestoreRewindCmd_NetworkError verifies an unreachable server yields
// RewindResultMsg.Err rather than a panic.
func TestRestoreRewindCmd_NetworkError(t *testing.T) {
	t.Parallel()

	msg := restoreRewindCmd("http://127.0.0.1:1", "conv-4", "point-1", "")()
	got, ok := msg.(RewindResultMsg)
	if !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestExecuteRewindCommand_NoArgsFetchesPoints verifies /rewind with no
// arguments lists points rather than restoring anything.
func TestExecuteRewindCommand_NoArgsFetchesPoints(t *testing.T) {
	t.Parallel()

	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"points":[]}`))
	}))
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	cmds, quit := executeRewindCommand(&m, Command{Name: "rewind"})
	if quit {
		t.Fatal("/rewind must not quit")
	}
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("expected exactly one command, got %d", len(cmds))
	}
	if _, ok := cmds[0]().(RewindPointsLoadedMsg); !ok {
		t.Fatalf("expected RewindPointsLoadedMsg from the returned command")
	}
	wantPath := "/v1/conversations/" + m.conversationID + "/rewind-points"
	if gotPath != wantPath {
		t.Errorf("path: want %q, got %q", wantPath, gotPath)
	}
}

// TestExecuteRewindCommand_RequiresConfirmToken verifies that a point id
// without the literal "confirm" token never issues a restore request.
func TestExecuteRewindCommand_RequiresConfirmToken(t *testing.T) {
	t.Parallel()

	requested := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	cmds, quit := executeRewindCommand(&m, Command{Name: "rewind", Args: []string{"point-1"}})
	if quit {
		t.Fatal("/rewind must not quit")
	}
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("expected exactly one status command, got %d", len(cmds))
	}
	cmds[0]()
	if requested {
		t.Fatal("executeRewindCommand issued an HTTP request without the confirm token")
	}
}

// TestExecuteRewindCommand_ConfirmRestores verifies /rewind <point-id> confirm
// issues the destructive restore request.
func TestExecuteRewindCommand_ConfirmRestores(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"FilesRestored":1,"MessagesTruncated":1}`))
	}))
	defer ts.Close()

	m := testRunControlModel(ts.URL)
	cmds, quit := executeRewindCommand(&m, Command{Name: "rewind", Args: []string{"point-1", "confirm"}})
	if quit {
		t.Fatal("/rewind must not quit")
	}
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatalf("expected exactly one command, got %d", len(cmds))
	}
	msg := cmds[0]()
	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %q", gotMethod)
	}
	wantPath := "/v1/conversations/" + m.conversationID + "/rewind"
	if gotPath != wantPath {
		t.Errorf("path: want %q, got %q", wantPath, gotPath)
	}
	if _, ok := msg.(RewindResultMsg); !ok {
		t.Fatalf("expected RewindResultMsg, got %T", msg)
	}
}
