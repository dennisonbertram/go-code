# Plan: harnesscli service install/uninstall (epic #807, slice 1)

## Context

- Problem: end users can only keep `harnessd` alive via detached tmux; there is
  no OS-service install. Epic #807 adds `harnesscli service
  install|uninstall|start|stop|status` matching kimi-code's `kimi server
  install`.
- User impact: one-command persistent install of `harnessd` as a user-level
  launchd agent (macOS) or systemd `--user` unit (Linux), with log capture and
  crash restart.
- Constraints: stdlib only (no plist/systemd libs); user-level services only
  (no root/system units); tmux remains the dev-agent rule; this slice covers
  ONLY install/uninstall + generators — start/stop/status are stubs for
  slice 2.

## Scope

- In scope:
  - `cmd/harnesscli/service.go`: `runService` nested dispatch (install,
    uninstall; start/stop/status stubbed as not-yet-implemented)
  - `case "service":` in `dispatch()` (`cmd/harnesscli/auth.go`)
  - Pure generators `renderLaunchdPlist(opts)` / `renderSystemdUnit(opts)`
  - Resolution: `--binary` or `exec.LookPath("harnessd")`; `--addr` or
    `internal/config` resolution (defaults `:8080`, `HARNESS_ADDR`);
    `--log-dir` default `~/.harness/logs`
  - `--dry-run` prints target path + rendered unit, writes nothing
  - `uninstall` removes unit file + best-effort `launchctl bootout` /
    `systemctl --user disable --now`
- Out of scope: start/stop/status (slice 2), docs (slice 3), Windows,
  system-wide units, daemon changes.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none in this slice (slice 3 owns
  `docs/runbooks/distribution.md` + `README.md`)
- Spec docs to update before code: none
- Implementation notes to add after code: none (slice 3)

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/service_test.go`):
  - Golden-content: plist contains resolved binary path in ProgramArguments,
    `KeepAlive`/`RunAtLoad` true, log paths, `HARNESS_ADDR`; unit contains
    `ExecStart`, `Restart=on-failure`, `WantedBy=default.target`, env addr
  - `--dry-run` writes nothing, prints target path + contents
  - install writes plist (darwin) and unit (linux, injected platform) under
    the temp-HOME paths
  - binary resolution: `--binary` wins; missing harnessd on PATH errors
  - addr resolution: `HARNESS_ADDR` env honored; `--addr` overrides env
  - uninstall when not installed: non-zero + clear message; install→uninstall
    removes file and invokes best-effort bootout/disable via injected runner
  - stubs: start/stop/status report not-yet-implemented, non-zero exit
- Existing tests to update: none
- Regression tests required: none beyond the above (new feature)

## Cross-Surface Impact Map

- Not a provider/model flow change — not required.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first; watch them fail (compile error: runService undefined).
- [x] Implement minimal code changes.
- [x] `go test ./cmd/harnesscli/ -run Service` green (17 tests).
- [x] `go test ./cmd/harnesscli/...` full package green; gofmt + go vet clean.
- [x] macOS acceptance: `--dry-run` prints plist; real install into temp HOME
      passes `plutil -lint` (OK); ProgramArguments[0] = resolved harnessd path.
- [ ] Commit, push `epic/807-service-install`, open PR (no merge).

## Risks and Mitigations

- Risk: platform-specific code paths untestable on darwin dev machine.
  Mitigation: injectable `servicePlatform` var + pure renderers; linux paths
  fully exercised in unit tests.
- Risk: unit tests invoking real launchctl/systemctl.
  Mitigation: injectable `serviceRunCommand` runner var; tests substitute a
  recording fake.
