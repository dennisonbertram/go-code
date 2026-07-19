---
title: "Event Catalog"
sidebar_label: "Event Catalog"
sidebar_position: 3
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

Every agent run produces a real-time stream of **events** — typed, JSON-structured messages delivered over Server-Sent Events (SSE) from `GET /v1/runs/{id}/events`. Each event tells you exactly what the harness is doing: when the LLM sends a token, when a tool starts, how much a step cost, why the run failed.

This catalog is the exhaustive reference for all 77 event types. Use it to build dashboards, billing meters, audit pipelines, debuggers, or any other SSE consumer that needs to know what payload to expect for a given event name.

---

## The SSE envelope

Every line delivered over `GET /v1/runs/{id}/events` follows the SSE protocol:

```
id: <runID>:<seq>
retry: 3000
event: <event-type>
data: <JSON Event object>

```

The `data:` value is a fully marshaled `Event` struct (source: `internal/harness/types.go`):

```go
type Event struct {
    ID        string         `json:"id"`        // "<runID>:<seq>"
    RunID     string         `json:"run_id"`
    Type      EventType      `json:"type"`      // e.g. "run.started"
    Timestamp time.Time      `json:"timestamp"` // RFC3339 UTC
    Payload   map[string]any `json:"payload,omitempty"`
}
```

The `id` field encodes both the run and its sequence number as `{runID}:{seq}`, where `seq` is a 0-based counter that increments with every event. You can pass `Last-Event-ID: <runID>:<seq>` when reconnecting — the server replays all events after that sequence number.

The server sends an SSE comment ``: ping`` on a keepalive interval (default 15 seconds, configurable via `HARNESS_SSE_KEEPALIVE_SECONDS`). Keepalive comments carry no payload and are not typed events.

### Auto-injected payload fields

Three fields are automatically injected into every event payload by the journal layer (`internal/harness/runner_event_journal.go`):

| Field | Value | Notes |
|---|---|---|
| `schema_version` | `"1"` | Always the string `"1"` (`EventSchemaVersion`) |
| `conversation_id` | string | The run's conversation ID |
| `step` | integer | Current step number (if not already set by the emitter) |

---

## Terminal events

<Callout type="warning">
Three events signal the end of the stream. Clients MUST stop reading after receiving any of them — no further events will be emitted for that run.
</Callout>

| Event | String | Meaning |
|---|---|---|
| `EventRunCompleted` | `run.completed` | Run finished successfully |
| `EventRunFailed` | `run.failed` | Run ended with an error |
| `EventRunCancelled` | `run.cancelled` | Run cancelled via `POST /v1/runs/{id}/cancel` |

`IsTerminalEvent(et EventType) bool` returns `true` for exactly these three types (source: `internal/harness/events.go:466–468`).

In headless mode (`harnesscli -prompt ...` or streaming `harnesscli continue`) the terminal event also determines the process exit code — see [Exit Codes](/docs/reference/exit-codes) for the mapping.

---

## Run lifecycle

Source: `internal/harness/events.go:18–41`

<Callout type="info">
`run.cost_limit_reached` is not itself a terminal event type, but it is immediately followed by `run.step.completed` and then the terminal `run.completed` — the run does not continue once the cost ceiling is hit, and it always ends with `run.completed` (never `run.failed`) on this path.
</Callout>

| Event | When emitted |
|---|---|
| `run.started` | Run transitions to running |
| `run.queued` | Run accepted but the worker pool is at capacity (bounded pool mode only) |
| `run.step.started` | Each step loop iteration begins |
| `run.step.completed` | Step loop iteration finishes |
| `run.waiting_for_user` | `ask_user_question` tool invoked; run paused |
| `run.resumed` | User answered; run continuing |
| `run.cost_limit_reached` | Cumulative cost hit `max_cost_usd`; immediately followed by `run.step.completed` then `run.completed` (always ends with `run.completed`, never `run.failed`) |
| `run.completed` | Run finished successfully **(terminal)** |
| `run.failed` | Run failed **(terminal)** |
| `run.cancelled` | Run cancelled **(terminal)** |

### Payloads

