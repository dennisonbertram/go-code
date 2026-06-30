---
title: "Configuration Cascade"
sidebar_label: "Configuration"
sidebar_position: 9
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

`harnessd` is configured through a layered *cascade*: settings flow down from built-in defaults through files on disk and named profiles, and are finally overridden by `HARNESS_*` environment variables. Each layer only overrides what it explicitly sets — absent fields fall through to the layer below. The result is that teams can share sensible base settings in a repo-committed file while operators or CI pipelines override only the few values they need.

## The layers

Six layers are evaluated in order, lowest to highest priority:

| # | Source | Notes |
|---|--------|-------|
| 1 | Built-in defaults | Hardcoded in `config.Defaults()` |
| 2 | `~/.harness/config.toml` | User-global settings applied after defaults |
| 3 | `<workspace>/.harness/config.toml` | Project settings; `<workspace>` is `HARNESS_WORKSPACE` or `.` |
| 4 | Named profile | `~/.harness/profiles/<name>.toml` or `<workspace>/.harness/profiles/<name>.toml` |
| 5 | `HARNESS_*` environment variables | Highest-priority runtime overrides |
| 6 | Cloud/team constraints | *(stub — not yet implemented)* |

<Callout variant="warning">
Layer 6 (cloud/team constraints) appears in the package documentation as a reserved slot but has no implementation. Do not rely on it today.
</Callout>

The highest layer that provides a value wins. For most scalar fields this is a simple "non-nil override". The one exception is `[mcp_servers]`: within the TOML config-file layers (2, 3, and the named profile), entries are **merged additively** — an entry from layer 2 is not erased by layer 3 unless both define a server with the same name, in which case the higher-numbered (higher-priority) TOML layer wins by name. `HARNESS_MCP_SERVERS` is handled separately from the TOML cascade: when both a TOML config file and the environment variable define a server with the same name, the TOML entry wins (see the MCP servers tab below).

### How `harnessd` loads layers

At startup `harnessd` calls `config.Load()` with paths for layers 2 and 3. Layer 4 is not used through `config.Load` directly — the `--profile` flag is handled separately by `profiles.LoadProfileWithDirs`, which searches project-level profiles first, then user-global profiles. This means the profile's fields become the effective baseline before environment variables are applied.

**Missing files are silently skipped.** A missing `~/.harness/config.toml` is fine — zero overrides are contributed. A missing profile that was *explicitly requested* by name (via `--profile`) returns an error.

## File format and schema

Both config files use **TOML**. The full schema is divided into a top-level core section and several subsections:

```toml
# ── Core ──────────────────────────────────────────────────
model = "gpt-4.1-mini"   # LLM model identifier
max_steps = 0            # 0 = unlimited; harnessd runtime default is 8
addr = ":8080"           # HTTP listen address (socket form, not a URL)

# ── Per-run cost ceiling ──────────────────────────────────
[cost]
max_per_run_usd = 0.0    # 0 = unlimited

# ── Observational memory ──────────────────────────────────
[memory]
enabled = true           # false disables observational memory entirely
mode = "auto"            # "auto" | "off" | "local_coordinator"
db_driver = "sqlite"     # "sqlite" | "postgres"
db_dsn = ""              # postgres DSN (postgres driver only)
sqlite_path = ".harness/state.db"
default_enabled = false
observe_min_tokens = 1200
snippet_max_tokens = 900
reflect_threshold_tokens = 4000
llm_mode = ""            # "inherit" | "openai" | "provider"
llm_provider = ""        # e.g. "openrouter", "anthropic"
llm_model = ""
llm_base_url = ""

# ── Context auto-compaction ───────────────────────────────
[auto_compact]
enabled = false
mode = ""                # "strip" | "summarize" | "hybrid"
threshold = 0.80         # fraction of model context window
keep_last = 8
model_context_window = 128000

# ── Forensics / audit ────────────────────────────────────
[forensics]
trace_tool_decisions = false
detect_anti_patterns = false
trace_hook_mutations = false
capture_request_envelope = false
snapshot_memory_snippet = false
error_chain_enabled = false
error_context_depth = 0
capture_reasoning = false
cost_anomaly_detection_enabled = false
cost_anomaly_step_multiplier = 0.0
audit_trail_enabled = false
context_window_snapshot_enabled = false
context_window_warning_threshold = 0.0
causal_graph_enabled = false
rollout_dir = ""

# ── Conclusion-jumping detector ──────────────────────────
[conclusion_watcher]
enabled = false
intervention_mode = "inject_validation_prompt"
evaluator_enabled = false
evaluator_model = "gpt-4o-mini"
evaluator_api_key = ""   # secret — prefer OPENAI_API_KEY env var

# ── Cron scheduler ───────────────────────────────────────
[cron]
jitter_enabled = true
jitter_min_sec = 60
jitter_max_sec = 300
avoid_minute_marks = [0, 30]   # TOML-only; no env var override
log_jittered_times = true      # TOML-only; no env var override

# ── MCP servers ──────────────────────────────────────────
[mcp_servers.my-tool]
transport = "stdio"      # "stdio" | "http"
command = "/path/to/server"
args = ["--flag"]

[mcp_servers.remote]
transport = "http"
url = "http://localhost:3001/mcp"
```

