---
title: "Building and Using Skills"
sidebar_label: "Skills"
sidebar_position: 2
---

import { Callout, Steps, Step, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

Skills are reusable prompt modules that you author once and inject into any agent run on demand. Each skill is a directory containing a `SKILL.md` file — YAML frontmatter followed by Markdown instructions. When a skill fires, its body is inserted into the agent's conversation (or run in an isolated subagent), giving the agent a focused set of instructions and a constrained tool set for a specific task.

Skills are useful when you have a pattern of work — deploying to a cloud provider, reviewing a pull request, auditing dependencies — that you want to encode once and reuse across many runs without pasting the same prompt repeatedly.

---

## SKILL.md format

Every skill lives in its own directory. The directory name **must** match the `name` field in the frontmatter exactly.

### Frontmatter fields

```yaml
---
name: git-pr-create
description: "Create GitHub pull requests with templates, labels, and reviewers. Trigger: when creating a pull request, opening a PR, or submitting code for review"
version: 1
allowed-tools:
  - bash
  - read
  - glob
  - grep
context: conversation
---
# GitHub PR Creation

You are now operating in PR creation mode...
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `name` | yes | — | Kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`); must match directory name |
| `description` | yes | — | Also used for trigger extraction (see [Trigger system](#trigger-system)) |
| `version` | yes | — | Must be exactly `1` |
| `context` | no | `"conversation"` | `"conversation"` or `"fork"` — controls execution mode |
| `allowed-tools` | no | nil (all tools) | Constrain the tool set available during the skill |
| `auto-invoke` | no | `true` | Whether trigger matching can auto-invoke this skill |
| `argument-hint` | no | `""` | Short hint displayed in the skill catalog |
| `agent` | no | `""` | Agent type hint, e.g. `"Explore"` or `"Code"` |
| `verified` | no | `false` | Set by the `verify` action; do not set manually |
| `verified_at` | no | `""` | RFC3339 timestamp; set automatically on verification |
| `verified_by` | no | `""` | Who ran verification |

**Body**: everything after the closing `---` delimiter, trimmed of leading and trailing whitespace.

### Hard-fail parse rules

The loader (`internal/skills/loader.go`) rejects a `SKILL.md` with a hard error — the skill will not load — when any of the following are true:

- `name` or `description` is missing
- `version` is not exactly `1`
- `name` fails the kebab-case regex
- `name` does not match the directory name
- `context` is not `"conversation"` or `"fork"`

Fix these before placing a skill in a watched directory. The harness logs the error and skips the skill entirely.

---

## Discovery and reload

### Where to put your skills

The harness scans two directories at startup:

1. **Global**: `$HARNESS_GLOBAL_DIR/skills/` — defaults to `~/.go-harness/skills/`
2. **Workspace**: `<workspace>/.go-harness/skills/`

**Precedence**: if a local (workspace) skill and a global skill share the same `name`, the local skill wins. The global entry is only kept when no skill with that name has already been loaded from the workspace directory.

<Callout type="info">
The `skills/` directory at the repository root contains approximately 50 reference `SKILL.md` files (git workflows, CI/CD, security, Docker, Kubernetes, cloud deploy, and more). These are **not** auto-loaded at runtime — they are copy-install examples. Copy any skill directory you want into `~/.go-harness/skills/` or your workspace `.go-harness/skills/` to activate it.
</Callout>

### Hot-reload

When both `HARNESS_WATCH_ENABLED=true` (default) and `HARNESS_SKILLS_ENABLED=true` (default), a background watcher polls both skill directories and calls an atomic reload when any file changes. The poll interval is controlled by `HARNESS_WATCH_INTERVAL_SECONDS` (default: `5` seconds). Deleted `SKILL.md` files are unregistered on the next poll.

| Env var | Default | Effect |
|---|---|---|
| `HARNESS_SKILLS_ENABLED` | `true` | Disables the entire skills system when `false` |
| `HARNESS_GLOBAL_DIR` | `~/.go-harness` | Root for global skills and workflow directories |
| `HARNESS_WATCH_ENABLED` | `true` | Enables hot-reload polling |
| `HARNESS_WATCH_INTERVAL_SECONDS` | `5` | Poll interval in seconds |

<Callout type="warning">
When `HARNESS_SKILLS_ENABLED=false`, the `skill`, `create_skill`, and `verify_skill` tools are not registered, and all `/v1/skills` HTTP endpoints return `501 Not Implemented`.
</Callout>

---

## Behavior

### Trigger system

**Extraction**: `ExtractTriggers` reads the `description` field and looks for the substring `"Trigger:"` or `"Triggers:"` (case-insensitive). Everything after that label is split on commas to produce trigger phrases.

```
description: "Does X. Trigger: phrase one, phrase two"
```

produces triggers `["phrase one", "phrase two"]`.

The `Trigger:`/`Triggers:` prefix and the `auto-invoke` frontmatter field are parsed and stored on each `Skill` struct (via `AutoInvokeHook` and `Registry.MatchTriggers` in `internal/skills/`), but **neither is wired into any runtime execution path in the running harness**. No code in `cmd/harnessd`, `internal/harness/runner.go`, or the HTTP server calls `MatchTriggers` or parses a leading `/` from user messages. The logic exists as library code and is covered by unit tests, but it does not fire at runtime.

**How skills are actually activated** — skills fire through one of three explicit paths:

1. **`skill` agent tool**: the agent calls `skill <name> [args]` (resolved by exact name in `internal/harness/tools/core/skill.go`).
2. **`prompt_extensions.skills` in the run request**: skill names listed in `req.Extensions.Skills` are injected into the system prompt at run start (`internal/systemprompt/engine.go`).
3. **`/v1/agents` `skill` field**: passed by name when creating an agent via the HTTP API (`internal/server/http_agents.go`).

### Variable interpolation

Before the skill body reaches the agent, the harness substitutes these variables:

| Variable | Replaced with |
|---|---|
| `$ARGUMENTS` | Full argument string passed to the skill |
| `$1` through `$9` | Individual positional arguments (whitespace-split) |
| `$WORKSPACE` | Run ID when invoked via the `skill` tool; empty string when activated via system-prompt extension, subagent, or `/v1/agents` |
| `$SKILL_DIR` | Absolute path to the directory containing `SKILL.md` |

### Shell pre-processing

After interpolation, patterns of the form `` !`command` `` that appear **outside** fenced code blocks are replaced with the command's stdout. This runs at inject time — before the skill body reaches the agent — so you can embed dynamic content like the current git status or a directory listing.

Limits: 10-second timeout per command, 32 KiB max captured output. Commands inside fenced code blocks are left untouched.

### Conversation vs. fork context

The `context` frontmatter field determines how the skill executes:

<Tabs>
<TabsList>
  <TabsTrigger value="conversation">context: conversation (default)</TabsTrigger>
  <TabsTrigger value="fork">context: fork</TabsTrigger>
</TabsList>
<TabsContent value="conversation">

The skill body is injected as a `<skill name="...">...</skill>` meta-message into the **current conversation**. The agent reads it and continues the same turn. This is the most common mode — the agent gets focused instructions without any isolation.

If `allowed-tools` is set, only those tools (plus the always-available `AskUserQuestion`, `find_tool`, and `skill`) are offered to the agent for the duration of the skill.

</TabsContent>
<TabsContent value="fork">

A new isolated subagent is spawned via `ForkedAgentRunner.RunForkedSkill`. The parent agent receives a JSON result with `status: "completed"` when the subagent finishes. The fork has a 120-second timeout.

Max fork depth is `DefaultMaxForkDepth`; exceeding it returns an error rather than spawning.

</TabsContent>
</Tabs>

### Verification and the warning banner

Any skill that has not been verified gets a `⚠ WARNING: skill is unverified` banner prepended to its injected body. This is intentional — it signals to the agent (and to operators reading transcripts) that the skill's instructions have not been reviewed.

Verify a skill with `skill verify <skill-name>` (agent tool) or `POST /v1/skills/{name}/verify` (HTTP API).

---

## Tools and API

### Agent-facing tools

<Card>
<CardHeader>
<CardTitle>`skill` (core tier)</CardTitle>
</CardHeader>
<CardContent>

The primary skill tool. Registered when `HARNESS_SKILLS_ENABLED=true` **and** at least one skill is registered.

**Parameter**: a single `command` string.

Built-in commands:

- `skill list` — prints all registered skills with `[verified]` or `[unverified]` status
- `skill verify <skill-name> [verified_by]` — writes verification metadata to the `SKILL.md` file
- `skill <name> [args]` — activates the named skill

The tool description is **dynamically generated** to include an `<available_skills>` XML block listing every registered skill's name, description, argument hint, and context mode. This is what the LLM sees when deciding whether to call the tool.

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>`create_skill` (deferred tier)</CardTitle>
</CardHeader>
<CardContent>

Allows the agent to author a new `SKILL.md` at runtime. Registered when `SkillsDir` is configured (set to `$HARNESS_GLOBAL_DIR/skills/` by default).

**Required parameters**: `name`, `description`, `trigger`, `content`. Optional: `tags` (accepted in the schema but not persisted — the generated `SKILL.md` frontmatter contains only `name`, `description`, and `version: 1`).

The `trigger` value is automatically appended to `description` as `"Trigger: <trigger>"`. Returns an error if a skill with that name already exists.

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>`verify_skill` (deferred tier)</CardTitle>
</CardHeader>
<CardContent>

Validates a skill's `SKILL.md` and writes verification metadata. Registered when a `SkillVerifier` is configured.

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>`manage_skill_packs` (deferred tier)</CardTitle>
</CardHeader>
<CardContent>

Manages skill pack subscriptions. Registered when a `PackRegistry` is configured.

**Actions**: `list`, `search` (requires `query`), `activate` (requires `name`).

On activate: validates prerequisites, loads the pack's instructions Markdown, and injects it as a `<skill_pack name="...">...</skill_pack>` meta-message.

</CardContent>
</Card>

### HTTP endpoints

All endpoints require a Bearer token. When the skills system is nil (i.e., `HARNESS_SKILLS_ENABLED=false`), all three endpoints return `501 Not Implemented`.

| Method | Path | Scope | Description |
|---|---|---|---|
| `GET` | `/v1/skills` | `runs:read` | List all registered skills. Returns `{"skills": [...]}` |
| `GET` | `/v1/skills/{name}` | `runs:read` | Get a single skill by name |
| `POST` | `/v1/skills/{name}/verify` | `runs:write` | Mark a skill as verified |

**Verify a skill via HTTP:**

```bash
curl -s -X POST http://localhost:8080/v1/skills/git-pr-create/verify \
  -H "Authorization: Bearer $HARNESS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"verified_by": "ci-system"}'
```

The `verified_by` field is optional; when omitted, it defaults to `"api"`. The response is the updated skill object with `verified: true` and `verified_at` set to the current UTC time in RFC3339 format.

---

## Skill packs

A skill pack groups related tools together under a single activation command. Unlike individual skills, a pack has a YAML manifest plus a separate Markdown instructions file.

**Directory layout**:

```
skills/packs/
└── cloudflare-deploy/
    ├── cloudflare-deploy.yaml   # manifest
    └── cloudflare-deploy.md     # instructions injected on activate
```

### Pack manifest fields

| Field | Required | Notes |
|---|---|---|
| `name` | yes | Unique identifier |
| `description` | yes | Human-readable description |
| `version` | yes | Integer, must be >= 1 |
| `instructions` | yes | Filename of the `.md` instructions file |
| `display_name` | no | Shown in the catalog |
| `category` | no | Grouping label |
| `tools` | no | Tool names the pack uses |
| `requires_cli` | no | CLI tools that must be on `PATH` before activation |
| `requires_env` | no | Environment variables that must be non-empty before activation |
| `tags` | no | Free-form tags |
| `allowed_tools` | no | Restrict the agent's tool set during pack execution |

**Example manifest** (`skills/packs/cloudflare-deploy/cloudflare-deploy.yaml`):

```yaml
name: cloudflare-deploy
display_name: "Cloudflare Deployment"
category: deployment
description: "Deploy Workers, Pages, and KV configurations to Cloudflare"
version: 1
tools:
  - bash
  - web_fetch
requires_cli:
  - wrangler
requires_env:
  - CLOUDFLARE_API_TOKEN
instructions: cloudflare-deploy.md
tags:
  - deploy
  - cloudflare
  - workers
  - edge
  - serverless
allowed_tools:
  - bash
  - read
  - write
  - glob
  - grep
```

### Prerequisite validation

When the agent calls `manage_skill_packs` with `action: activate`, the harness checks all entries in `requires_cli` (PATH lookup) and `requires_env` (non-empty check) **before** reading the instructions file. All prerequisite errors are returned at once rather than stopping at the first failure, so the agent can report everything missing in a single message.

---

## Authoring a skill: step by step

<Steps>

<Step>
**Create the directory**

The directory name must be kebab-case and match the `name` you will put in frontmatter.

```bash
mkdir -p ~/.go-harness/skills/db-migrations
```
</Step>

<Step>
**Write `SKILL.md`**

```markdown
---
name: db-migrations
description: "Run and manage database migrations safely. Trigger: run migrations, migrate the database, apply schema changes"
version: 1
allowed-tools:
  - bash
  - read
context: conversation
---
# Database Migration Guide

You are now operating in migration mode. Before running any migration:

1. Check the current migration state: `!`atlas migrate status --url "$DATABASE_URL"``
2. Review pending migrations listed above.
3. Run pending migrations: `atlas migrate apply --url "$DATABASE_URL"`
4. Verify post-migration: run `go test ./...` targeting the database layer.

Current workspace: $WORKSPACE
```

Note the `` !`...` `` shell pre-processing syntax in step 1 — the harness will execute that command and substitute its output when the skill is injected.
</Step>

<Step>
**Verify hot-reload picked it up**

Within 5 seconds (the default poll interval), the skill should appear:

```bash
curl -s http://localhost:8080/v1/skills \
  -H "Authorization: Bearer $HARNESS_API_KEY" | jq '.skills[].name'
```

Or ask the agent directly:

```
skill list
```
</Step>

<Step>
**Verify the skill**

Once you have confirmed the skill works correctly, mark it as verified to suppress the warning banner:

```bash
curl -s -X POST http://localhost:8080/v1/skills/db-migrations/verify \
  -H "Authorization: Bearer $HARNESS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"verified_by": "your-name"}'
```
</Step>

</Steps>

---

## Next steps

- See [Concepts: Skills, Profiles, and Subagents](/docs/concepts/skills-profiles-subagents) for a higher-level overview of how skills compose with profiles and forked agents.
- See [Tools and Permissions](/docs/concepts/tools-and-permissions) for details on how `allowed-tools` interacts with the per-run tool allowlist and the always-available tool set.
- The `skills/packs/` directory in the repository contains two worked examples — `cloudflare-deploy` and `railway-deploy` — that show how to structure a pack for a real deployment workflow.
