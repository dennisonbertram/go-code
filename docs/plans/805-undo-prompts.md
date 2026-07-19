# Plan: 805-undo-prompts — Slice 1: ConversationStore.UndoPrompts

Parent epic: #805 (`/undo` — remove recent prompts from the active context). Parent tracker: #803.
This plan covers **Slice 1 only**: `feat(harness): add ConversationStore.UndoPrompts with compaction-boundary guard`.

## Context

- Problem: a mis-typed or derailing prompt currently lives in go-code's context forever; the only escape is `/clear`, which destroys the whole session. kimi-code's `/undo [count]` trims the last N user prompts plus everything after them.
- User impact: store-level truncation is the foundation for the server route (Slice 2) and TUI command (Slices 3–4).
- Constraints: transactional; must refuse undo across a compaction boundary; must persist an undo-boundary marker; strict TDD per `docs/runbooks/testing.md`; reuse the `CompactConversation` transactional pattern in `internal/harness/conversation_store_sqlite.go`.

## Scope

- In scope:
  - `UndoPrompts(ctx, convID string, count int) (removedFromStep int, err error)` on `ConversationStore` (`internal/harness/conversation_store.go`).
  - SQLite implementation in `internal/harness/conversation_store_sqlite.go` (Nth-from-last non-meta `user` message lookup, compaction guard, transactional delete, `is_meta` marker insert, `msg_count`/`updated_at` maintenance).
  - Typed sentinel errors `ErrUndoCrossesCompaction` and `ErrUndoCountOutOfRange` for server-side 409/400 mapping in Slice 2.
  - Behavior tests in `internal/harness/conversation_undo_test.go` (mirrors `conversation_compact_test.go` file layout).
  - Stub implementations on all in-repo `ConversationStore` fakes so the repo compiles.
- Out of scope: server route, TUI command/picker, redo, rewind coupling, in-place prompt editing (later slices / out of epic scope).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (store-level change; user-facing docs land with the TUI slice).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: engineering-log entry lands with Slice 2 per epic doc-requirements; folder index update for this plan file.

## Test Plan (TDD)

- New failing tests to add first (`internal/harness/conversation_undo_test.go`):
  - `TestUndoPrompts_RemovesLastPromptAndTail` — undo 1 removes the last user prompt and trailing assistant/tool messages; `removedFromStep` is the prompt's step; `LoadMessages` round-trip reflects truncation.
  - `TestUndoPrompts_WalksBackNUserPromptsSkippingMeta` — undo N targets the Nth-from-last non-meta user message; interleaved `is_meta` user-role messages are not counted.
  - `TestUndoPrompts_CountOutOfRange` — count 0, negative count, and count greater than the number of user prompts all return `ErrUndoCountOutOfRange`.
  - `TestUndoPrompts_RefusesToCrossCompactionBoundary` — target step at or below the max `is_compact_summary` step returns `ErrUndoCrossesCompaction`; undo above the boundary still succeeds.
  - `TestUndoPrompts_PersistsBoundaryMarker` — an `is_meta` marker message exists at `removedFromStep` after undo and round-trips through `LoadMessages`; a subsequent undo skips the marker.
  - `TestUndoPrompts_ConversationNotFound` — unknown convID errors.
- Existing tests to update: none (new capability).
- Regression tests required: repo-wide compile check (`go build ./...`, `go vet ./...`) forces all fakes to satisfy the extended interface.

## Cross-Surface Impact Map

- Not a provider/model flow change — no impact map required. Server API and TUI surfaces are explicitly later slices.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above).
- [x] Document feature status and exact contract before code (this plan).
- [x] Write failing tests first; verify they fail to compile/run for the right reason (no `UndoPrompts` symbol).
- [x] Implement minimal code changes (interface + SQLite store + sentinel errors + fake stubs).
- [x] Run `go test ./internal/harness/ -run Undo -count=1` green.
- [x] Run `go test` on every touched package (`internal/harness`, `internal/server` if fakes touched).
- [x] gofmt + go vet clean.
- [x] Update `docs/plans/INDEX.md` with this plan (documentation-maintenance runbook).
- [ ] Commit, push `epic/805-undo-prompts`, open PR (no merge).

## Risks and Mitigations

- Risk: marker message could be counted as an undo target on later undos, corrupting count semantics.
  - Mitigation: marker is `is_meta = 1`; the target query filters `role = 'user' AND is_meta = 0`; covered by the subsequent-undo test.
- Risk: other worktree slices implement the same interface extension concurrently and conflict.
  - Mitigation: keep the diff minimal and exactly to the epic-specified signature so any merge conflict is trivial.
