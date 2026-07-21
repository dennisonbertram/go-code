# Plan: ACP epic #806 — stdio ACP server mode

Epic: #806 (parent #803). Branches: `epic/806-acp-server` (slice 1, merged), `epic/806-acp-server-s2` (slice 2).

## Slice 1 — stdio JSON-RPC framing and initialize handshake (MERGED)

### Context

- Problem: go-code has no Agent Client Protocol surface; editors (Zed, JetBrains) cannot spawn or drive it.
- User impact: without ACP, go-code is invisible to ACP clients.
- Constraints: slice 1 covers newline-delimited JSON-RPC 2.0 framing over stdio plus the `initialize` handshake and spec-correct error handling. Stdlib only (`encoding/json`); no new dependencies. stdout carries protocol messages only; diagnostics go to stderr. A pre-existing `internal/harnessacp` + `cmd/harness-acp` adapter (based on `github.com/coder/acp-go-sdk`) exists; the epic deliberately specifies a new stdlib-only `internal/acp` package and a `harness acp` subcommand — do not touch the existing adapter.

### Scope (slice 1)

- In scope:
  - New `internal/acp` package: framed reader/writer over `io.Reader`/`io.Writer` (newline-delimited JSON), goroutine-safe writer, JSON-RPC 2.0 envelope types and error codes (`-32700`, `-32600`, `-32601`, `-32602`).
  - `initialize` handler: protocol-version negotiation (agent supports v1 only, always replies 1), agent capabilities (`loadSession: false`, `promptCapabilities{image,audio,embeddedContext: false}`), `agentInfo`, empty `authMethods`.
  - New `cmd/harnesscli/acp.go` with `runACP(args)`; `case "acp"` in `dispatch` (`cmd/harnesscli/auth.go`).
- Out of scope: `session/new`, `session/prompt`, `session/cancel`, `session/update`, `session/request_permission`, `--server` flag / runs API wiring (slices 2–4), docs/e2e (slice 5).

## Slice 2 — session/new and session/prompt over the runs API (IN IMPLEMENTATION)

### Scope (slice 2)

- In scope:
  - `internal/acp/client.go`: stdlib `RunsClient` — `StartRun` (POST `/v1/runs` with `{"prompt": ...}`), `CancelRun` (POST `/v1/runs/{id}/cancel`), `WaitTerminal` (GET `/v1/runs/{id}/events` SSE scan until a terminal event, tracking `run.cost_limit_reached`). Bearer auth from config. Two HTTP clients: bounded timeout for request/response, no timeout for SSE.
  - `internal/acp/session.go`: mutex-guarded session store (sessionId -> runId, one run per session); `session/new` (unique `sess_<hex>` ids, accepts cwd/mcpServers without acting on them); `session/prompt` (content-block text extraction: `text` blocks joined, `resource_link` contributes its URI; empty extraction -> `-32602`); `session/cancel` notification -> cancel POST.
  - Stop reasons: `run.completed` -> `end_turn`, `run.cost_limit_reached` + completed -> `max_turn_requests`, `run.failed` -> `refusal`, `run.cancelled` -> `cancelled`.
  - Concurrent dispatch in `server.go`: `session/prompt` holds its response open until the run terminates, so handlers must run in goroutines or a mid-turn `session/cancel` could never be read. Writes stay serialized by the Conn mutex; `Serve` drains in-flight handlers before returning at EOF. `-32603` internal error code added.
  - `cmd/harnesscli/acp.go`: `--server` flag; resolution flag > `loadConfig().Server` > `http://localhost:8080`; API key from `loadConfig()`; `EnableSessions(client)` on the server.
- Out of scope: `session/update` translation (slice 3), `session/request_permission` (slice 4), multi-turn (one run per ACP session — second `session/prompt` on the same session errors), MCP-server wiring from `session/new` params, docs/e2e (slice 5).

### Test Plan (TDD, slice 2)

- New failing tests:
  - `internal/acp`: RunsClient against httptest (start/cancel/auth header/error paths; SSE terminal wait incl. cost-limit flag, ping/retry lines skipped, stream-ended-early error); content-block extraction table; stop-reason table; `session/new` uniqueness; `session/prompt` unknown session / second prompt rejected; full flows over scripted stdio (prompt -> `end_turn`; cancel mid-run -> cancel POST + `cancelled` stop reason via `io.Pipe` staging); concurrent sessions isolated; handler-concurrency proof (blocked handler does not stall `initialize`).
  - `cmd/harnesscli`: scripted initialize -> session/new -> session/prompt against an httptest harnessd double via `--server`; config-driven server URL + Bearer key; cancel mid-run issues the cancel POST.