<Tabs>
<TabsList>
<TabsTrigger value="started">run.started</TabsTrigger>
<TabsTrigger value="completed">run.completed</TabsTrigger>
<TabsTrigger value="failed">run.failed</TabsTrigger>
<TabsTrigger value="step">run.step.*</TabsTrigger>
<TabsTrigger value="user">run.waiting_for_user</TabsTrigger>
<TabsTrigger value="resumed">run.resumed</TabsTrigger>
<TabsTrigger value="cost">run.cost_limit_reached</TabsTrigger>
<TabsTrigger value="cancelled">run.cancelled</TabsTrigger>
<TabsTrigger value="queued">run.queued</TabsTrigger>
</TabsList>

<TabsContent value="started">

```json
{
  "prompt": "string",
  "previous_run_id": "string (if continuation)",
  "tenant_id": "string (if non-empty)"
}
```

</TabsContent>

<TabsContent value="completed">

```json
{
  "output": "string",
  "usage_totals": {},
  "cost_totals": {}
}
```

Typed as `RunCompletedPayload` in `internal/harness/events.go:471–483`.

</TabsContent>

<TabsContent value="failed">

```json
{
  "error": "string",
  "usage_totals": {},
  "cost_totals": {}
}
```

When the run hit `max_steps`, the payload also includes `"reason": "max_steps_reached"` and `"max_steps": N`. When `max_turns` was exhausted, it includes `"reason": "max_turns_exhausted"` and `"max_turns": N`. Typed as `RunFailedPayload` in `internal/harness/events.go:503–527`.

</TabsContent>

<TabsContent value="step">

`run.step.started`:
```json
{
  "step": 1,
  "step_start_ms": 1700000000000
}
```

`run.step.completed`:
```json
{
  "step": 1,
  "tool_calls": 2,
  "duration_ms": 320
}
```

</TabsContent>

<TabsContent value="user">

```json
{
  "call_id": "string",
  "tool": "ask_user_question",
  "questions": ["string", "..."],
  "deadline_at": "2026-01-01T00:00:00Z"
}
```

</TabsContent>

<TabsContent value="resumed">

```json
{
  "call_id": "string",
  "tool": "string",
  "answered_at": "2026-01-01T00:00:00Z"
}
```

</TabsContent>

<TabsContent value="cost">

```json
{
  "step": 3,
  "max_cost_usd": 0.10,
  "cumulative_cost_usd": 0.11
}
```

</TabsContent>

<TabsContent value="cancelled">

```json
{
  "usage_totals": {},
  "cost_totals": {}
}
```

</TabsContent>

<TabsContent value="queued">

```json
{
  "prompt": "string"
}
```

Only emitted when the server is running in bounded worker pool mode.

</TabsContent>
</Tabs>

---

## LLM turn

Source: `internal/harness/events.go:44–53`

These events bracket each call to the LLM provider and expose streaming tokens as they arrive.

| Event | When emitted |
|---|---|
| `llm.turn.requested` | Before each LLM call |
| `llm.turn.completed` | After LLM response received |
| `assistant.message.delta` | Streaming text chunk from the assistant |
| `assistant.thinking.delta` | Streaming reasoning/thinking chunk |
| `reasoning.complete` | Full reasoning text (emitted when `CaptureReasoning` is enabled and the provider returned reasoning content) |
| `assistant.message` | Full assistant message on a turn with no tool calls |

### Payloads

`llm.turn.requested`: `{"step": N}`

`llm.turn.completed`:
```json
{
  "step": 1,
  "tool_calls": 2,
  "total_duration_ms": 1200,
  "ttft_ms": 340,
  "provider": "openai"
}
```

`assistant.message.delta`:
```json
{
  "step": 1,
  "content": "Hello"
}
```

`assistant.thinking.delta`: same shape as `assistant.message.delta`.

`reasoning.complete`:
```json
{
  "text": "string",
  "tokens": 128,
  "step": 1
}
```

`assistant.message`: `{"content": "string"}`

---

## Tool execution

Source: `internal/harness/events.go:55–81`

<Callout type="info">
`tool.call.completed` is emitted whether the tool succeeded or failed. Check for the presence of `"error"` in the payload to distinguish the two cases.
</Callout>

