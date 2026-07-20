# Provider/Model Impact Map: TUI subscription credential import (#854)

## Task

- Task / issue: #854
- Plan link: `2026-07-20-tui-subscription-login-854-plan.md`
- Owner: Codex
- Status: in implementation

## Config

- User-facing config added or changed: None.
- Defaults / fallbacks: startup availability hints read the existing harness-owned Codex/Kimi stores; missing or invalid stores remain unavailable.
- Environment variables, config files, or saved settings touched: removes the nonexistent `KIMI_SUBSCRIPTION_AUTH` hint; existing `~/.harness/subscription-auth/{codex,kimi}.json` stores are read/import targets.
- Migration / backward-compatibility notes: existing API-key environment hints and `harnesscli auth` commands are unchanged.

## Server API

- Endpoints, request fields, response fields, or server wiring affected: adds bodyless `POST /v1/providers/{name}/import-subscription`; existing `GET /v1/providers` response is reused.
- Provider/model resolution or registry changes: successful local import rebuilds the same Codex/Kimi token source used during daemon bootstrap and calls `SetTokenSource`.
- Error states / validation changes: unknown/non-subscription names are 404; missing daemon-host vendor login returns the existing clear import error.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: `/keys` binds `i` only on subscription rows, triggers the endpoint, shows status, and refetches providers on success.
- Persisted client state or local config changes: None; startup only reads local stores.
- Keyboard/navigation implications: Enter remains API-key-only; `i` is visible in `/keys` help for subscription imports.

## Regression Tests

- New acceptance tests required: endpoint imports fake vendor credentials and flips `GET /v1/providers` to configured; no vendor login errors safely; TUI only sends import for subscription rows and schedules a refetch.
- Existing tests to update: model initialization and API-key overlay tests.
- Cross-surface regressions to guard: no credentials in request body/logs; real-store availability hints for both providers; refresh after a live import.
