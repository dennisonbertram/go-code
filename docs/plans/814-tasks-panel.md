# Plan: unified /tasks background panel (epic #814)

## Slice 1 (merged, PR #834): GET /v1/tasks union endpoint for subagents, cron, callbacks

- `internal/server/http_tasks.go`: unified `Task` DTO + `GET /v1/tasks` (runs:read) unioning subagents, cron jobs, pending callbacks.
- `CallbackManager.ListAll` for cross-conversation pending-callback enumeration; daemon wired via `ServerOptions.CallbackLister`.

## Slice 2 (merged, PR #869): expose background bash jobs to the task union

- `JobManager.List()` snapshot + tenant capture; daemon-level `harness.JobTracker` (`jm<N>:job_<n>` namespacing) registered via `DefaultRegistryOptions.JobTracker`.
- `GET /v1/tasks` unions `bash_job` entries; `POST /v1/jobs/{id}/kill` terminates them (runs:write, tenant-scoped).

## Slice 3 (merged, PR #893): /tasks overlay listing unified background tasks

- `components/taskspanel` (value-semantics Model): TYPE/STATUS/AGE/COMMAND columns, cursor navigation with scroll-into-view windowing, empty/loading/error states.
- `RemoteTask` + `loadTasksCmd`; `/tasks` registered in `cmd_parser.go` (auto-feeds `/help`, slash-complete, tab completion).

## Slice 4 (this branch, epic/814-tasks-panel-s4): task actions — view output and stop from the /tasks panel

### Context

- Problem: the `/tasks` overlay (slice 3) is read-only; stopping work or reading output still requires agent cooperation or per-surface commands.
- User impact: from a selected row the user can view recent output and stop/kill the task through the existing per-type mechanisms.
- Constraints: slice 4 only — actions on the existing panel. Cron deletion requires a confirmation prompt. Strict TDD.

### Scope

- In scope:
  - Server (TUI has no other path): `GET /v1/jobs/{id}/output` (bash job output via new `JobTracker.Output`, runs:read, tenant-checked) and `POST /v1/callbacks/{id}/cancel` (new `CallbackCanceler` option, runs:write, tenant-checked). Subagent cancel and cron delete endpoints already exist.
  - `components/taskspanel`: confirm mode (`AskConfirm`/`PendingConfirm`/`ResolveConfirm` + prompt rendering), detail mode (`ShowOutput`/`CloseDetail`/scroll + rendering), transient notice line (`SetNotice`), `HandleEscape` backing out of sub-modes.
  - TUI api: `fetchTaskOutputCmd` (bash_job → `/v1/jobs/{id}/output`, subagent → `/v1/subagents/{id}`), `cancelTaskCmd` (per-type dispatch: kill/cancel/DELETE/cancel), `TaskOutputLoadedMsg`/`TaskActionResultMsg`.
  - `model.go`: mode-aware key routing (`o`/Enter output, `x`/Ctrl+K stop, cron delete confirms first, detail scroll/back, confirm y/n), Esc consumed by panel sub-modes before the overlay close, list refresh after a successful stop, action errors surfaced via the panel notice + status message.
- Out of scope: streaming live output (SSE tail), periodic auto-refresh, per-type confirm prompts beyond cron delete, killing whole runs (only background tasks).

### Test Plan (TDD)

- Failing tests first:
  - `internal/harness/job_tracker_test.go`: `Output` routes to the owning manager, unknown IDs → `ErrJobNotFound`.
  - `internal/server/http_tasks_test.go`: job output endpoint (200 + payload, 404, 501, cross-tenant); callback cancel (200 + pending removed, 404, 501, cross-tenant).
  - `components/taskspanel`: confirm prompt render + resolve, detail render/scroll/indicators, notice line, Esc handling, Open resets modes.
  - `cmd/harnesscli/tui/api_tasks_test.go`: output fetch (bash_job, subagent, failure) and per-type cancel dispatch + server error.
  - `cmd/harnesscli/tui/tasks_overlay_814_test.go`: `o` bash job → detail with fetched output; `o` cron → static detail without fetch; Enter alias; `x` bash job → kill + refresh; `x` cron → confirm prompt, no DELETE until `y`, `n`/Esc cancel; `x` subagent/callback → cancel endpoints; action error in panel; Esc in detail/confirm returns to list.
- Existing tests to update: none expected.

### Cross-Surface Impact Map

- Not required (no provider/model flow surface). Config: None. Server API: two new routes (`/v1/jobs/{id}/output`, `/v1/callbacks/{id}/cancel`). TUI state: panel sub-modes (confirm/detail) + action messages. Regression tests: listed above.

### Implementation Checklist

- [x] Failing tests first (tracker, server, component, api, model).
- [x] `JobTracker.Output` + `GET /v1/jobs/{id}/output`.
- [x] `POST /v1/callbacks/{id}/cancel` + `CallbackCanceler` wiring.
- [x] taskspanel confirm/detail/notice modes.
- [x] TUI api commands + model wiring + Esc handling + refresh.
- [x] gofmt/vet clean; `go test ./cmd/harnesscli/... -count=1` green (+ internal/server, internal/harness, cmd/harnessd).
- [x] Docs (this plan, engineering log, indexes); commit, push, open PR (no merge).

