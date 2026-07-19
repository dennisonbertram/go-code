# Plan: ACP slice 1 — stdio JSON-RPC framing and initialize handshake

Epic: #806 (parent #803). Slice 1 of 5. Branch: `epic/806-acp-server`.

## Context

- Problem: go-code has no Agent Client Protocol surface; editors (Zed, JetBrains) cannot spawn or drive it.
- User impact: without ACP, go-code is invisible to ACP clients.
- Constraints: this slice only covers newline-delimited JSON-RPC 2.0 framing over stdio plus the `initialize` handshake and spec-correct error handling. No sessions, no runs API, no SSE — those are slices 2–4. Stdlib only (`encoding/json`); no new dependencies. stdout carries protocol messages only; diagnostics go to stderr. A pre-existing `internal/harnessacp` + `cmd/harness-acp` adapter (based on `github.com/coder/acp-go-sdk`) exists; the epic deliberately specifies a new stdlib-only `internal/acp` package and a `harness acp` subcommand — do not touch the existing adapter.

## Scope

- In scope:
  - New `internal/acp` package: framed reader/writer over `io.Reader`/`io.Writer` (newline-delimited JSON), goroutine-safe writer, JSON-RPC 2.0 envelope types and error codes (`-32700`, `-32600`, `-32601`, `-32602`).
  - `initialize` handler: protocol-version negotiation (agent supports v1 only, always replies 1), agent capabilities (`loadSession: false`, `promptCapabilities{image,audio,embeddedContext: false}`), `agentInfo`, empty `authMethods`.
  - New `cmd/harnesscli/acp.go` with `runACP(args)`; `case "acp"` in `dispatch` (`cmd/harnesscli/auth.go`).
- Out of scope: `session/new`, `session/prompt`, `session/cancel`, `session/update`, `session/request_permission`, `--server` flag / runs API wiring (slices 2–4), docs/e2e (slice 5).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (user-facing docs land in slice 5).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: engineering-log entry.

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/acp`: framing (single message, partial line across reads, multiple messages per read, oversized line, goroutine-safe concurrent writes); `initialize` response shape + version negotiation; invalid params `-32602`; malformed JSON `-32700`; valid JSON / invalid request `-32600` table; unknown method `-32601`; notifications and response-shaped messages produce no output; EOF exits cleanly.
  - `cmd/harnesscli`: `runACP` serves `initialize` over injected stdin/stdout and exits 0 on EOF; `dispatch(["acp"])` routes to `runACP`.
- Existing tests to update: none.
- Regression tests required: none beyond the above (new feature).

## Cross-Surface Impact Map

- None — no provider/model flow, gateway routing, model catalog, API-key, or TUI provider plumbing changes. New leaf package + one dispatch case only.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above; epic acceptance: `printf '...initialize...' | harness acp` prints a single JSON-RPC result with capabilities).
- [ ] Write failing tests first.
- [ ] Implement minimal code changes.
- [ ] gofmt + go vet clean; `go test ./internal/acp/... ./cmd/harnesscli/... -count=1` green.
- [ ] Update docs indexes (plans INDEX) and engineering log.
- [ ] Push branch and open PR (no merge — sibling agents continue later slices).

## Risks and Mitigations

- Risk: collision/confusion with existing `internal/harnessacp` (SDK-based adapter).
- Mitigation: keep new code strictly in `internal/acp` + `cmd/harnesscli/acp.go`; do not modify the existing adapter; note the distinction in the PR body.
