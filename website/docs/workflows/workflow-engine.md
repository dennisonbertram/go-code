---
title: "The Workflow Engine"
sidebar_label: "Workflow Engine"
sidebar_position: 1
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Steps, Step } from '@site/src/components/ui';

The **workflow engine** (`internal/workflow`) is a Go-native orchestration runtime for composing multi-agent pipelines inside a running `harnessd` process. Instead of a single agent prompt, you write a *Script* — a regular Go function — that calls sub-agents, fans work out in parallel, pipes items through stages, tracks a token budget, and emits structured progress events as it runs. The engine handles scheduling, lifecycle tracking, SSE event emission, and failure recovery so your script can focus entirely on what the pipeline needs to do.

<Callout variant="info">
The engine's full `Context` — including `Parallel`, `Pipeline`, budget tracking, and direct `Engine` usage — lives in `internal/workflow` and is not importable by external Go code. If you are writing a standalone workflow binary, you import `go-agent-harness/pkg/workflowsdk` instead. See [Go-authored Workflows](/docs/workflows/workflow-sdk) for that path.
</Callout>

---

## Scripts and the engine

### The `Script` type

A workflow script is a plain Go function with this exact signature:

```go
// internal/workflow/types.go
type Script func(ctx *Context) (any, error)
```

- Return any serializable value on success, or an `error` to fail the run.
- Panics inside a script are caught and automatically converted to errors — your script does not need to recover from panics.

### Creating an engine

```go
eng := workflow.NewEngine(workflow.EngineOptions{
    Subagents: mgr, // SubagentManager — required
})
```

`EngineOptions` controls the engine's behaviour:

| Field | Type | Default | Notes |
|---|---|---|---|
| `Subagents` | `SubagentManager` | — | **Required.** Panics when `Agent()` is called if nil. |
| `MaxConcurrency` | `int` | `min(16, NumCPU-2)` (min 1) | Cap on simultaneous `Agent()` calls. |
| `DefaultBudget` | `int` | `0` (unlimited) | Token budget for new runs. `0` means no limit. |
| `Store` | `Store` | in-memory | Persistence backend for runs and events. |
| `QuestionResponder` | `QuestionResponder` | nil | Handles `ctx.Question()` calls. When nil, `Question()` returns an error. |
| `Now` | `func() time.Time` | `time.Now` | Override for deterministic testing. |

### Registering and starting a workflow

```go
// Register a script under a name.
eng.Register("summarize-codebase", func(ctx *workflow.Context) (any, error) {
    r, err := ctx.Agent("Summarize the repo at /src", nil)
    if err != nil {
        return nil, err
    }
    return r.Output, nil
})

// Start an asynchronous run.
run, err := eng.Start(context.Background(), "summarize-codebase", nil)
// run.ID == "wf_<uuid>", run.Status == "running"
```

`Start` returns immediately after creating the run. The script executes in a background goroutine. The returned `Run` is a snapshot; poll `eng.GetRun(run.ID)` or subscribe to events to track progress.

For richer metadata, use `RegisterWithMeta`:

```go
eng.RegisterWithMeta(workflow.Meta{
    Name:        "summarize-codebase",
    Description: "Produces a high-level summary of every package in the repo.",
    WhenToUse:   "After any large refactor.",
    Phases: []workflow.PhaseInfo{
        {Title: "Discovery"},
        {Title: "Summarization"},
    },
}, myScript)
```

### Run IDs

Every run is assigned an ID prefixed with `"wf_"` followed by a UUID (e.g. `wf_01234567-...`). This prefix distinguishes workflow runs from standard agent runs in logs and event streams.

---

## Context primitives

Inside a script, `*Context` is your interface to the engine. The primitives below let you orchestrate sub-agents, emit progress, and compose pipelines.

### `ctx.Agent` — spawn a sub-agent

```go
func (c *Context) Agent(prompt string, opts *AgentOpts) (*AgentResult, error)
```