| Event | When emitted |
|---|---|
| `tool.call.started` | Before a tool handler runs |
| `tool.call.completed` | After the handler returns (success or error) |
| `tool.call.delta` | Streaming argument chunk while the LLM is constructing the call |
| `tool.output.delta` | Incremental output chunk from a running tool |
| `tool.approval_required` | Tool requires operator approval; run status moves to `waiting_for_approval` |
| `tool.approval_granted` | Operator approved the pending tool call |
| `tool.approval_denied` | Operator denied; run continues with a `permission_denied` result |
| `tool.call.blocked` | A skill constraint blocked the tool call before execution |

<Callout type="warning">
`tool.activated` is defined in the event catalog (constant `EventToolActivated`, string `"tool.activated"`) but no production emission site was found in the codebase. It appears to be reserved for future use when a deferred tool is activated via `find_tool`. Do not rely on it being emitted today.
</Callout>

### Payloads

`tool.call.started`:
```json
{
  "call_id": "call_abc",
  "tool": "bash",
  "arguments": "{\"command\":\"ls\"}"
}
```

`tool.call.completed` (success):
```json
{
  "call_id": "call_abc",
  "tool": "bash",
  "output": "file1.txt\nfile2.txt",
  "duration_ms": 45
}
```

`tool.call.completed` (error):
```json
{
  "call_id": "call_abc",
  "tool": "bash",
  "error": "exit status 1",
  "output": "",
  "duration_ms": 12
}
```

`tool.output.delta` (typed `ToolOutputDeltaPayload`):
```json
{
  "call_id": "call_abc",
  "tool": "bash",
  "stream_index": 0,
  "content": "chunk text"
}
```

`tool.approval_required`:
```json
{
  "call_id": "call_abc",
  "tool": "file_write",
  "arguments": "...",
  "deadline_at": "2026-01-01T00:00:00Z"
}
```

`tool.approval_granted` / `tool.approval_denied`: `{"call_id": "call_abc", "tool": "file_write"}`

`tool.approval_denied` (timeout case): also includes `"reason": "string"`.

---

## Accounting (token usage and cost)

Source: `internal/harness/events.go:113–125`

### usage.delta

Emitted after every LLM turn. This is the primary event for billing meters and cost dashboards.

```json
{
  "step": 2,
  "usage_status": "provider_reported",
  "cost_status": "available",
  "turn_usage": {},
  "turn_cost_usd": 0.001,
  "cumulative_usage": {},
  "cumulative_cost_usd": 0.003,
  "pricing_version": "string"
}
```

`usage_status` is one of `"provider_reported"` or `"provider_unreported"`. Typed as `UsageDeltaPayload` in `internal/harness/events.go:529–558`.

### cost.anomaly

Emitted when `RunnerConfig.CostAnomalyDetectionEnabled` is true and a step's cost exceeds `CostAnomalyStepMultiplier` × the rolling average of prior steps (default multiplier: 2.0).

```json
{
  "step": 3,
  "anomaly_type": "step_multiplier",
  "step_cost_usd": 0.05,
  "avg_cost_usd": 0.005,
  "threshold_multiplier": 2.0
}
```

---

## Workspace lifecycle

Source: `internal/harness/events.go:319–340`

These events fire only when the run specifies a `workspace_type` in the `RunRequest`.

| Event | When emitted |
|---|---|
| `workspace.provisioned` | Per-run workspace is ready |
| `workspace.destroyed` | Workspace torn down after run ends |
| `workspace.provision_failed` | Provisioning failed; `run.failed` follows |

`workspace.provisioned`: `{"workspace_type": "string", "workspace_path": "string"}`

`workspace.destroyed`: same fields; adds `"error"` when the destroy itself failed.

`workspace.provision_failed`: `{"workspace_type": "string", "error": "string"}`

---

## Memory events

Source: `internal/harness/events.go:105–110`

Emitted when the harness's observational memory subsystem runs its observe/reflect cycle after a completed step.

| Event | When emitted |
|---|---|
| `memory.observe.started` | Observation cycle begins |
| `memory.observe.completed` | Observation cycle finished |
| `memory.observe.failed` | Observation cycle failed (non-fatal to the run) |
| `memory.reflection.completed` | Reflection compression finished |

`memory.observe.completed` payload includes `step`, `observed` (bool), `reflected` (bool), and `observation` (count).

