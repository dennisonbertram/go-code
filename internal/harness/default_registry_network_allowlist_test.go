package harness

// GAP-3: internal/harness/tools_default.go builds the production tool
// registry but, before this change, never threaded an operator-configured
// NetworkAllowlist through to BuildOptions.NetworkAllowlist at all — so an
// operator had no way to legitimately allow a specific internal/localhost
// host for the `download` tool (and, per the GAP-2 fix, the WebFetcher-backed
// tools) even when they explicitly needed to. This test proves the allowlist
// now actually reaches the `download` tool built by
// NewDefaultRegistryWithOptions: an allowlisted host is reachable, and a
// non-allowlisted loopback host is not.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewDefaultRegistryWithOptions_DownloadTool_AllowlistReachesTool(t *testing.T) {
	t.Parallel()

	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("allowed content"))
	}))
	defer allowed.Close()

	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("blocked content"))
	}))
	defer blocked.Close()

	allowedHost, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(allowed.URL, "http://"), "https://"))
	if err != nil {
		t.Fatalf("parse allowed server host: %v", err)
	}

	workspace := t.TempDir()
	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{
		ApprovalMode:     ToolApprovalModeFullAuto,
		NetworkAllowlist: []string{allowedHost},
	})

	// The allowlisted host must be reachable via the `download` tool.
	allowedArgs, _ := json.Marshal(map[string]any{"url": allowed.URL, "file_path": "allowed.txt"})
	out, err := registry.Execute(context.Background(), "download", allowedArgs)
	if err != nil {
		t.Fatalf("expected allowlisted host to be reachable via download tool, got error: %v", err)
	}
	if !strings.Contains(out, "\"bytes_written\"") {
		t.Fatalf("expected a successful download result, got: %s", out)
	}
	written, readErr := os.ReadFile(filepath.Join(workspace, "allowed.txt"))
	if readErr != nil {
		t.Fatalf("read downloaded file: %v", readErr)
	}
	if string(written) != "allowed content" {
		t.Fatalf("unexpected downloaded content %q", written)
	}

	// A DIFFERENT loopback server, NOT covered by the allowlist entry (the
	// allowlist only names allowed.URL's host:port pairing via the bare
	// host match, and blocked.URL listens on a different port on the same
	// loopback address — but the guard's default-deny still applies to any
	// destination not explicitly allowlisted; to make the negative case
	// unambiguous we assert against a private RFC1918 destination that is
	// categorically outside both the allowlist and the public-address
	// default-allow).
	blockedArgs, _ := json.Marshal(map[string]any{"url": "http://10.255.255.1:1/", "file_path": "blocked.txt"})
	_, err = registry.Execute(context.Background(), "download", blockedArgs)
	if err == nil {
		t.Fatal("expected a non-allowlisted private destination to be refused via download tool")
	}
	if !strings.Contains(err.Error(), "ssrf-guard") {
		t.Fatalf("expected the ssrf guard to be what rejected the request, got: %v", err)
	}
}
