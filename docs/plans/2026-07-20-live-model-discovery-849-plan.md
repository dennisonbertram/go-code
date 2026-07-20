# Live Model Discovery — Epic #849 Plan

## Context

The static model catalog is useful for curated metadata but cannot keep pace with
provider releases. This epic generalizes OpenRouter's existing five-minute live
discovery cache so configured providers can add their live model listings without
ever removing static models when discovery is unavailable.

## Scope

- Generalize OpenRouter discovery types and preserve its cache/fallback behavior.
- Replace the single registry discoverer with provider-keyed registration and
  additive, metadata-preserving merging.
- Add OpenAI, Anthropic, and DeepSeek live discoverers using their configured
  credentials; DeepSeek is the confirmed OpenAI-compatible provider selected for
  this epic.
- Wire configured discoverers in `harnessd` and document the behavior.

Out of scope: changing credential resolution, removing static catalog metadata,
and discovery for unconfigured or subscription-only providers.

## Documentation Contract

- Append the mechanism, providers, five-minute TTL, and static/stale fallback
  policy to `docs/logs/engineering-log.md`, `docs/logs/system-log.md`, and
  `CLAUDE.md` after implementation.
- Keep this plan checklist current and keep the existing catalog as the durable
  curated metadata source.

## Test Plan (TDD)

For every checklist slice, add the behavior test first, run it red, implement
only what makes it pass, then run the affected package suite before committing.
Use realistic fake HTTP responses for the three provider APIs. Finish with
`gofmt -l .`, `go vet ./...`, provider/harnessd tests, and the full regression
script.

## Cross-Surface Impact Map

| Surface | Impact |
| --- | --- |
| Config | Existing provider API keys and base URLs remain the only inputs; discovery is registered only when `IsConfigured` is true. |
| Server API | Existing merged catalog consumers (`/models`, model resolution, context lookup) gain live provider models; no route contract changes. |
| TUI state | TUI and `/keys` receive the existing server model list with additive live entries; no client state or protocol change. |
| Regression tests | Catalog cache/merge/fallback tests plus realistic OpenAI, Anthropic, and DeepSeek fake-server tests; harnessd startup coverage. |

## Implementation Checklist

- [x] refactor(catalog): generalize OpenRouterModelDiscoverer/Discovery/OpenRouterModel to provider-agnostic ModelDiscoverer/Discovery/DiscoveredModel, OpenRouter behavior unchanged (regression-tested)
- [x] refactor(catalog): ProviderRegistry.discoverers map + SetDiscovery, effectiveCatalog/merge logic generalized across every registered discoverer
- [x] feat(provider/openai): live model discovery via GET /v1/models
- [x] feat(provider/anthropic): live model discovery via GET /v1/models
- [x] feat(provider): live model discovery for one additional OpenAI-compatible provider from the existing catalog (confirm which during implementation)
- [x] feat(harnessd): wire discoverers at startup for every configured provider
- [x] docs(provider): document the discovery mechanism, TTL, and fallback policy
