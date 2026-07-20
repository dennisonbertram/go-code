# Plan: Subscription-auth foundation — Epic #846

## Context

- Problem: Provider clients only accept static API-key strings, so they cannot obtain expiring bearer credentials at request time or attach subscription account headers.
- User impact: Follow-on Codex and Kimi subscription-auth work can reuse one safe provider-layer credential contract without altering existing static-key behavior.
- Constraints: No new dependency; never log credential values; no TUI, credential-import, or provider-specific refresh implementation; each slice is test-first and committed independently.

## Scope

- In scope: `provider.TokenSource`, static adapter, OpenAI-compatible request auth/header plumbing, generic mutex-single-flighted refresh cache, and registry token-source support.
- Out of scope: Codex/Kimi OAuth endpoints, disk persistence/import, TUI `/keys` changes, and changes to static-key request behavior.

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: None; this is internal plumbing.
- Spec docs to update before code: This plan and `2026-07-20-subscription-auth-foundation-846-impact-map.md`.
- Implementation notes to add after code: Engineering/system logs and `CLAUDE.md`.

## Test Plan (TDD)

- New failing tests to add first: static adapter behavior; dynamic-token/extra-header requests at both endpoints plus fetch errors; cache reuse/safety-margin/single-flight/failure policy; registry configuration, factory propagation, and cache eviction.
- Existing tests to update: Factory callbacks for the added optional token-source argument.
- Regression tests required: Static API-key headers remain byte-for-byte unchanged when no token source is configured.

## Cross-Surface Impact Map

- Required impact map: `2026-07-20-subscription-auth-foundation-846-impact-map.md`

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Add the provider/model impact map before implementation.
- [x] Slice 1: add `TokenSource` interface and `StaticToken` adapter.
- [x] Slice 2: extend OpenAI-compatible config/client with dynamic auth and extra headers.
- [x] Slice 3: add generic refreshable token cache.
- [x] Slice 4: add catalog token-source registration and factory propagation.
- [x] Update append-only logs, `CLAUDE.md`, and indexes.
- [ ] Run required formatting, vet, and regression gates.
- [ ] Fetch/merge `origin/main` if advanced, then rerun regression.
- [ ] Push branch and open PR without merging.

## Risks and Mitigations

- Risk: Dynamic auth introduces credential disclosure through errors or logs. Mitigation: errors identify only the token-source operation; no value is formatted or logged.
- Risk: Concurrent expiry refreshes cause a thundering herd. Mitigation: protect cache refresh with a mutex and prove one refresh under concurrent callers.
- Risk: Mutable header maps alias caller state. Mitigation: clone headers in `NewClient` and verify existing request behavior remains unchanged.
