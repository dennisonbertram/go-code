# Plan: epic(config) #815 Slice 1 — classify hot-swappable vs restart-only fields with reload diff

## Context

- Problem: `harnessd` loads `config.Config` once at startup; any edit requires a full restart. Epic #815 adds live reload (`/reload`, `POST /v1/config/reload`, `SIGHUP`). Before anything can be applied at runtime, the repo needs one authoritative classification of which `Config` fields may change at runtime and a pure diff function both the server endpoint and the TUI will consume.
- User impact: operators get correct, explicit "applied" vs "requires restart" reporting instead of silently ignored config edits.
- Constraints: no behavior change to `Load`, `Defaults`, or `Resolve`; no new third-party dependencies; strict TDD per `docs/runbooks/testing.md`. This slice is pure config-package work — later slices (runner swap, HTTP endpoint, SIGHUP, TUI) are out of scope here.

## Scope

- In scope:
  - New `internal/config/reload.go`: table-driven field classification (dotted TOML paths → hot-swappable | restart-only), `ReloadDiff(old, new Config) ReloadReport`, `ReloadReport` helpers, exported read-only access to the classification table.
  - Package doc comment in `internal/config/config.go` gains a short "Reload classification" section pointing at the table.
  - New `internal/config/reload_test.go` with behavior tests (TDD first).
- Out of scope: applying the diff (Slice 2+), HTTP/SIGHUP/TUI triggers, docs runbook matrix (Slice 6), file-watcher auto reload.

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: none (no operator-facing surface yet — anti-ghost-feature rule)
- Spec docs to update before code: this plan
- Implementation notes to add after code: engineering-log entry per `docs/logs/engineering-log.md` convention

## Test Plan (TDD)

- New failing tests to add first (`internal/config/reload_test.go`):
  - model-only change reported as hot-swappable (`Applied=["model"]`, no restart)
  - `addr` change reported as restart-only
  - `memory.db_driver` restart-only while `memory.enabled` is hot-swappable
  - identical configs produce an empty report
  - mixed changes populate both lists in deterministic (table) order
  - `mcp_servers` map change reported as restart-only
  - slice-valued field change (`hooks.dirs`) detected
  - exhaustiveness: every leaf field of `Config` (via reflection) is present in the classification table
- Existing tests to update: none
- Regression tests required: none (new capability, no bug)

## Cross-Surface Impact Map

- Config: new `reload.go` + package doc paragraph; no change to load/merge semantics.
- Server API: None — Slice 3 wires the endpoint; this slice only provides the pure function it will call.
- TUI state: None — Slice 5 consumes `ReloadReport` via the server response.
- Regression tests: `go test ./internal/config/...` must stay green.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Write failing tests first, watch them fail (compile error = red).
- [x] Implement `internal/config/reload.go` minimally.
- [x] Extend package doc comment with the reload classification summary.
- [x] `gofmt`, `go vet`, `go test ./internal/config/... -count=1` green.
- [x] Run `./scripts/test-regression.sh` if time permits.
- [x] Engineering-log entry; update folder indexes if docs touched.
- [ ] Commit, push `epic/815-config-reload`, open PR referencing #815. Do NOT merge.

## Risks and Mitigations

- Risk: classification drifts from `Config` as fields are added later.
- Mitigation: reflection-based exhaustiveness test fails any PR that adds a `Config` field without classifying it.
- Risk: misclassifying a startup-wired field as hot-swappable.
- Mitigation: classification grounded in `cmd/harnessd/main.go` consumption (persistence handles `memory.db_driver`/`db_dsn`/`sqlite_path`, listen `addr`, and `mcp_servers` process wiring are restart-only; everything flowing into per-run `RunnerConfig` or runtime policy knobs is hot-swappable).

---

# Plan: epic(config) #815 Slice 2 — allow Runner to apply reloaded config to new runs

## Context

- Problem: `Runner` stores `RunnerConfig` by value at construction (`runner.go:252`); every read is an unsynchronized field access (~198 sites across `runner.go`, `runner_step_engine.go`, `runner_event_journal.go`, `plan_mode.go`, `permission_rules.go`). A runtime config swap without synchronization is a data race, and naive global swap would change in-flight runs mid-flight.
- User impact: after Slice 3 wires reload, edits to model / max_steps / auto_compact / forensics take effect for new runs only; in-flight runs are undisturbed; the daemon stays `-race` clean.
- Constraints: `NewRunner` signature unchanged; no behavior change when `ApplyConfig` is never called; strict TDD; `-race` green. Cost ceiling (`RunRequest.MaxCostUSD`) is per-request, not `RunnerConfig` — wired in Slice 3, out of scope here.

