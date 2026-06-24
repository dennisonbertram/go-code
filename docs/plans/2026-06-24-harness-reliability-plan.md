# Plan: Harness Long-Session Reliability Hardening

## Context

- Problem: A code audit (2026-06-24) identified ~30 reliability bugs across the runner, server, workspace, and tools layers that would manifest during long-running, complicated coding agent sessions in `harnessd` daemon mode. Symptoms the audit is designed to eliminate: silent memory growth, dropped terminal events, orphaned containers/VMs/MCP subprocesses, silent empty-output run completion, an unrecoverable dispatcher, a broken audit hash chain, and an unauthenticated cron surface.
- User impact: a long-lived daemon stops taking work, leaks disk/cloud spend, drops the terminal SSE frame to slow clients, completes runs with empty output, or fails open on cron tenant isolation. Each failure mode is silent; current tests do not catch them because they live on concurrency/long-time-window paths.
- Constraints:
  - Strict TDD per `docs/runbooks/testing.md` — every bug fix needs a failing regression test written first.
  - Worktree-flow per `docs/runbooks/worktree-flow.md` — each slice is its own worktree branch, merged via `verify-and-merge.sh`.
  - Issue-triage per `docs/runbooks/issue-triage.md` — each bug has a GitHub issue, engineering-log entry, and regression test.
  - Zero tolerance for breaking the accepted baseline (`go test ./...` and `./scripts/test-regression.sh` must stay green).

## Scope

- In scope: 15 ordered TDD slices addressing the critical + high severity findings from the 2026-06-24 audit. Each slice is a self-contained red-green-refactor loop with its own failing test, fix, and regression coverage.
- Out of scope (deferred to follow-up plan): the MEDIUM/LOW findings (bash `output --wait` busy-poll, broker shutdown drain, parsePositiveInt overflow, columnExists swallowing SQLITE_BUSY, etc.). They are captured in `## Deferred` and will be triaged after the critical path is green.

## Documentation Contract

- Feature status: `planned` → moves to `in implementation` as each slice's worktree branch is created, and `implemented` after the slice's PR merges to `main`.
- Public docs affected: none (pure reliability fixes; no API or user-facing behavior changes). If any slice changes a documented failure-mode contract, it updates the relevant runbook inline.
- Spec docs to update before code: this plan + per-slice engineering-log entry.
- Implementation notes to add after code: per-slice engineering-log entry under the merge commit, plus a summary entry at the end of plan completion.

## Test Plan (TDD)

Per-slice test plans are embedded in the slice definitions below. Cross-cutting requirements:

- New failing tests added first: one regression test per slice in the appropriate `*_test.go` file, demonstrating the bug before the fix lands.
- Existing tests to update: any test that asserted the *broken* behavior (e.g. asserting empty-output completion returns `RunStatusCompleted`) must be retargeted to assert the corrected behavior — and the test that asserted the broken behavior must be deleted, not left to rot.
- Regression tests required: every slice ships a deterministic test (no real network, no real Docker except where an integration test already exists), runnable under `go test ./... -race`.
- Each slice's regression test must compile and fail on `main` before the fix is implemented.

## Cross-Surface Impact Map

This plan does not touch provider/model flows, gateway routing, model catalogs, API-key management, or TUI provider plumbing. Per `docs/runbooks/provider-model-impact-mapping.md` this map is not required for the reliability fixes themselves.

Per-slice impact is small and tracked at the slice level. The slices that touch public-server behavior (T09 cron, T10 server hardening, T11 replay) need server-API coverage updates and an inline note in their slices once they reach `in implementation`.

## Slice Ordering (recommended fix order)

The ordering is by **impact × likelihood-of-manifesting-in-a-long-session**, not by severity tag in isolation. Each slice is independent enough to ship on its own branch but assumes earlier ones are merged.

