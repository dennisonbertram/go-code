---
title: "Using the HTTP API"
sidebar_label: "HTTP API Guide"
sidebar_position: 3
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';
import RunRequestBuilder from '@site/src/components/RunRequestBuilder';

`harnessd` is the HTTP daemon that backs every go-code agent run. It exposes a REST + Server-Sent Events (SSE) API on a single port (default `:8080`). Any process that can make HTTP requests — a shell script, a CI job, another service — can start agent runs, stream their output in real time, and control them mid-flight.

This guide walks you through the two execution models, the full `RunRequest` body, the run-control endpoints, and how to handle errors and limits. The streamed-run examples use the built-in fake provider for key-free local testing, which requires a turns JSON file and `allow_fallback:true` in the request body — see the startup step for details. The `POST /v1/agents` synchronous endpoint requires a real configured provider (it does not support `allow_fallback` in the request body and cannot use the fake provider via the fallback path).

---

## Two execution models

There are two ways to run an agent:

| Model | Endpoint | When to use |
|---|---|---|
| **Streamed** | `POST /v1/runs` + `GET /v1/runs/{id}/events` | Long-running tasks; you want real-time tool/token events |
| **Synchronous** | `POST /v1/agents` | Short fire-and-wait tasks; you only need the final output |

<Callout type="info">
Streaming is not the only way to run an agent. `POST /v1/agents` is a distinct, fully synchronous execution model that blocks until the agent finishes and returns the result directly in the response body. Use it when you don't need the event stream and want simpler client code.
</Callout>

---

## Start and stream a run

The streamed path is two HTTP calls:

1. `POST /v1/runs` — start the run, get a `run_id`
2. `GET /v1/runs/{id}/events` — open an SSE stream and receive events until the terminal event

<Steps>
<Step>

### Start the daemon (key-free)

The fake provider requires a turns JSON file that describes the scripted responses it will return. Write one first, then start the daemon from the repository root (it loads `prompts/catalog.yaml` relative to the working directory):

```bash
# 1. Write a turns file (one scripted response per element).
cat > /tmp/fake-turns.json <<'EOF'
[
  {
    "content": "Hello!",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
EOF

# 2. Start the daemon pointing at that file.
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake-turns.json \
HARNESS_AUTH_DISABLED=true \
go run ./cmd/harnessd
```

The server prints `harness server listening on :8080` when ready. `HARNESS_PROVIDER=fake` selects the built-in scripted provider (no API key, no network calls), `HARNESS_FAKE_TURNS` tells it which turns file to load, and `HARNESS_AUTH_DISABLED=true` skips Bearer token validation.

</Step>
<Step>

### POST /v1/runs — start a run

```bash
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "say hello", "allow_fallback": true}'
```

`allow_fallback: true` is required in fake mode: without it, model resolution selects the OpenAI provider and the run fails immediately with `API key env "OPENAI_API_KEY" is not set`. With `allow_fallback: true` the runner falls back to the fake provider when the primary provider is unavailable.

The server responds immediately with **HTTP 202 Accepted**:

```json
{"run_id": "run_abc123", "status": "queued"}
```

The status is `"queued"` on creation; it transitions to `"running"` once a worker slot opens. The `run_id` is how you refer to this run in all subsequent calls.

</Step>
<Step>

### GET `/v1/runs/{id}/events` — stream events

```bash
curl -s -N \
  "http://localhost:8080/v1/runs/run_abc123/events"
```

The `-N` flag disables curl's output buffering so you see events as they arrive. The response is `text/event-stream`. With the fake-mode setup above (turns file + `allow_fallback: true`) you will see:

```
id: run_abc123:0
retry: 3000
event: run.started
data: {"id":"run_abc123:0","run_id":"run_abc123","type":"run.started","timestamp":"...","payload":{"prompt":"say hello"}}

id: run_abc123:1
retry: 3000
event: assistant.message
data: {"id":"run_abc123:1","run_id":"run_abc123","type":"assistant.message","timestamp":"...","payload":{"content":"Hello!"}}

id: run_abc123:2
retry: 3000
event: run.completed
data: {"id":"run_abc123:2","run_id":"run_abc123","type":"run.completed","timestamp":"...","payload":{"output":"Hello!","usage_totals":{...},"cost_totals":{...}}}
```