---

## Steering

Source: `internal/harness/events.go:161–165`

### steering.received

Emitted when a user steering message is injected into an active run via `POST /v1/runs/{id}/steer`.

```json
{
  "message": "string"
}
```

---

## Conversation

Source: `internal/harness/events.go:89–91`

### conversation.continued

Emitted when prior conversation history is loaded for the run (i.e., a run started with a `conversation_id` pointing to an existing conversation). Payload includes `conversation_id` and `prior_message_count`.

---

## Prompt and provider resolution

Source: `internal/harness/events.go:94–102`

| Event | When emitted |
|---|---|
| `prompt.resolved` | System prompt resolved via the prompt engine |
| `prompt.warning` | Warning encountered during prompt resolution |
| `provider.resolved` | Provider and model selected for this run |

---

## Context management

Source: `internal/harness/events.go:168–193`

| Event | When emitted |
|---|---|
| `auto_compact.started` | Automatic context compaction triggered |
| `auto_compact.completed` | Auto-compaction finished |
| `compact_history.completed` | `compact_history` tool call completed |
| `context.reset` | Agent called `reset_context` |

`context.reset` (typed `ContextResetPayload`):
```json
{
  "reset_index": 0,
  "at_step": 3,
  "persist": null
}
```

---

## Hooks, callbacks, and skill constraints

### Message-level hooks

Source: `internal/harness/events.go:128–132`

Hooks run before and after LLM turns. `hook.started`, `hook.failed`, `hook.completed`.

### Tool-level hooks

Source: `internal/harness/events.go:135–139`

<Callout type="info">
Note the underscore in `tool_hook.*` — these are tool-level hooks, not message-level. The prefix uses an underscore, not a dot.
</Callout>

`tool_hook.started`, `tool_hook.failed`, `tool_hook.completed`.

### Callbacks (deferred)

Source: `internal/harness/events.go:142–146`

| Event | When emitted |
|---|---|
| `callback.scheduled` | `set_delayed_callback` tool ran |
| `callback.fired` | Timer fires |
| `callback.canceled` | Callback was cancelled |

<Callout type="warning">
`callback.fired` and `callback.canceled` may be emitted after the originating run has ended. They are emitted on the most-recent live run for the conversation, or are a no-op if none exists.
</Callout>

`callback.*` payload:
```json
{
  "callback_id": "string",
  "conversation_id": "string",
  "state": "string",
  "delay": "duration",
  "prompt": "string",
  "fires_at": "RFC3339",
  "created_at": "RFC3339"
}
```

### Skill constraints

Source: `internal/harness/events.go:149–153`

| Event | When emitted |
|---|---|
| `skill.constraint.activated` | A skill constraint (tool filter) is now active |
| `skill.constraint.deactivated` | Skill constraint removed |
| `tool.call.blocked` | A tool call was blocked by an active skill constraint |

---

## Agent spawning and budget events

Source: `internal/harness/events.go:355–380`

| Event | Emission status | Notes |
|---|---|---|
| `step_budget.pressure` | **Actively emitted** | Step budget running low; payload `{"step": N, "steps_remaining": N, "depth": N}` |
| `max_turns.exhausted` | **Actively emitted** | Agent exhausted `MaxTurns`; `run.failed` follows with `reason: max_turns_exhausted` |
| `spawn_agent.started` | **Defined, not confirmed** | See warning below |
| `spawn_agent.completed` | **Defined, not confirmed** | See warning below |
| `task.completed` | **Defined, not confirmed** | See warning below |

`max_turns.exhausted` payload:
```json
{
  "run_id": "string",
  "step": 5,
  "turn_count": 10,
  "max_turns": 10
}
```

<Callout type="warning">
`spawn_agent.started`, `spawn_agent.completed`, and `task.completed` are defined in the event catalog and returned by `AllEventTypes()`, but no production emission site was found in the codebase for these three constants. They appear to be reserved for a future multi-agent phase. Do not build consumers that depend on them being emitted today.
</Callout>

---

## Skill fork events

Source: `internal/harness/events.go:176–180`

