---
title: "Exposing harnessd as an MCP Server"
sidebar_label: "harnessd as MCP Server"
sidebar_position: 5
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The Model Context Protocol (MCP) is a JSON-RPC 2.0 based standard for connecting AI models to tools and data sources. `harnessd` supports MCP in _both_ directions: it can consume tools from external MCP servers (acting as an MCP client), and it can expose itself as an MCP server so that Claude Desktop and other MCP hosts can drive it directly.

This page covers the second direction — `harnessd` as an MCP server. There are three distinct surfaces, each suited to a different deployment pattern:

| Surface | Transport | Best for |
|---------|-----------|----------|
| **HTTP MCP server (`/mcp`)** | HTTP (JSON-RPC POST + SSE GET) | Programmatic clients, CI integrations, agents calling agents |
| **stdio MCP server (`--mcp`)** | stdio (JSON-RPC over stdin/stdout) | Running `harnessd` directly as an MCP tool inside a host process |
| **`harness-mcp` proxy** | stdio → HTTP proxy | Connecting Claude Desktop to an _already-running_ `harnessd` instance |

<Callout variant="warning">
These are three separate surfaces with different tool sets and use cases. Connecting Claude Desktop to `harnessd --mcp` (stdio mode) exposes the full harness tool catalog. Connecting via `harness-mcp` exposes five task-management tools that drive the harnessd REST API. Choose based on whether you need the full catalog or a curated run-management interface.
</Callout>

---

## HTTP MCP server (`/mcp`)

When `harnessd` starts in normal HTTP mode, it mounts an MCP server on the same port as the REST API at the path `/mcp`. No extra configuration is needed — the endpoint is always available alongside `/v1/...`.

### Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST /mcp` | JSON-RPC 2.0 | Tool calls, `initialize`, `tools/list` |
| `GET /mcp` | SSE stream | JSON-RPC 2.0 notifications for subscribed runs |

The MCP server advertises protocol version `"2025-11-25"` and identifies itself as `name = "go-agent-harness"`, `version = "1.0"`.

### The 10 tools

<Card>
<CardHeader>
<CardTitle>Tools exposed by the HTTP MCP server</CardTitle>
</CardHeader>
<CardContent>

| Tool | Required arguments | Description |
|------|--------------------|-------------|
| `start_run` | `prompt` | Submit a new agent run; returns `run_id` |
| `get_run_status` | `run_id` | Current status and output |
| `list_runs` | — | List all known runs |
| `steer_run` | `run_id`, `message` | Inject a guidance message into an active run |
| `submit_user_input` | `run_id`, `input` | Respond when a run is paused at `waiting_for_user` |
| `subscribe_run` | `run_id` | Register for SSE notifications; returns `stream_id` |
| `list_conversations` | — | Paginated conversation list (default limit 20) |
| `get_conversation` | `conversation_id` | Full message history for a conversation |
| `search_conversations` | `query` | Full-text search across conversations |
| `compact_conversation` | `conversation_id` | Trigger context compaction on a conversation |

</CardContent>
</Card>

<Callout variant="warning">
The harnessd HTTP MCP server is always constructed via `mcpserver.NewServer` (see `runtime_container.go:188`), which never sets a `ConversationInterface`. `NewServerWithConversations` is not wired into any production code path. As a result, `list_conversations`, `search_conversations`, and `compact_conversation` **always** return `"conversations not available"` through the `/mcp` endpoint — there is no deployment configuration that enables them. The exception is `get_conversation`, which is backed by `runner.ConversationMessages` (via `mcpRunnerAdapter`) and does work.

None of the harnessd MCP server surfaces expose MCP resources (`resources/list` / `resources/read`). The `clientManagerRegistry` used for outbound MCP client calls also returns an empty list for `ListResources` and an error for `ReadResource` (`mcp_setup.go:49-57`).
</Callout>

### SSE notifications

When a client calls `subscribe_run`, the SSE stream from `GET /mcp` delivers JSON-RPC 2.0 notifications as events arrive. Two notification methods are published:

- `run/event` — emitted on non-terminal status changes; includes `run_id`, `event_type: "status_changed"`, and `status`.
- `run/completed` — emitted when a run reaches a terminal state (`"completed"` or `"failed"`); includes `run_id`, `status`, `cost_usd`, and `error`. Note: `cost_usd` is currently hardcoded to `0` in the poller (`poller.go:119`) and does not reflect the run's actual cost — fetch real cost via `get_run_status` or the REST run object instead.

The SSE keepalive ping interval is controlled by `HARNESS_SSE_KEEPALIVE_SECONDS` (default: 15 seconds).

---

## stdio MCP server (`--mcp`)

Running `harnessd --mcp` starts an MCP server over stdin/stdout instead of HTTP. This mode exposes the **full harness tool catalog** — both `TierCore` and `TierDeferred` tools — as MCP tools. Each tool's description includes tier and tag metadata appended in the format `[tier:X tags:Y,Z]`.

This surface is useful when a host process (another agent, an IDE, or a script) wants to launch `harnessd` as a subprocess and interact with it directly through stdio JSON-RPC.

### Workspace resolution order

The workspace root is resolved from these sources, in priority order:

1. `--mcp-workspace` flag
2. `HARNESS_WORKSPACE` environment variable
3. Default: `"."` (the current directory)

### Build and run

```bash
# Build
go build ./cmd/harnessd

# Start in stdio MCP mode (workspace defaults to current directory)
./harnessd --mcp

# Start with an explicit workspace
./harnessd --mcp --mcp-workspace /path/to/workspace
```

In stdio mode `harnessd` does not listen on any TCP port — all communication happens over stdin/stdout. The process exits when the host closes the stdin pipe.

---

## The `harness-mcp` proxy

