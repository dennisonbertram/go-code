# Provider/Model Impact Map — Epic #846

## Task

- Task / issue: Subscription-auth foundation, #846
- Plan link: `2026-07-20-subscription-auth-foundation-846-plan.md`
- Owner: codex/subscription-auth-foundation-846
- Status: in implementation

## Config

- User-facing config added or changed: Internal `openai.Config` gains optional `TokenSource` and `ExtraHeaders` fields.
- Defaults / fallbacks: A nil source preserves the current static `APIKey` authorization path byte-for-byte; either a non-empty API key or source is required.
- Environment variables, config files, or saved settings touched: None. Follow-on provider epics own credential import and persistence.
- Migration / backward-compatibility notes: Existing `APIKey` callers and provider catalog entries remain valid unchanged.

## Server API

- Endpoints, request fields, response fields, or server wiring affected: No public endpoint contract changes.
- Provider/model resolution or registry changes: The registry may hold a runtime token source, treats it as configured, evicts cached clients on replacement, and passes it to its factory.
- Error states / validation changes: Token-source failures are returned as request failures without logging or exposing credential values.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: None; `/keys` and provider hints are explicitly out of scope.
- Persisted client state or local config changes: None; no credentials are read or persisted in this epic.
- Keyboard/navigation implications: None.

## Regression Tests

- New acceptance tests required: Dynamic auth/extra headers at chat and responses endpoints, source errors, cache concurrency/expiry behavior, registry propagation/eviction.
- Existing tests to update: Factory callbacks receive the optional source argument.
- Cross-surface regressions to guard: Static API-key headers are unchanged and non-subscription providers still configure via API key/environment.
