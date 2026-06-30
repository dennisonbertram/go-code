---
title: "Providers, Models, and Routing"
sidebar_label: "Providers & Routing"
sidebar_position: 5
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

go-code can talk to ten different LLM backends — OpenAI, Anthropic, DeepSeek, Groq, xAI, Kimi, Qwen, Together AI, OpenRouter, and Google Gemini — through a single unified interface. Every provider, every model, and every pricing rate is described in a JSON catalog (`catalog/models.json`) that the server loads at startup. This page explains how that catalog works, how go-code decides which provider to use for a given run, and how it tracks and caps spending.

---

## The model catalog

`catalog/models.json` is the source of truth for everything provider-related. It has two top-level fields:

- `catalog_version` — a string tag for the catalog itself.
- `providers` — a map from provider key (e.g. `"openai"`) to a `ProviderEntry`.

### What a ProviderEntry contains

Each provider entry describes how to reach the API and what models are available:

```go
type ProviderEntry struct {
    DisplayName string            `json:"display_name"`
    BaseURL     string            `json:"base_url"`
    APIKeyEnv   string            `json:"api_key_env"`
    Protocol    string            `json:"protocol"`
    Quirks      []string          `json:"quirks,omitempty"`
    Models      map[string]Model  `json:"models"`
    Aliases     map[string]string `json:"aliases,omitempty"`
}
```

Both `base_url` and `api_key_env` are required — the catalog loader will reject an entry missing either field. The `protocol` field is descriptive metadata in the catalog; it is not consumed by routing. The actual wire client is selected by provider name — only the `anthropic` provider uses the native Anthropic client; all others use the OpenAI-compatible client. Observed values in the catalog are `openai_compat`, `anthropic`, and `openai` (used by the gemini provider).

### What a Model entry contains

Each model entry describes capabilities, pricing, and quirks:

```go
type Model struct {
    DisplayName       string        `json:"display_name"`
    ContextWindow     int           `json:"context_window"`      // required, must be > 0
    MaxOutputTokens   int           `json:"max_output_tokens,omitempty"`
    Modalities        []string      `json:"modalities"`
    ToolCalling       bool          `json:"tool_calling"`
    ParallelToolCalls bool          `json:"parallel_tool_calls,omitempty"`
    Streaming         bool          `json:"streaming"`
    ReasoningMode     bool          `json:"reasoning_mode,omitempty"`
    Quirks            []string      `json:"quirks,omitempty"`
    SpeedTier         string        `json:"speed_tier,omitempty"`
    CostTier          string        `json:"cost_tier,omitempty"`
    Pricing           *ModelPricing `json:"pricing,omitempty"`
    API               string        `json:"api,omitempty"`
}
```

The `api` field controls which HTTP endpoint is used: `"responses"` routes the call to `POST /v1/responses` (the OpenAI Responses API); any other value (or empty) uses `POST /v1/chat/completions`. Several OpenAI Codex models use the Responses API endpoint.

---

## Supported providers

go-code ships with ten providers pre-wired in the catalog. The table shows the provider key you use in API calls, the required API key environment variable, and the wire protocol.

<Callout variant="info">
The `anthropic` provider uses a native Anthropic client (`internal/provider/anthropic`). All other providers use an OpenAI-compatible client (`internal/provider/openai`), even when the underlying API is not from OpenAI.
</Callout>

| Provider key | Display name | API key env var | Protocol |
|---|---|---|---|
| `openai` | OpenAI | `OPENAI_API_KEY` | `openai_compat` |
| `anthropic` | Anthropic | `ANTHROPIC_API_KEY` | `anthropic` |
| `deepseek` | DeepSeek | `DEEPSEEK_API_KEY` | `openai_compat` |
| `groq` | Groq | `GROQ_API_KEY` | `openai_compat` |
| `xai` | xAI (Grok) | `XAI_API_KEY` | `openai_compat` |
| `kimi` | Kimi (Moonshot) | `MOONSHOT_API_KEY` | `openai_compat` |
| `qwen` | Qwen (DashScope) | `DASHSCOPE_API_KEY` | `openai_compat` |
| `together` | Together AI | `TOGETHER_API_KEY` | `openai_compat` |
| `openrouter` | OpenRouter | `OPENROUTER_API_KEY` | `openai_compat` |
| `gemini` | Google Gemini | `GOOGLE_API_KEY` | `openai` |