<Callout type="warning">
Without `allow_fallback: true` in the POST body, or without `HARNESS_FAKE_TURNS` set, the terminal event will be `run.failed` with `model "gpt-4.1-mini": provider "openai": API key env "OPENAI_API_KEY" is not set` instead of `run.completed`.
</Callout>

The stream closes automatically after a terminal event. There are exactly three terminal events:

| Event | Meaning |
|---|---|
| `run.completed` | Run finished successfully |
| `run.failed` | Run failed with an error |
| `run.cancelled` | Run was cancelled via `POST /v1/runs/{id}/cancel` |

</Step>
</Steps>

### Why streaming bypasses the 30-second timeout

Non-streaming endpoints have a 30-second handler timeout by default. SSE event stream paths (those ending in `events`, `stream`, or `wait`) bypass this timeout entirely so long-running agents can stream for as long as needed without the connection being cut.

### Reconnecting after a disconnect

Each SSE frame includes an `id:` field in `{run_id}:{seq}` format. If your connection drops, reconnect with the `Last-Event-ID` header set to the last ID you received and the server will replay all events you missed:

```bash
curl -s -N \
  -H "Last-Event-ID: run_abc123:5" \
  "http://localhost:8080/v1/runs/run_abc123/events"
```

The `retry: 3000` field in each frame tells SSE clients to wait 3 seconds before reconnecting. Keepalive pings (`: ping`) are sent as SSE comment lines every 15 seconds (configurable via `HARNESS_SSE_KEEPALIVE_SECONDS`).

---

## Synchronous single-shot: POST /v1/agents

For short, fire-and-wait tasks, use `POST /v1/agents`. It blocks until the agent finishes and returns the result directly:

```bash
curl -s -X POST http://localhost:8080/v1/agents \
  -H "Content-Type: application/json" \
  -d '{"prompt": "what is 2+2?", "timeout_seconds": 30}'
```

Response:

```json
{
  "output": "4",
  "summary": "...",
  "duration_ms": 412
}
```

Key differences from the streamed path:

| Field | Behavior |
|---|---|
| `prompt` | Required (XOR with `skill`) |
| `skill` | Run a named skill instead of a raw prompt |
| `skill_args` | Arguments passed to the skill |
| `allowed_tools` | Restrict available tools for this run |
| `timeout_seconds` | Default `120`, max `600` |

<Callout type="warning">
`prompt` and `skill` are mutually exclusive. Providing both returns HTTP 400.
</Callout>

<Callout type="warning">
**`POST /v1/agents` does not work with the fake provider setup.** The `agentRequest` body has no `allow_fallback` field. Internally, `POST /v1/agents` calls `RunPrompt`, which does not set `AllowFallback`, so model resolution tries `gpt-4.1-mini` → `openai` → and the run fails. Because `RunPrompt` returns only the (empty) output and discards the run's error, the handler still responds with **HTTP 200** and an empty body — `{"output":"","summary":"","duration_ms":<elapsed>}` — instead of an error. This endpoint requires a real configured provider (a set `OPENAI_API_KEY` or equivalent). Use `POST /v1/runs` with `"allow_fallback": true` for key-free local testing.
</Callout>

---

## RunRequest body

`POST /v1/runs` accepts a JSON body that maps to the `RunRequest` struct (`internal/harness/types.go`). Only `prompt` is required; everything else is optional.

<Callout type="info" title="Interactive request builder">
Fill in the fields below and copy the generated JSON body or curl command. The builder runs entirely in your browser — it does not send any requests. To actually run the command, paste the curl into a terminal where your own `harnessd` is listening. (The docs site cannot call your local server directly because `harnessd` does not send CORS headers.)
</Callout>

<RunRequestBuilder />

```json
{
  "prompt": "Refactor the auth module to use context propagation",
  "model": "gpt-4.1",
  "provider_name": "openai",
  "workspace_type": "worktree",
  "max_steps": 20,
  "max_turns": 40,
  "max_cost_usd": 0.50,
  "allowed_tools": ["bash", "read_file", "write_file"],
  "conversation_id": "conv_xyz789",
  "agent_id": "refactor-agent",
  "agent_intent": "refactor",
  "reasoning_effort": "high",
  "allow_fallback": true,
  "fallback_providers": ["anthropic"],
  "permissions": {
    "sandbox": "workspace",
    "approval": "destructive"
  },
  "mcp_servers": [
    {"name": "my-mcp", "url": "http://localhost:9000"}
  ],
  "profile": "coding"
}
```

