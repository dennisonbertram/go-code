# Plan: 805-undo-prompts

Parent epic: #805 (`/undo` — remove recent prompts from the active context). Parent tracker: #803.
Slices 1 (`ConversationStore.UndoPrompts`, PR #838), 2 (`POST /v1/conversations/{id}/undo`, PR #868), and 3 (`/undo [n]` TUI command, PR #898) are **implemented and merged**. This plan now tracks **Slice 4 (final)**: `feat(tui): picker overlay for bare /undo`.

## Context

- Problem: a mis-typed or derailing prompt currently lives in go-code's context forever; the only escape is `/clear`, which destroys the whole session. kimi-code's `/undo [count]` trims the last N user prompts plus everything after them; bare `/undo` opens a picker of recent prompts.
- User impact: Slice 4 matches kimi-code's bare-`/undo` behavior — pick the prompt to undo back to, instead of counting manually.
- Constraints: strict TDD per `docs/runbooks/testing.md`; new overlay component mirrors `sessionpicker` (model/view/messages + `Update` key routing + Escape chain + View switch); selection dispatches the Slice 3 `undoConversationCmd`; compaction-boundary rows render disabled using the `is_compact_summary` flag from `GET {id}/messages`.

## Scope

- In scope:
  - New component `cmd/harnesscli/tui/components/undopicker/` (`model.go`, `view.go`, `messages.go`): newest-first list of the last K=10 non-meta user prompts (`UndoEntry{Step, Count, Preview, Disabled}`), Enter confirms, Esc cancels, navigation skips disabled rows.
  - `EntriesFromMessages` pure function in the component: filters `role=user && !is_meta`, computes `Count` (1 = newest), and marks `Disabled` for steps at/below the most recent `is_compact_summary` message.
  - `fetchUndoCandidatesCmd` (`api.go`): GET `{id}/messages` decoding `is_meta`/`is_compact_summary`; `UndoCandidatesLoadedMsg` / `UndoPickerSelectedMsg` (`messages.go`).
  - `executeUndoCommand` change: bare `/undo` fetches candidates and opens the picker (numeric path from Slice 3 unchanged); `UndoPickerSelectedMsg` dispatches `undoConversationCmd` with the entry's `Count`.
  - Model wiring: `undoPicker` field, `UndoCandidatesLoadedMsg` case (opens overlay, or status when nothing undoable), key routing case, Escape-chain arm, View-switch arm.
  - Tests: component tests in `components/undopicker/model_test.go` + `view_test.go`; model-level flow tests in `cmd/harnesscli/tui/undo_command_test.go` (bare `/undo` → picker opens; Enter on the 2nd-newest entry → POST `{"count":2}`; disabled rows unselectable; Esc cancels without HTTP).
  - Existing test update: `TestExecuteUndoCommand_DefaultCount` (Slice 3) — bare `/undo` no longer POSTs count=1; it opens the picker.
- Out of scope: redo, server/store changes (merged), editing a prompt in place, slash-complete pagination changes.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: TUI help dialog description for `/undo` (registry entry text updated to mention the picker).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: engineering-log entry; plans INDEX refresh.

## Test Plan (TDD)

- New failing tests first:
  - `components/undopicker/model_test.go`: closed-by-default; open/close; up/down navigation wraps and skips disabled rows; Enter on an enabled row emits `UndoSelectedMsg` with the right `Count`; Enter on a disabled row emits nothing; Esc closes; `EntriesFromMessages` — meta skipping, count assignment, boundary disabling (at and below summary), empty input, K cap.
  - `components/undopicker/view_test.go`: closed renders empty; rows show previews newest-first; disabled rows carry the compacted hint; footer hint present.
  - `undo_command_test.go` (package `tui`): bare `/undo` GETs messages and opens the overlay with entries newest-first (no POST); Enter on the 2nd-newest entry issues POST `{"count":2}` (integration: picker → server call); Esc closes with no HTTP; fetch failure → status error.
- Existing tests to update: `TestExecuteUndoCommand_DefaultCount` → rewritten as the bare-opens-picker test.
- Regression tests required: `go test ./cmd/harnesscli/... -count=1`.

## Cross-Surface Impact Map

- Not a provider/model flow change — no impact map required.

## Implementation Checklist (Slice 4)

- [x] Define acceptance criteria in tests (listed above).
- [x] Document feature status and exact contract before code (this plan).
- [x] Write failing tests first; verify red for the right reason (package undefined).
- [x] Implement component + wiring.
- [x] Run `go test ./cmd/harnesscli/tui/... -run Undo -count=1` green.
- [x] Run full `go test ./cmd/harnesscli/... -count=1` green.
- [x] gofmt + go vet clean.
- [x] Engineering-log entry; plans INDEX refresh.
- [x] Manual smoke: bare `/undo` picker in tmux TUI — chose 2nd-newest, viewport trimmed to prompt 1 + answer; compacted prompt visibly disabled and skipped by navigation; Esc closes.
- [ ] Commit, push `epic/805-undo-prompts-s4`, open PR (no merge).

## Risks and Mitigations

- Risk: message index vs store step divergence (picker counts from GET-messages order).
  - Mitigation: the store keeps steps contiguous from 0 and `LoadMessages` orders by step, so array index == step; `Count` is computed the same way the store's `UndoPrompts` walks (non-meta user messages, newest-first), keeping client and server semantics aligned.
- Risk: changing bare `/undo` from undo-1 (Slice 3) to picker breaks Slice 3 expectations.
  - Mitigation: deliberate per epic ("Without a count, the TUI opens a picker"); the Slice 3 test is updated and the numeric path is untouched.

## Slice 1–3 record (completed)

- Slice 1 (PR #838): `ConversationStore.UndoPrompts` + SQLite implementation + sentinels + `is_meta` marker; `internal/harness/conversation_undo_test.go`.
- Slice 2 (PR #868): `POST /v1/conversations/{id}/undo` (POST-only, `runs:write`, cross-tenant 404, `count`/`to_step` body, 400/404/409/501 mapping); tmux curl smoke green.
- Slice 3 (PR #898): `/undo [n]` command + `undoConversationCmd` + viewport rebuild; fixed stale-mirror bug #895 via `Runner.DropConversationCache`; tmux TUI smoke green.
