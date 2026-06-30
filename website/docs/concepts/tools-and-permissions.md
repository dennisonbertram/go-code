---
title: "Tools, Tiers, and Permissions"
sidebar_label: "Tools & Permissions"
sidebar_position: 3
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The agent runtime exposes a catalog of **tools** — Go functions that an LLM can call to read files, run shell commands, search the web, spawn subagents, and more. Not every tool is visible to the LLM at once. Instead, the catalog is split into two tiers: a compact **core** set that is always present, and a larger **deferred** set that is hidden until the agent activates what it needs. On top of that, every run has a **permission model** that governs filesystem access and whether humans must approve tool calls before they execute.

This page explains both systems: what they are, why they are designed this way, and how to configure them.

---

<Callout variant="warning">
**Security default — read this first.**

When `permissions` is omitted from a run request, the runner applies `DefaultPermissionConfig()`: sandbox `"unrestricted"` and approval `"none"`. This means the agent has **unrestricted access to the filesystem** and **never asks for human approval** before executing any tool call — including writes, deletes, and arbitrary shell commands.

Sandboxing and approval are opt-in. A run with no `permissions` block is fully unsandboxed and full-auto. If you are running the harness in any environment with sensitive data, set permissions explicitly.
</Callout>

---

## Core vs deferred tools

The tool catalog is divided into two tiers, controlled by a `Tier` field on each registered tool.

| Tier | Constant | Behavior |
|------|----------|----------|
| Core | `TierCore = "core"` | Always included in the tool list sent to the LLM |
| Deferred | `TierDeferred = "deferred"` | Hidden from the LLM until activated via `find_tool` |

**Why two tiers?** Every tool definition consumes tokens in the LLM's context window — its name, description, and parameter schema all take space. The full catalog is large. By keeping infrequently-used tools deferred, the runtime delivers a lean, focused tool list to the model by default, reserving context for the actual task.

### Core tools

Core tools are always present. They cover the operations an agent needs on virtually every run: file I/O, shell execution, memory, context management, and a few meta-capabilities.

<Card>
<CardHeader><CardTitle>Selected core tools</CardTitle></CardHeader>
<CardContent>

| Tool | What it does |
|------|-------------|
| `read` | Read a file in the workspace (up to 1 MB, default 16 KB) |
| `write` | Write or append to a file |
| `edit` | Replace exact text in a file (`old_text` → `new_text`) |
| `apply_patch` | Apply a unified diff, a batch of edits, or a single find/replace |
| `bash` | Run a shell command (default timeout 30 s, max 3600 s) |
| `job_output` | Fetch stdout/stderr from a background `bash` job |
| `job_kill` | Kill a background job |
| `AskUserQuestion` | Pause the run and ask a human a question |
| `working_memory` | Per-run key-value store (`set`, `get`, `delete`, `list`) |
| `context_status` | Report estimated context token usage |
| `compact_history` | Compact conversation history to reduce context pressure |
| `todos` | Manage a per-run todo list |
| `skill` | Run a named skill by name and optional args |
| `find_tool` | Activate deferred tools by keyword search or direct select |

</CardContent>
</Card>

Source: `internal/harness/tools_default.go:191–228`.

### Deferred tools and `find_tool`

Deferred tools are registered into the catalog but **not** sent to the LLM in the initial tool list. The agent activates them by calling `find_tool`, a core meta-tool that accepts either a keyword `query` or a direct `select:<name>` specifier.

When `find_tool` activates a tool, the tool is added to the LLM's available set for the remainder of that run. The `ActivationTracker` (`internal/harness/activation.go`) maintains this per-run state and cleans it up when the run ends.

Deferred tool groups include:

- **Git / code intelligence** — `git_log_search`, `git_file_history`, `git_blame_context`, `git_diff_range`
- **Web** — `web_search`, `web_fetch`, `agentic_fetch`
- **Agent orchestration** — `run_agent`, `spawn_agent`, `start_subagent`, `wait_subagent`
- **MCP integration** — `connect_mcp`, `list_mcp_resources`, `read_mcp_resource`, plus dynamically registered `mcp_<server>_<tool>` tools
- **Scheduling** — `cron_create`, `cron_list`, `cron_get`, `cron_delete`, `set_delayed_callback`
- **Workflows and skills** — `create_workflow`, `run_workflow`, `create_skill`, `verify_skill`
- **Profile management** — `list_profiles`, `get_profile`, `create_profile`, `update_profile`

