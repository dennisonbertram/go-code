---
title: "Skills, Profiles, and Subagents"
sidebar_label: "Skills, Profiles, Subagents"
sidebar_position: 7
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

Skills, profiles, and subagents are the three composition primitives in go-code. They let you
package reusable instructions, constrain what an agent can do, and delegate work to focused
child runs — all without duplicating configuration across every task.

Here is how they fit together: a **skill** is a named prompt module that can be injected into
any run. A **profile** is a TOML file that bundles model, step limit, cost ceiling, and tool
allowlist into a named preset. A **subagent** is a child run that your parent agent (or API
caller) spawns and observes. The three compose naturally: a parent agent reads a task, picks a
profile that fits, spawns a subagent constrained by that profile, and optionally targets a skill
at the child.

---

## Skills: reusable prompt modules

A skill is a directory containing a single `SKILL.md` file. The file has YAML frontmatter
followed by a Markdown body. When a skill is activated, go-code either injects that body into
the running conversation or forks a fully isolated subagent to execute it — depending on the
skill's `context` mode.

### SKILL.md format

Every skill directory name must match the `name` field in the frontmatter. Names must be
kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`).

```yaml
# SKILL.md (YAML frontmatter between --- delimiters)
name: "go-deps"
description: "Audit and update Go module dependencies. Trigger: update dependencies, go mod tidy"
version: 1
context: "conversation"        # "conversation" (default) or "fork"
allowed-tools:
  - read
  - bash
  - write
auto-invoke: true
argument-hint: "[module path]"
```

**Required fields:** `name`, `description`, `version` (must be exactly `1`).

**Key optional fields:**

| Field | Default | What it does |
|---|---|---|
| `context` | `"conversation"` | Execution mode — see below |
| `allowed-tools` | all tools | Constrains the tool set during execution |
| `auto-invoke` | `true` | Whether trigger-phrase matching can activate this skill automatically |
| `argument-hint` | `""` | Shown in the skill catalog as a usage hint |
| `agent` | `""` | Agent type hint passed to the runner (e.g. `"Explore"`, `"Code"`) |

### Context modes: `conversation` vs `fork`

The `context` field determines how the skill body reaches the agent.

<Tabs defaultValue="conversation">
  <TabsList>
    <TabsTrigger value="conversation">conversation (default)</TabsTrigger>
    <TabsTrigger value="fork">fork</TabsTrigger>
  </TabsList>
  <TabsContent value="conversation">

**`context: "conversation"`** — the skill body is injected as a meta-message into the current
conversation under a `<skill name="...">` wrapper. The running agent reads it as additional
context and continues using its existing tools and conversation history. This is the lightest
weight option and the default for all bundled skills.

  </TabsContent>
  <TabsContent value="fork">

**`context: "fork"`** — go-code spawns an isolated subagent to execute the skill. The forked
agent receives the skill body as its prompt, runs independently, and returns a JSON result
with `status: "completed"`. The parent agent does not share its conversation history with the
fork. This is appropriate for skills that need to operate in a clean context or that have
side-effects you want to contain.

Fork depth is capped to prevent runaway recursion. Exceeding the maximum depth returns an
error.

  </TabsContent>
</Tabs>

### Trigger system and auto-invoke

Skills declare triggers inside their `description` field using the keyword `Trigger:` or
`Triggers:` (case-insensitive), followed by a comma-separated list of phrases:

```
description: "Deploy to Fly.io. Trigger: deploy to fly, fly deploy, flyctl"
```

When a user message contains any of those phrases (case-insensitive substring match) and
`auto-invoke: true`, the harness auto-invokes the matching skill before the agent turn. If
a user message starts with `/<skill-name>`, the skill is invoked explicitly regardless of the
`auto-invoke` setting.

<Callout variant="info">
  Auto-invoke fires only when exactly one skill matches. If two skills both contain a trigger
  phrase present in the message, neither is auto-invoked — you must invoke one explicitly.
</Callout>

### Discovery and hot-reload

Skills are loaded at startup from two directories (highest precedence first):

1. `<workspace>/.go-harness/skills/` — local, workspace-scoped
2. `$HARNESS_GLOBAL_DIR/skills/` (default: `~/.go-harness/skills/`) — global

A local skill with the same name as a global skill takes precedence. The server hot-reloads
both directories every `HARNESS_WATCH_INTERVAL_SECONDS` (default: `5` seconds) when
`HARNESS_WATCH_ENABLED=true` (the default). Deleted `SKILL.md` files are unregistered on the
next poll cycle.

Set `HARNESS_SKILLS_ENABLED=false` to disable the entire skills system; all skill-related HTTP
endpoints return `501 Not Implemented` when disabled.

### Variable interpolation

The skill body supports a small set of substitutions at activation time:

| Variable | Expands to |
|---|---|
| `$ARGUMENTS` | Full argument string passed to the skill |
| `$1` … `$9` | Positional arguments (whitespace-split) |
| `$WORKSPACE` | Workspace path for the current run |
| `$SKILL_DIR` | Absolute path to the skill's directory |

After interpolation, patterns of the form `` !`command` `` outside fenced code blocks are
replaced with the command's stdout (10-second per-command timeout, 32 KiB max output). This
lets a skill embed live output — for example, the current git branch — into its injected
context.

### Skill packs

A *skill pack* groups related tools into a single YAML manifest plus a Markdown instructions
file. Packs are stored in `skills/packs/` and can declare `requires_cli` (CLI tools that must
be on PATH) and `requires_env` (environment variables that must be set). Activation injects
the instructions as a `<skill_pack name="...">` meta-message. The bundled reference packs
live in `skills/packs/`.

---

## Profiles: named constraint bundles

A profile is a TOML file with four main sections — `[meta]`, `[runner]`, `[tools]`, and
optionally `[permissions]`. It gives a name to a set of constraints so that the same
configuration can be referenced by name from a run request, a subagent request, or the
`--profile` startup flag, without repeating the values everywhere.

### Profile structure

```toml
[meta]
name = "my-reviewer"
description = "Strict read-only code review, no writes"
version = 1
created_by = "user"

