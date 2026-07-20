# Plan: shell mode — epic #811 (slices 1-2)

Parent epic: #811 (parent tracking: #803).

## Slice 2: run shell-mode commands locally with streamed output card

Status: `in implementation` (building on merged slice 1).

### Context

- Problem: slice 1 shipped shell-mode input state with a submit stub. Slice 2
  executes the submitted command locally from the `harnesscli` TUI process and
  streams stdout/stderr into the conversation view as a tool-style card.
- User impact: `!echo hello` renders a `shell` card containing `hello`;
  `!sleep 999` is interruptible with Esc/Ctrl-C; `!false` shows a non-zero exit.
- Constraints: `Update()` must never block (async pattern from
  `plugin/execute.go`); output bounded (head+tail, same 30KB cap as bash
  plugins); kill the whole process group on interrupt (pattern from
  `internal/harness/tools/exec_group_unix.go`, #786).

### Scope

- In scope:
  - New `cmd/harnesscli/tui/shellexec.go` — `tea.Cmd`-based executor:
    `exec.CommandContext(ctx, "sh", "-c", ...)` with configurable timeout
    (default 120s), combined stdout/stderr, per-read delta messages (capped),
    and a final done message carrying exit code + bounded head/tail output.
  - `shellexec_kill_unix.go` / `shellexec_kill_other.go` — process-group kill
    (mirrors `configureGroupKill`, which is unexported in its package).
  - `cmd/harnesscli/tui/model.go` — route `CommandSubmittedMsg` to the executor
    while `shellMode` is set (replacing the slice-1 stub); exit shell mode on
    submit; card lifecycle via existing `handleToolStart`/`handleToolChunk`/
    `handleToolResult`/`handleToolError` with tool name `shell`; Esc and
    Ctrl-C kill the running command; `extractToolCommand` extended to `shell`.
- Out of scope (later slices): context injection (3), Ctrl+B background
  handoff (4), persisted shell history (5). No server/API changes.

### Test Plan (TDD)

- Executor unit tests (`shellexec_internal_test.go`, package `tui`):
  stdout capture, stderr capture, non-zero exit code, timeout kills process,
  output buffer bounded at cap, streaming deltas arrive before done,
  `kill()` interrupts promptly.
- Model tests (`shellmode_exec_test.go`, package `tui_test`):
  submit runs the command and produces running → completed card with output;
  `exit 1` shows non-zero exit in the card; Esc interrupts `sleep 999` fast;
  shell mode resets to normal after submit. Update the slice-1 stub test to
  the new execution behavior.
- Regression: full `./cmd/harnesscli/...` suite stays green; slice-1
  entry/exit tests unchanged.

### Cross-Surface Impact Map

- Config: None. Server API: None (shell mode runs in the TUI process).
- TUI state: `shellExecs map[string]*shellExec` + `shellRunningID` +
  `shellExecSeq` + `shellExecTimeout` on the root Model; executors run as
  goroutines feeding tea messages.
- Known rendering limitation: tooluse `ErrorView` shows only `ErrorText`
  (agent bash errors behave the same), so failed commands report
  `exit status N` plus the bounded output as reflowed error text; the pristine
  streamed output remains visible while running. Refinable in later slices.

### Implementation Checklist (slice 2)

- [x] Slice 1 merged: input state, marker, border, entry/exit, stub submit.
- [x] Write failing executor + model tests first; watch them fail.
- [x] shellexec.go executor (start/delta/done, bounded buffer, timeout).
- [x] Process-group kill files (unix + fallback).
- [x] model.go: submit routes to executor; card lifecycle; Esc/Ctrl-C kill.
- [x] `go test ./cmd/harnesscli/... -count=1` green; gofmt + go vet clean.
- [x] Engineering-log entry; plan/index maintenance.
- [ ] Commit, push `epic/811-shell-mode-s2`, open PR (do not merge).

## Slice 1: shell-mode input state with `!` prefix entry/exit

Status: `implemented` (merged to main via PR #843).

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
