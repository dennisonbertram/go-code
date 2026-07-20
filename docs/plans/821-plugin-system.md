# Plan: Epic #821 Slice 1 — plugin home decision + manifest v1 contract

## Context

- Problem: Two plugin homes coexist undecided (`~/.config/harnesscli/plugins/*.json` legacy vs `~/.go-harness/plugins` bundle root), and `plugin.json` has no published authoring contract. Trust is a dead end partly because the lifecycle is undocumented.
- User impact: Contributors cannot author a bundle from docs alone; legacy JSON plugin users get no migration signal.
- Constraints: Docs/contract slice only — do NOT implement the loader changes of later slices (trust CLI, zip, markdown commands, TUI manager). Legacy JSON plugins must keep loading. Strict TDD per `docs/runbooks/testing.md`.

## Scope

- In scope:
  - Extend `docs/design/installable-plugin-bundles.md` into the manifest v1 authoring reference: full field reference, install layout (`<name>/<version>`), trust model, plugin-home decision (`~/.go-harness/plugins` / `$HARNESS_GLOBAL_DIR` = bundle home; `~/.config/harnesscli/plugins/*.json` = legacy-but-supported with deprecation path).
  - Startup warning in `cmd/harnesscli/tui/plugin_loader.go` (wired in `model.go`) when the legacy dir is non-empty, pointing at the bundle format.
  - Doc index updates: `docs/design/INDEX.md`, `docs/plans/INDEX.md` (this plan), engineering-log entry. `docs/INDEX.md` lists only folder indexes; top-level structure unchanged, so no edit per `docs/runbooks/documentation-maintenance.md` step 2.
- Out of scope: trust CLI (slice 2), zip sources (slice 3), markdown commands (slice 4), `/plugins` TUI manager (slice 5), e2e (slice 6).

## Documentation Contract