<Callout variant="info">
`avoid_minute_marks` and `log_jittered_times` under `[cron]` have no `HARNESS_*` environment variable counterparts. They can only be set in a config file.
</Callout>

### Where project files live

`<workspace>` is the directory reported by `HARNESS_WORKSPACE` (default: `.`). The project config and profiles live at:

```
<workspace>/
└── .harness/
    ├── config.toml          ← layer 3
    └── profiles/
        └── <name>.toml      ← searched first for --profile
```

The user-global counterparts live at:

```
~/.harness/
├── config.toml              ← layer 2
└── profiles/
    └── <name>.toml          ← searched second for --profile
```

## Overriding with environment variables

Environment variables form layer 5 — the highest effective layer. Every `HARNESS_*` variable maps to a specific TOML key. Invalid values (wrong type, e.g. `HARNESS_MAX_STEPS=notanumber`) are **silently ignored** and the previous layer's value is kept with no warning.

<Callout variant="warning">
Invalid `HARNESS_*` values fail silently. Always verify your environment after a typo — the daemon will not tell you it ignored the value.
</Callout>

<Tabs>
  <TabsList>
    <TabsTrigger value="core">Core</TabsTrigger>
    <TabsTrigger value="memory">Memory</TabsTrigger>
    <TabsTrigger value="cron">Cron</TabsTrigger>
    <TabsTrigger value="mcp">MCP servers</TabsTrigger>
  </TabsList>

  <TabsContent value="core">

| Variable | TOML key | Default | Description |
|----------|----------|---------|-------------|
| `HARNESS_MODEL` | `model` | `gpt-4.1-mini` | LLM model identifier |
| `HARNESS_ADDR` | `addr` | `:8080` | HTTP listen address (socket form) |
| `HARNESS_MAX_STEPS` | `max_steps` | `0` in config; `8` at runtime | Max tool-call steps per run |
| `HARNESS_MAX_COST_PER_RUN_USD` | `cost.max_per_run_usd` | `0.0` (unlimited) | Per-run cost ceiling in USD |
| `HARNESS_WORKSPACE` | — | `.` | Workspace root; controls project config and DB paths |
| `HARNESS_SYSTEM_PROMPT` | — | built-in coding prompt | System prompt text for all runs |
| `HARNESS_DEFAULT_AGENT_INTENT` | — | `general` | Startup intent for prompt routing |
| `HARNESS_TOOL_APPROVAL_MODE` | — | `full_auto` | `full_auto` or `permissions` |
| `HARNESS_ASK_USER_TIMEOUT_SECONDS` | — | `300` | Timeout in seconds for ask-user checkpoints |
| `HARNESS_PROVIDER` | — | — | Set to `fake` for key-free deterministic smoke |
| `HARNESS_FAKE_TURNS` | — | — | Path to JSON turns file when `HARNESS_PROVIDER=fake` |

  </TabsContent>

  <TabsContent value="memory">

