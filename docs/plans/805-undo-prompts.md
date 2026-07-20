# Plan: 805-undo-prompts

Parent epic: #805 (`/undo` — remove recent prompts from the active context). Parent tracker: #803.
Slice 1 (`ConversationStore.UndoPrompts`) is **implemented and merged** (PR #838). This plan now tracks **Slice 2**: `feat(server): POST /v1/conversations/{id}/undo route`.

## Context

- Problem: a mis-typed or derailing prompt currently lives in go-code's context forever; the only escape is `/clear`, which destroys the whole session. kimi-code's `/undo [count]` trims the last N user prompts plus everything after them.
- User impact: the HTTP route exposes the Slice 1 store operation to the TUI (Slice 3) and any API client.
- Constraints: strict TDD per `docs/runbooks/testing.md`; route must sit next to `compact` in `handleConversations` with identical auth (`runs:write`) and tenant-isolation (`blockConversationCrossTenant`) shape; error mapping reuses Slice 1's typed sentinels.

## Scope

- In scope:
  - `POST /v1/conversations/{id}/undo` branch in `handleConversations` (`internal/server/http_conversations.go`), POST-only, `runs:write`, cross-tenant 404.
  - `handleUndoConversation` modeled on `handleCompactConversation`: body `{"count": N}` (absent → default 1) or `{"to_step": S}` (undo back to the prompt at step S; computed into a count), empty body treated as defaults; response `{"undone": true, "removed_from_step": S, "remaining_messages": M}`.
  - Error mapping: `ErrUndoCrossesCompaction` → 409; `ErrUndoCountOutOfRange` and bad `to_step` → 400; unknown conversation → 404; no store → 501; GET → 405.
  - Behavior tests in `internal/server/http_undo_test.go` (mirrors `http_compact_test.go`) + cross-tenant/scope tests in `internal/server/http_undo_tenant_test.go` (package `server_test`, reusing `newRunlessConversationFixture`).
  - Engineering-log entry per epic doc requirements (endpoint + boundary semantics).
- Out of scope: TUI `/undo` command (Slice 3), picker overlay (Slice 4), in-memory reconciliation of an active run's snapshot (matches compact's store-only semantics; the TUI refetches after undo in Slice 3).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (operator route lists land with the TUI slice per anti-ghost-feature rule; the route is only described in the engineering log once test-covered).
- Spec docs to update before code: this plan.
- Implementation notes to add after code: `docs/logs/engineering-log.md` entry (required by epic #805 once Slice 2 lands).

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/server/http_undo_test.go`: basic happy path (count, response shape, store truncation verified); default count on `{}` and on empty body; 400 on count 0 / negative / too large; 409 across a compaction boundary (conversation unchanged); `to_step` happy path + 400 on non-prompt step / out-of-range step / combined with count; 404 unknown conversation; 400 invalid JSON; 405 on GET; 501 without a store.
  - `internal/server/http_undo_tenant_test.go`: cross-tenant POST → 404 and no mutation (positive control: owner 200); key with only `runs:read` → 403.
- Existing tests to update: none.
- Regression tests required: `go test ./internal/server/ -run 'Undo|TenantIsolation'`; full `internal/server` + `internal/harness` suites.

## Cross-Surface Impact Map

- Not a provider/model flow change — no impact map required. TUI surface is a later slice.

## Implementation Checklist (Slice 2)

- [x] Define acceptance criteria in tests (listed above).
- [x] Document feature status and exact contract before code (this plan).
- [x] Write failing tests first; verify red for the right reason (POST undo → 404 from the catch-all).
- [x] Implement minimal code changes (route branch + handler).
- [x] Run `go test ./internal/server/ -run 'Undo|TenantIsolation' -count=1` green.
- [x] Run full `go test ./internal/server/... ./internal/harness/... -count=1` green.
- [x] gofmt + go vet clean.
- [x] Engineering-log entry; update `docs/plans/INDEX.md` description if materially changed.
- [ ] Commit, push `epic/805-undo-prompts-s2`, open PR (no merge).

## Risks and Mitigations

- Risk: `to_step` lets a caller target a non-prompt message, producing confusing truncations.
  - Mitigation: handler rejects `to_step` that does not reference a non-meta `user` message with 400 before calling the store; the store's own guards still apply to the computed count.
- Risk: undo on a conversation with an in-flight run leaves the runner's in-memory snapshot stale.
  - Mitigation: same semantics as the existing compact route (store mutation only); Slice 3 refetches messages after undo. Documented in the handler comment.

## Slice 1 record (completed, PR #838)

- Added `ConversationStore.UndoPrompts` + SQLite implementation + `ErrUndoCrossesCompaction`/`ErrUndoCountOutOfRange` + `is_meta` boundary marker; tests in `internal/harness/conversation_undo_test.go`; all repo fakes updated. Details in git history (`dec0d0c1`).
