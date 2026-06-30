---
title: "Agent Tool Catalog"
sidebar_label: "Tool Catalog"
sidebar_position: 6
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The **tool catalog** is the complete set of capabilities the coding agent can call during a run. Every tool is a Go function registered into a `Registry` and exposed to the LLM via JSON-schema definitions. Understanding the catalog lets you predict which tools an agent will see, enable optional tool groups by satisfying their dependencies, and scope a run to only the tools a task actually needs.

Two terms to know upfront:

- **Core tool** — always sent to the LLM on every run.
- **Deferred tool** — hidden by default; the agent must activate it with `find_tool` before it can call it. This keeps the LLM's context window from being flooded by rarely-used capabilities.

<Callout type="warning">
**Default permission model: unrestricted.** Every tool executes under the run's `PermissionConfig`, which defaults to `{sandbox: "unrestricted", approval: "none"}`. That means the agent has unrestricted filesystem access and is never asked for approval before calling a mutating tool — unless the caller opts in to a stricter policy. See [Tools and Permissions](/docs/concepts/tools-and-permissions) for how to harden this.
</Callout>

---

## How the catalog is built

The production entry point is `NewDefaultRegistryWithOptions` in `internal/harness/tools_default.go`. It:

1. Builds **core** tools from `internal/harness/tools/core/` — always visible.
2. Builds **deferred** tools from `internal/harness/tools/deferred/` — hidden until `find_tool` activates them.
3. Registers `find_tool` itself as a core meta-tool.
4. Wraps every handler with `htools.ApplyPolicy(...)` for approval enforcement.

A separate legacy function, `BuildCatalog` (`internal/harness/tools/catalog.go`), produces a flat sorted slice and is used in non-default paths (tests, custom harnesses). Both paths share the same underlying tool implementations.

Tool tier constants (defined in `internal/harness/tools/types.go`):

| Tier | Constant | Visibility |
|------|----------|------------|
| Core | `TierCore = "core"` | Always sent to the LLM |
| Deferred | `TierDeferred = "deferred"` | Activated via `find_tool` |

---

## Core tools

