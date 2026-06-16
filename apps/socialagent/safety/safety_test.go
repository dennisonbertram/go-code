package safety_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/apps/socialagent/safety"
)

// TestLlamaGuardScreener_Safe verifies that messages classified as safe pass
// through the screener and are not blocked.
func TestLlamaGuardScreener_Safe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and content type.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["text"] != "Hello, how are you?" {
			t.Errorf("expected text 'Hello, how are you?', got %q", body["text"])
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"safe":     true,
			"category": "",
			"reason":   "",
		})
	}))
	defer server.Close()

	screener := safety.NewLlamaGuardScreener(server.URL)
	result, err := screener.Screen(context.Background(), "Hello, how are you?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Safe {
		t.Errorf("expected Safe=true, got Safe=%v", result.Safe)
	}
	if result.Category != "" {
		t.Errorf("expected empty category, got %q", result.Category)
	}
}

// TestLlamaGuardScreener_Unsafe verifies that messages classified as unsafe
// are flagged with the appropriate category and reason.
func TestLlamaGuardScreener_Unsafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"safe":     false,
			"category": "S3",
			"reason":   "Hate speech detected",
		})
	}))
	defer server.Close()

	screener := safety.NewLlamaGuardScreener(server.URL)
	result, err := screener.Screen(context.Background(), "bad content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Safe {
		t.Errorf("expected Safe=false, got Safe=%v", result.Safe)
	}
	if result.Category != "S3" {
		t.Errorf("expected Category='S3', got %q", result.Category)
	}
	if result.Reason != "Hate speech detected" {
		t.Errorf("expected Reason='Hate speech detected', got %q", result.Reason)
	}
}

// TestLlamaGuardScreener_ServerError verifies fail-open behavior: when the
// screener endpoint returns an error (non-200 status), the message is treated
// as safe so the gateway remains operational.
func TestLlamaGuardScreener_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	screener := safety.NewLlamaGuardScreener(server.URL)
	result, err := screener.Screen(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Safe {
		t.Errorf("expected fail-open Safe=true on server error, got Safe=%v", result.Safe)
	}
}

// TestLlamaGuardScreener_NetworkError verifies fail-open behavior: when the
// screener endpoint is unreachable (connection refused), the message is treated
// as safe.
func TestLlamaGuardScreener_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"safe": true})
	}))
	// Close the server immediately so the next request fails to connect.
	server.Close()

	screener := safety.NewLlamaGuardScreener(server.URL)
	result, err := screener.Screen(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error (should fail open): %v", err)
	}
	if !result.Safe {
		t.Errorf("expected fail-open Safe=true on connection refused, got Safe=%v", result.Safe)
	}
}

// TestLlamaGuardScreener_InvalidJSON verifies fail-open behavior: when the
// screener returns invalid JSON, the message is treated as safe.
func TestLlamaGuardScreener_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	screener := safety.NewLlamaGuardScreener(server.URL)
	result, err := screener.Screen(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error (should fail open): %v", err)
	}
	if !result.Safe {
		t.Errorf("expected fail-open Safe=true on invalid JSON, got Safe=%v", result.Safe)
	}
}

// TestParseCategory verifies that ParseCategory returns appropriate refusal
// messages for various result states.
func TestParseCategory(t *testing.T) {
	tests := []struct {
		name string
		r    *safety.Result
		want string
	}{
		{
			name: "nil result",
			r:    nil,
			want: "",
		},
		{
			name: "safe result",
			r:    &safety.Result{Safe: true},
			want: "",
		},
		{
			name: "unsafe with reason",
			r:    &safety.Result{Safe: false, Reason: "Hate speech detected"},
			want: "I'm not able to help with that request. (Hate speech detected)",
		},
		{
			name: "unsafe with category only",
			r:    &safety.Result{Safe: false, Category: "S3"},
			want: "I'm not able to help with that request. (category: S3)",
		},
		{
			name: "unsafe with both",
			r:    &safety.Result{Safe: false, Category: "S3", Reason: "Hate speech"},
			want: "I'm not able to help with that request. (Hate speech)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safety.ParseCategory(tt.r)
			if got != tt.want {
				t.Errorf("ParseCategory() = %q, want %q", got, tt.want)
			}
		})
	}
}
