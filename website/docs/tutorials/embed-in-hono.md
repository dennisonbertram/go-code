---
title: "Tutorial: Building harnessd into a Hono Server"
sidebar_label: "Build into a Hono Server"
sidebar_position: 4
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

This tutorial shows you how to build a [Hono](https://hono.dev/) TypeScript server that delegates AI work to a running `harnessd` instance. You will add two routes:

- `POST /runs` — accepts a prompt from a browser or API client, starts a `harnessd` run, and returns the `run_id`.
- `GET /runs/:id/events` — proxies the live `harnessd` SSE stream to the caller so a browser `EventSource` can consume it directly.

The result is a thin gateway layer: your Hono server handles authentication, routing, and business logic; `harnessd` handles the LLM execution and event emission. The two processes communicate over HTTP on localhost (or a private network).

<Callout variant="warning">
`harnessd` is a separate Go process — it is not embedded inside Node.js. The Hono server calls it over HTTP like any other microservice. "Embed" in this tutorial's title means wiring it into your existing Hono application, not linking it in-process.
</Callout>

---

## Prerequisites

- Node.js 18+ and a package manager (`npm`, `pnpm`, or `bun`)
- Go 1.25+ (to build and run `harnessd`)
- The `go-code` repository cloned locally (the harnessd source lives in `cmd/harnessd/`)

---

## Set up harnessd (key-free)

Start `harnessd` first. The fake provider lets you run the whole tutorial without a paid API key.

<Steps>
<Step>

### Start harnessd with the fake provider

The fake provider requires a turns file: a JSON file that scripts the responses it will return. Each entry is consumed by one run, so write enough entries for all the smoke-test runs in this tutorial (four is sufficient):

```bash
cat > /tmp/turns.json <<'EOF'
[
  {"content": "smoke ok", "usage": {"prompt": 100, "completion": 50}, "cost_usd": 0.001, "cost_status": "available"},
  {"content": "smoke ok", "usage": {"prompt": 100, "completion": 50}, "cost_usd": 0.001, "cost_status": "available"},
  {"content": "smoke ok", "usage": {"prompt": 100, "completion": 50}, "cost_usd": 0.001, "cost_status": "available"},
  {"content": "smoke ok", "usage": {"prompt": 100, "completion": 50}, "cost_usd": 0.001, "cost_status": "available"}
]
EOF
```

Then start `harnessd` from the repository root pointing at it:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/turns.json \
HARNESS_AUTH_DISABLED=true \
go run ./cmd/harnessd
```

The server prints `harness server listening on :8080` when it is ready. The three environment variables are doing important work here:

- `HARNESS_PROVIDER=fake` — uses a built-in scripted provider instead of a real LLM. No network calls, no API key required.
- `HARNESS_FAKE_TURNS=/tmp/turns.json` — path to the turns file above. The fake provider requires this; if it is unset the server exits at startup with a fatal error.
- `HARNESS_AUTH_DISABLED=true` — disables Bearer token validation so your curl commands and Hono server don't need to supply credentials during development.

For production you would set `OPENAI_API_KEY` (or another provider's key), remove `HARNESS_PROVIDER=fake` and `HARNESS_FAKE_TURNS`, and remove `HARNESS_AUTH_DISABLED=true`.

</Step>
<Step>

### Confirm the runs endpoint works

```bash
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "say hello", "allow_fallback": true}'
```

`allow_fallback: true` is required in fake mode. Without it the run is dispatched to the default model (`gpt-4.1-mini` → OpenAI), which fails because no `OPENAI_API_KEY` is set. With `allow_fallback: true` the runner falls back to the configured fake provider and the run completes key-free.

Expected response — HTTP 202 with a `run_id`:

```json
{"run_id": "run_abc123", "status": "queued"}
```

</Step>
<Step>

### Confirm the SSE stream works

Replace `run_abc123` with the actual ID from the previous step:

```bash
curl -s -N "http://localhost:8080/v1/runs/run_abc123/events"
```

You should see a stream of `text/event-stream` frames. Each frame looks like:

```
id: run_abc123:0
retry: 3000
event: run.started
data: {"id":"run_abc123:0","run_id":"run_abc123","type":"run.started","timestamp":"…","payload":{…}}