### Risks and Mitigations

- Risk: Enter is captured by the global Enter-key block before overlay routing. Mitigation: explicit tasks guard in that block delegating to the shared `openSelectedTaskOutput` helper (covered by `TestTasks_EnterAlsoOpensOutput`).
- Risk: Esc closes the whole overlay instead of backing out of a sub-mode. Mitigation: the Esc chain consults `tasksPanel.Mode()` first; regression tests cover both sub-modes.

## Slice 3 detail (kept for reference)

### Context

- Problem: `GET /v1/tasks` (slices 1-2) has no TUI surface; `/subagents` only prints lines into the chat viewport.
- User impact: `/tasks` opens one overlay listing every piece of background work with type, status, age, and command/label columns.
- Constraints: slice 3 only — listing + navigation. Row actions (view output, stop/kill, cron-delete confirm) are slice 4. Strict TDD.

### Scope

- In scope:
  - New component `cmd/harnesscli/tui/components/taskspanel/` modeled on `components/helpdialog/` (value-semantics Model, bordered centered dialog, overflow indicators, footer hints): columns TYPE / STATUS / AGE / COMMAND, cursor row navigation (j/k, ↑/↓) with scroll-into-view, empty ("No background tasks."), loading ("Loading tasks…"), and error states.
  - `cmd/harnesscli/tui/api.go`: `RemoteTask` DTO + `loadTasksCmd` (mirrors `loadSubagentsCmd`); `TasksLoadedMsg`/`TasksLoadFailedMsg` in `messages.go`.
  - `model.go`: `tasksPanel` field; `executeTasksCommand` (open overlay + kick off fetch); `TasksLoadedMsg`/`TasksLoadFailedMsg` handlers; key routing for the `tasks` overlay (up/down/j/k, `r` refresh); View case; close on Esc/OverlayCloseMsg; test accessors.
  - `cmd_parser.go`: register `/tasks` next to `/subagents` (auto-feeds `/help`, slash-complete, and tab completion, which are registry-driven).
  - Update command-enumeration tests (`cmd_parser_test.go`, `search_test.go`, `tabcomplete_test.go` if it asserts exact sets).
- Out of scope: row actions (slice 4), live output streaming, periodic auto-refresh while open (manual `r` only).

### Test Plan (TDD)

- Failing tests first:
  - `components/taskspanel/model_test.go`: Open resets (loading, cursor 0), SetTasks clamps cursor + clears loading, SetError, MoveUp/Down clamping, Selected.
  - `components/taskspanel/view_test.go`: header/columns/rows render (type, status, formatted age, label); empty/loading/error states; cursor row highlight; overflow indicators on long lists.
  - `cmd/harnesscli/tui/api_tasks_test.go`: `loadTasksCmd` success → `TasksLoadedMsg`; non-200 and invalid JSON → `TasksLoadFailedMsg` (pattern: `api_coverage_test.go`).
  - `cmd/harnesscli/tui/tasks_overlay_814_test.go` (pattern: `overlay_670_test.go`): `/tasks` dispatch opens the `tasks` overlay in loading state; `TasksLoadedMsg` populates rows in View; empty list → "No background tasks."; fetch failure → error state; Esc closes; j/k move cursor; `r` re-fetches.
- Existing tests to update: command-enumeration lists (`cmd_parser_test.go`, `search_test.go`).

### Cross-Surface Impact Map

- Not required (no provider/model flow, gateway routing, catalog, or API-key surface). Config: None. Server API: consumes existing `GET /v1/tasks` only. TUI state: new `tasksPanel` field + `tasks` overlay kind. Regression tests: listed above.

### Implementation Checklist

- [x] Failing tests first (component, api, model overlay).
- [x] `taskspanel` component (model.go + view.go).
- [x] `RemoteTask` + `loadTasksCmd` + messages.
- [x] model wiring + `/tasks` registration + enumeration test updates.
- [x] gofmt/vet clean; `go test ./cmd/harnesscli/... -count=1` green.
- [x] Docs (this plan, engineering log, indexes); commit, push, open PR (no merge).

### Risks and Mitigations

- Risk: `initModel`-based overlay tests are sensitive to exact View output. Mitigation: assert on stable substrings (column headers, labels, state text), not exact frames.
- Risk: cursor/scroll coupling bugs on short terminals. Mitigation: component tests at small heights with overflow indicator assertions.

## Slice 2 detail (kept for reference)

### Context

- Problem: background bash jobs run through the agent-facing `job_output`/`job_kill` tools backed by a per-registry `JobManager` (`internal/harness/tools/bash_manager.go`). Registries are built per daemon (`cmd/harnessd/runtime_container.go`) and per provisioned-workspace run (`internal/harness/runner.go:1148`), so jobs are not enumerable or killable daemon-wide.
- User impact: `GET /v1/tasks` must answer "what background bash jobs are running?" and the daemon must be able to kill them server-side.
- Constraints: slice 2 only — no TUI changes (slice 3), no generic `/v1/tasks/{id}/cancel` for other types. Strict TDD.

