package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchModelsCmd_DecodesModalities verifies the TUI's /v1/models client
// keeps the modalities array the server returns (epic #818 slice 2 needs it
// for the image-paste pre-flight gate).
func TestFetchModelsCmd_DecodesModalities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"id":"gpt-4.1","provider":"openai","aliases":[],"input_cost_per_mtok":0,"output_cost_per_mtok":0,"modalities":["text","image"]},
			{"id":"claude-sonnet-4-6","provider":"anthropic","aliases":[],"input_cost_per_mtok":0,"output_cost_per_mtok":0,"modalities":["text"]}
		]}`))
	}))
	defer srv.Close()

	msg := fetchModelsCmd(srv.URL, "")()
	fetched, ok := msg.(ModelsFetchedMsg)
	if !ok {
		t.Fatalf("fetchModelsCmd must yield ModelsFetchedMsg, got %T", msg)
	}
	if len(fetched.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(fetched.Models))
	}
	byID := map[string][]string{}
	for _, e := range fetched.Models {
		byID[e.ID] = e.Modalities
	}
	if got := byID["gpt-4.1"]; len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Errorf("gpt-4.1 modalities = %v, want [text image]", got)
	}
	if got := byID["claude-sonnet-4-6"]; len(got) != 1 || got[0] != "text" {
		t.Errorf("claude-sonnet-4-6 modalities = %v, want [text]", got)
	}
}
