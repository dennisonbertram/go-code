package tools

// Direct unit tests of GuardedWebFetcher (GAP-2 implementation). These mirror
// the required proving-test scenarios: 169.254.169.254 and 127.0.0.1 are
// refused, a DNS name resolving to a private IP is refused AT DIAL TIME, a
// permitted (allowlisted) destination still works, and Search is passed
// through unchanged (it has no agent-supplied destination).

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGuardedWebFetcher_Fetch_RejectsLoopbackLiteral proves Fetch refuses a
// literal 127.0.0.1 destination by default, and does so via the guard (not
// via network unreachability) — the rejection is near-instant because
// sandboxedDialerControl's Control hook fires before any connect() syscall.
func TestGuardedWebFetcher_Fetch_RejectsLoopbackLiteral(t *testing.T) {
	fetcher := NewGuardedWebFetcher(nil, nil)

	start := time.Now()
	_, err := fetcher.Fetch(context.Background(), "http://127.0.0.1:1/")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected loopback literal destination to be rejected by default")
	}
	if !strings.Contains(err.Error(), "ssrf-guard") {
		t.Fatalf("expected the ssrf guard to be what rejected the request, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected the guard to reject before any real dial attempt (near-instant), took %s: %v", elapsed, err)
	}
}

// TestGuardedWebFetcher_Fetch_RejectsCloudMetadataLiteral proves Fetch
// refuses the AWS/GCP/Azure link-local metadata address 169.254.169.254, the
// highest-value SSRF target (credential theft), and that the guard (not
// network unreachability) is what blocks it.
func TestGuardedWebFetcher_Fetch_RejectsCloudMetadataLiteral(t *testing.T) {
	fetcher := NewGuardedWebFetcher(nil, nil)

	start := time.Now()
	_, err := fetcher.Fetch(context.Background(), "http://169.254.169.254/latest/meta-data/")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the cloud metadata destination to be rejected by default")
	}
	if !strings.Contains(err.Error(), "ssrf-guard") {
		t.Fatalf("expected the ssrf guard to be what rejected the request, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected the guard to reject before any real dial attempt (near-instant), took %s: %v", elapsed, err)
	}
}

// TestGuardedWebFetcher_Fetch_RejectsDNSNameResolvingToPrivateIP_AtDialTime
// proves the check runs at actual dial time (post-resolution), not just on
// the literal hostname string: "localhost" resolves via the normal system
// resolver, and must still be blocked because the resolved address is
// loopback.
func TestGuardedWebFetcher_Fetch_RejectsDNSNameResolvingToPrivateIP_AtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	if err != nil {
		t.Fatalf("parse httptest server port: %v", err)
	}

	fetcher := NewGuardedWebFetcher(nil, nil)
	_, err = fetcher.Fetch(context.Background(), "http://localhost:"+port)
	if err == nil {
		t.Fatal("expected a hostname resolving to loopback to be refused at dial time")
	}
}

// TestGuardedWebFetcher_Fetch_AllowlistPermitsExplicitHost proves the opt-in
// escape hatch: adding the httptest server's host to the allowlist permits
// the exact same request that would otherwise be blocked, and the actual
// content is returned — proving Fetch performs a real request when allowed
// (this stands in for "a public destination still works", since a real
// public IP is not reachable in this offline test environment; public-IP
// literal permission is unit-tested directly against the underlying dial
// Control in ssrf_guard_test.go, which GuardedWebFetcher reuses unchanged).
func TestGuardedWebFetcher_Fetch_AllowlistPermitsExplicitHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from allowlisted host"))
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	if err != nil {
		t.Fatalf("parse httptest server host: %v", err)
	}

	fetcher := NewGuardedWebFetcher(nil, []string{host})
	content, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected explicitly allowlisted host to be permitted, got: %v", err)
	}
	if content != "hello from allowlisted host" {
		t.Fatalf("unexpected content %q", content)
	}
}

// TestGuardedWebFetcher_Search_DelegatesToBaseUnchanged proves Search is
// passed through to the wrapped base implementation unchanged — it has no
// agent-supplied destination host, so it is outside Fetch's SSRF threat
// model.
func TestGuardedWebFetcher_Search_DelegatesToBaseUnchanged(t *testing.T) {
	base := &fakeWeb{}
	fetcher := NewGuardedWebFetcher(base, nil)

	results, err := fetcher.Search(context.Background(), "golang", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0]["query"] != "golang" {
		t.Fatalf("expected Search to delegate to base unchanged, got: %+v", results)
	}
}

// TestGuardedWebFetcher_Search_NoBaseConfigured_ReturnsError proves Search
// fails clearly (rather than silently returning nothing) when no base
// WebFetcher was supplied.
func TestGuardedWebFetcher_Search_NoBaseConfigured_ReturnsError(t *testing.T) {
	fetcher := NewGuardedWebFetcher(nil, nil)
	if _, err := fetcher.Search(context.Background(), "golang", 3); err == nil {
		t.Fatal("expected an error when no base WebFetcher is configured for Search")
	}
}

// TestRegression_GuardedWebFetcher_NoBase_RedirectToBlockedDestination_Refused
// proves the no-base Fetch path (fetchDirect) retains the fetch/download
// tools' redirect safety: a request to an allowlisted origin that redirects
// to a non-allowlisted destination must still fail, because both hops share
// the same guarded Transport (NewGuardedHTTPClient), and the dial-time
// Control check runs again for the redirect target. If a future change
// swapped fetchDirect's client for a plain (unguarded) one, or built a fresh
// http.Client per-request instead of reusing the guarded one, this is what
// would catch it — mirroring
// TestSSRFGuard_GuardedClient_RedirectToBlockedDestination_Refused for the
// underlying NewGuardedHTTPClient in ssrf_guard_test.go.
func TestRegression_GuardedWebFetcher_NoBase_RedirectToBlockedDestination_Refused(t *testing.T) {
	blocked := newLoopbackServerOn(t, "127.0.0.1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should never be reached"))
	})
	defer blocked.Close()

	allowed := newLoopbackServerOn(t, "::1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blocked.URL, http.StatusFound)
	})
	defer allowed.Close()

	// Only the redirect ORIGIN address (::1) is allowlisted — the redirect
	// target (127.0.0.1) is not.
	fetcher := NewGuardedWebFetcher(nil, []string{"::1"})
	_, err := fetcher.Fetch(context.Background(), allowed.URL)
	if err == nil {
		t.Fatal("expected redirect to a non-allowlisted blocked destination to fail")
	}
}