Source: `internal/harness/tools_default.go:224–470`.

<Callout variant="info">
LSP tools (`lsp_diagnostics`, `lsp_references`) are defined but are **not** included in the default registry. They require a running language server and must be wired manually.
</Callout>

---

## Permission model

Every run operates under a `PermissionConfig` with two independent axes: **sandbox scope** and **approval policy**.

```go
// internal/harness/types.go:693-696
type PermissionConfig struct {
    Sandbox  SandboxScope   `json:"sandbox"`
    Approval ApprovalPolicy `json:"approval"`
}
```

### Sandbox scope

The sandbox scope controls what the agent's `bash` tool can access.

<Tabs defaultValue="unrestricted">
  <TabsList>
    <TabsTrigger value="unrestricted">unrestricted</TabsTrigger>
    <TabsTrigger value="local">local</TabsTrigger>
    <TabsTrigger value="workspace">workspace</TabsTrigger>
  </TabsList>
  <TabsContent value="unrestricted">

**`"unrestricted"`** — No filesystem restrictions. This is the default when `permissions` is omitted.

The agent can read and write any path on the host filesystem and run arbitrary shell commands.

  </TabsContent>
  <TabsContent value="local">

**`"local"`** — Filesystem access is unrestricted, but outbound network commands (`curl`, `wget`, `nc`, `netcat`, `telnet`) are blocked inside `bash`.

Use this when you want to prevent exfiltration over the network while still allowing full local filesystem access.

  </TabsContent>
  <TabsContent value="workspace">

**`"workspace"`** — Bash commands that reference absolute paths outside the workspace or attempt `cd ..` escapes are rejected. This is a defence-in-depth heuristic, not a kernel-level filesystem jail — it tokenizes the command for out-of-workspace absolute paths and matches `cd ..` patterns. Network access is unrestricted under this scope.

This scope is recommended for untrusted prompts operating on a bounded codebase.

  </TabsContent>
</Tabs>

Source: `internal/harness/types.go:670–677`.

### Approval policy

The approval policy controls when the agent must pause and wait for a human operator to approve a tool call before it executes.

<Tabs defaultValue="none">
  <TabsList>
    <TabsTrigger value="none">none</TabsTrigger>
    <TabsTrigger value="destructive">destructive</TabsTrigger>
    <TabsTrigger value="all">all</TabsTrigger>
  </TabsList>
  <TabsContent value="none">

**`"none"`** — Never ask for approval. The agent runs fully autonomously. This is the default.

  </TabsContent>
  <TabsContent value="destructive">

**`"destructive"`** — Require approval before mutating tool calls (writes, `bash`, deletes, etc.). Read-only operations proceed without interruption.

  </TabsContent>
  <TabsContent value="all">

**`"all"`** — Require approval before every tool call, including reads.

  </TabsContent>
</Tabs>

Source: `internal/harness/types.go:684–690`.

### How approval requests flow through the API

When a tool call requires approval, the run transitions to `waiting_for_approval` status and the SSE stream emits a `tool.approval_required` event:

```json
{
  "type": "tool.approval_required",
  "payload": {
    "call_id": "call_abc",
    "tool": "bash",
    "arguments": "{\"command\":\"rm -rf build/\"}",
    "deadline_at": "2024-01-15T12:00:30Z"
  }
}
```

The operator approves or denies via the HTTP API:

```bash
# Approve
curl -X POST http://localhost:8080/v1/runs/{id}/approve

# Deny
curl -X POST http://localhost:8080/v1/runs/{id}/deny
```

After approval, the stream emits `tool.approval_granted` and execution continues. After denial, the stream emits `tool.approval_denied` and the tool returns a `permission_denied` result to the LLM — the run does not fail, it continues with that outcome.

Source: `internal/harness/approval_broker.go:72`, `internal/server/http_runs.go`.

