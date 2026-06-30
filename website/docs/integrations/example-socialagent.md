---
title: "Case Study: The socialagent App"
sidebar_label: "Example: socialagent"
sidebar_position: 6
---

import { Callout, Steps, Step, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

`socialagent` is a complete, production-style Go application built on top of `harnessd`. It is a Telegram-facing social networking bot ("The Connector") that receives messages from users, delegates all AI work to a running `harnessd` instance over HTTP, persists conversation state in Postgres, and exposes custom domain tools through an embedded MCP server.

The app lives in `apps/socialagent/` inside the repository and is explicitly written to never import any `internal/` package from the harness. Every harness interaction happens through the public `POST /v1/runs` and `GET /v1/runs/{id}/events` HTTP API. That makes it the canonical example of how an external service should talk to `harnessd`.

---

## Architecture

At a high level, three processes are involved: the `socialagent` HTTP server, the embedded MCP server it starts in-process, and the `harnessd` daemon.

```
Telegram API
    |
    | HTTPS webhook (POST /webhook/telegram)
    v
socialagent  (:8081)          MCP server  (:8082/mcp)
    |                               ^
    | HTTP REST                     |
    | POST /v1/runs                 | mcp_servers config in RunRequest
    | GET  /v1/runs/:id/events      |
    v                               |
harnessd     (:8080) <-------------+
    |
    | OpenAI API calls
    v
OpenAI

socialagent also reads/writes:
    Postgres (:5433)  -- users, user_profiles, activity_log, messages, user_insights
```

Telegram delivers messages to `socialagent` via HTTPS webhook. `socialagent` translates each message into a `RunRequest` and sends it to `harnessd`. The harness calls the LLM, executes any tool calls (including calls back to `socialagent`'s own MCP server), and streams events back. When the run completes, `socialagent` sends the agent's reply to the Telegram user.

<Callout type="warning">
`socialagent` is deliberately constrained to the public HTTP API. It never imports `internal/` packages. This is not a stylistic choice — it models how any external app must interact with `harnessd`. Importing internal packages from outside the module would couple your code to unstable implementation details.
</Callout>

### Package layout

| Package | Path | Purpose |
|---|---|---|
| `config` | `apps/socialagent/config/` | Loads env vars, validates required fields, applies defaults |
| `gateway` | `apps/socialagent/gateway/` | Wires Telegram bot, DB store, and harness client into one HTTP handler |
| `harness` | `apps/socialagent/harness/` | Minimal HTTP client for the `harnessd` REST/SSE API |
| `mcpserver` | `apps/socialagent/mcpserver/` | Embedded MCP server exposing social domain tools |
| `summarizer` | `apps/socialagent/summarizer/` | Async profile summarization via a separate harness conversation |
| `systemprompt` | `apps/socialagent/systemprompt/` | Go `text/template`-based system prompt renderer |
| `safety` | `apps/socialagent/safety/` | Optional Llama Guard HTTP safety screener |
| `telegram` | `apps/socialagent/telegram/` | Telegram Bot API client |
| `db` | `apps/socialagent/db/` | Postgres store (users, profiles, activity, messages, insights) |

---

## Request flow

Tracing one Telegram message through the system illustrates the gateway pattern you would replicate in your own integration.

<Steps>
<Step>

### Webhook authentication and deduplication

The gateway handler at `POST /webhook/telegram` checks the `X-Telegram-Bot-Api-Secret-Token` header against `TELEGRAM_WEBHOOK_SECRET`. When the check fails, the handler still returns HTTP 200 to prevent Telegram from retrying the delivery storm. The update's `update_id` is tracked in a `sync.Map` to drop duplicate deliveries.

</Step>
<Step>

### Immediate 200 acknowledgment

Telegram requires a fast acknowledgment. The gateway returns HTTP 200 to Telegram before doing any work, then dispatches the actual processing to a background goroutine with a 5-minute context timeout.

</Step>
<Step>

### Per-user mutex

Inside the goroutine, the gateway acquires a per-user mutex keyed on the Telegram user ID. This serializes concurrent messages from the same user so that parallel requests never start two harness runs against the same conversation simultaneously.

</Step>
<Step>

### User lookup and system prompt rendering

`store.GetOrCreateUser` maps the Telegram user ID to an internal UUID and a stable `ConversationID`. The system prompt is then rendered via `systemprompt.Render`, which fills a Go `text/template` with the user's display name, profile summary, interests, and whether they are a new user.

</Step>
<Step>

### Build and send the RunRequest

The gateway constructs the request and calls `SendAndWait` on a `*harness.Client`, which blocks until the run reaches a terminal state.

```go
client := harness.NewClient(harnessURL)

req := harness.RunRequest{
    Prompt:         text,
    ConversationID: user.ConversationID,
    SystemPrompt:   renderedPrompt,
    TenantID:       user.ID,
    AllowedTools: []string{
        "compact_history",
        "context_status",
        "mcp_social_search_users",
        "mcp_social_get_user_profile",
        "mcp_social_get_updates",
        "mcp_social_save_insight",
        "mcp_social_get_my_profile",
        "mcp_social_get_community_stats",
        "mcp_social_send_message_to_user",
        "mcp_social_get_my_messages",
    },
}
// Append the embedded MCP server when mcpServerURL is configured:
req.MCPServers = []harness.MCPServer{{Name: "social", URL: g.mcpServerURL}}

result, err := client.SendAndWait(ctx, req)
```

`AllowedTools` limits the agent to exactly the tools it needs. The `mcp_social_` prefix is the harness's naming convention for MCP tools: `mcp_{server}_{tool}`, where `social` is the registered server name.

</Step>
<Step>

### Reply and async follow-up

When `SendAndWait` returns, the gateway sends `result.Output` back to the Telegram user. Two background goroutines then fire: one calls `summarizer.UpdateProfile` to refresh the user's profile via a separate harness conversation, and another logs the activity.

</Step>
</Steps>

---

## The harness client

The `apps/socialagent/harness/` package is a minimal, self-contained HTTP client. There are no transitive harness dependencies — it talks to `harnessd` purely over REST/SSE. This is the pattern you would copy into your own service.

### Core types

```go
// MCPServer describes a single MCP server to attach to an agent run.
type MCPServer struct {
    Name string `json:"name"`
    URL  string `json:"url,omitempty"`
}

// RunRequest is the payload sent to POST /v1/runs.
type RunRequest struct {
    Prompt         string      `json:"prompt"`
    ConversationID string      `json:"conversation_id"`
    SystemPrompt   string      `json:"system_prompt,omitempty"`
    TenantID       string      `json:"tenant_id,omitempty"`
    Model          string      `json:"model,omitempty"`
    MCPServers     []MCPServer `json:"mcp_servers,omitempty"`
    AllowedTools   []string    `json:"allowed_tools,omitempty"`
}
```

### Key methods

| Method | What it does |
|---|---|
| `NewClient(baseURL string) *Client` | Creates a client with a 5-minute HTTP timeout |
| `StartRun(ctx, req) (*RunResponse, error)` | `POST /v1/runs` — starts the run, returns `run_id` and initial status |
| `StreamEvents(ctx, runID) (*RunResult, error)` | `GET /v1/runs/{runID}/events` — consumes the SSE stream until a terminal event |
| `SendAndWait(ctx, req) (*RunResult, error)` | Calls `StartRun` then `StreamEvents`; returns when the run completes |

### SSE terminal handling

The SSE consumer in `harness/sse.go` recognizes three terminal event types and closes the stream on any of them:

| Event | Action |
|---|---|
| `run.completed` | Parses `payload.output` into `RunResult.Output` and returns |
| `run.failed` | Returns an error from `payload.error` |
| `run.cancelled` | Returns a `"run cancelled"` error |

Non-terminal events (tool calls, token deltas, usage accounting, etc.) are silently skipped. The socialagent only cares about the final output, so the extra fidelity of the full event stream is not needed here. If your integration needs real-time progress, consume those intermediate events directly.

---

## The embedded MCP server

`socialagent` runs its own MCP server in-process, started in a background goroutine at `POST /mcp` on port `:8082`. The server uses `github.com/mark3labs/mcp-go` with stateless streamable-HTTP transport. Its URL is passed to every `RunRequest` via `MCPServers`, so `harnessd` can call back into it during each run.

```go
// Constructor in mcpserver/server.go
func New(store UserStore, deliverer MessageDeliverer) *Server
```

`deliverer` is optional. Passing `nil` disables Telegram push delivery for the `send_message_to_user` tool.

### Registered tools

These are the bare MCP tool names as registered in the `mcpserver` package. The harness prefixes them with the server name when exposing them to the agent — so `search_users` on the `social` server becomes `mcp_social_search_users` in `AllowedTools`.

| MCP tool name | Description |
|---|---|
| `search_users` | Search user profiles by keywords or interests |
| `get_user_profile` | Get a detailed profile by display name |
| `get_updates` | Get the recent community activity feed |
| `save_insight` | Save an agent observation about a user |
| `get_my_profile` | Get the current user's profile and insights |
| `get_community_stats` | Get total users, profiles, and activity counts |
| `send_message_to_user` | Forward a message to another user via Telegram push |
| `get_my_messages` | Retrieve pending messages and mark them delivered |

<Callout type="info">
MCP tool names follow the format `mcp_{server}_{tool}` — single underscores, with an `mcp_` prefix. For example: `mcp_social_search_users`. The MCP server's registered name (`"social"`) is the middle segment.
</Callout>

---

## Async profile summarizer

After each successful conversation turn, the gateway fires a background goroutine that calls `summarizer.UpdateProfile`. The summarizer:

1. Fetches the last 50 messages from `GET {harnessBaseURL}/v1/conversations/{conversationID}/messages`.
2. Sends a summarization prompt to `harnessd` via `SendAndWait`, using a dedicated `ConversationID` of `"summary-" + userID`. Isolating the summarizer to its own conversation keeps it from polluting the user's main conversation history.
3. Parses the JSON response: `{"summary": "...", "interests": [...], "looking_for": "..."}`.
4. Upserts the result into the `user_profiles` table.

The rendered system prompt on the next turn includes the updated summary, so the agent's persona adapts as it learns more about the user.

---

## Configuration

`socialagent` is configured entirely through environment variables. The quickstart scripts (`scripts/setup.sh` and `scripts/dev.sh`) handle the initial setup including starting Postgres, writing `.env` from `.env.example`, and registering the Telegram webhook via ngrok.

### Required variables

| Variable | Description |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_WEBHOOK_SECRET` | Secret for the `X-Telegram-Bot-Api-Secret-Token` webhook header |
| `DATABASE_URL` | Postgres connection string |
| `OPENAI_API_KEY` | API key — used by `harnessd`, not by `socialagent` directly |

### Optional variables

| Variable | Default | Description |
|---|---|---|
| `HARNESS_URL` | `http://localhost:8080` | Base URL of the `harnessd` HTTP API |
| `LISTEN_ADDR` | `:8081` | TCP address for `socialagent`'s own HTTP server |
| `MCP_SERVER_URL` | `http://localhost:8082/mcp` | URL of the embedded MCP server |
| `SAFETY_SCREENER_URL` | `""` (disabled) | Llama Guard-compatible safety endpoint; empty disables screening |

<Callout type="warning">
`SOCIALAGENT_SYSTEM_PROMPT` is read into `Config.SystemPrompt` (with a built-in default) but is never passed to the gateway. The system prompt is always rendered by `systemprompt.Render` from the template in `systemprompt/prompt.go`, regardless of whether this env var is set. Do not rely on it to override the persona.
</Callout>

---

## Takeaways for your integration

`socialagent` is a concrete blueprint, not a framework. The patterns it demonstrates apply to any service that needs to put an LLM agent in front of external users.

<Card>
<CardHeader>
<CardTitle>Use `AllowedTools` on every run</CardTitle>
</CardHeader>
<CardContent>
Pass an explicit `allowed_tools` list in every `RunRequest`. This limits the agent to only the tools it legitimately needs for the current context. Without it, the agent has access to the full harness tool catalog, which is almost certainly broader than what you want.
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Attach MCP servers per-run, not globally</CardTitle>
</CardHeader>
<CardContent>
The `mcp_servers` field in `RunRequest` attaches a server to a single run. This is the right pattern when different tenants or workflows need different tool sets — each run gets precisely the tools it needs, and nothing bleeds across.
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Isolate conversations by tenant</CardTitle>
</CardHeader>
<CardContent>
Every user gets a stable `ConversationID` stored in Postgres. The `TenantID` field is set to the per-user UUID and acts as an opaque scoping value the harness stores and filters runs by. `socialagent` talks to `harnessd` without authentication (the harness client sends no Authorization header), so `tenant_id` passes through as-is. When `harnessd` auth is enabled, `tenant_id` must match (or be omitted to inherit) the authenticated API key's tenant — a mismatch is rejected with an error. The summarizer doubles down on conversation isolation by using a dedicated `"summary-" + userID` conversation so summarization history never leaks into the main chat.
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Build the harness client yourself — do not import `internal/`</CardTitle>
</CardHeader>
<CardContent>
The `apps/socialagent/harness/` package is about 250 lines (~100 lines for the client plus ~150 for the SSE consumer). Its `RunRequest`, `MCPServer`, `StartRun`, `StreamEvents`, and `SendAndWait` are all you need for the common case. Copy or adapt them into your own service. The `internal/` packages are not part of the public API and may change without notice.
</CardContent>
</Card>

---

## Next steps

- For a deeper look at the full `RunRequest` field set and all available HTTP routes, see the [HTTP API Guide](/docs/server/http-api-guide).
- To understand every event the SSE stream can emit, see the [Events concept page](/docs/concepts/events).
- To learn how to attach external MCP servers globally (at daemon startup rather than per-run), see the [Consuming External MCP Servers](/docs/integrations/mcp-consume) guide.