`Agent` is the core building block. It:

1. Checks the budget — if `Total > 0` and there are no tokens remaining, returns an error immediately.
2. Acquires a concurrency slot (blocks if the engine is at capacity).
3. Emits `workflow.agent.started`, creates a sub-agent, and polls until its status is `"completed"`, `"failed"`, or `"cancelled"`.
4. If `opts.Schema` is set, validates the agent's output against that JSON Schema.
5. Emits `workflow.agent.completed` (or `workflow.agent.failed`) and releases the slot.

Key `AgentOpts` fields:

| Field | Type | Notes |
|---|---|---|
| `Label` | `string` | Human-readable label for progress UI. |
| `Phase` | `string` | Groups this agent under a named phase. |
| `Schema` | `map[string]any` | JSON Schema — agent output is validated and parsed on success. |
| `Model` | `string` | Override the model for this sub-agent. |
| `Provider` | `string` | Override the provider. |
| `Profile` | `string` | Named sub-agent profile. |
| `AllowedTools` | `[]string` | Tool allowlist for the sub-agent. |
| `Isolation` | `string` | `""` (inline) or `"worktree"` for filesystem isolation. |
| `CleanupPolicy` | `string` | Cleanup policy for isolated worktrees. |
| `AgentType` | `string` | Custom sub-agent type (e.g. `"Explore"`). |
| `MaxSteps` | `int` | Step limit for the child run. |
| `MaxCostUSD` | `float64` | Cost ceiling for the child run. |

### `ctx.Parallel` — concurrent fan-out with barrier

```go
func (c *Context) Parallel(thunks []func() (any, error)) ([]any, error)
```

Runs all thunks concurrently and **waits for all of them to finish** before returning (barrier semantics). Results are returned in the same order as the input slice. A thunk that errors or panics sets its result slot to `nil`; `Parallel` itself never returns a non-nil error. (If context-cancellation error surfacing matters, use `Pipeline` — it does return `ctx.Err()` when the context is cancelled.)

```go
results, _ := ctx.Parallel([]func() (any, error){
    func() (any, error) { return ctx.Agent("analyze package A", nil) },
    func() (any, error) { return ctx.Agent("analyze package B", nil) },
    func() (any, error) { return ctx.Agent("analyze package C", nil) },
})
// All three run concurrently. results[i] is nil if that thunk errored.
```

<Callout variant="info">
`Parallel` goroutines do **not** acquire the concurrency semaphore. Only `Agent()` calls inside a thunk do. This is intentional: if `Parallel` goroutines held semaphore slots, a deeply nested script could deadlock waiting for slots that its own children hold. Keep this design in mind when reasoning about maximum concurrency.
</Callout>

### `ctx.Pipeline` — staged processing without a barrier

```go
func (c *Context) Pipeline(items []any, stages ...PipelineStage) ([]any, error)

type PipelineStage func(prev any, item any, index int) (any, error)
```

Passes each item independently through every stage in order. Unlike `Parallel`, there is **no barrier between stages** — item A can reach stage 3 while item B is still in stage 1. A stage error drops that item (its result becomes `nil`) and skips remaining stages for it. Results are returned in the original item order.

```go
results, _ := ctx.Pipeline(
    []any{"pkg/server", "pkg/harness", "pkg/workflow"},
    func(prev any, item any, index int) (any, error) {
        // Stage 1: count files
        return ctx.Agent(fmt.Sprintf("Count Go files in %s", item), nil)
    },
    func(prev any, item any, index int) (any, error) {
        // Stage 2: summarize using stage 1's output
        return ctx.Agent(fmt.Sprintf("Summarize findings: %v", prev), nil)
    },
)
```

Like `Parallel`, `Pipeline` goroutines do not acquire the semaphore — only `Agent()` calls inside stages do.

### `ctx.Phase` — mark a progress phase

```go
func (c *Context) Phase(title string)
```