A provider is considered "configured" when its API key environment variable is set. You can check which providers are configured at runtime:

```bash
curl http://localhost:8080/v1/providers
```

Response shape:

```json
{
  "providers": [
    {
      "name": "openai",
      "configured": true,
      "api_key_env": "OPENAI_API_KEY",
      "base_url": "https://api.openai.com/v1",
      "model_count": 8
    }
  ]
}
```

You can also set a provider's API key at runtime without restarting the server:

```bash
curl -X PUT http://localhost:8080/v1/providers/anthropic/key \
  -H "Content-Type: application/json" \
  -d '{"key": "sk-ant-..."}'
```

This endpoint requires the `admin` scope when authentication is enabled.

---

## Model catalog by provider

<Callout variant="warning">
Several OpenAI Codex model IDs in the catalog (e.g. `gpt-5.1-codex`, `gpt-5.2-codex`, `gpt-5.3-codex`) may not be available on all accounts. Verify availability against your OpenAI account before selecting these models in production.
</Callout>

<Tabs defaultValue="openai">
  <TabsList>
    <TabsTrigger value="openai">OpenAI</TabsTrigger>
    <TabsTrigger value="anthropic">Anthropic</TabsTrigger>
    <TabsTrigger value="deepseek">DeepSeek</TabsTrigger>
    <TabsTrigger value="groq">Groq</TabsTrigger>
    <TabsTrigger value="xai">xAI</TabsTrigger>
    <TabsTrigger value="other">Others</TabsTrigger>
  </TabsList>
  <TabsContent value="openai">

| Model ID | Context | Max output | Tool calling | Endpoint |
|---|---|---|---|---|
| `gpt-4.1-mini` | 1,000,000 | 16,384 | yes (parallel) | chat/completions |
| `gpt-4.1` | 1,000,000 | 32,768 | yes (parallel) | chat/completions |
| `gpt-5.1-codex` | 128,000 | 32,768 | yes | responses |
| `gpt-5.1-codex-mini` | 128,000 | 32,768 | yes | responses |
| `gpt-5.1-codex-max` | 128,000 | 32,768 | yes | responses |
| `gpt-5.2-codex` | 128,000 | 32,768 | yes | responses |
| `gpt-5.3-codex` | 128,000 | 32,768 | yes | responses |
| `computer-use-preview` | 128,000 | 8,192 | yes | responses |

Aliases: `gpt4-mini` → `gpt-4.1-mini`, `gpt4` → `gpt-4.1`, `codex` → `gpt-5.1-codex-mini`, `codex-mini` → `gpt-5.1-codex-mini`

  </TabsContent>
  <TabsContent value="anthropic">

| Model ID | Context | Max output | Tool calling |
|---|---|---|---|
| `claude-opus-4-6` | 200,000 | 32,768 | yes (parallel) |
| `claude-sonnet-4-6` | 200,000 | 16,384 | yes (parallel) |
| `claude-haiku-4-5-20251001` | 200,000 | 8,192 | yes (parallel) |

Aliases: `claude-opus` → `claude-opus-4-6`, `claude-sonnet` → `claude-sonnet-4-6`, `claude-haiku` → `claude-haiku-4-5-20251001`

The Anthropic client posts to `{baseURL}/messages` with `anthropic-version: 2023-06-01` and handles the Anthropic-specific message format internally — callers use the same `RunRequest` structure as with any other provider.

  </TabsContent>
  <TabsContent value="deepseek">

