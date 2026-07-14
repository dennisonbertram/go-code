package openai

import (
	"testing"
)

// TestStreamAPIError_Error verifies the Error() string format for a
// mid-stream SSE error sentinel.
func TestStreamAPIError_Error(t *testing.T) {
	err := &streamAPIError{
		Message:    "rate limit exceeded",
		StatusCode: 429,
		Raw:        "raw-body",
	}

	got := err.Error()
	want := "stream error: rate limit exceeded"
	if got != want {
		t.Errorf("streamAPIError.Error() = %q, want %q", got, want)
	}
}
