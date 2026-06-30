---
title: "Observational and Working Memory"
sidebar_label: "Memory"
sidebar_position: 8
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

The harness ships two complementary memory subsystems that let agents remember facts and task state across turns and across separate runs. **Observational memory** watches the conversation transcript automatically and extracts durable facts without any explicit agent action. **Working memory** is a key/value store that the agent controls explicitly — it writes exactly what it wants to remember and reads it back later. Both systems inject their contents into every LLM turn so the model always has relevant context in scope.

## Two memory systems

<div style={{display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem', marginBottom: '1.5rem'}}>
<Card>
<CardHeader>
<CardTitle>Observational memory</CardTitle>
</CardHeader>
<CardContent>
Automatic, LLM-backed. The harness watches the transcript after each step and calls an observer model to extract facts. Observations are compressed by a reflector when they accumulate. The resulting snippet is injected as a `&lt;observational-memory&gt;` system message before every LLM turn.

Source: `internal/observationalmemory/`
</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Working memory</CardTitle>
</CardHeader>
<CardContent>
Explicit key/value. The agent calls the `working_memory` tool (`set`, `get`, `delete`, `list`) to store and retrieve named entries. All current entries are injected as a `&lt;working-memory&gt;` system message before every LLM turn.

Source: `internal/workingmemory/`
</CardContent>
</Card>
</div>

Both subsystems are scoped by three identifiers: `tenant_id`, `conversation_id`, and `agent_id`. When absent from a run request: `tenant_id` and `agent_id` fall back to `"default"`; `conversation_id` falls back to the `run_id`. The three-field key means memories from one conversation or agent never bleed into another.

### Snippet injection order

At the start of each LLM turn the runner calls both stores in order and prepends any non-empty results as `system` messages before the conversation history:

1. `WorkingMemoryStore.Snippet()` — injected as `<working-memory>...</working-memory>`
2. `MemoryManager.Snippet()` — injected as `<observational-memory>...</observational-memory>`

The observation snippet is also captured in `memorySnippetForSnapshot` for forensics events.

---

## Observe and reflect

Observational memory works in two phases: **observation** (extract facts from new transcript text) and **reflection** (compress accumulated observations into a structured summary). Both are driven by token-count thresholds.

### The observe phase

After each completed step, the runner calls `MemoryManager.Observe()`. The manager:

1. Checks that memory is enabled for the current scope.
2. Estimates tokens in unobserved messages using `RuneTokenEstimator` — approximately `(runes + 3) / 4` per message. This is an approximation; actual LLM token counts may differ.
3. Skips if the unobserved token count is below `ObserveMinTokens` (default `1200`).
4. Calls the observer LLM, which returns scored observation blocks in `IMPORTANCE:x.x\n<text>` format.
5. Appends the parsed `ObservationChunk` values to the scope's record.

If accumulated observation tokens then reach or exceed `ReflectThresholdTokens` (default `4000`), reflection runs automatically in the same call.

### The reflect phase

The reflector LLM is given all current observation chunks plus any existing reflection text. It must return a structured response with three sections:

```
SUMMARY:
<prose summary>

SUPERSESSIONS:
- [seq:N] replaces [seq:M]: reason

CONTRADICTIONS:
- [seq:N] vs [seq:M]: detail
```

The harness parses this into a `StructuredReflection` value containing `Summary`, `Supersessions`, and `Contradictions`. When the output cannot be parsed as structured format, it falls back to a legacy plain-text mode (`SchemaVersion == 0`).

You can trigger reflection immediately regardless of token thresholds by calling the `observational_memory` tool with `action: "reflect_now"`.

### Snippet assembly and budget

When building the `<observational-memory>` snippet the harness selects observations within the `SnippetMaxTokens` budget (default `900`) using importance-weighted recency scoring:

```
score = importance / (1 + ageSteps)
```

Observations that have been superseded are clamped to `importance = 0.1` so they still appear but rank below fresh observations. The final snippet includes the reflection summary first, then selected observations, then any supersession and contradiction warnings.

### Memory events

The harness emits SSE events for the observe/reflect lifecycle. You can subscribe to these on the run's event stream at `GET /v1/runs/{id}/events`:

| Event | When |
|---|---|
| `memory.observe.started` | Observe call begins |
| `memory.observe.completed` | Observe call finished (payload includes `observed` bool, `reflected` bool, `observation` count) |
| `memory.observe.failed` | Observe call failed |
| `memory.reflection.completed` | Reflection completed (either auto or `reflect_now`) |

See [Events](/docs/concepts/events) for the full SSE envelope format.

---

## The `working_memory` tool

The agent calls `working_memory` directly when it wants to stash explicit task state. Values are stored as JSON and retrieved by key.

```json
// Store a value
{"action": "set", "key": "plan", "value": {"step": 1, "target": "auth.go"}}

// Retrieve it
{"action": "get", "key": "plan"}
// Returns: {"key": "plan", "value": "{\"step\":1,\"target\":\"auth.go\"}", "found": true}
// Note: value is a JSON-encoded string — callers must JSON-parse it to recover the object.

// List all entries for this scope
{"action": "list"}
// Returns: {"entries": {"plan": "{\"step\":1,\"target\":\"auth.go\"}"}}
// Note: each entry value is likewise a JSON-encoded string.

// Remove an entry
{"action": "delete", "key": "plan"}
```

Working memory entries are sorted alphabetically in the injected `<working-memory>` snippet. By default entries are held in-memory (`MemoryStore`) and lost when the process restarts. To persist them across restarts, use the SQLite backend — both observational and working memory share the same SQLite file controlled by `HARNESS_MEMORY_SQLITE_PATH`.

---

## The `observational_memory` tool

Agents can inspect and control observational memory explicitly even though it runs automatically in the background.

```json
// Enable with custom thresholds
{
  "action": "enable",
  "config": {
    "observe_min_tokens": 1200,
    "snippet_max_tokens": 900,
    "reflect_threshold_tokens": 4000
  }
}

// Check current status
{"action": "status"}

// Trigger reflection immediately
{"action": "reflect_now"}

// Export memory to a file
{"action": "export", "format": "markdown"}
```

Export writes files to `.harness/observational-memory/<tenant>/<conv>/<agent>/memory-<timestamp>.<ext>` relative to the workspace root.

Observational memory is disabled by default for new scopes (`HARNESS_MEMORY_DEFAULT_ENABLED=false`). An agent must call `observational_memory` with `action: "enable"` to activate it, or you can flip the default via configuration.

---

## Configuration

<Tabs defaultValue="toml">
<TabsList>
<TabsTrigger value="toml">TOML</TabsTrigger>
<TabsTrigger value="env">Environment variables</TabsTrigger>
</TabsList>

<TabsContent value="toml">

Non-secret settings can go in `.harness/config.toml` (project) or `~/.harness/config.toml` (user-global) under a `[memory]` block:

```toml
[memory]
mode = "auto"                     # "auto" | "off" | "local_coordinator"
db_driver = "sqlite"              # "sqlite" | "postgres"
sqlite_path = ".harness/state.db" # relative to workspace root
default_enabled = false           # whether new scopes start enabled
observe_min_tokens = 1200
snippet_max_tokens = 900
reflect_threshold_tokens = 4000
llm_mode = "provider"             # "inherit" | "openai" | "provider"
llm_provider = "openrouter"
llm_model = "moonshotai/kimi-k2.5"
```

</TabsContent>

<TabsContent value="env">

| Variable | Default | Description |
|---|---|---|
| `HARNESS_MEMORY_MODE` | `auto` | `auto`, `off`, or `local_coordinator`. `auto` resolves to `local_coordinator` at startup. |
| `HARNESS_MEMORY_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `HARNESS_MEMORY_SQLITE_PATH` | `.harness/state.db` | SQLite file path, relative to workspace root |
| `HARNESS_MEMORY_DEFAULT_ENABLED` | `false` | Whether new scopes start with memory enabled |
| `HARNESS_MEMORY_OBSERVE_MIN_TOKENS` | `1200` | Minimum token threshold before observation runs |
| `HARNESS_MEMORY_SNIPPET_MAX_TOKENS` | `900` | Max tokens in the injected snippet |
| `HARNESS_MEMORY_REFLECT_THRESHOLD_TOKENS` | `4000` | Accumulated observation tokens that trigger reflection |
| `HARNESS_MEMORY_LLM_MODE` | auto-inferred | `inherit`, `openai`, or `provider` |
| `HARNESS_MEMORY_LLM_PROVIDER` | — | Provider key when `llm_mode=provider` (e.g. `openrouter`, `anthropic`) |
| `HARNESS_MEMORY_LLM_MODEL` | `gpt-5-nano` (openai mode) | Model for observer and reflector calls |
| `HARNESS_MEMORY_LLM_BASE_URL` | falls back to `OPENAI_BASE_URL` | Base URL for the memory LLM |
| `HARNESS_MEMORY_LLM_API_KEY` | falls back to `OPENAI_API_KEY` | API key for the memory LLM (not written to TOML) |

</TabsContent>
</Tabs>

### Choosing the memory LLM

The harness needs an LLM to run the observer and reflector. There are three modes, auto-detected in priority order:

1. If `HARNESS_MEMORY_LLM_PROVIDER` is set → `provider` mode (uses the named provider from the provider registry)
2. Else if `OPENAI_API_KEY` is set → `openai` mode (dedicated OpenAI-compatible client, 90 s timeout)
3. Else → `inherit` mode (wraps the same provider and model as the main run)

`inherit` is the safest default for local development — it requires no extra credentials. For production, `provider` lets you route memory calls to a cheaper or faster model independently of the main agent model.

<Callout variant="warning" title="Memory LLM is not free or local">
All three LLM modes call a hosted model. In `openai` mode the default model is `gpt-5-nano` — verify that this model ID is valid for your account before deploying. In `inherit` mode every observe/reflect call consumes tokens on the same API key as the agent run.
</Callout>

<Callout variant="warning" title="Postgres backend is not functional in v1">
Setting `HARNESS_MEMORY_DB_DRIVER=postgres` is accepted but every storage method returns `"postgres store is not implemented in v1"`. SQLite is the only working persistence backend. Postgres support is stubbed for a future release.
</Callout>

### SQLite storage

Both observational memory and working memory share one SQLite file (default `.harness/state.db` relative to the workspace root). The file is opened with `PRAGMA journal_mode=WAL` and `PRAGMA busy_timeout=5000`.

Three tables back observational memory:

- `om_memory_records` — one row per `(tenant_id, conversation_id, agent_id)` scope
- `om_operation_log` — append-only log of every mutation (`queued → processing → applied | failed`)
- `om_markers` — lightweight observation/reflection cycle boundary markers

Working memory uses a single table: `working_memory_entries (memory_id, entry_key, entry_json)`.

On startup, any `om_operation_log` rows stuck in `processing` older than 5 minutes are automatically reset to `queued` to recover from ungraceful shutdowns.

---

## What memory enables across runs

Storing observations in SQLite means agent context outlives individual runs.

**Persistent preferences.** An agent that observed "user prefers tabs over spaces" in run 1 receives that fact in the `<observational-memory>` injection on every subsequent run in the same scope — without the user repeating it. This is exercised by `TestCrossRunObservationalMemoryRecall` in the test suite.

**Contradiction and supersession detection.** When a newer preference overrides an older one the structured reflector flags it explicitly in the `SUPERSESSIONS` section. Unresolved conflicts appear under `CONTRADICTIONS`. Both are surfaced in the injected snippet so the model knows it is working with potentially conflicting information.

**Explicit task state.** An agent can call `working_memory set plan <json>` at the end of a run and read it back in a future run via `working_memory get plan`, or receive it automatically through the `<working-memory>` injection. This is useful for multi-session workflows where a plan spans multiple agent invocations.

**Budget-aware context.** The snippet builder prioritizes high-importance, recent observations within the `SnippetMaxTokens` cap. High-priority constraints (important facts, blockers) survive context pressure even when many observations have accumulated.

---

## Next steps

- [Configuration reference](/docs/concepts/configuration) — full cascade of TOML layers and environment variables
- [Events](/docs/concepts/events) — subscribe to `memory.observe.*` and `memory.reflection.completed` on the SSE stream
- [Tools catalog](/docs/reference/tools-catalog) — `working_memory` and `observational_memory` tool schemas
