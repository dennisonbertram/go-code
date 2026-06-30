---
title: "Runs and Conversations"
sidebar_label: "Runs & Conversations"
sidebar_position: 2
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Badge } from '@site/src/components/ui';

A **run** is one discrete unit of agent work: the harness receives a prompt, executes LLM turns and tool calls, and produces a final output. Every run is independent by default, but you can link runs together into a **conversation** so the agent remembers what happened before. This page explains the run lifecycle, how `conversation_id` stitches runs together, and how to steer or inspect a run while it is active.

---

## What a run is

Starting a run is a single HTTP call:

```bash
curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "List the files in the current directory"}'
```

The server responds immediately with HTTP 202 and a run ID:

```json
{"run_id": "run_abc123", "status": "queued"}
```

The run is accepted with status `queued` and transitions to `running` asynchronously once the run loop starts. Every step — LLM turn, tool call, token usage — is emitted as a Server-Sent Event on `GET /v1/runs/{id}/events`. Your client streams those events until it receives one of the three terminal events that close the stream.

### Run statuses

A run moves through a well-defined set of statuses over its lifetime:

<div style={{display: 'flex', flexWrap: 'wrap', gap: '8px', marginBottom: '16px'}}>
  <Badge variant="default">running</Badge>
  <Badge variant="secondary">queued</Badge>
  <Badge variant="success">completed</Badge>
  <Badge variant="destructive">failed</Badge>
  <Badge variant="outline">cancelled</Badge>
  <Badge variant="warning">waiting_for_user</Badge>
  <Badge variant="warning">waiting_for_approval</Badge>
</div>

| Status | Meaning |
|--------|---------|
| `running` | The agent is actively executing. |
| `queued` | Accepted, but the worker pool is full. The run will start when a slot opens. |
| `completed` | Finished successfully. `run.completed` is emitted and the stream closes. |
| `failed` | Finished with an error. `run.failed` is emitted and the stream closes. |
| `cancelled` | Stopped by a `POST /v1/runs/{id}/cancel` call. `run.cancelled` is emitted and the stream closes. |
| `waiting_for_user` | Paused on an interactive question (`AskUserQuestion` tool). Resumes when answers are submitted. |
| `waiting_for_approval` | Paused on a tool call that requires explicit operator approval. Resumes when approved or denied. |

The three **terminal events** — `run.completed`, `run.failed`, and `run.cancelled` — signal the definitive end of the run. When your SSE client receives any of these, it should close the connection.

<Callout variant="warning" title="Step limit applies by default">
  `harnessd` defaults to a maximum of 8 LLM steps per run when `HARNESS_MAX_STEPS` is not explicitly set. A run that hits this limit emits `run.failed` with `"reason": "max_steps_reached"`. See the [Configuration](/docs/concepts/configuration) page for how to raise or remove this limit.
</Callout>

---

## Conversations

A conversation is a named sequence of runs that share a persistent message history. When a new run is part of a conversation, the harness loads the prior messages before calling the LLM — giving the agent full context of what was said and done before.

### How `conversation_id` works

Every `RunRequest` accepts an optional `conversation_id` field. If you omit it, the server automatically sets `conversation_id` equal to the run's own ID. This means the first run in a new conversation doubles as the conversation identifier — no separate creation step required.

```bash
# Start a run without a conversation_id.
# The server assigns conversation_id = run_abc123 automatically.
curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "What is 2 + 2?"}'
# Response: {"run_id": "run_abc123", "status": "queued"}
```

To pin a follow-up run to the same conversation, pass the first run's ID as `conversation_id`:

```bash
# Follow-up run in the same conversation.
curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "Now multiply that by 10.",
    "conversation_id": "run_abc123"
  }'
```

Every event in every run includes the `conversation_id` in its payload via an automatically injected field, so you can always trace which conversation an event belongs to.

### Continuing from the last run

If you already have a run ID and just want to ask another question in the same conversation, use the `/continue` endpoint instead of constructing a new `RunRequest` manually:

```bash
# Start a new run in the same conversation as run_abc123.
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/continue \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "What was the result again?"}'
# Response: {"run_id": "run_def456", "status": "queued"}
```

`POST /v1/runs/{id}/continue` starts a fresh run using the `conversation_id` of the referenced run. It returns HTTP 202 with a new run ID — stream that new ID's events just as you would any other run. The `conversation.continued` SSE event is emitted on the new run once prior history has been loaded, and its payload includes the `conversation_id` and `prior_message_count` so you can confirm continuity.