id: run_abc123:1
retry: 3000
event: provider.resolved
data: {"id":"run_abc123:1","run_id":"run_abc123","type":"provider.resolved","timestamp":"…","payload":{…}}

…
```

Between `run.started` and the terminal event, harnessd emits several intermediate events. The full sequence observed in fake mode is:

`run.started` → `prompt.warning` (only when falling back from the default model) → `provider.resolved` → `prompt.resolved` → `run.step.started` → `llm.turn.requested` → `usage.delta` → `llm.turn.completed` → `assistant.message` → `memory.observe.started` → `memory.observe.completed` → `run.step.completed` → `run.completed`

The exact set depends on the run — tool calls, additional steps, and other conditions add more events. The stream closes automatically after whichever terminal event arrives (`run.completed`, `run.failed`, or `run.cancelled`).

</Step>
</Steps>

---

## Set up the Hono project

<Steps>
<Step>

### Create a new project

```bash
mkdir hono-harness-gateway
cd hono-harness-gateway
npm init -y
npm install hono @hono/node-server
npm install -D typescript tsx @types/node
```

</Step>
<Step>

### Add a tsconfig

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "strict": true,
    "outDir": "dist",
    "types": ["node"]
  },
  "include": ["src"]
}
```

Save this as `tsconfig.json` in the project root.

</Step>
</Steps>

---

## Hono: start a run

Create `src/index.ts` with the `POST /runs` route:

```typescript
import { Hono } from 'hono';
import { serve } from '@hono/node-server';

const HARNESSD_URL = process.env.HARNESSD_URL ?? 'http://localhost:8080';
const HARNESS_API_KEY = process.env.HARNESS_API_KEY ?? '';

const app = new Hono();

// POST /runs
// Accepts { prompt: string } and starts a harnessd run.
// Returns { run_id: string, status: string }.
app.post('/runs', async (c) => {
  const body = await c.req.json<{ prompt: string }>();
  if (!body.prompt) {
    return c.json({ error: 'prompt is required' }, 400);
  }

  // Build the harnessd RunRequest. Only prompt is required;
  // add model, provider_name, conversation_id, etc. as needed.
  // allow_fallback: true is required when running harnessd with
  // HARNESS_PROVIDER=fake — without it the runner uses the default
  // model (gpt-4.1-mini → OpenAI) and fails if no API key is set.
  const runRequest = {
    prompt: body.prompt,
    allow_fallback: true,
  };

  // Forward the Authorization header when auth is enabled on harnessd.
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  if (HARNESS_API_KEY) {
    headers['Authorization'] = `Bearer ${HARNESS_API_KEY}`;
  }

  const resp = await fetch(`${HARNESSD_URL}/v1/runs`, {
    method: 'POST',
    headers,
    body: JSON.stringify(runRequest),
  });

  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: 'upstream error' }));
    return c.json(err, resp.status as 400 | 500);
  }

  // harnessd replies with HTTP 202 and { run_id, status }.
  const data = (await resp.json()) as { run_id: string; status: string };
  return c.json(data, 202);
});
```

<Callout variant="info">
The full `RunRequest` body supports many optional fields: `model`, `provider_name`, `conversation_id`, `system_prompt`, `allowed_tools`, `max_steps`, `max_cost_usd`, and more. The only required field is `prompt`. Start simple and add fields as your use case grows.
</Callout>

---

## Hono: proxy the SSE stream

Add the streaming route to `src/index.ts`:

