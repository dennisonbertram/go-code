package tools

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests are written as ATTACKS against the outbound fetch/download
// tools' network guard (BUG 2, P2): by default, dialing loopback,
// link-local (incl. 169.254.169.254 cloud metadata), and RFC1918/ULA private
// destinations must be refused — including when a hostname only resolves to
// such an address AT DIAL TIME (DNS-rebinding safety), and including via
// HTTP redirects. The explicit opt-in allowlist is the only way through.

func TestSSRFGuard_DialControl_RejectsLoopback(t *testing.T) {
	control := sandboxedDialerControl(nil)
	if err := control("tcp", "127.0.0.1:80", nil); err == nil {
		t.Fatal("expected loopback destination to be rejected by default")
	}
}

func TestSSRFGuard_DialControl_RejectsCloudMetadataLinkLocal(t *testing.T) {
	control := sandboxedDialerControl(nil)
	if err := control("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("expected link-local cloud metadata address to be rejected by default")
	}
}

func TestSSRFGuard_DialControl_RejectsRFC1918Private(t *testing.T) {
	control := sandboxedDialerControl(nil)
	if err := control("tcp", "10.1.2.3:443", nil); err == nil {
		t.Fatal("expected RFC1918 private destination to be rejected by default")
	}
}

func TestSSRFGuard_DialControl_RejectsIPv6LinkLocal(t *testing.T) {
	control := sandboxedDialerControl(nil)
	if err := control("tcp", "[fe80::1]:80", nil); err == nil {
		t.Fatal("expected IPv6 link-local destination to be rejected by default")
	}
}

func TestSSRFGuard_DialControl_RejectsIPv6UniqueLocal(t *testing.T) {
	control := sandboxedDialerControl(nil)
	if err := control("tcp", "[fd00::1]:80", nil); err == nil {
		t.Fatal("expected IPv6 unique-local (fc00::/7) destination to be rejected by default")
	}
}

func TestSSRFGuard_DialControl_AllowsPublicAddress(t *testing.T) {
	control := sandboxedDialerControl(nil)
	// 93.184.216.34 (example.com) is a public IP literal — no real network
	// access happens in this test since Control only inspects the address
	// string; it never dials.
	if err := control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("expected public destination to be allowed by default, got: %v", err)
	}
}

func TestSSRFGuard_DialControl_CIDRAllowlistPermitsOtherwiseBlockedIP(t *testing.T) {
	control := sandboxedDialerControl([]string{"10.0.0.0/8"})
	if err := control("tcp", "10.5.5.5:80", nil); err != nil {
		t.Fatalf("expected 10.5.5.5 to be permitted by the 10.0.0.0/8 allowlist entry, got: %v", err)
	}
	// A private address NOT covered by the allowlist entry must still be blocked.
	if err := control("tcp", "192.168.1.1:80", nil); err == nil {
		t.Fatal("expected 192.168.1.1 (not covered by allowlist) to still be rejected")
	}
}

// TestSSRFGuard_GuardedClient_BlocksLoopbackByDefault proves the guard is
// wired into the actual http.Client used by the fetch/download tools: an
// httptest server is, by construction, listening on loopback — dialing it
// through the guarded client must fail unless explicitly allowlisted.
func TestSSRFGuard_GuardedClient_BlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	client := NewGuardedHTTPClient(&http.Client{Timeout: 5 * time.Second}, nil)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected request to loopback httptest server to be blocked by default")
	}
}

// TestSSRFGuard_GuardedClient_AllowlistPermitsExplicitHost proves the opt-in
// escape hatch: adding the httptest server's host to the allowlist permits
// the exact same request that was blocked above.
func TestSSRFGuard_GuardedClient_AllowlistPermitsExplicitHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	host, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	if err != nil {
		t.Fatalf("parse httptest server host: %v", err)
	}

	client := NewGuardedHTTPClient(&http.Client{Timeout: 5 * time.Second}, []string{host})
	res, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected explicitly allowlisted host to be permitted, got: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "hello" {
		t.Fatalf("unexpected body %q", body)
	}
}

// TestSSRFGuard_GuardedClient_DNSNameResolvingToLoopback_RefusedAtDialTime
// proves the check runs at actual dial time (post-resolution), not just on
// the literal hostname string: "localhost" is a real DNS/hosts name that
// resolves via the normal system resolver, and must still be blocked because
// the resolved address is loopback.
func TestSSRFGuard_GuardedClient_DNSNameResolvingToLoopback_RefusedAtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	if err != nil {
		t.Fatalf("parse httptest server port: %v", err)
	}

	client := NewGuardedHTTPClient(&http.Client{Timeout: 5 * time.Second}, nil)
	_, err = client.Get("http://localhost:" + port)
	if err == nil {
		t.Fatal("expected a hostname resolving to loopback to be refused at dial time")
	}
}

// newLoopbackServerOn starts an httptest server bound to a specific loopback
// address (rather than httptest's default, always-127.0.0.1 listener) so two
// servers in the same test can be distinguished by IP for allowlist purposes.
// Both "127.0.0.1" and "::1" are loopback addresses active by default on
// every supported platform, so this needs no special host configuration.
func newLoopbackServerOn(t *testing.T, ip string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ip, err)
	}
	srv := httptest.NewUnstartedServer(handler)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	return srv
}

// TestSSRFGuard_GuardedClient_RedirectToBlockedDestination_Refused proves
// that a request to an allowlisted ("public-standing-in") host that then
// redirects to a non-allowlisted ("private-standing-in") host is blocked —
// because both hops go through the same guarded Transport, and the dial-time
// Control check runs again for the redirect target. The two servers are
// bound to distinct loopback addresses (::1 vs 127.0.0.1) so the allowlist,
// which matches by address, can actually distinguish "allowed origin" from
// "blocked redirect target" — using httptest's default listener for both
// would put them on the identical IP and make the test meaningless.
func TestSSRFGuard_GuardedClient_RedirectToBlockedDestination_Refused(t *testing.T) {
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
	client := NewGuardedHTTPClient(&http.Client{Timeout: 5 * time.Second}, []string{"::1"})
	_, err := client.Get(allowed.URL)
	if err == nil {
		t.Fatal("expected redirect to a non-allowlisted blocked destination to fail")
	}
}

func TestSSRFGuard_ContextCancellation_DoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	client := NewGuardedHTTPClient(&http.Client{}, nil)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/", nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected blocked loopback request to fail")
	}
}
