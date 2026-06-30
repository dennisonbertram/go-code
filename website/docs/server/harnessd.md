---
title: "Running the harnessd Daemon"
sidebar_label: "harnessd Daemon"
sidebar_position: 1
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

`harnessd` is the HTTP daemon at the center of go-code. It boots a complete agent runtime — LLM provider, tool registry, memory, cron scheduler, MCP client, skills, and workflow engines — and exposes everything over a REST + SSE (Server-Sent Events) API on a single TCP port (`:8080` by default). Clients like `harnesscli`, the BubbleTea TUI, or any HTTP client can submit agent runs, stream live events, steer running agents, and manage conversations without coupling directly to the Go runtime.

If you need a daemon that stays running while you iterate in other terminals, or you want to connect Claude Desktop and other MCP hosts to your local agent runtime, `harnessd` is where you start.

---

## Starting the server

`harnessd` can be started four ways depending on your workflow.

<Tabs defaultValue="direct">
  <TabsList>
    <TabsTrigger value="direct">Direct (real key)</TabsTrigger>
    <TabsTrigger value="fake">Fake / key-free</TabsTrigger>
    <TabsTrigger value="startsh">scripts/start.sh</TabsTrigger>
    <TabsTrigger value="initsh">scripts/init.sh</TabsTrigger>
  </TabsList>

  <TabsContent value="direct">

Build the binary, then export your API key and workspace path:

```bash
go build -o harnessd ./cmd/harnessd
OPENAI_API_KEY=sk-...  HARNESS_WORKSPACE=$(pwd) ./harnessd
```

When the daemon is ready it prints:

```
harness server listening on :8080
```

You can then POST runs to `http://localhost:8080/v1/runs` and stream events from `http://localhost:8080/v1/runs/{id}/events`.

  </TabsContent>

  <TabsContent value="fake">

Fake mode replaces the LLM with a scripted, in-memory provider that needs no API key and no network. It is the canonical path for smoke tests, CI pipelines, and local development before you have credentials.

<Steps>
  <Step title="Write a turns file">

Create a JSON file that describes what the fake provider should reply. The short key names (`"prompt"`, `"completion"`) are required — do not use the longer `prompt_tokens` / `completion_tokens` forms.

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

Save it, for example, to `/tmp/fake-turns.json`.

  </Step>
  <Step title="Start harnessd with fake provider">

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake-turns.json \
HARNESS_AUTH_DISABLED=true \
./harnessd
```

`HARNESS_AUTH_DISABLED=true` disables the Bearer-token middleware so you can call the API without an API key. Without it, unauthenticated requests return `401`.

  </Step>
  <Step title="POST a run and read back the result">

```bash
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "hello", "allow_fallback": true}' \
  | jq -r .run_id)

