---
title: "Glossary"
sidebar_label: "Glossary"
sidebar_position: 8
---

import { Callout, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

This page is a plain-language lookup for every term used across the go-code documentation. If you hit an unfamiliar word in a guide or error message, find it here, get a one-sentence definition, and follow the cross-link for the full story.

Terms are grouped by theme: core run concepts first, then the composition and orchestration vocabulary, then platform components, and finally the forensics and compliance terms.

---

## Core terms

These terms describe the fundamental units of work that go-code creates and tracks.

<Card>
<CardHeader><CardTitle>run</CardTitle></CardHeader>
<CardContent>
A single agent execution started by `POST /v1/runs`. A run takes a `prompt`, dispatches one or more LLM turns and tool calls, emits a stream of events, and terminates with one of three terminal events: `run.completed`, `run.failed`, or `run.cancelled`. Each run has a unique ID (e.g. `run_abc123`) and belongs to exactly one conversation.

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>conversation</CardTitle></CardHeader>
<CardContent>
The shared message history that accumulates across multiple runs. A new run that passes the same `conversation_id` continues where the previous run left off — the LLM sees the full prior exchange as its context. Conversations are retained for `HARNESS_CONVERSATION_RETENTION_DAYS` (default 30) days, after which they are eligible for cleanup via `POST /v1/conversations/cleanup`.

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>step</CardTitle></CardHeader>
<CardContent>
One full LLM turn inside a run. Each step begins with `run.step.started`, involves a provider call (`llm.turn.requested` → `llm.turn.completed`), may trigger one or more tool calls, and ends with `run.step.completed`. The `HARNESS_MAX_STEPS` environment variable (default `8`) caps the number of steps per run.

See: [harnessd reference](/docs/server/harnessd)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>turn</CardTitle></CardHeader>
<CardContent>
A single provider (LLM) API call within a step. The events `llm.turn.requested` and `llm.turn.completed` bound each turn. Streaming token events (`assistant.message.delta`, `assistant.thinking.delta`) are emitted inside the turn boundary. A step contains exactly one turn.
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>event</CardTitle></CardHeader>
<CardContent>
A JSON-encoded notification emitted by the harness during a run and delivered over the Server-Sent Events (SSE) stream at `GET /v1/runs/{id}/events`. Every event has a `type` (e.g. `tool.call.started`), a `run_id`, a monotonically increasing sequence number, a timestamp, and a `payload` object. Events are the primary real-time interface to an in-progress run.

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>terminal event</CardTitle></CardHeader>
<CardContent>
An event that signals a run has ended. There are exactly three terminal event types: `run.completed`, `run.failed`, and `run.cancelled`. When a terminal event is sent, the SSE stream closes automatically. No further events follow a terminal event in a well-formed rollout.
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>workspace</CardTitle></CardHeader>
<CardContent>
The isolated execution environment in which an agent run operates — a filesystem directory plus the `harnessd` HTTP endpoint that serves it. go-code supports four built-in workspace backends: `local` (host directory), `worktree` (git worktree branch), `container` (Docker), and `vm` (Hetzner Cloud). Specify the type in a run request via the `workspace_type` field. The workspace backend is chosen in the `RunRequest` or via a profile's `isolation_mode` field.

See: [Subagents and profiles](/docs/integrations/subagents-and-profiles)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>sandbox scope</CardTitle></CardHeader>
<CardContent>
A constraint that limits what the agent's shell and file tools can reach. The three levels, set via `permissions.sandbox` in a `RunRequest`, are:

- `"unrestricted"` — no restrictions (default)
- `"local"` — filesystem access is unrestricted, but outbound network commands (`curl`, `wget`, `nc`, etc.) are blocked in the `bash` tool
- `"workspace"` — the `bash` tool can only access paths inside the workspace directory

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>approval policy</CardTitle></CardHeader>
<CardContent>
A rule that determines when a tool call must be explicitly approved by an operator before the agent may proceed. Set via `permissions.approval` in a `RunRequest`:

- `"none"` — never require approval (full auto, default)
- `"destructive"` — require operator approval before mutating tool calls (writes, bash, etc.)
- `"all"` — require approval before every tool call

When a tool call is awaiting approval, the run status becomes `waiting_for_approval` and the event `tool.approval_required` is emitted. Approve or deny via `POST /v1/runs/{id}/approve` or `/deny`.
</CardContent>
</Card>

---

## Composition terms

These terms describe how you structure multi-step agent work.

<Card>
<CardHeader><CardTitle>skill</CardTitle></CardHeader>
<CardContent>
A reusable prompt module authored as a directory containing a `SKILL.md` file (YAML frontmatter + Markdown body). When the agent calls the `skill` tool, the skill's body is injected into the current conversation as a focused instruction block, optionally with a restricted tool set. Skills with `context: fork` run in an isolated sub-agent instead of the current conversation. Skills are discovered from `~/.go-harness/skills/` (global) and `<workspace>/.go-harness/skills/` (local).

See: [Skills guide](/docs/integrations/skills)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>skill pack</CardTitle></CardHeader>
<CardContent>
A YAML manifest bundling a set of related tool permissions and a Markdown instructions file for a specific technology domain (e.g. `cloudflare-deploy`, `railway-deploy`). Skill packs validate prerequisites (`requires_cli`, `requires_env`) before injecting their instructions. Managed by the agent via the `manage_skill_packs` deferred tool.

See: [Skills guide](/docs/integrations/skills)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>profile</CardTitle></CardHeader>
<CardContent>
A named TOML file that bundles runner parameters (model, step and cost limits, system prompt), a tool allowlist, permission policies, and a workspace isolation mode. Profiles decouple task-specific agent configuration from the code that launches runs. Pass a profile name in a `RunRequest` as the `profile` field, or at server startup via the `--profile` flag. Six built-in profiles are embedded in the binary: `full`, `researcher`, `reviewer`, `file-writer`, `bash-runner`, and `github`.

See: [Subagents and profiles](/docs/integrations/subagents-and-profiles)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>subagent</CardTitle></CardHeader>
<CardContent>
A child agent run spawned by a parent run or directly via `POST /v1/subagents`. Subagents have their own run ID, lifecycle, and cleanup policy (`preserve`, `destroy_on_success`, or `destroy_on_completion`). They can run inline (sharing the parent's process) or in a dedicated git worktree (`isolation: "worktree"`). The agent tools `run_agent`, `spawn_agent`, `start_subagent`, and related tools are the agent-side interface; `GET/POST /v1/subagents` is the API-side interface.

See: [Subagents and profiles](/docs/integrations/subagents-and-profiles)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>workflow</CardTitle></CardHeader>
<CardContent>
A Go script-based multi-agent pipeline registered with the workflow `Engine` via `Engine.Register(name, script)` where the script is a `func(ctx *Context) (any, error)`. Workflows compose agents using primitives like `ctx.Agent()`, `ctx.Parallel()`, `ctx.Pipeline()`, `ctx.Phase()`, and `ctx.Log()`. They run asynchronously, emit their own event stream (`workflow.started`, `workflow.agent.completed`, etc.), and are exposed via the `/v1/script-workflows` HTTP routes.

See: [Workflow engine guide](/docs/workflows/workflow-engine)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>workflow bundle</CardTitle></CardHeader>
<CardContent>
A directory containing a `workflow.json` manifest and a Go source file (`main.go` or a custom entrypoint) that the `SourceManager` compiles into a standalone binary at runtime. The binary communicates with the harness over a newline-delimited JSON protocol on stdin/stdout. Bundles are cached by a SHA-256 hash of the directory contents and reused when the hash matches. Scoped to `workspace`, `global`, or `skill` directories.

See: [Workflow SDK guide](/docs/workflows/workflow-sdk)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>recipe</CardTitle></CardHeader>
<CardContent>
A declarative multi-step sequence defined in a YAML file and stored in the directory pointed to by `HARNESS_RECIPES_DIR`. Recipes are executed by the agent via the `run_recipe` deferred tool (`internal/harness/tools/deferred/recipe.go`). A recipe is distinct from a Go workflow (a compiled Go bundle run via `run_workflow`) and from a skill (a prompt module): a recipe is a YAML-defined sequence of steps, while a workflow is a Go program and a skill is a Markdown prompt. Recipes are listed at `GET /v1/recipes`.
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>network</CardTitle></CardHeader>
<CardContent>
An agent-graph definition loaded at startup from the directory pointed to by `HARNESS_NETWORKS_DIR` and served by the `networks.Engine` via `registerNetworkRoutes` in the HTTP server. Networks describe how multiple agents connect and communicate. Research on this feature is limited; for the current HTTP routes see the [HTTP API reference](/docs/reference/http-routes).
</CardContent>
</Card>

---

## Platform terms

These terms describe the daemons, binaries, and infrastructure components.

<Card>
<CardHeader><CardTitle>harnessd</CardTitle></CardHeader>
<CardContent>
The local HTTP daemon (`cmd/harnessd`) that wraps the go-code agent runtime and exposes it over a REST + Server-Sent Events API on port `8080` (default, overridable via `HARNESS_ADDR`). It boots the full runtime: LLM provider, tool registry, memory, cron, MCP clients, skills, and workflows. Start it with `go run ./cmd/harnessd` or via the `go-code` wrapper script.

See: [harnessd reference](/docs/server/harnessd)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>harnesscli</CardTitle></CardHeader>
<CardContent>
The terminal client (`cmd/harnesscli`) that connects to a running `harnessd` over HTTP. It can send prompts, stream run events, list and cancel runs, continue conversations, replay rollouts, and launch the BubbleTea TUI. The `--base-url` flag (default `http://localhost:8080`) controls which server it connects to.

See: [harnesscli reference](/docs/cli/harnesscli)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>go-code wrapper</CardTitle></CardHeader>
<CardContent>
The shell script (`scripts/go-code.sh`, installed as the `go-code` command) that is the primary user-facing launcher. It auto-starts `harnessd` when no healthy server is already running at the configured port, selects a mode based on arguments (TUI, single-prompt, server-only, or a CLI subcommand), and stops the server on exit only if it started it. Project root is detected by looking for `.git/` or `.harness/config.toml` up the directory tree.

See: [go-code wrapper reference](/docs/cli/go-code-wrapper)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>provider / model catalog</CardTitle></CardHeader>
<CardContent>
The JSON file at `catalog/models.json` (installed to `share/go-code/catalog/`) that lists every supported LLM provider and its models. The catalog currently covers ten providers: `openai`, `deepseek`, `groq`, `xai`, `kimi`, `qwen`, `together`, `anthropic`, `openrouter`, and `gemini`. At startup, `harnessd` reads this catalog to build the `ProviderRegistry` used for model routing. Provider and model listings are available at `GET /v1/providers` and `GET /v1/models`.

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>MCP (Model Context Protocol)</CardTitle></CardHeader>
<CardContent>
A JSON-RPC 2.0 protocol for connecting AI models to external tools and data sources. go-code speaks MCP in both directions:

- **As a client**: the agent can call tools hosted on external MCP servers (stdio subprocesses or HTTP endpoints). These appear in the agent's tool list as `mcp_{server}_{tool}` (e.g. `mcp_filesystem_read_file`). Configure them via the `HARNESS_MCP_SERVERS` environment variable or the `[mcp_servers]` TOML section.
- **As a server**: `harnessd` exposes itself as an MCP server at `/mcp` (HTTP) and via `harnessd --mcp` (stdio). The proxy binary `cmd/harness-mcp` bridges Claude Desktop to a running `harnessd` instance.

See: [Consuming MCP servers](/docs/integrations/mcp-consume), [Exposing as MCP server](/docs/server/expose-as-mcp-server)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>relay (Go Relay)</CardTitle></CardHeader>
<CardContent>
The multi-location control plane (`internal/relay/`) that sits above the execution runtime. While go-code executes one run inside one workspace, Go Relay decides *where* and *how* each run should execute across a fleet of registered workers. It owns worker registration, capability inventory, placement routing (scoring eligible workers across hard constraints and soft preferences), run contract composition, event/artifact relay, and checkpointed handoff between locations. Workers are registered via `POST /v1/relay/workers` and require `HARNESS_RELAY_DB` to be configured for persistence.

See: [Relay guide](/docs/workflows/relay)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>Symphony (symphd)</CardTitle></CardHeader>
<CardContent>
The issue-tracker-driven orchestration daemon (`cmd/symphd`, `internal/symphd/`). `symphd` polls a labeled set of GitHub Issues, claims each one as a work item, provisions an isolated workspace (local, worktree, container, VM, or pool), dispatches a `harnessd` run, and applies exponential-backoff retry with a dead-letter queue for exhausted issues. It runs on its own HTTP port (default `:8888`) and is distinct from both the workflow engine (which composes Go functions) and Go Relay (which routes across locations).

See: [Symphony guide](/docs/workflows/symphony)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>cron</CardTitle></CardHeader>
<CardContent>
The embedded scheduler that runs recurring agent jobs on a 5-field UTC cron schedule. When `HARNESS_CRON_URL` is empty (the default), `harnessd` starts an embedded SQLite-backed cron scheduler at `<workspace>/.harness/cron.db`. The agent can create, list, pause, and delete jobs using the `cron_create`, `cron_list`, `cron_pause`, and `cron_delete` deferred tools. The HTTP API is at `/v1/cron/jobs`.

See: [Cron scheduling guide](/docs/integrations/cron-scheduling)
</CardContent>
</Card>

---

## Forensics terms

These terms describe the tools for recording, replaying, diffing, and auditing agent runs.

<Card>
<CardHeader><CardTitle>rollout</CardTitle></CardHeader>
<CardContent>
A JSONL file that records every event emitted during a run. Rollout capture is opt-in: set `HARNESS_ROLLOUT_DIR` to enable it. Files are written to `<RolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl`. Each line is a JSON object with fields `ts`, `seq`, `type`, and `data`. The rollout file is the input to replay, fork, and drift detection.

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>replay</CardTitle></CardHeader>
<CardContent>
An offline integrity check that re-verifies the causal consistency of a recorded JSONL rollout file without re-executing any tools or LLM calls. Replay confirms that every `tool.call.started` event references a call ID announced in a preceding `llm.turn.completed`, that tool names and arguments match, and that every started call has a corresponding completed call. Initiated via `POST /v1/runs/replay` with `mode: "simulate"`.

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>fork</CardTitle></CardHeader>
<CardContent>
A replay mode that reconstructs the conversation history of a recorded run up to a specific step number, then hands that history to a live runner to resume execution or explore an alternate path from that point. Initiated via `POST /v1/runs/replay` with `mode: "fork"` and a `fork_step` value. By default, tool calls and results are stripped from the reconstructed history to prevent injection attacks from recorded tool outputs.

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>drift</CardTitle></CardHeader>
<CardContent>
A regression in harness behavior detected by replaying a recorded run against a `RecordedProvider` (which replays the original LLM turns verbatim) and diffing the resulting event stream against the original. Drift is declared when deterministic fields diverge: step counts, assistant content, tool call names or arguments, or the terminal outcome. Timing and provider metadata are considered variable and never trigger drift. Enable via `detect_drift: true` in a `POST /v1/runs/replay` request.

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>checkpoint</CardTitle></CardHeader>
<CardContent>
A pause/resume gate that allows a human or an automated system to approve, deny, or supply input before a tool call or workflow step proceeds. Checkpoints are separate from rollout files — they live in their own store (in-memory or SQLite) and are exposed via `GET /v1/checkpoints/{id}` and `POST /v1/checkpoints/{id}/resume`. Three checkpoint kinds exist: `"approval"`, `"user_input"`, and `"external_resume"`. Go Relay also uses the concept of a checkpoint boundary for describing where a handoff between workers occurred.

See: [HTTP API guide](/docs/server/http-api-guide)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>audit trail</CardTitle></CardHeader>
<CardContent>
An opt-in, append-only, hash-chained JSONL log for compliance that captures only security-relevant events: `run.started`, `audit.action`, `run.completed`, and `run.failed`. Enable by setting `RunnerConfig.AuditTrailEnabled = true` with `RolloutDir` configured. The audit file is written to `<RolloutDir>/<YYYY-MM-DD>/audit.jsonl` — one file shared across all runs per day. Each entry carries a `prev_hash` and `entry_hash` (SHA-256) forming a tamper-detectable chain.

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>redaction</CardTitle></CardHeader>
<CardContent>
A configurable pipeline (`internal/forensics/redaction/`) that filters sensitive values from event payloads before they are written to rollout files or audit logs. Built-in patterns cover JWTs, PEM private keys, AWS keys, database connection strings, `sk-*`-style API keys, and generic `api_key`/`secret_key`/`access_token` key-value patterns. Matched values are replaced with `[REDACTED:<label>]` (e.g. `[REDACTED:api_key]`). Four storage modes are available: `"redacted"` (default), `"full"`, `"hashed"` (SHA-256), and `"none"` (drop the event entirely).

See: [Rollout, replay, and forensics](/docs/operations/rollout-replay-forensics)
</CardContent>
</Card>

---

<Callout type="warning">
Definitions on this page are derived directly from the source fact sheets and source code. Keep them grounded: the recipe and network entries in particular describe only what is confirmed in the codebase — recipe is a YAML multi-step sequence executed by the `run_recipe` tool, and network is an agent-graph loaded from `HARNESS_NETWORKS_DIR`. Do not infer additional semantics beyond what is stated here.
</Callout>

<Callout type="info">
If a term you encountered in an error message or event payload is missing here, check the [HTTP routes reference](/docs/reference/http-routes) or the event types table in the [HTTP API guide](/docs/server/http-api-guide). The event catalog lists every `type` string the harness can emit.
</Callout>
