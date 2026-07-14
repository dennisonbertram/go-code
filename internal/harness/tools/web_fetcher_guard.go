package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// guardedWebFetchMaxBytes bounds how much of a fetched page GuardedWebFetcher
// will read into memory when it serves Fetch directly (no base configured),
// mirroring the existing `fetch` tool's default cap (fetch.go).
const guardedWebFetchMaxBytes = 512 * 1024

// GuardedWebFetcher is the enforcement point for GAP-2: web_fetch,
// web_search, and agentic_fetch (deferred/web.go, deferred/agent.go,
// agent.go) are all backed by the WebFetcher interface (types.go), whose
// concrete implementation lives outside this package — and, as of this
// change, there is no production implementation of WebFetcher anywhere in
// this repository at all, so there was no existing *http.Client construction
// site to wrap. Rather than leave every future implementation to remember to
// guard itself, GuardedWebFetcher closes the hole at the one choke point
// that already exists for every caller: the BuildOptions.WebFetcher /
// DefaultRegistryOptions.WebFetcher wiring in catalog.go and
// tools_default.go, which now wrap whatever WebFetcher is supplied with this
// type before handing it to the tool constructors.
//
// Fetch(url) is the surface with an agent/attacker-controlled destination —
// the LLM chooses the URL directly:
//   - When no base WebFetcher is configured, Fetch is served entirely by
//     GuardedWebFetcher itself, through NewGuardedHTTPClient — the same
//     dial-time, DNS-rebinding-safe, redirect-safe guard used by the
//     fetch/download tools.
//   - When a base WebFetcher IS configured, GuardedWebFetcher does not own
//     its transport (and must not silently discard whatever that
//     implementation actually does — content rendering, auth headers,
//     caching, etc. — by replacing it with a bare GET; doing so would be
//     surprising and would break legitimate implementations and tests that
//     inject a fake/mock WebFetcher). Instead it performs a best-effort
//     PRE-FLIGHT check: it resolves the destination host and rejects the
//     call before ever invoking base.Fetch if any resolved address is not
//     public or allowlisted. This is NOT dial-time safe against an
//     adversarial DNS server that changes its answer between this check and
//     base's own (separate, unowned) dial moments later — that stronger
//     guarantee is only available in the no-base branch above and in
//     ssrf_guard.go's NewGuardedHTTPClient, which own the transport. It DOES
//     catch the threat cases this gap is scoped to: a literal
//     private/loopback/link-local/cloud-metadata IP, and a hostname that
//     resolves (statically, i.e. absent an active rebinding attacker) to
//     one.
//
// Search(query) has no agent-supplied destination host — the search
// backend's endpoint is chosen by whichever concrete WebFetcher
// implementation the operator wires in, not by the LLM — so it is not a
// dial-time-guardable SSRF surface in the same sense as Fetch, and is passed
// through to base unchanged.
type GuardedWebFetcher struct {
	base      WebFetcher
	allowlist []string
	client    *http.Client // used only when base == nil
}

// NewGuardedWebFetcher wraps base (which may be nil) so that Fetch is always
// subject to the SSRF guard. allowlist is the same bare-hostname/CIDR opt-in
// used by NewGuardedHTTPClient and BuildOptions.NetworkAllowlist.
func NewGuardedWebFetcher(base WebFetcher, allowlist []string) *GuardedWebFetcher {
	return &GuardedWebFetcher{
		base:      base,
		allowlist: allowlist,
		client:    NewGuardedHTTPClient(&http.Client{Timeout: 30 * time.Second}, allowlist),
	}
}

// Fetch retrieves rawURL, refusing loopback, link-local (including
// 169.254.169.254 cloud metadata), and RFC1918/ULA-private destinations by
// default — unless the destination is covered by the allowlist this fetcher
// was constructed with. See the type doc comment for the dial-time guarantee
// difference between the base == nil and base != nil cases.
func (g *GuardedWebFetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}

	if g.base == nil {
		return g.fetchDirect(ctx, rawURL)
	}

	if err := checkFetchDestinationAllowed(ctx, parsed, g.allowlist); err != nil {
		return "", err
	}
	return g.base.Fetch(ctx, rawURL)
}

// fetchDirect serves Fetch entirely through the dial-time-guarded HTTP
// client, with no base implementation to delegate to.
func (g *GuardedWebFetcher) fetchDirect(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build fetch request: %w", err)
	}
	res, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch request failed: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, guardedWebFetchMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read fetch body: %w", err)
	}
	if len(body) > guardedWebFetchMaxBytes {
		body = body[:guardedWebFetchMaxBytes]
	}
	return string(body), nil
}

// checkFetchDestinationAllowed resolves parsed's host and rejects unless
// every resolved address is public or explicitly allowlisted (fail closed:
// if a hostname resolves to a mix of public and private addresses, the
// private one is what an attacker would actually want reached, so any
// non-allowed address in the result set is enough to reject the whole
// request). A bare-hostname allowlist match short-circuits without a DNS
// lookup, exactly like isHostAllowlisted's use in ssrf_guard.go.
func checkFetchDestinationAllowed(ctx context.Context, parsed *url.URL, allowlist []string) error {
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("ssrf-guard: url %q has no host", parsed.String())
	}
	if isHostAllowlisted(host, allowlist) {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("ssrf-guard: could not resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("ssrf-guard: host %q did not resolve to any address", host)
	}
	for _, addr := range addrs {
		if isIPAllowlisted(addr.IP, allowlist) || isPublicIP(addr.IP) {
			continue
		}
		return fmt.Errorf("ssrf-guard: destination %s (resolved from %q) is not a public address and is not in the network allowlist (blocked by default; see BuildOptions.NetworkAllowlist)", addr.IP, host)
	}
	return nil
}

// Search delegates to the wrapped base WebFetcher unchanged: the search
// backend's destination is operator-configured, not agent-supplied, so it is
// outside the SSRF threat model Fetch's guard addresses. Returns an error if
// no base WebFetcher was supplied.
func (g *GuardedWebFetcher) Search(ctx context.Context, query string, maxResults int) ([]map[string]any, error) {
	if g.base == nil {
		return nil, fmt.Errorf("web search is not configured")
	}
	return g.base.Search(ctx, query, maxResults)
}
