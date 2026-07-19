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
- [ ] Run `go test ./cmd/harnesscli/... ./internal/plugins/... -count=1`; gofmt + go vet clean.
- [ ] Push `epic/821-plugin-system` and open PR (no merge; worktree flow hands merge to maintainer).

## Risks and Mitigations

- Risk: Doc describes behavior the code does not have (ghost features).
- Mitigation: Every contract claim is grounded in `internal/plugins` code read for this plan; planned later-slice items are explicitly marked as planned, per the anti-ghost-feature rule.
