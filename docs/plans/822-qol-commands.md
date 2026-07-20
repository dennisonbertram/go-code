# Plan: /title — name sessions and show the title in statusbar and picker

Epic: #822 (slice 1 of 5). Parent: #803. Branch: `epic/822-qol-commands`.

## Context

- Problem: sessions are identified only by a bare conversation ID; users cannot label them.
- User impact: `/title fix auth bug` names a session; the name survives restarts and shows in the statusbar and `/sessions` picker.
- Constraints: title stays client-side (`sessions.json`); no server store changes; strict TDD.

## Scope

- In scope:
  - `Title string \`json:"title,omitempty"\`` on `StoredSessionEntry` + `SetTitle(id, title) bool` setter (`cmd/harnesscli/tui/sessionstore.go`).
  - `title` command in `builtinCommandEntries()` (`cmd_parser.go`): no args shows current title; args set it (`Save()`); `/title clear` removes it.
  - Optional title segment in `components/statusbar` (`SetTitle`), rendered after the model segment.
  - `Title` on `sessionpicker.SessionEntry`; picker row prefers title over the 8-char ID.
  - Wiring: statusbar title refreshed on WindowSizeMsg, `/title`, session switch, `/new`, and RunStartedMsg.
  - Docs: one row in `website/docs/cli/tui.md`, one row in `docs/ux-paths.md`, plans index entry.
- Out of scope: server-side title columns, other epic slices (`/init`, `/add-dir`, `/feedback`, `/upgrade`).

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md` (slash-command table), `docs/ux-paths.md` (command table)
- Spec docs to update before code: none (epic #822 body is the contract)
- Implementation notes to add after code: none required for this slice

## Test Plan (TDD)

- New failing tests to add first:
  - `sessionstore_test.go`: title round-trip; legacy JSON without `title` loads with empty Title; empty title omitted from saved JSON; `SetTitle` found/not-found.
  - `title_test.go` (new): `/title` set + show + clear via the model; no-session error path; persistence across store reload; statusbar shows title; session switch loads the titled session's title; `/new` clears it; picker shows title; registry/slash-complete contains `title`.
  - `components/statusbar/statusbar_test.go`: title rendered when set; unchanged when empty; long title truncated.
  - `components/sessionpicker/model_test.go`: row prefers title over short ID; falls back to ID when empty.
- Existing tests to update: none expected (additive change).
- Regression tests required: legacy `sessions.json` without `title` key loads unchanged.

## Cross-Surface Impact Map

- None required: no provider/model flow, gateway routing, model catalog, API-key, or server plumbing changes. All state is client-side in the TUI.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above; acceptance from epic: `/title fix auth bug`, restart, `/sessions` shows title, statusbar shows it in session).
- [x] Write failing tests first (store, command, statusbar, picker).
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, plans index).
- [x] Run `go test ./cmd/harnesscli/... -count=1` (27 packages ok); gofmt + go vet clean on touched files.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: `/title clear` collides with a literal one-word title "clear".
  - Mitigation: `clear` clears only when it is the sole argument; `/title clear screen` still sets the literal title "clear screen". Documented in the command description.
- Risk: tests writing to the developer's real `~/.config/harnesscli/sessions.json`.
  - Mitigation: every new model-level test sets `t.Setenv("HOME", t.TempDir())` before `initModel` (existing repo pattern).
