---
title: "Tutorial: Drive harnessd from Claude Desktop"
sidebar_label: "Claude Desktop via MCP"
sidebar_position: 7
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The `harness-mcp` proxy is a small binary that lets Claude Desktop talk to a running `harnessd` instance. Once registered, Claude Desktop can start agent runs, check their status, wait for completion, continue conversations, and list recent runs — all without leaving the chat interface.

This tutorial walks you through the full setup: start `harnessd`, build the proxy, register it with Claude Desktop, and exercise all five tools.

<Callout variant="warning">
Three separate MCP surfaces exist: the HTTP endpoint at `/mcp` (always available when harnessd runs in HTTP mode), the stdio server launched with `harnessd --mcp` (exposes the full harness tool catalog), and the `harness-mcp` proxy (five curated run-management tools over stdio). This tutorial uses the proxy path. See [Exposing harnessd as an MCP Server](/docs/server/expose-as-mcp-server) for a comparison of all three.
</Callout>

---

## What you will build

```
Claude Desktop
    │  (stdio JSON-RPC 2.0)
    ▼
harness-mcp  ←─ bin you build in this tutorial
    │  (HTTP REST)
    ▼
harnessd     ←─ daemon already listening on :8080
```

`harness-mcp` reads JSON-RPC 2.0 from stdin, translates each tool call into a REST request against `harnessd`, and writes the JSON-RPC response back to stdout. Claude Desktop launches the proxy as a subprocess; the proxy connects to `harnessd` at the URL given by the `HARNESS_ADDR` environment variable.

---

## Prerequisites

- Go 1.25 or later installed.
- The go-code repository cloned locally.
- Claude Desktop installed and running.
- `OPENAI_API_KEY` set, **or** willingness to run in key-free fake mode for a dry run.

---

## Step 1 — Run harnessd

Before the proxy can do anything, `harnessd` must be running and reachable.

<Tabs defaultValue="real">
<TabsList>
  <TabsTrigger value="real">With an API key</TabsTrigger>
  <TabsTrigger value="fake">Key-free (fake provider)</TabsTrigger>
</TabsList>
<TabsContent value="real">

```bash
# From the repo root
go build -o bin/harnessd ./cmd/harnessd
OPENAI_API_KEY=sk-...  ./bin/harnessd
```

`harnessd` logs `harness server listening on :8080` when it is ready.

</TabsContent>
<TabsContent value="fake">

The fake provider replays scripted turns and requires no API key. It is useful for confirming the wiring works before spending tokens.

The fake provider **requires** a `HARNESS_FAKE_TURNS` file — if the variable is unset, harnessd exits at startup with `fatal: create provider: fake provider: read fake turns file ""`. Create a minimal turns file first, then start the daemon:

```bash
go build -o bin/harnessd ./cmd/harnessd

# Write a minimal one-turn turns file
cat > /tmp/fake-turns.json <<'EOF'
[
  {
    "content": "smoke ok",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
EOF

HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake-turns.json \
HARNESS_AUTH_DISABLED=true \
  ./bin/harnessd
```

`HARNESS_AUTH_DISABLED=true` skips Bearer-token checks so curl and the proxy work without an API key stored in the key store.

**Fake provider and model routing**: `HARNESS_PROVIDER=fake` sets the _default_ provider used when model routing cannot resolve a provider. However, by default harnessd attempts to route runs through the model registry (e.g. `gpt-4.1-mini` → OpenAI). When testing directly with `curl`, include `"allow_fallback": true` in your POST body so that a failed model-registry lookup falls back to the fake provider:

```bash
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "What is 2 + 2?", "allow_fallback": true}'
```

The `harness-mcp` proxy does **not** set `allow_fallback` automatically, so runs started through the proxy will fail without a real API key. The fake-provider mode is intended for verifying that `harnessd` starts, serves `/healthz`, and accepts run requests — not for end-to-end Claude Desktop testing.

</TabsContent>
</Tabs>

Confirm `harnessd` is healthy:

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

The `/healthz` endpoint requires no authentication and always returns `{"status":"ok"}` when the server is up.

---

## Step 2 — Build and register the proxy

<Steps>
<Step title="Build harness-mcp">

```bash
go build -o bin/harness-mcp ./cmd/harness-mcp
```

This compiles `cmd/harness-mcp/main.go` into `bin/harness-mcp`. Note the **absolute path** to the binary — you will need it in the next step.

```bash
# Print the absolute path
echo "$(pwd)/bin/harness-mcp"
```

</Step>
<Step title="Edit claude_desktop_config.json">

Open `~/Library/Application Support/Claude/claude_desktop_config.json` in a text editor and add an entry under `mcpServers`:

```json
{
  "mcpServers": {
    "harness": {
      "command": "/ABSOLUTE/PATH/TO/bin/harness-mcp",
      "env": {
        "HARNESS_ADDR": "http://localhost:8080"
      }
    }
  }
}
```

Replace `/ABSOLUTE/PATH/TO/bin/harness-mcp` with the path you printed above.

`HARNESS_ADDR` defaults to `http://localhost:8080` if you omit it, but making it explicit avoids surprises when you later run `harnessd` on a different port.

</Step>
<Step title="Restart Claude Desktop">

Quit and reopen Claude Desktop. On restart it reads `claude_desktop_config.json` and launches the proxy subprocess. The "harness" server should appear in the tool list (the hammer icon or tool picker in the chat UI).

If it does not appear, check Claude Desktop's MCP log (usually accessible from the Developer menu) for errors from the `harness-mcp` process.

</Step>
</Steps>

---

## Step 3 — Use it

