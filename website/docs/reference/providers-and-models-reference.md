---
title: "Provider and Model Reference"
sidebar_label: "Providers & Models"
sidebar_position: 5
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

This page is the complete static lookup for every provider and model in the go-code catalog. Use it to find a provider's base URL and API key variable, compare model capabilities side by side, decode short alias names, and check the catalog pricing rates before you set a cost ceiling.

The catalog lives in `catalog/models.json`. At startup, `harnessd` loads it into a `ProviderRegistry` and routes every run to the right backend based on the model name the caller requested. Everything on this page is derived from that file — if you need to check the raw source, that is the place.

<Callout type="info">
For an explanation of **how** routing, alias resolution, and OpenRouter dynamic discovery work, see [Providers, Models, and Routing](/docs/concepts/providers-and-models). This page is a lookup companion; that page explains the mechanics.
</Callout>

---

## Providers

go-code ships with ten providers pre-wired in the catalog. Each entry specifies the base URL the client calls, the environment variable that must be set for the provider to be considered *configured*, and the wire protocol.

| Provider key | Display name | Protocol | API key env var | Base URL |
|---|---|---|---|---|
| `openai` | OpenAI | `openai_compat` | `OPENAI_API_KEY` | `https://api.openai.com/v1` |
| `anthropic` | Anthropic | `anthropic` | `ANTHROPIC_API_KEY` | `https://api.anthropic.com/v1` |
| `deepseek` | DeepSeek | `openai_compat` | `DEEPSEEK_API_KEY` | `https://api.deepseek.com/v1` |
| `groq` | Groq | `openai_compat` | `GROQ_API_KEY` | `https://api.groq.com/openai/v1` |
| `xai` | xAI (Grok) | `openai_compat` | `XAI_API_KEY` | `https://api.x.ai/v1` |
| `kimi` | Kimi (Moonshot) | `openai_compat` | `MOONSHOT_API_KEY` | `https://api.moonshot.ai/v1` |
| `qwen` | Qwen (DashScope) | `openai_compat` | `DASHSCOPE_API_KEY` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` |
| `together` | Together AI | `openai_compat` | `TOGETHER_API_KEY` | `https://api.together.xyz/v1` |
| `openrouter` | OpenRouter | `openai_compat` | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` |
| `gemini` | Google Gemini | `openai_compat` | `GOOGLE_API_KEY` | `https://generativelanguage.googleapis.com/v1beta/openai` |

**Protocol values** — `anthropic` means the server uses the native Anthropic messages client (`internal/provider/anthropic`). `openai_compat` means the OpenAI-compatible client (`internal/provider/openai`) is used, even when the upstream API is not from OpenAI.

<Callout type="info">
The `gemini` provider has `protocol: "openai"` in the raw catalog JSON (not `openai_compat` like the other non-Anthropic providers). Both values are handled identically at runtime — the protocol field is informational and not parsed by the Go client factory. Gemini is served by the same OpenAI-compatible client as all other non-Anthropic providers.
</Callout>

### Discovery endpoints

A provider is *configured* when its API key env var is present in the process environment. You can check configured status at runtime without restarting the server:

```bash
# See all providers and whether each is configured
curl http://localhost:8080/v1/providers

# Set a provider API key at runtime (no restart required)
curl -X PUT http://localhost:8080/v1/providers/anthropic/key \
  -H "Content-Type: application/json" \
  -d '{"key": "sk-ant-..."}'
```

---

## Models per provider

<Callout type="warning">
Several model IDs in the catalog (especially the OpenAI Codex variants) may not be available on all accounts. Verify model availability with your provider before relying on them in production.
</Callout>