<Callout type="warning">
`skill.fork.started`, `skill.fork.completed`, and `skill.fork.failed` are defined in the event catalog and included in `AllEventTypes()` but no production emission site was found in the codebase. They are defined-but-unconfirmed. Do not rely on them being emitted.
</Callout>

---

## Profile efficiency suggestion

Source: `internal/harness/events.go:343–352`

### profile.efficiency_suggestion

Emitted after a subagent run using a named profile when the run's efficiency score falls below 0.6. The efficiency formula is `1.0 / (1.0 + steps × 0.1 + costUSD × 10.0)`.

```json
{
  "profile_name": "researcher",
  "run_id": "run_abc",
  "efficiency_score": 0.42,
  "steps": 18,
  "cost_usd": 0.08
}
```

<Callout type="warning">
The comment in `events.go` for this event mentions `unused_tools` and `remove_tools` fields. The actual emission site (`runner.go:2767–2773`) uses `steps` and `cost_usd` instead. The comment describes an earlier planned payload shape; the payload above reflects the actual emitted fields.
</Callout>

---

## Diagnostic and forensics events (opt-in)

All events in this section require a flag set on `RunnerConfig`. They are never emitted unless explicitly enabled.

<Card>
<CardHeader>
<CardTitle>Context window forensics</CardTitle>
</CardHeader>
<CardContent>

**Enable:** `RunnerConfig.ContextWindowSnapshotEnabled = true`

| Event | When |
|---|---|
| `context.window.snapshot` | After each LLM turn |
| `context.window.warning` | When usage exceeds `ContextWindowWarningThreshold` |

`context.window.snapshot` payload (typed `ContextWindowSnapshotPayload`):
```json
{
  "step": 2,
  "provider_reported_tokens": 4096,
  "provider_reported": true,
  "estimated_total_tokens": 4200,
  "max_context_tokens": 128000,
  "usage_ratio": 0.033,
  "headroom_tokens": 123904,
  "breakdown": {
    "system_prompt_tokens": 512,
    "conversation_tokens": 3500,
    "tool_result_tokens": 84,
    "estimated": true
  }
}
```

`context.window.warning` payload:
```json
{
  "step": 2,
  "usage_ratio": 0.88,
  "threshold": 0.80,
  "provider_reported": false,
  "tokens_used": 112640,
  "max_context_tokens": 128000
}
```

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>LLM request envelope forensics</CardTitle>
</CardHeader>
<CardContent>

**Enable:** `RunnerConfig.CaptureRequestEnvelope = true`

| Event | When |
|---|---|
| `llm.request.snapshot` | Before each provider call |
| `llm.response.meta` | After each provider call |

`llm.request.snapshot`:
```json
{
  "step": 1,
  "prompt_hash": "sha256hexstring",
  "tool_names": ["bash", "read"],
  "memory_snippet": "string"
}
```

`llm.response.meta`:
```json
{
  "step": 1,
  "latency_ms": 980,
  "model_version": "string"
}
```

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Tool decision tracing</CardTitle>
</CardHeader>
<CardContent>

| Flag | Event |
|---|---|
| `RunnerConfig.TraceToolDecisions = true` | `tool.decision` — per-step tool selection trace |
| `RunnerConfig.DetectAntiPatterns = true` | `tool.antipattern` — same (tool, args) pair used 3+ times |
| `RunnerConfig.TraceHookMutations = true` | `tool.hook.mutation` — pre-tool hook modified or blocked the call |

`tool.antipattern`:
```json
{
  "type": "string",
  "tool": "bash",
  "call_count": 3,
  "step": 5
}
```

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Audit trail</CardTitle>
</CardHeader>
<CardContent>

**Enable:** `RunnerConfig.AuditTrailEnabled = true` (also requires `RolloutDir` to be set)

`audit.action` — emitted per state-modifying tool call (source: `runner_step_engine.go:732`):
```json
{
  "tool": "file_write",
  "call_id": "call_abc",
  "arguments": "...",
  "step": 3
}
```

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Causal graph</CardTitle>
</CardHeader>
<CardContent>

**Enable:** `RunnerConfig.CausalGraphEnabled = true`

`causal.graph.snapshot` — emitted at run end with the full causal dependency graph (`runner_step_engine.go:113`).

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Error chain</CardTitle>
</CardHeader>
<CardContent>

