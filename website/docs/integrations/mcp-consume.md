---
title: "Consuming External MCP Servers"
sidebar_label: "Consume MCP Servers"
sidebar_position: 1
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The Model Context Protocol (MCP) is a JSON-RPC 2.0 standard for connecting AI models to tools and data sources. `harnessd` is an MCP _client_: when you point it at external MCP servers, their tools appear alongside the built-in harness tools in the agent's tool list. The agent can then call those tools without any special plumbing — it just sees more tools.

This is useful for giving your agent access to domain-specific capabilities you do not want to reimplement: a filesystem server, a database query server, a custom API wrapper, or any existing MCP-compatible tool.

<Callout type="info">
This page covers `harnessd` as an MCP **consumer** (client). For the reverse direction — exposing `harnessd` itself as an MCP server — see [Exposing harnessd as an MCP Server](/docs/server/expose-as-mcp-server).
</Callout>

---

## Four ways to configure servers

You can register MCP servers at four distinct points in the lifecycle. Choose the one (or combination) that fits your deployment:

| Method | When it applies | Scope |
|--------|----------------|-------|
| `HARNESS_MCP_SERVERS` env var | Process startup | All runs |
| `[mcp_servers]` TOML section | Process startup | All runs |
| Profile TOML (`~/.harness/profiles/<name>.toml`) | Run startup | Runs using that profile |
| `mcp_servers` in `POST /v1/runs` body | Per-request | Single run |

### 1 — `HARNESS_MCP_SERVERS` environment variable

Set this variable to a JSON array of server config objects before starting `harnessd`. Each object describes one server.

```bash
# stdio server — command is the MCP server subprocess
export HARNESS_MCP_SERVERS='[
  {"name": "filesystem", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]},
  {"name": "fetch", "command": "uvx", "args": ["mcp-server-fetch"]}
]'

# HTTP server — url points at a running MCP endpoint
export HARNESS_MCP_SERVERS='[
  {"name": "my-server", "url": "http://localhost:3001/mcp"}
]'
```

**Config object fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique logical name for this server |
| `transport` | string | no (inferred) | `"stdio"` or `"http"` |
| `command` | string | one of `command` / `url` | Subprocess path (stdio transport) |
| `args` | array of strings | no | Subprocess arguments |
| `url` | string | one of `command` / `url` | HTTP endpoint URL (`http` or `https` only) |

**Transport inference:** If you omit `transport`, the harness infers it. A config with `command` and no `url` → `stdio`. A config with `url` and no `command` → `http`.

### 2 — TOML config (`[mcp_servers]`)

Add a `[mcp_servers.<name>]` section to any of the TOML config files (`~/.harness/config.toml` or `<workspace>/.harness/config.toml`):

```toml
[mcp_servers.my-tool]
transport = "stdio"
command = "/usr/local/bin/my-mcp-server"
args = ["--verbose"]

[mcp_servers.remote-tool]
transport = "http"
url = "http://localhost:3001/mcp"
```

TOML layers merge additively: entries from a higher-priority layer overwrite same-named entries from lower-priority layers.

### 3 — Profile TOML

A profile TOML file (`~/.harness/profiles/<name>.toml`) can contain its own `[mcp_servers]` section. These servers are **scoped to individual runs** that activate the profile — they are not added to the global server pool. This lets you give specific task profiles their own specialized toolsets.

```toml
# ~/.harness/profiles/db-analyst.toml
[mcp_servers.sqlite]
transport = "stdio"
command = "uvx"
args = ["mcp-server-sqlite", "--db-path", "/data/analytics.db"]
```

Profile files are loaded from `~/.harness/profiles/<name>.toml`. A missing profile file is non-fatal (treated as no extra servers). An invalid profile name (empty, or containing path-traversal characters) is an error.

### 4 — Per-run request body

Pass `mcp_servers` in the `POST /v1/runs` JSON body to attach a server to a single run. The server is created at run start and torn down when the run completes.

```json
{
  "prompt": "Query the database and summarize sales for Q2",
  "mcp_servers": [
    {
      "name": "sqlite",
      "command": "uvx",
      "args": ["mcp-server-sqlite", "--db-path", "/tmp/sales.db"]
    }
  ]
}
```

Using curl against a locally running `harnessd`:

```bash
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "query /tmp/my.db",
    "mcp_servers": [
      {"name": "sqlite", "command": "uvx", "args": ["mcp-server-sqlite", "--db-path", "/tmp/my.db"]}
    ]
  }'
```

**Name collision rules for per-run servers:** A per-run server name that collides with a _globally_ registered server name is rejected with an error. Profile servers can shadow global names for the duration of that run.

---

## Registration priority (TOML wins)

At `harnessd` startup, servers from the TOML config are registered first and take precedence. Servers from `HARNESS_MCP_SERVERS` are registered next; any entry whose name was already registered from TOML is logged and skipped.

In short: **TOML config beats the env var when names collide.**

```
TOML config (highest precedence for globals)
    ↓
HARNESS_MCP_SERVERS env var (duplicate names skipped)
    ↓
Profile servers (scoped to matching runs, can shadow globals)
    ↓
Per-run mcp_servers (scoped to one run, must not collide with globals)
```

---

## Transports

### stdio

The harness launches the MCP server as a subprocess using the `command` and `args` you provide. Communication happens over the subprocess's stdin/stdout as newline-delimited JSON-RPC 2.0. The scanner buffer is 4 MB per line, which is large enough for all normal MCP payloads. Concurrent requests within one session are multiplexed by request ID.