<Tabs defaultValue="openai">
  <TabsList>
    <TabsTrigger value="openai">OpenAI</TabsTrigger>
    <TabsTrigger value="anthropic">Anthropic</TabsTrigger>
    <TabsTrigger value="deepseek">DeepSeek</TabsTrigger>
    <TabsTrigger value="groq">Groq</TabsTrigger>
    <TabsTrigger value="xai">xAI</TabsTrigger>
    <TabsTrigger value="kimi">Kimi</TabsTrigger>
    <TabsTrigger value="qwen">Qwen</TabsTrigger>
    <TabsTrigger value="together">Together</TabsTrigger>
    <TabsTrigger value="openrouter">OpenRouter</TabsTrigger>
    <TabsTrigger value="gemini">Gemini</TabsTrigger>
  </TabsList>

  <TabsContent value="openai">

| Model ID | Context | Max output | Tool calling | Endpoint | Speed | Cost |
|---|---|---|---|---|---|---|
| `gpt-4.1-mini` | 1,000,000 | 16,384 | yes (parallel) | chat/completions | fast | budget |
| `gpt-4.1` | 1,000,000 | 32,768 | yes (parallel) | chat/completions | fast | standard |
| `gpt-5.1-codex` | 128,000 | 32,768 | yes | responses | medium | standard |
| `gpt-5.1-codex-mini` | 128,000 | 32,768 | yes | responses | fast | budget |
| `gpt-5.1-codex-max` | 128,000 | 32,768 | yes | responses | medium | premium |
| `gpt-5.2-codex` | 128,000 | 32,768 | yes | responses | medium | standard |
| `gpt-5.3-codex` | 128,000 | 32,768 | yes | responses | medium | standard |
| `computer-use-preview` | 128,000 | 8,192 | yes | responses | medium | standard |

The models listed as **responses** endpoint route to `POST /v1/responses` (OpenAI Responses API) rather than the default `POST /v1/chat/completions`. This is controlled by the `api: "responses"` field in the catalog entry.

  </TabsContent>

  <TabsContent value="anthropic">

| Model ID | Context | Max output | Tool calling | Reasoning |
|---|---|---|---|---|
| `claude-opus-4-6` | 200,000 | 32,768 | yes (parallel) | no |
| `claude-sonnet-4-6` | 200,000 | 16,384 | yes (parallel) | no |
| `claude-haiku-4-5-20251001` | 200,000 | 8,192 | yes (parallel) | no |

The Anthropic client posts to `{baseURL}/messages` with header `anthropic-version: 2023-06-01`. System messages and tool results are translated to Anthropic's native format internally — callers use the same `RunRequest` structure as with any other provider.

  </TabsContent>

  <TabsContent value="deepseek">

| Model ID | Context | Reasoning | Tool calling | Cost |
|---|---|---|---|---|
| `deepseek-chat` | 131,072 | no | yes | budget |
| `deepseek-reasoner` | 131,072 | yes | **no** | standard |
| `deepseek-v4-flash` | 1,048,576 | yes | yes | low |
| `deepseek-v4-pro` | 1,048,576 | yes | yes | standard |

