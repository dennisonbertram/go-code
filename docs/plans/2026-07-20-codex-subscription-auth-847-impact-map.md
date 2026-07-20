# Provider/Model Impact Map: Codex ChatGPT-subscription authentication (#847)

## Task

- Task / issue: #847
- Plan link: `2026-07-20-codex-subscription-auth-847-plan.md`
- Owner: Codex
- Status: in implementation

## Config

- User-facing config added or changed: catalog provider `codex-subscription`; durable credential at `~/.harness/subscription-auth/codex.json`.
- Defaults / fallbacks: no credential means unconfigured; CLI tells users to run `codex login` then `harnesscli auth codex login`.
- Environment variables, config files, or saved settings touched: `~/.codex/auth.json` is read-only import source; no API key is required for the new provider.
- Migration / backward-compatibility notes: `openai` and `OPENAI_API_KEY` behavior remains unchanged.

## Server API

- Endpoints, request fields, response fields, or server wiring affected: no public HTTP endpoint changes; bootstrap registers the provider token source and account header.
- Provider/model resolution or registry changes: catalog loader derives model metadata from `openai`; registry gets a Codex token source before client creation.
- Error states / validation changes: missing credentials stay unconfigured and produce a remediation error before an upstream request.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: existing `/keys` overlay adds a read-only Codex subscription status row.
- Persisted client state or local config changes: no TUI persistence changes; CLI owns credential import/remove.
- Keyboard/navigation implications: subscription status is not editable in `/keys`; setup remains the documented CLI command.

## Regression Tests

- New acceptance tests required: fake HTTPS Responses request plus forced refresh; static account header; missing credential error; store `0600` permissions.
- Existing tests to update: catalog loader, bootstrap factory config, auth dispatch, and keys overlay rendering.
- Cross-surface regressions to guard: static OpenAI key flow and catalog model isolation remain unchanged; source guard rejects token-bearing logging.