- Feature status: `in implementation` (slice 1 of epic #821)
- Public docs affected: none (no new user-facing behavior beyond a warning string)
- Spec docs to update before code: `docs/design/installable-plugin-bundles.md`
- Implementation notes to add after code: engineering-log entry

## Test Plan (TDD)

- New failing tests to add first:
  - `TestLegacyPluginsDirWarning_*` in `cmd/harnesscli/tui/plugin_loader_test.go`: non-empty legacy dir (has `.json`) yields a warning naming the bundle format/home; missing/empty/JSON-free dir yields none.
  - Model-level: legacy dir with a valid JSON plugin still registers the command AND surfaces the deprecation warning via `PluginWarnings()`.
- Existing tests to update: none expected (no test asserts the startup status message text; verified by grep).
- Regression tests required: legacy JSON registration tests already exist (`TestLoadAndRegisterPlugins_*`) and must stay green.

## Cross-Surface Impact Map

- None. No provider/model flows, gateway routing, catalogs, API keys, or server/TUI provider plumbing touched. One TUI startup warning string and docs only.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code (design doc).
- [x] Write failing tests first; watch them fail (`undefined: legacyPluginsDirWarning`).
- [x] Implement minimal code changes (warning func + wiring + status message wording).
- [x] Update docs, status ledgers, and indexes.
- [x] Update engineering log.
- [x] Run `go test ./cmd/harnesscli/... ./internal/plugins/... -count=1`; gofmt + go vet clean. **Green (28 packages ok).**
- [x] Push `epic/821-plugin-system` and open PR (no merge; worktree flow hands merge to maintainer). **PR #832.**

## Verification Results

- `GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... ./internal/plugins/... -count=1` — PASS, all 28 packages `ok`.
- `gofmt -l` on changed Go files — clean; `go vet ./cmd/harnesscli/tui/ ./internal/plugins/` — clean.
- `GOCACHE=/tmp/go-build ./scripts/test-regression.sh` — FAIL at the `go test ./...` stage in 3 packages, all unrelated to this slice (no TUI/plugin/doc coupling):
  - `internal/harness/tools` `TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly` — reproduced red on the clean `main` checkout in isolation (`go test ./internal/harness/tools/ -run TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly -count=1`), pre-existing.
  - `internal/provider/openai` (4 retry-budget tests) and `internal/watcher` `TestMultipleWatchedDirs` — pass on `main` in isolation, fail only under full-suite parallel load; flaky, pre-existing.

## Risks and Mitigations

- Risk: Doc describes behavior the code does not have (ghost features).
- Mitigation: Every contract claim is grounded in `internal/plugins` code read for this plan; planned later-slice items are explicitly marked as planned, per the anti-ghost-feature rule.

---

# Plan: Epic #821 Slice 2 — trust grant/revoke CLI + install-time confirmation

## Context

- Problem: Remote bundles install `Trusted=false` but `StateStore.SetTrusted` has no caller outside tests, so a remote bundle's commands/hooks/MCP can never activate. Remote installs also give the user no chance to review declared executable surfaces before they land.
- User impact: Trust is unreachable; remote install is a blind trust boundary crossing.
- Constraints: Strict TDD. Local installs keep current behavior (no prompt). No zip sources (slice 3), no markdown commands (slice 4), no TUI trust flow (slice 5).

## Scope

- In scope:
  - `internal/plugins/install.go`: split `Install` into `Stage` (fetch + symlink reject + validate into a private staging dir) and `StagedBundle.Promote`/`Discard`, so the CLI can review declared surfaces between validation and promotion. `Install` keeps its contract as Stage+Promote.
  - `cmd/harnesscli/plugins.go`: `plugin trust <name>` / `plugin untrust <name>` over `StateStore.SetTrusted`; `plugin list` untrusted hint (`untrusted — commands/hooks/MCP inactive`); install prints declared surfaces and requires confirmation for remote sources (`--yes`/`-y` flag, interactive y/N prompt on a TTY, refusal otherwise); update re-prints changed surfaces and re-requires confirmation for remote sources.
  - Swappable `stdinIsTerminal` seam mirroring the existing `stdin`/`stdout`/`stderr` test pattern; confirmation reads the existing `stdin` var.
  - Docs: mark slice-2 items implemented in `docs/design/installable-plugin-bundles.md`; engineering-log entry; this plan.
- Out of scope: zip sources, markdown commands, `/plugins` TUI panel changes, harnessd hot-reload (trust changes still activate at next daemon/TUI start).

## Test Plan (TDD)

- New failing tests first:
  - `internal/plugins`: Stage leaves nothing promoted; Promote moves into `<name>/<version>`; Discard removes staging and is a no-op after Promote.
  - CLI (`cmd/harnesscli/plugins_test.go`, git fixtures via `file://` remotes, skip without git):
    - trust/untrust round-trip incl. `plugins.TrustedBundles` gating proof and list hint;
    - trust on a non-installed name errors;
    - remote install declined at the prompt leaves no state record and no files;
    - remote install on a non-TTY without `--yes` refuses with a `--yes` hint;
    - remote install with `--yes` and with an interactive `y` both succeed and print declared surfaces;
    - update with unchanged surfaces needs no confirmation and preserves trust;
    - update with changed surfaces re-requires confirmation; declined update leaves the old version intact and trusted; confirmed update preserves trust.
- Existing tests to update: usage strings only if asserted (checked: not asserted).

## Cross-Surface Impact Map

- None. CLI + installer staging only; no provider/model flows, no server API, no TUI state.

## Implementation Checklist

- [x] Write failing tests first; watch them fail (`installer.Stage undefined`, `undefined: stdinIsTerminal`).
- [x] Implement Stage/Promote/Discard in `internal/plugins`.
- [x] Implement trust/untrust, list hint, install confirmation, update re-confirmation in the CLI.
- [x] Update design doc planned-markers, plan, engineering log.
- [x] `go test ./cmd/harnesscli/... ./internal/plugins/... -count=1` green; gofmt + go vet clean. **Green (28 packages ok).**
- [x] Push `epic/821-plugin-system-s2`, open PR (no merge).

## Slice 2 Verification Results

- `GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... ./internal/plugins/... -count=1` — PASS, all 28 packages `ok`.
- `gofmt -l` on changed Go files — clean; `go vet ./cmd/harnesscli/ ./internal/plugins/` — clean.
- Full-repo `./scripts/test-regression.sh` not re-run for this slice; slice-1 run established the pre-existing red baseline in `internal/harness/tools` (deterministic on main), `internal/provider/openai` and `internal/watcher` (flaky under full-suite load) — all unrelated to plugin code.

## Risks and Mitigations

- Risk: interactive prompt deadlocks in non-TTY contexts (scripts, CI).
- Mitigation: prompt only when `stdinIsTerminal()`; otherwise refuse with a `--yes` hint. Tests pin all three paths.
- Risk: declined install/update leaves residue.
- Mitigation: staging dir lives under the plugin root with a `.install-` prefix and is discarded on decline; tests assert no state record and no files.

---

# Plan: Epic #821 Slice 3 — zip and GitHub archive install sources

## Context

- Problem: `Installer` only accepts local directories and git-cloneable remotes. kimi-code parity requires `.zip` URLs (incl. GitHub archive links) and local zip files.
- User impact: Users cannot install from GitHub's archive URLs or distribute bundles as plain zip files.
- Constraints: stdlib only (`archive/zip`, `net/http`) — no new dependencies. Git/local-dir behavior unchanged. Strict TDD.

## Scope

- In scope (`internal/plugins/install.go`):
  - `Source.Zip` detection in `NormalizeSource`: `.zip` suffix (remote URLs and local files) and GitHub `/archive/` URLs; local zip files are non-remote (trusted by default), zip URLs are remote (untrusted by default).
  - `Stage` fetches zip sources (HTTP for remote, filesystem for local) and extracts with `archive/zip` into the existing staging dir; unchanged `rejectSymlinks` + `LoadBundle` + `Promote` afterwards.
  - Extraction rejects absolute entry paths, any `..` path element, backslash paths, and symlink entries; a single shared top-level directory (GitHub archive convention) is stripped so the bundle root lands at the staging dir.
  - Errors name the source (fetch, corrupt zip, bad entry).
  - CLI regression test: `plugin install --yes <zip URL>` end-to-end via `httptest`.
  - Docs: design doc sources section, engineering log, this plan.
- Out of scope: tarballs, codeload URLs without `.zip`, decompression limits, slice 4 markdown commands.

## Test Plan (TDD)

- New failing tests first (`internal/plugins/zip_test.go`):
  - `NormalizeSource` matrix: zip URL, GitHub `/archive/` with and without suffix, git URL not-zip, shorthand not-zip, local zip file (non-remote), local dir not-zip, local non-zip file rejected.
  - Local zip with `plugin.json` at archive root installs (trusted).
  - GitHub-style single-top-level-dir zip over `httptest` installs with the prefix stripped (remote).
  - `..`/absolute/backslash entries rejected, naming the entry; nothing promoted.
  - Symlink entries rejected.
  - Corrupt local zip and HTTP non-200 errors name the source.
- CLI: `TestPluginCLI_RemoteZipInstallYesFlag` — zip served by `httptest` installs untrusted and appears in `plugin list`.
- Existing tests to update: none (non-zip behavior pinned by existing install/normalize tests).

## Cross-Surface Impact Map

- None. Installer internals + one CLI regression test.

## Implementation Checklist

- [x] Write failing tests first; watch them fail (`Source.Zip undefined`; CLI zip install failed pre-implementation).
- [x] Implement detection + extraction.
- [x] Update design doc, plan, engineering log.
- [x] `go test ./cmd/harnesscli/... ./internal/plugins/... -count=1` green; gofmt + go vet clean. **Green (28 packages ok).**
- [x] Push `epic/821-plugin-system-s3`, open PR (no merge).

## Slice 3 Verification Results

- `GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... ./internal/plugins/... -count=1` — PASS, all 28 packages `ok`.
- `GOCACHE=/tmp/go-build go test ./internal/plugins/ ./cmd/harnesscli/ -count=1` — PASS (re-run on final bytes after a gofmt-only reformat of `zip_test.go`).
- `gofmt -l` on changed Go files — clean; `go vet ./internal/plugins/ ./cmd/harnesscli/` — clean.

## Risks and Mitigations

- Risk: zip-slip via crafted prefixes (e.g. all entries under `../`).
- Mitigation: every original entry name is validated (no `..` element, not absolute, no backslash) BEFORE the shared-prefix is computed and stripped; symlink entries rejected pre-extraction; pinned by tests.
