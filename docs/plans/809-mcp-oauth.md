# Plan: MCP OAuth login flow for remote servers (epic #809)

## Slice 3: OAuth 2.1 + PKCE authorization flow with localhost callback (in implementation)

- Goal: browser-based login as a library function usable by the CLI (slice 4): discover endpoints, drive the browser, complete the exchange, store tokens, refresh silently.
- Changes: new `internal/mcp/oauth` package (imports slice 2's `mcp.TokenStore`; no cycle — `internal/mcp` does not import it).
  - Discovery: parse `WWW-Authenticate` on a 401 probe for `resource_metadata` → fetch protected-resource metadata (RFC 9728, well-known inserted before path, bare-origin retry) → fetch AS metadata (RFC 8414); fallback to well-known probing when the header is absent, and to a co-located AS at the resource origin.
  - PKCE: S256 verifier/challenge on `crypto/rand`; random state.
  - Loopback `net/http` listener on `127.0.0.1:0`, path `/callback`, first request wins, ctx-cancellable.
  - Browser open via `os/exec` (`open`/`xdg-open`/`rundll32`), injectable as `Flow.OpenURL` so tests drive the redirect in-process.
  - RFC 8707 `resource` parameter on the authorization request.
  - Dynamic client registration (RFC 7591) when no client ID is configured and the AS advertises `registration_endpoint`; pre-registered client ID via `LoginOptions.ClientID`.
  - Token exchange + `Refresh(serverName)` using the stored refresh token and recorded client ID (`ClientID` added to `mcp.Token` — additive); un-rotated refresh tokens are preserved; `invalid_grant` maps to `ErrReauthRequired`.
- Tests (TDD; in-process `httptest` mock AS + mock resource server; no real network/browser/$HOME): full code+PKCE round trip with the browser stubbed (stub GETs the auth URL, mock AS 302s into the loopback), discovery via `WWW-Authenticate`, fallback when the header is absent, dynamic registration, pre-registered ID, missing-both error, state mismatch, AS error redirect, user abort via ctx cancel, token-endpoint error, refresh (success, un-rotated, invalid_grant, no token), PKCE known vector, `resource` param asserted.
- Acceptance: `go test ./internal/mcp/oauth/... -count=1` green; round trip completes without real network or browser.

## Slice 2: file-backed OAuth token store under `~/.harness/mcp/` (implemented, PR #856)

## Slice 1: MCP HTTP static headers and typed auth errors (implemented, PR #840)

## Context

- Problem: the MCP HTTP transport (`internal/mcp/http_conn.go`) sends no credentials — `ServerConfig` has no header support, and any non-2xx response (including 401/403) collapses into a generic error string, so auth failures are indistinguishable from other failures.
- User impact: no way to use an OAuth-protected (or static-bearer) remote MCP server at all; no actionable error on auth failure.
- Constraints: slice 1 of epic #809 only — static headers + typed 401/403 errors. OAuth flow, token store, CLI login, and docs slices are out of scope. No new third-party dependencies.

## Scope

- In scope:
  - `Headers map[string]string` on `mcp.ServerConfig` (`internal/mcp/mcp.go`).
  - `headers` in TOML `MCPServerConfig` (`internal/config/config.go`) and pass-through in `cmd/harnessd/mcp_setup.go`.
  - `headers` key in `HARNESS_MCP_SERVERS` JSON parsing (`internal/mcp/config.go`).
  - Header injection in `httpConn.sendRequest` (`internal/mcp/http_conn.go`).
  - Sentinel errors `ErrUnauthorized` / `ErrForbidden` returned (wrapped, `errors.Is`-compatible) on 401/403; other non-2xx handling unchanged.
- Out of scope: OAuth 2.1 flow, token store, `harnesscli mcp login`, SSE legacy transport, per-run `MCPServerConfig` in `internal/harness/scoped_mcp.go` (separate type; not cited by the slice).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none in this slice (docs are slice 5).
- Spec docs to update before code: none.
- Implementation notes to add after code: none beyond this plan and the folder index.

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/mcp/http_conn_test.go`: headers attached on initialize/list/call; table-driven 401→`ErrUnauthorized`, 403→`ErrForbidden`, other non-2xx unchanged; end-to-end bearer-gated mock server reachable via config; typed error survives `ClientManager` wrapping (`errors.Is`).
  - `internal/mcp/config_test.go`: env JSON `headers` round-trip.
  - `internal/config/config_test.go`: TOML `[mcp_servers.*.headers]` decode.
  - `cmd/harnessd/mcp_setup_test.go`: TOML headers passed through (behavioral: register via `registerMCPServersFromConfig` against a bearer-gated mock server, DiscoverTools succeeds).
- Existing tests to update: none.
- Regression tests required: covered by the new tests above.

## Cross-Surface Impact Map

- Not a provider/model flow change — impact map not required. Touches config schema additively (`headers` key only), no server API or TUI state changes.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first; verify they fail for the right reason.
- [x] Implement minimal code changes (5 files listed above).
- [x] gofmt + go vet clean.
- [x] Run `go test ./internal/mcp/... ./internal/config/... ./cmd/harnessd/... -count=1`.
- [x] Update `docs/plans/INDEX.md`.
- [x] Commit, push `epic/809-mcp-oauth`, open PR (no merge). — PR #840, merged.

## Risks and Mitigations

- Risk: configured headers overriding protocol headers (`Content-Type`, `Accept`) could break the transport.
- Mitigation: apply protocol headers first, then configured headers via `Header.Set`, so explicit user config wins predictably; document the behavior in the struct comment.
