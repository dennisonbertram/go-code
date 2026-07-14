package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─── BT-009: fetchSessionRunsCmd success ─────────────────────────────────────

// TestFetchSessionRunsCmd_Success verifies that when the server returns 200 with
// a valid JSON payload, fetchSessionRunsCmd emits a SessionRunsFetchedMsg with
// the correct conversation ID and run IDs.
func TestFetchSessionRunsCmd_Success(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/conversations/conv-abc/runs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"runs": []map[string]any{
				{"run_id": "run-001"},
				{"run_id": "run-002"},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := fetchSessionRunsCmd(ts.URL, "conv-abc", "")()
	got, ok := msg.(SessionRunsFetchedMsg)
	if !ok {
		t.Fatalf("expected SessionRunsFetchedMsg, got %T", msg)
	}
	if got.ConversationID != "conv-abc" {
		t.Errorf("ConversationID: want %q, got %q", "conv-abc", got.ConversationID)
	}
	if len(got.RunIDs) != 2 {
		t.Fatalf("RunIDs length: want 2, got %d", len(got.RunIDs))
	}
	if got.RunIDs[0] != "run-001" {
		t.Errorf("RunIDs[0]: want %q, got %q", "run-001", got.RunIDs[0])
	}
	if got.RunIDs[1] != "run-002" {
		t.Errorf("RunIDs[1]: want %q, got %q", "run-002", got.RunIDs[1])
	}
}

// ─── BT-010: fetchSessionRunsCmd on 501 returns empty msg ────────────────────

// TestFetchSessionRunsCmd_501ReturnsEmptyMsg verifies that a 501 Not Implemented
// response causes fetchSessionRunsCmd to emit a zero SessionRunsFetchedMsg
// (empty RunIDs) so callers handle the graceful-empty case.
func TestFetchSessionRunsCmd_501ReturnsEmptyMsg(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer ts.Close()

	msg := fetchSessionRunsCmd(ts.URL, "conv-xyz", "")()
	got, ok := msg.(SessionRunsFetchedMsg)
	if !ok {
		t.Fatalf("expected SessionRunsFetchedMsg, got %T", msg)
	}
	if got.ConversationID != "" {
		t.Errorf("ConversationID on 501: want empty, got %q", got.ConversationID)
	}
	if len(got.RunIDs) != 0 {
		t.Errorf("RunIDs on 501: want empty, got %v", got.RunIDs)
	}
}

// ─── BT-011: fetchSessionRunsCmd on network error returns empty msg ──────────

// TestFetchSessionRunsCmd_NetworkErrorReturnsEmptyMsg verifies that a network
// error (unreachable server) causes fetchSessionRunsCmd to emit a zero
// SessionRunsFetchedMsg rather than panicking or returning a non-msg type.
func TestFetchSessionRunsCmd_NetworkErrorReturnsEmptyMsg(t *testing.T) {
	t.Parallel()

	// Use an address that will immediately refuse/error.
	msg := fetchSessionRunsCmd("http://127.0.0.1:1", "conv-err", "")()
	got, ok := msg.(SessionRunsFetchedMsg)
	if !ok {
		t.Fatalf("expected SessionRunsFetchedMsg, got %T", msg)
	}
	if len(got.RunIDs) != 0 {
		t.Errorf("RunIDs on network error: want empty, got %v", got.RunIDs)
	}
}

// ─── BT-012: fetchSessionRunsCmd on malformed JSON returns empty msg ──────────

// TestFetchSessionRunsCmd_MalformedJSONReturnsEmptyMsg verifies that an invalid
// JSON body causes fetchSessionRunsCmd to emit a zero SessionRunsFetchedMsg
// rather than propagating a decode error.
func TestFetchSessionRunsCmd_MalformedJSONReturnsEmptyMsg(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-valid-json"))
	}))
	defer ts.Close()

	msg := fetchSessionRunsCmd(ts.URL, "conv-json-err", "")()
	got, ok := msg.(SessionRunsFetchedMsg)
	if !ok {
		t.Fatalf("expected SessionRunsFetchedMsg, got %T", msg)
	}
	if len(got.RunIDs) != 0 {
		t.Errorf("RunIDs on malformed JSON: want empty, got %v", got.RunIDs)
	}
}

// ─── Regression: fetchSessionRunsCmd trailing slash normalised ───────────────

// TestFetchSessionRunsCmd_TrailingSlashNormalised verifies that a base URL with
// a trailing slash still constructs the correct path, so the function is
// insensitive to how the user configures the base URL.
func TestFetchSessionRunsCmd_TrailingSlashNormalised(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path must be exactly /v1/conversations/conv-slash/runs (no double slash).
		if r.URL.Path != "/v1/conversations/conv-slash/runs" {
			t.Errorf("path with trailing base slash: want /v1/conversations/conv-slash/runs, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": []map[string]any{{"run_id": "r1"}}})
	}))
	defer ts.Close()

	msg := fetchSessionRunsCmd(ts.URL+"/", "conv-slash", "")()
	got, ok := msg.(SessionRunsFetchedMsg)
	if !ok {
		t.Fatalf("expected SessionRunsFetchedMsg, got %T", msg)
	}
	if len(got.RunIDs) != 1 || got.RunIDs[0] != "r1" {
		t.Errorf("unexpected RunIDs with trailing-slash base URL: %v", got.RunIDs)
	}
}