```typescript
// GET /runs/:id/events
// Fetches the harnessd SSE stream for a run and re-streams it to the caller.
// Closes the upstream connection on the three terminal events:
//   run.completed, run.failed, run.cancelled
app.get('/runs/:id/events', async (c) => {
  const runId = c.req.param('id');

  // Build the upstream URL. When harnessd auth is enabled but the caller cannot
  // set headers (e.g. a browser EventSource), harnessd also accepts the token
  // as a ?token= query parameter.
  const upstreamURL = HARNESS_API_KEY
    ? `${HARNESSD_URL}/v1/runs/${runId}/events?token=${HARNESS_API_KEY}`
    : `${HARNESSD_URL}/v1/runs/${runId}/events`;

  const headers: Record<string, string> = {};
  if (HARNESS_API_KEY) {
    headers['Authorization'] = `Bearer ${HARNESS_API_KEY}`;
  }

  // Pass Last-Event-ID through so harnessd can replay missed events.
  const lastEventId = c.req.header('Last-Event-ID');
  if (lastEventId) {
    headers['Last-Event-ID'] = lastEventId;
  }

  const upstream = await fetch(upstreamURL, { headers });

  if (!upstream.ok || !upstream.body) {
    return c.json({ error: 'could not connect to harnessd event stream' }, 502);
  }

  // The three event type strings that signal end-of-run.
  const TERMINAL_EVENTS = new Set([
    'run.completed',
    'run.failed',
    'run.cancelled',
  ]);

  // Re-stream the upstream body as text/event-stream.
  // We read the raw bytes from harnessd and forward them verbatim —
  // SSE framing (id:, retry:, event:, data:) is preserved.
  const stream = new ReadableStream({
    async start(controller) {
      const reader = upstream.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          const chunk = decoder.decode(value, { stream: true });
          controller.enqueue(value);
          buffer += chunk;

          // Scan for complete SSE frames (terminated by double newline).
          // When we see a terminal event type, close after forwarding the frame.
          const frames = buffer.split('\n\n');
          buffer = frames.pop() ?? '';

          for (const frame of frames) {
            // Extract the event type from the frame, e.g. "event: run.completed"
            const match = frame.match(/^event:\s*(.+)$/m);
            if (match && TERMINAL_EVENTS.has(match[1].trim())) {
              controller.close();
              reader.cancel();
              return;
            }
          }
        }
      } catch {
        // Upstream closed or errored; close our side too.
      } finally {
        controller.close();
      }
    },
  });

  return new Response(stream, {
    headers: {
      'Content-Type': 'text/event-stream; charset=utf-8',
      'Cache-Control': 'no-cache',
      'Connection': 'keep-alive',
      // Forward the Access-Control header when browser clients need CORS.
      'Access-Control-Allow-Origin': '*',
    },
  });
});

// Start the server
const PORT = Number(process.env.PORT ?? 3000);
serve({ fetch: app.fetch, port: PORT }, () => {
  console.log(`Hono gateway listening on http://localhost:${PORT}`);
});
```

<Callout variant="info">
`harnessd` sends an SSE comment line (`: ping`) every 15 seconds as a keepalive. Your proxy forwards these verbatim — they are harmless and keep the browser TCP connection from timing out.
</Callout>

---

## Smoke-test the Hono server

<Steps>
<Step>

### Start the Hono gateway

In a second terminal (harnessd is still running in the first):

```bash
npx tsx src/index.ts
```

You should see:

```
Hono gateway listening on http://localhost:3000
```

</Step>
<Step>

### Start a run via the Hono route

```bash
curl -s -X POST http://localhost:3000/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "say hello"}'
```

The Hono server appends `allow_fallback: true` before forwarding to `harnessd`, so this request will complete successfully in fake mode.

Expected response (HTTP 202):

```json
{"run_id": "run_abc123", "status": "queued"}
```

</Step>
<Step>

### Stream events via the Hono proxy

```bash
curl -s -N "http://localhost:3000/runs/run_abc123/events"
```

You should see the same SSE frames that harnessd emits — forwarded verbatim by your Hono server. The stream closes when the `run.completed` (or `run.failed` or `run.cancelled`) event arrives.

</Step>
</Steps>

---

## Client: consuming the stream in a browser

With the Hono gateway running, any page can consume the proxied stream using the standard `EventSource` API.

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <title>harnessd stream demo</title>
</head>
<body>
<button id="run">Start run</button>
<pre id="output"></pre>

<script type="module">
  const output = document.getElementById('output');

  document.getElementById('run').addEventListener('click', async () => {
    // 1. Start a run via the Hono POST /runs route.
    const resp = await fetch('http://localhost:3000/runs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt: 'say hello' }),
    });
    const { run_id } = await resp.json();

    // 2. Open an EventSource against the Hono proxy route.
    //    EventSource handles reconnection automatically; it sends
    //    Last-Event-ID on reconnect so the server can replay missed events.
    const es = new EventSource(`http://localhost:3000/runs/${run_id}/events`);

    es.addEventListener('run.started', (e) => {
      output.textContent += `[started]\n`;
    });

    es.addEventListener('assistant.message', (e) => {
      const event = JSON.parse(e.data);
      output.textContent += `[assistant] ${event.payload?.content ?? ''}\n`;
    });

    es.addEventListener('run.completed', (e) => {
      const event = JSON.parse(e.data);
      output.textContent += `[done] ${event.payload?.output ?? ''}\n`;
      es.close();
    });

    es.addEventListener('run.failed', (e) => {
      const event = JSON.parse(e.data);
      output.textContent += `[error] ${event.payload?.error ?? 'unknown error'}\n`;
      es.close();
    });

    es.addEventListener('run.cancelled', () => {
      output.textContent += `[cancelled]\n`;
      es.close();
    });

    es.onerror = () => {
      // EventSource reconnects automatically on transient errors.
      // Close explicitly only after a terminal event.
    };
  });
