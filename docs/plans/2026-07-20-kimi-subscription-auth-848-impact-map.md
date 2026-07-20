# Provider/Model Impact Map: Kimi Code subscription authentication (#848)

## Task

- Task / issue: #848 Kimi Code subscription authentication
- Plan link: `2026-07-20-kimi-subscription-auth-848-plan.md`
- Owner: codex/kimi-subscription-auth-848
- Status: in implementation

## Config

- User-facing config added or changed: go-code-owned credential file at `~/.harness/subscription-auth/kimi.json`, seeded only by `harnesscli auth kimi login`.
- Defaults / fallbacks: missing store leaves `kimi-subscription` unconfigured with an actionable `kimi-code login` then `harnesscli auth kimi login` error; metered `kimi` remains unchanged.
- Environment variables, config files, or saved settings touched: vendor credential is read only; no new environment variable; separate store is `0600` or more restrictive.
- Migration / backward-compatibility notes: additive provider and commands only.

## Server API

- Endpoints, request fields, response fields, or server wiring affected: no public server endpoint changes.
- Provider/model resolution or registry changes: bootstrap installs the credential-backed token source for `kimi-subscription`; OpenAI-compatible requests include static `X-Kimi-Client-*` headers.
- Error states / validation changes: unavailable subscription auth is clearly reported without printing secrets.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: `/keys` includes Kimi subscription status for `kimi-subscription`.
- Persisted client state or local config changes: none; credentials stay in the shared harness user store.
- Keyboard/navigation implications: none.

## Regression Tests

- New acceptance tests required: fake OAuth/API round trip with forced short-TTL refresh and Kimi headers.
- Existing tests to update: catalog loading/bootstrap and `/keys` provider mapping.
- Cross-surface regressions to guard: metered Kimi remains API-key-only; no vendor-store writes; no credential value in source logging.
