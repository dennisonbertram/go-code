---
title: "Quickstart: Your First Agent Run"
sidebar_label: "Quickstart"
sidebar_position: 3
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Badge } from '@site/src/components/ui';

go-code is a local-first coding agent runtime. You interact with it through a single command, `go-code`, which auto-starts the background daemon (`harnessd`), runs your prompt against an LLM provider, and streams back a structured event log — all without leaving your terminal.

This guide proves the install works **before you spend a single token**. You'll run a key-free deterministic smoke first, then fire a real prompt once you have an API key.

<Callout variant="info">
The key-free path is the default tutorial spine here. It exercises the full run pipeline — HTTP request, event streaming, summary — using a scripted fake provider that requires no credentials and makes no network calls.
</Callout>

---

## Step 0: Prove it works without a key

The fake provider (`HARNESS_PROVIDER=fake`) is a scripted, in-memory stand-in for a real LLM. It returns deterministic responses from a turns file, so the smoke output is byte-stable: you can assert exact token counts, cost, and status.

Two complementary smokes exist. Run whichever fits your workflow.

<Steps>
<Step>

### In-process Go smoke

This smoke runs entirely in-process. It starts a test server, POSTs a run request, polls for completion, and asserts the `RunSummary`. No Docker, no network, no API key.

```bash
go test ./internal/server/... -run TestRunSmoke
```

Add the race detector before merging:

```bash
go test ./internal/server/... -race -count=1 -run TestRunSmoke
```

The test asserts these exact fields on the `RunSummary`:

| Field | Expected value |
|---|---|
| `status` | `"completed"` |
| `steps_taken` | `1` |
| `total_prompt_tokens` | `100` |
| `total_completion_tokens` | `50` |
| `total_cost_usd` | `0.001` |
| `cost_status` | `"available"` |

</Step>
<Step>

### Shell smoke

This smoke builds `harnessd`, starts it with `HARNESS_PROVIDER=fake` and `HARNESS_AUTH_DISABLED=true`, waits for `/healthz`, POSTs a run, polls until completion, and asserts the same deterministic fields.

```bash
bash scripts/run-bench-smoke.sh
```

The script writes a fake turns file to `/tmp/harnessd-bench-smoke-turns.json` before starting:

```json
[
  {
    "content": "smoke ok",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
```

If both smokes pass, your install is healthy.

</Step>
</Steps>

<Callout variant="warning">
`HARNESS_FAKE_TURNS` is **required** when `HARNESS_PROVIDER=fake` — if the path is empty or the file does not exist, `harnessd` fails at startup. The shell smoke writes this file automatically; when running `harnessd` manually with the fake provider you must supply the path yourself.
</Callout>

---

## Step 1: Set a provider key

OpenAI is the primary bootstrap path. When `OPENAI_API_KEY` is set and no catalog model is configured, `harnessd` bootstraps an OpenAI client directly.

```bash
export OPENAI_API_KEY=sk-...
```

go-code supports ten providers out of the box. Set the corresponding key for any of them:

<Tabs defaultValue="openai">
<TabsList>
  <TabsTrigger value="openai">OpenAI</TabsTrigger>
  <TabsTrigger value="anthropic">Anthropic</TabsTrigger>
  <TabsTrigger value="deepseek">DeepSeek</TabsTrigger>
  <TabsTrigger value="others">Others</TabsTrigger>
</TabsList>
<TabsContent value="openai">

```bash
export OPENAI_API_KEY=sk-...
```

Default model: `gpt-4.1-mini`. Models available: `gpt-4.1-mini`, `gpt-4.1`, and the codex family (`gpt-5.1-codex`, `gpt-5.1-codex-mini`, `gpt-5.1-codex-max`, `gpt-5.2-codex`, `gpt-5.3-codex`).

</TabsContent>
<TabsContent value="anthropic">

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

Models available: `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`. Aliases: `claude-opus`, `claude-sonnet`, `claude-haiku`.

</TabsContent>
<TabsContent value="deepseek">

```bash
export DEEPSEEK_API_KEY=...
```

Models available: `deepseek-chat`, `deepseek-reasoner`, `deepseek-v4-flash`, `deepseek-v4-pro`.

</TabsContent>
<TabsContent value="others">