</script>
</body>
</html>
```

A few things worth noting about the browser side:

- **`EventSource` reconnects automatically.** When the connection drops before a terminal event, the browser waits for `retry` milliseconds (harnessd sets this to 3000 ms) and reopens the stream, sending the `Last-Event-ID` header with the last event it received. The Hono proxy forwards that header to harnessd, which replays any events with a sequence number higher than the last-seen ID.
- **Listen by event name.** Use `es.addEventListener('run.completed', ...)` rather than `es.onmessage`. harnessd always sets the `event:` field in each SSE frame, so named listeners are more precise than the catch-all `onmessage`.
- **Always close after a terminal event.** Once you receive `run.completed`, `run.failed`, or `run.cancelled`, call `es.close()` to release the connection. The Hono proxy closes the upstream connection at the same point, so resources are freed on both sides.

---

## Adding auth for production

When you remove `HARNESS_AUTH_DISABLED=true` from `harnessd`, it requires a Bearer token. Set `HARNESS_API_KEY` in your Hono process environment to the key you want to use:

```bash
HARNESSD_URL=http://localhost:8080 \
HARNESS_API_KEY=your-token-here \
npx tsx src/index.ts
```

The Hono server code already handles this: when `HARNESS_API_KEY` is set, it sends `Authorization: Bearer <token>` on every upstream request, and uses `?token=` for the SSE URL (because a browser `EventSource` cannot set custom headers).

<Callout variant="warning">
Never expose `HARNESS_API_KEY` to browser clients. The Hono server is the only process that should know the harnessd token. Browsers talk to Hono; Hono talks to harnessd.
</Callout>

You will likely also want your own per-user authentication on the Hono routes (e.g., Hono middleware that validates a session cookie or JWT) before forwarding requests to harnessd.

---

## Next steps

<Card>
<CardHeader>
<CardTitle>Where to go from here</CardTitle>
</CardHeader>
<CardContent>

- **Full event catalog** — see [Events](/docs/concepts/events) for every event type harnessd emits and what each payload contains.
- **All RunRequest fields** — see the [HTTP API Guide](/docs/server/http-api-guide) for `model`, `conversation_id`, `max_steps`, `allowed_tools`, and the rest of the request body.
- **Authentication** — see [Authentication](/docs/server/authentication) for how API keys, scopes, and tenant isolation work in production.
- **Real-world example** — the `socialagent` app in `apps/socialagent/` is a complete production-style integration: a Telegram bot that calls harnessd over HTTP/SSE using the same `StartRun` + `StreamEvents` pattern shown here.

</CardContent>
</Card>