- Existing tests to update: `TestServerSequentialRequestsAnsweredInOrder` — responses are correlated by id instead of slice position (concurrent dispatch does not guarantee response order; JSON-RPC clients correlate by id).

### Acceptance (slice 2)

- Scripted stdio exchange — `initialize`, `session/new`, `session/prompt` against the fake server — returns a `session/prompt` result whose `stopReason` matches the fake's terminal SSE event; cancel mid-run issues the cancel POST.
- `go test ./internal/acp/... ./cmd/harnesscli/... -count=1` green.

## Slice 3 — session/update notifications from the SSE stream (IN IMPLEMENTATION)

### Scope (slice 3)

- In scope:
  - `internal/acp/updates.go`: translator from harness SSE events to ACP `session/update` update objects — `assistant.message.delta` -> `agent_message_chunk`, `assistant.thinking.delta` -> `agent_thought_chunk` (payload field `content` for both), `tool.call.started` -> `tool_call` (`toolCallId` from `call_id`, `title` from `tool`, `status: "in_progress"`, `kind` via a tool-name table), `tool.call.completed` -> `tool_call_update` (`status: "completed"`, or `"failed"` when the payload has an `error`; output included as content).
  - Bounded per-turn notification queue: coalesce consecutive same-kind text deltas; drop deltas (counted + logged) under pressure; lifecycle updates never dropped (they evict buffered deltas first). The SSE reader never blocks on a slow editor beyond this one queue.
  - `client.go`: `WatchRun(ctx, runID, onEvent)` generalizes `WaitTerminal` (now a nil-callback wrapper); oversized SSE lines (>16 MiB, test-shrinkable var) are drained and their event skipped with a logged warning instead of corrupting the stream.
  - `server.go`/`jsonrpc.go`: `writeNotification("session/update", ...)`; the prompt handler drains the queue fully before writing the `session/prompt` response (spec: updates precede the turn result).
- Out of scope: `session/request_permission` (slice 4), `agent_thought_chunk` coalescing across message/thought boundaries (different kinds never coalesce), plan/todo updates, `tool.call.delta` argument streaming.

### Test Plan (TDD, slice 3)

- New failing tests:
  - Translator table (all four mappings incl. failed tool call, unmapped events, empty deltas).
  - Tool-kind mapping table.
  - Queue: same-kind deltas coalesce, different kinds don't, lifecycle breaks coalescing; full queue drops deltas (counted); lifecycle evicts buffered deltas; drain-after-close.
  - Client: oversized SSE event line skipped with warning, terminal still found.
  - Golden: scripted client performing `session/prompt` against the fake observes the exact ordered `session/update` stream (two message chunks, thought chunk, tool_call, tool_call_update with stable `toolCallId`) before the `session/prompt` result.
- Existing tests: none modified; all slice-1/2 tests must stay green.

### Acceptance (slice 3)

- Scripted client performing `session/prompt` against the fake server observes `agent_message_chunk` notifications in order before the `session/prompt` result arrives.
- `go test ./internal/acp/... -count=1` green (plus `-race`).

## Slice 4 — permission bridge via session/request_permission (IN IMPLEMENTATION)

### Scope (slice 4)

- In scope:
  - Client-bound calls: `Server.callClient(ctx, method, params)` — writes a JSON-RPC request to the editor, registers a pending call by id, and routes the client's response (result or error object) back to the waiter; `dispatch` now routes response-shaped messages to pending calls (unknown ids stay logged-and-ignored).
  - `RunsClient.ApproveRun`/`DenyRun` (POST `/v1/runs/{id}/approve|deny`); `ErrApprovalNotConfigured` sentinel for the server's 501 no-broker response.
  - `internal/acp/permission.go`: on `tool.approval_required` (payload `call_id`, `tool`, `arguments`, `deadline_at`), issue `session/request_permission` with options `allow-once`/`allow_once` and `reject-once`/`reject_once`; selected allow -> approve POST, selected reject / `cancelled` outcome / client error -> deny POST.
  - Deadline discipline: the client call carries a `deadline_at`-bounded context; when it passes, the pending call is deregistered and nothing is POSTed (harnessd auto-denies at the deadline). Bridge goroutines are tied to a turn-scoped context so a finished turn can't leave stragglers.
  - 501 no-broker: surface a `session/update` (`agent_message_chunk`) note instead of hanging until the deadline.
