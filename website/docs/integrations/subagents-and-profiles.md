---
title: "Subagents and Profiles"
sidebar_label: "Subagents & Profiles"
sidebar_position: 3
---

import { Callout, Steps, Step, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent, Badge } from '@site/src/components/ui';

Subagents and profiles are the two building blocks for multi-agent orchestration in go-code.
A **subagent** is a child agent run you create to handle a bounded piece of work — it has its
own run ID, tool set, cost budget, and optionally its own isolated git workspace. A **profile**
is a named TOML file that bundles model choice, step limit, cost ceiling, and a tool allowlist
into a reusable preset.

Together they let you build the canonical orchestration pattern: a top-level agent reads a
task, selects a profile appropriate for that task, spawns a scoped child run that only has
access to what it needs, and waits for the result — without baking any of that configuration
into code.

---

## Subagents

### Creating a subagent

Send a `POST /v1/subagents` request. The body must include either `prompt` **or** `skill` — not
both. All other fields are optional.

```bash
curl -s -X POST http://localhost:8080/v1/subagents \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Review internal/parser/parser.go for correctness and missing error handling",
    "profile": "reviewer",
    "isolation": "worktree",
    "cleanup_policy": "destroy_on_completion"
  }'
```

A successful response is HTTP 201 with a subagent object:

```json
{
  "id": "subagent_3e2f…",
  "run_id": "run_abc…",
  "status": "running",
  "isolation": "worktree",
  "cleanup_policy": "destroy_on_completion",
  "workspace_path": "/path/to/repo-subagents/subagent_3e2f",
  "branch_name": "subagent/subagent_3e2f",
  "created_at": "2026-06-28T12:00:00Z",
  "updated_at": "2026-06-28T12:00:00Z"
}
```

Subagent IDs always use the prefix `subagent_` followed by a UUID.

### Using a skill as the prompt

Specify `skill` (and optionally `skill_args`) instead of a literal `prompt`. The two fields are
mutually exclusive — the server rejects a request that sets both.

```json
{
  "skill": "go-deps",
  "skill_args": "./internal/...",
  "profile": "file-writer",
  "isolation": "inline"
}
```

The harness resolves the named skill to its body (with variable substitution applied) and uses
that as the subagent's prompt.

### Request fields reference