curl -s http://localhost:8080/v1/runs/$RUN_ID | jq .status
# "completed"
```

`"allow_fallback": true` is required in fake mode because the model registry lookup fails without an API key — it tells the runner to fall back to the default provider (the fake).

  </Step>
</Steps>

<Callout type="info" title="In-process smoke test">
  You can also run the full fake-mode pipeline without starting a server at all: <code>go test ./internal/server/... -run TestRunSmoke</code> (or add <code>-race</code> for the data-race check). No API key, no Docker, no network required.
</Callout>

  </TabsContent>

  <TabsContent value="startsh">

`scripts/start.sh` is a thin convenience wrapper. It reads `HARNESS_ADDR` (defaulting to `:8080`), kills any existing process on that port, and then runs `go run ./cmd/harnessd "$@"`.

```bash
./scripts/start.sh
```

Pass additional flags through `"$@"`, for example:

```bash
HARNESS_ADDR=:9090 ./scripts/start.sh --profile my-profile
```

  </TabsContent>

  <TabsContent value="initsh">

`scripts/init.sh` creates a fully isolated git worktree for a task, downloads dependencies, builds all three binaries (`harnessd`, `harnesscli`, `coveragegate`) into a `.tmp/bootstrap/bin` directory, and writes a sourceable `dev.env` file. Add `--start-server` to also launch `harnessd` in a tmux session via `start.sh`.

```bash
scripts/init.sh --start-server my-task-slug
```

After the script completes, source the env file to put the built binaries and workspace paths into your shell:

```bash
source .tmp/bootstrap/dev.env
```

  </TabsContent>
</Tabs>

---

## Flags and listen address

`harnessd` has three CLI flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--profile <name>` | `""` | Load a named profile from `~/.harness/profiles/<name>.toml` or `<workspace>/.harness/profiles/<name>.toml`. Profile names must not contain `/`, `\`, `..`, or be absolute paths. |
| `--mcp` | `false` | Start in MCP stdio mode instead of HTTP. See [MCP stdio mode](#mcp-stdio-mode) below. |
| `--mcp-workspace <path>` | `""` | Workspace root for MCP stdio mode. |

### Listen address resolution

The address `harnessd` binds to is resolved in five layers, lowest to highest priority:

| Priority | Source | Example |
|----------|--------|---------|
| 1 — lowest | Built-in default | `:8080` |
| 2 | `~/.harness/config.toml` (`addr` field) | `:9000` |
| 3 | `<workspace>/.harness/config.toml` (`addr` field) | `:9000` |
| 4 | Named profile (via `--profile`) | `:9090` |
| 5 — highest | `HARNESS_ADDR` env var | `:8888` |

The resolved address is passed directly to `net/http.Server.Addr`. Use the socket form (`:8080`), not a full URL.

### HTTP server timeout constants

These values are hardcoded and are not configurable via environment variables:

| Setting | Value |
|---------|-------|
| `ReadTimeout` | 60 s |
| `ReadHeaderTimeout` | 10 s |
| `IdleTimeout` | 120 s |
| `MaxHeaderBytes` | 1 MiB (1 &lt;&lt; 20) |

Non-streaming request handler timeout (for routes that do not end in `events`, `stream`, or `wait`) is **30 seconds**. Streaming paths bypass this timeout entirely.

---

## Bootstrap and provider resolution

### 20-step boot sequence

When `harnessd` starts in HTTP mode, `runWithSignalsWithDeps` performs these steps in order:

<Steps>
  <Step title="Load layered config">
    Calls `config.Load`, applying the six-layer cascade: built-in defaults → user global TOML → project TOML → named profile → `HARNESS_*` env vars → cloud/team constraints (stub, not yet active).
  </Step>
  <Step title="Resolve HARNESS_WORKSPACE">
    Defaults to `"."` if not set. All relative paths for DB files and content directories are anchored here.
  </Step>
  <Step title="Build catalog bootstrap">
    Loads `catalog/models.json` (auto-detected or via `HARNESS_MODEL_CATALOG_PATH`), builds the `ProviderRegistry`, and sets up the pricing resolver.
  </Step>
  <Step title="Resolve default provider">
    Runs `resolveDefaultProvider` — see the priority order immediately below.
  </Step>
  <Step title="Boot prompt engine">
    Initializes `systemprompt.NewFileEngine` from the prompts directory — `HARNESS_PROMPTS_DIR` when set, otherwise an auto-detected `prompts/` directory (found by walking up for `prompts/catalog.yaml`).
  </Step>
  <Step title="Open memory manager">
    Opens SQLite or Postgres based on `HARNESS_MEMORY_MODE` and `HARNESS_MEMORY_DB_DRIVER`.
  </Step>
  <Step title="Open SQLite stores">
    Opens checkpoint, workflow, and working-memory stores — all at `orchestrationDBPath` inside the workspace.
  </Step>
  <Step title="Load workflow and network definitions">
    Reads YAML files from `HARNESS_WORKFLOWS_DIR` and `HARNESS_NETWORKS_DIR` if set.
  </Step>
  <Step title="Boot skills system">
    Loads and registers `SKILL.md` files from the global and workspace skills directories.
  </Step>
  <Step title="Boot cron">
    Starts the embedded SQLite scheduler (default) or connects to an external service if `HARNESS_CRON_URL` is set.
  </Step>
  <Step title="Boot MCP client manager">
    Registers MCP servers from TOML config layers (TOML takes precedence), then from `HARNESS_MCP_SERVERS` env var (skipping duplicate names).
  </Step>
  <Step title="Boot persistence stores">
    Opens the run store, conversation store, and relay worker store — only when `HARNESS_RUN_DB`, `HARNESS_CONVERSATION_DB`, and `HARNESS_RELAY_DB` are set respectively.
  </Step>
  <Step title="Wire brokers and activations">
    Sets up the ask-user broker, tool-approval broker, and activation registry.
  </Step>
  <Step title="Build RunnerConfig and harness.Runner">
    Assembles the core run execution engine with all wired dependencies.
  </Step>
  <Step title="Start hot-reload watcher">
    When `HARNESS_WATCH_ENABLED=true` (default), starts a poll-based watcher on the global and workspace skills/workflows directories.
  </Step>
  <Step title="Build HTTP runtime">
    Initializes the `workflows.Engine`, `networks.Engine`, `subagents.Manager`, and script workflow engine.
  </Step>
  <Step title="Mount handlers">
    Mounts `mainHandler` at `/` and the MCP HTTP server at `/mcp` — both on the same TCP port.
  </Step>
  <Step title="Start http.Server">
    Calls `ListenAndServe` in a goroutine.
  </Step>
  <Step title="Log ready">
    Prints `harness server listening on <addr>` to stdout.
  </Step>
  <Step title="Block on signal">
    Waits for `SIGINT` or `SIGTERM`, then shuts down gracefully with a 10-second timeout.
  </Step>
</Steps>

### Provider resolution order

`resolveDefaultProvider` picks the default LLM provider at startup by trying these four paths in order:

1. **Fake** — if `HARNESS_PROVIDER=fake`, use `fakeprovider.New(turns)`. No API key needed.
2. **Catalog** — if the model catalog is loaded and the configured default model (`HARNESS_MODEL`, default `gpt-4.1-mini`) resolves to a provider that has its API key set, use that catalog client. Supported catalog providers include `openai`, `anthropic`, `deepseek`, `groq`, `xai`, `kimi`, `qwen`, `together`, `openrouter`, and `gemini`.
3. **OpenAI legacy** — if `OPENAI_API_KEY` is set, bootstrap OpenAI directly.
4. **Error** — if none of the above applies, `harnessd` exits with an error message.

---

## Key environment variables

The full set of `HARNESS_*` variables is documented on the [Configuration](/docs/concepts/configuration) page. The table below covers the most important ones for getting `harnessd` running.

### Core startup

| Variable | Default | Description |
|----------|---------|-------------|
| `HARNESS_ADDR` | `:8080` | HTTP listen address in socket form. |
| `HARNESS_WORKSPACE` | `.` | Workspace root — anchors all relative paths. |
| `HARNESS_MODEL` | `gpt-4.1-mini` | Default LLM model for runs. |
| `HARNESS_MAX_STEPS` | `8` | Max tool-calling steps per run. Set to `0` to remove the cap. |
| `HARNESS_PROVIDER` | — | Set to `fake` for key-free mode. |
| `HARNESS_FAKE_TURNS` | — | Path to the JSON turns file when `HARNESS_PROVIDER=fake`. Required when using fake mode. |
| `HARNESS_AUTH_DISABLED` | — | Set to `true` to disable Bearer-token auth. |

<Callout type="warning" title="HARNESS_MAX_STEPS gotcha">
  Setting <code>HARNESS_MAX_STEPS=0</code> in a TOML config file means "unlimited." However if the env var is <em>absent</em> and the config stack resolved to <code>0</code>, harnessd resets it to <code>8</code> as a backward-compatible default. To truly remove the step cap at runtime, set <code>HARNESS_MAX_STEPS=0</code> explicitly in your environment.
</Callout>

### Persistence

All persistence stores are disabled unless their env var is set. When `HARNESS_RUN_DB` is not set, run records are kept only in memory and are lost on restart.

| Variable | Description |
|----------|-------------|
| `HARNESS_RUN_DB` | SQLite path for run and event records. Required for `GET /v1/runs` (list). |
| `HARNESS_CONVERSATION_DB` | SQLite path for conversation persistence. |
| `HARNESS_RELAY_DB` | SQLite path for relay worker state. |

<Callout type="warning" title="Auth and persistence are linked">
  When <code>HARNESS_RUN_DB</code> is not set, there is no key store to validate against, so auth is implicitly disabled — the server will not return <code>401</code> even without <code>HARNESS_AUTH_DISABLED=true</code>. Similarly, <code>GET /v1/runs</code> (list all runs) returns <code>501 Not Implemented</code> without a persistent store. Reads of individual runs by ID still work because runs are held in memory for the life of the process.
</Callout>

### Provider API keys

Each provider reads its API key from a dedicated env var:

| Provider | Env var |
|----------|---------|
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| DeepSeek | `DEEPSEEK_API_KEY` |
| Groq | `GROQ_API_KEY` |
| xAI | `XAI_API_KEY` |
| Kimi / Moonshot | `MOONSHOT_API_KEY` |
| Qwen / DashScope | `DASHSCOPE_API_KEY` |
| Together AI | `TOGETHER_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Google Gemini | `GOOGLE_API_KEY` |