### Field reference

<Card>
<CardHeader>
<CardTitle>Core fields</CardTitle>
</CardHeader>
<CardContent>

| Field | Type | Notes |
|---|---|---|
| `prompt` | `string` | **Required.** The task or question for the agent. |
| `model` | `string` | Model ID, e.g. `"gpt-4.1"`, `"claude-sonnet"`. Falls back to server default (`gpt-4.1-mini`). |
| `provider_name` | `string` | Explicit provider override, e.g. `"openai"`, `"anthropic"`. When omitted, the provider is resolved from the model name. |
| `workspace_type` | `string` | One of `""` (server default), `"local"`, `"worktree"`, `"container"`, `"vm"`. Unknown values are rejected. |
| `extra_dirs` | `[]string` | Additional absolute directory roots the run may read/work in beyond the workspace root (TUI `/add-dir`). Each entry must be an absolute path to an existing directory; violations are rejected with HTTP 400. Applies to file-tool path confinement only — the bash sandbox and `glob` still confine to the primary workspace root. |
| `system_prompt` | `string` | Overrides the runner's default system prompt for this run. |
| `conversation_id` | `string` | Pin the run to an existing conversation so the agent has prior context. |

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Budget and limits</CardTitle>
</CardHeader>
<CardContent>

| Field | Type | Notes |
|---|---|---|
| `max_steps` | `int` | Cap on LLM turns. `0` = runner default (which itself may be unlimited). Negative values are rejected. |
| `max_turns` | `int` | Cap on assistant turns. Same semantics as `max_steps`. |
| `max_cost_usd` | `float64` | Spending ceiling in USD. When hit, a `run.cost_limit_reached` event fires and the run completes normally (not failed). `0` = unlimited. Negative values are rejected. |
| `reasoning_effort` | `string` | For OpenAI o-series models: `"low"`, `"medium"`, or `"high"`. Empty = provider default. |

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Tools, providers, and context</CardTitle>
</CardHeader>
<CardContent>

| Field | Type | Notes |
|---|---|---|
| `allowed_tools` | `[]string` | Allowlist of tool names. Empty/nil = all tools available. |
| `mcp_servers` | `[]MCPServerConfig` | Per-run MCP server configs. These shadow any global server with the same name for the duration of the run. |
| `allow_fallback` | `bool` | When `true`, two things happen: (1) if the primary provider cannot be resolved at run start (e.g. missing API key), the runner falls back to the server-level default provider instead of failing immediately; (2) if the primary provider returns a transient runtime error (HTTP 429, 500, 502, 503, 504), the runner retries with the next candidate in `fallback_providers`. This is required when using the fake provider via `HARNESS_PROVIDER=fake` — see the startup step. |
| `fallback_providers` | `[]string` | Ordered list of provider names to try when `allow_fallback` is true and the primary fails at runtime. When empty and `allow_fallback` is true, the runner falls back to the server-level default provider if it differs from the primary. |
| `profile` | `string` | TOML profile name to activate. Profiles are read from the runner's `ProfilesDir`. |
| `prompt_profile` | `string` | Prompt profile name (separate from the TOML run profile). |
| `agent_id` | `string` | Label identifying the agent instance (appears in events and audit logs). |
| `agent_intent` | `string` | High-level intent label used for prompt routing. |
| `task_context` | `string` | Additional context injected into the run. |
| `dynamic_rules` | `[]DynamicRule` | Pattern-triggered rules injected into the system prompt on demand. Zero token cost until the trigger fires. |
| `tenant_id` | `string` | Tenant isolation. When auth is enabled, must match the API key's tenant or be omitted. |

</CardContent>
</Card>

### Permissions

The `permissions` field controls the two-axis permission model. It is an object — **not** top-level fields:

```json
{
  "permissions": {
    "sandbox": "workspace",
    "approval": "destructive"
  }
}
```

