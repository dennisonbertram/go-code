# Plan: Codex ChatGPT-subscription authentication (#847)

## Context

- Problem: users authenticated with the vendor `codex` CLI cannot select the ChatGPT-subscription billing route in go-code.
- User impact: import a read-only copy of the existing Codex credential, then use it transparently for a distinct `codex-subscription` provider.
- Constraints: never modify `~/.codex`; never log credentials; use #846's token cache and registry token source; no OAuth dependency; own credential file mode is `0600`.

## Scope

- In scope: Codex OAuth refresh, safe credential import/store, catalog mirroring, harnessd wiring, `harnesscli auth codex`, `/keys` status, fake-server request/refresh integration, and operator docs.
- Out of scope: interactive OAuth, vendor logout/revocation, Kimi auth (#848), changing the API-key `openai` provider, and writing under `~/.codex`.

## Documentation Contract

- Feature status: in implementation
- Public docs affected: provider/auth setup documentation and CLI help.
- Spec docs to update before code: this plan and the linked impact map.
- Implementation notes to add after code: engineering/system logs and `CLAUDE.md`.

## Test Plan (TDD)

- New failing tests to add first: OAuth request/response contract; import/permissions/status/logout; catalog model mirroring and registry headers; CLI dispatch/status; TUI subscription row; fake HTTPS completion with forced refresh; token-log grep guard.
- Existing tests to update: catalog loader/registry, harnessd bootstrap, auth dispatcher, and keys overlay tests.
- Regression tests required: absent credential remediation, `chatgpt-account-id` on requests, transparent refresh, and no credential-bearing log calls.

## Cross-Surface Impact Map

- `docs/plans/2026-07-20-codex-subscription-auth-847-impact-map.md`

## Implementation Checklist

- [x] Define acceptance criteria and planned tests.
- [x] Document feature status and exact contract before code.
- [x] Add provider/model-flow impact map before implementation.
- [ ] Implement each requested slice test-first and commit it green.
- [ ] Update docs, logs, and indexes.
- [ ] Run focused tests, vet, and full regression gate.
- [ ] Push branch and open PR without merging.

## Risks and Mitigations

- Risk: ChatGPT backend endpoint differs from the standard Responses shape. Mitigation: use a fake HTTPS integration test and report live-validation limitations explicitly.
- Risk: credentials leak through error/log paths. Mitigation: sanitized errors, no credential formatting, and a source grep regression test.
- Risk: provider model definitions drift. Mitigation: loader-level structural mirroring of the `openai` model map.