[runner]
model = "gpt-4.1-mini"
max_steps = 10
max_cost_usd = 0.25
system_prompt = "You review code for correctness and style. Never write or modify files."

[tools]
allow = ["read", "grep", "glob", "ls", "git_diff"]

[permissions]
allow_bash = false
allow_file_write = false
allow_net_access = false
```

**`[runner]` zero-value semantics:** a zero value (`0` or `""`) means "no profile-level
limit" — the server's own defaults apply. For example, `max_cost_usd = 0.0` means unlimited
for this profile.

**`[tools]` semantics:** an empty `allow` list (`allow = []`) means *all tools are available*,
not zero tools. This is the `full` built-in's behavior. A non-empty list is a strict allowlist.

### Profile inheritance

A profile can extend another with `extends = "<base-name>"`:

```toml
extends = "researcher"

[meta]
name = "deep-researcher"
description = "Like researcher but also allows web_fetch"
version = 1

[tools]
allow = ["read", "grep", "glob", "ls", "web_search", "web_fetch"]
```

Non-zero fields in the child override the base. For tool allowlists, if the child defines
any list (including empty), the child's list wins entirely. Cycle detection is built in and
returns an error.

### Resolution tiers

When go-code resolves a profile name, it checks three tiers in order — the first match wins:

<div style={{display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '1rem', marginBottom: '1.5rem'}}>
  <Card>
    <CardHeader>
      <CardTitle>1. Project</CardTitle>
    </CardHeader>
    <CardContent>
      `.harness/profiles/&lt;name&gt;.toml` relative to the harnessd working directory. Highest priority.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>2. User-global</CardTitle>
    </CardHeader>
    <CardContent>
      `~/.harness/profiles/&lt;name&gt;.toml`. Applies to all projects for the current user.
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>3. Built-in</CardTitle>
    </CardHeader>
    <CardContent>
      Embedded in the harnessd binary. Read-only and cannot be modified or deleted via the API.
    </CardContent>
  </Card>
</div>

When the same name appears in more than one tier, the highest-priority tier wins entirely —
tiers are not merged. The `extends` mechanism is a separate, explicit way to compose two
profiles.

### Built-in profiles

Six profiles are embedded in every harnessd binary. All use `model = "gpt-4.1-mini"` and
have `review_eligible = false` (they never participate in the efficiency review system).

| Name | Tools allowed | `max_steps` | `max_cost_usd` | Purpose |
|---|---|---|---|---|
| `full` | all (empty allowlist) | 30 | $2.00 | Default — no restrictions |
| `researcher` | `read`, `grep`, `glob`, `ls`, `web_search`, `web_fetch` | 10 | $0.25 | Read-only analysis |
| `reviewer` | `read`, `grep`, `glob`, `ls`, `git_diff` | 10 | $0.25 | Code review, no writes |
| `file-writer` | `read`, `write`, `edit`, `apply_patch`, `bash` | 15 | $0.50 | Targeted file edits |
| `bash-runner` | `bash` | 10 | $0.25 | Script execution |
| `github` | `bash`, `read` | 20 | $0.50 | GitHub automation via gh CLI |

Built-ins cannot be modified or deleted. Creating a user or project profile with the same name
as a built-in returns HTTP 409.

<Callout variant="warning">
  In the profile `isolation_mode` field, only `"none"` (no isolation) and `"worktree"` are
  honored today; `"container"` and `"vm"` are accepted by the schema but not provisioned.
  (The corresponding subagent-level `isolation` field uses `"inline"` for no isolation and
  `"worktree"`.)
</Callout>

<Callout variant="warning">
  The `result_mode` field (`"summary"`, `"full"`, `"structured"`) is defined in the profile
  struct but no code path that reads and acts on it was confirmed at the time these docs were
  written. Treat it as reserved for future use.
</Callout>

### Selecting a profile

A profile is selected by name in the `"profile"` field of a run or subagent request:

```bash
# Start a run with the built-in reviewer profile
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Review the changes in src/parser.go", "profile": "reviewer"}'
```

The `--profile` flag at harnessd startup loads a profile as the server-wide default for all
runs that do not specify one explicitly. Per-run fields in the request body (`model`,
`max_steps`, `max_cost_usd`) always override the profile values.

### Profile recommendation

The `recommend_profile` deferred tool (activated via `find_tool`) uses a deterministic,
keyword-based heuristic to suggest a profile for a given task string:

| Task keywords | Suggested profile |
|---|---|
| `review`, `audit`, `check`, `analyze` | `reviewer` |
| `research`, `search`, `find`, `investigate` | `researcher` |
| `bash`, `shell`, `script`, `run command` | `bash-runner` |
| `write file`, `create file`, `edit file` | `file-writer` |
| `github`, `pull request`, ` pr `, `issue` | `github` |
| (no match) | `full` (low confidence) |

No LLM is involved — the match is a case-insensitive substring check, and the first matching
rule wins.

---

## Subagents: child runs

A subagent is a child agent run with its own run ID, tool set, cost budget, and — when
workspace isolation is requested — its own git worktree. The parent creates the subagent via
the HTTP API or through the `start_subagent` / `run_agent` tools, and then observes it via
polling or a blocking wait.

Subagent IDs use the prefix `subagent_` followed by a UUID (e.g. `subagent_3e2f…`).

### Isolation modes

<Tabs defaultValue="inline">
  <TabsList>
    <TabsTrigger value="inline">inline</TabsTrigger>
    <TabsTrigger value="worktree">worktree</TabsTrigger>
  </TabsList>
  <TabsContent value="inline">

**`isolation: "inline"`** (default) — the subagent runs in the same workspace as its parent
using the same runner infrastructure. No separate filesystem is provisioned. Use this when
the child needs to work in the same directory tree as the parent.

  </TabsContent>
  <TabsContent value="worktree">

**`isolation: "worktree"`** — a fresh git worktree is provisioned for the subagent under
`WorktreeRoot` (default: `<repoParentDir>/<repoName>-subagents`). A separate runner is
created for that workspace. A `harness.toml` config file is written into the worktree at
startup. The response includes `workspace_path` and `branch_name` fields. Use this when you
want the child to make changes independently without touching the parent's working tree.

  </TabsContent>
</Tabs>

### Cleanup policies

When a subagent's run reaches a terminal state (`completed`, `failed`, or `cancelled`), a
background monitor applies the cleanup policy for worktree-isolated subagents:

| Policy | JSON value | When workspace is removed |
|---|---|---|
| `CleanupPreserve` | `"preserve"` | Never — requires explicit `DELETE /v1/subagents/{id}` |
| `CleanupDestroyOnSuccess` | `"destroy_on_success"` | Only on `completed` status |
| `CleanupDestroyOnCompletion` | `"destroy_on_completion"` | On any terminal status |

The default cleanup policy when calling `POST /v1/subagents` is `"preserve"`. When spawning
subagents through the in-run tool layer (`start_subagent`), the default is
`"destroy_on_completion"`.

### Spawning a subagent with a profile

The orchestration pattern is: read the task, pick a profile, spawn a scoped child.

```bash
# Create a subagent: read-only review of a specific file, worktree-isolated
curl -s -X POST http://localhost:8080/v1/subagents \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Review internal/parser/parser.go for correctness and missing error handling",
    "profile": "reviewer",
    "isolation": "worktree",
    "cleanup_policy": "destroy_on_completion"
  }'

