---
title: "Architecture Overview"
sidebar_label: "Architecture"
sidebar_position: 1
---

import { Callout, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

go-code is a **local-first coding agent runtime**. At the center is `harnessd`, a long-running HTTP daemon that boots the full agent runtime — LLM providers, tools, memory, cron, MCP, skills, and workflow orchestration — and exposes it over a REST + Server-Sent Events (SSE) API. Clients (the interactive TUI, the `harnesscli` one-shot command, or any external service) talk to that daemon over HTTP. Above the daemon sit optional orchestration layers — the **Workflow Engine**, **Go Relay**, and **Symphony** — that compose, route, and dispatch runs at higher levels of abstraction.

This page maps every named component and shows how a prompt travels from your terminal through `harnessd` and back as a stream of events.

---

## Clients and the daemon

`harnessd` is the always-on local process that owns the agent runtime. Nothing executes inside it unless a client sends a request.

### The `go-code` wrapper

The `go-code` shell script (`scripts/go-code.sh`) is the single user-facing entry point. When you run it, it:

1. Detects whether a healthy `harnessd` is already listening on `HARNESS_ADDR` (default `:8080`).
2. If not, starts one in the background.
3. Forwards your command to `harnesscli`.
4. On exit, stops the server only if it started it — a pre-existing server is always left running.

| Invocation | Mode | What it does |
|---|---|---|
| `go-code` | TUI | Launches the BubbleTea interactive TUI |
| `go-code "write a test"` | prompt | Runs one prompt, streams events, then exits |
| `go-code --server` | server | Starts `harnessd` in background and prints the URL |
| `go-code runs` | list | Lists known runs |
| `go-code show <run-id>` | status | Shows one run |
| `go-code cancel <run-id>` | cancel | Cancels a run |
| `go-code continue <run-id> "prompt"` | continue | Continues a completed run |
| `go-code replay <run-id>` | replay | Replays a recorded run |

### Client types

Three kinds of clients talk to `harnessd` over HTTP:

<div className="grid grid-cols-1 sm:grid-cols-3 gap-4 my-6">
  <Card>
    <CardHeader>
      <CardTitle>harnesscli / TUI</CardTitle>
    </CardHeader>
    <CardContent>
      The bundled terminal client. One-shot with <code>--prompt</code>, or interactive with <code>--tui</code>. Default base URL: <code>http://localhost:8080</code>.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>External HTTP services</CardTitle>
    </CardHeader>
    <CardContent>
      Any service that can POST JSON. Webhook routes accept GitHub, Slack, and Linear payloads authenticated by HMAC. The external trigger endpoint accepts a generic HMAC-signed payload.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>MCP hosts</CardTitle>
    </CardHeader>
    <CardContent>
      <code>harnessd --mcp</code> starts in MCP stdio mode. The MCP endpoint is also mounted at <code>/mcp</code> on the same TCP port as the REST API.
    </CardContent>
  </Card>
</div>

### The request/event cycle

Every agent run follows the same two-step HTTP pattern:

```
POST /v1/runs          →  {"run_id": "run_…", "status": "queued"}
GET  /v1/runs/{id}/events  →  text/event-stream (SSE)
```

The `POST` returns HTTP 202 immediately with `status: "queued"` — the run transitions to `"running"` asynchronously once the run loop starts. The caller then opens an SSE stream and receives structured events — token deltas, tool calls, cost updates, and finally a terminal event (`run.completed`, `run.failed`, or `run.cancelled`) that closes the stream.

<Callout variant="info" title="Key-free smoke testing">
  Set <code>HARNESS_PROVIDER=fake</code> to run <code>harnessd</code> without any API key. The fake provider replays a pre-recorded turns file, which makes it suitable for CI and local exploration.
</Callout>

---

## Inside the daemon

### Bootstrap sequence

When `harnessd` starts, it runs through `runWithSignalsWithDeps` in `cmd/harnessd/main.go`:

1. Load the 6-layer config cascade (built-in defaults → `~/.harness/config.toml` → `.harness/config.toml` → named profile → `HARNESS_*` env vars → cloud/team constraints, a not-yet-applied stub).
2. Resolve `HARNESS_WORKSPACE` (default `.`).
3. Load `catalog/models.json`, build the `ProviderRegistry`, and set up the pricing resolver.
4. Resolve the default provider: `HARNESS_PROVIDER=fake` → catalog provider → `OPENAI_API_KEY` → error.
5. Boot the prompt engine (`systemprompt.NewFileEngine`).
6. Open the memory manager (SQLite at `.harness/state.db`, or Postgres via `HARNESS_MEMORY_DB_DSN`).
7. Open SQLite stores for checkpoints, workflows, and working memory.
8. Load workflow and network definitions from configured directories.
9. Boot the skills system (scan for `SKILL.md` files in global and workspace directories).
10. Boot cron: embedded SQLite scheduler by default, or a remote cron service when `HARNESS_CRON_URL` is set.
11. Boot the MCP client manager.
12. Boot persistence stores for runs, conversations, and relay workers (only when the corresponding env vars are set).
13. Wire the ask-user broker, approval broker, and tool activations.
14. Build the `harness.Runner`.
15. Start the hot-reload file watcher (polls every 5 seconds by default).
16. Build the HTTP runtime: workflow engine, networks engine, subagents manager, script workflow engine.
17. Mount the main HTTP handler at `/` and the MCP server at `/mcp`.
18. Start `http.Server.ListenAndServe` and log `harness server listening on <addr>`.
19. Block on signal, then gracefully shut down (10 s timeout).

### Subsystems in one view

| Subsystem | Package | What it owns |
|---|---|---|
| Run loop | `internal/harness` | LLM turns, tool dispatch, event emission, cost tracking |
| HTTP API | `internal/server` | Route registration, auth middleware, SSE fan-out |
| Provider routing | `internal/provider` | Model catalog, multi-provider clients, pricing |
| Memory | `internal/observationalmemory`, `internal/workingmemory` | Per-run and cross-run observation, reflection, and working memory |
| Cron | `internal/cron` | Scheduled prompt triggers |
| MCP client | `internal/mcp` | Connecting to external MCP servers |
| Skills | `internal/skills` | Loading, registering, and hot-reloading skill definitions |
| Workflows | `internal/workflow` | Go script-based multi-agent pipeline engine |
| Workspaces | `internal/workspace` | Local, container, VM, and worktree isolation |
| Config | `internal/config` | 6-layer cascade load and merge |

### Authentication

By default, `harnessd` uses Bearer token auth on all routes except `/healthz` and the webhook/external-trigger routes (which use HMAC). Auth is implicitly disabled when `HARNESS_RUN_DB` is not set (no key store to validate against) and can be explicitly disabled with `HARNESS_AUTH_DISABLED=true`.

### Event types

The SSE stream carries named event types. Terminal events close the stream:

- **Terminal:** `run.completed`, `run.failed`, `run.cancelled`
- **Run lifecycle:** `run.started`, `run.queued`, `run.waiting_for_user`, `run.resumed`, `run.cost_limit_reached`
- **LLM turns:** `llm.turn.requested`, `llm.turn.completed`, `assistant.message.delta`, `assistant.thinking.delta`, `assistant.message`
- **Tool execution:** `tool.call.started`, `tool.call.completed`, `tool.call.delta`, `tool.approval_required`, `tool.approval_granted`, `tool.approval_denied`
- **Accounting:** `usage.delta`
- **Workspace:** `workspace.provisioned`, `workspace.destroyed`
- **Callbacks:** `callback.scheduled`, `callback.fired`, `callback.canceled`

---

## Orchestration layers above the runtime

Three optional layers sit above `harnessd` to compose, route, or dispatch runs at scale.

### Workflow Engine (`internal/workflow`)

The workflow engine lets you write multi-agent pipelines in plain Go. A *script* is a `func(ctx *Context) (any, error)` that you register by name; the engine runs it asynchronously and exposes the result and event stream over HTTP.

The context object provides these primitives:

| Primitive | What it does |
|---|---|
| `ctx.Agent(prompt, opts)` | Dispatches a sub-agent run; acquires a semaphore slot |
| `ctx.Parallel(thunks)` | Runs thunks concurrently with barrier semantics |
| `ctx.Pipeline(items, stages...)` | Maps items through stages with no inter-stage barrier |
| `ctx.Phase(title)` | Labels the current phase for progress tracking |
| `ctx.Log(message)` | Emits a `workflow.log` event |
| `ctx.Workflow(name, args)` | Runs a nested named workflow |

Concurrency is semaphore-capped at `min(16, NumCPU-2)` (minimum 1). Only `ctx.Agent()` acquires a slot — `Parallel` and `Pipeline` goroutines do not, which prevents deadlocks when thunks call `Agent()` inside themselves.

Budget tracking (token limits) flows from parent to child workflows via `ctx.Budget.Clone()`. Failed runs can be resumed with `Engine.Resume(ctx, runID, args)`.

**HTTP routes for script workflows:**

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/script-workflows` | List registered workflows |
| `GET` | `/v1/script-workflows/{name}` | Get workflow metadata |
| `POST` | `/v1/script-workflows/{name}/runs` | Start a workflow run |
| `GET` | `/v1/script-workflow-runs/{id}` | Get run status and result |
| `GET` | `/v1/script-workflow-runs/{id}/events` | SSE event stream |
| `POST` | `/v1/script-workflow-runs/{id}/resume` | Resume a failed run |

### Go Relay (`internal/relay`)

Go Relay is a **control-plane design** that sits above the execution runtime. While `harnessd` executes one run inside one workspace, Relay answers the question of *where* each run should execute across a fleet of registered workers.

Relay owns:

- **Worker registration** — workers register with a location type (`local`, `worktree`, `container`, `vm`, `sandbox`), a trust tier (`untrusted`, `standard`, `privileged`), and a capability inventory.
- **Placement routing** — the `PlacementRouter` applies hard constraints (status, trust tier, required capabilities) then scores eligible workers on soft preferences (local bias, load, clean workspace).
- **Run contract composition** — the `Composer` translates user intent into a validated `RunContract` that specifies the prompt, workspace target, granted capabilities, permissions, and output expectations.
- **Event and artifact relay** — forwarding events and artifacts between workers and the control plane.
- **Checkpointed handoff** — a `HandoffPackage` captures run state (conversation summary, todos, patch refs, workspace fingerprint) so work can be transferred between workers at defined checkpoint boundaries.

Worker registration and heartbeating are exposed at `/v1/relay/workers`. Worker persistence requires `HARNESS_RELAY_DB` (a SQLite path).

<Callout variant="warning" title="Current transport scope">
  The Relay transport layer is in-memory only today — there is no network transport implementation connecting remote workers. Present Go Relay as a control-plane architecture with local worker registration, not a shipped multi-region service.
</Callout>

### Symphony (`cmd/symphd`, `internal/symphd`)

Symphony is an **issue-tracker-driven orchestration daemon**. It is distinct from both the workflow engine (which composes Go functions) and Go Relay (which routes runs across regions). Symphony is a single-node scheduler that:

1. Polls GitHub Issues for items labeled `symphd`.
2. Claims each issue and provisions an isolated workspace (local, worktree, container, VM, or pool).
3. Dispatches an agent run via `POST /v1/runs` to the `harnessd` inside that workspace.
4. Polls `GET /v1/runs/{id}` for completion.
5. On failure, retries with exponential backoff (base 10 s, max 5 min, max 5 attempts); exhausted issues go to a dead-letter queue.
6. Destroys the workspace on exit — success or failure.

`symphd` is a separate binary that runs on port `:8888` (configurable via `addr` in its YAML config).

<Callout variant="warning" title="Symphony scope">
  Symphony is a single-node scheduler. It does not use Go Relay for placement and does not distribute work across multiple go-code instances.
</Callout>

---

## Repository map

Understanding which directory owns what helps you navigate the codebase and know what is safe to import externally.

### Entry points (`cmd/`)

| Path | Binary | Purpose |
|---|---|---|
| `cmd/harnessd/` | `harnessd` | Local HTTP daemon and runtime bootstrap |
| `cmd/harnesscli/` | `harnesscli` | Terminal client and BubbleTea TUI |
| `cmd/symphd/` | `symphd` | Symphony orchestrator daemon |
| `cmd/cronctl/`, `cmd/cronsd/` | `cronctl`, `cronsd` | Cron control and scheduling daemons |
| `cmd/harness-mcp/` | `harness-mcp` | Standalone MCP server entry point |

### Core packages (`internal/`)

| Path | Contents |
|---|---|
| `internal/harness/` | Run loop, tools, event emission, conversation behavior |
| `internal/server/` | HTTP API handlers and middleware |
| `internal/provider/` | Provider clients (openai, anthropic; other catalog providers such as gemini, openrouter, etc. are routed via the OpenAI-compatible client), model catalogs, pricing, routing |
| `internal/workflow/` | Script-based multi-agent pipeline orchestrator |
| `internal/relay/` | Go Relay multi-location control plane |
| `internal/symphd/` | Symphony orchestrator logic |
| `internal/workspace/` | Local, container, VM, and worktree workspace implementations |
| `internal/config/` | 6-layer configuration cascade |
| `internal/mcp/` | MCP client management |
| `internal/skills/` | Skills loader and registry |
| `internal/cron/` | Embedded cron scheduler |
| `internal/networks/` | Network topology definitions |

### Runtime assets and tools

| Path | Contents |
|---|---|
| `catalog/` | Model and pricing catalogs (`models.json`) loaded at runtime |
| `prompts/` | Bundled prompt assets installed with `go-code` |
| `skills/` | Bundled skill fixtures and validation coverage |
| `apps/` | Experimental applications that integrate with the harness |
| `benchmarks/` | Terminal Bench and overnight benchmark harnesses (Python — not Go) |
| `harness_agent/` | Python adapter used by benchmark runners |
| `scripts/` | Install, development, Symphony, and regression helpers |
| `Formula/` | Homebrew formula |
| `docs/runbooks/` | Operational runbooks |

### Public SDK

`pkg/workflowsdk` is the importable public surface for embedding the workflow engine in external Go programs. Everything under `internal/` is internal-only by Go module convention.

<Callout variant="info" title="Provider support">
  OpenAI is the primary bootstrap path: if <code>OPENAI_API_KEY</code> is set and no model is configured, <code>harnessd</code> falls back to an OpenAI provider directly. The Anthropic provider is also implemented and live — not planned. The full provider catalog includes: <code>openai</code>, <code>anthropic</code>, <code>gemini</code>, <code>deepseek</code>, <code>groq</code>, <code>xai</code>, <code>kimi</code>, <code>qwen</code>, <code>together</code>, <code>openrouter</code>.
</Callout>

---

## Next steps

- **Run your first prompt** — the [Quickstart](/docs/getting-started/quickstart) walks through the `go-code` wrapper end to end.
- **Understand runs and events** — [The Event Model](/docs/concepts/events) explains the SSE stream format, and the [Event Catalog reference](/docs/reference/events-catalog) lists every event type.
- **Write a workflow** — the [Workflow Engine](/docs/workflows/workflow-engine) guide covers `Engine.Register`, `ctx.Agent`, and `ctx.Parallel` with full examples.
- **Configure harnessd** — the [Configuration Cascade](/docs/concepts/configuration) covers the six-layer config cascade, and the [Environment Variables reference](/docs/reference/environment-variables) lists every `HARNESS_*` variable.
