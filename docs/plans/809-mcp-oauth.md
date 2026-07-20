# Plan: MCP HTTP static headers and typed auth errors (epic #809, slice 1)

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
- [ ] Commit, push `epic/809-mcp-oauth`, open PR (no merge).

## Risks and Mitigations

- Risk: configured headers overriding protocol headers (`Content-Type`, `Accept`) could break the transport.
- Mitigation: apply protocol headers first, then configured headers via `Header.Set`, so explicit user config wins predictably; document the behavior in the struct comment.
