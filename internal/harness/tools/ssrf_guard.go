package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// sandboxedDialerControl returns a net.Dialer.Control hook that inspects the
// ACTUAL destination address being dialed (after DNS resolution) and refuses
// to connect unless the address is a public IP or explicitly allowlisted by
// IP/CIDR.
//
// This is the DNS-rebinding-safe enforcement point: Control fires once per
// real connection attempt, for the concrete resolved address, not for the
// original hostname string. A hostname that resolves to a public IP when
// checked ahead of time but to a private IP at the moment of connection is
// still caught here, because there is no "ahead of time" check to bypass —
// the check IS the connection attempt.
func sandboxedDialerControl(allowlist []string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("ssrf-guard: could not parse destination IP %q for dial", host)
		}
		if isIPAllowlisted(ip, allowlist) {
			return nil
		}
		if isPublicIP(ip) {
			return nil
		}
		return fmt.Errorf("ssrf-guard: destination %s is not a public address and is not in the network allowlist (blocked by default; see BuildOptions.NetworkAllowlist)", ip)
	}
}

// isPublicIP reports whether ip is safe to dial by default: not loopback,
// not link-local (covers 169.254.0.0/16 cloud-metadata range and IPv6
// fe80::/10), not private (RFC1918 10/8, 172.16/12, 192.168/16, and IPv6
// unique-local fc00::/7 via net.IP.IsPrivate), and not unspecified/multicast.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return false
	}
	return true
}

// isIPAllowlisted reports whether ip matches any allowlist entry that is an
// IP literal or CIDR. Bare-hostname entries are ignored here (they are
// matched against the pre-resolution request host by isHostAllowlisted).
func isIPAllowlisted(ip net.IP, allowlist []string) bool {
	for _, entry := range allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, ipnet, err := net.ParseCIDR(entry); err == nil && ipnet.Contains(ip) {
				return true
			}
			continue
		}
		if allowedIP := net.ParseIP(entry); allowedIP != nil && allowedIP.Equal(ip) {
			return true
		}
	}
	return false
}

// isHostAllowlisted reports whether host (the ORIGINAL, pre-resolution
// request host, e.g. "localhost" or "internal-api.corp.example") matches a
// bare-hostname allowlist entry, case-insensitively. IP/CIDR entries are
// deliberately skipped here — those are matched against the resolved
// destination address by isIPAllowlisted / sandboxedDialerControl instead,
// so a plain IP entry cannot be used to bypass by claiming an unrelated host
// string.
func isHostAllowlisted(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" || strings.Contains(entry, "/") || net.ParseIP(entry) != nil {
			continue
		}
		if entry == host {
			return true
		}
	}
	return false
}

// NewGuardedHTTPClient returns an *http.Client based on base (or a sane
// default if base is nil) whose Transport routes every dial through the SSRF
// guard: by default only public destination addresses can be connected to.
// This is the sole enforcement point for the download/fetch tools' SSRF
// protection (BUG-2) and is applied by every outbound-fetch tool constructor
// in this package that owns an *http.Client (fetch, download, and their
// deferred-tier equivalents) — regardless of what Transport the caller
// originally configured on base.
//
// allowlist entries are the explicit, safety-biased-default opt-in: a bare
// hostname (matched against the pre-resolution request host, e.g.
// "localhost") or an IP/CIDR literal (matched against the actual resolved
// destination address at dial time, e.g. "10.0.0.0/8" or "127.0.0.1"). An
// empty allowlist means no exceptions: only public addresses are reachable.
//
// The guard is both DNS-rebinding safe and redirect safe: the same
// Transport (and therefore the same guarded DialContext) is reused for every
// connection a request needs, including connections made while following
// HTTP redirects, so the dial-time check runs again for each new
// destination — a public URL that 302s to a blocked address still fails.
func NewGuardedHTTPClient(base *http.Client, allowlist []string) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	clone := *base

	var transport *http.Transport
	if t, ok := base.Transport.(*http.Transport); ok && t != nil {
		transport = t.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}

	control := sandboxedDialerControl(allowlist)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		if !isHostAllowlisted(host, allowlist) {
			// Not explicitly allowlisted by hostname: every candidate address this
			// dial resolves to must individually pass the public-address check.
			dialer.Control = control
		}
		return dialer.DialContext(ctx, network, address)
	}
	clone.Transport = transport
	return &clone
}