## Scope

- In scope:
  - `Runner.ApplyConfig(RunnerConfig)`: atomic config swap guarded by a new `configMu` (defaults normalized via the same block `NewRunner` uses, factored into a shared helper).
  - Per-run snapshot semantics: `runState` gains an immutable `*RunnerConfig` captured at run creation (`StartRun`, `ContinueRunWithOptions`); nil-fallback for the ~19 test-only direct `runState` constructions preserves current behavior.
  - Read-path conversion: every `r.config.X` read moves to a per-function snapshot — `rc := r.configForRun(runID)` when a run context exists, else `rc := r.snapshotConfig()` — so no unsynchronized read of a swappable struct remains.
  - Lock discipline: `configMu` is leaf-level (never held while acquiring `r.mu`); documented on the field.
- Out of scope: server endpoint / SIGHUP / TUI triggers (Slices 3–5), applying the Slice 1 `ReloadDiff` report, per-field (partial) merges — `ApplyConfig` takes a whole `RunnerConfig` rebuilt by the caller.

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: none (no operator-facing surface yet)
- Spec docs to update before code: this plan
- Implementation notes to add after code: engineering-log entry

## Test Plan (TDD)

- New failing tests first (`internal/harness/runner_apply_config_test.go`):
  - run started after `ApplyConfig` observes new `DefaultModel` (provider records `CompletionRequest.Model`) and new `MaxSteps` (run terminates at the new limit)
  - in-flight run keeps its snapshot: model resolved before apply is used for all later steps; `AutoCompactEnabled` flipped on mid-run does NOT emit `auto_compact.started` for the in-flight run, while a post-apply run does
  - concurrent `ApplyConfig` + `StartRun`/execute loop is race-free under `-race`
  - `ApplyConfig` normalizes defaults the same way `NewRunner` does (e.g. zero `AutoCompactThreshold` → 0.80)
- Existing tests: unchanged (nil-snapshot fallback in `configForRun` keeps direct `runState` constructions valid); whole `internal/harness` suite is the characterization net for "no behavior change when ApplyConfig is never called".
- Regression tests required: the race test doubles as the permanent regression for the swap path.

## Cross-Surface Impact Map

- Config: none (Slice 1 artifact consumed by Slice 3, not here).
- Server API: none — Slice 3 will call `ApplyConfig`.
- TUI state: none.
- Regression tests: `go test -race ./internal/harness/...` must stay green; full `test-regression.sh` before PR.

## Implementation Checklist

- [x] Design the seam (guarded swap + per-run snapshot in `runState`).
- [x] Write failing tests first, watch them fail (`undefined: ApplyConfig`).
- [x] Factor `NewRunner` defaulting into shared normalize helper; add `ApplyConfig`/`snapshotConfig`/`configForRun`.
- [x] Capture snapshot at `StartRun` / `ContinueRunWithOptions`; convert read sites per-function.
- [x] `gofmt`, `go vet`, `go test -race ./internal/harness/... -count=1` green.
- [x] Run `./scripts/test-regression.sh`.
- [x] Engineering-log entry.
- [ ] Commit, push `epic/815-config-reload-s2`, open PR referencing #815. Do NOT merge.

## Risks and Mitigations

- Risk: a missed `r.config` read site stays unsynchronized and races under reload.
- Mitigation: convert every non-test `r.config.` occurrence in the five files (verified by grep reaching zero); race test hammers apply+start concurrently.
- Risk: lock-order deadlock between `configMu` and `r.mu`.
- Mitigation: `configMu` is leaf-level — `ApplyConfig`/`snapshotConfig` never touch `r.mu`; `configForRun` acquires `r.mu.RLock` and releases before `configMu.RLock` (sequential, never nested in opposite order).
- Risk: mixed-config tearing inside one function (field A from old, field B from new).
- Mitigation: one snapshot local per function, captured at function entry.

---

# Plan: epic(config) #815 Slice 3 — POST /v1/config/reload endpoint

## Context