| Provider | Environment variable |
|---|---|
| Groq | `GROQ_API_KEY` |
| xAI (Grok) | `XAI_API_KEY` |
| Google Gemini | `GOOGLE_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Together AI | `TOGETHER_API_KEY` |
| Kimi (Moonshot) | `MOONSHOT_API_KEY` |
| Qwen (DashScope) | `DASHSCOPE_API_KEY` |

See `/docs/reference/providers-and-models-reference` for the full model list and pricing.

</TabsContent>
</Tabs>

---

## Step 2: Run a prompt

With your key exported, run a prompt from inside a git repository:

```bash
go-code "Summarize the repository"
```

### What happens under the hood

1. `go-code` traverses parent directories looking for `.git/` or `.harness/config.toml`. If neither is found it falls back to `$PWD`. The resolved directory becomes the workspace root.
2. If no healthy server is already running on the configured port (default `:8080`), `go-code` starts `harnessd` in the background. **It only stops the server on exit if it started it** — a pre-existing server is always left alone.
3. The `go-code` wrapper invokes `harnesscli`, which POSTs the run to harnessd via `POST /v1/runs`; harnessd's runner invokes the LLM, and `harnesscli` streams events over SSE from `GET /v1/runs/{id}/events`.

<Callout variant="info">
go-code auto-starts harnessd only if no healthy server is already on the configured port, and only stops a server it started. If you have a long-running `harnessd` session, `go-code` will reuse it without interruption.
</Callout>

### Reading streamed output

The default (non-TUI) output format looks like this:

```
run_id=01HZ...
run.started {"id":"01HZ...:0","run_id":"01HZ...","type":"run.started","timestamp":"...","payload":{"prompt":"...","step":0,...}}
run.step.started {"id":"01HZ...:4","run_id":"01HZ...","type":"run.step.started","timestamp":"...","payload":{"step":1,...}}
llm.turn.completed {"id":"01HZ...:7","run_id":"01HZ...","type":"llm.turn.completed","timestamp":"...","payload":{"step":1,"tool_calls":0,...}}
run.step.completed {"id":"01HZ...:9","run_id":"01HZ...","type":"run.step.completed","timestamp":"...","payload":{"step":1,"tool_calls":0,"duration_ms":1,...}}
run.completed {"id":"01HZ...:10","run_id":"01HZ...","type":"run.completed","timestamp":"...","payload":{"output":"...","usage_totals":{...},"cost_totals":{...}}}
terminal_event=run.completed
```

- The first line is always `run_id=<id>` — useful for scripting follow-up commands like `go-code show <id>`.
- Each subsequent line is `<event_type> <JSON event>`. The JSON is a serialized `harness.Event` with top-level fields `id`, `run_id`, `type`, `timestamp`, and `payload` (the event-specific data). The event type matches constants in `internal/harness/events.go` — for example `run.started`, `run.step.completed`, `run.completed`.
- The final line is `terminal_event=<event_type>`. Terminal events are `run.completed`, `run.failed`, and `run.cancelled`. Any of these signals the run is done.

<Callout variant="warning">
If the run ends with `terminal_event=run.failed`, check the JSON payload on the preceding `run.failed` line for the error detail. Common causes: missing or invalid API key, model not found in the catalog, or exceeded `HARNESS_MAX_STEPS` (default `8`).
</Callout>

### Override the model for one run

The `go-code` wrapper does not forward flags — any argument that is not a recognized subcommand is treated as the prompt itself. To override the model, use `harnesscli` directly:

```bash
harnesscli -base-url http://127.0.0.1:8080 -workspace . -model gpt-4.1 -prompt "Review the recent diff"
```

Model aliases are supported: `codex`, `codex-mini`, `claude-sonnet`, `gpt4`, `gpt4-mini`, and more. See `/docs/reference/providers-and-models-reference` for the full alias table.

---

## Step 3: Go interactive

Run `go-code` with no arguments to launch the BubbleTea TUI — an interactive terminal session where you can type prompts, browse run history, and switch models without leaving the terminal:

```bash
go-code
```

<Badge variant="outline">experimental</Badge> The TUI requires a real terminal. Running it in a piped context (e.g. `go-code | tee log.txt`) exits with an error: `--tui requires a terminal; pipe output or use without --tui for streaming mode`.

---

## Next steps

- **CLI reference** — every `go-code` subcommand (`runs`, `show`, `cancel`, `continue`, `replay`, `search`, `improve`) and all flags: `/docs/cli/harnesscli`.
- **Server reference** — `harnessd` environment variables, the full HTTP API, and auth configuration: `/docs/server/harnessd`.
- **Providers and models** — the full catalog, pricing, and per-provider quirks: `/docs/reference/providers-and-models-reference`.
- **Configuration** — the 6-layer config cascade (`~/.harness/config.toml`, `.harness/config.toml`, profiles, env vars): `/docs/concepts/configuration`.