| Model ID | Context | Reasoning | Tool calling |
|---|---|---|---|
| `deepseek-chat` | 131,072 | no | yes |
| `deepseek-reasoner` | 131,072 | yes | **no** |
| `deepseek-v4-flash` | 1,048,576 | yes | yes |
| `deepseek-v4-pro` | 1,048,576 | yes | yes |

Aliases: `deepseek-v3` → `deepseek-chat`, `deepseek-r1` → `deepseek-reasoner`

DeepSeek V4 models carry the `reasoning_content_passback` quirk — the client replays prior assistant reasoning back to the API on multi-turn tool use.

  </TabsContent>
  <TabsContent value="groq">

| Model ID | Context | Reasoning | Tool calling |
|---|---|---|---|
| `llama-3.3-70b-versatile` | 131,072 | no | yes (parallel) |
| `qwen-qwq-32b` | 131,072 | yes | no |

Alias: `llama-70b` → `llama-3.3-70b-versatile`

Groq uses LPU-based inference (`speed_optimized` quirk), making it a good choice when latency is the primary concern.

  </TabsContent>
  <TabsContent value="xai">

| Model ID | Context | Reasoning | Tool calling |
|---|---|---|---|
| `grok-3-mini` | 131,072 | yes | yes |
| `grok-4-1-fast-reasoning` | 262,144 | yes | yes |

Aliases: `grok-mini` → `grok-3-mini`, `grok` → `grok-4-1-fast-reasoning`

  </TabsContent>
  <TabsContent value="other">

**Kimi (Moonshot)**

| Model ID | Context | Tool calling |
|---|---|---|
| `kimi-k2.5` | 131,072 | yes |

**Qwen (DashScope)**

| Model ID | Context | Tool calling |
|---|---|---|
| `qwen-plus` | 131,072 | yes |
| `qwen-turbo` | 1,000,000 | yes |

Alias: `qwen` → `qwen-plus`

**Together AI**

| Model ID | Context | Tool calling |
|---|---|---|
| `meta-llama/Llama-4-Maverick-17B-128E` | 1,000,000 | yes |

Alias: `llama-4-maverick` → `meta-llama/Llama-4-Maverick-17B-128E`

**Google Gemini**

| Model ID | Context | Tool calling |
|---|---|---|
| `gemini-2.0-flash` | 1,048,576 | yes (not parallel) |
| `gemini-2.5-flash` | 1,048,576 | yes (not parallel) |
| `gemini-2.5-flash-preview-04-17` | 1,048,576 | yes (not parallel) |

Gemini carries the `no_parallel_tool_calls` quirk tag. Parallel tool calls are disabled and the `models/` prefix is prepended to all Gemini model IDs; both behaviors are hardcoded by provider name in the client factory (`providerName == "gemini"`), not driven by the quirk tag itself.

  </TabsContent>
</Tabs>

You can browse all catalog models at runtime:

```bash
curl http://localhost:8080/v1/models
```

---

## Routing: how go-code picks a provider

### Startup default

When `harnessd` starts, `resolveDefaultProvider` works through four paths in order:

1. **Fake provider** — if `HARNESS_PROVIDER=fake`, use the deterministic fake provider (no API key needed). Useful for smoke tests and CI.
2. **Catalog match** — if the default model (set via `HARNESS_MODEL`, config file, or the built-in default `"gpt-4.1-mini"`) resolves to a configured provider in the catalog, use that provider's client.
3. **Legacy OpenAI** — if `OPENAI_API_KEY` is set, bootstrap an OpenAI client directly.
4. **Error** — no provider configured; the server refuses to start.

The built-in default model is `"gpt-4.1-mini"`. You can change the server-wide default via the `HARNESS_MODEL` environment variable or the `model` key in your TOML config file.

### Per-run resolution

Each `POST /v1/runs` call resolves a provider independently, letting different runs use different providers without restarting the server:

1. **Explicit provider** — if `provider_name` is set in the `RunRequest`, that provider is used directly from the registry.
2. **Model lookup** — `ProviderRegistry.GetClientForModel(model)` searches all providers in order: direct model ID match, then alias match, then (for OpenRouter) live discovery, then the `/` heuristic for OpenRouter's dynamic model namespace.
3. **Fallback** — if lookup fails and `allow_fallback` is `true`, the run falls back to the server's default provider.

### Alias resolution

Aliases let you use short names like `"codex"` instead of `"gpt-5.1-codex-mini"`. Each provider in the catalog can define an `aliases` map. The resolver follows chains up to 8 hops to prevent cycles.

### OpenRouter dynamic discovery

OpenRouter can serve thousands of models not listed in the static catalog. When `OPENROUTER_API_KEY` is set, the harness fetches `https://openrouter.ai/api/v1/models` with a 5-minute TTL cache. Live results are merged additively with the static catalog — static metadata wins on conflicts.

As a convenience, any model ID containing `/` is automatically routed to the `openrouter` provider if the key is configured. This means you can pass `"openai/gpt-4.1"` directly in `model` without setting `provider_name`, and go-code will route it to OpenRouter.

---

## Per-run model and provider selection

Control routing at the run level with these `RunRequest` fields:

```json
{
  "prompt": "Refactor the auth module to use JWTs",
  "model": "claude-sonnet",
  "provider_name": "anthropic",
  "allow_fallback": true,
  "fallback_providers": ["openai", "openrouter"],
  "reasoning_effort": "high",
  "max_cost_usd": 0.50
}
```

| Field | Type | What it does |
|---|---|---|
| `model` | string | Model ID or alias. The resolver maps this to a provider. |
| `provider_name` | string | Skip auto-resolution and use this provider directly (e.g. `"anthropic"`, `"openrouter"`). |
| `allow_fallback` | bool | If the primary provider fails with a transient error (429, 5xx), try the next provider. |
| `fallback_providers` | `[]string` | Ordered list of provider keys to try when `allow_fallback` is true. |
| `reasoning_effort` | string | For reasoning models: `"low"`, `"medium"`, or `"high"`. Empty uses the provider's default. |
| `max_cost_usd` | float64 | Per-run spending ceiling in USD. Zero means unlimited. |

<Callout variant="info">
`provider_name` bypasses model lookup entirely — the specified model ID is sent to the chosen provider as-is. Use this when you want to override the catalog's routing decision or when a model is available through multiple providers and you prefer a specific one.
</Callout>

---

## Pricing and cost accounting

### How pricing is resolved

go-code uses a two-layer pricing system:

1. **External pricing file** — if `HARNESS_PRICING_CATALOG_PATH` is set, a `FileResolver` reads rates from that JSON file (the repository ships `catalog/pricing.json` with version `"2026-04-28"`).
2. **Inline catalog pricing** — when `HARNESS_PRICING_CATALOG_PATH` is not set (the default), rates are taken from the `pricing` block embedded in each model entry in `catalog/models.json`.

Pricing is computed per turn: `(tokens / 1,000,000) * rate_usd`. Cache-read tokens are billed at the lower `cache_read_per_1m_tokens_usd` rate when the model supports prompt caching.

### Selected rates (inline catalog, USD per 1M tokens)

<Callout variant="warning">
These rates come from the embedded pricing in `catalog/models.json` and are used by default. The separate `catalog/pricing.json` file shows different rates for some models (for example, `deepseek-chat` shows different input/output prices). Do not treat these numbers as authoritative billing figures — verify actual charges against your provider's invoices.
</Callout>

