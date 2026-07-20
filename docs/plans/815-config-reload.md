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