`harness-mcp` is a standalone binary that bridges a stdio MCP host (such as Claude Desktop) to a `harnessd` instance that is already running over HTTP. The proxy reads JSON-RPC from stdin, translates each call into a REST request against `harnessd`, and writes the JSON-RPC response back to stdout.

This is the recommended path for Claude Desktop integration: keep `harnessd` running as a persistent daemon and configure Claude Desktop to launch `harness-mcp` as a subprocess.

### Architecture

```
Claude Desktop
    │ (stdio JSON-RPC)
    ▼
harness-mcp (StdioTransport → Dispatcher → HarnessClient)
    │ (HTTP REST)
    ▼
harnessd (running at HARNESS_ADDR)
```

The proxy advertises `name = "harness-mcp"`, `version = "1.0.0"` and protocol version `"2025-11-25"`.

### The 5 tools

<Card>
<CardHeader>
<CardTitle>Tools exposed by harness-mcp</CardTitle>
</CardHeader>
<CardContent>

| Tool | Required arguments | Optional arguments | Description |
|------|--------------------|--------------------|-------------|
| `start_run` | `prompt` | `model`, `conversation_id`, `max_steps`, `max_cost_usd` | Start a new agent run |
| `get_run_status` | `run_id` | — | Returns status, messages, `cost_usd`, and error |
| `wait_for_run` | `run_id` | `timeout_seconds` (default 300) | Polls every 2 seconds until the run reaches `completed`, `failed`, or `waiting_for_user` |
| `continue_run` | `run_id`, `prompt` | — | Fetches the previous run's `conversation_id` and starts a new run in that conversation |
| `list_runs` | — | `conversation_id`, `limit` (default 20) | List runs, optionally filtered by conversation |

</CardContent>
</Card>

The proxy makes direct REST calls to `harnessd`:
- `POST /v1/runs` for `start_run`
- `GET /v1/runs/{runID}` for `get_run_status` and `wait_for_run`
- `GET /v1/runs?conversation_id=&limit=` for `list_runs`

### Build the proxy

```bash
go build -o bin/harness-mcp ./cmd/harness-mcp
```

### Configuration

| Environment variable | Default | Description |
|---------------------|---------|-------------|
| `HARNESS_ADDR` | `http://localhost:8080` | Base URL of the running `harnessd` instance |

---

## Claude Desktop registration

To register `harness-mcp` with Claude Desktop, edit `~/Library/Application Support/Claude/claude_desktop_config.json` and add an entry under `mcpServers`:

```json
{
  "mcpServers": {
    "harness": {
      "command": "/path/to/bin/harness-mcp",
      "env": {
        "HARNESS_ADDR": "http://localhost:8080"
      }
    }
  }
}
```

Replace `/path/to/bin/harness-mcp` with the absolute path to the binary you built above. Claude Desktop launches `harness-mcp` as a subprocess when it starts; the proxy connects to `harnessd` at the address specified by `HARNESS_ADDR`.

<Steps>
<Step title="Build harness-mcp">
```bash
go build -o bin/harness-mcp ./cmd/harness-mcp
```
Note the absolute path to the resulting binary.
</Step>
<Step title="Start harnessd">
Start `harnessd` in a separate terminal or as a background service. The proxy expects it to be reachable at `HARNESS_ADDR` (default `http://localhost:8080`).

```bash
OPENAI_API_KEY=sk-... ./harnessd
```
</Step>
<Step title="Edit claude_desktop_config.json">
Add the `mcpServers` entry shown above. Use the absolute path from step 1.
</Step>
<Step title="Restart Claude Desktop">
Quit and reopen Claude Desktop. The "harness" MCP server will appear in the tool list. You can ask Claude to start a run, check run status, or wait for a long-running task to complete.
</Step>
</Steps>

<Callout variant="info">
You can also use `harnessd --mcp` (stdio mode) directly as the Claude Desktop command without the proxy. The difference is that `--mcp` mode exposes the full harness tool catalog, while `harness-mcp` exposes only the five curated run-management tools. The proxy also lets you share one persistent `harnessd` daemon across multiple clients simultaneously.
</Callout>

---

## Choosing the right surface

<Tabs defaultValue="http">
<TabsList>
  <TabsTrigger value="http">HTTP MCP (`/mcp`)</TabsTrigger>
  <TabsTrigger value="stdio">stdio (`--mcp`)</TabsTrigger>
  <TabsTrigger value="proxy">harness-mcp proxy</TabsTrigger>
</TabsList>
<TabsContent value="http">

**Use the HTTP MCP endpoint when:**
- You are integrating from another service or agent over a network connection.
- You need live SSE notifications via `subscribe_run`.
- You want a single `harnessd` process to serve many concurrent MCP clients.

The endpoint lives at `POST /mcp` (tool calls) and `GET /mcp` (SSE) on the same port as the REST API (default 8080). No extra build step is required.

</TabsContent>
<TabsContent value="stdio">

**Use `harnessd --mcp` when:**
- An MCP host (a parent agent or IDE) wants to launch `harnessd` as a subprocess.
- You need access to the full harness tool catalog — not just run management — over MCP.
- You are building a single-client, single-process integration.

</TabsContent>
<TabsContent value="proxy">

**Use `harness-mcp` when:**
- You want Claude Desktop (or any stdio MCP host) to drive a persistent, separately-managed `harnessd` daemon.
- You want a minimal, curated interface (5 run-management tools) rather than the full catalog.
- Multiple clients or processes share one `harnessd` instance.

</TabsContent>
</Tabs>

---

## Next steps

- Understand the events that runs emit in [The Event Model](/docs/concepts/events).
- Learn how authentication works for the `harnessd` HTTP server in [Authentication](/docs/server/authentication).
