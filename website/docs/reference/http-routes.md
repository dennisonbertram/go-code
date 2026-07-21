---
title: "HTTP Route Reference"
sidebar_label: "HTTP Routes"
sidebar_position: 2
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

`harnessd` exposes a REST + Server-Sent Events (SSE) API over a single TCP port (default `:8080`). Every agent run, subagent, scheduled job, script workflow, and relay worker is reachable through this surface. This page covers the primary public route inventory — method, scope, request/response shape, and notes on when a route requires an optional server component. Note: the definition-based workflow routes (`/v1/workflows*`, `/v1/workflow-runs/*`) are registered but not yet fully documented here.

**Key terms used throughout this page:**

- **Scope** — the API key permission level required. Three levels exist: `runs:read` (read-only access), `runs:write` (mutations; also satisfies `runs:read`), and `admin` (superscope; satisfies any check). See [Authentication](#authentication).
- **501** — a route returns `501 Not Implemented` when the server option or backing store it depends on is not configured (for example, `GET /v1/runs` requires a persistent store, and relay routes require `HARNESS_RELAY_DB`).
- **SSE** — Server-Sent Events. Streaming endpoints return `Content-Type: text/event-stream` and bypass the 30-second handler timeout.

<Callout type="warning">
Several routes in this table are only active when the corresponding server option is wired at startup. Routes marked "requires store" need `HARNESS_RUN_DB` to be set. Routes for skills, subagents, script-workflows, and relay workers return 501 when their respective managers are not configured.
</Callout>

---

## Authentication

All routes (except `/healthz` and the webhook routes) pass through `authMiddleware`.

**How to authenticate:** pass `Authorization: Bearer <token>` on every request. SSE clients that cannot set request headers may use the `?token=<token>` query parameter instead.

**When auth is disabled:** set `HARNESS_AUTH_DISABLED=true`, or start without `HARNESS_RUN_DB` — no store means no key validation, so auth is implicitly off.

| Scope | Constant | What it grants |
|-------|----------|---------------|
| `runs:read` | `store.ScopeRunsRead` | GET on runs, conversations, models, skills |
| `runs:write` | `store.ScopeRunsWrite` | POST/PUT/DELETE mutations; also satisfies `runs:read` |
| `admin` | `store.ScopeAdmin` | Superscope; satisfies any scope check |

**Tenant isolation:** API keys carry a `tenant_id`. Run-creation and run-lookup routes enforce that the authenticated tenant matches the resource tenant. Cross-tenant access returns HTTP 404 (not 403) to prevent resource-existence disclosure. The empty tenant `""` is normalized to `"default"`.

---

## Health

| Method | Path | Auth | Response |
|--------|------|------|----------|
| GET | `/healthz` | None | `{"status":"ok"}` |

---

## Runs and Conversations

Runs are the core unit of execution in `harnessd`. A run accepts a prompt, invokes an LLM agent with a set of tools, streams back events, and reaches a terminal state.

### Two execution models

<Callout type="info">
`harnessd` has two distinct ways to run an agent. Choose based on whether you need streaming or a blocking call:

- **`POST /v1/runs`** — asynchronous streaming model. Returns HTTP 202 immediately with a `run_id`. The caller streams real-time events from `GET /v1/runs/{id}/events`. This is the primary execution path used by the CLI and TUI.
- **`POST /v1/agents`** — synchronous single-shot model. Blocks until the agent finishes (default timeout 120s, max 600s) and returns `{"output":"…","summary":"…","duration_ms":N}` in one response. Simpler to integrate when you do not need event streaming.
</Callout>

### Run routes

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `POST` | `/v1/runs` | `runs:write` | Start a run. Body: `RunRequest`. Returns HTTP 202 `{"run_id":"…","status":"…"}`. |
| `GET` | `/v1/runs` | `runs:read` | List runs. Query: `conversation_id`, `status`, `tenant_id`. **Requires persistent store (501 without it).** |
| `GET` | `/v1/runs/{id}` | `runs:read` | Get run by ID. Checks in-memory first, then store fallback. Returns a `Run` object. |
| `GET` | `/v1/runs/{id}/events` | `runs:read` | **SSE stream.** Supports `Last-Event-ID` reconnection. Terminal events close the stream. |
| `GET` | `/v1/runs/{id}/summary` | `runs:read` | Post-run telemetry: steps, tokens, cost, tool calls, cache hit rate. |
| `GET` | `/v1/runs/{id}/context` | `runs:read` | Context window status for an active run. |
| `GET` | `/v1/runs/{id}/input` | `runs:read` | Get a pending `ask_user_question` request. |
| `POST` | `/v1/runs/{id}/input` | `runs:write` | Submit answers. Body: `{"answers": {"q_id": "answer"}}`. Returns HTTP 202. |
| `GET` | `/v1/runs/{id}/todos` | `runs:read` | Get the todo list for the run. |
| `PUT` | `/v1/runs/{id}/todos` | `runs:write` | Replace the todo list. Body: `{"todos": [...]}`. |
| `POST` | `/v1/runs/{id}/continue` | `runs:write` | Start a new run in the same conversation. Body: `{"prompt":"…","allowed_tools":[],"permissions":{}}`. Returns HTTP 202. |
| `POST` | `/v1/runs/{id}/steer` | `runs:write` | Inject a steering message into an active run. Body: `{"prompt":"…"}`. Returns HTTP 202 `{"status":"accepted"}`. |
| `POST` | `/v1/runs/{id}/compact` | `runs:write` | Trigger in-memory context compaction. Body: `{"mode":…,"keep_last":N}`. Returns `{"ok":true,"messages_removed":N}`. |
| `POST` | `/v1/runs/{id}/cancel` | `runs:write` | Request cooperative cancellation. Returns `{"status":"cancelling"}`. |
| `POST` | `/v1/runs/{id}/approve` | `runs:write` | Approve a pending tool call (requires `ApprovalBroker`). Returns `{"status":"approved"}`. |
| `POST` | `/v1/runs/{id}/deny` | `runs:write` | Deny a pending tool call. Returns `{"status":"denied"}`. |
| `POST` | `/v1/runs/replay` | `runs:write` | Replay a recorded rollout. Body fields: `rollout_path` (required), `mode` (`"simulate"` or `"fork"`, required), `fork_step` (required for fork mode), `detect_drift` (bool, simulate only). |

### Conversation routes

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/conversations/` | `runs:read` | List conversations. Query: `workspace`, `tenant_id`, `limit` (default 50), `offset`. Delegates to search when `q=` is present. |
| `GET` | `/v1/conversations/search` | `runs:read` | Full-text search. Required: `q=`. Optional: `limit` (default 20), `tenant_id`. |
| `GET` | `/v1/conversations/{id}/messages` | `runs:read` | In-memory messages for the conversation. |
| `GET` | `/v1/conversations/{id}/runs` | `runs:read` | All runs for a conversation. |
| `GET` | `/v1/conversations/{id}/export` | `runs:read` | JSONL (ndjson) export of all messages. |
| `POST` | `/v1/conversations/{id}/compact` | `runs:write` | Replace early messages with a summary. Body: `{"keep_from_step":N,"summary":"…","role":"system"}`. Auto-generates summary via LLM when `summary` is omitted. |
| `POST` | `/v1/conversations/{id}/fork` | `runs:write` | Duplicate the conversation — full message history included — under a server-minted ID. No body. Returns `{"conversation_id":"…","forked_from":"…","message_count":N}`. The fork inherits the source's workspace and tenant (cross-tenant requests are rejected with 404); pinned flag and token/cost counters start at zero. Works for persisted conversations and ones held only in server memory (mid-run), capturing the latest in-memory view. 404 unknown source; 405 for non-POST; 501 when conversation persistence is not configured. Afterwards the two conversations diverge independently. |
| `POST` | `/v1/conversations/cleanup` | `runs:write` | Bulk-delete old conversations. Body: `{"max_age_days":30}`. Returns `{"deleted":N}`. |
| `DELETE` | `/v1/conversations/{id}` | `runs:write` | Delete a conversation. |

### SSE event stream

`GET /v1/runs/{id}/events` returns `Content-Type: text/event-stream; charset=utf-8`. Each frame:

```
id: <run_id>:<seq>
retry: 3000
event: <event_type>
data: {"id":"…","run_id":"…","type":"…","timestamp":"…","payload":{…}}
```

**Reconnection:** include `Last-Event-ID: <run_id>:<seq>` to replay events the client missed.

**Keepalive:** the server sends `: ping` comment lines every `HARNESS_SSE_KEEPALIVE_SECONDS` seconds (default 15) to keep the connection alive through proxies.

**Terminal events** that close the stream: `run.completed`, `run.failed`, `run.cancelled`.

---

## Catalog, Models, Providers, Summarize

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/models` | `runs:read` | Returns `{"models":[{id,provider,aliases,input_cost_per_mtok,output_cost_per_mtok}]}`. |
| `GET` | `/v1/providers` | `runs:read` | Returns `{"providers":[{name,configured,api_key_env,base_url,model_count}]}`. |
| `PUT` | `/v1/providers/{name}/key` | `admin` | Set a provider API key at runtime. Body: `{"key":"…"}`. Returns HTTP 204. |
| `POST` | `/v1/summarize` | `runs:write` | LLM-generated summary of a message list. Body: `{"messages":[…],"system":"…"}`. Returns `{"summary":"…"}`. |

---

## Skills, Agents, Subagents, Profiles

### Skills

<Callout type="warning">
`GET /v1/skills` and related routes return 501 when the skills system is not configured.
</Callout>

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/skills` | `runs:read` | List all skills. Returns `{"skills":[…]}`. |
| `GET` | `/v1/skills/{name}` | `runs:read` | Get skill by name. |
| `POST` | `/v1/skills/{name}/verify` | `runs:write` | Mark skill as verified. Body: `{"verified_by":"api"}`. |

### Agents (synchronous)

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `POST` | `/v1/agents` | `runs:write` | **Synchronous single-shot execution.** Body: `{"prompt":"…"}` or `{"skill":"…","skill_args":"…"}` plus optional `allowed_tools`, `timeout_seconds` (default 120, max 600). Blocks until done; returns `{"output":"…","summary":"…","duration_ms":N}`. |

### Subagents

<Callout type="warning">
All subagent routes return 501 when `ServerOptions.SubagentManager` is nil.
</Callout>

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/subagents` | `runs:read` | List subagents. Returns `{"subagents":[…]}`. |
| `POST` | `/v1/subagents` | `runs:write` | Create a subagent. Body: `subagents.Request`. Returns HTTP 201 with the subagent object. |
| `GET` | `/v1/subagents/{id}` | `runs:read` | Get subagent by ID. |
| `DELETE` | `/v1/subagents/{id}` | `runs:write` | Delete subagent. Returns HTTP 409 Conflict if the subagent is still active. |
| `POST` | `/v1/subagents/{id}/wait` | `runs:read` | Long-poll until the subagent reaches a terminal state (polls at 200ms). |
| `POST` | `/v1/subagents/{id}/cancel` | `runs:write` | Cancel subagent. Returns `{"id":"…","status":"cancelling"}`. |

### Profiles

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/profiles` | `runs:read` | List all profiles across project/user/built-in tiers. Returns `{"profiles":[…],"count":N}`. |
| `GET` | `/v1/profiles/{name}` | `runs:read` | Get profile by name. |
| `POST` | `/v1/profiles/{name}` | `runs:write` | Create a user profile. Returns HTTP 201. Returns 409 Conflict for built-in names. Requires `ProfilesDir` configured (501 otherwise). |
| `PUT` | `/v1/profiles/{name}` | `runs:write` | Update a user-tier profile. Returns 403 for built-in names. |
| `DELETE` | `/v1/profiles/{name}` | `runs:write` | Delete a user-tier profile. Returns 403 for built-ins. |

---

## Cron, Checkpoints, Recipes, MCP, Networks, Script Workflows, Relay

### Cron jobs

`harnessd` embeds a cron scheduler by default (backed by SQLite at `<workspace>/.harness/cron.db`, concurrency cap 5). Set `HARNESS_CRON_URL` to delegate to a remote `cronsd` instance instead.

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/cron/jobs` | `runs:read` | List cron jobs (tenant-filtered). |
| `POST` | `/v1/cron/jobs` | `runs:write` | Create job. Body: `{"name":"…","schedule":"*/5 * * * *","execution_type":"shell","execution_config":"{\"command\":\"…\"}","timeout_seconds":30}`. |
| `GET` | `/v1/cron/jobs/{id}` | `runs:read` | Get job by ID or name. |
| `PATCH` | `/v1/cron/jobs/{id}` | `runs:write` | Update job. Body: any subset of create fields; `status` accepts only `"active"` or `"paused"`. |
| `DELETE` | `/v1/cron/jobs/{id}` | `runs:write` | Soft-delete job. Returns HTTP 204. |
| `POST` | `/v1/cron/jobs/{id}/pause` | `runs:write` | Pause job. |
| `POST` | `/v1/cron/jobs/{id}/resume` | `runs:write` | Resume paused job. |

### Checkpoints

Checkpoints allow a paused run to be resumed with additional context — for example, after a human review step.

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/checkpoints/{id}` | `runs:read` | Get checkpoint record. |
| `POST` | `/v1/checkpoints/{id}/resume` | `runs:write` | Resume a paused checkpoint. Body: `{"payload":{"key":"value"}}`. Returns `{"status":"resumed"}`. |

### Recipes

Recipes are reusable run definitions loaded from `HARNESS_RECIPES_DIR`.

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/recipes` | `runs:read` | List recipe definitions. |
| `GET` | `/v1/recipes/{name}` | `runs:read` | Get a specific recipe. |
| `GET` | `/v1/recipes/{name}/schema` | `runs:read` | Returns `{"parameters":{…}}` — the recipe's parameter schema. |

### MCP servers

`harnessd` can manage connections to external MCP servers at runtime. The MCP HTTP endpoint (`/mcp`) is a separate JSON-RPC surface on the same port — see the MCP documentation for details.

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/mcp/servers` | `runs:read` | List connected MCP servers with their tool lists. |
| `POST` | `/v1/mcp/servers` | `admin` | Connect a new MCP server at runtime. Body: `{"url":"…","name":"…"}`. |

### Networks

<Callout type="info">
Networks are agent-graph definitions loaded from `HARNESS_NETWORKS_DIR`. The `/v1/networks` routes are always registered but return 501 when no networks engine is configured. Set `HARNESS_NETWORKS_DIR` (which wires `ServerOptions.Networks`) to activate them. Detailed network documentation is not yet published; this entry covers the route group's existence and the env var that activates it.
</Callout>

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/networks` | `runs:read` | List network definitions. Returns 501 when networks engine is not configured. |
| `GET` | `/v1/networks/{name}` | `runs:read` | Get a specific network definition. |
| `POST` | `/v1/networks/{name}/runs` | `runs:write` | Start a network run. Body: `{"input":{…}}`. Returns HTTP 202 `{"run_id":"…","status":"…"}`. |

### Script workflows

Script workflows are Go `func(ctx *Context) (any, error)` functions registered with the workflow engine and exposed over HTTP with SSE event streaming.

<Callout type="warning">
All script-workflow routes return 501 when `ServerOptions.ScriptWorkflows` is nil.
</Callout>

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/script-workflows` | `runs:read` | List registered script workflows. |
| `GET` | `/v1/script-workflows/{name}` | `runs:read` | Get workflow metadata by name. |
| `POST` | `/v1/script-workflows/{name}/runs` | `runs:write` | Start a workflow run. Body: `{"args":{…}}`. Returns HTTP 202 `{"run_id":"wf_…","status":"running","workflow_name":"…"}`. |
| `GET` | `/v1/script-workflow-runs/{id}` | `runs:read` | Get run status and result. Returns `{"id":"wf_…","workflow_name":"…","status":"…","result_json":"…","error":"…"}`. |
| `GET` | `/v1/script-workflow-runs/{id}/events` | `runs:read` | **SSE stream** of workflow events. Historical events are replayed before live ones. Closes on `workflow.completed` or `workflow.failed`. |
| `POST` | `/v1/script-workflow-runs/{id}/resume` | `runs:write` | Resume a failed run. Body: `{"args":{…}}`. Returns HTTP 202 `{"run_id":"wf_…","status":"running"}`. |

### Relay workers

Go Relay is the multi-location control plane that routes work across registered execution environments. The worker CRUD and heartbeat routes are the HTTP surface for worker management.

<Callout type="warning">
All relay routes return 501 when `HARNESS_RELAY_DB` is not set.
</Callout>

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET` | `/v1/relay/workers` | `runs:read` | List registered workers. Query: `status`, `location_type`, `trust_tier`, `tenant_id`. |
| `POST` | `/v1/relay/workers` | `runs:write` | Register a new worker. Setting `trust_tier: "privileged"` requires `admin` scope. |
| `GET` | `/v1/relay/workers/{id}` | `runs:read` | Get worker by ID. |
| `PUT` | `/v1/relay/workers/{id}` | `runs:write` | Update worker fields. |
| `DELETE` | `/v1/relay/workers/{id}` | `runs:write` | Deregister worker. |
| `POST` | `/v1/relay/workers/{id}/heartbeat` | `runs:write` | Submit heartbeat. Body: `{"load":N,"status":"online"}`. `status` must be `"online"` or `"draining"`. Workers not heartbeating within 30 seconds transition to `"stale"`. |

### Webhooks

Webhook routes bypass Bearer auth entirely — they authenticate via HMAC signature headers. Enable each webhook by setting the corresponding secret env var.

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| `POST` | `/v1/external/trigger` | HMAC (`X-Trigger-Signature`) | Source-agnostic trigger. Requires `ValidatorRegistry`. Actions: `"start"`, `"steer"`, `"continue"`. |
| `POST` | `/v1/webhooks/github` | HMAC-SHA256 (`X-Hub-Signature-256`) | Requires `GITHUB_WEBHOOK_SECRET`. |
| `POST` | `/v1/webhooks/slack` | HMAC-SHA256 (`X-Slack-Signature`) | Requires `SLACK_SIGNING_SECRET`. |
| `POST` | `/v1/webhooks/linear` | HMAC-SHA256 (`X-Linear-Signature`) | Requires `LINEAR_WEBHOOK_SECRET`. |

### Search

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `POST` | `/v1/search/code` | `runs:write` | Sourcegraph code search proxy. Requires `HARNESS_SOURCEGRAPH_ENDPOINT` and `HARNESS_SOURCEGRAPH_TOKEN`. |

---

## Schemas

### `RunRequest` — `POST /v1/runs` body

Source: `internal/harness/types.go`.

```json
{
  "prompt": "write a hello world in Go",
  "model": "gpt-4o",
  "provider_name": "openai",
  "workspace_type": "",
  "allow_fallback": false,
  "fallback_providers": [],
  "system_prompt": "",
  "tenant_id": "",
  "conversation_id": "",
  "agent_id": "",
  "agent_intent": "",
  "task_context": "",
  "prompt_profile": "",
  "prompt_extensions": {
    "behaviors": [],
    "talents": [],
    "skills": [],
    "custom": ""
  },
  "max_steps": 0,
  "max_turns": 0,
  "max_cost_usd": 0.0,
  "reasoning_effort": "",
  "allowed_tools": [],
  "mcp_servers": [
    {"name": "sqlite", "command": "uvx", "args": ["mcp-server-sqlite", "--db-path", "/tmp/my.db"]}
  ],
  "dynamic_rules": [],
  "profile": "",
  "parent_context_handoff": null,
  "permissions": {
    "sandbox": "unrestricted",
    "approval": "none"
  },
  "role_models": {
    "primary": "",
    "summarizer": ""
  }
}
```

Selected field notes:

- `prompt` is required for a direct run. Omit when using `profile` + `skill` via `POST /v1/agents`.
- `workspace_type` accepts: `""` (server default), `"local"`, `"worktree"`, `"container"`, `"vm"`.
- `max_steps` and `max_turns`: `0` means runner default/unlimited; negative values are rejected.
- `max_cost_usd`: `0` means unlimited; the run emits `run.cost_limit_reached` on breach (run still completes normally).
- `permissions.sandbox`: `"unrestricted"` (default), `"local"`, or `"workspace"`.
- `permissions.approval`: `"none"` (default), `"destructive"`, or `"all"`.
- `initiator_api_key_prefix` is server-populated from the auth context — it is never accepted from the request body.

### `Run` — `GET /v1/runs/{id}` response

Source: `internal/harness/types.go`.

```json
{
  "id": "run_abc123",
  "prompt": "write a hello world in Go",
  "model": "gpt-4o",
  "provider_name": "openai",
  "status": "completed",
  "output": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"Hello, World!\") }",
  "error": "",
  "usage_totals": {},
  "cost_totals": {},
  "tenant_id": "default",
  "conversation_id": "conv_xyz",
  "agent_id": "",
  "created_at": "2026-06-28T10:00:00Z",
  "updated_at": "2026-06-28T10:00:05Z"
}
```

**`status` values:** `running`, `completed`, `failed`, `cancelled`, `queued`, `waiting_for_user`, `waiting_for_approval`.

### `RunSummary` — `GET /v1/runs/{id}/summary` response

Source: `internal/harness/types.go`.

```json
{
  "run_id": "run_abc123",
  "status": "completed",
  "steps_taken": 3,
  "total_prompt_tokens": 1200,
  "total_completion_tokens": 400,
  "total_cost_usd": 0.0018,
  "cost_status": "available",
  "tool_calls": [
    {"tool_name": "write", "step": 2}
  ],
  "cache_hit_rate": 0.0,
  "error": ""
}
```

### Error envelope

All non-streaming errors follow a consistent shape:

```json
{"error": {"code": "not_found", "message": "run not found"}}
```

Scope errors use a slightly different shape:

```json
{"error": "insufficient_scope", "required": "runs:write"}
```

### Request limits and timeouts

| Setting | Default | Notes |
|---------|---------|-------|
| Max request body | 1 MiB | Applies to all non-replay endpoints |
| Max body for replay | 4 MiB | `POST /v1/runs/replay` only |
| Handler timeout | 30s | Non-streaming endpoints only |
| SSE keepalive | 15s | Controlled by `HARNESS_SSE_KEEPALIVE_SECONDS` |

Streaming paths (`/events`, `/stream`, `/wait` suffix) bypass the 30-second handler timeout.

---

## Key environment variables

| Env var | Default | Effect |
|---------|---------|--------|
| `HARNESS_ADDR` | `:8080` | HTTP listen address |
| `HARNESS_AUTH_DISABLED` | `""` (false) | Set `"true"` to bypass all Bearer auth |
| `HARNESS_RUN_DB` | `""` | SQLite path; enables `GET /v1/runs` and auth |
| `HARNESS_RELAY_DB` | `""` | SQLite path; enables `/v1/relay/workers` routes |
| `HARNESS_RECIPES_DIR` | `""` | Enables `/v1/recipes` routes |
| `HARNESS_NETWORKS_DIR` | `""` | Enables `/v1/networks` routes |
| `HARNESS_SSE_KEEPALIVE_SECONDS` | `15` | SSE ping interval |
| `HARNESS_CRON_URL` | `""` | Set to delegate cron to external `cronsd`; empty uses embedded scheduler |
| `GITHUB_WEBHOOK_SECRET` | `""` | Enables `/v1/webhooks/github` |
| `SLACK_SIGNING_SECRET` | `""` | Enables `/v1/webhooks/slack` |
| `LINEAR_WEBHOOK_SECRET` | `""` | Enables `/v1/webhooks/linear` |

---

## Quick start (key-free smoke)

The simplest way to exercise the API without a real LLM key. The fake provider requires a turns file (`HARNESS_FAKE_TURNS`) — starting without one is a fatal error.

```bash
# Write a fake turns file (required by HARNESS_PROVIDER=fake)
cat > /tmp/turns.json <<'EOF'
[{"content":"smoke ok","usage":{"prompt":100,"completion":50},"cost_usd":0.001,"cost_status":"available"}]
EOF

# Start harnessd with the fake provider
HARNESS_ADDR=":8080" \
HARNESS_AUTH_DISABLED=true \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/turns.json \
  go run ./cmd/harnessd &

# Start a run
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt":"say hello"}' | jq .

# Stream its events (replace run_abc123 with the run_id from above)
curl -s -N http://localhost:8080/v1/runs/run_abc123/events

# Check health
curl -s http://localhost:8080/healthz
```

Alternatively, run `bash scripts/run-bench-smoke.sh` which handles the turns file, build, and health-check automatically.

---

Next steps: see the [Events reference](/docs/reference/events-catalog) for the full SSE event catalog, or the [Script Workflows](/docs/workflows/workflow-engine) guide to learn how to register and run Go-authored pipelines.
