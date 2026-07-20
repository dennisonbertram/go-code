# Plan: harnesscli service OS-service commands (epic #807, slices 1–2)

## Context

- Problem: end users can only keep `harnessd` alive via detached tmux; there is
  no OS-service install. Epic #807 adds `harnesscli service
  install|uninstall|start|stop|status` matching kimi-code's `kimi server
  install`.
- User impact: one-command persistent install of `harnessd` as a user-level
  launchd agent (macOS) or systemd `--user` unit (Linux), with log capture and
  crash restart, plus lifecycle management without hand-typed
  `launchctl`/`systemctl`.
- Constraints: stdlib only (no plist/systemd libs); user-level services only
  (no root/system units); tmux remains the dev-agent rule. Slice 1 (merged,
  PR #826) shipped install/uninstall + generators; this branch adds slice 2:
  start/stop/status lifecycle.

## Scope

- In scope (slice 2, this branch):
  - `start`: darwin `launchctl bootstrap gui/<uid> <plist>` with
    `kickstart -k gui/<uid>/com.gocode.harnessd` fallback when already loaded;
    linux `systemctl --user start harnessd.service`
  - `stop`: darwin `launchctl bootout gui/<uid>/com.gocode.harnessd`; linux
    `systemctl --user stop harnessd.service`
  - `status`: installed check (unit file present), loaded/active query
    (`launchctl print` / `systemctl --user is-active`), HTTP health probe of
    `<--base-url>/healthz` (flag default `http://localhost:8080`, mirroring
    `cmd/harnesscli/main.go:144`)
  - Clear "not installed" error (non-zero exit) for all lifecycle commands
    run before `install`
  - Real runner made quiet-on-success (buffered output shown only on failure)
    so `launchctl print` doesn't spam the terminal
- Delivered (slice 1, merged): `runService` dispatch, `case "service":` in
  `dispatch()`, pure generators, `--binary`/`--addr`/`--log-dir` resolution,
  `--dry-run`, `uninstall` with best-effort bootout/disable.
- Out of scope: docs (slice 3), Windows, system-wide units, daemon changes.

## Documentation Contract

- Feature status: slice 1 `implemented` (merged PR #826); slice 2
  `in implementation`
- Public docs affected: none in this slice (slice 3 owns
  `docs/runbooks/distribution.md` + `README.md`)
- Spec docs to update before code: this file (done)
- Implementation notes to add after code: none (slice 3)

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/service_test.go`):
  - Fake-runner exact argument construction per platform and verb:
    bootstrap/kickstart/bootout/print (darwin, exact `gui/<uid>[/<label>]`
    domains), start/stop/is-active (linux)
  - start: bootstrap success; already-loaded → kickstart -k restart; both
    fail → non-zero + stderr; not installed → non-zero + clear message
  - stop: success per platform; runner error → non-zero + stderr; not
    installed → non-zero
  - status table: not-installed (non-zero); installed-not-loaded (exit 0,
    "not running"); loaded-and-healthy (httptest 200 on /healthz);
    loaded-but-unreachable (closed port → "unreachable", exit 0)
  - stub test from slice 1 removed (stubs replaced by real commands)
- Existing tests to update: `setupServiceTest` helper now returns a
  `serviceRunnerFake` with per-call error queue; slice-1 runner assertions
  updated accordingly (same assertions, new accessor)
- Regression tests required: none beyond the above

## Cross-Surface Impact Map

- Not a provider/model flow change — not required.

## Implementation Checklist

- [x] Slice 1: install/uninstall + generators, merged via PR #826.
- [x] Update this plan for slice 2 before code.
- [x] Write failing slice-2 tests first; watch them fail (16 red against stubs).
- [x] Implement start/stop/status + quiet-on-success runner (rich wrapped
      errors on failure).
- [x] `go test ./cmd/harnesscli/ -run Service` green (32 tests).
- [x] `go test ./cmd/harnesscli/... -count=1` full package green; gofmt +
      go vet clean.
- [x] macOS acceptance: install → start → status (running; healthy once the
      daemon bound) → existing `harnesscli status --base-url ... <run-id>`
      reached the daemon → stop → status (installed, not running, silent
      stderr) → restart path → uninstall; `launchctl print` confirms the job
      is gone.
- [ ] Commit, push `epic/807-service-install-s2`, open PR (no merge).

## Risks and Mitigations

- Risk: platform-specific code paths untestable on darwin dev machine.
  Mitigation: injectable `servicePlatform` var; linux paths fully exercised
  in unit tests.
- Risk: unit tests invoking real launchctl/systemctl.
  Mitigation: injectable `serviceRunLifecycle` runner var; tests substitute a
  recording fake with an error queue.
- Risk: `status` query verbs (`launchctl print`, `systemctl is-active`) exit
  non-zero for the normal "not running" state.
  Mitigation: status treats query error as the not-running state (never a
  hard failure); only missing unit file fails the command.
