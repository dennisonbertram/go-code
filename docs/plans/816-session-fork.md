# Plan: /fork — fork live conversations (epic #816)

## Slice 2: `POST /v1/conversations/{id}/fork` endpoint (branch epic/816-session-fork-s2)

### Context

- Problem: slice 1 landed the store primitive; there is still no HTTP way to fork a conversation, which the TUI `/fork` (slice 3) and scripts need.
- User impact: any client can duplicate a conversation — full history included — via one authenticated POST.
- Constraints: strict TDD; mirror the `compact` route (POST-only, `runs:write`, `blockConversationCrossTenant`); in-memory-first message resolution per `handleExportConversation`.

### Scope

- In scope: `fork` sub-route in `handleConversations` + `handleForkConversation` in `internal/server/http_conversations.go`; server-minted uuid conversation ID; response `{"conversation_id","forked_from","message_count"}`; 404 unknown source, 405 wrong method, 501 no store; cross-tenant rejection; tenant stamping for memory-only forks.
- Out of scope: TUI `/fork` (slice 3), docs site pages (slice 4), capturing messages of a run currently in flight (runner exposes no active-run-state accessor; resolution covers the mirror + store per the epic's prescribed pattern).

### Test Plan (TDD)

- `internal/server/http_fork_test.go` (package server):
  - 200 store-backed fork; `GET .../{new}/messages` equals source; source unchanged.
  - 200 in-memory-only fork (mirror present, store row deleted) includes the latest message.
  - 200 hybrid: mirror ahead of store → fork captures the newer mirror messages.
  - 404 unknown source; 405 GET on fork path; 501 no store configured.
- `internal/server/http_fork_tenant_test.go` (package server_test):
  - Cross-tenant: tenant B forking tenant A's conversation → 404; tenant A fork → 200; fork inherits tenant (B cannot read the fork's messages).
  - Memory-only fork is stamped with the source run's tenant (B cannot read the fork).

### Checklist

- [x] Failing tests first (404/405 on unimplemented route).
- [x] Route + handler implemented; `internal/server` tests green.
- [x] gofmt/vet clean; commit, push, PR.

---

## Slice 1: ConversationStore.ForkConversation primitive (merged via PR #828)

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
- [x] Commit, push `epic/816-session-fork`, open PR against repo (no merge).

## Risks and Mitigations

- Risk: FTS index desync when bulk-copying message rows.
- Mitigation: the existing `conv_msgs_fts_insert` trigger fires per-row on INSERT, so a plain INSERT…SELECT keeps FTS in sync; verified by the message-equality test reading back via `LoadMessages`.
- Risk: `UNIQUE(conversation_id, step)` violations if source steps are non-contiguous.
- Mitigation: copy with the source's own step values verbatim; test asserts order/content equality.
