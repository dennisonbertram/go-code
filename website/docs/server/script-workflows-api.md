---
title: "Script Workflows over HTTP"
sidebar_label: "Script Workflows API"
sidebar_position: 4
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The Script Workflows API lets you run multi-agent Go pipelines through `harnessd` without writing a single line of HTTP client code. You define a **script** — a plain Go function registered under a name — and the engine runs it asynchronously, tracks its lifecycle, emits structured events, and exposes everything over HTTP with Server-Sent Events (SSE) for real-time streaming.

This page covers the six HTTP routes, the JSON shapes for starting and inspecting runs, the SSE event stream format, and how to resume a run that failed.

---

## Prerequisites

The script-workflow routes are gated behind the `ScriptWorkflows` field in `ServerOptions`. If that field is not set (i.e., no `scriptWorkflowManager` is wired in), every route returns `501 Not Implemented`. Check your server setup before calling these endpoints.

<Callout type="info">
Both `workflow.Engine` and `workflow.SourceManager` satisfy the `scriptWorkflowManager` interface, so you can use either as the backing implementation.
</Callout>

---

## Routes

Six routes form the complete surface area of the Script Workflows API:

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| `GET` | `/v1/script-workflows` | `runs:read` | List all registered script workflows |
| `GET` | `/v1/script-workflows/{name}` | `runs:read` | Get metadata for a specific workflow |
| `POST` | `/v1/script-workflows/{name}/runs` | `runs:write` | Start a workflow run |
| `GET` | `/v1/script-workflow-runs/{id}` | `runs:read` | Get run status and result |
| `GET` | `/v1/script-workflow-runs/{id}/events` | `runs:read` | Stream run events over SSE |
| `POST` | `/v1/script-workflow-runs/{id}/resume` | `runs:write` | Resume a failed run |

When auth is enabled, all routes require a Bearer token (`Authorization: Bearer <token>`) and the listed scopes. With auth disabled (no key store configured), scope checks are skipped. The `runs:write` scope also satisfies `runs:read` checks.

<Callout type="warning">
When `ScriptWorkflows` is not configured in `ServerOptions`, all six routes return `501 Not Implemented` with body `{"error":{"code":"not_implemented","message":"script workflow service is not configured"}}`.
</Callout>

---

## Listing and inspecting workflows

### List all workflows

```bash
curl -s http://localhost:8080/v1/script-workflows \
  -H "Authorization: Bearer $HARNESS_TOKEN"
```

Response (200 OK):

```json
{
  "workflows": [
    {
      "name": "ux-feedback-check",
      "description": "Workflow UX smoke that emits phase, log, finding feedback, and returns args.",
      "when_to_use": "Use to validate workflow discovery, run, feedback, SSE, and result handling."
    }
  ]
}
```

The array is sorted by name. Each entry is a `workflow.Meta` value — the same metadata you set when you called `engine.Register` or defined in a bundle's `workflow.json`.

### Get a single workflow

```bash
curl -s http://localhost:8080/v1/script-workflows/ux-feedback-check \
  -H "Authorization: Bearer $HARNESS_TOKEN"
```

Returns the `Meta` object for that workflow, or `404` with `"not_found"` if no workflow with that name is registered.

---

## Starting a run

Send a `POST` to `/v1/script-workflows/{name}/runs` with an optional JSON body. The `args` field is a free-form object that gets passed into the workflow's `Context.Args`.

```bash
curl -s -X POST http://localhost:8080/v1/script-workflows/ux-feedback-check/runs \
  -H "Authorization: Bearer $HARNESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": {"target": "homepage", "depth": 2}}'
```

The body is optional. If you omit it entirely or send an empty body, `args` defaults to `nil`.

### Start response (202 Accepted)

```json
{
  "run_id": "wf_f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "running",
  "workflow_name": "ux-feedback-check"
}
```

<Callout type="warning">
Run IDs are always prefixed `wf_` followed by a UUID. The `"queued"` status constant exists in the codebase but `Start` creates runs with status `"running"` directly — you will not see `"queued"` in the start response today.
</Callout>

Save the `run_id`; you will use it to poll for results and stream events.

---

## Getting run status and result

```bash
curl -s http://localhost:8080/v1/script-workflow-runs/wf_f47ac10b-.../  \
  -H "Authorization: Bearer $HARNESS_TOKEN"
```

Response (200 OK):

```json
{
  "id": "wf_f47ac10b-...",
  "workflow_name": "ux-feedback-check",
  "status": "completed",
  "result_json": "{\"ok\":true,\"args\":{\"target\":\"homepage\",\"depth\":2}}",
  "error": "",
  "created_at": "2026-06-28T12:00:00Z",
  "updated_at": "2026-06-28T12:00:03Z"
}
```

The `status` field follows the run lifecycle:

| Value | Meaning |
|-------|---------|
| `"running"` | Script is executing |
| `"completed"` | Script returned a result; `result_json` is populated |
| `"failed"` | Script returned an error; `error` is populated |

The `result_json` field is a JSON string (not an embedded object) containing whatever your script returned. Deserialize it separately on the client side.

---

## Streaming events over SSE

The events endpoint gives you a real-time feed of everything happening inside a workflow run. It replays all historical events first, then delivers live events as they arrive, and closes the stream when the run reaches a terminal state.

```bash
curl -N http://localhost:8080/v1/script-workflow-runs/wf_f47ac10b-.../events \
  -H "Authorization: Bearer $HARNESS_TOKEN"
```