With `harnessd` running and the proxy registered, Claude Desktop has access to five tools.

<Card>
<CardHeader>
<CardTitle>The 5 harness-mcp tools</CardTitle>
</CardHeader>
<CardContent>

| Tool | Required args | Optional args | What it does |
|------|--------------|---------------|--------------|
| `start_run` | `prompt` | `model`, `conversation_id`, `max_steps`, `max_cost_usd` | Submits a new agent run; returns `run_id` immediately |
| `get_run_status` | `run_id` | — | Returns current status and any error; `messages` and `cost_usd` are always empty in this version — use `wait_for_run` or the REST API to get output |
| `wait_for_run` | `run_id` | `timeout_seconds` (default 300) | Polls every 2 seconds until the run reaches a terminal state; returns status and any error; `messages` and `cost_usd` in the result are always empty in this version |
| `continue_run` | `run_id`, `prompt` | — | Looks up the previous run's `conversation_id` and starts a new run in that conversation |
| `list_runs` | — | `conversation_id`, `limit` (default 20) | Lists recent runs, optionally filtered by conversation. **Requires `HARNESS_RUN_DB` to be set** — returns an error if run persistence is not configured. |

</CardContent>
</Card>

### Example conversation in Claude Desktop

Try asking Claude something like:

> "Start a harness run with the prompt 'What is 2 + 2?' and wait for it to finish."

Claude will call `start_run` to get a `run_id`, then call `wait_for_run` to block until the run completes or times out (default 300 seconds), and finally surface the output to you.

For a multi-turn session:

> "Continue that run with the follow-up: 'What is 3 + 3?'"

Claude will call `continue_run` using the `run_id` from the previous turn. Internally the proxy fetches the `conversation_id` from `GET /v1/runs/{runID}` and submits a new run tied to that conversation, preserving context across turns.

### Understanding `wait_for_run`

`wait_for_run` is a blocking poll — it keeps calling `GET /v1/runs/{runID}` every 2 seconds and returns as soon as the run status becomes one of:

- `completed` — run finished successfully
- `failed` — run encountered an error
- `waiting_for_user` — run is paused and needs user input

If the timeout expires (default 300 seconds) before a terminal state is reached, the tool returns an error. You can pass `timeout_seconds` to extend or shorten this window.

---

## Step 4 — Alternative: `harnessd --mcp` (stdio mode)

If you want Claude Desktop to have access to the **full harness tool catalog** — not just the five run-management tools — you can point Claude Desktop directly at `harnessd --mcp` without the proxy:

```json
{
  "mcpServers": {
    "harness-full": {
      "command": "/ABSOLUTE/PATH/TO/bin/harnessd",
      "args": ["--mcp", "--mcp-workspace", "/path/to/your/workspace"],
      "env": {
        "OPENAI_API_KEY": "sk-..."
      }
    }
  }
}
```

In this mode Claude Desktop launches `harnessd` as a subprocess that speaks JSON-RPC over stdin/stdout. `harnessd` does not listen on any TCP port — all communication is via stdio. The workspace root is resolved from `--mcp-workspace`, then `HARNESS_WORKSPACE`, then `.`.

**When to prefer the proxy over `--mcp`:**

- You are already running `harnessd` as a persistent daemon and want to keep it running across Claude Desktop restarts.
- Multiple clients or processes share one `harnessd` instance.
- You want a minimal, focused interface (5 tools) rather than the full catalog.

**When to prefer `--mcp` directly:**

- You want the full harness tool catalog available in Claude Desktop.
- You are fine with Claude Desktop owning the `harnessd` process lifetime.
- You are in a single-client setup.

---

## Troubleshooting

**The "harness" server does not appear in Claude Desktop.**

Check the absolute path in `claude_desktop_config.json`. The `command` field must be an absolute path to the binary — relative paths are not resolved. Re-run `echo "$(pwd)/bin/harness-mcp"` from the repo root and paste the result.

**harnessd exits immediately when `HARNESS_PROVIDER=fake`.**

The fake provider requires `HARNESS_FAKE_TURNS` to be set to a valid turns file. If it is unset, harnessd fails at startup with `fatal: create provider: fake provider: read fake turns file ""` and never serves any requests. The fix is to create a turns file and set `HARNESS_FAKE_TURNS` to its path before starting harnessd — see the Key-free tab in Step 1 for a minimal example.

**`start_run` returns an error about authentication.**

`harnessd` implicitly disables auth when no run store (`HARNESS_RUN_DB`) is configured, so in the default no-persistence setup the proxy works without credentials. If you have `HARNESS_RUN_DB` set (which enables auth), use `HARNESS_AUTH_DISABLED=true` for local development. Direct Bearer-token configuration for `harness-mcp` is not supported today.

**`start_run` via the proxy returns "API key env OPENAI_API_KEY is not set".**

This happens when running `harnessd` with `HARNESS_PROVIDER=fake` but submitting a run via the proxy without `allow_fallback: true`. The `harness-mcp` proxy does not set `allow_fallback` automatically, so harnessd's model router tries to reach the real OpenAI provider. The fake-provider mode is intended for smoke-testing harnessd directly (via curl with `"allow_fallback": true`) — not for verifying the Claude Desktop integration path. For the proxy to work end-to-end you need a real API key.

---

## Next steps

- Read [Exposing harnessd as an MCP Server](/docs/server/expose-as-mcp-server) for a full comparison of the three MCP surfaces and reference documentation for each.
- Learn what SSE events `harnessd` emits during a run — useful for building more advanced integrations — in [The Event Model](/docs/concepts/events).
- Explore the full REST API in [HTTP API Guide](/docs/server/http-api-guide).
