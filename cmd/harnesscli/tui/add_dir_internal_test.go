package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStartRunCmdIncludesExtraDirs verifies extra directories added via
// /add-dir are marshaled onto the run request as extra_dirs.
func TestStartRunCmdIncludesExtraDirs(t *testing.T) {
	t.Parallel()

	var got runCreateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(runCreateResponse{RunID: "run-extra-dirs"}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "low", "default", "/tmp/ws", "", nil, []string{"/tmp/shared-libs"})()
	if _, ok := msg.(RunStartedMsg); !ok {
		t.Fatalf("expected RunStartedMsg, got %T: %+v", msg, msg)
	}
	if len(got.ExtraDirs) != 1 || got.ExtraDirs[0] != "/tmp/shared-libs" {
		t.Fatalf("extra_dirs = %v, want [/tmp/shared-libs]", got.ExtraDirs)
	}
}

// TestStartRunCmdOmitsExtraDirsWhenEmpty verifies extra_dirs is omitted (not
// sent as an empty array) when no directories were added.
func TestStartRunCmdOmitsExtraDirsWhenEmpty(t *testing.T) {
	t.Parallel()

	var rawBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runCreateResponse{RunID: "run-no-extra-dirs"})
	}))
	defer ts.Close()

	msg := startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "", "default", "/tmp/ws", "", nil, nil)()
	if _, ok := msg.(RunStartedMsg); !ok {
		t.Fatalf("expected RunStartedMsg, got %T: %+v", msg, msg)
	}
	if _, present := rawBody["extra_dirs"]; present {
		t.Fatalf("extra_dirs must be omitted when empty, body: %v", rawBody)
	}
}
