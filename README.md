# Go Agent Harness

`go-agent-harness` is a Go service for running coding-oriented agent sessions with a streamed event API, conversation storage, optional subagent and cron helpers, and a thin CLI for live testing.

The implementation is centered in:

- `cmd/harnessd`
- `cmd/harnesscli`
- `internal/server`
- `internal/harness`
- `internal/config`

## Repository Layout

- `cmd/`: product entrypoints and operator CLIs.
- `internal/`: main application packages for runner execution, HTTP APIs, provider integrations, config, storage, and workspace management.
- `plugins/`: optional runtime plugins and plugin-side training helpers.
- `playground/`: isolated experimental and training snippets in a separate Go module.
- `docs/`: plans, logs, runbooks, and project context.
- `scripts/`: regression, bootstrap, smoke, and workflow automation.

The repo root is intentionally kept free of Go source now. If you want to work on snippet-style exercises, use `playground/` instead of adding ad hoc files at the top level.

## What The Service Does

- Starts runs with `POST /v1/runs`.
- Streams run events from `GET /v1/runs/{id}/events`.
- Exposes run control endpoints for input, continue, steer, compact, and replay.
- Exposes conversation, agent, subagent, cron, skill, recipe, provider, model, search, and MCP discovery endpoints.
- Builds a default tool registry from local file/shell helpers plus optional integrations enabled by config.

## Quick Start

### Installed Command

For daily use, install the local `go-code` command:

```bash
./scripts/install.sh --add-to-path
```

The installer defaults to `~/.local`, copies `harnesscli`, `harnessd`, and `go-code` into `~/.local/bin`, and installs runtime prompts plus model catalogs into `~/.local/share/go-code`.

After opening a new shell, or exporting `PATH="$HOME/.local/bin:$PATH"`, use:

```bash
go-code              # launch the TUI from the current project
go-code "prompt"     # run one prompt from the current project
go-code --server     # start harnessd and leave it running
```

Distribution and publishing notes live in `docs/runbooks/distribution.md`. The GitHub Pages source lives in `docs/site/`.

### Development From Source

1. Set `OPENAI_API_KEY`.
2. Start the server:

```bash
go run ./cmd/harnessd
```

3. Start the CLI against the server:

```bash
go run ./cmd/harnesscli -base-url http://127.0.0.1:8080 -prompt "Summarize the repository docs"
```

## HTTP API

### Health And Discovery

- `GET /healthz`
- `GET /v1/models`
- `GET /v1/providers`
- `GET /v1/mcp/servers`
- `POST /v1/mcp/servers`
- `POST /v1/search/code`
- `POST /v1/summarize`
- `GET /v1/profiles`
- `GET|POST|PUT|DELETE /v1/profiles/{name}`

### Runs

- `POST /v1/runs`
- `GET /v1/runs`
- `GET /v1/runs/{id}`
- `GET /v1/runs/{id}/events`
- `GET|POST /v1/runs/{id}/input`
- `GET /v1/runs/{id}/summary`
- `POST /v1/runs/{id}/continue` — request body: `{"prompt": "..."}`
- `POST /v1/runs/{id}/steer` — request body: `{"prompt": "..."}`
- `GET /v1/runs/{id}/context`
- `POST /v1/runs/{id}/compact`
- `GET|PUT /v1/runs/{id}/todos`
- `POST /v1/runs/{id}/cancel`
- `POST /v1/runs/{id}/approve`
- `POST /v1/runs/{id}/deny`
- `POST /v1/runs/replay`

### Conversations

- `GET /v1/conversations/`
- `GET /v1/conversations/search`
- `POST /v1/conversations/cleanup`
- `DELETE /v1/conversations/{id}`
- `GET /v1/conversations/{id}/messages`
- `GET /v1/conversations/{id}/runs`
- `GET /v1/conversations/{id}/export`
- `POST /v1/conversations/{id}/compact`

### Agents And Subagents

- `POST /v1/agents`
- `GET /v1/subagents`
- `POST /v1/subagents`
- `GET /v1/subagents/{id}`
- `DELETE /v1/subagents/{id}`
- `POST /v1/subagents/{id}/wait`
- `POST /v1/subagents/{id}/cancel`

### Cron, Skills, And Recipes