### Scope

- In scope:
  - `JobManager.List()` snapshot (unexported `list()` in `bash_manager.go`, exported wrapper in `job_manager_exports.go` per the existing pattern) returning `JobInfo{ID, Command, WorkingDir, StartedAt, TenantID, Running, ExitCode, TimedOut}`; tenant captured from `RunMetadataFromContext` in `runBackground`.
  - Daemon-level `JobTracker` (`internal/harness/job_tracker.go`): register/unregister per-registry managers, `List()` union with namespaced task IDs (`jm<N>:job_<n>`), `Get`, and `Kill` reusing `JobManager.Kill`.
  - `DefaultRegistryOptions.JobTracker` so the main registry, per-run workspace registries (runner.go:1148), and subagent worktree registries all register their managers; unregister via registry shutdown hook.
  - Server: `bash_job` entries in `GET /v1/tasks` (status `running`/`exited`/`timed_out`, label = command, tenant-filtered like callbacks) and `POST /v1/jobs/{id}/kill` (runs:write, 404 unknown/cross-tenant, 501 unconfigured).
  - harnessd wiring: one `JobTracker` created in `main.go`, threaded to `baseRegistryOptions` and `ServerOptions.JobTracker`.
- Out of scope: TUI panel (slice 3), panel stop actions for other task types (slice 4), changing agent-facing `shell_id` format (`job_<n>` stays), the pre-existing flaky `TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly`.

### Test Plan (TDD)

- New failing tests first:
  - `internal/harness/tools/bash_manager_list_test.go`: empty List; running → exited transition with exit code; timed-out status; ttl-evicted jobs absent; tenant captured from run metadata; concurrent List/RunBackground/Kill (race).
  - `internal/harness/job_tracker_test.go`: register idempotence + unique refs; List unions with namespaced IDs; unregister removes; Get; Kill routes to the right manager and unknown ID → ErrJobNotFound.
  - `internal/server/http_tasks_test.go` (extend): `bash_job` union entries (status/label/actions); tenant filtering; `POST /v1/jobs/{id}/kill` kills a real running job (200, `job_output` shows not-running), 404 unknown, 404 cross-tenant, 403 without runs:write, 405 on GET, 501 when tracker unconfigured.
- Regression net: the above plus existing slice-1 handler tests.

### Cross-Surface Impact Map

- Not required (no provider/model flow, gateway routing, catalog, or API-key surface). Config: None. Server API: `/v1/tasks` gains `bash_job` entries; new `/v1/jobs/{id}/kill`. TUI state: None (slice 3). Regression tests: listed above.

### Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Failing tests first (tools list, tracker, server union + kill).
- [x] `JobManager.List` + tenant capture.
- [x] `JobTracker` + `DefaultRegistryOptions.JobTracker` + tools_default registration/unregistration.
- [x] Server union + kill route + harnessd wiring.
- [x] gofmt/vet clean; package tests green; docs (this plan, engineering log, indexes).
- [x] Commit, push `epic/814-tasks-panel-s2`, open PR (no merge).

### Risks and Mitigations

- Risk: job IDs collide across managers (each starts at `job_1`). Mitigation: tracker namespaces task IDs as `jm<N>:job_<n>`; kill parses the namespace.
- Risk: tracker grows unboundedly with per-run registries. Mitigation: registry shutdown hook unregisters; entries are tiny; closed managers hold no jobs.
- Risk: flaky `TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly` (pre-existing on main) interferes with package test runs. Mitigation: re-run targeted tests; report exact failing command if it persists; do not fix it here.

## Documentation Contract (slice 2)

- Feature status: `in implementation`
- Public docs affected: none (server-internal; TUI docs land with slice 3).
- Implementation notes after code: engineering log entry.

## Slice 1 detail (kept for reference)

### Context

- Problem: background work (subagents, cron jobs, delayed callbacks) is only visible through separate surfaces; there is no single endpoint answering "what is running right now?".
- Constraints: slice 1 of epic #814 only — subagents + cron + callbacks.

### Scope

- Unified `Task` DTO + `GET /v1/tasks` handler in `internal/server/http_tasks.go`.
- Route registration in `internal/server/http.go` next to `/v1/subagents` and `/v1/cron/jobs`, requiring `runs:read`.
- `CallbackManager.ListAll()` for cross-conversation enumeration of pending callbacks (`internal/harness/tools/delayed_callback.go`).
- Server wiring: new `CallbackLister` option on `ServerOptions`; pass the daemon's `*tools.CallbackManager` through `cmd/harnessd` (`main.go` → `runtime_container.go` → `bootstrap_helpers.go`).

### Test Plan (TDD) — done

- `internal/harness/tools/delayed_callback_test.go`: `ListAll` across conversations; fired/canceled excluded; empty manager.
- `internal/server/http_tasks_test.go`: empty union → 200 `{"tasks": []}`; per-source typing; tenant filtering; 401/403; nil sources skipped.

### Implementation Checklist (slice 1)

- [x] All items complete; merged via PR #834.