# Response: HTTP 201
# {"id":"subagent_3e2f...","run_id":"run_abc...","status":"queued",...}
```

Poll until done:

```bash
# Block until terminal state (polls internally at 200 ms)
curl -s -X POST http://localhost:8080/v1/subagents/subagent_3e2f.../wait
```

The profile constrains everything the subagent can do: model, step budget, cost ceiling, and
tools. A `reviewer`-profiled subagent cannot write files or call bash even if the parent
could.

### In-run orchestration tools

When running inside an agent turn, several deferred tools are available. The two highest-level
spawn-and-wait tools are:

- **`run_agent`** — spawns a subagent with an optional named profile and blocks until it
  finishes. Parameters: `task`, `profile` (optional), `model` (optional), `max_steps`
  (optional).
- **`spawn_agent`** — spawns a recursive child agent. Maximum fork depth is 5
  (`DefaultMaxForkDepth`). Parameters include `task`, `model`, `max_steps`, `max_turns`,
  `allowed_tools`, and `profile`.

Lower-level lifecycle tools — **`start_subagent`** (fire-and-return an ID), **`get_subagent`**,
**`wait_subagent`**, and **`cancel_subagent`** — are also available for fine-grained control.
`start_subagent` is the tool used for in-run subagent spawning when the default cleanup policy
(`"destroy_on_completion"`) applies (see Cleanup policies above).

All of these tools are deferred (hidden by default); use `find_tool` to activate them.

### Using a skill as the subagent's prompt

A subagent request can specify a `skill` name instead of a literal `prompt` (the two fields
are mutually exclusive):

```json
{
  "skill": "go-deps",
  "skill_args": "./internal/...",
  "profile": "file-writer",
  "isolation": "inline"
}
```

The harness resolves the named skill to its body (with variable substitution applied), then
starts the run with that body as the prompt.

---

## How the three primitives compose

The pattern that appears most often in multi-agent workflows:

1. A top-level run receives a broad task.
2. It calls `find_tool` to activate `recommend_profile` and `run_agent`.
3. It calls `recommend_profile` with the task description to get a profile suggestion.
4. It calls `run_agent` with the task and the suggested profile name.
5. The child run executes with the profile's tool allowlist and budget enforced.
6. The child's output is returned to the parent as the `run_agent` tool result.

Because profiles are just TOML files on disk (resolved lazily on each `LoadProfile` call),
you can ship task-appropriate presets alongside your project and reference them by name without
touching any Go code.

<Callout variant="info">
  The `profile.efficiency_suggestion` SSE event is emitted on a run's event stream whenever
  the post-run efficiency score falls below `0.6`. The score is computed from steps taken and
  cost incurred. Suggestions identify unused tools in the profile's allowlist. They are never
  auto-applied — they are informational only.
</Callout>

---

## Next steps

- **Authoring skills** — step-by-step guide to writing, testing, and verifying a `SKILL.md`:
  see the integrations section on skills.
- **Managing profiles** — creating user and project profiles, the `POST /v1/profiles` API,
  and the efficiency system: see the integrations section on subagents and profiles.
- **HTTP API reference** — full route table for `/v1/skills`, `/v1/profiles`, and
  `/v1/subagents`: see the server API reference.