`deepseek-reasoner` does not support tool calling. The `reasoning_content_passback` quirk is applied at the DeepSeek provider level and therefore covers all DeepSeek models — not only V4 (see [Provider quirks](#provider-quirks)).

  </TabsContent>

  <TabsContent value="groq">

| Model ID | Context | Reasoning | Tool calling |
|---|---|---|---|
| `llama-3.3-70b-versatile` | 131,072 | no | yes (parallel) |
| `qwen-qwq-32b` | 131,072 | yes | no |

Groq carries the `speed_optimized` quirk — it uses LPU-based inference and is a good fit when latency is the primary concern. `qwen-qwq-32b` is a reasoning model but does not support tool calling.

  </TabsContent>

  <TabsContent value="xai">

| Model ID | Context | Reasoning | Tool calling |
|---|---|---|---|
| `grok-3-mini` | 131,072 | yes | yes |
| `grok-4-1-fast-reasoning` | 262,144 | yes | yes |

xAI models carry the `reasoning_content_passback` quirk.

  </TabsContent>

  <TabsContent value="kimi">

| Model ID | Context | Tool calling |
|---|---|---|
| `kimi-k2.5` | 131,072 | yes |

No aliases are defined for the Kimi provider.

  </TabsContent>

  <TabsContent value="qwen">

| Model ID | Context | Tool calling |
|---|---|---|
| `qwen-plus` | 131,072 | yes |
| `qwen-turbo` | 1,000,000 | yes |

  </TabsContent>

  <TabsContent value="together">

| Model ID | Context | Tool calling |
|---|---|---|
| `meta-llama/Llama-4-Maverick-17B-128E` | 1,000,000 | yes |

  </TabsContent>

  <TabsContent value="openrouter">

**Static catalog models:**

| Model ID | Context | Tool calling |
|---|---|---|
| `openai/gpt-4.1-mini` | — | yes |
| `deepseek/deepseek-v4-pro` | — | yes |
| `deepseek/deepseek-v4-flash` | — | yes |

OpenRouter also exposes thousands of additional models via live discovery — see [Aliases and routing](#aliases-and-routing) below. The static entries above are pre-populated in the catalog for convenience; they carry pricing metadata.

  </TabsContent>

  <TabsContent value="gemini">

| Model ID | Context | Tool calling | Note |
|---|---|---|---|
| `gemini-2.0-flash` | 1,048,576 | yes (not parallel) | |
| `gemini-2.5-flash` | 1,048,576 | yes (not parallel) | |
| `gemini-2.5-flash-preview-04-17` | 1,048,576 | yes (not parallel) | |

Gemini carries the `no_parallel_tool_calls` quirk — parallel tool calls are forced off on every request. The client also automatically prepends the `models/` prefix to all Gemini model IDs before sending the request.

  </TabsContent>
</Tabs>

Browse the full live catalog:

```bash
curl http://localhost:8080/v1/models
```

---

## Aliases and routing

### Alias maps

Aliases are short names that resolve to full model IDs. They are defined per-provider in the catalog. The resolver follows chains up to 8 hops to prevent cycles.

<Tabs defaultValue="openai-aliases">
  <TabsList>
    <TabsTrigger value="openai-aliases">OpenAI</TabsTrigger>
    <TabsTrigger value="anthropic-aliases">Anthropic</TabsTrigger>
    <TabsTrigger value="deepseek-aliases">DeepSeek</TabsTrigger>
    <TabsTrigger value="groq-aliases">Groq</TabsTrigger>
    <TabsTrigger value="xai-aliases">xAI</TabsTrigger>
    <TabsTrigger value="qwen-aliases">Qwen</TabsTrigger>
    <TabsTrigger value="together-aliases">Together</TabsTrigger>
  </TabsList>

  <TabsContent value="openai-aliases">

| Alias | Resolves to |
|---|---|
| `gpt4-mini` | `gpt-4.1-mini` |
| `gpt4` | `gpt-4.1` |
| `codex` | `gpt-5.1-codex-mini` |
| `codex-mini` | `gpt-5.1-codex-mini` |

  </TabsContent>

  <TabsContent value="anthropic-aliases">

| Alias | Resolves to |
|---|---|
| `claude-opus` | `claude-opus-4-6` |
| `claude-sonnet` | `claude-sonnet-4-6` |
| `claude-haiku` | `claude-haiku-4-5-20251001` |

  </TabsContent>

  <TabsContent value="deepseek-aliases">

| Alias | Resolves to |
|---|---|
| `deepseek-v3` | `deepseek-chat` |
| `deepseek-r1` | `deepseek-reasoner` |

  </TabsContent>

  <TabsContent value="groq-aliases">

| Alias | Resolves to |
|---|---|
| `llama-70b` | `llama-3.3-70b-versatile` |

  </TabsContent>

  <TabsContent value="xai-aliases">

| Alias | Resolves to |
|---|---|
| `grok-mini` | `grok-3-mini` |
| `grok` | `grok-4-1-fast-reasoning` |

  </TabsContent>

  <TabsContent value="qwen-aliases">

| Alias | Resolves to |
|---|---|
| `qwen` | `qwen-plus` |

  </TabsContent>

  <TabsContent value="together-aliases">

| Alias | Resolves to |
|---|---|
| `llama-4-maverick` | `meta-llama/Llama-4-Maverick-17B-128E` |

  </TabsContent>
</Tabs>

Kimi and Gemini define no aliases in the current catalog.

### OpenRouter dynamic discovery and the `/` heuristic

OpenRouter hosts thousands of models not listed in the static catalog. When `OPENROUTER_API_KEY` is set, go-code fetches `https://openrouter.ai/api/v1/models` with a 5-minute TTL cache. Live results are merged additively into the static catalog — static metadata wins on conflicts.

**The `/` heuristic:** any model ID containing a `/` character is automatically routed to the `openrouter` provider whenever the `openrouter` provider entry is present in the loaded catalog — key configuration is not required for routing. A missing key surfaces as an error at client creation. This means you can write `"model": "openai/gpt-4.1"` directly in a `RunRequest` without setting `provider_name`, and the harness will route it to OpenRouter.

<Callout type="info">
To use a `/`-namespaced model on a provider other than OpenRouter, set `provider_name` explicitly in the `RunRequest` to bypass the heuristic.
</Callout>

---

## Pricing

### How pricing is resolved

go-code uses a two-layer pricing system:

1. **External pricing file** — if `HARNESS_PRICING_CATALOG_PATH` is set, a `FileResolver` reads rates from that JSON file. The repository ships `catalog/pricing.json` (version `"2026-04-28"`) as an optional override.
2. **Inline catalog pricing** (default) — when `HARNESS_PRICING_CATALOG_PATH` is not set, rates come from the `pricing` block embedded in each model entry in `catalog/models.json`.

Pricing is computed per turn: `(tokens / 1,000,000) * rate_usd`. Cache-read tokens are billed at the lower `cache_read_per_1m_tokens_usd` rate when the model supports prompt caching.

<Callout type="warning">
The rates below come from the embedded pricing blocks in `catalog/models.json` and reflect what the default resolver uses. They are catalog data — **not a billing source of truth**. Actual charges depend on your provider's current pricing. Always verify against your provider's invoices before making cost decisions.
</Callout>

<Callout type="warning">
A known discrepancy exists between the two catalog files: `catalog/pricing.json` shows `deepseek-chat` at $0.27 input / $1.10 output per 1M tokens, while the inline block in `catalog/models.json` shows $0.28 / $0.42. The inline value is what the default resolver uses. The two files are not synchronized.
</Callout>

### Catalog rates (USD per 1M tokens)

| Provider | Model | Input | Output | Cache read |
|---|---|---|---|---|
| openai | `gpt-4.1-mini` | $0.40 | $1.60 | — |
| openai | `gpt-4.1` | $2.00 | $8.00 | — |
| anthropic | `claude-opus-4-6` | $15.00 | $75.00 | — |
| anthropic | `claude-sonnet-4-6` | $3.00 | $15.00 | — |
| anthropic | `claude-haiku-4-5-20251001` | $0.80 | $4.00 | — |
| deepseek | `deepseek-chat` | $0.28 | $0.42 | $0.014 |
| deepseek | `deepseek-reasoner` | $0.55 | $2.19 | $0.014 |
| deepseek | `deepseek-v4-flash` | $0.14 | $0.28 | — |
| deepseek | `deepseek-v4-pro` | $0.435 | $0.87 | — |
| groq | `llama-3.3-70b-versatile` | $0.59 | $0.79 | — |
| groq | `qwen-qwq-32b` | $0.20 | $0.20 | — |
| xai | `grok-3-mini` | $0.30 | $0.50 | — |
| xai | `grok-4-1-fast-reasoning` | $5.00 | $25.00 | — |
| kimi | `kimi-k2.5` | $0.35 | $1.40 | — |
| qwen | `qwen-plus` | $0.80 | $2.00 | — |
| qwen | `qwen-turbo` | $0.20 | $0.60 | — |
| together | `meta-llama/Llama-4-Maverick-17B-128E` | $0.27 | $0.35 | — |
| openrouter | `openai/gpt-4.1-mini` | $0.44 | $1.76 | — |
| openrouter | `deepseek/deepseek-v4-pro` | $0.435 | $0.87 | $0.003625 |
| openrouter | `deepseek/deepseek-v4-flash` | $0.14 | $0.28 | $0.0028 |
| gemini | `gemini-2.0-flash` | $0.10 | $0.40 | — |
| gemini | `gemini-2.5-flash` | $0.15 | $0.60 | — |
| gemini | `gemini-2.5-flash-preview-04-17` | $0.15 | $0.60 | — |

---

## Provider quirks

Quirks are string tags in the catalog entry that instruct the client to apply special handling automatically. You do not configure quirks directly — they are read at client-factory time and applied for every request to that provider.

| Quirk tag | Providers | What the client does |
|---|---|---|
| `reasoning_content_passback` | DeepSeek (all models, provider-level), xAI (provider-level), OpenRouter/DeepSeek routes | Replays prior assistant reasoning content back to the API on multi-turn tool-use calls. Required for correct tool-use behavior with these models. Applied at the provider level; model-level quirk tags in the catalog are not consumed by the client factory. |
| `speed_optimized` | Groq | Informational only; flags LPU-based inference. No behavioral change in the client. |
| `no_parallel_tool_calls` | Gemini | Forces `parallel_tool_calls: false` on every request. Gemini does not support parallel tool calls and returns an error if they are requested. |

---

## Environment variable quick reference

The env vars that directly affect provider and model behavior:

| Variable | Default | Purpose |
|---|---|---|
| `HARNESS_MODEL` | `"gpt-4.1-mini"` | Server-wide default model (layer 5 in the config cascade) |
| `HARNESS_PROVIDER` | — | Set to `"fake"` for key-free deterministic smoke testing |
| `HARNESS_FAKE_TURNS` | — | Path to turns JSON file; required when `HARNESS_PROVIDER=fake` |
| `HARNESS_MODEL_CATALOG_PATH` | auto-detected | Path to `catalog/models.json` |
| `HARNESS_PRICING_CATALOG_PATH` | — | Path to external pricing JSON; when unset, inline catalog pricing is used |
| `HARNESS_OPENROUTER_REFERER` | `"https://github.com/dennisonbertram/go-agent-harness"` | `HTTP-Referer` header sent to OpenRouter |
| `HARNESS_OPENROUTER_TITLE` | `"go-agent-harness"` | `X-Title` header sent to OpenRouter |
| `OPENAI_API_KEY` | — | OpenAI key; also the legacy fallback bootstrap path |
| `ANTHROPIC_API_KEY` | — | Anthropic key |
| `DEEPSEEK_API_KEY` | — | DeepSeek key |
| `GROQ_API_KEY` | — | Groq key |
| `XAI_API_KEY` | — | xAI key |
| `MOONSHOT_API_KEY` | — | Kimi / Moonshot key |
| `DASHSCOPE_API_KEY` | — | Qwen / DashScope key |
| `TOGETHER_API_KEY` | — | Together AI key |
| `OPENROUTER_API_KEY` | — | OpenRouter key |
| `GOOGLE_API_KEY` | — | Google Gemini key |

---

## Next steps

- [Providers, Models, and Routing](/docs/concepts/providers-and-models) — the conceptual page explaining how routing, fallback, and cost accounting work end-to-end.
- [Configuration](/docs/concepts/configuration) — the full 6-layer cascade for setting a default model in TOML config files.
- [HTTP Route Reference](/docs/reference/http-routes) — full route inventory and `RunRequest` schema including `provider_name`, `allow_fallback`, `fallback_providers`, and `max_cost_usd`.
- [Events Reference](/docs/concepts/events) — every event emitted during a run, including `provider.resolved`, `usage.delta`, and `run.cost_limit_reached`.
