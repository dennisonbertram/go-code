package anthropic

import (
	"testing"
)

// TestStreamAPIError_Error verifies the Error() string format for a
// mid-stream SSE error sentinel.
func TestStreamAPIError_Error(t *testing.T) {
	err := &streamAPIError{
		Message:    "overloaded",
		StatusCode: 503,
		Raw:        "raw-body",
	}

	got := err.Error()
	want := "stream error: overloaded"
	if got != want {
		t.Errorf("streamAPIError.Error() = %q, want %q", got, want)
	}
}
