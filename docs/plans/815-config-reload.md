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