**Precondition:** `/continue` only works once the referenced run has reached `completed` status. Calling it on a run that is still active returns HTTP 409 with code `run_not_completed`. Runs that have been pruned from in-memory state return 404 `not_found`.

<Callout variant="info">
  The TUI (`harnesscli --tui`) tracks `conversation_id` automatically across messages in a session. Each new message reuses the ID from the first run in that session, so you never need to manage it manually when using the TUI.
</Callout>

---

## Mid-run control

Once a run is active you can send it instructions without waiting for it to finish.

### steer — inject a message

`POST /v1/runs/{id}/steer` pushes a new user message into the agent's active context window. The agent sees the message at the start of its next LLM turn and can adjust its course accordingly.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/steer \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "Actually, write the output to output.txt instead."}'
# Response: {"status": "accepted"}
```

The `prompt` field is required. The server responds synchronously with HTTP 202 `{"status": "accepted"}` once the message has been queued. The run emits a `steering.received` event when the message is ingested.

### compact — reduce context pressure

`POST /v1/runs/{id}/compact` replaces older messages in the in-memory context with a summary, freeing token headroom for long-running tasks.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/compact \
  -H 'Content-Type: application/json' \
  -d '{"keep_last": 10}'
# Response: {"ok": true, "messages_removed": 42}
```

Optional body fields: `mode` and `keep_last` (the number of most-recent messages to preserve). The response includes `messages_removed` so you can verify how much context was trimmed.

<Callout variant="warning" title="Compact vs conversation-level compact">
  This endpoint compacts the in-memory context of an active run. A separate endpoint — `POST /v1/conversations/{id}/compact` — compacts the persisted conversation history and can auto-generate a summary via LLM when no `summary` body field is provided. They are different operations; do not confuse them.
</Callout>

### cancel — request cooperative cancellation

`POST /v1/runs/{id}/cancel` asks the agent to stop after finishing its current step. It does not kill the process mid-tool.

```bash
curl -s -X POST http://localhost:8080/v1/runs/run_abc123/cancel
# Response: {"status": "cancelling"}
```

The run transitions to `cancelled` and emits the terminal `run.cancelled` event once the current step completes. Cancellation is cooperative — if a long-running tool is active, the run finishes that tool call first.

---

## Run telemetry

After a run ends (or while it is still active), you can fetch a structured summary:

```bash
curl -s http://localhost:8080/v1/runs/run_abc123/summary
```

Response shape (`RunSummary`):

```json
{
  "run_id": "run_abc123",
  "status": "completed",
  "steps_taken": 4,
  "total_prompt_tokens": 3120,
  "total_completion_tokens": 847,
  "total_cost_usd": 0.0041,
  "cost_status": "available",
  "tool_calls": [
    {"tool_name": "bash", "step": 1},
    {"tool_name": "read", "step": 2}
  ],
  "cache_hit_rate": 0.62
}
```

<Card>
  <CardHeader>
    <CardTitle>Summary fields at a glance</CardTitle>
  </CardHeader>
  <CardContent>

| Field | What it tells you |
|-------|------------------|
| `steps_taken` | Number of LLM turns executed |
| `total_prompt_tokens` | Tokens sent to the model (including system prompt and history) |
| `total_completion_tokens` | Tokens generated by the model |
| `total_cost_usd` | Estimated USD cost of the run |
| `cache_hit_rate` | Fraction of prompt tokens served from the provider's prompt cache (0.0–1.0) |
| `tool_calls` | Ordered list of every tool invocation with its step number |
| `cost_status` | `available` when priced, otherwise `unpriced_model`, `provider_unreported`, or `pending` |

  </CardContent>
</Card>

<Callout variant="warning" title="cost_limit_reached stops the run early">
  When a run's cumulative cost reaches the `max_cost_usd` ceiling, the harness emits `run.cost_limit_reached`, then `run.step.completed`, and immediately terminates the run with the terminal `run.completed` event — no further LLM steps are executed. `run.cost_limit_reached` is not itself a terminal event (it is not in `IsTerminalEvent`), but it is an early-termination signal: the run ends cleanly with status `completed` rather than `failed`, without executing any more work.
</Callout>

---

## Next steps

- **Events reference**: every event type, its payload shape, and when it fires — see [Events](/docs/concepts/events).
- **Configuration**: adjusting `HARNESS_MAX_STEPS`, cost ceilings, and other server-level defaults — see [Configuration](/docs/concepts/configuration).
- **Starting the server**: how to run `harnessd` locally with the fake provider for key-free testing — see [Getting Started](/docs/getting-started/key-free-testing).