These tools are visible to the agent on every run. You cannot remove them via `allowed_tools` (except for the three that are _always_ available — see [Per-run tool filtering](#per-run-tool-filtering) below).

<Tabs defaultValue="files">
<TabsList>
  <TabsTrigger value="files">File I/O</TabsTrigger>
  <TabsTrigger value="shell">Shell & Jobs</TabsTrigger>
  <TabsTrigger value="memory">Memory & Context</TabsTrigger>
  <TabsTrigger value="search">Search & Nav</TabsTrigger>
  <TabsTrigger value="skills-meta">Skills & Meta</TabsTrigger>
</TabsList>
<TabsContent value="files">

| Tool | Mutating | Key parameters |
|------|----------|----------------|
| `read` | no | `path` (or alias `file_path`), `offset`, `limit`, `max_bytes` (default 16 KB, max 1 MB) |
| `write` | yes | `path`, `content`, `append`, `expected_version` |
| `edit` | yes | `path`, `old_text`, `new_text`, `replace_all`, `expected_version` |
| `apply_patch` | yes | `patch` for a unified diff; or `edits` array; or single find/replace via `find`/`replace`/`replace_all` |
| `file_inspect` | no | `path`, `preview_lines` (default 20), `hex_bytes` (default 256) |
| `download` | yes | `url`, `file_path` (required), `timeout_seconds` (default 20), `max_bytes` (default 50 MB). Core tier — always registered. |

</TabsContent>
<TabsContent value="shell">

| Tool | Mutating | Key parameters |
|------|----------|----------------|
| `bash` | yes | `command` (required), `timeout_seconds` (default 30, max 3600), `working_dir`, `run_in_background`, `description`. Strips `sudo`; rejects dangerous patterns. |
| `job_output` | no | `shell_id` (required), `wait` (optional). Fetches stdout/stderr from a background job. |
| `job_kill` | yes | `shell_id`. Terminates a background job. |

</TabsContent>
<TabsContent value="memory">

| Tool | Mutating | Key parameters |
|------|----------|----------------|
| `working_memory` | yes | `action` (`set`/`get`/`delete`/`list`), `key`, `value`. Per-run key-value store. |
| `observational_memory` | yes | `action` (`enable`/`disable`/`status`/`export`/`review`/`reflect_now`). Long-term memory. |
| `context_status` | no | No parameters. Reports estimated token count and a health label (`healthy`/`elevated`/`warning`/`critical`). |
| `compact_history` | yes | `mode` (`strip`/`summarize`/`hybrid`), `keep_last` (default 4). Reduces context pressure. |
| `todos` | yes | `action` (`set`/`update`/`delete`/`get`). Per-run todo list. |

</TabsContent>
<TabsContent value="search">

| Tool | Mutating | Key parameters |
|------|----------|----------------|
| `ls` | no | `path`, `recursive`, `depth`, `include_hidden`, `max_entries` (default 200) |
| `glob` | no | `pattern`. Default max 500 matches. |
| `grep` | no | `pattern`, `path`, `regex`, `case_sensitive`, `literal_text`, `max_matches` (default 200, max 2000) |
| `git_status` | no | `porcelain` (boolean, default `true`). Runs `git status --porcelain=v1` by default; pass `porcelain: false` for plain `git status`. |
| `git_diff` | no | `staged`, `target`, `path`, `max_bytes` |
| `fetch` | no | `url`, `format`, `timeout_seconds` (default 20, max 120), `max_bytes` (default 128 KB, max 1 MB) |

</TabsContent>
<TabsContent value="skills-meta">

| Tool | Mutating | Key parameters |
|------|----------|----------------|
| `skill` | no | `command` string — `"<skill-name> [args...]"`. Runs a named skill or built-in sub-commands (`list`, `verify`). |
| `find_tool` | — | `query` (keyword search) or `select:<name>` (direct activation). Activates matched deferred tools for this run. |
| `AskUserQuestion` | — | `question`, `options`. Pauses the run and asks a human. Requires `AskUserBroker`. Default timeout: 5 minutes. |
| `list_conversations` | no | Lists prior conversations (requires `ConversationStore`). |
| `search_conversations` | no | `query`. Full-text search over conversation history. |

</TabsContent>
</Tabs>

---

## Deferred tools

Deferred tools are invisible until the agent calls `find_tool`. Once activated, they remain available for the duration of that run. Activation is tracked per-run by `ActivationTracker` (`internal/harness/activation.go`).

To see which tools are available, the agent calls:

```json
{
  "name": "find_tool",
  "input": { "query": "git history" }
}
```

Or to activate a specific tool by name:

```json
{
  "name": "find_tool",
  "input": { "query": "select:git_log_search" }
}
```

### Git and code intelligence

| Tool | What it does |
|------|--------------|
| `git_log_search` | Search commit history by message (`--grep`) or diff content (`-S` pickaxe), or both. Params: `query`, `mode` (`message`/`pickaxe`/`both`), `path`, `max_results` (default 20), `since`. |
| `git_file_history` | File-level commit history with diff summaries. |
| `git_blame_context` | Blame annotations with surrounding context lines. |
| `git_diff_range` | Diff between two refs. |
| `git_contributor_context` | Contributor activity statistics for a file or repo. |
| `sourcegraph` | Search Sourcegraph. Requires `Sourcegraph.Endpoint` in server config. |

<Callout type="warning">
**LSP tools are not in the default registry.** `lsp_diagnostics`, `lsp_references`, and `lsp_restart` require a running language server and must be wired manually — they are explicitly excluded from `NewDefaultRegistryWithOptions` (`internal/harness/tools_default.go`). Do not expect them to appear even after `find_tool`.
</Callout>

### Web

Registered when `EnableAgent && EnableWebOps && WebFetcher != nil`.

| Tool | What it does |
|------|--------------|
| `web_search` | Keyword web search, up to 50 results (default 5). |
| `web_fetch` | Fetch a single web page via `WebFetcher`. |
| `agentic_fetch` | Agent-assisted fetch — uses `AgentRunner` to process the page. |

### Scheduling

Registered when `EnableCron && CronClient != nil`.

| Tool | What it does |
|------|--------------|
| `cron_create` | Create a recurring job. Params: `name`, `schedule` (5-field UTC cron), `command`, `timeout_seconds` (default 30). |
| `cron_list` | List all cron jobs. |
| `cron_get` | Get a job and its 5 most recent executions by ID. |
| `cron_delete` | Delete a cron job (soft-delete). |
| `cron_pause` | Pause a job (sets `status=paused`). |
| `cron_resume` | Resume a paused job (sets `status=active`). |

Registered when `EnableCallbacks && CallbackManager != nil`.

| Tool | What it does |
|------|--------------|
| `set_delayed_callback` | Schedule a one-shot callback that re-invokes the agent after a delay. Min 5 s, max 1 hour, max 10 per conversation. |
| `cancel_delayed_callback` | Cancel a pending delayed callback. |
| `list_delayed_callbacks` | List all pending delayed callbacks for the current conversation. |

### Agent orchestration

Registered when `EnableAgent && AgentRunner != nil`.

| Tool | What it does |
|------|--------------|
| `agent` | Inline sub-agent call via `AgentRunner.RunPrompt`. |
| `spawn_agent` | Spawn a recursive child agent. Max fork depth: 5 (`DefaultMaxForkDepth`). |
| `task_complete` | Used by child agents to return a result to their parent. Not available at depth 0. |

Registered when `SubagentManager != nil`.

| Tool | What it does |
|------|--------------|
| `run_agent` | Spawn a subagent with a named profile and wait for it. Params: `task`, `profile`, `model`, `max_steps`. |
| `start_subagent` | Start a subagent (fire-and-forget). |
| `get_subagent` | Poll subagent status by ID. |
| `wait_subagent` | Block until a subagent completes. |
| `cancel_subagent` | Cancel a running subagent. |

### MCP integration

Registered when `EnableMCP && MCPRegistry != nil`.

| Tool | What it does |
|------|--------------|
| `list_mcp_resources` | List MCP resources across all connected servers. |
| `read_mcp_resource` | Read a single MCP resource by URI. |
| `mcp_<server>_<tool>` | Any tool from a connected MCP server (dynamic). |

Registered when `MCPConnector != nil` (wired after the registry is built, independent of `EnableMCP`).

| Tool | What it does |
|------|--------------|
| `connect_mcp` | Connect to a new HTTP/SSE MCP server mid-session. Registers its tools as `mcp_<server>_<tool>`. |

### Profile management

Most profile tools are always registered; `create_profile`, `update_profile`, and `delete_profile` require `ProfilesDir`.

| Tool | What it does |
|------|--------------|
| `list_profiles` | List available agent profiles. |
| `get_profile` | Get a profile definition. |
| `get_profile_manifest` | Get the effective tool manifest for a profile. |
| `create_profile` | Create a new profile TOML. Requires `ProfilesDir`. |
| `update_profile` | Update an existing profile. Requires `ProfilesDir`. |
| `delete_profile` | Delete a profile. Requires `ProfilesDir`. |
| `validate_profile` | Dry-run validate a profile definition. |
| `recommend_profile` | Suggest a profile for a given task. |
| `get_efficiency_report` | Report on profile run history. |

### Skills and workflows

| Tool | Enabling condition | What it does |
|------|--------------------|--------------|
| `create_skill` | `SkillsDir != ""` | Author a new `SKILL.md` file. |
| `verify_skill` | `SkillVerifier != nil` | Validate a skill and write verification metadata. |
| `manage_skill_packs` | `PackRegistry` configured | Manage skill pack subscriptions (`list`/`search`/`activate`). |
| `create_workflow` | `WorkflowService != nil` | Author a new Go workflow. Params: `name`, `description`, `source`, `scope`. |
| `run_workflow` | `WorkflowService != nil` | Run a named workflow. Params: `name`, `args`, `wait`, `timeout_seconds`, `resume_run_id`. |
| `run_recipe` | `RecipesDir != ""` | Execute a multi-step recipe from a YAML file. |
| `create_prompt_extension` | always registered | Create a behavior or talent prompt extension. |

---

## Activation and naming

### `find_tool` — the gateway to deferred tools

`find_tool` is a **core** meta-tool, so it is always in the LLM's context. It accepts either:

- `query` — keyword search over deferred tool names, descriptions, and tags.
- `select:<name>` — directly activate a tool by exact name.

Once `find_tool` activates a tool, it becomes visible in subsequent LLM turns for that run only.

<Callout type="info">
A `tool.activated` event type (constant `EventToolActivated`) is reserved for this activation, but it has no confirmed production emission site today — do not build consumers that depend on receiving it. See the [Event Catalog](/docs/reference/events-catalog#reserved-and-unconfirmed-events).
</Callout>

### MCP tool naming

When an external MCP server is connected (globally at startup or per-run via `mcp_servers`), each of its tools is registered with the name:

```
mcp_<server>_<tool>
```

Both `<server>` and `<tool>` are sanitized: lowercased, and `-`, `/`, `.`, and space are replaced with `_`. For example, the `read_file` tool on the `filesystem` server becomes `mcp_filesystem_read_file`.

**Implementation:** `internal/harness/registry.go`

```go
toolName := "mcp_" + safeServer + "_" + safeName
```

MCP tools are always `TierDeferred` and are tagged `["mcp", "integration", "external", "dynamic", "mcp_server:<serverName>"]`.

<Callout type="info">
The runbook at `docs/runbooks/mcp.md` incorrectly states the format as `{server_name}__{tool_name}` (double underscore). The code uses `mcp_{server}_{tool}` (single underscore with `mcp_` prefix). Trust the code.
</Callout>

### `AlwaysAvailableTools`

Three tools always bypass the `AllowedTools` filter and any active skill constraint:

```go
var AlwaysAvailableTools = map[string]bool{
    "AskUserQuestion": true,
    "find_tool":       true,
    "skill":           true,
}
```

**Source:** `internal/harness/skill_constraint.go`

---

## Enabling conditions

Tool groups are gated by flags and runtime dependencies. A group is silently absent when its condition is not met — there is no error.

| Tool group | Count | Enabling condition | Source |
|------------|-------|--------------------|--------|
| Cron tools | 6 | `EnableCron && CronClient != nil` | `catalog.go:86`, `tools_default.go:273` |
| Callback tools | 3 | `EnableCallbacks && CallbackManager != nil` | `catalog.go:97`, `tools_default.go:283` |
| LSP tools | 3 | `EnableLSP` (not in default registry) | `catalog.go:57` |
| Sourcegraph | 1 | `Sourcegraph.Endpoint != ""` | `catalog.go:60` |
| MCP tools | dynamic | `EnableMCP && MCPRegistry != nil` | `catalog.go:63` |
| Skills | varies | `EnableSkills && SkillLister != nil` | `catalog.go:74` |
| Web ops | 3 | `EnableAgent && EnableWebOps && WebFetcher != nil` | `catalog.go:82` |
| Recipes | 1 | `RecipesDir != ""` | `tools_default.go:298` |
| Agent tools (`agent`, `spawn_agent`, `task_complete`) | 3 | `EnableAgent && AgentRunner != nil` | `tools_default.go:256` |
| Subagent tools (`run_agent`, `start/get/wait/cancel_subagent`) | 5 | `SubagentManager != nil` | `tools_default.go:344` |
| Workflow tools | 2 | `WorkflowService != nil` | `tools_default.go:331` |

### Recipes vs workflows vs skills — what's the difference?

These three abstractions are related but distinct:

<Card>
<CardHeader>
<CardTitle>Recipe</CardTitle>
</CardHeader>
<CardContent>
A **declarative YAML file** that defines a named sequence of tool calls (steps). Each step specifies a tool name and static args; `{{variable}}` placeholders are substituted at execution time. Recipes live in `RecipesDir` (env: `HARNESS_RECIPES_DIR`) and are executed by the `run_recipe` deferred tool. They are the right choice for deterministic, repeatable multi-step sequences that you want to author without writing Go code.
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Workflow (Go workflow)</CardTitle>
</CardHeader>
<CardContent>
A **compiled Go bundle** registered with the workflow engine (`internal/workflow/`). Workflows use primitives like `ctx.Agent()`, `ctx.Parallel()`, and `ctx.Pipeline()` to compose sub-agents and stages. They support full Go logic, budget tracking, and schema validation. Executed via the `run_workflow` deferred tool or the `POST /v1/script-workflows/{name}/runs` HTTP route.
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Skill</CardTitle>
</CardHeader>
<CardContent>
A **prompt module** — a `SKILL.md` file with YAML frontmatter and a Markdown body. When invoked, the skill's body is injected into the conversation (or spawned in a forked subagent for `context: fork` skills). Skills constrain the agent's tool access via `allowed-tools` for the duration of the invocation. See [Skills](/docs/integrations/skills).
</CardContent>
</Card>

---

## Per-run tool filtering

To restrict the tools available to a specific run, pass `allowed_tools` in the `POST /v1/runs` body:

```json
{
  "prompt": "Review this PR for security issues",
  "allowed_tools": ["read", "grep", "glob", "ls", "git_diff"]
}
```

When `allowed_tools` is non-empty, only the listed names **plus** `AlwaysAvailableTools` (`AskUserQuestion`, `find_tool`, `skill`) are offered to the LLM. An empty or omitted list means all tools are available.

**Source:** `internal/harness/types.go` — `AllowedTools []string \`json:"allowed_tools,omitempty"\``

---

## Default permissions (security)

<Callout type="warning">
By default, tools run without any sandboxing and without any approval prompts. This is appropriate for trusted development environments and automated pipelines where you control the workspace. For anything handling untrusted input, explicitly set a `permissions` policy.
</Callout>

The `PermissionConfig` struct (source: `internal/harness/types.go`) controls two independent axes:

**Sandbox scope** (`sandbox` field):

| Value | Behavior |
|-------|----------|
| `"unrestricted"` | No restrictions. **Default.** |
| `"local"` | Filesystem access unrestricted; outbound network commands (`curl`, `wget`, `nc`, `netcat`, `telnet`) blocked in bash. |
| `"workspace"` | Bash can only access paths inside the workspace directory. |

**Approval policy** (`approval` field):

| Value | Behavior |
|-------|----------|
| `"none"` | Never ask for approval. **Default.** |
| `"destructive"` | Require operator approval before mutating tool calls (writes, bash, etc.). |
| `"all"` | Require approval before every tool call. |

To harden a run, pass a `permissions` object in the run request:

```json
{
  "prompt": "...",
  "permissions": {
    "sandbox": "workspace",
    "approval": "destructive"
  }
}
```

Approval requests flow through `ApprovalBroker` (`internal/harness/approval_broker.go`) and are resolved via `POST /v1/runs/{id}/approve` or `POST /v1/runs/{id}/deny`. See [Tools and Permissions](/docs/concepts/tools-and-permissions) for the full approval workflow.

---

## Next steps

- **Run your first agent:** [Quickstart](/docs/getting-started/quickstart)
- **Understand the permission model in depth:** [Tools and Permissions](/docs/concepts/tools-and-permissions)
- **Configure MCP servers:** [MCP Integration](/docs/integrations/mcp-consume)
- **Author and invoke skills:** [Skills](/docs/integrations/skills)
- **Write and run Go workflows:** [Workflow Engine](/docs/workflows/workflow-engine)
