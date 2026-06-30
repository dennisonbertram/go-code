---
title: "The Event Model"
sidebar_label: "Event Model"
sidebar_position: 6
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

Every agent run in go-code communicates progress through a stream of **Server-Sent Events (SSE)**. When you start a run with `POST /v1/runs`, the server assigns it an ID, queues it, and starts executing it asynchronously. You then connect to `GET /v1/runs/{id}/events` to receive a real-time feed of everything that happens: when the LLM was called, which tools ran, how many tokens were used, and finally whether the run succeeded or failed.

This page explains the wire format of that stream, how to know when it is finished, how to reconnect if your connection drops, and what categories of events to expect. For the complete list of every event type and its payload fields, see the [Event Catalog reference](/docs/reference/events-catalog).

---

## The SSE wire format

The harness streams events as plain-text SSE over HTTP. Each event is a block of four lines followed by a blank line:

```
id: <runID>:<seq>
retry: 3000
event: <event-type>
data: <JSON Event object>

```

- **`id`** — A globally unique identifier in the form `{runID}:{seq}`, where `seq` is a zero-based counter incremented for each event in the run. Browsers and clients use this value for reconnection (see [Reconnection and keepalive](#reconnection-and-keepalive)).
- **`retry`** — Tells the client to wait 3,000 ms before reconnecting after a dropped connection.
- **`event`** — The event type string, e.g. `run.started` or `tool.call.completed`.
- **`data`** — A single line containing the full `Event` JSON object.

### The Event JSON struct

The `data` field is always a JSON-serialized `Event`:

```go
type Event struct {
    ID        string         `json:"id"`        // "<runID>:<seq>"
    RunID     string         `json:"run_id"`
    Type      EventType      `json:"type"`      // e.g. "run.started"
    Timestamp time.Time      `json:"timestamp"` // RFC3339 UTC
    Payload   map[string]any `json:"payload,omitempty"`
}
```

A full frame on the wire looks like this:

```
id: run_abc123:4
retry: 3000
event: tool.call.completed
data: {"id":"run_abc123:4","run_id":"run_abc123","type":"tool.call.completed","timestamp":"2026-06-28T12:34:56Z","payload":{"call_id":"call_1","tool":"bash","output":"hello world\n","duration_ms":142,"schema_version":"1","conversation_id":"conv_xyz","step":2}}

```

### Live event viewer

The block below is fully editable — change the event fields and watch the formatted output update in real time. No server needed; this runs entirely in your browser.

```jsx live
function EventViewer() {
  const event = {
    id: "run_abc123:3",
    run_id: "run_abc123",
    type: "run.step.completed",
    timestamp: "2026-06-28T12:34:56Z",
    payload: {
      step: 3,
      tool_calls: ["bash", "read_file"],
      duration_ms: 842,
      schema_version: "1",
      conversation_id: "conv_xyz",
    },
  };

  const labelStyle = {
    fontSize: "0.7rem",
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.05em",
    color: "#888",
    marginBottom: "2px",
  };
  const valueStyle = {
    fontFamily: "monospace",
    fontSize: "0.85rem",
  };
  const rowStyle = {
    display: "flex",
    flexDirection: "column",
    gap: "2px",
    padding: "8px 12px",
    borderRadius: "6px",
    background: "rgba(0,144,255,0.06)",
    border: "1px solid rgba(0,144,255,0.2)",
  };
  const gridStyle = {
    display: "grid",
    gridTemplateColumns: "1fr 1fr",
    gap: "8px",
  };

  return (
    <div style={{ padding: "16px", fontFamily: "sans-serif" }}>
      <div style={{ marginBottom: "12px", display: "flex", alignItems: "center", gap: "8px" }}>
        <span style={{
          background: "rgba(40,169,72,0.15)",
          color: "#28a948",
          borderRadius: "4px",
          padding: "2px 8px",
          fontSize: "0.75rem",
          fontWeight: 700,
          fontFamily: "monospace",
        }}>
          {event.type}
        </span>
        <span style={{ fontSize: "0.8rem", color: "#888" }}>{event.timestamp}</span>
      </div>

      <div style={gridStyle}>
        <div style={rowStyle}>
          <span style={labelStyle}>run_id</span>
          <span style={valueStyle}>{event.run_id}</span>
        </div>
        <div style={rowStyle}>
          <span style={labelStyle}>id (seq)</span>
          <span style={valueStyle}>{event.id}</span>
        </div>
        <div style={rowStyle}>
          <span style={labelStyle}>step</span>
          <span style={valueStyle}>{event.payload.step}</span>
        </div>
        <div style={rowStyle}>
          <span style={labelStyle}>duration_ms</span>
          <span style={valueStyle}>{event.payload.duration_ms} ms</span>
        </div>
        <div style={{ ...rowStyle, gridColumn: "1 / -1" }}>
          <span style={labelStyle}>tool_calls</span>
          <span style={valueStyle}>{event.payload.tool_calls.join(", ")}</span>
        </div>
      </div>
    </div>
  );
}
```

### Auto-injected payload fields

Every payload automatically receives three fields injected by the event journal before the event is written to the stream:

| Field | Value |
|---|---|
| `schema_version` | Always `"1"` (`EventSchemaVersion`) |
| `conversation_id` | The run's conversation ID |
| `step` | Current step number (if not already set by the emitter) |

You can rely on these fields being present in every payload without checking per-event documentation.

---

## Terminal events

Three event types signal that the run is finished and the stream will close. Clients **must** stop reading after receiving any of these:

| Event type | Meaning |
|---|---|
| `run.completed` | Run finished successfully; `payload.output` contains the final response |
| `run.failed` | Run encountered an unrecoverable error; `payload.error` explains why |
| `run.cancelled` | Run was cancelled via `POST /v1/runs/{id}/cancel` |

The Go function `IsTerminalEvent(et EventType) bool` returns `true` for exactly these three. If you are writing your own consumer, use this same set.

<Callout variant="warning" title="run.cancelled is terminal">
Some older internal documentation states that only two events signal stream termination. That document is stale. The code (`IsTerminalEvent` in `internal/harness/events.go:466-468`) includes `run.cancelled` as a third terminal event. Always treat all three as terminal.
</Callout>

### What terminal payloads look like

**`run.completed`**:
```json
{
  "output": "The task is done. Here is what I found...",
  "usage_totals": { "prompt_tokens_total": 1200, "completion_tokens_total": 340, "total_tokens": 1540, "last_turn_tokens": 0 },
  "cost_totals": { "cost_usd_total": 0.0042, "last_turn_cost_usd": 0.0008, "cost_status": "available" },
  "schema_version": "1",
  "conversation_id": "conv_xyz",
  "step": 5
}
```

**`run.failed`** (max steps reached):
```json
{
  "error": "max steps (8) reached",
  "reason": "max_steps_reached",
  "max_steps": 8,
  "usage_totals": {},
  "cost_totals": {},
  "schema_version": "1",
  "conversation_id": "conv_xyz",
  "step": 8
}
```

**`run.cancelled`**:
```json
{
  "usage_totals": {},
  "cost_totals": {},
  "schema_version": "1",
  "conversation_id": "conv_xyz"
}
```

---

## Reconnection and keepalive

### Reconnecting with Last-Event-ID

If your connection drops mid-run, include the `Last-Event-ID` header on reconnect. The server replays all events after the sequence number you provide:

```bash
curl -N \
  -H "Accept: text/event-stream" \
  -H "Last-Event-ID: run_abc123:11" \
  http://localhost:8080/v1/runs/run_abc123/events
```

The server parses the `{runID}:{seq}` format and skips every event whose sequence number is at or below the one you provided. This means you receive exactly the events you missed — nothing more, nothing less.

<Callout variant="info" title="EventSource and query-string auth">
Browser `EventSource` cannot set custom headers. To authenticate an SSE connection from the browser, pass the token as a query parameter: `GET /v1/runs/{id}/events?token=your-api-key`. The server accepts both `Authorization: Bearer` and `?token=` for all SSE endpoints.
</Callout>

### Keepalive pings

The server sends an SSE comment line every 15 seconds to keep the connection alive through proxies and load balancers:

```
: ping

```

SSE comments (lines beginning with `:`) carry no event type and no data. Clients should ignore them. You can tune the interval with the `HARNESS_SSE_KEEPALIVE_SECONDS` environment variable.

---

## Observing a live stream

The examples below show how to connect to the event stream from the command line and from TypeScript.

<Tabs defaultValue="bash">
  <TabsList>
    <TabsTrigger value="bash">curl</TabsTrigger>
    <TabsTrigger value="typescript">TypeScript / EventSource</TabsTrigger>
  </TabsList>
  <TabsContent value="bash">

Start the server in key-free mode, submit a run, then tail the stream:

```bash
# Terminal 1: start harnessd with the fake provider (no API key needed)
HARNESS_PROVIDER=fake \
HARNESS_AUTH_DISABLED=true \
  go run ./cmd/harnessd

# Terminal 2: start a run and capture the run ID
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt":"hello"}' | jq -r .run_id)

# Terminal 2: stream the events until the connection closes
curl -N http://localhost:8080/v1/runs/$RUN_ID/events
```

You will see frames like:

```
id: run_abc123:0
retry: 3000
event: run.started
data: {"id":"run_abc123:0","run_id":"run_abc123","type":"run.started","timestamp":"...","payload":{"prompt":"hello","schema_version":"1","conversation_id":"conv_xyz","step":0}}

id: run_abc123:1
retry: 3000
event: llm.turn.requested
data: {"id":"run_abc123:1",...}

...

id: run_abc123:9
retry: 3000
event: run.completed
data: {"id":"run_abc123:9",...,"payload":{"output":"Hello! How can I help?","usage_totals":{...},...}}

```

  </TabsContent>
  <TabsContent value="typescript">

A minimal TypeScript client that closes cleanly on terminal events:

```typescript
const TERMINAL_EVENTS = ["run.completed", "run.failed", "run.cancelled"] as const;
type TerminalEvent = (typeof TERMINAL_EVENTS)[number];

function streamRun(
  baseUrl: string,
  runId: string,
  token?: string,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const url = new URL(`${baseUrl}/v1/runs/${runId}/events`);
    if (token) url.searchParams.set("token", token);

    const source = new EventSource(url.toString());

    // The server always sets an `event: <type>` line on every frame.
    // EventSource dispatches each frame to a listener registered for that
    // specific event name — the default `onmessage` handler only fires for
    // frames with no event type (or type "message"), which the harness never
    // emits. Register a named listener for each event type you care about.
    function onTerminal(e: MessageEvent) {
      const event = JSON.parse(e.data);
      console.log(`[${event.type}]`, event.payload);
      source.close();
      if (event.type === "run.failed") {
        reject(new Error(event.payload?.error ?? "run failed"));
      } else {
        resolve();
      }
    }

    TERMINAL_EVENTS.forEach((type) => source.addEventListener(type, onTerminal));

    source.onerror = (err) => {
      source.close();
      reject(err);
    };
  });
}

// Usage
const response = await fetch("http://localhost:8080/v1/runs", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ prompt: "What is 2 + 2?" }),
});
const { run_id } = await response.json();
await streamRun("http://localhost:8080", run_id);
```

  </TabsContent>
</Tabs>

---

## Event categories at a glance

The harness emits 77 event types across several categories. Here is a high-level map so you know where to look in the full catalog.

<Card>
  <CardHeader>
    <CardTitle>Run lifecycle</CardTitle>
  </CardHeader>
  <CardContent>
    Ten events track the overall run: <code>run.started</code>, <code>run.queued</code>, <code>run.step.started</code>, <code>run.step.completed</code>, <code>run.waiting_for_user</code>, <code>run.resumed</code>, <code>run.cost_limit_reached</code>, <code>run.completed</code>, <code>run.failed</code>, <code>run.cancelled</code>. The last three are terminal.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>LLM turns</CardTitle>
  </CardHeader>
  <CardContent>
    Five events bracket each call to the language model: <code>llm.turn.requested</code>, <code>llm.turn.completed</code>, <code>assistant.message.delta</code> (streaming text), <code>assistant.thinking.delta</code> (streaming reasoning), and <code>assistant.message</code> (full text, no-tool-call turns). The <code>llm.turn.completed</code> payload includes <code>total_duration_ms</code>, <code>ttft_ms</code>, and <code>provider</code>.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Tool execution</CardTitle>
  </CardHeader>
  <CardContent>
    Eight events cover tool calls: <code>tool.call.started</code>, <code>tool.call.completed</code>, <code>tool.call.delta</code> (streaming arguments), <code>tool.output.delta</code> (streaming output), <code>tool.call.blocked</code> (blocked by skill constraint), and the approval gate trio <code>tool.approval_required</code>, <code>tool.approval_granted</code>, <code>tool.approval_denied</code>.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Accounting</CardTitle>
  </CardHeader>
  <CardContent>
    <code>usage.delta</code> fires after every LLM turn with per-step and cumulative token counts and cost in USD. The <code>usage_status</code> field is either <code>provider_reported</code> or <code>provider_unreported</code>. A separate <code>cost_status</code> field is one of <code>available</code>, <code>unpriced_model</code>, <code>provider_unreported</code>, or <code>pending</code>. The opt-in <code>cost.anomaly</code> event fires when a step's cost exceeds a configurable multiplier of the rolling average.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Workspace</CardTitle>
  </CardHeader>
  <CardContent>
    When a run provisions a per-run workspace (<code>workspace_type</code> set in the run request), you receive <code>workspace.provisioned</code> at the start and <code>workspace.destroyed</code> at the end. If provisioning fails, <code>workspace.provision_failed</code> fires and is immediately followed by <code>run.failed</code>.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Memory</CardTitle>
  </CardHeader>
  <CardContent>
    Four events track the memory subsystem: <code>memory.observe.started</code>, <code>memory.observe.completed</code>, <code>memory.observe.failed</code>, and <code>memory.reflection.completed</code>.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Hooks and callbacks</CardTitle>
  </CardHeader>
  <CardContent>
    Message-level hooks emit <code>hook.started</code>, <code>hook.failed</code>, <code>hook.completed</code>. Tool-level hooks use the <code>tool_hook.*</code> prefix (note the underscore). Deferred callbacks emit <code>callback.scheduled</code>, <code>callback.fired</code>, and <code>callback.canceled</code> — these may fire after the originating run has already ended.
  </CardContent>
</Card>

<Card>
  <CardHeader>
    <CardTitle>Forensics (opt-in)</CardTitle>
  </CardHeader>
  <CardContent>
    Several diagnostic categories are off by default and must be enabled in <code>RunnerConfig</code>: context window snapshots (<code>context.window.snapshot</code>, <code>context.window.warning</code>), LLM request envelopes (<code>llm.request.snapshot</code>, <code>llm.response.meta</code>), error chain (<code>error.context</code>), audit trail (<code>audit.action</code>), tool decision tracing (<code>tool.decision</code>, <code>tool.antipattern</code>, <code>tool.hook.mutation</code>), and causal graph (<code>causal.graph.snapshot</code>).
  </CardContent>
</Card>

<Callout variant="warning" title="Reserved event types">
Several event type constants — including <code>skill.fork.started</code>, <code>skill.fork.completed</code>, <code>skill.fork.failed</code>, <code>spawn_agent.started</code>, <code>spawn_agent.completed</code>, <code>task.completed</code>, and <code>tool.activated</code> — are defined in <code>internal/harness/events.go</code> and appear in <code>AllEventTypes()</code>, but no production emit site for these specific constants was found in the codebase at research time. Treat them as reserved; do not build logic that depends on receiving them until confirmed in the changelog.
</Callout>

---

## Next steps

- **Full catalog**: Every event type, its payload schema, and which `RunnerConfig` flags control opt-in events are documented in the [Event Catalog reference](/docs/reference/events-catalog).
- **Starting a run**: The fields you can set when calling `POST /v1/runs` (including `max_steps`, `max_cost_usd`, and workspace options) are covered in the [HTTP API reference](/docs/reference/http-routes).
- **Connecting from the CLI**: `harnesscli` streams events automatically — see the [CLI reference](/docs/cli/harnesscli) for flags that control output verbosity.