(`-N` disables curl's output buffering so you see events as they arrive.)

### SSE frame format

Each event is a block of three lines followed by a blank line:

```
id: <seq>
event: <event-type>
data: <payload-json>

```

- `id` — A monotonically increasing sequence number (integer) scoped to this run.
- `event` — One of the `workflow.*` event type strings.
- `data` — The event payload as a JSON object (may be `null`).

### Workflow event types

| Event type | When it fires |
|------------|--------------|
| `workflow.started` | Script begins executing (also fires on resume, with `"resumed": true` in payload) |
| `workflow.phase.started` | Script called `ctx.Phase(title)` |
| `workflow.agent.started` | A sub-agent call was dispatched |
| `workflow.agent.completed` | A sub-agent call succeeded |
| `workflow.agent.failed` | A sub-agent call failed |
| `workflow.log` | Script called `ctx.Log(message)` — payload has `"message"` key |
| `workflow.feedback` | Script called `ctx.Feedback(...)` with a non-finding/warning kind |
| `workflow.finding` | Script called `ctx.Feedback("finding", ...)` |
| `workflow.warning` | Script called `ctx.Feedback("warning", ...)` |
| `workflow.question` | Script called `ctx.Question(...)` |
| `workflow.completed` | Script finished — payload is `{"workflow": <name>}`; **stream closes** (fetch `result_json` from the get-run endpoint for the return value) |
| `workflow.failed` | Script returned an error — **stream closes** |

The stream closes automatically when it emits `workflow.completed` or `workflow.failed`. If the run is already finished when you connect, historical events are replayed immediately and the stream closes without waiting.

### A complete SSE session example

<Tabs>
<TabsList>
  <TabsTrigger value="bash">bash</TabsTrigger>
  <TabsTrigger value="output">example output</TabsTrigger>
</TabsList>
<TabsContent value="bash">
```bash
# Start the run
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/script-workflows/ux-feedback-check/runs \
  -H "Authorization: Bearer $HARNESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": {"target": "homepage"}}' \
  | jq -r '.run_id')

echo "Run ID: $RUN_ID"

# Stream events until the run finishes
curl -N "http://localhost:8080/v1/script-workflow-runs/${RUN_ID}/events" \
  -H "Authorization: Bearer $HARNESS_TOKEN"
```
</TabsContent>
<TabsContent value="output">
```
id: 1
event: workflow.started
data: {"workflow":"ux-feedback-check"}

id: 2
event: workflow.phase.started
data: {"phase":"Workflow UX"}

id: 3
event: workflow.log
data: {"message":"workflow ux path running"}

id: 4
event: workflow.finding
data: {"kind":"finding","message":"workflow feedback reached host","requires_response":false,"data":{"path":"api-and-tmux"}}

id: 5
event: workflow.completed
data: {"workflow":"ux-feedback-check"}

```
</TabsContent>
</Tabs>

---

## Resuming a failed run

If a run reaches `"failed"` status you can retry it — optionally with new args — using the resume endpoint.

```bash
curl -s -X POST \
  "http://localhost:8080/v1/script-workflow-runs/wf_f47ac10b-.../resume" \
  -H "Authorization: Bearer $HARNESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": {"target": "homepage", "depth": 3}}'
```

Response (202 Accepted):

```json
{
  "run_id": "wf_f47ac10b-...",
  "status": "running"
}
```

<Callout type="warning">
Resume only works when the run's current status is `"failed"`. Attempting to resume a `"running"` or `"completed"` run returns `400 Bad Request`. After a successful resume, connect to the events endpoint again to re-stream from the new `workflow.started` event onward.
</Callout>

The same `run_id` is reused across resumes. The engine clears the previous error, resets status to `"running"`, and emits a new `workflow.started` event with `"resumed": true` in the payload before re-executing the script.

---

## Putting it all together

<Steps>
<Step>
**Register a workflow** in your server setup by calling `engine.Register("my-workflow", scriptFn)` (or by dropping a bundle into a discovery directory for Go-authored workflows).
</Step>
<Step>
**Start a run** with `POST /v1/script-workflows/my-workflow/runs`. Save the `run_id` from the 202 response.
</Step>
<Step>
**Stream events** from `GET /v1/script-workflow-runs/{run_id}/events`. Parse each SSE frame; the stream closes on `workflow.completed` or `workflow.failed`.
</Step>
<Step>
**Read the result** from `GET /v1/script-workflow-runs/{run_id}`. The `result_json` field contains the serialized return value from your script.
</Step>
<Step>
**Resume on failure** (optional) by sending `POST /v1/script-workflow-runs/{run_id}/resume` if status is `"failed"`, then stream events again.
</Step>
</Steps>

---

## Error shapes

All error responses follow the standard harness envelope:

```json
{"error": {"code": "<machine_code>", "message": "<human text>"}}
```

Scope errors use a slightly different shape:

```json
{"error": "insufficient_scope", "required": "runs:write"}
```

Common error codes for this API:

| HTTP status | Code | When |
|-------------|------|------|
| 400 | `invalid_request` | `POST .../runs` — workflow name not registered; or `POST .../resume` — run is not in `"failed"` status |
| 404 | `not_found` | `GET /v1/script-workflows/{name}` — workflow name not registered; or `GET/POST /v1/script-workflow-runs/{id}` — run ID does not exist |
| 501 | `not_implemented` | `ScriptWorkflows` not configured in `ServerOptions` |

---

## Next steps

- To understand how to write the Go scripts that power these runs, see the [Workflow Engine](/docs/workflows/workflow-engine) page.
- To write a Go-authored workflow bundle (compiled binary, discovered from disk), see the [Workflow SDK](/docs/workflows/workflow-sdk) guide.
- For the broader HTTP API including standard agent runs and SSE format details, see the [HTTP Routes Reference](/docs/reference/http-routes).