- Problem: Slices 1–2 provided the classification (`config.ReloadDiff`) and the runner swap (`Runner.ApplyConfig`), but nothing triggers a reload. This slice exposes it over HTTP and wires it through `harnessd`.
- User impact: an operator edits `~/.harness/config.toml` or `.harness/config.toml`, calls `POST /v1/config/reload` with an admin token, and hot-swappable fields (model, max_steps, auto-compact, forensics, hooks, conclusion watcher) take effect for subsequent runs; restart-only diffs (addr, memory db_*, mcp_servers) come back as warnings; invalid TOML yields 400 with the parse error and the last-known-good config stays active.
- Constraints: admin scope per the `PUT /v1/providers/{name}/key` precedent; strict TDD; no behavior change when the callback is not wired (501, the repo's optional-feature convention).

## Scope

- In scope:
  - `internal/server/http_config.go`: `ConfigReloadFunc`, `ServerOptions.ConfigReload`, route `POST /v1/config/reload` (admin scope) in `buildMux`, handler (501 unwired / 400 on reload error / 200 with `{applied, restart_required}`).
  - `cmd/harnessd/config_reload.go`: `configReloader` (mutex-serialized) that re-runs the startup load sequence, diffs via `config.ReloadDiff` against last-known-good, reassembles the full `RunnerConfig`, and calls `ApplyConfig`.
  - Extraction shared by startup and reload (behavior-identical): `loadHarnessConfig` (Load → Resolve → applyProfileDefaults → MaxSteps 0→8 rule) and `assembleRunnerConfig` (buildRunnerConfig + ProfileRunStore + S3 uploader + conclusion watcher + config-driven hooks + trusted plugin hooks). Extraction is required, not cosmetic: `ApplyConfig` replaces hook slices wholesale, so a reload via bare `buildRunnerConfig` would silently wipe compiled-in/config/plugin hooks and the conclusion watcher.
  - Wiring: `httpRuntimeOptions.configReloader` → `buildHTTPRuntime` (binds the created runner, `subagentRunnerHandoff` precedent) → `serverBootstrapOptions` → `ServerOptions.ConfigReload`.
- Out of scope: SIGHUP (slice 4), TUI `/reload` (slice 5), runbook matrix (slice 6), auto file-watching. Memory-manager-resident knobs (`memory.*` LLM/thresholds) live in the `om.Manager` built once at startup and are NOT rebuilt by this slice (documented limitation; the RunnerConfig swap covers what `buildRunnerConfig` maps). `GET /v1/hooks` keeps serving the startup-computed summary.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none yet (runbook lands in slice 6)
- Spec docs to update before code: this plan
- Implementation notes to add after code: engineering-log entry

## Test Plan (TDD)

- New failing tests first:
  - `internal/server/http_config_test.go`: model edit in temp config file → 200 + `applied:["model"]` + a subsequent run uses the new model; invalid TOML → 400 with parse error text + runner keeps old model; `addr` edit → 200 + `restart_required:["addr"]` + empty applied; unwired server → 501; GET → 405.
  - `internal/server/auth_scope_test.go`: extend with `/v1/config/reload` — read_only and write tokens → 403 (`insufficient_scope`), admin token → 200 (stub callback).
  - `cmd/harnessd/config_reload_test.go`: reloader applies new model to subsequent runs; invalid TOML → error + current config unchanged; `addr` change → restart-only report; hook slices + conclusion watcher survive a reload (assembly fidelity); concurrent reloads serialize.
- Existing tests to update: none (startup extraction must keep `cmd/harnessd` tests green unchanged).
- Regression tests required: the 400-retains-config test doubles as the permanent regression for last-known-good semantics.

## Cross-Surface Impact Map

- Config: reuses Slice 1 `ReloadDiff`; load sequence factored, semantics identical.
- Server API: new admin route `POST /v1/config/reload`; scope table extended.
- TUI state: none (slice 5 consumes the endpoint).
- Regression tests: `go test ./internal/server/... ./cmd/harnessd/... -count=1` green; full `test-regression.sh` before PR.

## Implementation Checklist

- [x] Write failing tests first (undefined route/callback = red).
- [x] `internal/server/http_config.go` endpoint + `ServerOptions` wiring.
- [x] Extract `loadHarnessConfig`/`assembleRunnerConfig`; startup uses them (no behavior change).
- [x] `configReloader` + wiring through `buildHTTPRuntime`/`buildServerOptions`.
- [x] `gofmt`, `go vet`, package tests green.
- [x] `./scripts/test-regression.sh`.
- [x] Engineering-log entry; plan checklist.
- [ ] Commit, push `epic/815-config-reload-s3`, open PR referencing #815. Do NOT merge.

## Risks and Mitigations

- Risk: reload wipes startup-registered hooks/plugins (ApplyConfig replaces slices).
- Mitigation: single `assembleRunnerConfig` used by startup AND reload; harnessd test asserts hook slice survival across reload.
- Risk: concurrent reloads interleave (diff base torn).
- Mitigation: `configReloader.mu` serializes the whole load→diff→apply→commit sequence.
- Risk: reload diverges from startup's effective-config resolution (MaxSteps 0→8, profile defaults).
- Mitigation: single `loadHarnessConfig` used by both paths.

---

# Plan: epic(config) #815 Slice 4 — SIGHUP triggers config reload

## Context

- Problem: slice 3 exposed reload only over HTTP. Operators expect the classic `kill -HUP <pid>` path. Today `harnessd` registers only SIGINT/SIGTERM (`main.go:200`); SIGHUP would terminate the process (Go default) or, worse, once registered naively would be treated as shutdown by both the HTTP path and the MCP stdio path.
- User impact: `kill -HUP <harnessd pid>` after a config edit logs `config reloaded (SIGHUP): applied=[...] restart_required=[...]` and the daemon keeps serving; reload errors are logged, never fatal; repeated SIGHUPs reload again.
- Constraints: SIGINT/SIGTERM shutdown behavior byte-identical; MCP stdio mode (`--mcp`) must NOT gain SIGHUP handling (its signal goroutine cancels on any received signal — registering SIGHUP there would make a hangup kill the stdio server). Strict TDD.

## Scope

- In scope:
  - `cmd/harnessd/main.go`: register `syscall.SIGHUP` on the HTTP-server signal path only (MCP stdio registration unchanged); replace the one-shot shutdown `select` with a loop that dispatches SIGHUP to the slice-3 `configReloader.reload` and everything else to shutdown.
  - Extraction for testability: `awaitServer(sig, serverErr, reloadFn) error` — the signal loop as a pure-ish unit (channels + injected reload func).
- Out of scope: TUI `/reload` (slice 5), runbook (slice 6), file-watcher auto reload.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none yet (runbook lands in slice 6)
- Spec docs to update before code: this plan
- Implementation notes to add after code: engineering-log entry

## Test Plan (TDD)

- New failing tests first (`cmd/harnessd/sighup_test.go`):
  - SIGHUP invokes the injected reload func exactly once, then keeps waiting; SIGTERM after returns nil
  - reload error is logged not fatal: first SIGHUP (failing) → still alive, second SIGHUP invokes reload again (repeat reload works)
  - server error on `serverErr` returns that error immediately
  - SIGINT and SIGTERM return nil promptly without invoking reload (shutdown unchanged)
  - nil reloadFn + SIGHUP does not kill the wait loop (defensive)
- Existing tests to update: none
- Regression tests required: the shutdown-unchanged test is the permanent guard for SIGINT/SIGTERM semantics.

## Cross-Surface Impact Map

- Config: none (reuses slices 1–3).
- Server API: none.
- TUI state: none.
- Regression tests: `go test ./cmd/harnessd/... ./internal/config/... -count=1` green; full `test-regression.sh` before PR.

## Implementation Checklist

- [x] Write failing tests first (`undefined: awaitServer` = red).
- [x] Implement `awaitServer` loop + SIGHUP registration (HTTP path only).
- [x] `gofmt`, `go vet`, package tests green.
- [x] `./scripts/test-regression.sh`.
- [x] Engineering-log entry; plan checklist.
- [ ] Commit, push `epic/815-config-reload-s4`, open PR referencing #815. Do NOT merge.

## Risks and Mitigations

- Risk: SIGHUP registration leaks into MCP stdio mode and turns hangups into shutdowns.
- Mitigation: SIGHUP registered only on the non-MCP path in `run()`; MCP registration untouched.
- Risk: a reload panic/error takes the daemon down.
- Mitigation: reload runs synchronously inside the loop with error logged not returned; `configReloader.reload` is mutex-serialized and panic-free by construction (load errors are values); shutdown path unchanged.
