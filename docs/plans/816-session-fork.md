# Plan: ConversationStore.ForkConversation primitive (epic #816, slice 1)

## Context

- Problem: There is no way to fork a *live* conversation; the `/fork` epic (#816) needs a store-level primitive first.
- User impact: Unblocks the `POST /v1/conversations/{id}/fork` endpoint and TUI `/fork` command in later slices.
- Constraints: Strict TDD per `docs/runbooks/testing.md`; worktree-only; minimal diff scoped to slice 1 only.

## Scope

- In scope:
  - `ForkConversation(ctx, srcID, newID string) (*Conversation, error)` on `ConversationStore` (`internal/harness/conversation_store.go`).
  - SQLite implementation in `internal/harness/conversation_store_sqlite.go`.
  - Update in-memory/mock stores that break compilation.
- Out of scope: HTTP endpoint (slice 2), TUI command (slice 3), docs site pages (slice 4).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none in this slice (endpoint/TUI docs land in slices 2–4).
- Spec docs to update before code: none.
- Implementation notes to add after code: engineering log entry.

## Test Plan (TDD)

- New failing tests to add first (`internal/harness/conversation_store_fork_test.go`):
  - Fork of a persisted conversation: `LoadMessages` on the new ID equals source (count, order, role, content, tool calls).
  - Divergence isolation: post-fork `SaveConversation` on either side is invisible to the other.
  - Fork of nonexistent source errors; fork onto an existing target ID errors.
  - Fork inherits workspace/tenant (`GetConversationOwner`), gets fresh timestamps, zero token/cost counters, unpinned, correct msg_count.
- Existing tests to update: none (mock stores updated only to satisfy the widened interface).
- Regression tests required: the four behavior tests above are the regression net.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first and watch them fail (compile error = red).
- [x] Add `ForkConversation` to the interface with contract doc comment.
- [x] Implement in SQLite store (single tx: insert metadata row + copy message rows).
- [x] Fix compile breakage in mock stores enumerated by `go build ./...` / `go vet ./...`.
- [x] `go test ./internal/harness/ -run Fork -v` green; touched packages' tests green.
- [x] gofmt + go vet clean.
- [ ] Commit, push `epic/816-session-fork`, open PR against repo (no merge).

## Risks and Mitigations

- Risk: FTS index desync when bulk-copying message rows.
- Mitigation: the existing `conv_msgs_fts_insert` trigger fires per-row on INSERT, so a plain INSERT…SELECT keeps FTS in sync; verified by the message-equality test reading back via `LoadMessages`.
- Risk: `UNIQUE(conversation_id, step)` violations if source steps are non-contiguous.
- Mitigation: copy with the source's own step values verbatim; test asserts order/content equality.
