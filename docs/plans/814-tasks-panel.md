# Plan: unified /tasks background panel (epic #814)

## Slice 1 (merged, PR #834): GET /v1/tasks union endpoint for subagents, cron, callbacks

- `internal/server/http_tasks.go`: unified `Task` DTO + `GET /v1/tasks` (runs:read) unioning subagents, cron jobs, pending callbacks.
- `CallbackManager.ListAll` for cross-conversation pending-callback enumeration; daemon wired via `ServerOptions.CallbackLister`.

## Slice 2 (this branch, epic/814-tasks-panel-s2): expose background bash jobs to the task union

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
