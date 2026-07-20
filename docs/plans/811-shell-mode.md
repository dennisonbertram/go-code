# Plan: shell mode — Slice 1: shell-mode input state with `!` prefix entry/exit

Parent epic: #811 (parent tracking: #803). This plan covers ONLY slice 1 of the epic.

## Context

- Problem: the `harnesscli` TUI has no shell mode. kimi-code parity requires that
  typing `!` in an empty input box switches the input into a visible, reversible
  shell mode. This slice ships only the input state (entry/exit/rendering);
  execution lands in slice 2.
- User impact: users get the familiar `!` affordance and clear visual feedback
  (`!` prompt marker, violet border) without any behavior change to normal prompts.
- Constraints: strict TDD (`docs/runbooks/testing.md`); worktree-only; no server/API
  changes; minimal diff scoped to the slice.

## Scope

- In scope:
  - `cmd/harnesscli/tui/model.go` — `shellMode bool` on the root Model; intercept
    `!` when input is empty (typed or pasted) before `m.input.Update(msg)`;
    Backspace/Esc on empty shell-mode input exits; submit in shell mode is a stub
    (status message, no execution) that returns to normal mode; shell-mode flag
    survives window resizes.
  - `cmd/harnesscli/tui/components/inputarea/model.go` — `SetShellMode`/`ShellMode`;
    render `!` prompt marker and a violet rounded border while active.
  - `cmd/harnesscli/tui/keys.go` + help dialog key list — help entry for shell mode.
- Out of scope (later slices): local execution + output card (2), context
  injection (3), Ctrl+B background handoff (4), persisted shell history (5).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none this slice (epic doc updates land with later slices).
- Spec docs to update before code: none.
- Implementation notes to add after code: engineering-log entry per convention.

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/tui/shellmode_test.go`,
  `cmd/harnesscli/tui/components/inputarea/shellmode_test.go`):
  - `!` on empty input enters shell mode (model flag + rendered `!` marker + border).
  - `!` typed when input is non-empty stays literal text.
  - Backspace on empty shell-mode input exits.
  - Esc on empty shell-mode input exits.
  - Non-empty shell input survives editing (typing/backspace keep mode; Esc with
    text clears input but stays in shell mode, matching the existing Esc chain).
  - Pasted text starting with `!` (multi-rune KeyRunes) enters shell mode and
    keeps the remainder as input text.
  - Submit in shell mode: exits shell mode, shows stub status message, starts no run.
  - Shell mode survives WindowSizeMsg (input component is re-created on resize).
  - inputarea component: `SetShellMode(true)` renders `!` marker + border; false
    renders `❯`; value/cursor preserved.
- Existing tests to update: none expected.
- Regression tests required: existing TUI suite must stay green (prompt submit,
  slash dispatch, @-mention, Esc chain, layout).

## Cross-Surface Impact Map

- Config: None — no new persisted fields this slice (shell history persistence is slice 5).
- Server API: None — shell mode is local TUI input state; no run is started on submit.
- TUI state: one new bool on the root Model + one on inputarea; both ephemeral.
- Regression tests: full `cmd/harnesscli/tui/...` package runs green.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first; watch them fail.
- [x] inputarea: shell-mode flag + `!` marker + violet border rendering.
- [x] root model: `shellMode` state, `!` intercept (typed + pasted), Backspace/Esc
  exit on empty input, submit stub, resize preservation.
- [x] keys.go + help dialog: shell-mode help entry.
- [x] Run `go test ./cmd/harnesscli/tui/... -count=1` green; gofmt + go vet clean.
- [x] Update engineering log; maintain docs folder index for `docs/plans/`.
- [ ] Commit, push `epic/811-shell-mode`, open PR against the repo (do not merge).

## Risks and Mitigations

- Risk: adding a border changes input height (1 → 3 lines) in shell mode only.
  Mitigation: layout already reserves 3 input lines; border only appears in shell
  mode so existing snapshots are untouched.
- Risk: `!` intercept breaking literal `!` typing. Mitigation: intercept only when
  input is empty and no overlay is active; behavior test locks this in.
- Risk: Esc priority chain regressions. Mitigation: shell-mode check slots in
  after overlay/run handling, before the input-clear arm; existing Esc tests rerun.