<Callout type="warning">
`sandbox` and `approval` are nested inside the `permissions` object. They are not top-level `RunRequest` fields. Omitting the `permissions` block entirely is equivalent to `{"sandbox": "unrestricted", "approval": "none"}` — the agent runs unsandboxed with no approval gate. See [Tools and Permissions](/docs/concepts/tools-and-permissions) for a full explanation of what each value means.
</Callout>

| Field | Valid values | Default |
|---|---|---|
| `sandbox` | `"unrestricted"`, `"local"`, `"workspace"` | `"unrestricted"` |
| `approval` | `"none"`, `"destructive"`, `"all"` | `"none"` |

### What the server sets (never send this)

`initiator_api_key_prefix` is populated by the server from the auth context and written to the audit log. It is excluded from JSON deserialization (`json:"-"`) — any value you send in the body is silently ignored.

---

## Controlling a run

Once a run is in progress you can influence or inspect it through the control endpoints. All of these require the `runs:write` scope (or `admin`).

<Tabs defaultValue="cancel">
<TabsList>
  <TabsTrigger value="cancel">Cancel</TabsTrigger>
  <TabsTrigger value="steer">Steer</TabsTrigger>
  <TabsTrigger value="continue">Continue</TabsTrigger>
  <TabsTrigger value="compact">Compact</TabsTrigger>
  <TabsTrigger value="approve">Approve / Deny</TabsTrigger>
  <TabsTrigger value="input">Input</TabsTrigger>
</TabsList>

<TabsContent value="cancel">

#### POST `/v1/runs/{id}/cancel`
Request cooperative cancellation. The agent finishes its current step, then stops.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/cancel
```

Response:

```json
{"status": "cancelling"}
```

A `run.cancelled` terminal event will follow on the event stream.

</TabsContent>

<TabsContent value="steer">

#### POST `/v1/runs/{id}/steer`
Inject a message into an **active** run without starting a new conversation turn. Use this to redirect the agent mid-flight.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/steer \
  -H "Content-Type: application/json" \
  -d '{"prompt": "focus on the auth module, skip the tests for now"}'
```

Response:

```json
{"status": "accepted"}
```

A `steering.received` event will appear on the event stream.

</TabsContent>

<TabsContent value="continue">

#### POST `/v1/runs/{id}/continue`
Start a **new run** in the same conversation. The conversation history from the referenced run is carried forward as context.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/continue \
  -H "Content-Type: application/json" \
  -d '{"prompt": "now add tests for everything you just wrote"}'
```

Response (HTTP 202):

```json
{"run_id": "run_def456", "status": "queued"}
```

The continuation run is created with status `"queued"` and transitions to `"running"` once a worker slot is available. The body also accepts `allowed_tools` and `permissions` to change the tool set or permission model for the continuation.

</TabsContent>

<TabsContent value="compact">

#### POST `/v1/runs/{id}/compact`
Trigger in-memory context compaction — summarize and remove older messages to free up context window space.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/compact \
  -H "Content-Type: application/json" \
  -d '{"keep_last": 10}'
```

Response:

```json
{"ok": true, "messages_removed": 42}
```

</TabsContent>

<TabsContent value="approve">

#### POST `/v1/runs/{id}/approve` and `/deny`
When a run is waiting for operator approval (status `waiting_for_approval`, event `tool.approval_required`), approve or deny the pending tool call.

```bash
# Approve
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/approve

# Deny
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/deny
```

Responses:

```json
{"status": "approved"}
{"status": "denied"}
```

Approval gates are enabled by setting `permissions.approval` to `"destructive"` or `"all"` in the `RunRequest`. See [Tools and Permissions](/docs/concepts/tools-and-permissions).

</TabsContent>

<TabsContent value="input">

#### GET and POST `/v1/runs/{id}/input`
When the agent calls the `ask_user_question` tool the run enters `waiting_for_user` status and emits a `run.waiting_for_user` event. Poll `GET /v1/runs/{id}/input` to retrieve the pending question, then `POST` your answers.

```bash
# Get the pending question
curl -s http://localhost:8080/v1/runs/run_abc123/input

# Submit answers
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/input \
  -H "Content-Type: application/json" \
  -d '{"answers": {"q1": "yes, overwrite the file"}}'
```

`answers` is a `map[string]string` where keys match the question IDs in the `run.waiting_for_user` payload.

</TabsContent>
</Tabs>