You only need to set the keys for providers you actually want to use.

---

## MCP stdio mode

In addition to its HTTP API, `harnessd` can run as an MCP (Model Context Protocol) server over stdin/stdout. This is useful when you want to drive harnessd from a host that speaks MCP natively — for example, from another agent or from a Claude Code session that uses harnessd as a tool provider.

```bash
# Start harnessd as an MCP stdio server
./harnessd --mcp

# Specify a workspace root explicitly
./harnessd --mcp --mcp-workspace /path/to/my-project
```

When `--mcp` is set, `harnessd` starts the stdio MCP server instead of the HTTP server. The workspace root is resolved from, in priority order:

1. `--mcp-workspace` flag
2. `HARNESS_WORKSPACE` env var
3. `.` (current directory)

The stdio server exposes the full harness tool catalog as MCP tools under their native catalog names (e.g. `read_file`, `bash`, `start_run`, `subscribe_run`). The `mcp_{server}_{tool}` naming scheme applies in the reverse direction: it is how harnessd names tools it imports from external MCP servers it connects to as a client.

<Callout type="info" title="Proxy binary for Claude Desktop">
  If you want to connect Claude Desktop to a running harnessd HTTP instance (rather than spawning a new stdio process), build <code>cmd/harness-mcp</code> instead. It is a lightweight stdio proxy that bridges Claude Desktop's MCP host to any <code>harnessd</code> HTTP server at the address in <code>HARNESS_ADDR</code>.