Sets the current phase label for all subsequent `Agent()` calls and `Log()` messages. Emits a `workflow.phase.started` event. Use phases to give the operator visible progress milestones:

```go
ctx.Phase("Discovery")
ctx.Agent("List all packages", nil)

ctx.Phase("Analysis")
ctx.Agent("Analyze package dependencies", nil)
```

### `ctx.Log` — emit a progress message

```go
func (c *Context) Log(message string)
```

Emits a `workflow.log` event with the message as its payload. Useful for structured checkpoints that appear in the SSE stream and any connected UI.

### `ctx.Feedback` — emit structured findings

```go
func (c *Context) Feedback(kind, message string, data map[string]any)
```

Emits one of three event types depending on `kind`:

| `kind` value | Event emitted |
|---|---|
| `"finding"` | `workflow.finding` |
| `"warning"` | `workflow.warning` |
| anything else (including `""`) | `workflow.feedback` |

Use `"finding"` for actionable observations, `"warning"` for issues that need attention, and `"progress"` (or empty) for general status updates.

### `ctx.Question` — ask for human input

```go
func (c *Context) Question(prompt string, choices []QuestionOption) (any, error)
```

Emits a `workflow.question` event and blocks until the configured `QuestionResponder` replies. If no `QuestionResponder` was set in `EngineOptions`, returns the error `"workflow question responder is not configured"`.

### `ctx.Workflow` — call a nested workflow

```go
func (c *Context) Workflow(name string, args any) (any, error)
```

Runs another registered workflow by name as a synchronous nested call. The nested workflow shares the current run's ID and the parent's (cloned) budget (so child spending bubbles up to the parent), but gets its own concurrency semaphore sized to the same `MaxConcurrency` value. Returns the nested result or an error.

---

## Budgets and schema validation

### Token budgets

The `Budget` type tracks token consumption across nested workflows and is thread-safe via an `atomic.Int64`:

```go
type Budget struct {
    Total int
    // unexported: spent atomic.Int64, parent *Budget
}

func NewBudget(total int) *Budget
func (b *Budget) Spent() int
func (b *Budget) Remaining() int  // returns math.MaxInt when Total == 0
func (b *Budget) Spend(n int)     // also charges parent if one is set
func (b *Budget) Clone() *Budget  // new child budget sharing the same parent
```

Key rules:

- `Total == 0` means **unlimited** — `Remaining()` returns `math.MaxInt` in that case.
- `Clone()` creates a child with the same `Total` and links it back to the parent, so `child.Spend(n)` also calls `parent.Spend(n)`.
- `ctx.Workflow(name, args)` clones the current budget automatically, so nested workflows share the parent's token pool.

```go
parent := workflow.NewBudget(100)
parent.Spend(20)
// parent.Remaining() == 80

child := parent.Clone()
child.Spend(30)
// child.Remaining()  == 70 (child: 100-30)
// parent.Remaining() == 50 (parent: 100-20-30, child spend bubbled up)
```

### Schema validation and structured output

When you pass a `Schema` field to `AgentOpts`, the engine calls `ParseStructuredOutput` on the agent's raw output and validates the result against your JSON Schema. You can also call these utilities directly:

```go
// Validate data against a JSON Schema subset.
func ValidateSchema(schema map[string]any, data any) error

// Parse and validate agent output — handles raw JSON or markdown-fenced JSON.
func ParseStructuredOutput(output string, schema map[string]any) (any, error)
```

`ParseStructuredOutput` understands two formats automatically:

```go
schema := map[string]any{
    "type": "object",
    "properties": map[string]any{
        "score": map[string]any{"type": "integer"},
    },
    "required": []any{"score"},
}

// Raw JSON
parsed, err := workflow.ParseStructuredOutput(`{"score": 42}`, schema)

// Markdown-fenced JSON (common in LLM output)
parsed, err = workflow.ParseStructuredOutput("```json\n{\"score\":42}\n```", schema)
```

Supported JSON Schema keywords: `type`, `required`, `properties`, `items`, `enum`, `additionalProperties`. `type` can be a string or an array of type strings.

---

## Lifecycle and events

### Run lifecycle states

A run transitions through these states:

<Steps>
<Step>

**`running`** — The run is created and the script goroutine has started. This is the state returned immediately by `Start()`.

</Step>
<Step>

**`completed`** — The script returned `(result, nil)`. The result is stored as JSON in `Run.ResultJSON`.

</Step>
<Step>

**`failed`** — The script returned a non-nil error (or panicked). The error message is stored in `Run.Error`.

</Step>
</Steps>

<Callout variant="info">
The constant `RunStatusQueued = "queued"` is defined in the type system but `Start()` creates runs directly in `"running"` state. The `"queued"` status is not currently used by the engine.
</Callout>

**Resuming a failed run:** only runs with status `"failed"` can be resumed. `Resume` clears the error, resets the status to `"running"`, emits `workflow.started` with `"resumed": true`, and re-runs the script.

```go
run, _ := eng.Start(ctx, "my-workflow", nil)
// ... wait for failure ...

resumed, err := eng.Resume(ctx, run.ID, nil)
// Fails with an error if run.Status != "failed"
```

### Event types

The engine emits these `workflow.*` event types:

<Tabs>
<TabsList>
  <TabsTrigger value="lifecycle">Lifecycle</TabsTrigger>
  <TabsTrigger value="agents">Agents</TabsTrigger>
  <TabsTrigger value="structured">Structured feedback</TabsTrigger>
</TabsList>
<TabsContent value="lifecycle">

| Event type | When emitted |
|---|---|
| `workflow.started` | `Start()` or `Resume()` — includes `"resumed": true` on resume |
| `workflow.phase.started` | `ctx.Phase(title)` |
| `workflow.log` | `ctx.Log(message)` |
| `workflow.completed` | Script returned successfully |
| `workflow.failed` | Script returned an error or panicked |

</TabsContent>
<TabsContent value="agents">

| Event type | When emitted |
|---|---|
| `workflow.agent.started` | Before a sub-agent is created |
| `workflow.agent.completed` | Sub-agent finished successfully |
| `workflow.agent.failed` | Sub-agent failed or schema validation failed |

</TabsContent>
<TabsContent value="structured">

| Event type | When emitted |
|---|---|
| `workflow.feedback` | `ctx.Feedback("", ...)` or any unrecognized `kind` |
| `workflow.finding` | `ctx.Feedback("finding", ...)` |
| `workflow.warning` | `ctx.Feedback("warning", ...)` |
| `workflow.question` | `ctx.Question(prompt, choices)` |

</TabsContent>
</Tabs>

<Callout variant="warning">
The `workflow.feedback`, `workflow.finding`, `workflow.warning`, and `workflow.question` event types are defined in source (`internal/workflow/types.go`) but are **not listed** in `CLAUDE.md`'s event summary. The CLAUDE.md list is incomplete. Always use the source-defined constants (`EventWorkflowFeedback`, `EventWorkflowFinding`, `EventWorkflowWarning`, `EventWorkflowQuestion`) when subscribing to events programmatically.
</Callout>

### Subscribing to events

```go
func (e *Engine) Subscribe(runID string) ([]Event, <-chan Event, func(), error)
```

Returns historical events, a buffered live channel (capacity 64), and a cancel function. **Always call the cancel function** when you are done to avoid goroutine leaks. If a subscriber is too slow, events are dropped rather than blocking script execution.

```go
history, ch, cancel, err := eng.Subscribe(run.ID)
defer cancel()