When the run or server registration is torn down, the harness drains pending requests, closes the subprocess's stdin, and waits for the read loop to exit. This shutdown is bounded and idempotent.

### HTTP

The harness sends JSON-RPC 2.0 requests to the URL you specify. The URL scheme must be `http` or `https`. Requests include `Accept: application/json, text/event-stream`; the response may be plain JSON or an SSE stream — both are handled. The HTTP client timeout is 30 seconds and is not currently configurable per-server.

### Protocol handshake and fallback

For both transports, the harness attempts the protocol version `"2025-11-25"` first. If the server returns JSON-RPC error code `-32602` or `-32600` (invalid/unsupported request), the harness retries with `"2024-11-05"`. This means the harness works with both new and older MCP server implementations without manual configuration.

**Connection timing:** Global MCP servers (configured via `HARNESS_MCP_SERVERS`, TOML, or a profile) are connected and initialized eagerly at `harnessd` startup so their tools can be enumerated for the tool catalog. Servers added mid-session via `connect_mcp` or supplied in the per-run `mcp_servers` field are connected on first use — their discovery is deferred until the agent calls `connect_mcp` or until run startup, respectively.

---

## How tools appear to the agent

### Naming convention: `mcp_<server>_<tool>`

Every tool from an external MCP server is added to the agent's tool list under the name:

```
mcp_<server>_<tool>
```

Both `<server>` and `<tool>` are sanitized: lowercased, with `-`, ` `, `/`, and `.` replaced by `_`. There is a single underscore between the prefix, server name, and tool name.

**Example:** The `read_file` tool from the `filesystem` server becomes `mcp_filesystem_read_file`.

<Callout type="warning">
An older runbook (`docs/runbooks/mcp.md`) describes the naming format as `{server_name}__{tool_name}` (double underscore, no prefix). This is incorrect. The code produces `mcp_{server}_{tool}` — single underscore, with the `mcp_` prefix. Rely on the format shown here, not the runbook.
</Callout>

MCP tools are always registered at the **deferred tier**, meaning they are hidden from the LLM's initial tool list and activated on demand via `find_tool`. This keeps the context window from being flooded when many MCP tools are registered. Global MCP tools (discovered at startup from `HARNESS_MCP_SERVERS`, TOML, or profile servers) are tagged `mcp`, `integration`, `external`. Tools registered dynamically via `connect_mcp` or per-run `mcp_servers` additionally carry `dynamic` and `mcp_server:<serverName>`.

### Listing tools at runtime

After `harnessd` is running, you can inspect which MCP servers and tools are registered:

```bash
# List connected MCP servers and their tool lists (requires runs:read scope when auth is enabled)
curl http://localhost:8080/v1/mcp/servers
```

### `list_mcp_resources` and `read_mcp_resource`

Two additional deferred tools let the agent work with MCP _resources_ (data objects exposed by a server, as opposed to callable tools):

| Tool | Parameters | Description |
|------|-----------|-------------|
| `list_mcp_resources` | `mcp_name` | List all resources exposed by the named server |
| `read_mcp_resource` | `mcp_name`, `uri` | Read a resource by its URI |

<Callout type="warning">
In the current production implementation, `list_mcp_resources` returns an empty list and `read_mcp_resource` returns an error. MCP resource support is defined in the interface but not yet implemented in the production `clientManagerRegistry`. Do not rely on these tools returning meaningful data in the current release.
</Callout>

### `connect_mcp` — connecting mid-session

The `connect_mcp` deferred tool lets the agent connect to a new HTTP/SSE MCP server at any point during a run, without restarting `harnessd`. Once connected, the server's tools are immediately available as `mcp_<server>_<tool>` names in the same run.

Required parameter: `url` — the HTTP/SSE endpoint of the MCP server (e.g. `http://localhost:3000/mcp`).

Optional parameter: `server_name` — a display name for the server. If omitted, a name is derived from the URL hostname. Must contain only alphanumeric characters, hyphens, and underscores.

The agent activates `connect_mcp` via `find_tool` and can then call it:

```
find_tool({"query": "select:connect_mcp"})
connect_mcp({"url": "http://localhost:3001/mcp", "server_name": "my-tool"})
```

After the call completes, `mcp_my_tool_<tool>` names appear in the tool list for the rest of the run (hyphens in `server_name` are replaced with underscores in the registered tool prefix).

---

## Quick reference

<Card>
<CardHeader>
<CardTitle>Configuration summary</CardTitle>
</CardHeader>
<CardContent>

| Goal | How |
|------|-----|
| Add a server for all runs | `HARNESS_MCP_SERVERS` env var or `[mcp_servers]` TOML |
| Add a server for a specific agent profile | `[mcp_servers]` in the profile TOML |
| Add a server for one run only | `mcp_servers` field in the `POST /v1/runs` body |
| Connect a server after a run has started | Agent calls `connect_mcp` tool |
| Inspect registered servers at runtime | `GET /v1/mcp/servers` |

</CardContent>
</Card>

---

## Next steps

- See [Exposing harnessd as an MCP Server](/docs/server/expose-as-mcp-server) to learn how to let other MCP clients drive `harnessd`.
- Read [The Tool Catalog](/docs/concepts/tools-and-permissions) for the full list of built-in tools and how tiers work.
- Review [Configuration Reference](/docs/concepts/configuration) for the complete set of `HARNESS_*` environment variables and TOML schema.