| Variable | TOML key | Default |
|----------|----------|---------|
| `HARNESS_MEMORY_MODE` | `memory.mode` | `auto` |
| `HARNESS_MEMORY_DB_DRIVER` | `memory.db_driver` | `sqlite` |
| `HARNESS_MEMORY_DB_DSN` | `memory.db_dsn` | — |
| `HARNESS_MEMORY_SQLITE_PATH` | `memory.sqlite_path` | `.harness/state.db` |
| `HARNESS_MEMORY_DEFAULT_ENABLED` | `memory.default_enabled` | `false` |
| `HARNESS_MEMORY_OBSERVE_MIN_TOKENS` | `memory.observe_min_tokens` | `1200` |
| `HARNESS_MEMORY_SNIPPET_MAX_TOKENS` | `memory.snippet_max_tokens` | `900` |
| `HARNESS_MEMORY_REFLECT_THRESHOLD_TOKENS` | `memory.reflect_threshold_tokens` | `4000` |
| `HARNESS_MEMORY_LLM_MODE` | `memory.llm_mode` | auto-detected |
| `HARNESS_MEMORY_LLM_PROVIDER` | `memory.llm_provider` | — |
| `HARNESS_MEMORY_LLM_MODEL` | `memory.llm_model` | `gpt-5-nano` (openai mode) |
| `HARNESS_MEMORY_LLM_BASE_URL` | `memory.llm_base_url` | falls back to `OPENAI_BASE_URL` |
| `HARNESS_MEMORY_LLM_API_KEY` | *(no TOML key — secret)* | falls back to `OPENAI_API_KEY` |