for _, ev := range history {
    fmt.Println("past:", ev.Type)
}
for ev := range ch {
    fmt.Println("live:", ev.Type)
    if ev.Type == workflow.EventWorkflowCompleted || ev.Type == workflow.EventWorkflowFailed {
        break
    }
}
```

### HTTP API

The workflow engine is exposed through the server when `ServerOptions.ScriptWorkflows` is set. Both `*workflow.Engine` and `*workflow.SourceManager` satisfy the `scriptWorkflowManager` interface.

| Method | Path | Scope required |
|---|---|---|
| `GET` | `/v1/script-workflows` | `runs:read` |
| `GET` | `/v1/script-workflows/{name}` | `runs:read` |
| `POST` | `/v1/script-workflows/{name}/runs` | `runs:write` |
| `GET` | `/v1/script-workflow-runs/{id}` | `runs:read` |
| `GET` | `/v1/script-workflow-runs/{id}/events` | `runs:read` (SSE) |
| `POST` | `/v1/script-workflow-runs/{id}/resume` | `runs:write` |

Start a workflow run:

```bash
curl -X POST http://localhost:8080/v1/script-workflows/summarize-codebase/runs \
  -H "Authorization: Bearer $HARNESS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"args": {"path": "/src"}}'
```

Response (`202 Accepted`):

```json
{
  "run_id": "wf_01234567-89ab-cdef-...",
  "status": "running",
  "workflow_name": "summarize-codebase"
}
```

Stream events:

```bash
curl -N http://localhost:8080/v1/script-workflow-runs/wf_01234567-.../events \
  -H "Authorization: Bearer $HARNESS_API_KEY"
```

Each SSE frame uses the event type as its `event:` field and the event's payload as `data:` (the `id:` field carries the sequence number and the `event:` field carries the type — they are not repeated inside `data:`):

```
id: 3
event: workflow.phase.started
data: {"phase":"Analysis"}

```

The stream closes automatically after a `workflow.completed` or `workflow.failed` event.

---

## Concurrency model

The engine caps concurrent `Agent()` calls via a semaphore. The default cap is `min(16, NumCPU-2)` with a minimum of 1 (matching the Claude Code default). Override it with `EngineOptions.MaxConcurrency`.

The concurrency design follows one rule: **only `Agent()` acquires a semaphore slot.** `Parallel` goroutines and `Pipeline` stage goroutines do not. This prevents deadlocks: if a thunk inside `ctx.Parallel(...)` calls `ctx.Agent(...)`, the agent call acquires a slot normally — there is no risk of a goroutine that holds a slot waiting for a slot to become available.

```
                     Semaphore (cap = min(16, NumCPU-2))
                     ┌─────────────────────────────────┐
ctx.Agent(...)  ──▶  │  acquires slot, blocks until    │
                     │  sub-agent completes             │
                     └─────────────────────────────────┘

ctx.Parallel(thunks)  ──▶  spawns goroutines (no slot)
  └─▶ thunk calls ctx.Agent(...)  ──▶  acquires slot normally

ctx.Pipeline(items, stages...)  ──▶  spawns goroutines (no slot)
  └─▶ stage calls ctx.Agent(...)  ──▶  acquires slot normally
```

---

## Persistence

By default the engine stores runs and events in memory (`memoryStore`). To persist across restarts, provide a `Store` implementation in `EngineOptions`:

```go
type Store interface {
    CreateRun(ctx context.Context, run *Run) error
    UpdateRun(ctx context.Context, run *Run) error
    GetRun(ctx context.Context, id string) (*Run, error)
    AppendEvent(ctx context.Context, event *Event) error
    GetEvents(ctx context.Context, runID string, afterSeq int64) ([]Event, error)
}
```

`GetRun` should return `(nil, nil)` for unknown run IDs — not an error. The in-memory store is goroutine-safe.

---

## Next steps

- To write a Go workflow binary that `harnessd` discovers and runs without importing `internal/`: see [Go-authored Workflows](/docs/workflows/workflow-sdk).
- To understand the broader run event model and SSE stream format: see [The Event Model](/docs/concepts/events).
