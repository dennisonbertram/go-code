package tools

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"time"
)

// TODO(BUG-2 red phase): stub only — allows every destination. The real
// implementation blocks non-public IPs at dial time unless allowlisted.
func sandboxedDialerControl(allowlist []string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		return nil
	}
}

// TODO(BUG-2 red phase): stub only — returns base unmodified (today's
// vulnerable behavior: no SSRF protection at all).
func NewGuardedHTTPClient(base *http.Client, allowlist []string) *http.Client {
	if base == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return base
}

var _ = context.Background
var _ = net.SplitHostPort