| JSON field | Type | Description |
|---|---|---|
| `prompt` | string | Task prompt. Mutually exclusive with `skill`. |
| `skill` | string | Named skill to resolve as prompt. |
| `skill_args` | string | Arguments passed to the skill resolver. |
| `profile` | string | Profile name to apply (see [Profiles](#profiles) below). |
| `model` | string | Override the model for this subagent. |
| `provider_name` | string | Override the provider. |
| `allow_fallback` | bool | Allow automatic provider fallback on transient errors. |
| `system_prompt` | string | Override the system prompt. |
| `max_steps` | int | Maximum tool-call steps. |
| `max_cost_usd` | float64 | Per-run cost ceiling in USD. |
| `reasoning_effort` | string | Reasoning hint: `"low"`, `"medium"`, or `"high"`. |
| `allowed_tools` | []string | Tool allowlist (empty = all tools). |
| `permissions` | object | Sandbox policy override (`sandbox`, `approval`). |
| `isolation` | string | `"inline"` or `"worktree"` (default: `"inline"`). |
| `cleanup_policy` | string | `"preserve"`, `"destroy_on_success"`, or `"destroy_on_completion"`. |
| `worktree_root` | string | Override the worktree root directory. |
| `base_ref` | string | Git base ref for the worktree. |

### Isolation modes

The `isolation` field controls whether the subagent gets its own filesystem.

<Tabs defaultValue="inline">
  <TabsList>
    <TabsTrigger value="inline">inline (default)</TabsTrigger>
    <TabsTrigger value="worktree">worktree</TabsTrigger>
  </TabsList>
  <TabsContent value="inline">

**`isolation: "inline"`** — the subagent runs in the same workspace as its parent using the same
runner infrastructure. No separate filesystem is provisioned; `workspace_path` in the response
will be empty. Use this when the child needs to work in the same directory tree as the parent or
when you want the lightest possible overhead.

  </TabsContent>
  <TabsContent value="worktree">

**`isolation: "worktree"`** — a fresh git worktree is provisioned for the subagent under
`worktree_root` (default: `<repoParentDir>/<repoName>-subagents`). A separate runner is
created for that workspace, and when a harness config is set on the server, a `harness.toml`
file is written into the worktree. The response includes `workspace_path` and `branch_name`.
Use this when you want the child to make changes independently without touching the parent's
working tree.

Requires `RepoPath` to be configured on the server.

  </TabsContent>
</Tabs>

<Callout type="warning">
  The profile schema accepts `isolation_mode` values `"container"` and `"vm"`, but only
  `"inline"` and `"worktree"` have concrete subagent code paths today. Specifying `"container"`
  or `"vm"` in a subagent request will return an error.
</Callout>

### Cleanup policies

When a subagent's run reaches a terminal state (`completed`, `failed`, or `cancelled`), a
background monitor applies the cleanup policy. This only has visible effect for worktree-isolated
subagents where a workspace was provisioned.

| Policy | JSON value | When workspace is removed |
|---|---|---|
| `CleanupPreserve` | `"preserve"` | Never — requires explicit `DELETE /v1/subagents/{id}` |
| `CleanupDestroyOnSuccess` | `"destroy_on_success"` | Only when `status` is `completed` |
| `CleanupDestroyOnCompletion` | `"destroy_on_completion"` | On any terminal status |

<Callout type="info">
  The default cleanup policy differs by layer. When you call `POST /v1/subagents` directly, the
  default is `"preserve"` — the workspace is kept until you explicitly delete it. When a running
  agent spawns a subagent through the in-run tool layer (`start_subagent`), the default is
  `"destroy_on_completion"`.
</Callout>

### Polling, waiting, and cancelling

Once you have a subagent ID, you have four management operations:

<Steps>
  <Step>
    **Poll for status** — `GET /v1/subagents/{id}` returns the current subagent object.
    Useful when you want non-blocking progress checks.

    ```bash
    curl -s http://localhost:8080/v1/subagents/subagent_3e2f...
    ```
  </Step>
  <Step>
    **Block until terminal** — `POST /v1/subagents/{id}/wait` holds the HTTP connection open,
    polling internally at 200 ms intervals, and returns the subagent object only when the run
    reaches a terminal status. The response `output` field contains the agent's final output.

    ```bash
    curl -s -X POST http://localhost:8080/v1/subagents/subagent_3e2f.../wait
    ```
  </Step>
  <Step>
    **Cancel** — `POST /v1/subagents/{id}/cancel` requests cooperative cancellation. The
    cancel endpoint returns an acknowledgement (`{"id":…,"status":"cancelling"}`); the
    subagent's actual status then transitions to `cancelled` (there is no persisted
    `cancelling` status).

    ```bash
    curl -s -X POST http://localhost:8080/v1/subagents/subagent_3e2f.../cancel
    ```
  </Step>
  <Step>
    **Delete** — `DELETE /v1/subagents/{id}` removes the subagent record and, for worktree
    isolation, destroys the workspace. Returns HTTP 409 Conflict if the subagent is still active
    (`queued`, `running`, or `waiting_for_user`). Cancel it first.

    ```bash
    curl -s -X DELETE http://localhost:8080/v1/subagents/subagent_3e2f...
    ```
  </Step>
</Steps>

### Subagents route summary

| Method | Path | Scope | Notes |
|---|---|---|---|
| `GET` | `/v1/subagents` | `runs:read` | List all subagents |
| `POST` | `/v1/subagents` | `runs:write` | Create — returns HTTP 201 |
| `GET` | `/v1/subagents/{id}` | `runs:read` | Get by ID |
| `DELETE` | `/v1/subagents/{id}` | `runs:write` | Delete — HTTP 409 if active |
| `POST` | `/v1/subagents/{id}/wait` | `runs:read` | Block until terminal |
| `POST` | `/v1/subagents/{id}/cancel` | `runs:write` | Cancel |

Returns `501 Not Implemented` if the server was not started with a SubagentManager configured.

---

## Profiles

A profile is a named TOML file that bundles runner parameters, a tool allowlist, and optional
permission and workspace settings into a single preset. You reference it by name in a subagent
or `run_agent` request and the harness applies all of its constraints automatically — no need to
repeat the same `max_steps`, `model`, and `allowed_tools` in every request. (On the top-level
`POST /v1/runs` path the `profile` field currently governs isolation mode, workspace
provisioning, and MCP server selection only — the tool allowlist and step/cost limits from the
profile are not enforced there.)

Profiles are loaded lazily on every `LoadProfile` call, so changes to TOML files on disk take
effect without restarting the server.

### Anatomy of a profile

```toml
extends = "researcher"           # optional: inherit from another profile

[meta]
name        = "deep-researcher"
description = "Like researcher but also fetches page content"
version     = 1
created_by  = "user"             # "built-in" | "agent" | "user" | "api"

[runner]
model           = "gpt-4.1-mini"
max_steps       = 15
max_cost_usd    = 0.40
system_prompt   = "You are a thorough research assistant."
reasoning_effort = "medium"

[tools]
allow = ["read", "grep", "glob", "ls", "web_search", "web_fetch"]

[permissions]
allow_bash       = false
allow_file_write = false
allow_net_access = true
```

**`[runner]` zero-value semantics:** a zero value (`0` or `""`) means "no profile-level limit"
— the server's own defaults apply. `max_cost_usd = 0.0` means unlimited for this profile.

**`[tools]` semantics:** an empty `allow` list (`allow = []`) means *all tools are available*,
not zero tools. A non-empty list is a strict allowlist.

**`[permissions]` semantics:** all permission fields default to `false` (no override). There is
no way to use a profile to revoke a permission that was granted at a higher precedence tier.

### Profile inheritance

A profile can extend another with `extends = "<base-name>"`. The child's non-zero fields
override the base; zero-value fields fall back to the base. For tool allowlists, if the child
defines any list (including an empty one), the child's list wins entirely. Cycle detection is
built in and returns an error if a cycle is found.

```toml
extends = "researcher"

[meta]
name        = "deep-researcher"
description = "Like researcher but also allows web_fetch"
version     = 1

[tools]
allow = ["read", "grep", "glob", "ls", "web_search", "web_fetch"]
```

### Resolution tiers

When go-code resolves a profile name, it checks three tiers in priority order. The first match
wins — tiers are not merged.

<div style={{display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '1rem', marginBottom: '1.5rem'}}>
  <Card>
    <CardHeader>
      <CardTitle>1. Project</CardTitle>
    </CardHeader>
    <CardContent>
      `.harness/profiles/<name>.toml` — relative to the harnessd working directory. Highest priority.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>2. User-global</CardTitle>
    </CardHeader>
    <CardContent>
      `~/.harness/profiles/<name>.toml` — applies across all projects for the current user.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>3. Built-in</CardTitle>
    </CardHeader>
    <CardContent>
      Embedded in the harnessd binary. Read-only — cannot be modified or deleted via the API.
    </CardContent>
  </Card>
</div>

### Built-in profiles

Six profiles are embedded in every `harnessd` binary. All use `model = "gpt-4.1-mini"` and
have `review_eligible = false` (they never participate in the efficiency review system).

| Name | `max_steps` | `max_cost_usd` | Tools allowed |
|---|---|---|---|
| `full` | 30 | $2.00 | all (empty allowlist) |
| `researcher` | 10 | $0.25 | `read`, `grep`, `glob`, `ls`, `web_search`, `web_fetch` |
| `reviewer` | 10 | $0.25 | `read`, `grep`, `glob`, `ls`, `git_diff` |
| `file-writer` | 15 | $0.50 | `read`, `write`, `edit`, `apply_patch`, `bash` |
| `bash-runner` | 10 | $0.25 | `bash` |
| `github` | 20 | $0.50 | `bash`, `read` |

Built-ins cannot be modified or deleted. `POST /v1/profiles/{name}` returns HTTP 409 if the
name matches a built-in. `PUT` and `DELETE` return HTTP 403.

### Selecting a profile in a request

Set the `"profile"` field in a subagent or `run_agent` request body:

```bash
# Subagent with the built-in reviewer profile — tool allowlist and step/cost limits enforced
curl -s -X POST http://localhost:8080/v1/subagents \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Review changes in src/parser.go", "profile": "reviewer"}'

# Subagent with a custom project profile
curl -s -X POST http://localhost:8080/v1/subagents \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Scan for SQL injection vulnerabilities", "profile": "deep-researcher"}'
```

Per-request fields (`model`, `max_steps`, `max_cost_usd`) override the values the profile would
otherwise supply for that run.

Separately, the server resolves its baseline configuration through a layered cascade. From
highest precedence to lowest, the layers are: `HARNESS_*` environment variables → the
`--profile` named profile → project `.harness/config.toml` → user `~/.harness/config.toml` →
compiled-in defaults. The `--profile` flag at `harnessd` startup loads a profile as the
server-wide default for all runs that do not explicitly specify one.

---

## Profile Management API

The profile API lets you create, read, update, and delete user-tier profiles without touching
the filesystem directly. The mutation endpoints (`POST`, `PUT`, `DELETE`) require the server to
have `ProfilesDir` configured — they return `501 Not Implemented` otherwise.

### Routes

| Method | Path | Scope | Notes |
|---|---|---|---|
| `GET` | `/v1/profiles` | `runs:read` | List profiles from all tiers; returns `{"profiles":[…],"count":N}` |
| `GET` | `/v1/profiles/{name}` | `runs:read` | Get full metadata including `source_tier` and `allowed_tools` |
| `POST` | `/v1/profiles/{name}` | `runs:write` | Create — HTTP 201; HTTP 409 if built-in |
| `PUT` | `/v1/profiles/{name}` | `runs:write` | Update — HTTP 403 for built-ins, HTTP 404 if not found |
| `DELETE` | `/v1/profiles/{name}` | `runs:write` | Delete — HTTP 403 for built-ins, HTTP 404 if not found |

### Request body for POST and PUT

```json
{
  "description": "Strict read-only code review",
  "model": "gpt-4.1-mini",
  "max_steps": 10,
  "max_cost_usd": 0.25,
  "system_prompt": "You review code for correctness and style. Never write or modify files.",
  "allowed_tools": ["read", "grep", "glob", "ls", "git_diff"]
}
```

Fields omitted from a `PUT` body retain their existing values. `description` is required on
`POST` (the server validates it is non-empty).

Profiles created via the API have `created_by = "api"` and `review_eligible = true` by default,
meaning they participate in the efficiency review system.

### Example: create and verify a custom profile

```bash
# Create a new user-tier profile
curl -s -X POST http://localhost:8080/v1/profiles/security-scanner \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Read-only security review — no writes or network access",
    "model": "gpt-4.1-mini",
    "max_steps": 12,
    "max_cost_usd": 0.30,
    "allowed_tools": ["read", "grep", "glob", "ls"]
  }'

# Verify it was stored
curl -s http://localhost:8080/v1/profiles/security-scanner

# Update — bump max_steps without changing anything else
curl -s -X PUT http://localhost:8080/v1/profiles/security-scanner \
  -H "Content-Type: application/json" \
  -d '{"description": "Read-only security review — no writes or network access", "max_steps": 20}'

# Delete when no longer needed
curl -s -X DELETE http://localhost:8080/v1/profiles/security-scanner
```

---

## Profile Recommender

The harness includes a deterministic, keyword-based recommender that suggests a profile name
given a task description string. It requires no LLM — it is a fast heuristic you can call from
orchestration logic.

The recommender is exposed as the `recommend_profile` deferred tool (activated via `find_tool`
inside a run), or you can call `profiles.RecommendProfile(task)` directly from Go code.

Rules are evaluated in order; the first match wins:

| Task contains… | Suggested profile | Confidence |
|---|---|---|
| `review`, `audit`, `check`, `analyze` | `reviewer` | high |
| `research`, `search`, `find`, `investigate` | `researcher` | high |
| `bash`, `shell`, `script`, `run command` | `bash-runner` | high |
| `write file`, `create file`, `edit file` | `file-writer` | high |
| `github`, `pull request`, ` pr `, `issue` | `github` | high |
| (no match) | `full` | low |

The match is a case-insensitive substring check. A `Recommendation` result carries
`ProfileName`, `Reason`, and `Confidence` (`"high"` on a keyword match, `"low"` on fallback).

---

## Profile Efficiency System

After each run that uses a profile, the harness computes an efficiency score and optionally
emits suggestions for tightening the profile.

**Score formula:**

```
efficiency = 1.0 / (1.0 + steps * 0.1 + costUSD * 10.0)
```

Scores range from 0.0 to 1.0 — higher is better. When a run's score falls below the threshold
of `0.6` (`EfficiencyThreshold`), a `profile.efficiency_suggestion` event is emitted on the
run's SSE event stream. The payload includes `profile_name`, `run_id`, `efficiency_score`,
`steps`, and `cost_usd`.

<Callout type="warning">
  Efficiency suggestions are **never auto-applied**. They are informational events only — your
  code or an operator must decide whether to act on them. In addition, `GenerateSuggestions`
  requires at least 3 completed runs for the same profile before it produces actionable output;
  with fewer runs it returns `"Not enough history to generate suggestions (need ≥ 3 runs)"`.
</Callout>

---

## Putting it together: the orchestration pattern

The most common multi-agent pattern in go-code:

<Steps>
  <Step>
    A top-level run receives a broad task (e.g. "review and then fix the security issues in
    `internal/auth`").
  </Step>
  <Step>
    The agent calls `find_tool` to activate `recommend_profile` and `run_agent`.
  </Step>
  <Step>
    It calls `recommend_profile` with the task description to get a profile name and confidence.
  </Step>
  <Step>
    It calls `run_agent` with the task and the suggested profile. The child executes with the
    profile's tool allowlist, step limit, and cost ceiling enforced. It cannot exceed those
    bounds even if the parent could.
  </Step>
  <Step>
    The child's output is returned to the parent as the `run_agent` tool result. The parent
    continues with the next step of the broader task.
  </Step>
</Steps>

Because profiles are TOML files resolved lazily, you can ship task-appropriate presets alongside
your project in `.harness/profiles/` and reference them by name from any orchestration code
without changing Go source.

---

## Next steps

- **Concepts overview** — how subagents, profiles, and skills compose as go-code's three
  orchestration primitives: [Skills, Profiles, and Subagents](/docs/concepts/skills-profiles-subagents)
- **HTTP API reference** — full route tables, request/response shapes, and authentication:
  see the server API reference section
- **Events** — the full event catalog and SSE wire format:
  [Events](/docs/concepts/events)
