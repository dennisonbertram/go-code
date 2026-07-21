# Plan: harnesscli service OS-service commands (epic #807, slices 1–3)

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
  PR #826) shipped install/uninstall + generators; slice 2 (merged, PR #866)
  shipped start/stop/status; this branch adds slice 3: documentation.

## Scope

- In scope (slice 3, this branch — docs-only):
  - `docs/runbooks/distribution.md`: "OS Service Install" section — commands,
    per-OS unit paths, log locations, flags, lifecycle notes, troubleshooting
    (`launchctl print`, `systemctl --user status`, `journalctl --user -u
    harnessd`, lingering), scope guardrails
  - `README.md`: end-user pointer to `harnesscli service install`; tmux
    guidance explicitly re-scoped to repository dev agents
  - `docs/runbooks/INDEX.md`: distribution entry updated
  - `docs/logs/engineering-log.md`: epic entry
- Delivered (slice 1, merged PR #826): `runService` dispatch, `case
  "service":` in `dispatch()`, pure generators,
  `--binary`/`--addr`/`--log-dir` resolution, `--dry-run`, `uninstall` with
  best-effort bootout/disable.
- Delivered (slice 2, merged PR #866): `start` (bootstrap + kickstart -k
  fallback), `stop` (bootout / systemctl stop), `status` (installed check,
  loaded/active query, `/healthz` probe), quiet-on-success runner with rich
  wrapped errors.
- Out of scope: Windows, system-wide units, daemon changes.

## Documentation Contract

- Feature status: slices 1–2 `implemented` (merged PRs #826, #866); slice 3
  `in implementation`
- Public docs affected: `docs/runbooks/distribution.md`, `README.md`,
  `docs/runbooks/INDEX.md`, `docs/logs/engineering-log.md` (all this branch)
- Spec docs to update before code: this file (done)
- Implementation notes to add after code: engineering-log entry (this branch)

## Test Plan (TDD)

- Slice 3 is docs-only: no new tests. Validation = every documented
  command/flag/path checked against `cmd/harnesscli/service.go` and live
  `-h` output of a built binary; `go test ./cmd/harnesscli/ -run Service
  -count=1` re-run to confirm the documented behavior is the tested behavior.
- Slice 1–2 tests (merged): 32 `Service` tests — generator golden content,
  install/dry-run/binary/addr resolution, uninstall, fake-runner exact
  argument construction per platform/verb, status table.

## Cross-Surface Impact Map

- Not a provider/model flow change — not required.

## Implementation Checklist

- [x] Slice 1: install/uninstall + generators, merged via PR #826.
- [x] Slice 2: start/stop/status lifecycle, merged via PR #866.
- [x] Write distribution runbook OS-service section.
- [x] README end-user pointer + tmux re-scope; INDEX + engineering log.
- [ ] Validate docs against implementation (flags, paths, commands).
- [ ] Commit, push `epic/807-service-install-s3`, open PR (no merge).

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