**Enable:** `RunnerConfig.ErrorChainEnabled = true`

`error.context` — emitted immediately before `run.failed`. Contains the error class, message, optional cause chain, and a rolling snapshot of recent tool calls and messages (default depth: 10).

</CardContent>
</Card>

---

## Other system events

### Empty-response retry

Source: `internal/harness/events.go:288–295`

`llm.empty_response.retry` — emitted when the LLM returned no text and no tool calls, triggering a retry:

```json
{
  "step": 2,
  "retry": 1,
  "max_retries": 3
}
```

### Dynamic rule injection

Source: `internal/harness/events.go:298–304`

`rule.injected` — emitted when a `DynamicRule` fires and is injected into the system prompt:

```json
{
  "rule_id": "string",
  "step": 2,
  "trigger_tool": "bash"
}
```

### Meta message injection

Source: `internal/harness/events.go:156–158`

`meta.message.injected` — emitted when a meta-message is injected into the conversation (`runner_step_engine.go:1216`).

### Recorder drop detection

Source: `internal/harness/events.go:307–316`

`recorder.drop_detected` — emitted when the recorder channel is full and a non-terminal event is dropped. If you see this, the recorder is under pressure and your event log has a gap.

```json
{
  "dropped_event_id": "string",
  "dropped_event_type": "string",
  "dropped_seq": 42
}
```

---

## Reserved and unconfirmed events

The following constants are defined and returned by `AllEventTypes()` but have no confirmed production emission site in the current codebase:

| Event string | Constant | Status |
|---|---|---|
| `skill.fork.started` | `EventSkillForkStarted` | Defined, no emission site found |
| `skill.fork.completed` | `EventSkillForkCompleted` | Defined, no emission site found |
| `skill.fork.failed` | `EventSkillForkFailed` | Defined, no emission site found |
| `spawn_agent.started` | `EventSpawnAgentStarted` | Defined, no emission site found |
| `spawn_agent.completed` | `EventSpawnAgentCompleted` | Defined, no emission site found |
| `task.completed` | `EventTaskCompleted` | Defined, no emission site found |
| `tool.activated` | `EventToolActivated` | Defined, no emission site found |

<Callout type="warning">
These events appear reserved for multi-agent and skill-fork features not yet plumbed into the production runner. They are included in `AllEventTypes()` for completeness but should not be used in consumer logic that must handle real traffic. Verify before using in production integrations.
</Callout>

---

## Quick reference: event count by category

| Category | Count | Opt-in? |
|---|---|---|
| Run lifecycle | 10 | No |
| LLM turn | 6 | No |
| Tool execution | 8 | No (+ 1 unconfirmed) |
| Accounting / cost | 2 | `cost.anomaly` only |
| Workspace | 3 | When `workspace_type` is set |
| Memory | 4 | When memory is enabled |
| Hooks (message + tool) | 6 | No |
| Callbacks | 3 | No |
| Skill constraints | 3 | No |
| Steering / conversation / prompt / provider | 5 | No |
| Context management | 4 | No |
| Agent spawning / budget | 5 | No (2 active + 3 unconfirmed) |
| Profile efficiency | 1 | No |
| Context window forensics | 2 | `ContextWindowSnapshotEnabled` |
| LLM request envelope | 2 | `CaptureRequestEnvelope` |
| Tool decision tracing | 3 | Per flag |
| Audit trail | 1 | `AuditTrailEnabled` |
| Causal graph | 1 | `CausalGraphEnabled` |
| Error chain | 1 | `ErrorChainEnabled` |
| Other system events | 4 | No |
| Reserved / unconfirmed | 7 | — |
| **Total** | **77** | |

> **Note:** The Count column sums to more than 77 because a few events (`tool.call.blocked`, `spawn_agent.started`, `spawn_agent.completed`, `task.completed`) are cross-listed in multiple categories. The true distinct total is 77, as returned by `AllEventTypes()`.

---

## Next steps

- To stream events in practice, see [Using the HTTP API](/docs/server/http-api-guide).
- To replay and diff event streams from recorded runs, see the forensics tooling in [Operations](/docs/operations/rollout-replay-forensics).
- To understand how events map to run state, see [Concepts: Events](/docs/concepts/events).