---

## Restricting a run with `allowed_tools`

In addition to the permission model, you can limit which tools an agent can even attempt to call by passing an `allowed_tools` allowlist in the `RunRequest`.

```json
{
  "prompt": "Summarize the README file",
  "allowed_tools": ["read", "bash", "working_memory"],
  "permissions": {
    "sandbox": "workspace",
    "approval": "none"
  }
}
```

When `allowed_tools` is non-empty, the LLM only sees the listed tools. If it is empty or omitted, all registered tools are available.

### AlwaysAvailableTools

Three tools always bypass the `allowed_tools` filter regardless of what is listed:

```go
// internal/harness/skill_constraint.go:13
var AlwaysAvailableTools = map[string]bool{
    "AskUserQuestion": true,
    "find_tool":       true,
    "skill":           true,
}
```

These are infrastructure tools the agent needs to function: `AskUserQuestion` for human-in-the-loop interactions, `find_tool` for activating deferred tools, and `skill` for running named skills. You cannot exclude them via `allowed_tools`.

### Skill constraints

When the agent invokes a skill that carries its own tool-filter constraint, that constraint temporarily **overrides** the base `allowed_tools` for the duration of the skill execution. The `skill.constraint.activated` and `skill.constraint.deactivated` events mark the boundaries of this override.

Source: `internal/harness/types.go:383–389`, `internal/harness/skill_constraint.go`.

---

## Putting it together — a hardened RunRequest

Here is a `RunRequest` body that activates all three safety levers: workspace-confined sandbox, approval required for destructive calls, and a narrow tool allowlist.

```json
{
  "prompt": "Refactor internal/server/http.go to extract the route table",
  "model": "gpt-4.1",
  "allowed_tools": [
    "read",
    "edit",
    "apply_patch",
    "bash",
    "git_status",
    "working_memory"
  ],
  "permissions": {
    "sandbox": "workspace",
    "approval": "destructive"
  }
}
```

Send it to a running `harnessd` instance:

```bash
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Refactor internal/server/http.go to extract the route table",
    "model": "gpt-4.1",
    "allowed_tools": ["read", "edit", "apply_patch", "bash", "working_memory"],
    "permissions": {
      "sandbox": "workspace",
      "approval": "destructive"
    }
  }'
```

For a key-free smoke test, start `harnessd` with the fake provider first:

```bash
HARNESS_PROVIDER=fake HARNESS_AUTH_DISABLED=true go run ./cmd/harnessd
```

Source: `internal/harness/types.go:319–425`, `cmd/harnessd/main.go`.

---

<Callout variant="warning">
`permissions` is a **nested object** inside the run body — not top-level `sandbox`/`approval` fields. Passing `{"sandbox": "workspace"}` at the top level has no effect; it must be `{"permissions": {"sandbox": "workspace", "approval": "destructive"}}`.
</Callout>

---

## Quick reference

<Card>
<CardHeader><CardTitle>Sandbox scopes</CardTitle></CardHeader>
<CardContent>

| Value | Filesystem access | Network (bash) |
|-------|------------------|----------------|
| `"unrestricted"` | Anywhere (default) | Unrestricted |
| `"local"` | Anywhere | Blocked (`curl`, `wget`, `nc`, etc.) |
| `"workspace"` | Workspace directory only (heuristic) | Unrestricted |

</CardContent>
</Card>

<Card>
<CardHeader><CardTitle>Approval policies</CardTitle></CardHeader>
<CardContent>

| Value | Reads | Writes / bash | All calls |
|-------|-------|--------------|-----------|
| `"none"` | Auto | Auto (default) | — |
| `"destructive"` | Auto | Requires approval | — |
| `"all"` | Requires approval | Requires approval | Requires approval |

</CardContent>
</Card>

---

## Next steps

- **[Events](/docs/concepts/events)** — see the full list of tool-related events (`tool.call.started`, `tool.approval_required`, `tool.call.blocked`, and more) that surface in the SSE stream.
- **[HTTP API](/docs/reference/http-routes)** — complete `RunRequest` field reference and the approve/deny endpoint details.
