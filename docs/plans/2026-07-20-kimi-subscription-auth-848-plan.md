# Plan: Kimi Code subscription authentication — Epic #848

## Context

- Problem: Kimi Code subscribers can currently use only the metered Moonshot API-key provider.
- User impact: An existing `kimi-code` login can be imported into go-code and used as a refreshable bearer credential for a separate `kimi-subscription` provider.
- Constraints: Never write under `~/.kimi-code`; use stdlib HTTP; never log credentials; preserve metered `kimi`; token storage must be `0600` or stricter; each slice is test-first and committed independently.
- Spike findings (2026-07-20): Local vendor-session evidence identifies `https://auth.kimi.com/api/oauth/token`. One unauthenticated `OPTIONS` probe returned `405` with `Allow: POST`, confirming the route accepts POST but **not** the authenticated body/response. The API wire format was not exercised against the live subscription endpoint because an authenticated completion could consume subscription capacity. The implementation therefore uses the conventional OAuth2 form grant (`grant_type=refresh_token`, `refresh_token`, `client_id`) and the existing OpenAI-compatible client, both verified only against fake servers. Manual live verification is still required before real-world reliance.

## Scope

- In scope: Kimi refresh/client headers, read-only vendor credential import, separate credential store, CLI lifecycle commands, catalog/provider wiring, TUI status, fake-server end-to-end refresh coverage, and operator documentation.
- Out of scope: interactive Kimi device login, writes to vendor credentials, changes to the existing metered `kimi` provider, or guessing/retrying live endpoint requests.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: README/provider setup documentation and CLI help.
- Spec docs to update before code: this plan and `2026-07-20-kimi-subscription-auth-848-impact-map.md`.
- Implementation notes to add after code: engineering/system logs, `CLAUDE.md`, and docs indexes.

## Test Plan (TDD)

- New failing tests to add first: OAuth form/response/error redaction; safe import and matching permissions; CLI login/status/logout; catalog derived models and bootstrap headers/token source; TUI key status; fake-server completion with forced refresh inside a 900-second-token-appropriate safety window; source grep prevents token logging.
- Existing tests to update: catalog/bootstrap and `/keys` behavior where provider maps are enumerated.
- Regression tests required: full requested package tests plus `scripts/test-regression.sh`.

## Cross-Surface Impact Map

- Required impact map: `docs/plans/2026-07-20-kimi-subscription-auth-848-impact-map.md`.

## Implementation Checklist

- [x] Spike: record endpoint evidence, one safe live probe, and live-verification gap.
- [ ] Refresh: add convention-based OAuth refresh and 30-second safety margin for 900-second credentials.
- [ ] Import: copy vendor credential read-only into `~/.harness/subscription-auth/kimi.json` with restrictive matching mode.
- [ ] Catalog/bootstrap: derive `kimi-subscription` models from `kimi`; wire dynamic token and client headers.
- [ ] CLI: add `harnesscli auth kimi login|status|logout`.
- [ ] TUI: expose `kimi-subscription` status in `/keys`.
- [ ] Integration: fake API/token server completion plus forced refresh; add token-log grep regression.
- [ ] Documentation/logs/indexes: mark implemented and record live-verification limitation.
- [ ] Run focused tests, requested package tests, `gofmt`, `go vet ./...`, and `./scripts/test-regression.sh`.

## Risks and Mitigations

- Risk: Live OAuth body/client identity may differ from the documented convention. Mitigation: fake-server contract only, clear manual-verification notice, no repeated live probing.
- Risk: 900-second access tokens expire during normal runs. Mitigation: independent 30-second margin and forced-window refresh test.
- Risk: credential disclosure. Mitigation: no credential logging, fixtures use only fake values, source grep regression, and separate `0600` store.