### Additional inspection endpoints

| Method | Path | What it returns |
|---|---|---|
| `GET` | `/v1/runs/{id}` | Full run object including status, output, error, usage |
| `GET` | `/v1/runs/{id}/summary` | Post-run telemetry: steps, tokens, cost, tool calls, cache hit rate |
| `GET` | `/v1/runs/{id}/context` | Context window status (token usage, headroom) for an active run |
| `GET` | `/v1/runs/{id}/todos` | Todo list for the run |
| `PUT` | `/v1/runs/{id}/todos` | Replace the todo list |

---

## Errors and limits

### Error envelope

All error responses use a consistent JSON envelope:

```json
{"error": {"code": "invalid_request", "message": "prompt is required"}}
```

Scope errors use a slightly different shape:

```json
{"error": "insufficient_scope", "required": "runs:write"}
```

### Request body limits

| Limit | Value | Applies to |
|---|---|---|
| Default max body | 1 MiB | All endpoints except replay |
| Replay max body | 4 MiB | `POST /v1/runs/replay` only |
| Handler timeout | 30 seconds | All non-streaming endpoints |

Streaming endpoints (`/events`, `/stream`, `/wait` path suffixes) bypass the 30-second handler timeout.

### Authentication

By default, harnessd validates a Bearer token on every request:

```bash
curl -H "Authorization: Bearer <your-token>" \
  http://localhost:8080/v1/runs
```

For SSE EventSource clients that cannot set custom headers, pass the token as a query parameter instead:

```
GET /v1/runs/{id}/events?token=<your-token>
```

Auth is disabled when `HARNESS_AUTH_DISABLED=true` is set, or when no database store is configured (i.e., `HARNESS_RUN_DB` is not set). There are three permission scopes:

| Scope | Grants |
|---|---|
| `runs:read` | GET on runs, conversations, models, skills |
| `runs:write` | POST/PUT/DELETE mutations; also satisfies `runs:read` |
| `admin` | Superscope; satisfies any check |

<Callout type="warning">
Auth is **implicitly disabled** when `HARNESS_RUN_DB` is not set, even without `HARNESS_AUTH_DISABLED=true`. There is no key store to validate against, so all requests are accepted unauthenticated. This is intentional for local development but set `HARNESS_RUN_DB` before exposing the server to a network.
</Callout>

### HTTP server timeouts

These are hardcoded at the `http.Server` level and are not configurable via environment variables:

| Timeout | Value |
|---|---|
| `ReadTimeout` | 60s |
| `ReadHeaderTimeout` | 10s |
| `IdleTimeout` | 120s |

---

## Event reference (quick look-up)

A run emits dozens of event types. Here are the most commonly consumed ones. For a complete catalog with payload shapes, see [Events](/docs/concepts/events).

| Event | When |
|---|---|
| `run.started` | Run begins executing |
| `run.queued` | Worker pool is full; run will start when a slot opens |
| `run.completed` | Terminal: run finished successfully |
| `run.failed` | Terminal: run failed |
| `run.cancelled` | Terminal: run was cancelled |
| `run.waiting_for_user` | Agent called `ask_user_question`; run paused |
| `run.cost_limit_reached` | `max_cost_usd` ceiling hit; run continues to completion |
| `assistant.message.delta` | Streaming text token from the assistant |
| `assistant.message` | Full assistant message (no tool calls in this turn) |
| `tool.call.started` | A tool is being executed |
| `tool.call.completed` | A tool finished (success or error) |
| `tool.approval_required` | Tool waiting for operator approval |
| `usage.delta` | Per-step token and cost accounting |

The `run.completed` payload always includes `output`, `usage_totals`, and `cost_totals`. The `run.failed` payload includes `error` and, when the reason is `max_steps_reached` or `max_turns_exhausted`, a `reason` field explaining why.

---

## Next steps

- [Events](/docs/concepts/events) — full event catalog with payload shapes for every event type
- [Tools and Permissions](/docs/concepts/tools-and-permissions) — sandbox scopes, approval policies, and tool allowlists
- [Providers and Models](/docs/concepts/providers-and-models) — which model IDs and `provider_name` values are valid
- [Key-Free Testing](/docs/getting-started/key-free-testing) — running the full pipeline without an API key
