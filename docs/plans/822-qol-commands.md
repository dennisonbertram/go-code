# Plan: quality-of-life commands — epic #822 slices

Epic: #822. Parent: #803. Slice 1 branch: `epic/822-qol-commands` (merged, PR #842). Slice 2 branch: `epic/822-qol-commands-s2`.

---

# Slice 2: /init — generate AGENTS.md for the current workspace

## Context

- Problem: `internal/systemprompt` auto-injects `<workspace>/AGENTS.md` into the system prompt when present (`readAgentsMd`, engine.go), but nothing helps users create one.
- User impact: `/init` produces a starter AGENTS.md via a normal harness run; the next run's system prompt contains it.
- Constraints: write happens client-side after the run completes; overwrite requires explicit confirm; strict TDD.

## Scope

- In scope:
  - `init` command in `builtinCommandEntries()` (`cmd_parser.go`).
  - New `cmd/harnesscli/tui/init_agents.go`: fixed generation prompt (`initAgentsPrompt`), `executeInitCommand`, `extractAgentsMarkdown` (unwrap a single outer ``` fence), and the completion write path.
  - Model state `pendingInitAgentsMd`; on `RunCompletedMsg` with the flag set, write `<workspace>/AGENTS.md` (workspace = `m.config.Workspace`, falling back to cwd like `resolveWorkspacePath`); on `RunFailedMsg`, clear the flag without writing.
  - Overwrite guard: existing AGENTS.md + `/init` → hint to run `/init confirm`; `/init confirm` proceeds. Mirrors the `/rewind <id> confirm` approval pattern.
  - `/init` refused while a run is active (avoids mixing the assistant-text accumulator).
  - Docs: one row in `website/docs/cli/tui.md`, rows in `docs/ux-paths.md`, this plan.
- Out of scope: server changes; other epic slices (`/add-dir`, `/feedback`, `/upgrade`).

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md`, `docs/ux-paths.md`
- Spec docs to update before code: none (epic #822 body is the contract)
- Implementation notes to add after code: none required

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/tui/init_agents_test.go`):
  - `/init` starts a run carrying the generation prompt (observed via transcript), sets status, and writes `<workspace>/AGENTS.md` from the assistant markdown on completion.
  - Existing AGENTS.md + `/init` → no run, file untouched, hint message. `/init confirm` → run proceeds, file overwritten on completion.
  - `/init` while a run is active → refused; completion of the other run writes nothing.
  - Empty/fence-only assistant output → no file written. Run failure → no file, flag cleared.
  - `extractAgentsMarkdown` table: plain passthrough, ```markdown unwrap, ``` unwrap, unterminated fence, empty.
  - Written file is picked up by `internal/systemprompt` (`NewFileEngine` + `Resolve` on a temp fixture; AGENTS_MD section contains the content).
  - Registration: registry + slash-complete (`/in`).
- Existing tests to update: `TestTUI364_RegistryCompleteness` known-commands list (add `init`).
- Regression tests required: none beyond the above (additive change).

## Cross-Surface Impact Map

- None required: no provider/model flow, gateway, catalog, API-key, or server changes. The run uses the existing `startRunCmd` path unchanged.

## Implementation Checklist

- [x] Define acceptance criteria in tests (acceptance: repo without AGENTS.md → `/init` writes a plausible file; next run's prompt contains it — covered by the systemprompt integration test).
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, plan).
- [x] Run `go test ./cmd/harnesscli/... -count=1`; gofmt + go vet clean.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: model wraps the markdown in ``` fences → `extractAgentsMarkdown` strips a single outer fence; fence-only/empty output is treated as failure and nothing is written.
- Risk: concurrent run mixing up `lastAssistantText` → `/init` refused while `runActive`.
- Risk: tests writing outside temp dirs → every test sets `cfg.Workspace = t.TempDir()` (and HOME where the store matters).

---

# Slice 1: /title — name sessions and show the title in statusbar and picker

Status: **implemented and merged** (PR #842). Original slice-1 plan below for reference.

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