- `GET /v1/cron/jobs`
- `POST /v1/cron/jobs`
- `GET /v1/cron/jobs/{id}`
- `PATCH /v1/cron/jobs/{id}`
- `DELETE /v1/cron/jobs/{id}`
- `POST /v1/cron/jobs/{id}/pause`
- `POST /v1/cron/jobs/{id}/resume`
- `GET /v1/skills`
- `GET /v1/skills/{name}`
- `POST /v1/skills/{name}/verify`
- `GET /v1/recipes/`
- `GET /v1/recipes/{name}`
- `GET /v1/recipes/{name}/schema`
- `POST /v1/external/trigger`
- `POST /v1/webhooks/github`
- `POST /v1/webhooks/slack`
- `POST /v1/webhooks/linear`
- `PUT /v1/providers/{name}/key`

## Run Request Shape

`POST /v1/runs` accepts a richer request than the original MVP docs described. The current server supports:

- Core prompt fields: `prompt`, `system_prompt`, `agent_intent`, `task_context`, `prompt_profile`, `prompt_extensions`
- Model and provider fields: `model`, `provider_name`, `allow_fallback`, `reasoning_effort`
- Budget and limits: `max_steps`, `max_cost_usd`
- Tooling and integrations: `allowed_tools`, `mcp_servers`, `dynamic_rules`
- Role models: `role_models.primary`, `role_models.summarizer`
- Identity and tenancy: `tenant_id`, `agent_id`
- Permissions: `permissions.sandbox`, `permissions.approval`
- Profile selection: `profile`

The response includes identifiers such as `conversation_id`, `tenant_id`, `provider_name`, and `agent_id` when available.

## Model Discovery

The backend uses a hybrid model-discovery path.

- The static catalog in `catalog/models.json` remains the baseline source for provider definitions, aliases, pricing, quirks, and default metadata.
- Live discovery is currently implemented only for OpenRouter via `https://openrouter.ai/api/v1/models`.
- Runtime provider resolution and `GET /v1/models` merge live OpenRouter results into the static catalog view.
- When the same OpenRouter model exists in both places, static metadata wins.
- OpenRouter-only live models are still routable and listable even when they are absent from the static catalog. This is what enables dynamic slugs such as `moonshotai/kimi-k2.5`.

### Cache And Fallbacks

- OpenRouter discovery is cached in memory with a TTL, so the backend does not fetch on every request.
- Startup does not depend on a live discovery request.
- If a refresh fails, the backend uses cached OpenRouter data when available.
- If no cached discovery data exists, the backend falls back to the static catalog.
- Providers that remain static-catalog-only are unchanged by this layer.

## Event Stream

Run events are streamed from `GET /v1/runs/{id}/events`. The catalog is broader than the original README and includes lifecycle, streaming, tool, context, hook, memory, and provider events.

Common event families include:

- Lifecycle: `run.started`, `run.completed`, `run.failed`, `run.cancelled`, `run.input.required`, `run.waiting_for_user`, `run.continued`, `run.cost_limit_reached`, `run.step.started`, `run.step.completed`
- Model streaming: `assistant.message.delta`, `assistant.message.completed`, `assistant.thinking.delta`, `reasoning.complete`, `llm.request.snapshot`, `llm.response.meta`, `llm.empty_response.retry`, `provider.resolved`
- Tooling: `tool.activated`, `tool.call.started`, `tool.call.completed`, `tool.output.delta`, `tool.decision`, `tool.antipattern`, `tool.call.blocked`
- Context and compaction: `context.window.snapshot`, `context.window.warning`, `auto_compact.started`, `auto_compact.completed`, `compact_history.completed`, `context.reset`
- Hooks and steering: `hook.*`, `callback.*`, `tool_hook.*`, `meta.message.injected`, `steering.received`
- Memory and skills: `memory.*`, `skill.constraint.*`

Some events are feature-gated or only emitted when the relevant subsystem is enabled. The canonical definitions live in `internal/harness/events.go`.

## Tool Surface

The default registry is broader than the old “coding toolset” list in the README. It currently includes:

- Core file and shell helpers such as `read`, `write`, `edit`, `apply_patch`, and `bash`
- Process and run helpers such as `job_output`, `job_kill`, `compact_history`, and context/status inspection
- Clarification and memory helpers such as `ask_user_question` and observational memory
- Optional conversation helpers when a conversation store is configured
- Optional integrations exposed through the tool catalog, including MCP, skills, recipes, sourcegraph search, cron, subagent helpers, fetch/search helpers, and other catalog-backed tools

If a tool is missing from a live run, check the corresponding config or integration guard in `internal/harness/tools_default.go` and `internal/harness/tools/catalog.go`.

## Configuration

The server is configured primarily through environment variables. The current code reads more settings than the original docs listed.

### Server And Provider Settings

- `HARNESS_ADDR`
- `OPENAI_API_KEY`
- `OPENAI_BASE_URL`
- `HARNESS_MODEL`
- `HARNESS_SYSTEM_PROMPT`
- `HARNESS_DEFAULT_AGENT_INTENT`
- `HARNESS_MAX_STEPS`
- `HARNESS_MAX_COST_PER_RUN_USD`
- `HARNESS_TOOL_APPROVAL_MODE`
- `HARNESS_ASK_USER_TIMEOUT_SECONDS`
- `HARNESS_MODEL_CATALOG_PATH`
- `HARNESS_PRICING_CATALOG_PATH`

If the loaded model catalog includes an `openrouter` provider, the server enables cached live OpenRouter discovery automatically. No additional discovery-specific environment variable is required in this pass.

### Workspace And Content Roots

- `HARNESS_WORKSPACE`
- `HARNESS_PROMPTS_DIR`
- `HARNESS_RECIPES_DIR`
- `HARNESS_GLOBAL_DIR`
- `HARNESS_ROLLOUT_DIR`
- `HARNESS_SUBAGENT_BASE_REF`
- `HARNESS_SUBAGENT_WORKTREE_ROOT`

### Optional Integrations And Features

- `HARNESS_SKILLS_ENABLED`
- `HARNESS_WATCH_ENABLED`
- `HARNESS_WATCH_INTERVAL_SECONDS`
- `HARNESS_CRON_URL`
- `HARNESS_ENABLE_CALLBACKS`
- `HARNESS_SOURCEGRAPH_ENDPOINT`
- `HARNESS_SOURCEGRAPH_TOKEN`
- `HARNESS_MCP_SERVERS`
- `HARNESS_ROLE_MODEL_PRIMARY`
- `HARNESS_ROLE_MODEL_SUMMARIZER`

### Memory, Retention, And Watcher Settings

- `HARNESS_MEMORY_*`
- `HARNESS_CONVERSATION_RETENTION_DAYS`
- `HARNESS_CONVERSATION_DB`
- `HARNESS_CONCLUSION_WATCHER_ENABLED`
- `HARNESS_CONCLUSION_WATCHER_INTERVENTION_MODE`
- `HARNESS_CONCLUSION_WATCHER_EVALUATOR_ENABLED`
- `HARNESS_CONCLUSION_WATCHER_EVALUATOR_MODEL`

## CLI Flags

`cmd/harnesscli` currently supports:

- `-base-url`
- `-prompt`
- `-model`
- `-system-prompt`
- `-agent-intent`
- `-task-context`
- `-prompt-profile`
- `-prompt-behavior`
- `-prompt-talent`
- `-prompt-custom`
- `-list-profiles`
- `-tui`

The prompt extension flags are forwarded into the run request so the CLI can exercise the same request shape the server accepts.

`harnesscli` also supports an auth helper subcommand:

- `harnesscli auth login` (flags: `-server`, `-tenant`, `-name`)

Additional run-management subcommands:

- `harnesscli list` (flags: `-base-url`, `-status`, `-conversation-id`)
- `harnesscli status <run-id>` (flags: `-base-url`)
- `harnesscli cancel <run-id>` (flags: `-base-url`)

## Source Of Truth

When in doubt, use the implementation as the source of truth:

- HTTP routing and handlers: `internal/server/http.go` and the `internal/server/http_*.go` files
- Run model and request/response types: `internal/harness/types.go`
- Event definitions: `internal/harness/events.go`
- Tool catalog and defaults: `internal/harness/tools_default.go` and `internal/harness/tools/catalog.go`
- Config loading: `internal/config`
