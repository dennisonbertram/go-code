# Issue #1058 TUI waiting-overlay impact map

## Task

- Task / issue: TUI loses top-level SSE `run_id` (#1058).
- Plan: `2026-07-31-issue-1058-tui-waiting-overlay-plan.md`
- Owner: current issue worktree.
- Status: correlation fix locally verified; review promotion pending.

## Current Ownership, Callers, and Data Flow

- Entry: run-scoped SSE from `internal/server/http_runs.go`.
- Owners: `tui.sseEnvelope`/`decodeSSE` -> `SSEEventMsg` ->
  `Model.Update` -> `fetchAskUserPendingCmd`/`submitAskUserAnswerCmd`.
- Source of truth: top-level event-envelope `run_id`; payload owns call data.
- Wait-instance source of truth: each `run.waiting_for_user` activation owns a
  model generation and call ID. Async GET success/error messages are valid
  only while that exact wait instance remains active.
- Searches: `rg` across `cmd/harnesscli`, `internal/server`,
  `internal/harness`, and e2e tests for waiting/input/run-ID symbols.
- Similar paths: tool/plan approvals use model `RunID`; non-TUI AskUser uses
  the known run ID outside this decoder.
- Conclusion: normalize once in `SSEEventMsg`; do not mutate every payload.

## Config, API, CLI, and Tools

- Config/defaults: none.
- API/wire: no public change to SSE or GET/POST `/input`.
- CLI: TUI behavior only; headless/non-TUI behavior unchanged.
- Errors: existing fetch/submit status messages remain authoritative.

## Persistence and Compatibility

- Schema/cache: none.
- Compatibility: additive internal field preserves current server envelopes.
  Payload remains byte-for-byte payload-only. Replay uses the same decoder.
- Mixed versions: old TUI remains stalled; new TUI can answer already-pending
  runs after reconnect. No data repair.

## Lifecycle, Security, and Reliability

- Preserve backpressure, bounded reconnect, `Last-Event-ID`, deadline,
  cancellation, resume dismissal, and terminal handling.
- A resume invalidates the active wait generation. A newer wait in the same run
  supersedes the prior generation. Late successes and errors must not
  resurrect or overwrite the overlay.
- Preserve bearer auth on SSE and input requests.
- No prompt, answer, token, or credential logging.
- A fetch/submit failure remains recoverable through current status handling.

## Product and Integration Surfaces

- Server/runtime: wire producer unchanged.
- TUI: bridge, message, waiting handler, overlay/answer acceptance.
- Web/macOS/ACP: none; they do not consume this decoder.
- Provider/model/tool catalogs: none; AskUserQuestion schema is unchanged.
- Callbacks/cron/automation: none.
- UX: visible question/options, keyboard selection, resume, continuation.

## Deployment and Operations

- Deploy as the next `harnesscli` client build; no migration or flag.
- Observe stalled-wait reports and fetch-input errors.
- Roll back the isolated client commit if unrelated SSE decoding/reconnect
  regresses.
- Operator/public runbooks: none.

## Regression Tests

- Red: production-shape SSE through bridge/model shows no overlay/no GET.
- Acceptance: overlay visible, exact GET/POST paths/body, resume dismissal,
  later assistant output and terminal continuation.
- Replay: top-level run ID survives the resumed decoder path without a payload
  copy or duplicate fetch.
- Concurrency: deterministically block a real GET, then deliver resume or a
  newer call before releasing it; only the current active generation may render.
- Commands: focused normal, focused race, package normal/race, and
  `./scripts/test-regression.sh`.

## Documentation and Handoff

- Before code: issue, plan, impact map, active plan, long-term intent, plan
  index, and diagnostic log entries.
- After code: final engineering/observational/system evidence and PR handoff.
- Public docs/release notes: none; intended behavior is unchanged.

## Warning Check

All required surfaces were searched and reconciled; unaffected surfaces carry
an explicit rationale above.