- Out of scope: plan-approach option bridging (`ApproveWithOption`), `allow_always`/`reject_always` persistence, permission for mid-run user-input requests, docs/e2e (slice 5).

### Test Plan (TDD, slice 4)

- New failing tests:
  - Units: permission-params shape (option ids/kinds, toolCall fields); outcome parsing table (selected allow/reject, cancelled, garbage).
  - Server: `callClient` routes a client response to the waiter by id.
  - Flows over scripted stdio + fake harnessd (new `/approve`/`/deny` routes with a 501 no-broker mode): grant -> approve POST + run completes; reject -> deny POST; `cancelled` outcome -> deny; client error -> deny; deadline expiry -> no POST, prompt still completes, late response ignored; 501 -> `session/update` note, no hang.
- Existing tests: none modified; slices 1–3 tests must stay green.

### Acceptance (slice 4)

- Scripted run: fake server emits `tool.approval_required` and blocks until `/approve` arrives; the scripted client grants via `session/request_permission`; the run completes (`end_turn`).
- `go test ./internal/acp/... -count=1` green (plus `-race`).

## Documentation Contract

- Feature status: slice 1 `implemented` (PR #835); slice 2 `in implementation`.
- Public docs affected: none (user-facing docs land in slice 5).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: engineering-log entry.

## Cross-Surface Impact Map

- None — no provider/model flow, gateway routing, model catalog, API-key, or TUI provider plumbing changes. New leaf package + dispatch case + CLI flags only. The runs API is consumed as an HTTP client; no server-side changes.

## Implementation Checklist

- [x] Slice 1: framing + initialize, merged via PR #835.
- [x] Slice 2: write failing tests first (red: `undefined: NewRunsClient`; CLI red: `-server` flag undefined).
- [x] Slice 2: implement minimal code changes (`client.go`, `session.go`, concurrent dispatch in `server.go`, flags in `acp.go`).
- [x] Slice 2: gofmt + go vet clean; `go test ./internal/acp/... ./cmd/harnesscli/... -count=1` green.
- [x] Slice 2: update engineering log.
- [x] Slice 2: push branch `epic/806-acp-server-s2` and open PR (no merge) — PR #874.
- [x] Slice 3: failing tests first (red: `undefined: runEvent`, `translateRunEvent`, `maxSSELineSize` const).
- [x] Slice 3: translator + bounded coalescing queue + `WatchRun` + `writeNotification` + prompt wiring.
- [x] Slice 3: gofmt + go vet clean; `go test ./internal/acp/ -count=1` (+ `-race`, `-count=3`) green; `cmd/harnesscli` green.
- [x] Slice 3: update engineering log.
- [x] Slice 3: push branch `epic/806-acp-server-s3` and open PR (no merge) — PR #891.
- [x] Slice 4: failing tests first (red: `undefined: permissionParams`, `srv.callClient`); id-unquote bug found by `TestCallClientRoutesResponses` hang and fixed with the test as regression.
- [x] Slice 4: callClient pending registry + response routing, ApproveRun/DenyRun + 501 sentinel, permission bridge with deadline + turn-scoped contexts.
- [x] Slice 4: gofmt + go vet clean; `go test ./internal/acp/... -count=1` (+ `-race`, repeat) green; `cmd/harnesscli` green.
- [x] Slice 4: update engineering log.
- [x] Slice 4: push branch `epic/806-acp-server-s4` and open PR (no merge) — PR #916.

## Risks and Mitigations

- Risk: collision/confusion with existing `internal/harnessacp` (SDK-based adapter).
- Mitigation: keep new code strictly in `internal/acp` + `cmd/harnesscli/acp.go`; do not modify the existing adapter; note the distinction in the PR body.
- Risk: concurrent dispatch regresses slice-1 ordering assumptions.
- Mitigation: responses correlated by id in the updated test; writer stays mutex-serialized; handler-concurrency proof test.
