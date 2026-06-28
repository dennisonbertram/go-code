// Package fakeprovider provides a reusable fake implementation of
// harness.Provider for use in tests. It supports scripted turns,
// streaming, delays, hang/release, and thread-safe invocation recording.
package fakeprovider

import (
	"errors"
	"fmt"

	"go-agent-harness/internal/harness"
)

// retryableStatusCodes is the set of HTTP status codes that are considered
// retryable / fallback-eligible by the harness.
var retryableStatusCodes = map[int]bool{
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

// RetryableError wraps an existing error so that IsRetryable returns true.
// If err is already a *harness.ProviderHTTPError with a retryable status code
// this is a no-op wrapper; if not, it is wrapped in a synthetic
// *harness.ProviderHTTPError with status 500 so IsRetryable recognises it.
func RetryableError(err error) error {
	var phe *harness.ProviderHTTPError
	if errors.As(err, &phe) && retryableStatusCodes[phe.StatusCode] {
		// Already retryable — return as-is.
		return err
	}
	// Wrap in a 500 ProviderHTTPError so IsRetryable can detect it.
	return &harness.ProviderHTTPError{
		Provider:   "fake",
		StatusCode: 500,
		Body:       err.Error(),
	}
}

// RateLimitError returns a *harness.ProviderHTTPError with status 429
// and the given message as the body.  IsRetryable returns true for this error.
func RateLimitError(msg string) error {
	return &harness.ProviderHTTPError{
		Provider:   "fake",
		StatusCode: 429,
		Body:       msg,
	}
}

// GenericError returns a plain non-retryable error (a simple fmt.Errorf).
// IsRetryable returns false for this error.
func GenericError(msg string) error {
	return fmt.Errorf("%s", msg)
}

// IsRetryable returns true when err is (or wraps) a *harness.ProviderHTTPError
// whose StatusCode is one of the fallback-eligible codes: 429, 500, 502, 503, 504.
func IsRetryable(err error) bool {
	var phe *harness.ProviderHTTPError
	if errors.As(err, &phe) {
		return retryableStatusCodes[phe.StatusCode]
	}
	return false
}
