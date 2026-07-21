# Plan: 805-undo-prompts

Parent epic: #805 (`/undo` — remove recent prompts from the active context). Parent tracker: #803.
Slices 1 (`ConversationStore.UndoPrompts`, PR #838) and 2 (`POST /v1/conversations/{id}/undo`, PR #868) are **implemented and merged**. This plan now tracks **Slice 3**: `feat(tui): /undo [n] command with immediate viewport refresh`.

## Context

- Problem: a mis-typed or derailing prompt currently lives in go-code's context forever; the only escape is `/clear`, which destroys the whole session. kimi-code's `/undo [count]` trims the last N user prompts plus everything after them.
- User impact: Slice 3 is the first user-facing surface — the TUI command that calls the Slice 2 endpoint and re-renders the trimmed conversation.
- Constraints: strict TDD per `docs/runbooks/testing.md`; mirror the `/rewind` command + API-call patterns (`executeRewindCommand`, `restoreRewindCmd`, `rewind_api_test.go`); help dialog and slash completion pick the command up automatically from `builtinCommandEntries()` (`buildHelpDialog` consumes the registry).

## Scope

- In scope:
  - `undoConversationCmd(baseURL, conversationID string, count int, apiKey string)` in `cmd/harnesscli/tui/api.go` following `restoreRewindCmd`: POST `{"count": n}`, on 200 also GET the trimmed history so the viewport can rebuild; `UndoResultMsg` in `messages.go` (`RemovedFromStep`, `RemainingMessages`, `Messages`, `Err`, `Conflict` for 409).
  - `executeUndoCommand` in `model.go`: optional numeric arg (default 1); non-numeric/zero/negative/extra args are command errors (status message, no HTTP); refuse while `runActive` (an in-flight run's terminal persistence would clobber the undo); refuse with empty `conversationID`.
  - `UndoResultMsg` handling in `Update`: success rebuilds viewport+transcript from the refetched history (extracted `resetTranscriptView` shared with `/clear`, extracted `appendConversationMessages` shared with `ConversationHistoryMsg`); 409 renders the compaction-boundary explanation inline in the viewport; other errors land in the status bar.
  - Register `undo` in `builtinCommandEntries()` (`cmd_parser.go`); add `undoConversationCmd` to the `harnessAuthCases()` auth-header table; add `undo` to `TestTUI364_RegistryCompleteness`'s known-commands list.
  - Tests in `cmd/harnesscli/tui/undo_command_test.go` (mirrors `rewind_api_test.go`).
  - Bug fix riding along (issue #895, found by the slice's own smoke): `Runner.DropConversationCache` (`internal/harness/runner.go`) + call in `handleUndoConversation` (`internal/server/http_conversations.go`) so `GET {id}/messages` stops serving the stale in-memory mirror after undo; regression tests in `internal/harness/runner_prune_test.go` and `internal/server/http_undo_test.go`.
- Out of scope: picker overlay for bare `/undo` (Slice 4), redo, store changes (merged), in-place prompt editing.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: TUI help dialog and slash completion (auto-populated from the registry — no static lists to edit).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: engineering-log entry (endpoint consumption + viewport semantics); plans INDEX description refresh.

## Test Plan (TDD)

- New failing tests first (`cmd/harnesscli/tui/undo_command_test.go`, package `tui`):
  - API level (httptest): success (method/path/body, decoded result + refetched messages); 409 → `Conflict=true` with the server's message; non-200 → `Err`; unreachable server → `Err`.
  - Command level: `/undo` default count 1 and `/undo 3` reach the server with the right body; `/undo abc`, `/undo 0`, `/undo 1 2` → status error, zero HTTP calls; no conversation → status error, zero HTTP; run active → status error, zero HTTP.
  - Model level: `UndoResultMsg` success removes the tail bubbles (`vp.View()` keeps prompt 1, drops prompt 2; transcript rebuilt; `is_meta` marker not rendered); `UndoResultMsg{Conflict}` shows the compaction explanation inline and leaves the view intact; `UndoResultMsg{Err}` leaves the view intact.
  - Registry: `undo` resolves via `NewCommandRegistry().Lookup` and `ParseCommand("/undo")` dispatches `CmdOK`.
- Existing tests to update: `TestTUI364_RegistryCompleteness` known-commands list (registry surface grew); `harnessAuthCases()` table (new authed call).
- Regression tests required: `go test ./cmd/harnesscli/... -count=1`.

## Cross-Surface Impact Map

- Not a provider/model flow change — no impact map required. Server API consumed is merged (Slice 2).

## Implementation Checklist (Slice 3)

- [x] Define acceptance criteria in tests (listed above).
- [x] Document feature status and exact contract before code (this plan).
- [x] Write failing tests first; verify red for the right reason (unknown command / nil message types).
- [x] Implement minimal code changes (messages.go + api.go + model.go + cmd_parser.go + test allowlists).
- [x] Run `go test ./cmd/harnesscli/tui/ -run Undo -count=1` green.
- [x] Run full `go test ./cmd/harnesscli/... -count=1` green.
- [x] gofmt + go vet clean.
- [x] Engineering-log entry; plans INDEX refresh.
- [x] Manual smoke: TUI against `harnessd`, 3 prompts, `/undo 2`, viewport shows only prompt 1 + responses (surfaced + fixed issue #895).
- [ ] Commit, push `epic/805-undo-prompts-s3`, open PR (no merge).

## Risks and Mitigations

- Risk: undo during an in-flight run is silently clobbered when the run's terminal persistence rewrites the store.
  - Mitigation: `/undo` refuses while `m.runActive`; covered by a no-HTTP test.
- Risk: refetched history renders the `is_meta` undo marker as a phantom bubble.
  - Mitigation: the shared `appendConversationMessages` renders only `user`/`assistant` roles (same as resume); covered by the model-level test seeding a system-role marker.
- Bug found (issue #895, fixed in this slice): `GET {id}/messages` prefers the runner's in-memory mirror, so the TUI refetch after a successful undo rebuilt the viewport with the removed messages. Fixed by `Runner.DropConversationCache` called from `handleUndoConversation`; regression tests in `internal/harness/runner_prune_test.go` and `internal/server/http_undo_test.go`.

## Slice 1–2 record (completed)

- Slice 1 (PR #838): `ConversationStore.UndoPrompts` + SQLite implementation + sentinels + `is_meta` marker; `internal/harness/conversation_undo_test.go`.
- Slice 2 (PR #868): `POST /v1/conversations/{id}/undo` (POST-only, `runs:write`, cross-tenant 404, `count`/`to_step` body, 400/404/409/501 mapping); `internal/server/http_undo_test.go`, `http_undo_tenant_test.go`; tmux curl smoke green.
