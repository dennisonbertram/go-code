# Plan: GET /v1/tasks union endpoint (epic #814, slice 1)

## Context

- Problem: background work (subagents, cron jobs, delayed callbacks) is only visible through separate surfaces; there is no single endpoint answering "what is running right now?".
- User impact: TUI `/tasks` panel (later slices) and non-TUI clients need one authenticated union endpoint.
- Constraints: slice 1 of epic #814 only — subagents + cron + callbacks. Bash jobs are slice 2, TUI panel is slice 3. Strict TDD per `docs/runbooks/testing.md`.

## Scope

- In scope:
  - Unified `Task` DTO + `GET /v1/tasks` handler in `internal/server/http_tasks.go` (new).
  - Route registration in `internal/server/http.go` next to `/v1/subagents` and `/v1/cron/jobs`, requiring `runs:read`.
  - `CallbackManager.ListAll()` for cross-conversation enumeration of pending callbacks (`internal/harness/tools/delayed_callback.go`).
  - Server wiring: new `CallbackLister` option on `ServerOptions`; pass the daemon's `*tools.CallbackManager` through `cmd/harnessd` (`main.go` → `runtime_container.go` → `bootstrap_helpers.go`).
- Out of scope: bash jobs in the union, any cancel/kill endpoint, TUI changes (later slices).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (new endpoint; later slices own user-facing `/tasks` docs).
- Spec docs to update before code: none.
- Implementation notes to add after code: engineering log entry per runbook.

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/harness/tools/delayed_callback_test.go`: `ListAll` returns pending callbacks across conversations; fired/canceled excluded; empty manager returns empty.
  - `internal/server/http_tasks_test.go`:
    - empty union → 200 with `"tasks": []`;
    - subagent entries map to `type=subagent` with status/label/age/actions;
    - cron entries map to `type=cron`;
    - callback entries map to `type=callback`;
    - tenant filtering matches `/v1/subagents` and `/v1/cron/jobs` behavior;
    - missing `runs:read` scope → 403; unauthenticated → 401 (auth enabled);
    - nil sources are skipped (partial union still 200).
- Existing tests to update: none expected.
- Regression tests required: handler tests above are the regression net.

## Cross-Surface Impact Map

- Required? This adds a server API route but does not touch provider/model flows, gateway routing, model catalogs, API-key management, or provider plumbing → not required. Config: None. TUI state: None (slice 3). Regression tests: new handler tests listed above.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first (`internal/server/http_tasks_test.go`, `delayed_callback_test.go` addition).
- [x] Implement `CallbackManager.ListAll`.
- [x] Implement `internal/server/http_tasks.go` (DTO, mapping, tenant filter, sorting) + route in `http.go`.
- [x] Wire `CallbackLister` through `ServerOptions` and `cmd/harnessd`.
- [x] gofmt + go vet clean; `go test ./internal/server/... ./internal/harness/tools/... ./cmd/harnessd/... -count=1` green (one pre-existing `internal/harness/tools` failure also red on main — see engineering log).
- [x] Update engineering log; commit, push `epic/814-tasks-panel`, open PR (no merge).

## Risks and Mitigations

- Risk: tenant-scoping semantics differ per source (subagents normalize ""→"default"; cron exact-match with ""-caller passthrough).
- Mitigation: reuse the existing helpers verbatim (`filterSubagentsByTenant`, `filterCronJobsByTenant`) and mirror the cron helper shape for callbacks, with tests for each.