| ID  | Finding (audit ref) | Files touched |
|-----|---|---|
| T01 | In-memory `r.runs` / `r.conversations` / `state.events` grow unbounded (#1) | `internal/harness/runner.go`, `runner_event_journal.go`, `conversation_store.go` |
| T02 | `r.mu` held across `Store.AppendEvent` for terminal events (#2) | `internal/harness/runner.go`, `runner_event_journal.go` |
| T03 | Empty-response retry exhaustion silently completes with empty output (#5) | `internal/harness/runner_step_engine.go` |
| T04 | Background bash jobs outlive the run; no `JobManager.Shutdown` (#4) | `internal/harness/tools/bash_manager.go`, `tools_default.go`, `runner.go` |
| T05 | Scoped MCP subprocesses not closed if run is wedged at shutdown (#16) | `internal/harness/runner.go`, `internal/mcp/mcp.go` |
| T06 | Audit hash chain broken under concurrent writers (#3) | `internal/harness/runner.go`, `internal/forensics/audittrail/writer.go` |
| T07 | `poolDispatcher` has no `recover()`; one panic kills dispatch forever (#6) | `internal/harness/runner.go` |
| T08 | Container `Provision`/`Destroy` leaks container, port, dir (#7) | `internal/workspace/container.go` |
| T09 | Cloud VM orphaned on any post-`Server.Create` error (#8) | `internal/workspace/hetzner.go`, `vm.go` |
| T10 | Worktree ops unsynchronized; `git worktree prune` never invoked (#9) | `internal/workspace/worktree.go`, `pool.go` |
| T11 | Bash streaming deadlocks on lines > 1 MiB (#10) | `internal/harness/tools/bash_manager.go` |
| T12 | Cron API has zero tenant isolation (#11) | `internal/server/http_cron.go`, `internal/harness/tools/cron.go`, `internal/cron/*` |
| T13 | `harnessd` `http.Server` only sets `ReadHeaderTimeout`; no `MaxBytesReader` anywhere (#12 + #14) | `cmd/harnessd/runtime_container.go`, `internal/server/auth.go` |
| T14 | `POST /v1/runs/replay?detect_drift=true` spawns unbounded `Runner`s (#13) | `internal/server/http_replay.go` |
| T15 | `Registry.ReplaceByTag` desyncs `mcpServerTools`; MCP stuck "already connected" (#15) | `internal/harness/registry.go` |

## Slice Definitions

### T01 — Prune completed runs + conversations from in-memory maps

**Type:** bugfix
**Allowed files (test):** `internal/harness/runner_prune_test.go` (new), `internal/harness/conversation_store_test.go` (extend)
**Allowed files (impl):** `internal/harness/runner.go`, `internal/harness/conversation_store.go`
**Forbidden:** any package outside `internal/harness/`
**Dependencies:** none
**Complexity:** medium
**Risk:** medium (touches the central lifecycle path; must not free state while subscribers or streaming callers still reference it)

#### Failing test (sketch)
```go
// TestRunner_PruneCompletedRunsFromMemory asserts that after a run reaches a
// terminal status AND its terminal record is persisted AND any SSE
// subscribers have drained, the run's *runState is removed from r.runs so a
// long-lived daemon does not accumulate state.events and message slices
// unbounded.
```
The test starts 100 short runs in a `NewRunner` with a fake provider, waits for terminal, ensures no subscriber is attached, then asserts `len(runner.runs)` shrinks back toward a small bounded value (e.g. ≤ `runnerMaxCompletedRetention`). Second test: a run with an attached subscriber at terminal time stays in the map until the subscriber's cancel returns. Third: `r.conversations` mirror evicts older-than-N entries even when the SQLite store keeps them.

#### Implementation
- Add `RunnerConfig.MaxCompletedRetention` (default 32) and `RunnerConfig.MaxConversationRetention` (default 256).
- After every terminal store-append success, schedule a background `pruneCompletedRunsLocked` that drops the oldest terminal entries past the cap, *only after* their `state.subscribers` map is empty and the store terminal record has been confirmed written.
- Mirror in `r.conversations` and `r.conversationOwners`: keep the most-recently-touched N, evict oldest on insert.

#### Acceptance
- `go test ./internal/harness/... -run TestRunner_Prune -race` green
- `go test ./internal/harness/... -race` green
- `./scripts/test-regression.sh` green

---

### T02 — Defer terminal store-append + subscriber fanout outside `r.mu`

**Type:** bugfix
**Allowed files (test):** `internal/harness/runner_terminal_store_test.go` (new)
**Allowed files (impl):** `internal/harness/runner.go`, `internal/harness/runner_event_journal.go`
**Forbidden:** store implementation files
**Dependencies:** T01 (so the prune path runs in the same window and does not race the deferred store write)
**Complexity:** medium
**Risk:** medium (must preserve event ordering: terminal event must be appended to the store and fanned out *before* any later `GetRun`/`Subscribe` can observe `state.terminated == true`)

#### Failing test
Assert via a stub store that emits a per-call goroutine-blocking condition variable: in `main`, with the bug, terminal `AppendEvent` is called while `r.mu` is held (detectable because a concurrent `GetRun` blocks longer than the store append). After the fix, `AppendEvent` is called after `r.mu.Unlock()`, and the test fakes a 2 s store stall and asserts `GetRun` for a *different* run returns within 50 ms.

#### Implementation
- In `publishTerminalLocked`, only mutate `state.events`, `state.nextEventSeq`, `state.terminated = true`, and snapshot the subscriber list + event payload while holding `r.mu`.
- After `r.mu.Unlock()`, call `storeAppendEvent` with a `context.WithTimeout(5*time.Second)`-bounded ctx, then fan out the event to subscribers (non-blocking send with short drop-after-200ms for terminal events — separate slice).
- Pass store calls everywhere else through the bounded ctx helper (`storeAppendEventBounded`).

---

### T03 — Fail the run when empty-response retries are exhausted; don't burn step budget on retries

**Type:** bugfix
**Allowed files (test):** `internal/harness/runner_empty_response_test.go` (extend)
**Allowed files (impl):** `internal/harness/runner_step_engine.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing tests
1. After `maxEmptyRetries` consecutive empty responses, the run status is `RunStatusFailed` with reason `"max_empty_responses"` — *not* `RunStatusCompleted`. Today it asserts `RunStatusCompleted` (the bug); flip the assertion.
2. Three consecutive empty responses consume step budget of 1, not 4 — prove retries run on a nested loop, not the outer `step++`.

#### Implementation
- Replace the bare `continue` on the empty-response branch with an inner `for` retry loop that does not advance `step`.
- When `consecutiveEmptyResponses >= maxEmptyRetries`, call `r.failRun(runID, errors.New("runner: max consecutive empty responses reached"))` and `return`.

---

### T04 — `JobManager.Shutdown` + run-ctx-bound background bash

**Type:** bugfix
**Allowed files (test):** `internal/harness/tools/bash_manager_test.go` (extend)
**Allowed files (impl):** `internal/harness/tools/bash_manager.go`, `internal/harness/tools_default.go`, `internal/harness/runner.go` (call site only)
**Dependencies:** T05 (shutdown iteration pattern is shared)
**Complexity:** medium
**Risk:** medium (kill ordering on shutdown)

#### Failing tests
1. Start a background job with a 60 s sleep, cancel the run ctx, assert the underlying process is killed within 1 s (today the job runs until the 60 s timeout).
2. Start background jobs, then call `JobManager.Shutdown(ctx)`; assert all `cmd.Wait` goroutines return and the jobs map is empty.
3. Call `Runner.Shutdown` with pending background jobs; assert `JobManager.Shutdown` was invoked.

#### Implementation
- Add `JobManager.Shutdown(ctx)` that cancels every job, sends SIGKILL, waits on a `WaitGroup`-tracked set of `cmd.Wait` goroutines, clears `m.jobs`.
- In `runBackground`, replace `context.Background()` with a child of the run ctx (so cancelling the run kills jobs).
- In `Runner.Shutdown`, call `r.jobManager.Shutdown(shutdownCtx)` after the run ctxs are cancelled.

---

### T05 — `Runner.Shutdown` iterates `r.runs` and closes scoped MCP registries

**Type:** bugfix
**Allowed files (test):** `internal/harness/runner_shutdown_mcp_test.go` (new)
**Allowed files (impl):** `internal/harness/runner.go`, `internal/harness/scoped_mcp.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low (idempotent `Close`)

#### Failing test
Stub a wedged scoped MCP registry (its `ClientManager.Close` blocks until called). Start a run, wedge its execute goroutine by injecting a tool that blocks on a channel. Call `Runner.Shutdown`. Assert `ClientManager.Close` was called and the underlying MCP subprocess (a fake `os/exec` recording KILL via `cmd.Process.Kill`) was signalled.

#### Implementation
- `closeScopedMCP` is already idempotent — set `state.scopedMCPRegistry = nil` at the end of the call so re-entry is a no-op.
- In `Shutdown`, snapshot `state.scopedMCPRegistry` for every live `*runState`, then `_ = reg.Close(shutdownCtx)` after cancelling `cancelFuncs`. Add `defer r.closeScopedMCP(runID)` inside `execute()` so a panic still tears down MCP.

---

### T06 — One shared `*AuditWriter` per date bucket, guarded by a mutex

**Type:** bugfix
**Allowed files (test):** `internal/forensics/audittrail/writer_chain_test.go` (new)
**Allowed files (impl):** `internal/forensics/audittrail/writer.go`, `internal/harness/runner.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing test
Two goroutines each running a fake run that completes on the same UTC day append audit entries through a shared writer. Then read the file back, parse each line's `prev_hash` and `entry_hash`, and assert the chain is unbroken (each `prev_hash` equals the previous `entry_hash`). Today, with per-run writers, the chain breaks.

#### Implementation
- Hoist the per-run `*AuditWriter` to a `Runner.auditBuckets` `map[string]*auditBucket` (`auditBucket = {mu sync.Mutex; *AuditWriter}`). Keyed by UTC date (`2006-01-02`).
- `auditBucketFor(day)` lazily creates a bucket for the day. StartRun grabs it once; terminal-cleanup does not close it (only the day flip / Shutdown closes stale buckets).
- `AuditWriter` itself gains a `Write(entry)` that takes the bucket mutex so the chain state is consistent.

---

### T07 — `poolDispatcher` recover() keeps dispatch alive after one bad item

**Type:** bugfix
**Allowed files (test):** `internal/harness/runner_panic_test.go` (extend)
**Allowed files (impl):** `internal/harness/runner.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing test
Inject a queue item that panics when the dispatcher processes it (a test hook that swaps a func field). Then enqueue two more items behind it. Assert the dispatcher logs the panic, does not leak a `r.inflight` token, and the two later items still execute. Today, after the panic the dispatcher goroutine is dead and the two later items hang forever.

#### Implementation
- Wrap the `poolDispatcher` loop body in `defer func() { if p := recover(); p != nil { ... log ...; r.inflight.Done() } }()`.
- Add a small test seam so test can drive the panic branch deterministically.

---

### T08 — Container `Provision` cleans up on error; `Destroy` removes workspace dir

**Type:** bugfix
**Allowed files (test):** `internal/workspace/container_test.go` (extend), `internal/workspace/container_integration_test.go` (extend with `GO_CODE_DOCKER_TESTS=1` guard)
**Allowed files (impl):** `internal/workspace/container.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing tests
1. With a fake docker client whose `ContainerStart` returns an error after `ContainerCreate` succeeded, assert `Provision` returns the error AND `ContainerStop`+`ContainerRemove` are called AND the workspace directory is `os.RemoveAll`'d.
2. After a successful `Provision`, `Destroy` removes both the container and the workspace dir.
3. `Destroy` with an already-cancelled ctx still stops and removes the container (uses `context.WithTimeout(context.Background(), 30s)` internally).

#### Implementation
- Wrap the post-`ContainerCreate` body so any returned error calls `_ = w.Destroy(forceCtx)` (forceCtx = `context.WithTimeout(context.Background(), 30s)`).
- In `Destroy`, `defer os.RemoveAll(w.workspacePath)` after `ContainerRemove` succeeds.
- Use `forceCtx` for stop/remove regardless of caller ctx.
- Add `select { case <-ctx.Done(): return ctx.Err(); case <-time.After(500ms): }` inside the inspect poll loop.

---

### T09 — Hetzner `Create` deletes the server on any post-create error

**Type:** bugfix
**Allowed files (test):** `internal/workspace/hetzner_test.go` (extend), `vm_test.go` (extend)
**Allowed files (impl):** `internal/workspace/hetzner.go`, `internal/workspace/vm.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing test
With a fake Hetzner client whose `GetByID` always errors *after* `Server.Create` returned a server, `HetznerProvider.Create` returns an error AND the fake records a `Server.Delete(server.ID)` call. Today the server lingers.

#### Implementation
- In `HetznerProvider.Create`, after `Server.Create` succeeds, defer-on-error a best-effort `p.client.Server.Delete(forceCtx, server)`.
- In `VMWorkspace.Provision`, set `w.vmID = server.ID` immediately after `Create` succeeds so a caller-initiated `Destroy` can clean up.

---

### T10 — Per-repo worktree mutex + `git worktree prune` on Destroy and Pool.Close

**Type:** bugfix
**Allowed files (test):** `internal/workspace/worktree_test.go` (extend)
**Allowed files (impl):** `internal/workspace/worktree.go`, `internal/workspace/pool.go`
**Dependencies:** none
**Complexity:** medium
**Risk:** medium (git index lock semantics)

#### Failing tests
1. Two goroutines each call `WorktreeWorkspace.Provision` against the same `repoPath` and a fake `exec.CommandContext` that records calls; assert the two `worktree add` commands do not interleave (held under the per-repo mutex).
2. After a partial Destroy (fake `git worktree remove` returns an error), the next `Destroy` still invokes `git worktree prune`.
3. `Pool.Close` invokes `git worktree prune` on each distinct `repoPath` once.

#### Implementation
- Add a package-level `sync.Map` keyed by `repoPath` of `*sync.Mutex`.
- Wrap `worktree add`, `worktree remove`, and `branch -D` in `lockRepo(repoPath) / defer unlock`.
- Invoke `git worktree prune` at the end of every `Destroy` (idempotent, cheap) and once per repoPath in `Pool.Close`.

---

### T11 — Bash streaming uses `bufio.Reader` and never blocks the subprocess on overlong lines

**Type:** bugfix
**Allowed files (test):** `internal/harness/tools/bash_manager_test.go` (extend)
**Allowed files (impl):** `internal/harness/tools/bash_manager.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing test
Stub a subprocess that prints a single 4 MiB line followed by `EOF`. Today the run-loop hangs (eventually times out at 300 s); assert the tool returns promptly with the line truncated to a configurable `MaxLineBytes` and `scanner.Err()` (or its replacement) is reported in the result, not silently swallowed.

#### Implementation
- Replace `bufio.Scanner` with `bufio.Reader.ReadString('\n')`; on `ErrBufferFull`, truncate the accumulated chunk to `MaxLineBytes`, log "line truncated", and continue.
- Always drain `pr` to EOF before `streamDone.Wait()` returns.
- Add `MaxLineBytes` (default 1 MiB) to the bash tool config.

---

### T12 — Cron API gains `TenantID` scoping on every by-id route

**Type:** bugfix (security)
**Allowed files (test):** `internal/server/http_cron_test.go` (extend), `internal/harness/tools/cron_test.go` (extend)
**Allowed files (impl):** `internal/server/http_cron.go`, `internal/harness/tools/cron.go`, `internal/cron/store.go` (if in-tree), `internal/cron/client.go`
**Dependencies:** none
**Complexity:** medium
**Risk:** medium (schema migration to add `TenantID` column; mirror of `http_runs` isolation pattern)

#### Failing tests
1. Tenant A creates a job; tenant B requests `GET /v1/cron/jobs/{A_job_id}` → `404 not_found`.
2. Tenant B requests `DELETE /v1/cron/jobs/{A_job_id}` → `404 not_found`, and the job still exists when A lists their jobs.
3. `ListJobs` for tenant A returns only A's jobs.
4. `GetJob` distinguishing `IsNotFound` (404) from a real store error (500) — see audit finding on `handleCronGetJob`.

#### Implementation
- Add `TenantID` to `CronJob` and the cron store schema (migration ADD COLUMN + backfill tenant_id from `owner` if needed, or empty default for backward compat).
- Stamp `TenantID` from `TenantIDFromContext(r.Context())` on `CreateJob`.
- Filter `ListJobs`/`GetJob`/`Update`/`Delete`/pause/resume by tenant; 404 on mismatch.
- Discriminate `IsNotFound` vs other errors in every by-id handler.

---

### T13 — Server hardening: timeouts + `MaxBytesReader` middleware

**Type:** bugfix (security/DoS)
**Allowed files (test):** `internal/server/http_server_hardening_test.go` (new), `internal/server/http_test.go` (extend)
**Allowed files (impl):** `cmd/harnessd/runtime_container.go`, `internal/server/http.go`, `internal/server/auth.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low (must ensure SSE/streaming handlers are NOT wrapped by `TimeoutHandler`)

#### Failing tests
1. `http.Server` is constructed with non-zero `ReadTimeout`, `IdleTimeout`, `MaxHeaderBytes`; non-streaming handlers are wrapped in `http.TimeoutHandler` (introspect via reflect on the chain or wrap and assert a body larger than the limit triggers `http.MaxBytesError`).
2. Sending a 5 MiB JSON body to `POST /v1/runs` returns 413 (or whichever status `MaxBytesReader` produces) and does not consume more than ~1.1 MiB of memory.
3. An SSE handler that respects `r.Context().Done()` is NOT wrapped by `TimeoutHandler` (introspect or assert a long-lived SSE stream survives a 60 s `TimeoutHandler` boundary).

#### Implementation
- Set `ReadTimeout: 60s`, `IdleTimeout: 120s`, `MaxHeaderBytes: 1 << 20` on the `http.Server`.
- Add an `authAndLimit` middleware that wraps `r.Body` in `http.MaxBytesReader(w, r.Body, maxBodyBytes)` (default 4 MiB for replay, 1 MiB otherwise; per-route override).
- Wrap non-streaming handlers (everything whose handler name does NOT contain `Events`/`Stream`/`Wait`) in `http.TimeoutHandler(h, 30*time.Second, `{"error":{"code":"timeout"}}`)`.
- Streaming handlers keep their own `r.Context().Done()` discipline.

---

### T14 — Replay drift detection gated by a small semaphore

**Type:** bugfix (DoS)
**Allowed files (test):** `internal/server/http_replay_test.go` (extend)
**Allowed files (impl):** `internal/server/http_replay.go`
**Dependencies:** none
**Complexity:** low
**Risk:** low

#### Failing test
Pre-fill the semaphore to its size (e.g. 2) with stub acquisitions that never release, then issue a third `POST /v1/runs/replay?detect_drift=true`. Assert a 503/429 within a few hundred ms; assert no `Runner` was constructed.

#### Implementation
- Add `Server.replayDriftSem chan struct{}` (size 2, configurable via `ServerConfig.ReplayDriftConcurrency`).
- In the drift branch, `select { case s.replayDriftSem <- ctx.Done(): return drain }` then `defer func(){ <-s.replayDriftSem }()`.

---

### T15 — `Registry.ReplaceByTag` rewrites `mcpServerTools`; in-flight gate

**Type:** bugfix
**Allowed files (test):** `internal/harness/registry_mcp_test.go` (extend)
**Allowed files (impl):** `internal/harness/registry.go`
**Dependencies:** none
**Complexity:** medium
**Risk:** medium (concurrent hot-swap of a tool while a call is in flight)

#### Failing tests
1. Register an `mcp_*`-tagged tool via `RegisterMCPTools`, then `ReplaceByTag` with a new handler. Assert `mcpServerTools` no longer contains the old entry's references and contains the new ones (or none, if the new tool isn't MCP-tagged). Then call `UnregisterMCPServer(serverName)`; assert it removes the correct set.
2. Start a slow handler, then `ReplaceByTag` the same name; assert `ReplaceByTag` waits for the in-flight call to return (use a `WaitGroup` or per-tool in-flight counter) before swapping.

#### Implementation
- Per-tool `inflight sync.WaitGroup`: `Execute` `tool.wg.Wait()` is not added (we don't want to block normal execution on hot-swap); instead `ReplaceByTag` calls `tool.swapWg.Wait()` on the *old* tool before deleting it.
- Recompute `r.mcpServerTools` in `ReplaceByTag`: enumerate `r.tools`, rebuild `mcpServerTools[serverName] = set-of-tool-names` from the survivors' tags.

---

## Deferred (follow-up plan after T01..T15 land)

- Bash `output --wait` busy-polls — replace bool with `done chan struct{}` (audit #15).
- Approval/ask-user/checkpoint brokers have no `Shutdown` drain (audit #16).
- Approval broker one-pending-per-run hard-error on re-ask (audit #17).
- MCP stdio scanner drops/aborts on oversized lines (audit #18).
- `state.events` cap to a rolling tail (covered partially in T01 but T01 focuses on cross-run growth; per-run tail cap is a separate slice).
- Subscriber-drop signal to subscriber (audit #20).
- Pool `Get` busy-wait + ready/closing race + lease reaper (audit #8, #9, #14, #21).
- Local/workspace path containment — destructive `RemoveAll` outside root (audit #11).
- `handleConversationsCleanup` swallows decode errors (audit #15 in server audit).
- Shutdown ordering (`cronStore.Close()` before `httpServer.Shutdown`) (audit #7).
- Workflow SSE no keepalive + fragile terminal set (audit #10).
- `parsePositiveInt` overflow / no upper bound on `limit` (audit #16 server).
- `columnExists` swallows DB errors as "doesn't exist" (audit #16 conversation store).
- Silent cost-ceiling race on `accountingTotals` reads (audit #10 runner).

## Implementation Checklist

This plan-level checklist tracks the workflow. Per-slice checklists live in each slice's engineering-log entry and PR description.

- [ ] T01 failing test red on `main`
- [ ] T01 PR merged to `main`
- [ ] T02 ...
- [ ] ... T15 PR merged to `main`
- [ ] Final summary entry in `docs/logs/engineering-log.md`
- [ ] Long-term-thinking-log entry updated with success definition
- [ ] Plan moved to `implemented` in this document's Documentation Contract

## Risks and Mitigations

- Risk: A slice's fix collides with another slice's in-flight change.
  - Mitigation: each slice is its own worktree branch off `main`, merged serially via `verify-and-merge.sh`; the tracker file (`.context/harness-reliability/tracker.md`) records which slice is in-flight to prevent two agents picking the same one.

- Risk: A regression test on a concurrency path flakes (false positives in CI).
  - Mitigation: every regression test must be deterministic — no real Docker/SSH/network, no `time.Sleep` to "wait" for a goroutine (use channels / `wg.Wait` / `require.Eventually` with bounded retries). Each PR's regression suite must pass `-race` locally before merge.

- Risk: A slice's fix changes a behavior an existing test asserted (e.g. "empty-output completion returns RunStatusCompleted"). The fix corrects that behavior; the asserted-bug test must be deleted or retargeted, not left as a flaky green test.
  - Mitigation: per-slice plan section calls out "Existing tests to update" explicitly.

- Risk: The workflow loses context across agent handoffs.
  - Mitigation: tracker file at `.context/harness-reliability/tracker.md` is the single source of truth — every slice update records: state (red/green/merged/blocked), branch name, commit hash, blocker. Subagents read it before starting work and write it after.