| Provider | Model | Input | Output | Cache read |
|---|---|---|---|---|
| openai | `gpt-4.1-mini` | $0.40 | $1.60 | — |
| openai | `gpt-4.1` | $2.00 | $8.00 | — |
| anthropic | `claude-opus-4-6` | $15.00 | $75.00 | — |
| anthropic | `claude-sonnet-4-6` | $3.00 | $15.00 | — |
| anthropic | `claude-haiku-4-5-20251001` | $0.80 | $4.00 | — |
| deepseek | `deepseek-chat` | $0.28 | $0.42 | $0.014 |
| deepseek | `deepseek-v4-flash` | $0.14 | $0.28 | — |
| deepseek | `deepseek-v4-pro` | $0.435 | $0.87 | — |
| groq | `llama-3.3-70b-versatile` | $0.59 | $0.79 | — |
| groq | `qwen-qwq-32b` | $0.20 | $0.20 | — |
| xai | `grok-3-mini` | $0.30 | $0.50 | — |
| xai | `grok-4-1-fast-reasoning` | $5.00 | $25.00 | — |
| gemini | `gemini-2.0-flash` | $0.10 | $0.40 | — |
| gemini | `gemini-2.5-flash` | $0.15 | $0.60 | — |
| together | `meta-llama/Llama-4-Maverick-17B-128E` | $0.27 | $0.35 | — |

### Cost ceiling and the `run.cost_limit_reached` event

Set `max_cost_usd` in a `RunRequest` to cap spending for that run. When cumulative cost crosses the ceiling, the harness emits a `run.cost_limit_reached` event after the current turn finishes, then stops the agent loop (no further turns run) and marks the run completed (not failed) — it is checked at the turn boundary, so it is never aborted mid-turn. You can also set a server-wide default ceiling with `HARNESS_MAX_COST_PER_RUN_USD` (0 = unlimited).

After a run completes, `GET /v1/runs/{id}/summary` returns `total_cost_usd` and `cost_status` so you can see exactly what was spent.

---

## Provider quirks

Some providers need special handling that the client applies automatically when the catalog entry carries the relevant quirk tag.

| Quirk | Providers | What it does |
|---|---|---|
| `reasoning_content_passback` | DeepSeek, xAI, OpenRouter/DeepSeek | Replays prior assistant reasoning back to the API on multi-turn tool use calls. |
| `speed_optimized` | Groq | Informational only; flags LPU-based inference. |
| `no_parallel_tool_calls` | Gemini | Informational tag indicating Gemini does not support parallel tool calls. The actual disabling is hardcoded by provider name in the client factory, not read from this quirk tag. |

---

## Key-free smoke testing

To run the server without any provider API key — useful in CI or local development — use the fake provider:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=path/to/turns.json \
HARNESS_AUTH_DISABLED=true \
go run ./cmd/harnessd
```

With the server running, you can verify catalog routes work:

```bash
# List all providers
curl http://localhost:8080/v1/providers

# List all models
curl http://localhost:8080/v1/models

# Start a run against the fake provider
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "hello"}'
```

The fake provider returns deterministic responses from the turns file and never calls a real LLM.

---

## Configuration reference

The server-wide default model and related paths flow through the standard [configuration cascade](/docs/concepts/configuration). The most common environment variables for providers are:

| Variable | Default | Purpose |
|---|---|---|
| `HARNESS_MODEL` | `"gpt-4.1-mini"` | Server-wide default model |
| `HARNESS_MODEL_CATALOG_PATH` | auto-detected | Path to `catalog/models.json` |
| `HARNESS_PRICING_CATALOG_PATH` | (none) | Path to external pricing JSON; when unset, inline catalog pricing is used |
| `HARNESS_PROVIDER` | (none) | Set to `"fake"` for key-free deterministic mode |
| `HARNESS_OPENROUTER_REFERER` | `"https://github.com/dennisonbertram/go-agent-harness"` | HTTP Referer header sent to OpenRouter |
| `HARNESS_OPENROUTER_TITLE` | `"go-agent-harness"` | X-Title header sent to OpenRouter |

---

## Next steps

- See the [HTTP routes reference](/docs/reference/http-routes) for the full `RunRequest` schema and response shapes.
- The [configuration guide](/docs/concepts/configuration) covers the full six-layer cascade for setting a default model in TOML config files.
- The [events reference](/docs/concepts/events) lists every event emitted during a run, including `provider.resolved`, `usage.delta`, and `run.cost_limit_reached`.