`HARNESS_MEMORY_LLM_MODE` is auto-detected at startup: `provider` when `HARNESS_MEMORY_LLM_PROVIDER` is set, `openai` when `OPENAI_API_KEY` is set, otherwise `inherit` (uses the main run's provider and model).

  </TabsContent>

  <TabsContent value="cron">

| Variable | TOML key | Default |
|----------|----------|---------|
| `HARNESS_CRON_JITTER_ENABLED` | `cron.jitter_enabled` | `true` |
| `HARNESS_CRON_JITTER_MIN_SEC` | `cron.jitter_min_sec` | `60` |
| `HARNESS_CRON_JITTER_MAX_SEC` | `cron.jitter_max_sec` | `300` |
| `HARNESS_CRON_URL` | — | — (embedded scheduler) |

When `HARNESS_CRON_URL` is empty, an embedded SQLite-backed cron scheduler starts automatically. Set `HARNESS_CRON_URL` to point to an external cron service instead.

  </TabsContent>

  <TabsContent value="mcp">

MCP servers can be registered either in a config file (under `[mcp_servers.*]`) or via the `HARNESS_MCP_SERVERS` environment variable. Both sources are merged additively. When both define a server with the same name, the TOML entry takes precedence over the environment variable entry.

```json
// HARNESS_MCP_SERVERS — JSON array
[
  {"name": "my-tool", "transport": "stdio", "command": "/usr/local/bin/server", "args": ["--flag"]},
  {"name": "remote",  "transport": "http",  "url": "http://localhost:3001/mcp"}
]
```

`transport` is inferred when omitted: a `command`-only entry defaults to `stdio`; a `url`-only entry defaults to `http`.

  </TabsContent>
</Tabs>

For the full list of every environment variable (persistence, S3 backup, webhooks, provider API keys, skills hot-reload, role models, and more) see the [Environment Variables reference](/docs/reference/environment-variables).

## Profiles

A *profile* is a named TOML file that bundles a set of runner parameters — model, step limit, cost ceiling, system prompt — plus a tool allowlist and optional permission policy. Profiles are the primary mechanism for giving different runs different capabilities and constraints without changing server-level config.

<Card>
  <CardHeader>
    <CardTitle>Profile file locations (searched in priority order)</CardTitle>
  </CardHeader>
  <CardContent>

1. `<workspace>/.harness/profiles/<name>.toml` — project-level (highest priority)
2. `~/.harness/profiles/<name>.toml` — user-global
3. Built-ins embedded in the binary (read-only)

  </CardContent>
</Card>

### Selecting a profile

Pass `--profile <name>` to `harnessd` to apply a profile as the server-wide default for all runs. You can also select a profile per-run by including `"profile": "<name>"` in the `POST /v1/runs` request body.

```bash
# Server-wide default
harnessd --profile researcher

# Per-run override (curl)
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "summarize the codebase", "profile": "researcher"}'
```

### Built-in profiles

Six profiles ship with the binary and cannot be modified or deleted via the API:

| Name | Tool allowlist | `max_steps` | `max_cost_usd` |
|------|---------------|-------------|-----------------|
| `full` | all tools | 30 | $2.00 |
| `researcher` | `read`, `grep`, `glob`, `ls`, `web_search`, `web_fetch` | 10 | $0.25 |
| `reviewer` | `read`, `grep`, `glob`, `ls`, `git_diff` | 10 | $0.25 |
| `file-writer` | `read`, `write`, `edit`, `apply_patch`, `bash` | 15 | $0.50 |
| `bash-runner` | `bash` | 10 | $0.25 |
| `github` | `bash`, `read` | 20 | $0.50 |

All built-ins use `model = "gpt-4.1-mini"`.

### Writing a custom profile

```toml
# <workspace>/.harness/profiles/my-profile.toml

[meta]
name = "my-profile"
description = "Custom profile for focused file edits"
version = 1
created_by = "user"

[runner]
model = "gpt-4.1-mini"
max_steps = 20
max_cost_usd = 1.00

[tools]
allow = ["read", "write", "edit", "bash"]

[permissions]
allow_bash = true
allow_file_write = true
allow_net_access = false
```

A profile may also declare `extends = "<base-profile-name>"` to inherit fields from another profile. Zero-value child fields fall back to the base; non-zero child fields override it. Profile name validation rejects names containing `/`, `\`, `..`, or absolute paths.

## Gotchas

<Callout variant="warning">
**`max_steps = 0` does not mean unlimited at the process level.**

`0` means "unlimited" in the config schema. But if no `HARNESS_MAX_STEPS` env var is present *and* the resolved config value is `0`, `harnessd` resets it to `8` as a backward-compatible runtime default. To truly unlock step limits, set `HARNESS_MAX_STEPS=0` explicitly in the environment.
</Callout>

<Callout variant="warning">
**`--profile` and `config.Load` layer 4 are separate code paths.**

`harnessd` calls `profiles.LoadProfileWithDirs` for the `--profile` flag — it does **not** populate the `ProfileName` field in `config.Load`. This means profile values become defaults that sit below environment variables in priority, which is the intended behavior. But be aware that `config.Load`'s own layer 4 slot is only exercised by callers that explicitly set `LoadOptions.ProfileName`, not by `harnessd` itself.
</Callout>

<Callout variant="info">
**`HARNESS_ADDR` is a socket address, not a URL.**

The default is `:8080` — a bare port suitable for `net.Listen`. It is not `http://localhost:8080`. Clients connect to `http://localhost:8080`; the server *listens* on `:8080`.
</Callout>

## Next steps

- [Environment Variables reference](/docs/reference/environment-variables) — the complete table of every `HARNESS_*` variable with types and defaults
- [Skills, Profiles, and Subagents](/docs/concepts/skills-profiles-subagents) — deeper coverage of the profile system, inheritance, and the efficiency scoring mechanism
- [Getting started](/docs/getting-started/key-free-testing) — spin up `harnessd` for the first time with `HARNESS_PROVIDER=fake`