</Callout>

---

## Healthcheck

A simple healthcheck endpoint is available without authentication:

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

This is useful in Docker / Kubernetes liveness probes and in scripts that need to wait for the daemon to be ready.

---

## Quick reference: common startup patterns

```bash
# Production: OpenAI, persistent runs DB, custom address
OPENAI_API_KEY=sk-...          \
HARNESS_ADDR=:9090             \
HARNESS_WORKSPACE=/srv/myapp   \
HARNESS_RUN_DB=/var/lib/harness/runs.db \
./harnessd

# Development: fake provider, no auth, default address
HARNESS_PROVIDER=fake          \
HARNESS_FAKE_TURNS=./turns.json \
HARNESS_AUTH_DISABLED=true     \
./harnessd

# MCP stdio mode: expose harness tools to a parent MCP host
./harnessd --mcp --mcp-workspace $(pwd)

# Multi-provider: Anthropic as default model
ANTHROPIC_API_KEY=sk-ant-...   \
HARNESS_MODEL=claude-sonnet    \
HARNESS_WORKSPACE=$(pwd)       \
./harnessd
```

---

## Next steps

- **[Configuration](/docs/concepts/configuration)** — Full reference for the six-layer TOML + env var cascade and every `HARNESS_*` variable.
- **[HTTP API Reference](/docs/server/http-api-guide)** — Every route, request body shape, and response schema.
- **[SSE Event Stream](/docs/concepts/events)** — The event types emitted during a run and how to subscribe to them.
- **[MCP Integration](/docs/integrations/mcp-consume)** — Connect external MCP tool servers to your agents, or drive `harnessd` from Claude Desktop.
- **[Key-Free Testing](/docs/getting-started/key-free-testing)** — More depth on the fake provider and the `TestRunSmoke` in-process smoke test.
